package importer

import (
	"collection/internal/media"
	"collection/internal/model"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"path/filepath"
	"regexp"
	"sort"
	"time"
)

var fanboxAuthor = regexp.MustCompile(`^(\d+)-(.+)$`)
var fanboxPost = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2})-(.+)$`)

func (c *Collection) DiscoverFanbox(ctx context.Context, root string, rating int) ([]Candidate, error) {
	out := []Candidate{}
	groups := map[string]int{}
	add := func(dir string) (int, error) {
		if i, ok := groups[dir]; ok {
			return i, nil
		}
		var id [16]byte
		if _, err := rand.Read(id[:]); err != nil {
			return 0, err
		}
		v := Candidate{Work: model.Work{Platform: "fanbox", SourceKind: "post", SourceID: hex.EncodeToString(id[:]), Title: filepath.Base(dir), SourceRating: rating, SourceTags: []string{}}}
		author := fanboxAuthor.FindStringSubmatch(filepath.Base(filepath.Dir(dir)))
		post := fanboxPost.FindStringSubmatch(filepath.Base(dir))
		if author == nil {
			v.Error = "作者目录须为 ID-作者名"
		} else {
			v.Work.AuthorID, v.Work.AuthorName = author[1], author[2]
			if post == nil {
				v.Error = "作品目录须为 YYYY-MM-DD-标题"
			} else if _, err := time.Parse("2006-01-02", post[1]); err != nil {
				v.Error = "作品目录中的日期无效"
			} else {
				v.Work.PublishedAt, v.Work.Title = post[1], post[2]
			}
		}
		raw, err := json.Marshal(map[string]string{"format": "fanbox-directory", "authorDirectory": filepath.Base(filepath.Dir(dir)), "postDirectory": filepath.Base(dir)})
		if err != nil {
			return 0, err
		}
		v.Raw = string(raw)
		i := len(out)
		groups[dir] = i
		out = append(out, v)
		return i, nil
	}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, e error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if e != nil {
			if path == root {
				return e
			}
			dir := path
			if d == nil || !d.IsDir() {
				dir = filepath.Dir(path)
			}
			i, err := add(dir)
			if err != nil {
				return err
			}
			out[i].Error = "无法读取作品文件夹"
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		if d.IsDir() {
			if path != root && !Within(c.Config.DataDir, root) && Within(c.Config.DataDir, path) {
				return fs.SkipDir
			}
			if fanboxAuthor.MatchString(filepath.Base(filepath.Dir(path))) {
				_, err := add(path)
				return err
			}
			return nil
		}
		if kind := media.Kind(path); kind != "image" && kind != "animation" {
			return nil
		}
		i, err := add(filepath.Dir(path))
		if err != nil {
			return err
		}
		out[i].Files = append(out[i].Files, path)
		return nil
	})
	if err != nil {
		return nil, err
	}
	for i := range out {
		sort.Slice(out[i].Files, func(a, b int) bool { return NaturalLess(out[i].Files[a], out[i].Files[b]) })
		if len(out[i].Files) == 0 && out[i].Error == "" {
			out[i].Error = "作品文件夹中没有图片"
		}
	}
	if len(out) == 0 {
		return nil, errors.New("目录中没有 FANBOX 作品")
	}
	return out, nil
}
