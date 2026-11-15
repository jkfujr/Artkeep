package queue

import (
	"collection/internal/config"
	"collection/internal/media"
	"collection/internal/model"
	"collection/internal/store"
	"encoding/json"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestFanboxBatchRetryAndRepeatedImport(t *testing.T) {
	base := t.TempDir()
	c := config.Config{DataDir: filepath.Join(base, "data")}
	if err := os.MkdirAll(filepath.Join(c.DataDir, "staging"), 0700); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(c.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.DB.Close()
	q := New(st, media.New(c), c)
	root := filepath.Join(base, "fanbox")
	good := filepath.Join(root, "42-作者", "2026-01-01-正常", "0.png")
	bad := filepath.Join(root, "42-作者", "2026-01-02-修復", "0.png")
	write := func(path string, valid bool) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		if valid {
			err = png.Encode(f, image.NewRGBA(image.Rect(0, 0, 12, 16)))
		} else {
			_, err = f.WriteString("broken")
		}
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	write(good, true)
	write(bad, false)
	input := BatchInput{Source: "server", Path: root, Rating: 1, Format: "fanbox"}
	raw, _ := json.Marshal(input)
	j, err := q.Enqueue("batch", string(raw), string(raw))
	if err != nil {
		t.Fatal(err)
	}
	if err = q.batch(t.Context(), &j); err == nil {
		t.Fatal("损坏图片未报告失败")
	}
	j.Status = "partial"
	if err = st.UpdateJob(j); err != nil {
		t.Fatal(err)
	}
	first, err := st.Job(j.ID)
	if err != nil || first.Succeeded != 1 || first.Failed != 1 {
		t.Fatalf("正常作品未继续导入: %v %+v", err, first)
	}
	goodID := *first.Items[0].WorkID
	failedPayload := first.Items[1].Payload
	write(bad, true)
	j, err = q.Retry(j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if j.Items[0].Status != "completed" || j.Items[1].Payload != failedPayload {
		t.Fatal("重试重新生成作品或重置成功项")
	}
	if err = q.batch(t.Context(), &j); err != nil {
		t.Fatal("修复图片后重试失败", err)
	}
	j.Status = "completed"
	if err = st.UpdateJob(j); err != nil {
		t.Fatal(err)
	}
	works, count, err := st.List(model.Filter{Platform: "fanbox", Adult: true, Page: 1, PageSize: 20})
	if err != nil || count != 2 {
		t.Fatal("重试产生重复作品", count, err)
	}
	for _, w := range works {
		if w.Rating != 1 || w.AuthorID != "42" || w.SourceURL != "" {
			t.Fatalf("导入属性错误: %+v", w)
		}
		if w.ID == goodID && w.MediaCount != 1 {
			t.Fatal("重试修改了成功作品")
		}
	}
	j, err = q.Enqueue("batch", string(raw), string(raw))
	if err != nil {
		t.Fatal(err)
	}
	if err = q.batch(t.Context(), &j); err != nil {
		t.Fatal(err)
	}
	_, count, err = st.List(model.Filter{Platform: "fanbox", Adult: true, Page: 1, PageSize: 20})
	if err != nil || count != 4 {
		t.Fatal("重新提交被去重", count, err)
	}
	// 上传目录与服务器目录走同一个解析入口。
	uploadRoot := filepath.Join(c.DataDir, "staging", "upload-test", "files", "fanbox")
	write(filepath.Join(uploadRoot, "42-作者", "2026-01-01-正常", "0.png"), true)
	raw, _ = json.Marshal(BatchInput{Source: "upload", UploadID: "test", Rating: 0, Format: "fanbox"})
	upload, err := q.Enqueue("batch", "upload:test", string(raw))
	if err != nil {
		t.Fatal(err)
	}
	if err = q.batch(t.Context(), &upload); err != nil {
		t.Fatal("上传导入失败", err)
	}
	result, err := st.Job(upload.ID)
	if err != nil || result.Succeeded != 1 {
		t.Fatal(err, result)
	}
	w, err := st.Work(*result.Items[0].WorkID, true)
	if err != nil || w.Platform != "fanbox" || w.Title != "正常" || w.AuthorName != "作者" || w.PublishedAt != "2026-01-01" || w.Rating != 0 {
		t.Fatalf("上传与服务器解析不一致: %v %+v", err, w)
	}
	if _, err = q.Media.Resolve(w.Assets[0]); err != nil {
		t.Fatal("上传清理后作品不可用", err)
	}
}
