package importer

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"mime"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

func (c *Collection) prepareNovel(v Candidate) (Candidate, func(), error) {
	textPath := ""
	for _, path := range v.Files {
		if strings.EqualFold(filepath.Ext(path), ".txt") {
			textPath = path
			break
		}
	}
	if textPath == "" {
		return v, func() {}, nil
	}
	body, err := os.ReadFile(textPath)
	if err != nil {
		return v, func() {}, err
	}
	images := map[string]string{}
	for _, path := range v.Metadata {
		if !strings.EqualFold(filepath.Ext(path), ".json") {
			continue
		}
		raw, e := os.ReadFile(path)
		if e != nil {
			return v, func() {}, e
		}
		var meta map[string]any
		dec := json.NewDecoder(strings.NewReader(string(raw)))
		dec.UseNumber()
		if dec.Decode(&meta) != nil {
			continue
		}
		base := strings.TrimSuffix(path, ".json")
		for _, file := range v.Files {
			if file != base && strings.TrimSuffix(file, filepath.Ext(file)) != base {
				continue
			}
			if value(meta["category"]) == "pixiv" {
				id := value(meta["id"])
				images["pixivimage:"+id+"-"+strconv.Itoa(integer(meta["num"])+1)] = file
				images["pixivimage:"+id] = file
			} else {
				for _, key := range []string{"novelImageId", "novel_image_id", "id", "name", "filename"} {
					if id := value(meta[key]); id != "" {
						images["uploadedimage:"+id] = file
					}
				}
			}
		}
	}
	temp, err := os.MkdirTemp(filepath.Join(c.Config.DataDir, "staging"), "novel-")
	if err != nil {
		return v, func() {}, err
	}
	cleanup := func() { os.RemoveAll(temp) }
	epub := filepath.Join(temp, "novel.epub")
	if err = WriteNovelEPUB(epub, v.Work.Title, v.Work.AuthorName, string(body), images); err != nil {
		cleanup()
		return v, func() {}, err
	}
	v.Metadata = append(v.Metadata, textPath)
	v.Files = []string{epub}
	return v, cleanup, nil
}

func ValidateDownloaded(v Candidate) error {
	if len(v.Files) == 0 {
		return errors.New("作品媒体没有下载完成")
	}
	var m map[string]any
	if json.Unmarshal([]byte(v.Raw), &m) != nil {
		return errors.New("作品元数据无效")
	}
	expected := integer(m["page_count"])
	if expected == 0 {
		expected = integer(m["count"])
	}
	if expected > 0 && len(v.Files) != expected {
		return errors.New("作品媒体没有全部下载完成")
	}
	return nil
}

func DownloadedNovel(dir, id string) (Candidate, error) {
	textPath := filepath.Join(dir, id+"_p0.txt")
	raw, err := os.ReadFile(textPath + ".json")
	if err != nil {
		return Candidate{}, errors.New("小说元数据没有下载完成")
	}
	w, ok := ParseGallery(raw)
	if !ok || w.SourceID != id {
		return Candidate{}, errors.New("小说元数据与来源不匹配")
	}
	w.SourceKind = "novel"
	w.SourceURL = "https://www.pixiv.net/novel/show.php?id=" + id
	body, err := os.ReadFile(textPath)
	if err != nil || len(body) == 0 {
		return Candidate{}, errors.New("小说正文没有下载完成")
	}
	images := map[string]string{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return Candidate{}, err
	}
	metas := []string{textPath, textPath + ".json"}
	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") || path == textPath+".json" {
			continue
		}
		b, e := os.ReadFile(path)
		if e != nil {
			return Candidate{}, e
		}
		var m map[string]any
		dec := json.NewDecoder(strings.NewReader(string(b)))
		dec.UseNumber()
		if dec.Decode(&m) != nil {
			continue
		}
		file := strings.TrimSuffix(path, ".json")
		if _, e = os.Stat(file); e != nil {
			continue
		}
		ext := strings.ToLower(filepath.Ext(file))
		if ext != ".jpg" && ext != ".jpeg" && ext != ".png" && ext != ".webp" && ext != ".gif" {
			continue
		}
		metas = append(metas, path)
		category := value(m["category"])
		imageID := value(m["id"])
		if category == "pixiv" {
			images["pixivimage:"+imageID+"-"+strconv.Itoa(integer(m["num"])+1)] = file
			images["pixivimage:"+imageID] = file
		} else {
			for _, key := range []string{"novelImageId", "novel_image_id", "id", "name"} {
				if v := value(m[key]); v != "" {
					images["uploadedimage:"+v] = file
				}
			}
		}
	}
	epub := filepath.Join(dir, id+".epub")
	if err = WriteNovelEPUB(epub, w.Title, w.AuthorName, string(body), images); err != nil {
		return Candidate{}, err
	}
	return Candidate{Work: w, Files: []string{epub}, Metadata: metas, Raw: string(raw)}, nil
}

