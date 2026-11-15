package queue

import (
	"bytes"
	"collection/internal/media"
	"collection/internal/model"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var tweetPath = regexp.MustCompile(`^/(?:[A-Za-z0-9_]+|i/web)/status/(\d+)(?:/(?:photo|video)/\d+)?/?$`)

func NormalizeX(raw string) (string, string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return "", "", errors.New("请输入有效的 X 帖子链接")
	}
	switch strings.ToLower(u.Hostname()) {
	case "x.com", "www.x.com", "twitter.com", "www.twitter.com":
	default:
		return "", "", errors.New("仅支持 X 或 Twitter 的单条帖子链接")
	}
	m := tweetPath.FindStringSubmatch(u.Path)
	if m == nil || len(m[1]) > 25 {
		return "", "", errors.New("请输入单条帖子链接")
	}
	id := m[1]
	return id, "https://x.com/i/web/status/" + id, nil
}

type limitedBuffer struct{ bytes.Buffer }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if b.Len() < 32768 {
		limit := min(len(p), 32768-b.Len())
		b.Buffer.Write(p[:limit])
	}
	return n, nil
}

func (q *Queue) downloadX(ctx context.Context, j *model.Job) (int64, error) {
	id, canonical, err := NormalizeX(j.Input)
	if err != nil {
		return 0, err
	}
	existing, _, ready, err := q.Store.Find("x", "tweet", id)
	if err != nil {
		return 0, err
	}
	if existing > 0 && ready {
		return existing, nil
	}
	stage := filepath.Join(q.Config.DataDir, "staging", "x-"+id)
	final := filepath.Join(q.Config.DownloadDir(), "x", id)
	// 先读取上次已落盘的结果，完整时直接继续入库。
	if _, err = os.Stat(filepath.Join(final, ".complete.json")); err == nil {
		return q.indexX(ctx, final, id)
	}
	if _, err = os.Stat(filepath.Join(stage, ".complete.json")); err == nil {
		if err = os.MkdirAll(filepath.Dir(final), 0700); err != nil {
			return 0, err
		}
		if err = os.Rename(stage, final); err != nil {
			return 0, errors.New("无法保存已下载作品")
		}
		return q.indexX(ctx, final, id)
	}
	if _, err = exec.LookPath(q.Config.GalleryDL); err != nil {
		return 0, errors.New("未找到 gallery-dl，请配置下载器路径")
	}
	if err = os.MkdirAll(stage, 0700); err != nil {
		return 0, err
	}
	twitter := map[string]any{"size": []string{"orig"}, "videos": true, "quoted": false, "conversations": false, "retweets": false, "retries-api": 2, "ratelimit": "abort", "text-tweets": false, "cards": false, "unavailable": true, "metadata-url": "download_url", "postprocessors": []any{map[string]any{"name": "metadata", "mode": "json", "event": []string{"file", "skip"}, "filename": "{tweet_id}_{num}.{extension}.json"}}}
	if q.Config.CookiesFile != "" {
		if _, e := os.Stat(q.Config.CookiesFile); e != nil {
			return 0, errors.New("X 登录文件不可用")
		}
		twitter["cookies"] = q.Config.CookiesFile
	}
	if q.Config.Proxy != "" {
		twitter["proxy"] = q.Config.Proxy
	}
	conf := map[string]any{"extractor": map[string]any{"base-directory": stage, "directory": []string{}, "filename": "{tweet_id}_{num}.{extension}", "retries": 2, "timeout": 30, "twitter": twitter}}
	configBytes, err := json.Marshal(conf)
	if err != nil {
		return 0, err
	}
	cfgFile, err := os.CreateTemp(q.Config.DataDir, "download-config-*.json")
	if err != nil {
		return 0, err
	}
	cfgPath := cfgFile.Name()
	if err = cfgFile.Chmod(0600); err == nil {
		_, err = cfgFile.Write(configBytes)
	}
	closeErr := cfgFile.Close()
	defer os.Remove(cfgPath)
	if err != nil {
		return 0, err
	}
	if closeErr != nil {
		return 0, closeErr
	}
	jobCtx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(jobCtx, q.Config.GalleryDL, "--config-ignore", "--config", cfgPath, "--no-input", canonical)
	var output limitedBuffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err = cmd.Run(); err != nil {
		return 0, downloadError(output.String(), jobCtx.Err())
	}
	w, assets, err := q.readX(ctx, stage, id, "staging")
	if err != nil {
		return 0, err
	}
	j.Total = len(assets)
	j.Progress = len(assets)
	if err = q.Store.UpdateJob(*j); err != nil {
		return 0, err
	}
	// 完成标记只在下载器正常结束且媒体与元数据齐全时写入。
	marker, err := json.Marshal(map[string]any{"tweetId": id, "files": len(assets), "authorId": w.AuthorID})
	if err != nil {
		return 0, err
	}
	if err = os.WriteFile(filepath.Join(stage, ".complete.json"), marker, 0600); err != nil {
		return 0, err
	}
	if err = os.MkdirAll(filepath.Dir(final), 0700); err != nil {
		return 0, err
	}
	if err = os.Rename(stage, final); err != nil {
		return 0, errors.New("无法保存已下载作品")
	}
	return q.indexX(ctx, final, id)
}

func downloadError(output string, ctxErr error) error {
	if ctxErr != nil {
		return errors.New("下载已中断或超时，可重试继续")
	}
	text := strings.ToLower(output)
	switch {
	case strings.Contains(text, "429") || strings.Contains(text, "rate limit"):
		return errors.New("X 请求受限，请稍后重试")
	case strings.Contains(text, "auth") || strings.Contains(text, "login") || strings.Contains(text, "401") || strings.Contains(text, "403") || strings.Contains(text, "cookies"):
		return errors.New("X 登录不可用，请更新服务端登录文件")
	case strings.Contains(text, "404") || strings.Contains(text, "not found") || strings.Contains(text, "unavailable"):
		return errors.New("帖子不存在或当前无法访问")
	default:
		return errors.New("下载失败，请检查网络和下载器版本后重试")
	}
}

