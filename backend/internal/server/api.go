package server

import (
	"collection/internal/model"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
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
	page, size, e := pagination(r)
	if e != nil {
		s.report(w, e)
		return model.Filter{}, false
	}
	for key, values := range q {
		repeated := key == "tag" || key == "year" || key == "shape"
		allowed := repeated || key == "q" || key == "platform" || key == "author" || key == "sort" || key == "page" || key == "pageSize" || key == "includeAdult" || key == "management" || key == "minWidth" || key == "minHeight" || key == "prefix"
		if !allowed || !repeated && len(values) != 1 {
			fail(w, 422, "invalid_filter", "筛选字段无效")
			return model.Filter{}, false
		}
	}
	for _, key := range []string{"includeAdult", "management"} {
		if v, ok := q[key]; ok && v[0] != "true" && v[0] != "false" {
			fail(w, 422, "invalid_filter", "布尔筛选值无效")
			return model.Filter{}, false
		}
	}
	if v := q.Get("platform"); v != "" && v != "pixiv" && v != "x" && v != "fanbox" && v != "local" {
		fail(w, 422, "invalid_filter", "平台筛选无效")
		return model.Filter{}, false
	}
	if v := q.Get("sort"); v != "" && v != "published" && v != "added" {
		fail(w, 422, "invalid_filter", "排序无效")
		return model.Filter{}, false
	}
	if q.Get("management") == "true" && !s.isAdmin(r) {
		fail(w, 403, "forbidden", "管理筛选需要管理员权限")
		return model.Filter{}, false
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
	respond(w, 200, map[string]any{"items": years})
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
	respond(w, 200, map[string]any{"items": authors})
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
	items := make([]workDTO, len(works))
	for i := range works {
		items[i] = workResponse(works[i])
	}
	respond(w, 200, map[string]any{"items": items, "total": total, "page": f.Page, "pageSize": f.PageSize, "hasMore": f.Page*f.PageSize < total})
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
	respond(w, 200, workResponse(work))
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
	respond(w, 200, map[string]any{"items": tags})
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
	if variant != "original" && variant != "thumbnail" && variant != "preview" && variant != "playback" {
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
		if variant == "playback" {
			file, err = s.Media.Playback(r.Context(), asset)
		} else {
			v := variant
			if v == "thumbnail" {
				v = "thumb"
			}
			file, err = s.Media.Rendition(r.Context(), asset, v)
		}
		if err != nil {
			fail(w, 422, "preview_failed", "预览生成失败")
			return
		}
		if variant == "playback" {
			w.Header().Set("Content-Type", "video/mp4")
		} else {
			w.Header().Set("Content-Type", "image/jpeg")
		}
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
			w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": s.originalFilename(asset)}))
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
		var input map[string]json.RawMessage
		if !decode(w, r, &input) {
			return
		}
		for key, value := range input {
			if key != "adultPasswordRequired" {
				fail(w, 422, "invalid_field", "设置字段无效")
				return
			}
			var required bool
			if string(value) == "null" || json.Unmarshal(value, &required) != nil {
				fail(w, 422, "invalid_field", "请输入布尔值")
				return
			}
			hash, e := s.Store.Setting("adult_password")
			if e != nil {
				s.internal(w, e)
				return
			}
			if required && hash == "" {
				fail(w, 422, "password_required", "请先设置 R18 浏览密码")
				return
			}
			if e = s.Store.SetSetting("adult_required", strconv.FormatBool(required)); e != nil {
				s.internal(w, e)
				return
			}
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
	respond(w, 200, workResponse(updated))
}
