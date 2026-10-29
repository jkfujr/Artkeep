package server

import (
	"collection/internal/importer"
	"collection/internal/model"
	"collection/internal/queue"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"golang.org/x/crypto/bcrypt"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"
)

func (s *Server) site(w http.ResponseWriter, r *http.Request) {
	required, err := s.Store.Setting("adult_required")
	if err != nil {
		s.internal(w, err)
		return
	}
	respond(w, 200, map[string]any{"admin": s.isAdmin(r), "adultPasswordRequired": required == "true", "adultUnlocked": s.adultAllowed(r)})
}

func (s *Server) filter(w http.ResponseWriter, r *http.Request) (model.Filter, bool) {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}
	size := 48
	if requested, e := strconv.Atoi(q.Get("pageSize")); e == nil && requested > 0 {
		size = min(requested, 100)
	}
	adult := q.Get("includeAdult") == "true"
	if adult && !s.adultAllowed(r) {
		fail(w, 403, "adult_locked", "请输入 R18 浏览密码")
		return model.Filter{}, false
	}
	f := model.Filter{Query: strings.TrimSpace(q.Get("q")), Platform: q.Get("platform"), Author: q.Get("author"), Tags: q["tag"], Adult: adult, Sort: q.Get("sort"), Page: page, PageSize: size, Management: q.Get("management") == "true" && s.isAdmin(r), Years: q["year"], Shapes: q["shape"]}
	valid := true
	for _, year := range f.Years {
		n, err := strconv.Atoi(year)
		if year != "unknown" && (err != nil || len(year) != 4 || n < 1 || n > 9999) {
			valid = false
		}
	}
	for _, shape := range f.Shapes {
		if shape != "landscape" && shape != "portrait" && shape != "square" && model.ShapeRatio(shape) == 0 {
			valid = false
		}
	}
	for name, target := range map[string]*int{"minWidth": &f.MinWidth, "minHeight": &f.MinHeight} {
		if values, exists := q[name]; exists {
			n, err := strconv.Atoi(values[0])
			if len(values) != 1 || err != nil || n < 0 || n > 2147483647 {
				valid = false
			} else {
				*target = n
			}
		}
	}
	if !valid {
		fail(w, 400, "invalid_filter", "筛选条件无效")
		return model.Filter{}, false
	}
	return f, true
}

func (s *Server) years(w http.ResponseWriter, r *http.Request) {
	f, ok := s.filter(w, r)
	if !ok {
		return
	}
	years, err := s.Store.Years(f)
	if err != nil {
		s.internal(w, err)
		return
	}
	respond(w, 200, years)
}

func (s *Server) authors(w http.ResponseWriter, r *http.Request) {
	f, ok := s.filter(w, r)
	if !ok {
		return
	}
	authors, err := s.Store.Authors(f, r.URL.Query().Get("prefix"))
	if err != nil {
		s.internal(w, err)
		return
	}
	respond(w, 200, authors)
}
func (s *Server) works(w http.ResponseWriter, r *http.Request) {
	f, ok := s.filter(w, r)
	if !ok {
		return
	}
	works, total, err := s.Store.List(f)
	if err != nil {
		s.internal(w, err)
		return
	}
	respond(w, 200, map[string]any{"items": works, "total": total, "page": f.Page, "pageSize": f.PageSize, "hasMore": f.Page*f.PageSize < total})
}
func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		fail(w, 404, "not_found", "内容不存在")
		return 0, false
	}
	return id, true
}
func (s *Server) work(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	work, err := s.Store.Work(id, true)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, 404, "not_found", "作品不存在")
		return
	}
	if err != nil {
		s.internal(w, err)
		return
	}
	if !work.Ready || !work.Available && !s.isAdmin(r) {
		fail(w, 404, "not_found", "作品暂不可用")
		return
	}
	if work.Rating > 0 {
		if !s.adultAllowed(r) {
			fail(w, 403, "adult_locked", "请输入 R18 浏览密码")
			return
		}
		if r.URL.Query().Get("includeAdult") != "true" {
			fail(w, 403, "adult_disabled", "请开启 R18 显示")
			return
		}
	}
	respond(w, 200, work)
}
func (s *Server) tags(w http.ResponseWriter, r *http.Request) {
	f, ok := s.filter(w, r)
	if !ok {
		return
	}
	tags, err := s.Store.Tags(f, r.URL.Query().Get("prefix"))
	if err != nil {
		s.internal(w, err)
		return
	}
	respond(w, 200, tags)
}

