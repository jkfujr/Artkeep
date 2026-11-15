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
	mux.HandleFunc("GET /api/v1/site", s.site)
	mux.HandleFunc("GET /api/v1/capabilities", s.capabilities)
	mux.HandleFunc("GET /api/v1/openapi.json", serveOpenAPI)
	mux.HandleFunc("GET /api/v1/works", s.works)
	mux.HandleFunc("GET /api/v1/works/{id}", s.work)
	mux.HandleFunc("PATCH /api/v1/works/{id}", s.edit)
	mux.HandleFunc("GET /api/v1/facets/tags", s.tags)
	mux.HandleFunc("GET /api/v1/facets/years", s.years)
	mux.HandleFunc("GET /api/v1/facets/authors", s.authors)
	mux.HandleFunc("GET /api/v1/sources/{platform}/{sourceKind}/{sourceId}", s.source)
	mux.HandleFunc("GET /api/v1/assets/{id}", s.assetMetadata)
	mux.HandleFunc("GET /api/v1/assets/{id}/{variant}", s.asset)
	mux.HandleFunc("POST /api/v1/sessions", s.login)
	mux.HandleFunc("GET /api/v1/sessions/current", s.currentSession)
	mux.HandleFunc("DELETE /api/v1/sessions/current", s.currentSession)
	mux.HandleFunc("PUT /api/v1/credentials/admin", s.changeAdminPassword)
	mux.HandleFunc("PUT /api/v1/credentials/adult", s.adultCredential)
	mux.HandleFunc("GET /api/v1/settings", s.settings)
	mux.HandleFunc("PATCH /api/v1/settings", s.settings)
	mux.HandleFunc("GET /api/v1/directories", s.directories)
	mux.HandleFunc("GET /api/v1/access-tokens", s.accessTokens)
	mux.HandleFunc("POST /api/v1/access-tokens", s.accessTokens)
	mux.HandleFunc("DELETE /api/v1/access-tokens/{id}", s.revokeToken)
	mux.HandleFunc("POST /api/v1/uploads", s.uploadCreate)
	mux.HandleFunc("GET /api/v1/uploads/{upload}", s.uploadGet)
	mux.HandleFunc("DELETE /api/v1/uploads/{upload}", s.uploadCancel)
	mux.HandleFunc("GET /api/v1/uploads/{upload}/files/{file}", s.uploadFileGet)
	mux.HandleFunc("PUT /api/v1/uploads/{upload}/files/{file}/chunks/{offset}", s.uploadChunk)
	mux.HandleFunc("POST /api/v1/jobs", s.createJob)
	mux.HandleFunc("GET /api/v1/jobs", s.listJobs)
	mux.HandleFunc("GET /api/v1/jobs/{id}", s.getJob)
	mux.HandleFunc("GET /api/v1/jobs/{id}/items", s.jobItems)
	mux.HandleFunc("PATCH /api/v1/jobs/{id}", s.patchJob)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		var allowed []string
		for _, method := range []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE"} {
			probe := r.Clone(r.Context())
			probe.Method = method
			if _, pattern := mux.Handler(probe); pattern != "/api/" && pattern != "/" && pattern != "" {
				allowed = append(allowed, method)
			}
		}
		if len(allowed) > 0 {
			w.Header().Set("Allow", strings.Join(allowed, ", "))
			fail(w, 405, "method_not_allowed", "请求方式不支持")
			return
		}
		fail(w, 404, "not_found", "接口不存在")
	})
	mux.HandleFunc("/", s.frontend)
	return s.middleware(mux)
}

func respond(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("写入响应失败: %v", err)
	}
}
func fail(w http.ResponseWriter, status int, code, message string) {
	if status == 400 && code != "invalid_request" {
		status = 422
	}
	if status == 401 && w.Header().Get("WWW-Authenticate") == "" {
		w.Header().Set("WWW-Authenticate", `Bearer realm="Artkeep"`)
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"type": "urn:artkeep:problem:" + code, "title": http.StatusText(status), "status": status, "code": code, "detail": message})
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
