package server

import (
	"bytes"
	"collection/internal/config"
	"collection/internal/media"
	"collection/internal/queue"
	"collection/internal/store"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
)

func TestOpenAPIIncludesEveryRoute(t *testing.T) {
	source, e := os.ReadFile("server.go")
	if e != nil {
		t.Fatal(e)
	}
	var document struct {
		OpenAPI string                    `json:"openapi"`
		Paths   map[string]map[string]any `json:"paths"`
	}
	if e = json.Unmarshal(openAPI, &document); e != nil || document.OpenAPI != "3.1.1" {
		t.Fatal("OpenAPI 契约无效", e)
	}
	normalize := func(path string) string { return regexp.MustCompile(`\{[^}]+\}`).ReplaceAllString(path, "{}") }
	expected := map[string]bool{}
	for _, match := range regexp.MustCompile(`HandleFunc\("(GET|POST|PUT|PATCH|DELETE) /api/v1([^"]+)"`).FindAllStringSubmatch(string(source), -1) {
		expected[strings.ToLower(match[1])+" "+normalize(match[2])] = true
	}
	for path, methods := range document.Paths {
		for method := range methods {
			key := method + " " + normalize(path)
			if !expected[key] {
				t.Fatalf("契约中的接口缺少实现: %s", key)
			}
			delete(expected, key)
		}
	}
	if len(expected) > 0 {
		t.Fatalf("接口缺少契约: %v", expected)
	}
}

