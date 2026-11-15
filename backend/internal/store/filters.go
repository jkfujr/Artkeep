package store

import (
	"collection/internal/model"
	"strings"
)

const yearSQL = "CASE WHEN strftime('%Y',w.published_at) IS NULL THEN 'unknown' ELSE strftime('%Y',w.published_at) END"

func imageCondition(f model.Filter) (string, []any) {
	conditions := []string{"im.available=1", "im.kind IN ('image','animation')", "im.width>0", "im.height>0", "im.width>=?", "im.height>=?"}
	args := []any{f.MinWidth, f.MinHeight}
	if len(f.Shapes) > 0 {
		shapes := []string{}
		for _, shape := range f.Shapes {
			switch shape {
			case "landscape":
				shapes = append(shapes, "im.width>im.height")
			case "portrait":
				shapes = append(shapes, "im.width<im.height")
			case "square":
				shapes = append(shapes, "im.width=im.height")
			default:
				ratio := model.ShapeRatio(shape)
				shapes = append(shapes, "(1.0*im.width/im.height BETWEEN ? AND ?)")
				args = append(args, ratio*0.99, ratio*1.01)
			}
		}
		conditions = append(conditions, "("+strings.Join(shapes, " OR ")+")")
	}
	return strings.Join(conditions, " AND "), args
}

func matchesImage(a model.Asset, f model.Filter) bool {
	if !a.Available || (a.Kind != "image" && a.Kind != "animation") || a.Width <= 0 || a.Height <= 0 || a.Width < f.MinWidth || a.Height < f.MinHeight {
		return false
	}
	if len(f.Shapes) == 0 {
		return true
	}
	for _, shape := range f.Shapes {
		if shape == "landscape" && a.Width > a.Height || shape == "portrait" && a.Width < a.Height || shape == "square" && a.Width == a.Height {
			return true
		}
		if ratio := model.ShapeRatio(shape); ratio > 0 && float64(a.Width)/float64(a.Height) >= ratio*0.99 && float64(a.Width)/float64(a.Height) <= ratio*1.01 {
			return true
		}
	}
	return false
}

type YearCount struct {
	Year  string `json:"year"`
	Count int    `json:"count"`
}

func (s *Store) Years(f model.Filter) ([]YearCount, error) {
	f.Years = nil
	where, args := filters(f)
	rows, err := s.DB.Query("SELECT "+yearSQL+",count(*) FROM works w JOIN authors a ON a.id=w.author"+where+" GROUP BY 1 ORDER BY 1 DESC", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []YearCount{}
	for rows.Next() {
		var year YearCount
		if err = rows.Scan(&year.Year, &year.Count); err != nil {
			return nil, err
		}
		out = append(out, year)
	}
	return out, rows.Err()
}

type AuthorCount struct {
	Platform string `json:"platform"`
	ID       string `json:"id"`
	Name     string `json:"name"`
	Count    int    `json:"count"`
}

func (s *Store) Authors(f model.Filter, prefix string) ([]AuthorCount, error) {
	f.Author = ""
	where, args := filters(f)
	where += " AND a.name<>'' AND instr(lower(a.name),lower(?))>0"
	args = append(args, prefix)
	rows, err := s.DB.Query("SELECT a.platform,a.source_id,a.name,count(*) FROM works w JOIN authors a ON a.id=w.author"+where+" GROUP BY a.id ORDER BY count(*) DESC,a.name,a.id LIMIT 100", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AuthorCount{}
	for rows.Next() {
		var author AuthorCount
		if err = rows.Scan(&author.Platform, &author.ID, &author.Name, &author.Count); err != nil {
			return nil, err
		}
		out = append(out, author)
	}
	return out, rows.Err()
}
