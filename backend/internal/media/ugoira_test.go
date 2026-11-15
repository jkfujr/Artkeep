package media

import (
	"archive/zip"
	"bytes"
	"collection/internal/config"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestUgoiraPlaybackAndArchiveValidation(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{DataDir: root, FFmpeg: "ffmpeg", FFprobe: "ffprobe"}
	if e := os.MkdirAll(cfg.DownloadDir(), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.MkdirAll(cfg.CacheDir(), 0700); e != nil {
		t.Fatal(e)
	}
	makeZip := func(name string, unsafe bool) string {
		var buf bytes.Buffer
		z := zip.NewWriter(&buf)
		for i, name := range []string{"000000.png", "000001.png"} {
			f, e := z.Create(name)
			if e != nil {
				t.Fatal(e)
			}
			img := image.NewRGBA(image.Rect(0, 0, 16, 12))
			img.Set(0, 0, color.RGBA{R: uint8(100 + i*50), A: 255})
			if e = png.Encode(f, img); e != nil {
				t.Fatal(e)
			}
		}
		f, e := z.Create("animation.json")
		if e != nil {
			t.Fatal(e)
		}
		f.Write([]byte(`{"frames":[{"file":"000000.png","delay":100},{"file":"000001.png","delay":200}]}`))
		if unsafe {
			z.Create("../escape.png")
		}
		if e = z.Close(); e != nil {
			t.Fatal(e)
		}
		path := filepath.Join(cfg.DownloadDir(), name)
		if e = os.WriteFile(path, buf.Bytes(), 0600); e != nil {
			t.Fatal(e)
		}
		return path
	}
	path := makeZip("123.ugoira", false)
	service := New(cfg)
	asset, e := service.Inspect(context.Background(), "collection", cfg.DownloadDir(), path, 0)
	if e != nil {
		t.Fatal(e)
	}
	if asset.Kind != "animation" || asset.Width != 16 || asset.Height != 12 || asset.Duration < 0.29 {
		t.Fatalf("动图信息错误: %+v", asset)
	}
	if _, e = openUgoira(makeZip("bad.ugoira", true)); e == nil {
		t.Fatal("接受危险压缩包路径")
	}
	if _, e = exec.LookPath("ffmpeg"); e != nil {
		t.Skip("需要 FFmpeg")
	}
	if _, e = exec.LookPath("ffprobe"); e != nil {
		t.Skip("需要 FFprobe")
	}
	asset.ID = 1
	playback, e := service.Playback(context.Background(), asset)
	if e != nil {
		t.Fatal(e)
	}
	if filepath.Ext(playback) != ".mp4" {
		t.Fatal("播放资源格式错误")
	}
	if _, e = service.Rendition(context.Background(), asset, "thumb"); e != nil {
		t.Fatal(e)
	}
	if original, e := os.ReadFile(path); e != nil || len(original) == 0 {
		t.Fatal("原始动图未保留")
	}
}