func (s *Server) asset(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	asset, rating, err := s.Store.Asset(id)
	if errors.Is(err, sql.ErrNoRows) || err == nil && !asset.Available {
		fail(w, 404, "not_found", "文件不可用")
		return
	}
	if err != nil {
		s.internal(w, err)
		return
	}
	if rating > 0 && !s.adultAllowed(r) {
		fail(w, 403, "adult_locked", "请输入 R18 浏览密码")
		return
	}
	variant := r.PathValue("variant")
	if variant != "original" && variant != "thumb" && variant != "preview" {
		fail(w, 404, "not_found", "文件不存在")
		return
	}
	file, err := s.Media.Resolve(asset)
	if errors.Is(err, os.ErrNotExist) {
		s.Store.MissingAsset(asset.ID)
		fail(w, 404, "not_found", "原文件暂不可用")
		return
	}
	if err != nil {
		fail(w, 404, "not_found", "文件不可用")
		return
	}
	if variant != "original" {
		if asset.Kind == "document" {
			fail(w, 404, "no_preview", "没有图片预览")
			return
		}
		file, err = s.Media.Rendition(r.Context(), asset, variant)
		if err != nil {
			fail(w, 422, "preview_failed", "预览生成失败")
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
	}
	f, err := os.Open(file)
	if err != nil {
		fail(w, 404, "not_found", "文件不可用")
		return
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		s.internal(w, err)
		return
	}
	if variant == "original" {
		mimeType := mime.TypeByExtension("." + asset.Format)
		if asset.Format == "apng" {
			mimeType = "image/apng"
		}
		if asset.Format == "epub" {
			mimeType = "application/epub+zip"
		}
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}
		w.Header().Set("Content-Type", mimeType)
		if r.URL.Query().Get("download") == "1" {
			w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filepath.Base(asset.Path)}))
		}
	}
	if rating > 0 {
		w.Header().Set("Cache-Control", "private, no-store")
	} else {
		w.Header().Set("Cache-Control", "private, no-cache")
	}
	w.Header().Add("Vary", "Cookie")
	http.ServeContent(w, r, filepath.Base(file), stat.ModTime(), f)
}

func (s *Server) settings(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if r.Method == "PATCH" {
		var input struct {
			Required bool   `json:"adultPasswordRequired"`
			Password string `json:"adultPassword"`
		}
		if !decode(w, r, &input) {
			return
		}
		hash, err := s.Store.Setting("adult_password")
		if err != nil {
			s.internal(w, err)
			return
		}
		if input.Password != "" {
			if len(input.Password) < 6 || len(input.Password) > 72 {
				fail(w, 400, "invalid_password", "R18 密码须为 6 至 72 字节")
				return
			}
			b, e := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
			if e != nil {
				s.internal(w, e)
				return
			}
			hash = string(b)
		}
		if input.Required && hash == "" {
			fail(w, 400, "password_required", "请先设置 R18 浏览密码")
			return
		}
		tx, err := s.Store.DB.Begin()
		if err != nil {
			s.internal(w, err)
			return
		}
		defer tx.Rollback()
		for key, value := range map[string]string{"adult_required": strconv.FormatBool(input.Required), "adult_password": hash, "adult_version": randomToken()} {
			if _, err = tx.Exec("INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", key, value); err != nil {
				s.internal(w, err)
				return
			}
		}
		if _, err = tx.Exec("DELETE FROM sessions WHERE scope='adult'"); err != nil {
			s.internal(w, err)
			return
		}
		if err = tx.Commit(); err != nil {
			s.internal(w, err)
			return
		}
	}
	required, err := s.Store.Setting("adult_required")
	if err != nil {
		s.internal(w, err)
		return
	}
	password, err := s.Store.Setting("adult_password")
	if err != nil {
		s.internal(w, err)
		return
	}
	_, galleryErr := exec.LookPath(s.Config.GalleryDL)
	_, ffmpegErr := exec.LookPath(s.Config.FFmpeg)
	_, ffprobeErr := exec.LookPath(s.Config.FFprobe)
	respond(w, 200, map[string]any{"adultPasswordRequired": required == "true", "adultPasswordSet": password != "", "pixivDirectory": s.Config.PixivDir, "galleryDlAvailable": galleryErr == nil, "ffmpegAvailable": ffmpegErr == nil, "ffprobeAvailable": ffprobeErr == nil, "cookiesConfigured": s.Config.CookiesFile != ""})
}

