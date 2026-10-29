package importer

import (
	"collection/internal/config"
	"collection/internal/media"
	"collection/internal/model"
	"collection/internal/store"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Candidate struct {
	Work     model.Work `json:"work"`
	Files    []string   `json:"files"`
	Metadata []string   `json:"metadata"`
	Raw      string     `json:"raw"`
	Error    string     `json:"error,omitempty"`
}
type Collection struct {
	Store  *store.Store
	Media  *media.Service
	Config config.Config
}

func Within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
func Directory(path, data string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("请选择服务器目录")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	// 检查每层目录，服务器导入不经过符号链接。
	for current := abs; ; current = filepath.Dir(current) {
		info, e := os.Lstat(current)
		if e != nil {
			return "", e
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("不能导入符号链接目录")
		}
		if filepath.Dir(current) == current {
			break
		}
	}
	if Within(data, abs) {
		return "", errors.New("不能导入数据目录")
	}
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		return "", errors.New("目录不可用")
	}
	return abs, nil
}

var numberParts = regexp.MustCompile(`\d+|\D+`)

func NaturalLess(a, b string) bool {
	x, y := numberParts.FindAllString(strings.ToLower(a), -1), numberParts.FindAllString(strings.ToLower(b), -1)
	for i := 0; i < len(x) && i < len(y); i++ {
		if x[i] == y[i] {
			continue
		}
		xn, yn := strings.TrimLeft(x[i], "0"), strings.TrimLeft(y[i], "0")
		if numericRE.MatchString(x[i]) && numericRE.MatchString(y[i]) {
			if len(xn) != len(yn) {
				return len(xn) < len(yn)
			}
			if xn != yn {
				return xn < yn
			}
		}
		return x[i] < y[i]
	}
	if len(x) != len(y) {
		return len(x) < len(y)
	}
	return a < b
}

func (c *Collection) Discover(ctx context.Context, root string, rating int) ([]Candidate, error) {
	files := []string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if d.IsDir() {
			if path != root && !Within(c.Config.DataDir, root) && Within(c.Config.DataDir, path) {
				return fs.SkipDir
			}
			return nil
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool { return NaturalLess(files[i], files[j]) })
	used := map[string]bool{}
	out := []Candidate{}
	groups := map[string]int{}
	for _, path := range files {
		if !strings.HasSuffix(path, "-meta.txt") {
			continue
		}
		raw, e := os.ReadFile(path)
		if e != nil {
			return nil, e
		}
		w, e := ParsePixiv(string(raw))
		v := Candidate{Work: w, Raw: string(raw), Metadata: []string{path}}
		id := w.SourceID
		if id == "" {
			id = strings.TrimSuffix(filepath.Base(path), "-meta.txt")
		}
		re := regexp.MustCompile(`^` + regexp.QuoteMeta(id) + `(?:_p\d+)?\.[^.]+$`)
		for _, f := range files {
			if filepath.Dir(f) == filepath.Dir(path) && media.Kind(f) != "" && re.MatchString(filepath.Base(f)) {
				v.Files = append(v.Files, f)
				used[f] = true
			}
		}
		if e != nil {
			v.Error = e.Error()
		}
		if len(v.Files) == 0 && v.Error == "" {
			v.Error = "元数据没有对应媒体"
		}
		if e == nil {
			groups[w.Platform+":"+w.SourceKind+":"+w.SourceID] = len(out)
		}
		out = append(out, v)
	}
	for _, path := range files {
		if strings.ToLower(filepath.Ext(path)) != ".json" {
			continue
		}
		raw, e := os.ReadFile(path)
		if e != nil {
			return nil, e
		}
		w, ok := ParseGallery(raw)
		if !ok {
			continue
		}
		if !SourceRatingKnown(raw) {
			w.SourceRating = rating
		}
		key := w.Platform + ":" + w.SourceKind + ":" + w.SourceID
		v := Candidate{Work: w, Raw: string(raw)}
		base := strings.TrimSuffix(path, filepath.Ext(path))
		for _, f := range files {
			if !associatedMedia(f, w.SourceKind) || used[f] || filepath.Dir(f) != filepath.Dir(path) {
				continue
			}
			stem := strings.TrimSuffix(f, filepath.Ext(f))
			if f == base || stem == base {
				v.Files = append(v.Files, f)
				used[f] = true
			}
		}
		// 部分 gallery-dl 配置为一部作品保存一份 JSON。
		if len(v.Files) == 0 {
			re := regexp.MustCompile(`^` + regexp.QuoteMeta(w.SourceID) + `(?:_p\d+|_\d+)?\.[^.]+$`)
			for _, f := range files {
				if !used[f] && associatedMedia(f, w.SourceKind) && filepath.Dir(f) == filepath.Dir(path) && re.MatchString(filepath.Base(f)) {
					v.Files = append(v.Files, f)
					used[f] = true
				}
			}
		}
		v.Metadata = []string{path}
		if i, exists := groups[key]; exists {
			out[i].Files = append(out[i].Files, v.Files...)
			out[i].Metadata = append(out[i].Metadata, path)
		} else {
			groups[key] = len(out)
			out = append(out, v)
		}
	}
	local := map[string]int{}
	for _, f := range files {
		if used[f] || media.Kind(f) == "" {
			continue
		}
		dir := filepath.Dir(f)
		if i, ok := local[dir]; ok {
			out[i].Files = append(out[i].Files, f)
		} else {
			local[dir] = len(out)
			out = append(out, Candidate{Work: model.Work{Platform: "local", SourceKind: "album", AuthorID: "unknown", Title: filepath.Base(dir), SourceRating: rating, SourceTags: []string{}}, Files: []string{f}})
		}
	}
	for i := range out {
		sort.Slice(out[i].Files, func(a, b int) bool { return NaturalLess(out[i].Files[a], out[i].Files[b]) })
		if len(out[i].Files) == 0 && out[i].Error == "" {
			out[i].Error = "元数据没有对应媒体"
		}
	}
	if len(out) == 0 {
		return nil, errors.New("目录中没有可导入的作品")
	}
	return out, nil
}