var novelToken = regexp.MustCompile(`\[\[(?:rb|jumpuri):[^\]]*\]\]|\[(?:chapter:[^\]]*|newpage|pixivimage:[^\]]+|uploadedimage:[^\]]+|jump:\d+)\]`)
var pageToken = regexp.MustCompile(`\[chapter:([^\]]*)\]|\[newpage\]`)

// 将 Pixiv 的文本标记转换为 EPUB；所有普通正文先转义，书籍不包含脚本。
func WriteNovelEPUB(path, title, author, body string, images map[string]string) error {
	type chapter struct{ title, body string }
	chapters := []chapter{}
	matches := pageToken.FindAllStringSubmatchIndex(body, -1)
	start := 0
	heading := title
	for _, m := range matches {
		if strings.TrimSpace(body[start:m[0]]) != "" {
			chapters = append(chapters, chapter{heading, body[start:m[0]]})
		}
		if m[2] >= 0 {
			heading = body[m[2]:m[3]]
		} else {
			heading = fmt.Sprintf("第 %d 页", len(chapters)+1)
		}
		start = m[1]
	}
	if strings.TrimSpace(body[start:]) != "" {
		chapters = append(chapters, chapter{heading, body[start:]})
	}
	if len(chapters) == 0 {
		return errors.New("小说正文为空")
	}
	imagePaths := []string{}
	imageNames := map[string]string{}
	for _, m := range novelToken.FindAllString(body, -1) {
		if !strings.HasPrefix(m, "[pixivimage:") && !strings.HasPrefix(m, "[uploadedimage:") {
			continue
		}
		key := strings.TrimSuffix(strings.TrimPrefix(m, "["), "]")
		source, ok := images[key]
		if !ok {
			return fmt.Errorf("小说插图 %s 没有下载完成", key)
		}
		if _, ok = imageNames[source]; !ok {
			imagePaths = append(imagePaths, source)
			imageNames[source] = fmt.Sprintf("images/%04d%s", len(imagePaths), strings.ToLower(filepath.Ext(source)))
		}
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	z := zip.NewWriter(file)
	add := func(name, value string, method uint16) error {
		f, e := z.CreateHeader(&zip.FileHeader{Name: name, Method: method})
		if e == nil {
			_, e = io.WriteString(f, value)
		}
		return e
	}
	if err = add("mimetype", "application/epub+zip", zip.Store); err != nil {
		return err
	}
	if err = add("META-INF/container.xml", `<?xml version="1.0"?><container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`, zip.Deflate); err != nil {
		return err
	}
	esc := html.EscapeString
	var manifest, spine, toc strings.Builder
	for i, ch := range chapters {
		name := fmt.Sprintf("chapter-%04d.xhtml", i+1)
		fmt.Fprintf(&manifest, `<item id="c%d" href="%s" media-type="application/xhtml+xml"/>`, i, name)
		fmt.Fprintf(&spine, `<itemref idref="c%d"/>`, i)
		fmt.Fprintf(&toc, `<li><a href="%s">%s</a></li>`, name, esc(ch.title))
		var content strings.Builder
		last := 0
		for _, m := range novelToken.FindAllStringIndex(ch.body, -1) {
			content.WriteString(strings.ReplaceAll(esc(ch.body[last:m[0]]), "\n", "<br/>"))
			token := ch.body[m[0]:m[1]]
			switch {
			case strings.HasPrefix(token, "[[rb:"):
				parts := strings.SplitN(strings.TrimSuffix(strings.TrimPrefix(token, "[[rb:"), "]]"), " > ", 2)
				if len(parts) == 2 {
					fmt.Fprintf(&content, "<ruby>%s<rt>%s</rt></ruby>", esc(parts[0]), esc(parts[1]))
				} else {
					content.WriteString(esc(token))
				}
			case strings.HasPrefix(token, "[pixivimage:") || strings.HasPrefix(token, "[uploadedimage:"):
				key := strings.TrimSuffix(strings.TrimPrefix(token, "["), "]")
				fmt.Fprintf(&content, `<img src="%s" alt="插图"/>`, imageNames[images[key]])
			case strings.HasPrefix(token, "[jump:"):
				n, _ := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(token, "[jump:"), "]"))
				if n > 0 && n <= len(chapters) {
					fmt.Fprintf(&content, `<a href="chapter-%04d.xhtml">第 %d 页</a>`, n, n)
				}
			case strings.HasPrefix(token, "[[jumpuri:"):
				parts := strings.SplitN(strings.TrimSuffix(strings.TrimPrefix(token, "[[jumpuri:"), "]]"), " > ", 2)
				content.WriteString(esc(parts[0]))
			default:
				content.WriteString(esc(token))
			}
			last = m[1]
		}
		content.WriteString(strings.ReplaceAll(esc(ch.body[last:]), "\n", "<br/>"))
		document := `<?xml version="1.0" encoding="UTF-8"?><html xmlns="http://www.w3.org/1999/xhtml"><head><title>` + esc(ch.title) + `</title><style>body{line-height:1.9;padding:1em;overflow-wrap:anywhere}img{max-width:100%;height:auto}rt{font-size:.6em}</style></head><body><h1>` + esc(ch.title) + `</h1><p>` + content.String() + `</p></body></html>`
		if err = add("OEBPS/"+name, document, zip.Deflate); err != nil {
			return err
		}
	}
	sort.Strings(imagePaths)
	for i, src := range imagePaths {
		name := imageNames[src]
		typ := mime.TypeByExtension(filepath.Ext(src))
		if typ == "" {
			typ = "image/jpeg"
		}
		fmt.Fprintf(&manifest, `<item id="i%d" href="%s" media-type="%s"/>`, i, name, typ)
		entry, e := z.Create("OEBPS/" + name)
		if e != nil {
			return e
		}
		in, e := os.Open(src)
		if e != nil {
			return e
		}
		_, e = io.Copy(entry, in)
		in.Close()
		if e != nil {
			return e
		}
	}
	nav := `<?xml version="1.0" encoding="UTF-8"?><html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops"><head><title>目录</title></head><body><nav epub:type="toc"><ol>` + toc.String() + `</ol></nav></body></html>`
	if err = add("OEBPS/nav.xhtml", nav, zip.Deflate); err != nil {
		return err
	}
	opf := `<?xml version="1.0" encoding="UTF-8"?><package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="id"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:identifier id="id">artkeep:` + esc(title) + `</dc:identifier><dc:title>` + esc(title) + `</dc:title><dc:creator>` + esc(author) + `</dc:creator><dc:language>ja</dc:language><meta property="dcterms:modified">` + time.Now().UTC().Format(time.RFC3339) + `</meta></metadata><manifest><item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>` + manifest.String() + `</manifest><spine>` + spine.String() + `</spine></package>`
	if err = add("OEBPS/content.opf", opf, zip.Deflate); err != nil {
		return err
	}
	if err = z.Close(); err != nil {
		return err
	}
	return file.Sync()
}
