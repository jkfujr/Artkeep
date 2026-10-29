package importer

import (
	"collection/internal/config"
	"collection/internal/media"
	"collection/internal/model"
	"collection/internal/store"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestPixivScanGroupsPagesAndPreservesEdits(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "pixiv")
	author := filepath.Join(root, "42 -作者")
	if err := os.MkdirAll(author, 0700); err != nil {
		t.Fatal(err)
	}
	raw := "ID\n123\n\nURL\nhttps://www.pixiv.net/i/123\n\nOriginal\nhttps://i.pximg.net/123_p0.png\n\nThumbnail\nhttps://i.pximg.net/thumbnail.jpg\n\nxRestrict\nR-18\n\nAI\nNo\n\nUser\n作者\n\nUserID\n42\n\nTitle\n测试作品\n\nDescription\n第一段\n\n第二段\n\nTags\n#风景\n#blue sky\n\nSize\n16 x 12\n\nBookmark\n1\n\nDate\n2026-01-01T08:00:00+08:00\n\n"
	meta := filepath.Join(author, "123-meta.txt")
	if err := os.WriteFile(meta, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"123_p10.png", "123_p2.png", "123_p0.png"} {
		f, err := os.Create(filepath.Join(author, name))
		if err != nil {
			t.Fatal(err)
		}
		err = png.Encode(f, image.NewRGBA(image.Rect(0, 0, 16, 12)))
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	c := config.Config{DataDir: dir, PixivDir: root}
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.DB.Close()
	scanner := Pixiv{Store: st, Media: media.New(c), Root: root}
	progress := func(int, int, string) {}
	if count, err := scanner.Scan(t.Context(), progress); err != nil || count != 1 {
		t.Fatalf("导入失败: %d %v", count, err)
	}
	works, total, err := st.List(model.Filter{Adult: true, Page: 1, PageSize: 48})
	if err != nil || total != 1 {
		t.Fatal("作品未归组", err)
	}
	work, err := st.Work(works[0].ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if work.Description != "第一段\n\n第二段" || !slices.Contains(work.SourceTags, "blue sky") || work.PublishedAt != "2026-01-01T00:00:00Z" {
		t.Fatalf("元数据解析错误: %+v", work)
	}
	for i, position := range []int{0, 2, 10} {
		if work.Assets[i].Position != position {
			t.Fatal("页码没有按数字排序")
		}
	}
	rating := 0
	if err = st.EditWork(work.ID, []string{"本地标签"}, &rating); err != nil {
		t.Fatal(err)
	}
	if _, err = scanner.Scan(t.Context(), progress); err != nil {
		t.Fatal(err)
	}
	again, err := st.Work(work.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if again.Rating != 0 || len(again.LocalTags) != 1 || again.Assets[0].ID != work.Assets[0].ID {
		t.Fatal("重复扫描丢失编辑或重复建文件")
	}
	if err = os.Remove(filepath.Join(author, "123_p2.png")); err != nil {
		t.Fatal(err)
	}
	if _, err = scanner.Scan(t.Context(), progress); err != nil {
		t.Fatal(err)
	}
	again, err = st.Work(work.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if again.Assets[1].Available || !again.Available || again.LocalTags[0] != "本地标签" {
		t.Fatal("缺失文件未正确标记")
	}
	if err = os.Remove(meta); err != nil {
		t.Fatal(err)
	}
	if _, err = scanner.Scan(t.Context(), progress); err == nil {
		t.Fatal("空目录扫描被计为成功")
	}
}
