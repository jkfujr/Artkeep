package importer

import (
	"collection/internal/media"
	"collection/internal/model"
	"collection/internal/store"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var headerRE = regexp.MustCompile(`(?m)^(ID|URL|Original|Thumbnail|xRestrict|AI|User|UserID|Title|Description|Tags|Size|Bookmark|Date)\n`)
var numericRE = regexp.MustCompile(`^\d+$`)

func ParsePixiv(raw string) (model.Work, error) {
	text := strings.ReplaceAll(strings.TrimPrefix(raw, "\ufeff"), "\r\n", "\n")
	matches := headerRE.FindAllStringSubmatchIndex(text, -1)
	fields := map[string]string{}
	for i, m := range matches {
		if m[0] > 0 && (m[0] < 2 || text[m[0]-2:m[0]] != "\n\n") {
			continue
		}
		end := len(text)
		if i+1 < len(matches) {
			end = matches[i+1][0]
		}
		fields[text[m[2]:m[3]]] = strings.TrimSpace(text[m[1]:end])
	}
	w := model.Work{Platform: "pixiv", SourceKind: "artwork", SourceID: fields["ID"], SourceURL: fields["URL"], Title: fields["Title"], Description: fields["Description"], AuthorID: fields["UserID"], AuthorName: fields["User"], Raw: raw, Root: "pixiv", Ready: true, SourceTags: []string{}}
	if !numericRE.MatchString(w.SourceID) || !numericRE.MatchString(w.AuthorID) || w.SourceURL == "" {
		return w, errors.New("作品元数据缺少有效 ID 或来源")
	}
	if strings.Contains(w.SourceURL, "/n/") || strings.Contains(w.SourceURL, "/novel/") {
		w.SourceKind = "novel"
	}
	w.SourceURL = "https://www.pixiv.net/artworks/" + w.SourceID
	if w.SourceKind == "novel" {
		w.SourceURL = "https://www.pixiv.net/novel/show.php?id=" + w.SourceID
	}
	w.AuthorURL = "https://www.pixiv.net/users/" + w.AuthorID
	switch fields["xRestrict"] {
	case "AllAges":
		w.SourceRating = 0
	case "R-18":
		w.SourceRating = 1
	case "R-18G":
		w.SourceRating = 2
	default:
		return w, errors.New("作品分级无法识别")
	}
	date, err := time.Parse(time.RFC3339, fields["Date"])
	if err != nil {
		return w, errors.New("作品发布时间无法识别")
	}
	w.PublishedAt = date.UTC().Format(time.RFC3339)
	for _, tag := range strings.Split(fields["Tags"], "\n") {
		if strings.HasPrefix(tag, "#") {
			w.SourceTags = append(w.SourceTags, strings.TrimPrefix(tag, "#"))
		}
	}
	return w, nil
}

type Pixiv struct {
	Store *store.Store
	Media *media.Service
	Root  string
}

func (p *Pixiv) Scan(ctx context.Context, progress func(int, int, string)) (int, error) {
	if p.Root == "" {
		return 0, errors.New("尚未配置 Pixiv 收藏目录")
	}
	stat, err := os.Stat(p.Root)
	if err != nil || !stat.IsDir() {
		return 0, errors.New("Pixiv 收藏目录不可用")
	}
	paths := []string{}
	err = filepath.WalkDir(p.Root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), "-meta.txt") {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	sort.Strings(paths)
	if len(paths) == 0 {
		return 0, errors.New("目录中没有找到作品元数据")
	}
	seen := map[int64]bool{}
	failures := 0
	firstError := ""
	progress(0, len(paths), "")
	for i, path := range paths {
		if ctx.Err() != nil {
			return len(seen), ctx.Err()
		}
		id, e := p.importOne(ctx, path)
		if e != nil {
			failures++
			if firstError == "" {
				firstError = fmt.Sprintf("%s: %s", filepath.Base(path), e.Error())
			}
			if id > 0 {
				seen[id] = true
			}
		} else {
			seen[id] = true
		}
		if i%20 == 0 || i == len(paths)-1 {
			progress(i+1, len(paths), firstError)
		}
	}
	if failures > 0 {
		return len(seen), fmt.Errorf("%d 部作品导入失败；%s", failures, firstError)
	}
	if err = p.Store.MarkMissingPixiv(seen); err != nil {
		return len(seen), err
	}
	return len(seen), nil
}

func (p *Pixiv) importOne(ctx context.Context, path string) (int64, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	w, err := ParsePixiv(string(raw))
	if err != nil {
		return 0, err
	}
	rel, err := filepath.Rel(p.Root, path)
	if err != nil {
		return 0, err
	}
	w.MetadataPath = filepath.ToSlash(rel)
	existing, oldFP, _, err := p.Store.Find(w.Platform, w.SourceKind, w.SourceID)
	if err != nil {
		return 0, err
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		return existing, err
	}
	metaStat, err := os.Stat(path)
	if err != nil {
		return existing, err
	}
	var fp strings.Builder
	fmt.Fprintf(&fp, "%s:%d:%d", w.MetadataPath, metaStat.Size(), metaStat.ModTime().UnixNano())
	pageRE := regexp.MustCompile(`^` + regexp.QuoteMeta(w.SourceID) + `(?:_p(\d+))?\.[^.]+$`)
	type candidate struct {
		path     string
		position int
		size     int64
		modified int64
	}
	files := []candidate{}
	for _, entry := range entries {
		if entry.IsDir() || media.Kind(entry.Name()) == "" {
			continue
		}
		m := pageRE.FindStringSubmatch(entry.Name())
		if m == nil {
			continue
		}
		position := 0
		if m[1] != "" {
			position, _ = strconv.Atoi(m[1])
		}
		full := filepath.Join(filepath.Dir(path), entry.Name())
		stat, e := os.Stat(full)
		if e != nil {
			return existing, e
		}
		files = append(files, candidate{full, position, stat.Size(), stat.ModTime().UnixNano()})
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].position == files[j].position {
			return files[i].path < files[j].path
		}
		return files[i].position < files[j].position
	})
	for _, f := range files {
		fmt.Fprintf(&fp, "|%s:%d:%d", filepath.Base(f.path), f.size, f.modified)
	}
	w.Fingerprint = fp.String()
	if existing > 0 && oldFP == w.Fingerprint {
		known, e := p.Store.Assets(existing)
		if e != nil {
			return existing, e
		}
		present := map[string]bool{}
		for _, f := range files {
			rel, e := filepath.Rel(p.Root, f.path)
			if e != nil {
				return existing, e
			}
			present[filepath.ToSlash(rel)] = true
		}
		unchanged := true
		for _, a := range known {
			if a.Available != present[a.Path] {
				unchanged = false
				break
			}
		}
		if unchanged {
			available := len(files) > 0
			_, e = p.Store.DB.Exec("UPDATE works SET available=? WHERE id=? AND available<>?", available, existing, available)
			return existing, e
		}
	}
	assets := []model.Asset{}
	for _, f := range files {
		a, e := p.Media.Inspect(ctx, "pixiv", p.Root, f.path, f.position)
		if e != nil {
			return existing, e
		}
		assets = append(assets, a)
	}
	if err = p.Store.UpsertWork(&w, assets); err != nil {
		return existing, err
	}
	return w.ID, nil
}
