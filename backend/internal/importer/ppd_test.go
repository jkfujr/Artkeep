package importer

import (
	"collection/internal/config"
	"collection/internal/media"
	"context"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestPPDJSONPriorityAndAttachments(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "pixiv", "42-作者", "123-作品")
	if e := os.MkdirAll(dir, 0700); e != nil {
		t.Fatal(e)
	}
	raw := `{"idNum":123,"id":"123_p0","userId":"42","user":"作者","title":"JSON 标题","description":"第一行<br /><a href=\"https://example.com\">链接</a>","date":"2026-11-11T17:08:00+00:00","type":0,"xRestrict":1,"tags":["R-18","测试"],"pageCount":5}`
	for name, data := range map[string]string{"123-meta.json": raw, "123-meta.txt": "无效 TXT", "123-简介-links.txt": "原始简介"} {
		if e := os.WriteFile(filepath.Join(dir, name), []byte(data), 0600); e != nil {
			t.Fatal(e)
		}
	}
	for _, name := range []string{"123_p0.png", "123_p1.png", "123_p2.png", "123_p3.png", "123_p4.png"} {
		f, e := os.Create(filepath.Join(dir, name))
		if e != nil {
			t.Fatal(e)
		}
		if e = png.Encode(f, image.NewRGBA(image.Rect(0, 0, 2, 3))); e != nil {
			t.Fatal(e)
		}
		f.Close()
	}
	c := Collection{Config: config.Config{DataDir: filepath.Join(root, "data")}, Media: media.New(config.Config{})}
	works, e := c.Discover(context.Background(), root, 0)
	if e != nil {
		t.Fatal(e)
	}
	if len(works) != 1 || len(works[0].Files) != 5 || len(works[0].Metadata) != 3 || works[0].Work.Title != "JSON 标题" || works[0].Work.SourceRating != 1 || works[0].Work.SourceID != "123" || works[0].Error != "" {
		t.Fatalf("归组或元数据错误: %+v", works)
	}
	if filepath.Base(works[0].Files[4]) != "123_p4.png" {
		t.Fatal("页序错误")
	}
}