func value(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case json.Number:
		return x.String()
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	}
	return ""
}
func integer(v any) int { n, _ := strconv.Atoi(value(v)); return n }

func associatedMedia(path, kind string) bool {
	return media.Kind(path) != "" || (kind == "novel" && strings.EqualFold(filepath.Ext(path), ".txt") && !strings.HasSuffix(path, "-meta.txt"))
}
func SourceRatingKnown(raw []byte) bool {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return false
	}
	if m["x_restrict"] != nil {
		return true
	}
	if _, ok := m["sensitive"].(bool); ok {
		return true
	}
	_, ok := m["sensitive_flags"]
	return ok
}
func ParseGallery(raw []byte) (model.Work, bool) {
	var m map[string]any
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	if dec.Decode(&m) != nil {
		return model.Work{}, false
	}
	w := model.Work{SourceTags: []string{}, Raw: string(raw)}
	category := value(m["category"])
	if category == "pixiv" || category == "pixiv-novel" || m["x_restrict"] != nil {
		w.Platform = "pixiv"
		w.SourceKind = "artwork"
		w.SourceID = value(m["id"])
		if category == "pixiv-novel" || value(m["subcategory"]) == "novel" || m["novel_id"] != nil || m["text_length"] != nil {
			w.SourceKind = "novel"
			if m["novel_id"] != nil {
				w.SourceID = value(m["novel_id"])
			}
		}
		author, _ := m["user"].(map[string]any)
		w.AuthorID = value(author["id"])
		w.AuthorName = value(author["name"])
		w.AuthorURL = "https://www.pixiv.net/users/" + w.AuthorID
		w.SourceURL = "https://www.pixiv.net/artworks/" + w.SourceID
		if w.SourceKind == "novel" {
			w.SourceURL = "https://www.pixiv.net/novel/show.php?id=" + w.SourceID
		}
		w.SourceRating = integer(m["x_restrict"])
		w.Title = value(m["title"])
		w.Description = value(m["caption"])
	} else if category == "twitter" || m["tweet_id"] != nil {
		w.Platform = "x"
		w.SourceKind = "tweet"
		w.SourceID = value(m["tweet_id"])
		author, _ := m["author"].(map[string]any)
		w.AuthorID = value(author["id"])
		if w.AuthorID == "" {
			w.AuthorID = value(author["id_str"])
		}
		w.AuthorName = value(author["nick"])
		if w.AuthorName == "" {
			w.AuthorName = value(author["name"])
		}
		w.AuthorURL = "https://x.com/i/user/" + w.AuthorID
		w.SourceURL = "https://x.com/i/web/status/" + w.SourceID
		w.Description = value(m["content"])
		w.Title = strings.Split(w.Description, "\n")[0]
		if sensitive, _ := m["sensitive"].(bool); sensitive {
			w.SourceRating = 1
		}
		if flags, ok := m["sensitive_flags"].([]any); ok && len(flags) > 0 {
			w.SourceRating = 1
		}
	} else {
		return w, false
	}
	if !numericRE.MatchString(w.SourceID) || !numericRE.MatchString(w.AuthorID) {
		return w, false
	}
	if w.Title == "" {
		w.Title = w.SourceID
	}
	for _, key := range []string{"tags", "hashtags"} {
		if tags, ok := m[key].([]any); ok {
			for _, tag := range tags {
				if obj, ok := tag.(map[string]any); ok {
					tag = obj["name"]
				}
				if s := value(tag); s != "" {
					w.SourceTags = append(w.SourceTags, s)
				}
			}
		}
	}
	for _, key := range []string{"date", "create_date"} {
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05", "2006-01-02T15:04:05"} {
			if d, e := time.Parse(layout, value(m[key])); e == nil {
				w.PublishedAt = d.UTC().Format(time.RFC3339)
				break
			}
		}
	}
	return w, true
}

