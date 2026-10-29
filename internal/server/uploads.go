package server

import (
	"collection/internal/importer"
	"collection/internal/model"
	"collection/internal/queue"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

const uploadChunkSize int64 = 8 * 1024 * 1024

type uploadFile struct {
	Path     string `json:"path"`
	Size     int64  `json:"size"`
	Modified int64  `json:"modified"`
	Offset   int64  `json:"offset"`
}
type uploadSession struct {
	ID        string       `json:"id"`
	Name      string       `json:"name"`
	Status    string       `json:"status"`
	Files     []uploadFile `json:"files"`
	JobID     *int64       `json:"jobId,string,omitempty"`
	ChunkSize int64        `json:"chunkSize"`
}

func validUploadPath(path string) bool {
	if path == "" || len(path) > 2000 || strings.ContainsAny(path, "\\:\x00") || strings.HasPrefix(path, "/") || filepath.IsAbs(path) {
		return false
	}
	for _, part := range strings.Split(path, "/") {
		if part == "" || part == "." || part == ".." || strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") || strings.ContainsAny(part, `<>"|?*`) {
			return false
		}
		base := strings.ToUpper(strings.SplitN(part, ".", 2)[0])
		if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" || (len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '1' && base[3] <= '9') {
			return false
		}
	}
	return true
}
func (s *Server) uploadPath(id, path string) string {
	return filepath.Join(s.Config.DataDir, "staging", "upload-"+id, "files", filepath.FromSlash(path))
}
func (s *Server) readUpload(id string) (uploadSession, error) {
	var v uploadSession
	var raw string
	v.ChunkSize = uploadChunkSize
	err := s.Store.DB.QueryRow("SELECT id,name,status,manifest,job_id FROM uploads WHERE id=?", id).Scan(&v.ID, &v.Name, &v.Status, &raw, &v.JobID)
	if err != nil {
		return v, err
	}
	if err = json.Unmarshal([]byte(raw), &v.Files); err != nil {
		return v, err
	}
	for i := range v.Files {
		v.Files[i].Offset = 0
		if info, e := os.Stat(s.uploadPath(v.ID, v.Files[i].Path)); e == nil {
			v.Files[i].Offset = info.Size()
		}
	}
	return v, nil
}
func (s *Server) uploadCreate(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	s.uploadMu.Lock()
	defer s.uploadMu.Unlock()
	var input struct {
		ID    string       `json:"id"`
		Name  string       `json:"name"`
		Files []uploadFile `json:"files"`
	}
	// 文件清单单独限额，不受普通 JSON 接口的 128 KiB 限制。
	r.Body = http.MaxBytesReader(w, r.Body, 32*1024*1024)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&input); err != nil || len(input.Files) == 0 || len(input.Files) > 100000 {
		fail(w, 400, "invalid_manifest", "文件清单无效")
		return
	}
	if dec.Decode(&struct{}{}) != io.EOF {
		fail(w, 400, "invalid_manifest", "文件清单无效")
		return
	}
	seen := map[string]bool{}
	for i, f := range input.Files {
		key := strings.ToLower(f.Path)
		if !validUploadPath(f.Path) || f.Size < 0 || f.Modified < 0 || seen[key] {
			fail(w, 400, "invalid_path", "上传路径或文件信息无效")
			return
		}
		seen[key] = true
		input.Files[i].Offset = 0
	}
	for key := range seen {
		parts := strings.Split(key, "/")
		for i := 1; i < len(parts); i++ {
			if seen[strings.Join(parts[:i], "/")] {
				fail(w, 400, "invalid_path", "文件路径冲突")
				return
			}
		}
	}
	var old uploadSession
	var err error
	if input.ID != "" {
		old, err = s.readUpload(input.ID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			s.internal(w, err)
			return
		}
		if err == nil && old.Status != "uploading" {
			fail(w, 409, "upload_closed", "该上传已结束，请重新选择文件夹")
			return
		}
	}
	if old.ID == "" {
		old = uploadSession{ID: randomToken(), Name: input.Name, Status: "uploading"}
		if err = os.MkdirAll(filepath.Join(s.Config.DataDir, "staging", "upload-"+old.ID, "files"), 0700); err != nil {
			s.internal(w, err)
			return
		}
	}
	next := map[string]uploadFile{}
	for _, f := range input.Files {
		next[f.Path] = f
	}
	for _, f := range old.Files {
		replacement, exists := next[f.Path]
		if !exists || replacement.Size != f.Size || replacement.Modified != f.Modified {
			if err = os.Remove(s.uploadPath(old.ID, f.Path)); err != nil && !os.IsNotExist(err) {
				s.internal(w, err)
				return
			}
		}
	}
	// 仅持久化原始清单；续传偏移由已保存的分块长度确定。
	raw, err := json.Marshal(input.Files)
	if err != nil {
		s.internal(w, err)
		return
	}
	_, err = s.Store.DB.Exec("INSERT INTO uploads(id,name,status,manifest,updated_at) VALUES(?,?,'uploading',?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,manifest=excluded.manifest,updated_at=excluded.updated_at", old.ID, input.Name, string(raw), model.Now())
	if err != nil {
		s.internal(w, err)
		return
	}
	v, err := s.readUpload(old.ID)
	if err != nil {
		s.internal(w, err)
		return
	}
	respond(w, 200, v)
}
func (s *Server) uploadGet(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	s.uploadMu.Lock()
	defer s.uploadMu.Unlock()
	v, err := s.readUpload(r.PathValue("upload"))
	if err != nil {
		fail(w, 404, "not_found", "上传不存在")
		return
	}
	respond(w, 200, v)
}
func (s *Server) uploadChunk(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/octet-stream") {
		fail(w, 415, "invalid_content_type", "分块必须为二进制数据")
		return
	}
	s.uploadMu.Lock()
	defer s.uploadMu.Unlock()
	v, err := s.readUpload(r.PathValue("upload"))
	if err != nil {
		fail(w, 404, "not_found", "上传不存在")
		return
	}
	index, e := strconv.Atoi(r.PathValue("file"))
	offset, e2 := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
	if v.Status != "uploading" || e != nil || e2 != nil || index < 0 || index >= len(v.Files) || offset < 0 {
		fail(w, 409, "invalid_upload", "上传状态无效")
		return
	}
	f := v.Files[index]
	if offset != f.Offset {
		respond(w, 409, map[string]any{"offset": f.Offset, "error": map[string]string{"code": "offset_mismatch", "message": "上传偏移已更新，请继续上传"}})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, uploadChunkSize)
	chunk, err := io.ReadAll(r.Body)
	if err != nil || len(chunk) == 0 && f.Size != 0 || int64(len(chunk))+offset > f.Size {
		fail(w, 400, "invalid_chunk", "分块大小无效")
		return
	}
	path := s.uploadPath(v.ID, f.Path)
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		s.internal(w, err)
		return
	}
	out, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		s.internal(w, err)
		return
	}
	n, err := out.WriteAt(chunk, offset)
	if err == nil {
		err = out.Sync()
	}
	closeErr := out.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		s.internal(w, err)
		return
	}
	respond(w, 200, map[string]int64{"offset": offset + int64(n)})
}
func (s *Server) uploadComplete(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var input struct {
		Rating int `json:"rating"`
	}
	if !decode(w, r, &input) {
		return
	}
	s.completeUploadImport(w, r, r.PathValue("upload"), input.Rating)
}
func (s *Server) completeUploadImport(w http.ResponseWriter, r *http.Request, id string, rating int) {
	s.uploadMu.Lock()
	defer s.uploadMu.Unlock()
	if rating < 0 || rating > 2 {
		fail(w, 400, "invalid_rating", "分级无效")
		return
	}
	v, err := s.readUpload(id)
	if err != nil {
		fail(w, 404, "not_found", "上传不存在")
		return
	}
	if v.JobID != nil {
		j, err := s.Store.Job(*v.JobID)
		if err != nil {
			s.internal(w, err)
			return
		}
		respond(w, 200, j)
		return
	}
	if v.Status != "uploading" {
		fail(w, 409, "upload_closed", "该上传已取消")
		return
	}
	for _, f := range v.Files {
		if f.Offset != f.Size {
			fail(w, 409, "upload_incomplete", "还有文件尚未上传完成")
			return
		}
		if f.Size == 0 {
			path := s.uploadPath(v.ID, f.Path)
			if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				s.internal(w, err)
				return
			}
			out, e := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0600)
			if e != nil {
				s.internal(w, e)
				return
			}
			out.Close()
		}
	}
	raw, _ := json.Marshal(queue.BatchInput{Source: "upload", UploadID: v.ID, Rating: rating})
	j, err := s.Queue.Enqueue("batch", "upload:"+v.ID, string(raw))
	if err != nil {
		s.internal(w, err)
		return
	}
	_, err = s.Store.DB.Exec("UPDATE uploads SET status='queued',job_id=?,updated_at=? WHERE id=?", j.ID, model.Now(), v.ID)
	if err != nil {
		s.internal(w, err)
		return
	}
	respond(w, 202, j)
}
func (s *Server) uploadCancel(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var input struct{}
	if !decode(w, r, &input) {
		return
	}
	s.uploadMu.Lock()
	defer s.uploadMu.Unlock()
	v, err := s.readUpload(r.PathValue("upload"))
	if err != nil {
		fail(w, 404, "not_found", "上传不存在")
		return
	}
	if v.Status != "uploading" {
		fail(w, 409, "upload_closed", "已经开始导入，不能取消上传")
		return
	}
	dir := filepath.Join(s.Config.DataDir, "staging", "upload-"+v.ID)
	if !importer.Within(filepath.Join(s.Config.DataDir, "staging"), dir) {
		fail(w, 400, "invalid_path", "上传目录无效")
		return
	}
	if err = os.RemoveAll(dir); err != nil {
		s.internal(w, err)
		return
	}
	_, err = s.Store.DB.Exec("UPDATE uploads SET status='cancelled',updated_at=? WHERE id=?", model.Now(), v.ID)
	if err != nil {
		s.internal(w, err)
		return
	}
	respond(w, 200, map[string]bool{"cancelled": true})
}
func (s *Server) directories(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	path := r.URL.Query().Get("path")
	type entry struct {
		Name string `json:"name"`
		Path string `json:"path"`
	}
	entries := []entry{}
	if path == "" && runtime.GOOS == "windows" {
		for letter := 'A'; letter <= 'Z'; letter++ {
			root := fmt.Sprintf("%c:\\", letter)
			if info, e := os.Stat(root); e == nil && info.IsDir() {
				entries = append(entries, entry{root, root})
			}
		}
		respond(w, 200, map[string]any{"path": "", "parent": "", "directories": entries})
		return
	}
	if path == "" {
		path = "/"
	}
	path, err := importer.Directory(path, s.Config.DataDir)
	if err != nil {
		fail(w, 400, "invalid_directory", "目录不可访问")
		return
	}
	children, err := os.ReadDir(path)
	if err != nil {
		fail(w, 400, "invalid_directory", "目录不可访问")
		return
	}
	for _, child := range children {
		full := filepath.Join(path, child.Name())
		if child.IsDir() && child.Type()&os.ModeSymlink == 0 && !importer.Within(s.Config.DataDir, full) {
			entries = append(entries, entry{child.Name(), full})
		}
	}
	sort.Slice(entries, func(i, j int) bool { return importer.NaturalLess(entries[i].Name, entries[j].Name) })
	parent := filepath.Dir(path)
	if runtime.GOOS == "windows" && parent == path {
		parent = ""
	}
	respond(w, 200, map[string]any{"path": path, "parent": parent, "directories": entries})
}
