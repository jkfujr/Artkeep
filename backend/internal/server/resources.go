package server

import (
	"collection/internal/model"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

func (s *Server) capabilities(w http.ResponseWriter, r *http.Request) {
	_, ff := exec.LookPath(s.Config.FFmpeg)
	_, fp := exec.LookPath(s.Config.FFprobe)
	respond(w, 200, map[string]any{"apiVersion": "v1", "collectorProtocol": 1, "chunkSize": uploadChunkSize, "formats": []string{"auto", "fanbox", "ppd-json", "gallery-dl", "ugoira"}, "ffmpegAvailable": ff == nil, "ffprobeAvailable": fp == nil})
}

type accessToken struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Origin    string `json:"origin"`
	CreatedAt string `json:"createdAt"`
	Token     string `json:"token,omitempty"`
}

func (s *Server) accessTokens(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if r.Method == "POST" {
		var in struct {
			Name   string `json:"name"`
			Origin string `json:"origin"`
		}
		if !decode(w, r, &in) {
			return
		}
		u, e := url.Parse(in.Origin)
		if strings.TrimSpace(in.Name) == "" || utf8.RuneCountInString(in.Name) > 100 || e != nil || u.Scheme != "chrome-extension" || !regexp.MustCompile(`^[a-p]{32}$`).MatchString(u.Host) || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
			fail(w, 422, "invalid_token_settings", "请填写令牌名称和有效扩展来源")
			return
		}
		v := accessToken{ID: randomToken(), Name: strings.TrimSpace(in.Name), Origin: in.Origin, CreatedAt: model.Now(), Token: randomToken()}
		if _, e = s.Store.DB.Exec("INSERT INTO access_tokens(id,token_hash,name,origin,created_at) VALUES(?,?,?,?,?)", v.ID, tokenHash(v.Token), v.Name, v.Origin, v.CreatedAt); e != nil {
			s.internal(w, e)
			return
		}
		w.Header().Set("Location", "/api/v1/access-tokens/"+v.ID)
		respond(w, 201, v)
		return
	}
	rows, e := s.Store.DB.Query("SELECT id,name,origin,created_at FROM access_tokens ORDER BY created_at DESC,id")
	if e != nil {
		s.internal(w, e)
		return
	}
	defer rows.Close()
	items := []accessToken{}
	for rows.Next() {
		var v accessToken
		if e = rows.Scan(&v.ID, &v.Name, &v.Origin, &v.CreatedAt); e != nil {
			s.internal(w, e)
			return
		}
		items = append(items, v)
	}
	if e = rows.Err(); e != nil {
		s.internal(w, e)
		return
	}
	respond(w, 200, map[string]any{"items": items})
}
func (s *Server) revokeToken(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if _, e := s.Store.DB.Exec("DELETE FROM access_tokens WHERE id=?", r.PathValue("id")); e != nil {
		s.internal(w, e)
		return
	}
	w.WriteHeader(204)
}
func scope(r *http.Request) (string, bool) {
	v := r.URL.Query().Get("scope")
	return v, v == "admin" || v == "adult"
}
func (s *Server) currentSession(w http.ResponseWriter, r *http.Request) {
	v, ok := scope(r)
	if !ok {
		fail(w, 422, "invalid_scope", "会话范围无效")
		return
	}
	if r.Method == "DELETE" {
		if r.Header.Get("Authorization") != "" {
			fail(w, 403, "forbidden", "请使用网页会话")
			return
		}
		if e := s.clearSession(w, r, v); e != nil {
			s.internal(w, e)
			return
		}
		w.WriteHeader(204)
		return
	}
	respond(w, 200, map[string]any{"scope": v, "authenticated": s.session(r, v)})
}
func (s *Server) adultCredential(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var in struct {
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	if len(in.Password) < 6 || len(in.Password) > 72 {
		fail(w, 422, "invalid_password", "R18 密码须为 6 至 72 字节")
		return
	}
	hash, e := hashAdult(in.Password)
	if e != nil {
		s.internal(w, e)
		return
	}
	tx, e := s.Store.DB.Begin()
	if e != nil {
		s.internal(w, e)
		return
	}
	defer tx.Rollback()
	for k, v := range map[string]string{"adult_password": hash, "adult_version": randomToken()} {
		if _, e = tx.Exec("INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", k, v); e != nil {
			s.internal(w, e)
			return
		}
	}
	if _, e = tx.Exec("DELETE FROM sessions WHERE scope='adult'"); e == nil {
		e = tx.Commit()
	}
	if e != nil {
		s.internal(w, e)
		return
	}
	w.WriteHeader(204)
}
func (s *Server) source(w http.ResponseWriter, r *http.Request) {
	if !s.requireWriter(w, r) {
		return
	}
	platform, kind, id := r.PathValue("platform"), r.PathValue("sourceKind"), r.PathValue("sourceId")
	if !((platform == "pixiv" && (kind == "artwork" || kind == "novel")) || platform == "x" && kind == "tweet") || !regexp.MustCompile(`^[0-9]+$`).MatchString(id) {
		fail(w, 422, "invalid_source", "来源标识无效")
		return
	}
	wid, _, ready, e := s.Store.Find(platform, kind, id)
	if e != nil {
		s.internal(w, e)
		return
	}
	result := map[string]any{"collected": wid > 0 && ready}
	if wid > 0 && ready {
		result["workId"] = strconv.FormatInt(wid, 10)
	}
	respond(w, 200, result)
}
func pagination(r *http.Request) (int, int, error) {
	page, size := 1, 48
	for key, target := range map[string]*int{"page": &page, "pageSize": &size} {
		if vs, ok := r.URL.Query()[key]; ok {
			v, e := strconv.Atoi(vs[0])
			if len(vs) != 1 || e != nil || v < 1 || v > 2147483647 {
				return 0, 0, problem(422, "invalid_pagination", "分页参数无效")
			}
			*target = v
		}
	}
	if size > 100 {
		return 0, 0, problem(422, "invalid_pagination", "每页最多 100 条")
	}
	return page, size, nil
}
func collectionPage(items any, total, page, size int) map[string]any {
	return map[string]any{"items": items, "total": total, "page": page, "pageSize": size, "hasMore": page*size < total}
}
func (s *Server) assetMetadata(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	a, rating, e := s.Store.Asset(id)
	if errors.Is(e, sql.ErrNoRows) {
		fail(w, 404, "not_found", "文件不存在")
		return
	}
	if e != nil {
		s.internal(w, e)
		return
	}
	if rating > 0 && !s.adultAllowed(r) {
		fail(w, 403, "adult_locked", "请输入 R18 浏览密码")
		return
	}
	respond(w, 200, assetResponse(a))
}

// 对外响应省略内部任务输入和完整子项；子项通过独立分页接口读取。
func jobResponse(j model.Job) map[string]any {
	var input any
	if json.Unmarshal([]byte(j.Input), &input) != nil {
		input = map[string]any{"source": j.Kind}
	}
	return map[string]any{"id": strconv.FormatInt(j.ID, 10), "kind": publicJobKind(j), "input": input, "status": j.Status, "progress": j.Progress, "total": j.Total, "error": j.Error, "createdAt": j.CreatedAt, "updatedAt": j.UpdatedAt, "succeeded": j.Succeeded, "existing": j.Existing, "failed": j.Failed, "phase": j.Phase, "bytes": j.Bytes, "bytesTotal": j.BytesTotal}
}

func (s *Server) originalFilename(a model.Asset) string {
	w, e := s.Store.Work(a.WorkID, false)
	if e != nil || w.Root != "collection" {
		return filepath.Base(a.Path)
	}
	mapping := filepath.Join(s.Config.DownloadDir(), filepath.FromSlash(filepath.Dir(w.MetadataPath)), "files.json")
	raw, e := os.ReadFile(mapping)
	if e != nil {
		return filepath.Base(a.Path)
	}
	var entries []struct {
		File     string `json:"file"`
		Original string `json:"original"`
	}
	if json.Unmarshal(raw, &entries) == nil && a.Position >= 0 && a.Position < len(entries) {
		return filepath.Base(entries[a.Position].Original)
	}
	return filepath.Base(a.Path)
}

func publicJobKind(j model.Job) string {
	var input struct{ Source string }
	if json.Unmarshal([]byte(j.Input), &input) == nil && input.Source == "migration" {
		return "migration"
	}
	return "import"
}