func contentID(ctx context.Context, files []string) (string, error) {
	h := sha256.New()
	for _, path := range files {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		f, err := os.Open(path)
		if err != nil {
			return "", err
		}
		info, err := f.Stat()
		if err != nil {
			f.Close()
			return "", err
		}
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(info.Size()))
		h.Write(size[:])
		_, err = io.Copy(h, f)
		f.Close()
		if err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func segment(v string) string {
	if v == "" {
		return "unknown"
	}
	if !numericRE.MatchString(v) && v != "unknown" {
		for _, r := range v {
			if !strings.ContainsRune("0123456789abcdef", r) {
				return "unknown"
			}
		}
	}
	return v
}
func workDir(w model.Work) string {
	return filepath.Join(w.Platform, w.SourceKind, segment(w.AuthorID), segment(w.SourceID))
}

func Copy(ctx context.Context, src, dst string, progress func(int64)) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("来源不是普通文件")
	}
	if err = os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	buf := make([]byte, 1024*1024)
	var written int64
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		n, e := in.Read(buf)
		if n > 0 {
			n2, e2 := out.Write(buf[:n])
			written += int64(n2)
			if progress != nil {
				progress(int64(n2))
			}
			if e2 != nil {
				return e2
			}
			if n2 != n {
				return io.ErrShortWrite
			}
		}
		if e == io.EOF {
			break
		}
		if e != nil {
			return e
		}
	}
	if written != info.Size() {
		return errors.New("来源文件在复制时发生变化")
	}
	if err = out.Sync(); err != nil {
		return err
	}
	if err = out.Close(); err != nil {
		return err
	}
	return os.Chtimes(dst, info.ModTime(), info.ModTime())
}

func (c *Collection) save(ctx context.Context, w model.Work, files, metas []string, raw string, progress func(int64)) (model.Work, []model.Asset, error) {
	rel := workDir(w)
	final := filepath.Join(c.Config.DownloadDir(), rel)
	stage, err := os.MkdirTemp(filepath.Join(c.Config.DataDir, "staging"), "work-")
	if err != nil {
		return w, nil, err
	}
	defer os.RemoveAll(stage)
	assets := []model.Asset{}
	mapping := []map[string]string{}
	for i, src := range files {
		ext := strings.ToLower(filepath.Ext(src))
		name := fmt.Sprintf("%06d%s", i+1, ext)
		dst := filepath.Join(stage, "media", name)
		if err = Copy(ctx, src, dst, progress); err != nil {
			return w, nil, err
		}
		a, e := c.Media.Inspect(ctx, "collection", stage, dst, i)
		if e != nil {
			return w, nil, e
		}
		a.Path = filepath.ToSlash(filepath.Join(rel, "media", name))
		assets = append(assets, a)
		mapping = append(mapping, map[string]string{"file": name, "original": filepath.Base(src)})
	}
	if len(assets) == 0 {
		return w, nil, errors.New("作品没有媒体")
	}
	metaDir := filepath.Join(stage, "metadata")
	if err = os.MkdirAll(metaDir, 0700); err != nil {
		return w, nil, err
	}
	// 原始来源信息和文件名映射分开存放，不写入下载配置或凭据。
	if raw == "" {
		raw = "{}"
	}
	source := []byte(raw)
	if !json.Valid(source) {
		source, _ = json.Marshal(map[string]string{"format": "pixiv-txt", "text": raw})
	}
	if err = os.WriteFile(filepath.Join(metaDir, "source.json"), source, 0600); err != nil {
		return w, nil, err
	}
	mapped, _ := json.Marshal(mapping)
	if err = os.WriteFile(filepath.Join(metaDir, "files.json"), mapped, 0600); err != nil {
		return w, nil, err
	}
	for i, src := range metas {
		if err = Copy(ctx, src, filepath.Join(metaDir, fmt.Sprintf("original-%04d%s", i+1, filepath.Ext(src))), nil); err != nil {
			return w, nil, err
		}
	}
	if err = os.MkdirAll(filepath.Dir(final), 0700); err != nil {
		return w, nil, err
	}
	// 目录可能已在中断前保存。完整复制结果仍先保留在 staging。
	if _, err = os.Stat(final); err == nil {
		return w, nil, errors.New("目标目录已存在，等待恢复入库")
	}
	if err = os.Rename(stage, final); err != nil {
		return w, nil, err
	}
	w.Root = "collection"
	w.MetadataPath = filepath.ToSlash(filepath.Join(rel, "metadata", "source.json"))
	w.Raw = raw
	w.Ready = true
	return w, assets, nil
}

