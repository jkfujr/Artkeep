package media

import (
	"bytes"
	"collection/internal/config"
	"collection/internal/model"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/disintegration/imaging"
	_ "golang.org/x/image/webp"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Service struct {
	Config  config.Config
	sem     chan struct{}
	mu      sync.Mutex
	pending map[string]*sync.Mutex
}

func New(c config.Config) *Service {
	return &Service{Config: c, sem: make(chan struct{}, 2), pending: map[string]*sync.Mutex{}}
}

func Kind(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg", ".png", ".webp":
		return "image"
	case ".apng", ".gif":
		return "animation"
	case ".mp4", ".webm", ".mov":
		return "video"
	case ".epub":
		return "document"
	}
	return ""
}

func SafePath(root, rel string) (string, error) {
	if root == "" || rel == "" || filepath.IsAbs(rel) {
		return "", errors.New("文件不可用")
	}
	clean := filepath.Clean(filepath.FromSlash(rel))
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errors.New("文件不可用")
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	realPath, err := filepath.EvalSymlinks(filepath.Join(realRoot, clean))
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(realRoot, realPath)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("文件不可用")
	}
	return realPath, nil
}
func (s *Service) Resolve(a model.Asset) (string, error) {
	root, ok := s.Config.Roots()[a.Root]
	if !ok {
		return "", errors.New("目录不可用")
	}
	return SafePath(root, a.Path)
}

func (s *Service) Inspect(ctx context.Context, rootID, root, path string, position int) (model.Asset, error) {
	a := model.Asset{Root: rootID, Position: position, Kind: Kind(path), Format: strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), "."), Available: true}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return a, err
	}
	a.Path = filepath.ToSlash(rel)
	path, err = SafePath(root, a.Path)
	if err != nil {
		return a, err
	}
	stat, err := os.Stat(path)
	if err != nil {
		return a, err
	}
	a.Size = stat.Size()
	a.Modified = stat.ModTime().UnixNano()
	if a.Kind == "document" {
		a.Width = 600
		a.Height = 800
		return a, nil
	}
	if a.Kind == "video" {
		return s.inspectVideo(ctx, a, path)
	}
	f, err := os.Open(path)
	if err != nil {
		return a, err
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return a, fmt.Errorf("无法读取图片尺寸: %w", err)
	}
	a.Width = cfg.Width
	a.Height = cfg.Height
	if a.Format == "png" {
		head := make([]byte, 128*1024)
		n, _ := f.ReadAt(head, 0)
		if bytes.Contains(head[:n], []byte("acTL")) {
			a.Kind = "animation"
		}
	}
	return a, nil
}

func (s *Service) inspectVideo(ctx context.Context, a model.Asset, path string) (model.Asset, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, s.Config.FFprobe, "-v", "error", "-select_streams", "v:0", "-show_entries", "stream=width,height:format=duration", "-of", "json", path)
	b, err := cmd.Output()
	if err != nil {
		return a, errors.New("无法读取视频，请检查 FFprobe")
	}
	var probe struct {
		Streams []struct {
			Width  int `json:"width"`
			Height int `json:"height"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err = json.Unmarshal(b, &probe); err != nil || len(probe.Streams) == 0 {
		return a, errors.New("视频没有可读取的画面")
	}
	a.Width = probe.Streams[0].Width
	a.Height = probe.Streams[0].Height
	a.Duration, _ = strconv.ParseFloat(probe.Format.Duration, 64)
	return a, nil
}

func (s *Service) Rendition(ctx context.Context, a model.Asset, variant string) (string, error) {
	maxDim := 480
	if variant == "preview" {
		maxDim = 1920
	} else if variant != "thumb" {
		return "", errors.New("预览类型无效")
	}
	name := fmt.Sprintf("%d-%d-%d-%s.jpg", a.ID, a.Size, a.Modified, variant)
	dst := filepath.Join(s.Config.CacheDir(), name)
	if stat, err := os.Stat(dst); err == nil && stat.Size() > 0 {
		return dst, nil
	}
	s.mu.Lock()
	lock := s.pending[name]
	if lock == nil {
		lock = &sync.Mutex{}
		s.pending[name] = lock
	}
	s.mu.Unlock()
	lock.Lock()
	defer lock.Unlock()
	if stat, err := os.Stat(dst); err == nil && stat.Size() > 0 {
		return dst, nil
	}
	select {
	case s.sem <- struct{}{}:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	defer func() { <-s.sem }()
	src, err := s.Resolve(a)
	if err != nil {
		return "", err
	}
	temp, err := os.CreateTemp(s.Config.CacheDir(), "preview-*.jpg")
	if err != nil {
		return "", err
	}
	tmp := temp.Name()
	temp.Close()
	defer os.Remove(tmp)
	if a.Kind == "video" || a.Kind == "animation" {
		ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		filter := fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease", maxDim, maxDim)
		cmd := exec.CommandContext(ctx, s.Config.FFmpeg, "-v", "error", "-nostdin", "-y", "-i", src, "-frames:v", "1", "-vf", filter, "-q:v", "3", tmp)
		if err = cmd.Run(); err != nil {
			return "", errors.New("无法生成媒体封面，请检查 FFmpeg")
		}
	} else {
		if int64(a.Width)*int64(a.Height) > 120_000_000 {
			return "", errors.New("图片尺寸超出预览范围")
		}
		img, e := imaging.Open(src, imaging.AutoOrientation(true))
		if e != nil {
			return "", e
		}
		img = imaging.Fit(img, maxDim, maxDim, imaging.Lanczos)
		canvas := image.NewRGBA(img.Bounds())
		draw.Draw(canvas, canvas.Bounds(), &image.Uniform{C: color.White}, image.Point{}, draw.Src)
		draw.Draw(canvas, canvas.Bounds(), img, img.Bounds().Min, draw.Over)
		out, e := os.OpenFile(tmp, os.O_WRONLY|os.O_TRUNC, 0600)
		if e != nil {
			return "", e
		}
		e = jpeg.Encode(out, canvas, &jpeg.Options{Quality: 85})
		closeErr := out.Close()
		if e != nil {
			return "", e
		}
		if closeErr != nil {
			return "", closeErr
		}
	}
	if err = os.Rename(tmp, dst); err != nil {
		if _, e := os.Stat(dst); e != nil {
			return "", err
		}
	}
	return dst, nil
}