func (s *Server) imports(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var input struct {
		Kind     string   `json:"kind"`
		URL      string   `json:"url"`
		URLs     []string `json:"urls"`
		Path     string   `json:"path"`
		UploadID string   `json:"uploadId"`
		Rating   int      `json:"rating"`
	}
	if !decode(w, r, &input) {
		return
	}
	batch := queue.BatchInput{Source: input.Kind, Rating: input.Rating}
	if batch.Rating < 0 || batch.Rating > 2 {
		fail(w, 400, "invalid_rating", "分级无效")
		return
	}
	switch input.Kind {
	case "url", "x":
		batch.Source = "url"
		batch.URLs = input.URLs
		if len(batch.URLs) == 0 {
			batch.URLs = strings.Split(input.URL, "\n")
		}
		clean := []string{}
		for _, value := range batch.URLs {
			if value = strings.TrimSpace(value); value != "" {
				clean = append(clean, value)
			}
		}
		batch.URLs = clean
		if len(batch.URLs) == 0 || len(batch.URLs) > 1000 {
			fail(w, 400, "invalid_urls", "每批支持 1 至 1000 个链接")
			return
		}
	case "pixiv", "server":
		batch.Source = "server"
		batch.Path = input.Path
		if input.Kind == "pixiv" {
			batch.Path = s.Config.PixivDir
		}
		var err error
		batch.Path, err = importer.Directory(batch.Path, s.Config.DataDir)
		if err != nil {
			fail(w, 400, "invalid_directory", err.Error())
			return
		}
	case "migration":
	case "upload":
		s.completeUploadImport(w, r, input.UploadID, batch.Rating)
		return
	default:
		fail(w, 400, "invalid_kind", "不支持的导入来源")
		return
	}
	encoded, err := json.Marshal(batch)
	if err != nil {
		s.internal(w, err)
		return
	}
	job, err := s.Queue.Enqueue("batch", string(encoded), string(encoded))
	if err != nil {
		s.internal(w, err)
		return
	}
	respond(w, 202, job)
}
func (s *Server) jobs(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	jobs, err := s.Store.Jobs()
	if err != nil {
		s.internal(w, err)
		return
	}
	respond(w, 200, jobs)
}
func (s *Server) retry(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var input struct{}
	if !decode(w, r, &input) {
		return
	}
	job, err := s.Queue.Retry(id)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, 404, "not_found", "任务不存在")
		return
	}
	if err != nil {
		fail(w, 409, "retry_failed", "任务当前不能重试，或已有相同任务正在运行")
		return
	}
	respond(w, 202, job)
}
func (s *Server) edit(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var input map[string]json.RawMessage
	if !decode(w, r, &input) {
		return
	}
	work, err := s.Store.Work(id, false)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, 404, "not_found", "作品不存在")
		return
	}
	if err != nil {
		s.internal(w, err)
		return
	}
	for key, value := range input {
		switch key {
		case "localTags":
			var tags []string
			if err = json.Unmarshal(value, &tags); err != nil || len(tags) > 100 {
				fail(w, 400, "invalid_tags", "标签格式无效")
				return
			}
			for _, tag := range tags {
				if utf8.RuneCountInString(tag) > 100 || strings.ContainsAny(tag, "\r\n") {
					fail(w, 400, "invalid_tags", "单个标签不能超过 100 个字")
					return
				}
			}
			work.LocalTags = tags
		case "ratingOverride":
			var rating *int
			if err = json.Unmarshal(value, &rating); err != nil || rating != nil && (*rating < 0 || *rating > 2) {
				fail(w, 400, "invalid_rating", "分级无效")
				return
			}
			work.RatingOverride = rating
		default:
			fail(w, 400, "invalid_field", fmt.Sprintf("不支持修改 %s", key))
			return
		}
	}
	if err = s.Store.EditWork(id, work.LocalTags, work.RatingOverride); err != nil {
		s.internal(w, err)
		return
	}
	updated, err := s.Store.Work(id, true)
	if err != nil {
		s.internal(w, err)
		return
	}
	respond(w, 200, updated)
}