func (q *Queue) indexX(ctx context.Context, dir, id string) (int64, error) {
	w, assets, err := q.readX(ctx, dir, id, "collection")
	if err != nil {
		return 0, err
	}
	w.Ready = false
	w.Root = "collection"
	if err = q.Store.UpsertWork(&w, assets); err != nil {
		return 0, err
	}
	for _, a := range w.Assets {
		if a.Kind != "document" {
			if _, err = q.Media.Rendition(ctx, a, "thumb"); err != nil {
				return 0, errors.New("文件已保存，封面生成失败，可重试继续")
			}
			break
		}
	}
	if err = q.Store.SetReady(w.ID, true); err != nil {
		return 0, err
	}
	return w.ID, nil
}

func (q *Queue) readX(ctx context.Context, dir, id, rootID string) (model.Work, []model.Asset, error) {
	w := model.Work{Platform: "x", SourceKind: "tweet", SourceID: id, SourceURL: "https://x.com/i/web/status/" + id, SourceTags: []string{}, Ready: false}
	assets := []model.Asset{}
	metas := []map[string]any{}
	raws := []json.RawMessage{}
	files := []string{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && media.Kind(path) != "" {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return w, nil, err
	}
	if len(files) == 0 {
		return w, nil, errors.New("帖子没有可收藏的媒体")
	}
	positions := map[int]bool{}
	expected := 0
	for _, path := range files {
		raw, e := os.ReadFile(path + ".json")
		if e != nil {
			return w, nil, errors.New("媒体元数据不完整，可重试继续")
		}
		var m map[string]any
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		if e = dec.Decode(&m); e != nil {
			return w, nil, errors.New("媒体元数据无法读取")
		}
		if stringValue(m["tweet_id"]) != id {
			return w, nil, errors.New("下载结果不属于指定帖子")
		}
		position := intValue(m["num"])
		if position <= 0 || positions[position] {
			return w, nil, errors.New("媒体顺序无法确认")
		}
		positions[position] = true
		if count := intValue(m["count"]); count > expected {
			expected = count
		}
		root := q.Config.DownloadDir()
		if rootID == "staging" {
			root = filepath.Join(q.Config.DataDir, "staging")
		}
		a, e := q.Media.Inspect(ctx, rootID, root, path, position-1)
		if e != nil {
			return w, nil, e
		}
		if a.Size == 0 {
			return w, nil, errors.New("媒体文件尚未下载完成")
		}
		if stringValue(m["type"]) == "animated_gif" {
			a.Kind = "animation"
		}
		assets = append(assets, a)
		metas = append(metas, m)
		raws = append(raws, json.RawMessage(raw))
	}
	if expected == 0 || expected != len(assets) {
		return w, nil, errors.New("帖子媒体尚未全部保存，可重试继续")
	}
	sort.Slice(assets, func(i, j int) bool { return assets[i].Position < assets[j].Position })
	m := metas[0]
	author, _ := m["author"].(map[string]any)
	if author == nil {
		return w, nil, errors.New("作者信息缺失")
	}
	w.AuthorID = stringValue(author["id"])
	if w.AuthorID == "" {
		w.AuthorID = stringValue(author["id_str"])
	}
	w.AuthorName = stringValue(author["nick"])
	if w.AuthorName == "" {
		w.AuthorName = stringValue(author["name"])
	}
	if w.AuthorID == "" {
		return w, nil, errors.New("作者 ID 缺失")
	}
	w.AuthorURL = "https://x.com/i/user/" + w.AuthorID
	w.Description = stringValue(m["content"])
	lines := strings.Split(w.Description, "\n")
	runes := []rune(strings.TrimSpace(lines[0]))
	if len(runes) > 80 {
		runes = runes[:80]
	}
	w.Title = string(runes)
	if w.Title == "" {
		w.Title = w.AuthorName + "的作品"
	}
	date := stringValue(m["date"])
	var published time.Time
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05", "2006-01-02T15:04:05"} {
		if parsed, e := time.Parse(layout, date); e == nil {
			published = parsed
			break
		}
	}
	if published.IsZero() {
		return w, nil, errors.New("帖子发布时间缺失")
	}
	w.PublishedAt = published.UTC().Format(time.RFC3339)
	for _, m := range metas {
		if flag, ok := m["sensitive"].(bool); ok && flag {
			w.SourceRating = 1
		}
		if flags, ok := m["sensitive_flags"].([]any); ok && len(flags) > 0 {
			w.SourceRating = 1
		}
		if tags, ok := m["hashtags"].([]any); ok {
			for _, tag := range tags {
				value := stringValue(tag)
				if value != "" && !contains(w.SourceTags, value) {
					w.SourceTags = append(w.SourceTags, value)
				}
			}
		}
	}
	raw, err := json.Marshal(raws)
	if err != nil {
		return w, nil, err
	}
	w.Raw = string(raw)
	w.MetadataPath = filepath.ToSlash(filepath.Join("x", id))
	w.Fingerprint = fmt.Sprintf("x:%s:%d", id, len(assets))
	return w, assets, nil
}

func stringValue(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case json.Number:
		return x.String()
	}
	return ""
}
func intValue(v any) int { n, _ := strconv.Atoi(stringValue(v)); return n }
func contains(xs []string, v string) bool {
	for _, s := range xs {
		if s == v {
			return true
		}
	}
	return false
}
