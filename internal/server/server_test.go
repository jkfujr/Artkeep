package server

import (
	"bytes"
	"collection/internal/config"
	"collection/internal/media"
	"collection/internal/model"
	"collection/internal/queue"
	"collection/internal/store"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
)

func TestRestrictedBrowsingAndLocalOverrides(t *testing.T) {
	t.Setenv("ARCHIVE_ADMIN_PASSWORD", "test-admin-password")
	dir := t.TempDir()
	c := config.Config{DataDir: dir, PixivDir: filepath.Join(dir, "pixiv"), FFmpeg: "ffmpeg", FFprobe: "ffprobe", GalleryDL: "gallery-dl"}
	var err error
	if err = os.MkdirAll(c.PixivDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(c.CacheDir(), 0700); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(filepath.Join(c.PixivDir, "sample.png"))
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 16, 12))
	img.Set(0, 0, color.RGBA{B: 200, A: 255})
	if err = png.Encode(file, img); err != nil {
		t.Fatal(err)
	}
	file.Close()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.DB.Close()
	m := media.New(c)
	asset, err := m.Inspect(t.Context(), "pixiv", c.PixivDir, filepath.Join(c.PixivDir, "sample.png"), 0)
	if err != nil {
		t.Fatal(err)
	}
	makeWork := func(id string, rating int, tags []string) model.Work {
		w := model.Work{Platform: "pixiv", SourceKind: "artwork", SourceID: id, SourceURL: "https://www.pixiv.net/artworks/" + id, Title: "测试作品", Description: "说明", AuthorID: "42", AuthorName: "测试作者", AuthorURL: "https://www.pixiv.net/users/42", PublishedAt: "2026-01-01T00:00:00Z", SourceRating: rating, SourceTags: tags, Root: "pixiv", Raw: "source", Ready: true}
		if e := st.UpsertWork(&w, []model.Asset{asset}); e != nil {
			t.Fatal(e)
		}
		return w
	}
	makeWork("1", 0, []string{"蓝色"})
	restricted := makeWork("2", 2, []string{"日落"})
	app, err := New(st, m, queue.New(st, m, c), c, fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<title>作品收藏</title>")}})
	if err != nil {
		t.Fatal(err)
	}
	host := httptest.NewServer(app.Handler())
	defer host.Close()
	request := func(method, path string, body any, cookies []*http.Cookie, headers map[string]string) (*http.Response, []byte) {
		var data io.Reader
		if body != nil {
			b, e := json.Marshal(body)
			if e != nil {
				t.Fatal(e)
			}
			data = bytes.NewReader(b)
		}
		r, e := http.NewRequest(method, host.URL+path, data)
		if e != nil {
			t.Fatal(e)
		}
		if body != nil {
			r.Header.Set("Content-Type", "application/json")
		}
		for _, cookie := range cookies {
			r.AddCookie(cookie)
		}
		for key, value := range headers {
			r.Header.Set(key, value)
		}
		response, e := host.Client().Do(r)
		if e != nil {
			t.Fatal(e)
		}
		b, e := io.ReadAll(response.Body)
		response.Body.Close()
		if e != nil {
			t.Fatal(e)
		}
		return response, b
	}
	response, _ := request("POST", "/api/admin/session", map[string]string{"password": "test-admin-password"}, nil, nil)
	if response.StatusCode != 200 {
		t.Fatalf("登录失败: %d", response.StatusCode)
	}
	admin := response.Cookies()
	response, _ = request("PATCH", "/api/admin/settings", map[string]any{"adultPasswordRequired": true, "adultPassword": "test-adult-password"}, admin, nil)
	if response.StatusCode != 200 {
		t.Fatalf("配置失败: %d", response.StatusCode)
	}
	response, b := request("GET", "/api/works", nil, nil, nil)
	var list struct {
		Items []model.Work `json:"items"`
		Total int          `json:"total"`
	}
	if err = json.Unmarshal(b, &list); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 || list.Total != 1 || list.Items[0].Rating != 0 {
		t.Fatalf("默认列表包含受限内容: %s", b)
	}
	_, b = request("GET", "/api/tags", nil, nil, nil)
	if bytes.Contains(b, []byte("日落")) {
		t.Fatal("标签统计泄露受限作品")
	}
	assetURL := restricted.Assets[0].ID
	for _, variant := range []string{"original", "thumb", "preview"} {
		response, _ = request("GET", "/api/assets/"+idString(assetURL)+"/"+variant, nil, nil, nil)
		if response.StatusCode != 403 {
			t.Fatalf("受限 %s 返回 %d", variant, response.StatusCode)
		}
	}
	response, _ = request("GET", "/api/works?includeAdult=true", nil, nil, nil)
	if response.StatusCode != 403 {
		t.Fatal("列表绕过成人授权")
	}
	response, _ = request("POST", "/api/adult/session", map[string]string{"password": "wrong"}, nil, nil)
	if response.StatusCode != 401 {
		t.Fatal("错误密码被接受")
	}
	response, _ = request("POST", "/api/adult/session", map[string]string{"password": "test-adult-password"}, nil, nil)
	if response.StatusCode != 200 {
		t.Fatal("成人解锁失败")
	}
	adult := response.Cookies()
	response, b = request("GET", "/api/works?includeAdult=true", nil, adult, nil)
	if err = json.Unmarshal(b, &list); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 || list.Total != 2 {
		t.Fatalf("解锁后列表错误: %s", b)
	}
	response, b = request("GET", "/api/assets/"+idString(assetURL)+"/original", nil, adult, map[string]string{"Range": "bytes=0-7"})
	if response.StatusCode != 206 || len(b) != 8 {
		t.Fatalf("文件分段响应错误: %d %d", response.StatusCode, len(b))
	}
	if !strings.Contains(response.Header.Get("Cache-Control"), "no-store") {
		t.Fatal("受限文件允许缓存")
	}
	response, _ = request("GET", "/api/assets/"+idString(assetURL)+"/thumb", nil, adult, nil)
	if response.StatusCode != 200 {
		t.Fatal("缩略图不可用")
	}
	response, _ = request("PATCH", "/api/admin/settings", map[string]any{"adultPasswordRequired": true, "adultPassword": "changed-adult-password"}, admin, nil)
	if response.StatusCode != 200 {
		t.Fatal("修改密码失败")
	}
	response, _ = request("GET", "/api/assets/"+idString(assetURL)+"/original", nil, adult, nil)
	if response.StatusCode != 403 {
		t.Fatal("旧授权未失效")
	}
	response, _ = request("PATCH", "/api/admin/works/"+idString(restricted.ID), map[string]any{"localTags": []string{"自选"}, "ratingOverride": 0}, admin, nil)
	if response.StatusCode != 200 {
		t.Fatal("修改本地信息失败")
	}
	restricted.SourceTags = []string{"新来源标签"}
	if err = st.UpsertWork(&restricted, restricted.Assets); err != nil {
		t.Fatal(err)
	}
	saved, err := st.Work(restricted.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Rating != 0 || len(saved.LocalTags) != 1 || saved.LocalTags[0] != "自选" || saved.SourceTags[0] != "新来源标签" {
		t.Fatalf("重复导入覆盖人工修改: %+v", saved)
	}
	response, _ = request("POST", "/api/admin/imports", map[string]string{"kind": "pixiv"}, nil, nil)
	if response.StatusCode != 401 {
		t.Fatal("访客可以创建任务")
	}
	response, _ = request("PATCH", "/api/admin/works/"+idString(restricted.ID), map[string]any{"localTags": []string{}}, admin, map[string]string{"Origin": "https://invalid.example"})
	if response.StatusCode != 403 {
		t.Fatal("跨站写入未拦截")
	}
	response, b = request("GET", "/api/works/"+idString(restricted.ID), nil, nil, nil)
	if response.StatusCode != 200 {
		t.Fatal("人工调整为全年龄后无法公开")
	}
	if bytes.Contains(b, []byte(c.PixivDir)) || bytes.Contains(b, []byte("raw_metadata")) {
		t.Fatal("接口泄露存储信息")
	}
}

func TestUploadResumeManifestChangesAndAccess(t *testing.T) {
	t.Setenv("ARCHIVE_ADMIN_PASSWORD", "test-admin-password")
	base := t.TempDir()
	data := filepath.Join(base, "data")
	source := filepath.Join(base, "source")
	for _, dir := range []string{data, source, filepath.Join(source, "folder")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(source, "hidden.txt"), []byte("hidden"), 0600); err != nil {
		t.Fatal(err)
	}
	c := config.Config{DataDir: data}
	st, err := store.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { st.DB.Close() }()
	m := media.New(c)
	app, err := New(st, m, queue.New(st, m, c), c, fstest.MapFS{})
	if err != nil {
		t.Fatal(err)
	}
	handler := app.Handler()
	var cookies []*http.Cookie
	request := func(method, path string, body []byte, binary bool, origin string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "http://artkeep.test"+path, bytes.NewReader(body))
		if method != "GET" {
			req.Header.Set("Content-Type", "application/json")
			if binary {
				req.Header.Set("Content-Type", "application/octet-stream")
			}
		}
		for _, cookie := range cookies {
			req.AddCookie(cookie)
		}
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	if r := request("GET", "/api/admin/directories", nil, false, ""); r.Code != 401 {
		t.Fatal("目录未要求管理员登录")
	}
	login := request("POST", "/api/admin/session", []byte(`{"password":"test-admin-password"}`), false, "")
	if login.Code != 200 {
		t.Fatal("登录失败", login.Body.String())
	}
	cookies = login.Result().Cookies()
	r := request("GET", "/api/admin/directories?path="+url.QueryEscape(source), nil, false, "")
	if r.Code != 200 || strings.Contains(r.Body.String(), "hidden.txt") || !strings.Contains(r.Body.String(), "folder") {
		t.Fatal("目录浏览包含文件或缺失目录", r.Body.String())
	}
	for _, path := range []string{"../escape.png", "/absolute.png", "C:/escape.png", "root/../x.png", "root\\x.png", "root/NUL.png", "root/folder./x.png"} {
		raw, _ := json.Marshal(map[string]any{"name": "root", "files": []uploadFile{{Path: path, Size: 1}}})
		if r := request("POST", "/api/admin/uploads", raw, false, ""); r.Code != 400 {
			t.Fatalf("接受危险上传路径 %s", path)
		}
	}
	manifest := []uploadFile{{Path: "root/large.epub", Size: uploadChunkSize + 5, Modified: 10}, {Path: "root/small.png", Size: 3, Modified: 20}}
	create := func(id string) uploadSession {
		raw, _ := json.Marshal(map[string]any{"id": id, "name": "root", "files": manifest})
		r := request("POST", "/api/admin/uploads", raw, false, "")
		if r.Code != 200 {
			t.Fatal(r.Body.String())
		}
		var v uploadSession
		if err := json.Unmarshal(r.Body.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	upload := create("")
	prefix := "/api/admin/uploads/" + upload.ID
	chunk := bytes.Repeat([]byte("a"), int(uploadChunkSize))
	if r := request("PUT", prefix+"/files/0?offset=0", chunk, true, "https://other.example"); r.Code != 403 {
		t.Fatal("分块未检查请求来源")
	}
	savedCookies := cookies
	cookies = nil
	if r := request("PUT", prefix+"/files/0?offset=0", chunk, true, ""); r.Code != 401 {
		t.Fatal("分块未检查管理员权限")
	}
	cookies = savedCookies
	if r := request("PUT", prefix+"/files/0?offset=0", chunk, true, ""); r.Code != 200 {
		t.Fatal("8MiB 分块受普通 JSON 限额影响", r.Body.String())
	}
	if r := request("PUT", prefix+"/files/1?offset=0", []byte("png"), true, ""); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	if r := request("PUT", prefix+"/files/0?offset=0", []byte("wrong"), true, ""); r.Code != 409 {
		t.Fatal("接受错误续传偏移")
	}
	if r := request("POST", prefix+"/complete", []byte(`{"rating":0}`), false, ""); r.Code != 409 {
		t.Fatal("未完成上传已进入解析")
	}
	if err = st.DB.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = store.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	app, err = New(st, m, queue.New(st, m, c), c, fstest.MapFS{})
	if err != nil {
		t.Fatal(err)
	}
	handler = app.Handler()
	resumed := create(upload.ID)
	if resumed.Files[0].Offset != uploadChunkSize || resumed.Files[1].Offset != 3 {
		t.Fatal("重启未恢复分块偏移")
	}
	manifest[0].Modified = 11
	resumed = create(upload.ID)
	if resumed.Files[0].Offset != 0 || resumed.Files[1].Offset != 3 {
		t.Fatal("文件变更未单独重传")
	}
	if r := request("PUT", prefix+"/files/0?offset=0", chunk, true, ""); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	if r := request("PUT", prefix+"/files/0?offset="+strconv.FormatInt(uploadChunkSize, 10), []byte("12345"), true, ""); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	complete := request("POST", prefix+"/complete", []byte(`{"rating":1}`), false, "")
	if complete.Code != 202 {
		t.Fatal(complete.Body.String())
	}
	again := request("POST", prefix+"/complete", []byte(`{"rating":1}`), false, "")
	if again.Code != 200 {
		t.Fatal(again.Body.String())
	}
	jobs, err := st.Jobs()
	if err != nil || len(jobs) != 1 {
		t.Fatal("完成接口重复入队", err)
	}
	if r := request("DELETE", prefix, []byte(`{}`), false, ""); r.Code != 409 {
		t.Fatal("允许取消正在解析的上传")
	}
	other := create("")
	if r := request("PUT", "/api/admin/uploads/"+other.ID+"/files/1?offset=0", []byte("png"), true, ""); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	if r := request("DELETE", "/api/admin/uploads/"+other.ID, []byte(`{}`), false, ""); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	if _, err = os.Stat(app.uploadPath(upload.ID, "root/large.epub")); err != nil {
		t.Fatal("取消上传删除了其他会话")
	}
}

func idString(id int64) string { return strconv.FormatInt(id, 10) }

func TestGalleryFiltersAndCandidates(t *testing.T) {
	t.Setenv("ARCHIVE_ADMIN_PASSWORD", "test-admin-password")
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.DB.Close()
	c := config.Config{DataDir: dir}
	m := media.New(c)
	app, err := New(st, m, queue.New(st, m, c), c, fstest.MapFS{})
	if err != nil {
		t.Fatal(err)
	}
	add := func(id, platform, author, name, published string, rating int, assets []model.Asset) model.Work {
		w := model.Work{Platform: platform, SourceKind: "artwork", SourceID: id, AuthorID: author, AuthorName: name, PublishedAt: published, SourceRating: rating, SourceTags: []string{"共同"}, Ready: true}
		if e := st.UpsertWork(&w, assets); e != nil {
			t.Fatal(e)
		}
		return w
	}
	asset := func(path, kind string, position, width, height int, available bool) model.Asset {
		return model.Asset{Root: "pixiv", Path: path, Kind: kind, Position: position, Width: width, Height: height, Available: available}
	}
	multi := add("multi", "pixiv", "42", "公开作者", "2026-01-01T00:00:00Z", 0, []model.Asset{asset("a.png", "image", 0, 1000, 500, true), asset("b.png", "image", 1, 800, 800, true), asset("c.png", "image", 2, 600, 1000, true)})
	wide := add("wide", "pixiv", "43", "宽图作者", "2025-06-01T00:00:00Z", 0, []model.Asset{asset("gone.png", "image", 0, 5000, 5000, false), asset("wide.png", "image", 1, 1600, 900, true)})
	add("private", "x", "42", "受限作者", "2024-01-01T00:00:00Z", 2, []model.Asset{asset("private.png", "image", 0, 1200, 1800, true)})
	add("document", "pixiv", "42", "公开作者", "", 0, []model.Asset{asset("book.epub", "document", 0, 600, 800, true), asset("video.mp4", "video", 1, 1920, 1080, true)})
	handler := app.Handler()
	get := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRecorder()
		handler.ServeHTTP(r, httptest.NewRequest("GET", path, nil))
		return r
	}
	for _, tc := range []struct {
		query string
		total int
		cover string
	}{
		{"year=2026", 1, "a.png"},
		{"year=2026&year=2025", 2, "a.png"},
		{"year=unknown", 1, "video.mp4"},
		{"shape=square&minWidth=700", 1, "b.png"},
		{"minWidth=900&minHeight=900", 1, "wide.png"},
		{"shape=portrait&minWidth=700", 0, ""},
		{"shape=16:9", 1, "wide.png"},
		{"shape=portrait&shape=16:9", 2, "c.png"},
		{"year=2026&shape=square&tag=共同", 1, "b.png"},
	} {
		t.Run(tc.query, func(t *testing.T) {
			r := get("/api/works?" + tc.query)
			var result struct {
				Total int
				Items []model.Work
			}
			if err := json.Unmarshal(r.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if r.Code != 200 || result.Total != tc.total {
				t.Fatalf("列表不匹配: %d %s", r.Code, r.Body.String())
			}
			if tc.cover != "" {
				w, err := st.Work(result.Items[0].ID, true)
				if err != nil {
					t.Fatal(err)
				}
				found := false
				for _, a := range w.Assets {
					if a.ID == result.Items[0].Cover.ID && a.Path == tc.cover {
						found = true
					}
				}
				if !found {
					t.Fatalf("封面不匹配: %+v", result.Items[0].Cover)
				}
			}
		})
	}
	for _, query := range []string{"year=bad", "year=", "year=0000", "shape=invalid", "minWidth=-1", "minHeight=1.5", "minWidth=", "minWidth=1&minWidth=2"} {
		if r := get("/api/works?" + query); r.Code != 400 {
			t.Fatalf("非法参数未拒绝: %s %d", query, r.Code)
		}
	}
	for _, path := range []string{"/api/years?year=2026", "/api/authors?author=42", "/api/tags"} {
		r := get(path)
		if r.Code != 200 || strings.Contains(r.Body.String(), "受限作者") || strings.Contains(r.Body.String(), "2024") {
			t.Fatalf("候选泄露受限内容: %s", r.Body.String())
		}
		if strings.HasPrefix(path, "/api/years") && (!strings.Contains(r.Body.String(), "2025") || !strings.Contains(r.Body.String(), "unknown")) {
			t.Fatal("年份未排除自身条件")
		}
		if strings.HasPrefix(path, "/api/authors") && !strings.Contains(r.Body.String(), "宽图作者") {
			t.Fatal("作者未排除自身条件")
		}
	}
	if r := get("/api/authors?prefix=宽图"); r.Code != 200 || strings.Contains(r.Body.String(), "公开作者") || !strings.Contains(r.Body.String(), "宽图作者") {
		t.Fatal("作者搜索错误")
	}
	if r := get("/api/tags?minWidth=900&minHeight=900"); !strings.Contains(r.Body.String(), `"count":1`) {
		t.Fatal("标签统计未复用图片条件")
	}
	if r := get("/api/works?author=42&platform=pixiv&includeAdult=true"); r.Code != 200 || strings.Contains(r.Body.String(), "受限作者") {
		t.Fatal("作者跨平台串联")
	}
	if err := st.SetSetting("adult_required", "true"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/years", "/api/authors", "/api/tags", "/api/works"} {
		if r := get(path + "?includeAdult=true"); r.Code != 403 {
			t.Fatalf("候选绕过成人授权: %s", path)
		}
	}
	if err := st.SetSetting("adult_required", "false"); err != nil {
		t.Fatal(err)
	}
	if r := get("/api/years?includeAdult=true"); !strings.Contains(r.Body.String(), "2024") {
		t.Fatal("授权后年份缺失")
	}
	if r := get("/api/authors?includeAdult=true"); !strings.Contains(r.Body.String(), "受限作者") {
		t.Fatal("授权后作者缺失")
	}
	if r := get("/api/works?shape=portrait&minWidth=900&includeAdult=true"); r.Code != 200 || !strings.Contains(r.Body.String(), `"total":1`) {
		t.Fatal("受限图片筛选错误")
	}
	_ = multi
	_ = wide
}
