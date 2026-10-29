package importer

import (
	"archive/zip"
	"collection/internal/config"
	"collection/internal/media"
	"collection/internal/model"
	"collection/internal/store"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
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

func TestCollectionCopyDedupAndMigration(t *testing.T) {
	base := t.TempDir()
	data := filepath.Join(base, "data")
	source := filepath.Join(base, "source")
	if err := os.MkdirAll(filepath.Join(data, "staging"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(source, 0700); err != nil {
		t.Fatal(err)
	}
	c := config.Config{DataDir: data, PixivDir: source}
	st, err := store.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	defer st.DB.Close()
	m := media.New(c)
	collection := Collection{Store: st, Media: m, Config: c}
	write := func(name string, width int) string {
		path := filepath.Join(source, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		err = png.Encode(f, image.NewRGBA(image.Rect(0, 0, width, 12)))
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
		return path
	}
	second := write("album/2.png", 16)
	tenth := write("album/10.png", 20)
	candidates, err := collection.Discover(t.Context(), source, 1)
	if err != nil || len(candidates) != 1 {
		t.Fatalf("本地归组失败: %v %+v", err, candidates)
	}
	if candidates[0].Files[0] != second || candidates[0].Files[1] != tenth {
		t.Fatal("自然排序错误")
	}
	id, exists, err := collection.Import(t.Context(), candidates[0], nil)
	if err != nil || exists {
		t.Fatalf("本地导入失败: %v", err)
	}
	w, err := st.Work(id, true)
	if err != nil {
		t.Fatal(err)
	}
	if w.Platform != "local" || w.SourceURL != "" || w.AuthorName != "" || w.PublishedAt != "" || w.Rating != 1 {
		t.Fatalf("本地来源被虚构: %+v", w)
	}
	again, exists, err := collection.Import(t.Context(), candidates[0], nil)
	if err != nil || !exists || again != id {
		t.Fatalf("本地内容去重失败: %d %v", again, err)
	}
	if err = os.RemoveAll(filepath.Join(source, "album")); err != nil {
		t.Fatal(err)
	}
	for _, a := range w.Assets {
		if _, err = m.Resolve(a); err != nil {
			t.Fatal("导入仍依赖原目录", err)
		}
	}
	legacy := write("123.png", 16)
	a, err := m.Inspect(t.Context(), "pixiv", source, legacy, 0)
	if err != nil {
		t.Fatal(err)
	}
	original := model.Work{Platform: "pixiv", SourceKind: "artwork", SourceID: "123", AuthorID: "42", AuthorName: "作者", Title: "原作", SourceRating: 2, Ready: true, Root: "pixiv", Raw: `{"id":123}`}
	if err = st.UpsertWork(&original, []model.Asset{a}); err != nil {
		t.Fatal(err)
	}
	override := 1
	if err = st.EditWork(original.ID, []string{"人工标签"}, &override); err != nil {
		t.Fatal(err)
	}
	before, _ := st.Work(original.ID, true)
	if err = collection.Migrate(t.Context(), original.ID, nil); err != nil {
		t.Fatal(err)
	}
	after, err := st.Work(original.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if before.ID != after.ID || before.Assets[0].ID != after.Assets[0].ID || before.AddedAt != after.AddedAt || after.Rating != 1 || after.LocalTags[0] != "人工标签" || after.Root != "collection" {
		t.Fatalf("迁移改变人工信息或 ID: %+v", after)
	}
	copied, err := m.Resolve(after.Assets[0])
	if err != nil || !Within(data, copied) {
		t.Fatal("迁移路径未进入 data", err)
	}
	if _, err = os.Stat(legacy); err != nil {
		t.Fatal("来源被删除")
	}
	if err = collection.Migrate(t.Context(), original.ID, func(int64) { t.Fatal("已完成作品被再次复制") }); err != nil {
		t.Fatal(err)
	}
	novel := original
	novel.ID = 0
	novel.SourceKind = "novel"
	if err = st.UpsertWork(&novel, []model.Asset{a}); err != nil {
		t.Fatal(err)
	}
	if novel.ID == original.ID {
		t.Fatal("插画与小说 ID 冲突")
	}
	missing := original
	missing.ID = 0
	missing.SourceID = "999"
	a.Path = "missing.png"
	if err = st.UpsertWork(&missing, []model.Asset{a}); err != nil {
		t.Fatal(err)
	}
	if err = collection.Migrate(t.Context(), missing.ID, nil); err == nil {
		t.Fatal("缺失来源被计为迁移成功")
	}
	unchanged, _ := st.Work(missing.ID, true)
	if unchanged.Root != "pixiv" || unchanged.Assets[0].Path != "missing.png" {
		t.Fatal("失败迁移改写了原路径")
	}
	replacement := write("another/123_p0.png", 16)
	candidate := Candidate{Work: original, Files: []string{replacement}, Raw: `{}`}
	found, exists, err := collection.Import(t.Context(), candidate, nil)
	if err != nil || !exists || found != original.ID {
		t.Fatalf("平台去重失败: %v", err)
	}
	ids, err := st.MigrationIDs()
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(ids, original.ID) || !slices.Contains(ids, missing.ID) {
		t.Fatal("迁移重试未跳过已完成作品")
	}
}

func TestFolderMetadataAndNovelEPUB(t *testing.T) {
	base := t.TempDir()
	source := filepath.Join(base, "source")
	data := filepath.Join(base, "data")
	for _, dir := range []string{source, filepath.Join(data, "staging")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	imagePath := filepath.Join(source, "123_p0.png")
	f, err := os.Create(imagePath)
	if err != nil {
		t.Fatal(err)
	}
	err = png.Encode(f, image.NewRGBA(image.Rect(0, 0, 16, 12)))
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	meta := map[string]any{"category": "pixiv", "id": 123, "user": map[string]any{"id": 42, "name": "作者"}, "title": "测试", "x_restrict": 2, "page_count": 1, "tags": []string{"标签"}, "date": "2026-01-01 00:00:00"}
	raw, _ := json.Marshal(meta)
	if err = os.WriteFile(imagePath+".json", raw, 0600); err != nil {
		t.Fatal(err)
	}
	collection := Collection{Config: config.Config{DataDir: data}}
	candidates, err := collection.Discover(t.Context(), source, 0)
	if err != nil || len(candidates) != 1 || candidates[0].Work.Platform != "pixiv" || candidates[0].Work.SourceRating != 2 || len(candidates[0].Files) != 1 {
		t.Fatalf("JSON 未关联媒体: %+v %v", candidates, err)
	}
	if _, err = Directory(data, data); err == nil {
		t.Fatal("允许导入数据目录")
	}
	epub := filepath.Join(base, "novel.epub")
	body := "[chapter:序章]\n正文 [[rb:漢字 > かんじ]]\n[newpage]\n[pixivimage:123-1]\n<script>alert(1)</script>"
	if err = WriteNovelEPUB(epub, "标题", "作者", body, map[string]string{"pixivimage:123-1": imagePath}); err != nil {
		t.Fatal(err)
	}
	book, err := zip.OpenReader(epub)
	if err != nil {
		t.Fatal(err)
	}
	defer book.Close()
	var chapters, images int
	for _, entry := range book.File {
		if strings.Contains(entry.Name, "images/") {
			images++
		}
		if strings.HasPrefix(entry.Name, "OEBPS/chapter-") {
			chapters++
			reader, e := entry.Open()
			if e != nil {
				t.Fatal(e)
			}
			content, e := io.ReadAll(reader)
			reader.Close()
			if e != nil {
				t.Fatal(e)
			}
			if strings.Contains(string(content), "<script>") {
				t.Fatal("正文标记未转义")
			}
		}
	}
	if chapters != 2 || images != 1 {
		t.Fatalf("章节或插图丢失: %d %d", chapters, images)
	}
	if err = WriteNovelEPUB(filepath.Join(base, "missing.epub"), "标题", "作者", body, nil); err == nil {
		t.Fatal("缺失插图被当成完整小说")
	}
}
