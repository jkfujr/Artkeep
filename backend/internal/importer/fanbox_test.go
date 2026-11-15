package importer

import (
	"collection/internal/config"
	"os"
	"path/filepath"
	"testing"
)

func TestFanboxDirectoryMetadataAndOrdering(t *testing.T) {
	root := t.TempDir()
	author := filepath.Join(root, "5786317-マヤタカ")
	post := filepath.Join(author, "2026-08-07-４点。-差分")
	for _, name := range []string{"10.jpeg", "2.jpeg", "0.png"} {
		if err := os.MkdirAll(post, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(post, name), []byte("image"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"不正な名前", "2026-02-30-日付", "2026-08-08-空"} {
		if err := os.MkdirAll(filepath.Join(author, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	c := Collection{Config: config.Config{DataDir: filepath.Join(root, "data")}}
	for _, selected := range []string{root, author} {
		items, err := c.DiscoverFanbox(t.Context(), selected, 1)
		if err != nil || len(items) != 4 {
			t.Fatalf("目录解析失败: %v %+v", err, items)
		}
		valid, failed := 0, 0
		for _, item := range items {
			if item.Error != "" {
				failed++
				continue
			}
			valid++
			w := item.Work
			if w.Platform != "fanbox" || w.SourceKind != "post" || len(w.SourceID) != 32 || w.AuthorID != "5786317" || w.AuthorName != "マヤタカ" || w.Title != "４点。-差分" || w.PublishedAt != "2026-08-07" || w.SourceRating != 1 || w.SourceURL != "" || w.AuthorURL != "" || len(w.SourceTags) != 0 {
				t.Fatalf("目录元数据错误: %+v", w)
			}
			if len(item.Files) != 3 || filepath.Base(item.Files[0]) != "0.png" || filepath.Base(item.Files[1]) != "2.jpeg" || filepath.Base(item.Files[2]) != "10.jpeg" {
				t.Fatalf("图片自然排序错误: %v", item.Files)
			}
		}
		if valid != 1 || failed != 3 {
			t.Fatalf("错误目录影响正常作品: %d %d", valid, failed)
		}
	}
	auto, err := c.Discover(t.Context(), root, 0)
	if err != nil || len(auto) != 1 || auto[0].Work.Platform != "local" {
		t.Fatal("自动识别被改为 FANBOX", err)
	}
}