func TestV1OwnershipIdempotencyAndResume(t *testing.T) {
	t.Setenv("ARCHIVE_ADMIN_PASSWORD", "test-admin-password")
	data := t.TempDir()
	c := config.Config{DataDir: data, FFmpeg: "ffmpeg", FFprobe: "ffprobe"}
	st, e := store.Open(data)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { st.DB.Close() }()
	m := media.New(c)
	app, e := New(st, m, queue.New(st, m, c), c, fstest.MapFS{})
	if e != nil {
		t.Fatal(e)
	}
	handler := app.Handler()
	var cookies []*http.Cookie
	request := func(method, path string, body []byte, bearer, key, origin string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "http://artkeep.test"+path, bytes.NewReader(body))
		if len(body) > 0 {
			req.Header.Set("Content-Type", "application/json")
		}
		if strings.Contains(path, "/chunks/") {
			req.Header.Set("Content-Type", "application/octet-stream")
		}
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		} else {
			for _, c := range cookies {
				req.AddCookie(c)
			}
		}
		if key != "" {
			req.Header.Set("Idempotency-Key", key)
		}
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	login := request("POST", "/api/v1/sessions", []byte(`{"scope":"admin","password":"test-admin-password"}`), "", "", "")
	if login.Code != 201 || login.Header().Get("Location") == "" {
		t.Fatal(login.Code, login.Body.String())
	}
	cookies = login.Result().Cookies()
	for _, path := range []string{"/api/site", "/api/admin/uploads", "/api/v1/missing"} {
		if r := request("GET", path, nil, "", "", ""); r.Code != 404 {
			t.Fatal("旧接口仍可调用", path, r.Code)
		}
	}
	if r := request("PUT", "/api/v1/works", nil, "", "", ""); r.Code != 405 || !strings.Contains(r.Header().Get("Allow"), "GET") {
		t.Fatal("方法语义错误", r.Code, r.Body.String())
	}
	if r := request("GET", "/api/v1/works?page=bad", nil, "", "", ""); r.Code != 422 || r.Header().Get("Content-Type") != "application/problem+json" {
		t.Fatal("错误契约无效", r.Code)
	}
	if r := request("PUT", "/api/v1/credentials/adult", []byte(`{"password":"adult-pass"}`), "", "", ""); r.Code != 204 {
		t.Fatal(r.Body.String())
	}
	if r := request("PATCH", "/api/v1/settings", []byte(`{"adultPasswordRequired":true}`), "", "", ""); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	if r := request("PATCH", "/api/v1/settings", []byte(`{}`), "", "", ""); !strings.Contains(r.Body.String(), `"adultPasswordRequired":true`) {
		t.Fatal("省略字段改变设置", r.Body.String())
	}
	origin := "chrome-extension://aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	token := func(name string) accessToken {
		raw, _ := json.Marshal(map[string]string{"name": name, "origin": origin})
		r := request("POST", "/api/v1/access-tokens", raw, "", "", "")
		var v accessToken
		if r.Code != 201 || json.Unmarshal(r.Body.Bytes(), &v) != nil || v.Token == "" {
			t.Fatal("创建令牌失败", r.Body.String())
		}
		return v
	}
	one, two := token("one"), token("two")
	if r := request("GET", "/api/v1/access-tokens", nil, "", "", ""); strings.Contains(r.Body.String(), one.Token) {
		t.Fatal("列表泄露令牌")
	}
	if r := request("GET", "/api/v1/settings", nil, one.Token, "", origin); r.Code != 403 {
		t.Fatal("令牌越权")
	}
	if r := request("OPTIONS", "/api/v1/uploads", nil, "", "", origin); r.Code != 204 || r.Header().Get("Access-Control-Allow-Origin") != origin {
		t.Fatal("扩展预检失败", r.Code)
	}
	if r := request("GET", "/api/v1/jobs", nil, one.Token, "", "chrome-extension://bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"); r.Code != 403 {
		t.Fatal("接受错误扩展来源")
	}
	manifest := []byte(`{"name":"root","files":[{"path":"root/large.png","size":8388613,"modified":10},{"path":"root/small.png","size":3,"modified":20}]}`)
	create := request("POST", "/api/v1/uploads", manifest, one.Token, "upload-one", origin)
	if create.Code != 201 {
		t.Fatal(create.Body.String())
	}
	replay := request("POST", "/api/v1/uploads", manifest, one.Token, "upload-one", origin)
	if replay.Code != 201 || !bytes.Equal(create.Body.Bytes(), replay.Body.Bytes()) {
		t.Fatal("创建上传重复执行")
	}
	if r := request("POST", "/api/v1/uploads", []byte(`{"name":"changed","files":[]}`), one.Token, "upload-one", origin); r.Code != 409 {
		t.Fatal("幂等冲突未拒绝")
	}
	var upload uploadSession
	json.Unmarshal(create.Body.Bytes(), &upload)
	prefix := "/api/v1/uploads/" + upload.ID
	if r := request("GET", prefix, nil, two.Token, "", origin); r.Code != 404 {
		t.Fatal("其他令牌可读取上传")
	}
	large := bytes.Repeat([]byte("a"), int(uploadChunkSize))
	if r := request("PUT", prefix+"/files/0/chunks/0", large, one.Token, "", origin); r.Code != 204 {
		t.Fatal("8MiB 上传失败", r.Code, r.Body.String())
	}
	if r := request("PUT", prefix+"/files/0/chunks/0", large, one.Token, "", origin); r.Code != 204 {
		t.Fatal("分块重放失败")
	}
	if r := request("PUT", prefix+"/files/0/chunks/0", []byte("bad"), one.Token, "", origin); r.Code != 409 || r.Header().Get("Upload-Offset") != "8388608" {
		t.Fatal("分块冲突无偏移", r.Code)
	}
	st.DB.Close()
	st, e = store.Open(data)
	if e != nil {
		t.Fatal(e)
	}
	app, e = New(st, m, queue.New(st, m, c), c, fstest.MapFS{})
	if e != nil {
		t.Fatal(e)
	}
	handler = app.Handler()
	if r := request("GET", prefix, nil, one.Token, "", origin); r.Code != 200 || !strings.Contains(r.Body.String(), `"offset":8388608`) {
		t.Fatal("重启未恢复偏移", r.Body.String())
	}
	jobBody := []byte(`{"kind":"import","source":"upload","uploadId":"` + upload.ID + `"}`)
	if r := request("POST", "/api/v1/jobs", jobBody, one.Token, "import-one", origin); r.Code != 409 {
		t.Fatal("未完成文件可以入库")
	}
	for path, data := range map[string][]byte{"/files/0/chunks/8388608": []byte("12345"), "/files/1/chunks/0": []byte("png")} {
		if r := request("PUT", prefix+path, data, one.Token, "", origin); r.Code != 204 {
			t.Fatal(r.Body.String())
		}
	}
	created := request("POST", "/api/v1/jobs", jobBody, one.Token, "import-one", origin)
	if created.Code != 202 {
		t.Fatal(created.Body.String())
	}
	replay = request("POST", "/api/v1/jobs", jobBody, one.Token, "import-one", origin)
	if replay.Code != 202 || !bytes.Equal(created.Body.Bytes(), replay.Body.Bytes()) {
		t.Fatal("任务重放重复入队")
	}
	location := created.Header().Get("Location")
	if r := request("GET", location, nil, two.Token, "", origin); r.Code != 404 {
		t.Fatal("其他令牌可读取任务")
	}
	if r := request("GET", "/api/v1/jobs", nil, two.Token, "", origin); !strings.Contains(r.Body.String(), `"total":0`) {
		t.Fatal("任务列表越权")
	}
	if r := request("PATCH", location, []byte(`{"status":"queued"}`), one.Token, "retry-one", origin); r.Code != 409 {
		t.Fatal("排队中任务可重试")
	}
	if _, e := st.DB.Exec("UPDATE jobs SET status='failed' WHERE id=?", strings.TrimPrefix(location, "/api/v1/jobs/")); e != nil {
		t.Fatal(e)
	}
	retried := request("PATCH", location, []byte(`{"status":"queued"}`), one.Token, "retry-one", origin)
	if retried.Code != 202 {
		t.Fatal(retried.Body.String())
	}
	if r := request("PATCH", location, []byte(`{"status":"queued"}`), one.Token, "retry-one", origin); r.Code != 202 || !bytes.Equal(r.Body.Bytes(), retried.Body.Bytes()) {
		t.Fatal("重试请求不幂等")
	}
	if r := request("DELETE", prefix, nil, one.Token, "", origin); r.Code != 409 {
		t.Fatal("已入队上传可以取消")
	}
	if r := request("DELETE", "/api/v1/access-tokens/"+one.ID, nil, "", "", ""); r.Code != 204 {
		t.Fatal(r.Body.String())
	}
	if r := request("GET", prefix, nil, one.Token, "", origin); r.Code != 401 {
		t.Fatal("撤销令牌仍可用")
	}
	if _, e = os.Stat(filepath.Join(data, "staging", "upload-"+upload.ID, "files", "root", "large.png")); e != nil {
		t.Fatal("数据未保留", e)
	}
}
