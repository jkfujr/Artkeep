package queue

import (
	"collection/internal/importer"
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var pixivArtworkPath = regexp.MustCompile(`^/(?:en/)?artworks/(\d+)/?$`)
var pixivShortNovel = regexp.MustCompile(`^/n/(\d+)/?$`)

func NormalizeURL(raw string) (platform, kind, id, canonical string, err error) {
	u, e := url.Parse(strings.TrimSpace(raw))
	if e != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" {
		err = errors.New("请输入 Pixiv 作品、小说或 X 帖子链接")
		return
	}
	switch strings.ToLower(u.Hostname()) {
	case "pixiv.net", "www.pixiv.net", "touch.pixiv.net":
		platform = "pixiv"
		kind = "artwork"
		if m := pixivArtworkPath.FindStringSubmatch(u.Path); m != nil {
			id = m[1]
		} else if m := pixivShortNovel.FindStringSubmatch(u.Path); m != nil {
			kind = "novel"
			id = m[1]
		} else if u.Path == "/novel/show.php" {
			kind = "novel"
			id = u.Query().Get("id")
		} else {
			err = errors.New("仅支持 Pixiv 单部插画、动图或小说链接")
			return
		}
		if len(id) > 25 || id == "" || regexp.MustCompile(`[^0-9]`).MatchString(id) {
			err = errors.New("作品 ID 无效")
			return
		}
		canonical = "https://www.pixiv.net/artworks/" + id
		if kind == "novel" {
			canonical = "https://www.pixiv.net/novel/show.php?id=" + id
		}
	default:
		platform = "x"
		kind = "tweet"
		id, canonical, err = NormalizeX(raw)
	}
	return
}

func (q *Queue) downloadURL(ctx context.Context, jobID int64, raw string, rating int, progress func(int64)) (int64, bool, error) {
	platform, kind, id, canonical, err := NormalizeURL(raw)
	if err != nil {
		return 0, false, err
	}
	existing, _, ready, err := q.Store.Find(platform, kind, id)
	if err != nil {
		return 0, false, err
	}
	if existing > 0 && ready {
		err = q.collection().Migrate(ctx, existing, progress)
		return existing, true, err
	}
	if platform == "pixiv" && q.Config.PixivRefreshToken == "" {
		return 0, false, errors.New("未配置 Pixiv 登录，请设置服务端 pixiv_refresh_token")
	}
	if _, err = exec.LookPath(q.Config.GalleryDL); err != nil {
		return 0, false, errors.New("未找到 gallery-dl")
	}
	stage := filepath.Join(q.Config.DataDir, "staging", "url-"+platform+"-"+kind+"-"+id)
	if err = os.MkdirAll(stage, 0700); err != nil {
		return 0, false, err
	}
	filename := "{id}_p{num}.{extension}"
	pixiv := map[string]any{"refresh-token": q.Config.PixivRefreshToken, "ugoira": true, "metadata": false, "related": false, "max-posts": 1, "postprocessors": []any{map[string]any{"name": "ugoira", "extension": "mp4", "ffmpeg-location": q.Config.FFmpeg, "ffmpeg-args": []string{"-c:v", "libx264", "-pix_fmt", "yuv420p", "-movflags", "+faststart"}}, map[string]any{"name": "metadata", "mode": "json", "event": []string{"file", "skip"}, "filename": "{id}_p{num}.{extension}.json"}}}
	novel := map[string]any{"refresh-token": q.Config.PixivRefreshToken, "full-series": false, "embeds": true, "covers": true, "max-posts": 1, "postprocessors": []any{map[string]any{"name": "metadata", "mode": "json", "event": []string{"file", "skip"}, "filename": "{id}_p{num}.{extension}.json"}}}
	twitter := map[string]any{"size": []string{"orig"}, "videos": true, "quoted": false, "conversations": false, "retweets": false, "retries-api": 2, "ratelimit": "abort", "text-tweets": false, "cards": false, "unavailable": true, "metadata-url": "download_url", "filename": "{tweet_id}_{num}.{extension}", "postprocessors": []any{map[string]any{"name": "metadata", "mode": "json", "event": []string{"file", "skip"}, "filename": "{tweet_id}_{num}.{extension}.json"}}}
	if q.Config.CookiesFile != "" {
		if _, e := os.Stat(q.Config.CookiesFile); e != nil {
			return 0, false, errors.New("X 登录文件不可用")
		}
		twitter["cookies"] = q.Config.CookiesFile
	}
	extractor := map[string]any{"base-directory": stage, "directory": []string{}, "filename": filename, "retries": 2, "timeout": 30, "pixiv": pixiv, "pixiv-novel": novel, "twitter": twitter}
	if q.Config.Proxy != "" {
		extractor["proxy"] = q.Config.Proxy
	}
	cfg, err := os.CreateTemp(q.Config.DataDir, "download-config-*.json")
	if err != nil {
		return 0, false, err
	}
	cfgPath := cfg.Name()
	defer os.Remove(cfgPath)
	if err = cfg.Chmod(0600); err == nil {
		err = json.NewEncoder(cfg).Encode(map[string]any{"extractor": extractor, "cache": map[string]any{"file": nil}})
	}
	closeErr := cfg.Close()
	if err != nil {
		return 0, false, err
	}
	if closeErr != nil {
		return 0, false, closeErr
	}
	jobCtx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(jobCtx, q.Config.GalleryDL, "--config-ignore", "--config", cfgPath, "--no-input", canonical)
	var output limitedBuffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	q.Store.Transfer(jobID, "downloading", 0, 0)
	if err = cmd.Run(); err != nil {
		if platform == "pixiv" {
			return 0, false, errors.New("Pixiv 下载失败，请检查登录配置与网络")
		}
		return 0, false, downloadError(output.String(), jobCtx.Err())
	}
	var candidate importer.Candidate
	if platform == "x" {
		w, assets, e := q.readX(ctx, stage, id, "staging")
		if e != nil {
			return 0, false, e
		}
		candidate.Work = w
		candidate.Raw = w.Raw
		known := false
		for _, a := range assets {
			candidate.Files = append(candidate.Files, filepath.Join(q.Config.DataDir, "staging", filepath.FromSlash(a.Path)))
			candidate.Metadata = append(candidate.Metadata, candidate.Files[len(candidate.Files)-1]+".json")
			if b, e := os.ReadFile(candidate.Metadata[len(candidate.Metadata)-1]); e == nil && importer.SourceRatingKnown(b) {
				known = true
			}
		}
		if !known {
			candidate.Work.SourceRating = rating
		}
	} else if kind == "novel" {
		candidate, err = importer.DownloadedNovel(stage, id)
		if err != nil {
			return 0, false, err
		}
	} else {
		candidates, e := q.collection().Discover(ctx, stage, 0)
		if e != nil {
			return 0, false, e
		}
		found := false
		for _, v := range candidates {
			if v.Work.Platform == platform && v.Work.SourceKind == kind && v.Work.SourceID == id {
				candidate = v
				found = true
				break
			}
		}
		if !found {
			return 0, false, errors.New("下载结果缺少作品元数据")
		}
		if err = importer.ValidateDownloaded(candidate); err != nil {
			return 0, false, err
		}
	}
	q.Store.Transfer(jobID, "copying", 0, 0)
	result, exists, err := q.collection().Import(ctx, candidate, progress)
	if err == nil {
		os.RemoveAll(stage)
	}
	return result, exists, err
}
