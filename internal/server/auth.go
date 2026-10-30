package server

import (
	"collection/internal/store"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"golang.org/x/crypto/bcrypt"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

func randomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func tokenHash(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

func ResetAdmin(st *store.Store) error {
	password := os.Getenv("ARCHIVE_ADMIN_PASSWORD")
	if password == "" {
		return errors.New("请通过 ARCHIVE_ADMIN_PASSWORD 设置管理员密码")
	}
	return setAdminPassword(st, password)
}

const adminHashPrefix = "bcrypt-sha256:"

func hashAdminPassword(password string) (string, error) {
	// 先固定输入长度，避免 bcrypt 的 72 字节上限截断或拒绝长密码。
	hash, err := bcrypt.GenerateFromPassword([]byte(tokenHash(password)), bcrypt.DefaultCost)
	return adminHashPrefix + string(hash), err
}

func matchesAdminPassword(hash, password string) bool {
	if strings.HasPrefix(hash, adminHashPrefix) {
		return bcrypt.CompareHashAndPassword([]byte(strings.TrimPrefix(hash, adminHashPrefix)), []byte(tokenHash(password))) == nil
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

func setAdminPassword(st *store.Store, password string) error {
	hash, err := hashAdminPassword(password)
	if err != nil {
		return err
	}
	tx, err := st.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("INSERT INTO settings(key,value) VALUES('admin_password',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", hash); err != nil {
		return err
	}
	if _, err = tx.Exec("DELETE FROM sessions WHERE scope='admin'"); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Server) bootstrap() error {
	hash, err := s.Store.Setting("admin_password")
	if err != nil {
		return err
	}
	password := os.Getenv("ARCHIVE_ADMIN_PASSWORD")
	if hash != "" {
		return nil
	}
	if password == "" {
		password = randomToken()[:20]
		log.Printf("首次启动的管理员密码: %s", password)
	}
	hashed, err := hashAdminPassword(password)
	if err != nil {
		return err
	}
	if err = s.Store.SetSetting("admin_password", hashed); err != nil {
		return err
	}
	return s.Store.SetSetting("adult_version", randomToken())
}

func (s *Server) session(r *http.Request, scope string) bool {
	cookie, err := r.Cookie("collection_" + scope)
	if err != nil || len(cookie.Value) > 128 {
		return false
	}
	var expiry int64
	var version string
	err = s.Store.DB.QueryRow("SELECT expires,version FROM sessions WHERE token_hash=? AND scope=?", tokenHash(cookie.Value), scope).Scan(&expiry, &version)
	if err != nil || time.Now().Unix() >= expiry {
		return false
	}
	if scope == "adult" {
		current, e := s.Store.Setting("adult_version")
		if e != nil || version != current {
			return false
		}
	}
	return true
}
func (s *Server) isAdmin(r *http.Request) bool { return s.session(r, "admin") }
func (s *Server) adultAllowed(r *http.Request) bool {
	required, err := s.Store.Setting("adult_required")
	if err != nil {
		return false
	}
	return required != "true" || s.isAdmin(r) || s.session(r, "adult")
}
func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	if s.isAdmin(r) {
		return true
	}
	fail(w, 401, "admin_required", "请先登录管理员")
	return false
}

func (s *Server) issueSession(w http.ResponseWriter, r *http.Request, scope string) error {
	version := ""
	if scope == "adult" {
		var err error
		version, err = s.Store.Setting("adult_version")
		if err != nil {
			return err
		}
	}
	token := randomToken()
	expiry := time.Now().Add(24 * time.Hour)
	_, err := s.Store.DB.Exec("INSERT INTO sessions(token_hash,scope,expires,version) VALUES(?,?,?,?)", tokenHash(token), scope, expiry.Unix(), version)
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{Name: "collection_" + scope, Value: token, Path: "/", HttpOnly: true, Secure: r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https", SameSite: http.SameSiteStrictMode, Expires: expiry, MaxAge: 86400})
	return nil
}
func (s *Server) clearSession(w http.ResponseWriter, r *http.Request, scope string) error {
	if c, err := r.Cookie("collection_" + scope); err == nil {
		if _, err = s.Store.DB.Exec("DELETE FROM sessions WHERE token_hash=?", tokenHash(c.Value)); err != nil {
			return err
		}
	}
	http.SetCookie(w, &http.Cookie{Name: "collection_" + scope, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https", SameSite: http.SameSiteStrictMode})
	return nil
}

type attempt struct {
	count int
	start time.Time
}
type limiter struct {
	mu      sync.Mutex
	entries map[string]attempt
}

func (l *limiter) allow(r *http.Request) bool {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	key := host + ":" + r.URL.Path
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if len(l.entries) > 10000 {
		for k, v := range l.entries {
			if now.Sub(v.start) > 10*time.Minute {
				delete(l.entries, k)
			}
		}
		if len(l.entries) > 10000 {
			return false
		}
	}
	v := l.entries[key]
	if now.Sub(v.start) > 10*time.Minute {
		v = attempt{start: now}
	}
	v.count++
	l.entries[key] = v
	return v.count <= 15
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	scope := "admin"
	if r.URL.Path == "/api/adult/session" {
		scope = "adult"
	}
	if r.Method == "DELETE" {
		if err := s.clearSession(w, r, scope); err != nil {
			s.internal(w, err)
			return
		}
		respond(w, 200, map[string]bool{"ok": true})
		return
	}
	if !s.Limiter.allow(r) {
		fail(w, 429, "too_many_attempts", "尝试次数过多，请稍后再试")
		return
	}
	var input struct {
		Password string `json:"password"`
	}
	if !decode(w, r, &input) {
		return
	}
	if scope == "adult" && len(input.Password) > 72 {
		fail(w, 401, "invalid_password", "密码不正确")
		return
	}
	if scope == "adult" {
		required, err := s.Store.Setting("adult_required")
		if err != nil {
			s.internal(w, err)
			return
		}
		if required != "true" || s.isAdmin(r) {
			respond(w, 200, map[string]bool{"ok": true})
			return
		}
	}
	hash, err := s.Store.Setting(scope + "_password")
	if err != nil {
		s.internal(w, err)
		return
	}
	valid := false
	if hash != "" {
		if scope == "admin" {
			valid = matchesAdminPassword(hash, input.Password)
		} else {
			valid = bcrypt.CompareHashAndPassword([]byte(hash), []byte(input.Password)) == nil
		}
	}
	if !valid {
		fail(w, 401, "invalid_password", "密码不正确")
		return
	}
	if err = s.issueSession(w, r, scope); err != nil {
		s.internal(w, err)
		return
	}
	respond(w, 200, map[string]bool{"ok": true})
}

func (s *Server) changeAdminPassword(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var input struct {
		Current  string `json:"currentPassword"`
		Password string `json:"newPassword"`
	}
	if !decode(w, r, &input) {
		return
	}
	if input.Password == "" {
		fail(w, 400, "password_required", "请输入新密码")
		return
	}
	hash, err := s.Store.Setting("admin_password")
	if err != nil {
		s.internal(w, err)
		return
	}
	if !matchesAdminPassword(hash, input.Current) {
		fail(w, 401, "invalid_password", "当前密码不正确")
		return
	}
	if err = setAdminPassword(s.Store, input.Password); err != nil {
		s.internal(w, err)
		return
	}
	if err = s.issueSession(w, r, "admin"); err != nil {
		s.internal(w, err)
		return
	}
	respond(w, 200, map[string]bool{"ok": true})
}
