package server

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

type principalKey struct{}
type principal struct {
	Owner  string
	Origin string
}

func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; media-src 'self' blob:; font-src 'self' data:; connect-src 'self'; object-src 'none'; frame-src blob:; frame-ancestors 'none'; base-uri 'self'")
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		origin := r.Header.Get("Origin")
		u, e := url.Parse(origin)
		same := origin == "" || e == nil && (u.Scheme == "http" || u.Scheme == "https") && strings.EqualFold(u.Host, r.Host)
		extension := e == nil && u.Scheme == "chrome-extension" && regexp.MustCompile(`^[a-p]{32}$`).MatchString(u.Host) && u.Path == "" && u.RawQuery == "" && u.Fragment == ""
		if r.Method == "OPTIONS" {
			if !same && !extension {
				fail(w, 403, "origin_denied", "请求来源无效")
				return
			}
			if extension {
				var count int
				if e := s.Store.DB.QueryRow("SELECT count(*) FROM access_tokens WHERE origin=?", origin).Scan(&count); e != nil || count == 0 {
					fail(w, 403, "origin_denied", "请先为此扩展创建访问令牌")
					return
				}
				allowCORS(w, origin)
			}
			w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Idempotency-Key")
			w.WriteHeader(204)
			return
		}
		p := principal{}
		if auth := r.Header.Get("Authorization"); auth != "" {
			if !strings.HasPrefix(auth, "Bearer ") || len(auth) > 256 {
				fail(w, 401, "invalid_token", "访问令牌无效")
				return
			}
			var id, allowed string
			if e := s.Store.DB.QueryRow("SELECT id,origin FROM access_tokens WHERE token_hash=?", tokenHash(strings.TrimPrefix(auth, "Bearer "))).Scan(&id, &allowed); e != nil {
				w.Header().Set("WWW-Authenticate", `Bearer realm="Artkeep"`)
				fail(w, 401, "invalid_token", "访问令牌已失效")
				return
			}
			if origin != "" && origin != allowed {
				fail(w, 403, "origin_denied", "令牌与扩展来源不匹配")
				return
			}
			p = principal{Owner: "token:" + id, Origin: allowed}
			if extension {
				allowCORS(w, origin)
			}
		} else if s.session(r, "admin") {
			p.Owner = "admin"
		}
		if !same && (!extension || p.Owner == "") {
			fail(w, 403, "origin_denied", "请求来源无效")
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" && p.Origin == "" && r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			fail(w, 403, "origin_denied", "请求来源无效")
			return
		}
		if r.Body != nil && r.ContentLength != 0 && r.Method != "GET" && r.Method != "HEAD" {
			binary := r.Method == "PUT" && strings.Contains(r.URL.Path, "/chunks/")
			want := "application/json"
			if binary {
				want = "application/octet-stream"
			}
			if strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]) != want {
				fail(w, 415, "invalid_content_type", "请求格式错误")
				return
			}
		}
		defer func() {
			if recover() != nil {
				fail(w, 500, "internal_error", "操作失败")
			}
		}()
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey{}, p)))
	})
}
func allowCORS(w http.ResponseWriter, origin string) {
	w.Header().Set("Access-Control-Allow-Origin", origin)
	w.Header().Add("Vary", "Origin")
	w.Header().Set("Access-Control-Expose-Headers", "Location, Upload-Offset, Retry-After")
}
func identity(r *http.Request) string {
	p, _ := r.Context().Value(principalKey{}).(principal)
	return p.Owner
}
func (s *Server) requireWriter(w http.ResponseWriter, r *http.Request) bool {
	if identity(r) != "" {
		return true
	}
	fail(w, 401, "admin_required", "请先登录或配置访问令牌")
	return false
}
func (s *Server) owns(r *http.Request, kind, id string) bool {
	if identity(r) == "admin" {
		return true
	}
	var owner string
	table, column := "upload_owners", "upload_id"
	if kind == "job" {
		table, column = "job_owners", "job_id"
	}
	return identity(r) != "" && s.Store.DB.QueryRow("SELECT owner FROM "+table+" WHERE "+column+"=?", id).Scan(&owner) == nil && owner == identity(r)
}

type apiFailure struct {
	Status        int
	Code, Message string
}

func (e *apiFailure) Error() string                  { return e.Message }
func problem(status int, code, message string) error { return &apiFailure{status, code, message} }
func (s *Server) report(w http.ResponseWriter, err error) {
	var p *apiFailure
	if errors.As(err, &p) {
		fail(w, p.Status, p.Code, p.Message)
	} else {
		s.internal(w, err)
	}
}

// 业务写入和响应记录使用同一事务，断线重放直接读取已提交的结果。
func (s *Server) idempotent(w http.ResponseWriter, r *http.Request, limit int64, apply func(*sql.Tx, []byte) (int, string, any, error)) {
	key := r.Header.Get("Idempotency-Key")
	if len(key) < 1 || len(key) > 128 || strings.ContainsAny(key, "\r\n") {
		fail(w, 422, "invalid_idempotency_key", "请提供 1 至 128 字节的 Idempotency-Key")
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if err != nil {
		fail(w, 413, "body_too_large", "请求内容过大")
		return
	}
	var normalized any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if dec.Decode(&normalized) != nil || dec.Decode(&struct{}{}) != io.EOF {
		fail(w, 400, "invalid_request", "请求内容无效")
		return
	}
	canonical, _ := json.Marshal(normalized)
	digest := tokenHash(string(canonical))
	tx, err := s.Store.DB.BeginTx(r.Context(), nil)
	if err != nil {
		s.internal(w, err)
		return
	}
	defer tx.Rollback()
	var oldDigest, loc string
	var status int
	var response []byte
	err = tx.QueryRow("SELECT digest,status,location,response FROM api_requests WHERE owner=? AND method=? AND path=? AND request_key=?", identity(r), r.Method, r.URL.Path, key).Scan(&oldDigest, &status, &loc, &response)
	if err == nil {
		if digest != oldDigest {
			fail(w, 409, "idempotency_conflict", "同一请求键已用于不同内容")
			return
		}
		if err = tx.Commit(); err != nil {
			s.internal(w, err)
			return
		}
	} else {
		if !errors.Is(err, sql.ErrNoRows) {
			s.internal(w, err)
			return
		}
		var value any
		status, loc, value, err = apply(tx, raw)
		if err != nil {
			s.report(w, err)
			return
		}
		response, err = json.Marshal(value)
		if err != nil {
			s.internal(w, err)
			return
		}
		_, err = tx.Exec("INSERT INTO api_requests(owner,method,path,request_key,digest,status,location,response) VALUES(?,?,?,?,?,?,?,?)", identity(r), r.Method, r.URL.Path, key, digest, status, loc, response)
		if err == nil {
			err = tx.Commit()
		}
		if err != nil {
			s.internal(w, err)
			return
		}
		if status == 202 {
			s.Queue.Notify()
		}
	}
	if loc != "" {
		w.Header().Set("Location", loc)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(response)
}
func parseBody(raw []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if dec.Decode(v) != nil || dec.Decode(&struct{}{}) != io.EOF {
		return problem(400, "invalid_request", "请求内容无效")
	}
	return nil
}
