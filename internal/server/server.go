package server

import (
	"collection/internal/config"
	"collection/internal/media"
	"collection/internal/queue"
	"collection/internal/store"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
)

type Server struct {
	Store    *store.Store
	Media    *media.Service
	Queue    *queue.Queue
	Config   config.Config
	Web      fs.FS
	Limiter  limiter
	uploadMu sync.Mutex
}

func New(st *store.Store, m *media.Service, q *queue.Queue, c config.Config, web fs.FS) (*Server, error) {
	s := &Server{Store: st, Media: m, Queue: q, Config: c, Web: web, Limiter: limiter{entries: map[string]attempt{}}}
	if err := s.bootstrap(); err != nil {
		return nil, err
	}
	_, err := s.Store.DB.Exec("DELETE FROM sessions WHERE expires < unixepoch()")
	return s, err
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/site", s.site)
	mux.HandleFunc("GET /api/works", s.works)
	mux.HandleFunc("GET /api/works/{id}", s.work)
	mux.HandleFunc("GET /api/tags", s.tags)
	mux.HandleFunc("GET /api/years", s.years)
	mux.HandleFunc("GET /api/authors", s.authors)
	mux.HandleFunc("GET /api/assets/{id}/{variant}", s.asset)
	mux.HandleFunc("POST /api/admin/session", s.login)
	mux.HandleFunc("DELETE /api/admin/session", s.login)
	mux.HandleFunc("POST /api/adult/session", s.login)
	mux.HandleFunc("DELETE /api/adult/session", s.login)
	mux.HandleFunc("GET /api/admin/settings", s.settings)
	mux.HandleFunc("PATCH /api/admin/settings", s.settings)
	mux.HandleFunc("POST /api/admin/imports", s.imports)
	mux.HandleFunc("GET /api/admin/directories", s.directories)
	mux.HandleFunc("POST /api/admin/uploads", s.uploadCreate)
	mux.HandleFunc("GET /api/admin/uploads/{upload}", s.uploadGet)
	mux.HandleFunc("PUT /api/admin/uploads/{upload}/files/{file}", s.uploadChunk)
	mux.HandleFunc("POST /api/admin/uploads/{upload}/complete", s.uploadComplete)
	mux.HandleFunc("DELETE /api/admin/uploads/{upload}", s.uploadCancel)
	mux.HandleFunc("GET /api/admin/jobs", s.jobs)
	mux.HandleFunc("POST /api/admin/jobs/{id}/retry", s.retry)
	mux.HandleFunc("PATCH /api/admin/works/{id}", s.edit)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) { fail(w, 404, "not_found", "接口不存在") })
	mux.HandleFunc("/", s.frontend)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; media-src 'self' blob:; font-src 'self' data:; connect-src 'self'; object-src 'none'; frame-src blob:; frame-ancestors 'none'; base-uri 'self'")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		if r.Method != "GET" && r.Method != "HEAD" && r.Method != "OPTIONS" {
			if origin := r.Header.Get("Origin"); origin != "" {
				u, err := url.Parse(origin)
				if err != nil || !strings.EqualFold(u.Host, r.Host) || (u.Scheme != "http" && u.Scheme != "https") {
					fail(w, 403, "origin_denied", "请求来源无效")
					return
				}
			}
			if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
				fail(w, 403, "origin_denied", "请求来源无效")
				return
			}
			binaryUpload := r.Method == "PUT" && strings.HasPrefix(r.URL.Path, "/api/admin/uploads/") && strings.HasPrefix(r.Header.Get("Content-Type"), "application/octet-stream")
			if !binaryUpload && !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
				fail(w, 415, "invalid_content_type", "请求格式错误")
				return
			}
		}
		defer func() {
			if recovered := recover(); recovered != nil {
				log.Printf("请求处理失败: %v", recovered)
				fail(w, 500, "internal_error", "操作失败")
			}
		}()
		mux.ServeHTTP(w, r)
	})
}

func respond(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("写入响应失败: %v", err)
	}
}
func fail(w http.ResponseWriter, status int, code, message string) {
	respond(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
func (s *Server) internal(w http.ResponseWriter, err error) {
	log.Printf("操作失败: %v", err)
	fail(w, 500, "internal_error", "操作失败，请重试")
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 128*1024)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		fail(w, 400, "invalid_request", "请求内容无效")
		return false
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		fail(w, 400, "invalid_request", "请求内容无效")
		return false
	}
	return true
}

func (s *Server) frontend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		fail(w, http.StatusMethodNotAllowed, "method_not_allowed", "请求方式不支持")
		return
	}
	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if name == "" || name == "." {
		name = "index.html"
	}
	if name != "index.html" {
		if info, err := fs.Stat(s.Web, name); err == nil && !info.IsDir() {
			http.FileServerFS(s.Web).ServeHTTP(w, r)
			return
		}
	}
	if name != "index.html" && (strings.HasPrefix(name, "assets/") || strings.Contains(path.Base(name), ".")) {
		http.NotFound(w, r)
		return
	}
	b, err := fs.ReadFile(s.Web, "index.html")
	if err != nil {
		http.Error(w, "页面尚未构建", 503)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(b)
}
