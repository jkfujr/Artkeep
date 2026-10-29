package queue

import (
	"collection/internal/config"
	"collection/internal/media"
	"encoding/json"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeX(t *testing.T) {
	const id = "1703346616298832270"
	for _, raw := range []string{"https://x.com/KuroTuki_nn/status/" + id + "?s=20", "https://twitter.com/user/status/" + id + "/photo/1", "https://x.com/i/web/status/" + id} {
		got, url, err := NormalizeX(raw)
		if err != nil || got != id || url != "https://x.com/i/web/status/"+id {
			t.Fatalf("链接规范化失败: %s %v", raw, err)
		}
	}
	for _, raw := range []string{"http://x.com/user/status/1", "https://x.com/user", "https://x.com.evil.example/user/status/1", "https://x.com@127.0.0.1/user/status/1", "https://127.0.0.1/user/status/1", "https://x.com:8443/user/status/1"} {
		if _, _, err := NormalizeX(raw); err == nil {
			t.Fatalf("接受了不支持的链接: %s", raw)
		}
	}
}

func TestXMetadataCompletenessAndRating(t *testing.T) {
	const id = "1703346616298832270"
	for _, test := range []struct {
		name      string
		sensitive any
		rating    int
	}{{"missing", nil, 0}, {"false", false, 0}, {"true", true, 1}} {
		t.Run(test.name, func(t *testing.T) {
			c := config.Config{DataDir: t.TempDir()}
			dir := filepath.Join(c.DownloadDir(), "x", id)
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			q := &Queue{Config: c, Media: media.New(c)}
			write := func(num int, count int) {
				path := filepath.Join(dir, id+"_"+string(rune('0'+num))+".png")
				f, err := os.Create(path)
				if err != nil {
					t.Fatal(err)
				}
				err = png.Encode(f, image.NewRGBA(image.Rect(0, 0, 12, 16)))
				f.Close()
				if err != nil {
					t.Fatal(err)
				}
				m := map[string]any{"tweet_id": json.Number(id), "author": map[string]any{"id": json.Number("123456789012345678"), "name": "example", "nick": "作者"}, "num": num, "count": count, "date": "2023-09-17 09:54:14", "content": "测试作品\n说明", "type": "photo"}
				if test.sensitive != nil {
					m["sensitive"] = test.sensitive
				}
				data, err := json.Marshal(m)
				if err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(path+".json", data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			write(1, 2)
			if _, _, err := q.readX(t.Context(), dir, id, "collection"); err == nil {
				t.Fatal("部分文件被计为完整作品")
			}
			write(2, 2)
			w, assets, err := q.readX(t.Context(), dir, id, "collection")
			if err != nil {
				t.Fatal(err)
			}
			if len(assets) != 2 || w.SourceID != id || w.AuthorID != "123456789012345678" || w.SourceRating != test.rating || len(w.SourceTags) != 0 {
				t.Fatalf("元数据映射错误: %+v", w)
			}
			if assets[0].Position != 0 || assets[1].Position != 1 {
				t.Fatal("媒体顺序错误")
			}
		})
	}
}
