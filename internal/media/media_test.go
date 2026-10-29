package media

import (
	"collection/internal/config"
	"context"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestVideoInspectionAndPoster(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("需要 FFmpeg")
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("需要 FFprobe")
	}
	root := t.TempDir()
	path := filepath.Join(root, "video.mp4")
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, ffmpeg, "-v", "error", "-nostdin", "-f", "lavfi", "-i", "color=c=0x376bee:s=160x100:d=0.3", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-y", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("生成测试视频失败: %v %s", err, out)
	}
	c := config.Config{DataDir: root, PixivDir: root, FFmpeg: ffmpeg, FFprobe: ffprobe}
	if err = os.MkdirAll(c.CacheDir(), 0700); err != nil {
		t.Fatal(err)
	}
	service := New(c)
	a, err := service.Inspect(ctx, "pixiv", root, path, 0)
	if err != nil {
		t.Fatal(err)
	}
	a.ID = 1
	if a.Kind != "video" || a.Width != 160 || a.Height != 100 || a.Duration <= 0 {
		t.Fatalf("视频信息错误: %+v", a)
	}
	poster, err := service.Rendition(ctx, a, "thumb")
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(poster)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err = jpeg.DecodeConfig(f); err != nil {
		t.Fatalf("封面不是有效图片: %v", err)
	}
	if _, err = SafePath(root, "../outside.mp4"); err == nil {
		t.Fatal("允许读取目录以外的文件")
	}
}
