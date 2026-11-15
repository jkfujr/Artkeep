package server

import (
	"bytes"
	"collection/internal/importer"
	"collection/internal/model"
	"database/sql"
	"encoding/json"
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
	if !s.requireWriter(w, r) {
		return
	}
	s.idempotent(w, r, 32*1024*1024, func(tx *sql.Tx, raw []byte) (int, string, any, error) {
		var in struct {
			Name  string       `json:"name"`
			Files []uploadFile `json:"files"`
		}
		if e := parseBody(raw, &in); e != nil {
			return 0, "", nil, e
		}
		if len(in.Files) == 0 || len(in.Files) > 100000 || len(in.Name) > 1000 {
			return 0, "", nil, problem(422, "invalid_manifest", "文件清单无效")
		}
		seen := map[string]bool{}
		for i, f := range in.Files {
			key := strings.ToLower(f.Path)
			if !validUploadPath(f.Path) || f.Size < 0 || f.Modified < 0 || f.Offset != 0 || seen[key] {
				return 0, "", nil, problem(422, "invalid_path", "上传路径或文件信息无效")
			}
			seen[key] = true
			in.Files[i].Offset = 0
		}
		for key := range seen {
			parts := strings.Split(key, "/")
			for i := 1; i < len(parts); i++ {
				if seen[strings.Join(parts[:i], "/")] {
					return 0, "", nil, problem(422, "invalid_path", "文件路径冲突")
				}
			}
		}
		v := uploadSession{ID: randomToken(), Name: in.Name, Status: "uploading", Files: in.Files, ChunkSize: uploadChunkSize}
		manifest, _ := json.Marshal(in.Files)
		if _, e := tx.Exec("INSERT INTO uploads(id,name,status,manifest,updated_at) VALUES(?,?,'uploading',?,?)", v.ID, v.Name, string(manifest), model.Now()); e != nil {
			return 0, "", nil, e
		}
		_, e := tx.Exec("INSERT INTO upload_owners(upload_id,owner) VALUES(?,?)", v.ID, identity(r))
		return 201, "/api/v1/uploads/" + v.ID, v, e
	})
}
func (s *Server) authorizedUpload(w http.ResponseWriter, r *http.Request) (uploadSession, bool) {
	if !s.requireWriter(w, r) {
		return uploadSession{}, false
	}
	if !s.owns(r, "upload", r.PathValue("upload")) {
		fail(w, 404, "not_found", "上传不存在")
		return uploadSession{}, false
	}
	v, e := s.readUpload(r.PathValue("upload"))
	if e != nil {
		fail(w, 404, "not_found", "上传不存在")
		return v, false
	}
	return v, true
}
func (s *Server) uploadGet(w http.ResponseWriter, r *http.Request) {
	s.uploadMu.Lock()
	defer s.uploadMu.Unlock()
	v, ok := s.authorizedUpload(w, r)
	if ok {
		respond(w, 200, v)
	}
}
func (s *Server) uploadFileGet(w http.ResponseWriter, r *http.Request) {
	s.uploadMu.Lock()
	defer s.uploadMu.Unlock()
	v, ok := s.authorizedUpload(w, r)
	if !ok {
		return
	}
	i, e := strconv.Atoi(r.PathValue("file"))
	if e != nil || i < 0 || i >= len(v.Files) {
		fail(w, 404, "not_found", "文件不存在")
		return
	}
	w.Header().Set("Upload-Offset", strconv.FormatInt(v.Files[i].Offset, 10))
	respond(w, 200, v.Files[i])
}
func (s *Server) uploadChunk(w http.ResponseWriter, r *http.Request) {
	s.uploadMu.Lock()
	defer s.uploadMu.Unlock()
	v, ok := s.authorizedUpload(w, r)
	if !ok {
		return
	}
	i, e := strconv.Atoi(r.PathValue("file"))
	offset, e2 := strconv.ParseInt(r.PathValue("offset"), 10, 64)
	if e != nil || e2 != nil || i < 0 || i >= len(v.Files) || offset < 0 {
		fail(w, 422, "invalid_upload", "上传位置无效")
		return
	}
	f := v.Files[i]
	chunk, e := io.ReadAll(http.MaxBytesReader(w, r.Body, uploadChunkSize))
	if e != nil {
		fail(w, 413, "body_too_large", "分块超过 8 MiB")
		return
	}
	if len(chunk) == 0 || int64(len(chunk)) > f.Size-offset {
		fail(w, 422, "invalid_chunk", "分块大小无效")
		return
	}
	w.Header().Set("Upload-Offset", strconv.FormatInt(f.Offset, 10))
	path := s.uploadPath(v.ID, f.Path)
	if offset < f.Offset && offset+int64(len(chunk)) <= f.Offset {
		old := make([]byte, len(chunk))
		in, e := os.Open(path)
		if e == nil {
			_, e = in.ReadAt(old, offset)
			in.Close()
		}
		if e == nil && bytes.Equal(old, chunk) {
			w.WriteHeader(204)
			return
		}
	}
	if offset != f.Offset {
		fail(w, 409, "offset_mismatch", "分块冲突，请查询当前上传偏移")
		return
	}
	if v.Status != "uploading" {
		fail(w, 409, "upload_closed", "该上传已结束")
		return
	}
	if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		s.internal(w, e)
		return
	}
	out, e := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0600)
	if e != nil {
		s.internal(w, e)
		return
	}
	n, e := out.WriteAt(chunk, offset)
	if e == nil {
		e = out.Sync()
	}
	ce := out.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		s.internal(w, e)
		return
	}
	w.Header().Set("Upload-Offset", strconv.FormatInt(offset+int64(n), 10))
	w.WriteHeader(204)
}
func (s *Server) uploadCancel(w http.ResponseWriter, r *http.Request) {
	s.uploadMu.Lock()
	defer s.uploadMu.Unlock()
	v, ok := s.authorizedUpload(w, r)
	if !ok {
		return
	}
	if v.Status == "cancelled" {
		w.WriteHeader(204)
		return
	}
	if v.Status != "uploading" {
		fail(w, 409, "upload_closed", "已经开始导入，不能取消上传")
		return
	}
	dir := filepath.Join(s.Config.DataDir, "staging", "upload-"+v.ID)
	if !importer.Within(filepath.Join(s.Config.DataDir, "staging"), dir) {
		fail(w, 422, "invalid_path", "上传目录无效")
		return
	}
	if _, e := s.Store.DB.Exec("UPDATE uploads SET status='cancelled',updated_at=? WHERE id=?", model.Now(), v.ID); e != nil {
		s.internal(w, e)
		return
	}
	if e := os.RemoveAll(dir); e != nil {
		s.internal(w, e)
		return
	}
	w.WriteHeader(204)
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