func (c *Collection) Import(ctx context.Context, v Candidate, progress func(int64)) (int64, bool, error) {
	if v.Error != "" {
		return 0, false, errors.New(v.Error)
	}
	w := v.Work
	if w.Platform == "local" {
		id, err := contentID(ctx, v.Files)
		if err != nil {
			return 0, false, err
		}
		w.SourceID = id
	}
	existing, _, ready, err := c.Store.Find(w.Platform, w.SourceKind, w.SourceID)
	if err != nil {
		return 0, false, err
	}
	if existing > 0 && ready {
		err = c.Migrate(ctx, existing, progress)
		return existing, true, err
	}
	if w.SourceKind == "novel" {
		var cleanup func()
		v, cleanup, err = c.prepareNovel(v)
		if err != nil {
			return 0, false, err
		}
		defer cleanup()
	}
	w, assets, err := c.saveOrRecover(ctx, w, v.Files, v.Metadata, v.Raw, progress)
	if err != nil {
		return 0, false, err
	}
	w.Fingerprint = w.Platform + ":" + w.SourceKind + ":" + w.SourceID
	if err = c.Store.UpsertWork(&w, assets); err != nil {
		return 0, false, err
	}
	return w.ID, false, nil
}
func (c *Collection) saveOrRecover(ctx context.Context, w model.Work, files, metas []string, raw string, progress func(int64)) (model.Work, []model.Asset, error) {
	rel := workDir(w)
	final := filepath.Join(c.Config.DownloadDir(), rel)
	marker := filepath.Join(final, "metadata", "files.json")
	if b, err := os.ReadFile(marker); err == nil {
		var mapping []map[string]string
		if json.Unmarshal(b, &mapping) != nil || len(mapping) != len(files) {
			return w, nil, errors.New("已保存的作品文件不完整")
		}
		assets := []model.Asset{}
		for i, m := range mapping {
			a, e := c.Media.Inspect(ctx, "collection", c.Config.DownloadDir(), filepath.Join(final, "media", filepath.Base(m["file"])), i)
			if e != nil {
				return w, nil, e
			}
			assets = append(assets, a)
		}
		source, e := os.ReadFile(filepath.Join(final, "metadata", "source.json"))
		if e != nil {
			return w, nil, e
		}
		w.Root = "collection"
		w.MetadataPath = filepath.ToSlash(filepath.Join(rel, "metadata", "source.json"))
		w.Ready = true
		w.Raw = string(source)
		return w, assets, nil
	}
	return c.save(ctx, w, files, metas, raw, progress)
}
func (c *Collection) Migrate(ctx context.Context, id int64, progress func(int64)) error {
	w, err := c.Store.Work(id, true)
	if err != nil {
		return err
	}
	rel := workDir(w)
	desired := filepath.ToSlash(filepath.Join(rel, "metadata", "source.json"))
	done := w.Root == "collection" && w.MetadataPath == desired
	for _, a := range w.Assets {
		done = done && a.Root == "collection" && strings.HasPrefix(a.Path, filepath.ToSlash(filepath.Join(rel, "media"))+"/")
	}
	if done {
		return nil
	}
	files := []string{}
	for _, a := range w.Assets {
		path, e := c.Media.Resolve(a)
		if e != nil {
			return fmt.Errorf("原文件缺失: %s", filepath.Base(a.Path))
		}
		files = append(files, path)
	}
	var raw string
	if err = c.Store.DB.QueryRow("SELECT raw_metadata FROM works WHERE id=?", id).Scan(&raw); err != nil {
		return err
	}
	metas := []string{}
	if w.MetadataPath != "" {
		if path, e := media.SafePath(c.Config.Roots()[w.Root], w.MetadataPath); e == nil {
			if info, e := os.Stat(path); e == nil && info.Mode().IsRegular() {
				metas = append(metas, path)
			}
		}
	}
	updated, assets, err := c.saveOrRecover(ctx, w, files, metas, raw, progress)
	if err != nil {
		return err
	}
	if len(assets) != len(w.Assets) {
		return errors.New("迁移媒体数量不一致")
	}
	for i := range assets {
		assets[i].ID = w.Assets[i].ID
	}
	return c.Store.Relocate(updated, assets)
}
