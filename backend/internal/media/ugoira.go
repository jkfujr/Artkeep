package media

import (
	"archive/zip"
	"collection/internal/model"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type animationFrame struct {
	File  string `json:"file"`
	Delay int    `json:"delay"`
}
type animationInfo struct {
	Frames []animationFrame `json:"frames"`
}
type ugoiraArchive struct {
	*zip.ReadCloser
	Info    animationInfo
	Entries map[string]*zip.File
}

func openUgoira(src string) (*ugoiraArchive, error) {
	z, e := zip.OpenReader(src)
	if e != nil {
		return nil, e
	}
	a := &ugoiraArchive{ReadCloser: z, Entries: map[string]*zip.File{}}
	bad := func() (*ugoiraArchive, error) { z.Close(); return nil, errors.New("动图压缩包或帧清单无效") }
	if len(z.File) > 10001 {
		return bad()
	}
	var total uint64
	for _, f := range z.File {
		name := f.Name
		total += f.UncompressedSize64
		if name == "" || strings.ContainsAny(name, "\\:\x00\r\n") || path.IsAbs(name) || path.Clean(name) != name || strings.HasPrefix(name, "../") || f.Mode()&os.ModeSymlink != 0 || f.UncompressedSize64 > 64*1024*1024 || total > 1024*1024*1024 || a.Entries[name] != nil {
			return bad()
		}
		a.Entries[name] = f
	}
	meta := a.Entries["animation.json"]
	if meta == nil || meta.UncompressedSize64 > 4*1024*1024 {
		return bad()
	}
	f, e := meta.Open()
	if e != nil {
		return bad()
	}
	e = json.NewDecoder(io.LimitReader(f, 4*1024*1024)).Decode(&a.Info)
	f.Close()
	if e != nil || len(a.Info.Frames) == 0 || len(a.Info.Frames) > 10000 {
		return bad()
	}
	seen := map[string]bool{}
	duration := 0
	for _, frame := range a.Info.Frames {
		ext := strings.ToLower(path.Ext(frame.File))
		if seen[frame.File] || a.Entries[frame.File] == nil || (ext != ".jpg" && ext != ".jpeg" && ext != ".png") || frame.Delay < 1 || frame.Delay > 60000 {
			return bad()
		}
		duration += frame.Delay
		seen[frame.File] = true
	}
	if duration > 6*60*60*1000 {
		return bad()
	}
	return a, nil
}
func inspectUgoira(a model.Asset, src string) (model.Asset, error) {
	z, e := openUgoira(src)
	if e != nil {
		return a, e
	}
	defer z.Close()
	f, e := z.Entries[z.Info.Frames[0].File].Open()
	if e != nil {
		return a, e
	}
	defer f.Close()
	cfg, _, e := image.DecodeConfig(f)
	if e != nil {
		return a, e
	}
	if int64(cfg.Width)*int64(cfg.Height) > 120000000 {
		return a, errors.New("动图帧尺寸过大")
	}
	a.Kind = "animation"
	a.Width = cfg.Width
	a.Height = cfg.Height
	for _, frame := range z.Info.Frames {
		a.Duration += float64(frame.Delay) / 1000
	}
	return a, nil
}

func (s *Service) Playback(ctx context.Context, a model.Asset) (string, error) {
	src, e := s.Resolve(a)
	if e != nil {
		return "", e
	}
	if a.Format != "ugoira" {
		if a.Kind == "video" {
			return src, nil
		}
		return "", errors.New("媒体没有视频播放资源")
	}
	name := fmt.Sprintf("%d-%d-%d-playback.mp4", a.ID, a.Size, a.Modified)
	dst := filepath.Join(s.Config.CacheDir(), name)
	s.mu.Lock()
	lock := s.pending[name]
	if lock == nil {
		lock = &sync.Mutex{}
		s.pending[name] = lock
	}
	s.mu.Unlock()
	lock.Lock()
	defer lock.Unlock()
	if st, e := os.Stat(dst); e == nil && st.Size() > 0 {
		return dst, nil
	}
	select {
	case s.sem <- struct{}{}:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	defer func() { <-s.sem }()
	z, e := openUgoira(src)
	if e != nil {
		return "", e
	}
	defer z.Close()
	dir, e := os.MkdirTemp(s.Config.CacheDir(), "ugoira-")
	if e != nil {
		return "", e
	}
	defer os.RemoveAll(dir)
	var list strings.Builder
	for i, frame := range z.Info.Frames {
		if e = ctx.Err(); e != nil {
			return "", e
		}
		name := fmt.Sprintf("frame-%06d%s", i, path.Ext(frame.File))
		in, e := z.Entries[frame.File].Open()
		if e != nil {
			return "", e
		}
		out, e := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			in.Close()
			return "", e
		}
		_, e = io.Copy(out, io.LimitReader(in, 64*1024*1024+1))
		in.Close()
		ce := out.Close()
		if e == nil {
			e = ce
		}
		if e != nil {
			return "", e
		}
		fmt.Fprintf(&list, "file '%s'\nduration %.6f\n", name, float64(frame.Delay)/1000)
		if i == len(z.Info.Frames)-1 {
			fmt.Fprintf(&list, "file '%s'\n", name)
		}
	}
	if e = os.WriteFile(filepath.Join(dir, "frames.txt"), []byte(list.String()), 0600); e != nil {
		return "", e
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	tmp := filepath.Join(dir, "playback.mp4")
	cmd := exec.CommandContext(ctx, s.Config.FFmpeg, "-v", "error", "-nostdin", "-f", "concat", "-safe", "1", "-i", filepath.Join(dir, "frames.txt"), "-vf", "pad=ceil(iw/2)*2:ceil(ih/2)*2", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-fps_mode", "vfr", "-movflags", "+faststart", "-y", tmp)
	if e = cmd.Run(); e != nil {
		return "", errors.New("动图预览生成失败，请检查 FFmpeg")
	}
	if e = os.Rename(tmp, dst); e != nil {
		return "", e
	}
	return dst, nil
}
