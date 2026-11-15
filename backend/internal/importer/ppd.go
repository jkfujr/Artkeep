package importer

import (
	"bytes"
	"collection/internal/model"
	"encoding/json"
	"errors"
	"html"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func ParsePPD(raw []byte) (model.Work, error) {
	var m map[string]any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if dec.Decode(&m) != nil || m["idNum"] == nil || m["userId"] == nil {
		return model.Work{}, errors.New("PPD 元数据无效")
	}
	id, uid := value(m["idNum"]), value(m["userId"])
	if !numericRE.MatchString(id) || !numericRE.MatchString(uid) {
		return model.Work{}, errors.New("作品或作者 ID 无效")
	}
	rating, e := strconv.Atoi(value(m["xRestrict"]))
	if e != nil || rating < 0 || rating > 2 {
		return model.Work{}, errors.New("作品分级无效")
	}
	date, e := time.Parse(time.RFC3339, value(m["date"]))
	if e != nil {
		return model.Work{}, errors.New("作品发布时间无效")
	}
	kind := "artwork"
	if integer(m["type"]) == 3 {
		kind = "novel"
	}
	source := "https://www.pixiv.net/artworks/" + id
	if kind == "novel" {
		source = "https://www.pixiv.net/novel/show.php?id=" + id
	}
	w := model.Work{Platform: "pixiv", SourceKind: kind, SourceID: id, AuthorID: uid, AuthorName: value(m["user"]), AuthorURL: "https://www.pixiv.net/users/" + uid, SourceURL: source, Title: value(m["title"]), Description: plainDescription(value(m["description"])), SourceRating: rating, PublishedAt: date.UTC().Format(time.RFC3339), SourceTags: []string{}, Raw: string(raw)}
	if tags, ok := m["tags"].([]any); ok {
		for _, v := range tags {
			if tag := value(v); tag != "" {
				w.SourceTags = append(w.SourceTags, tag)
			}
		}
	}
	return w, nil
}

func plainDescription(s string) string {
	s = regexp.MustCompile(`(?i)<br\s*/?>`).ReplaceAllString(s, "\n")
	s = regexp.MustCompile(`<[^>]*>`).ReplaceAllString(s, "")
	return strings.TrimSpace(html.UnescapeString(s))
}
