package model

import "time"

type Asset struct {
	ID        int64   `json:"id,string"`
	WorkID    int64   `json:"workId,string"`
	Root      string  `json:"-"`
	Path      string  `json:"-"`
	Position  int     `json:"position"`
	Kind      string  `json:"kind"`
	Format    string  `json:"format"`
	Width     int     `json:"width"`
	Height    int     `json:"height"`
	Duration  float64 `json:"duration"`
	Size      int64   `json:"size"`
	Modified  int64   `json:"-"`
	Available bool    `json:"available"`
	URL       string  `json:"url"`
	Thumbnail string  `json:"thumbnail"`
	Preview   string  `json:"preview"`
}

type Work struct {
	ID             int64    `json:"id,string"`
	Platform       string   `json:"platform"`
	SourceKind     string   `json:"sourceKind"`
	SourceID       string   `json:"sourceId"`
	SourceURL      string   `json:"sourceUrl"`
	Title          string   `json:"title"`
	Description    string   `json:"description"`
	AuthorID       string   `json:"authorId"`
	AuthorName     string   `json:"authorName"`
	AuthorURL      string   `json:"authorUrl"`
	PublishedAt    string   `json:"publishedAt"`
	AddedAt        string   `json:"addedAt"`
	SourceRating   int      `json:"sourceRating"`
	RatingOverride *int     `json:"ratingOverride"`
	Rating         int      `json:"rating"`
	SourceTags     []string `json:"sourceTags"`
	LocalTags      []string `json:"localTags"`
	Tags           []string `json:"tags"`
	MediaCount     int      `json:"mediaCount"`
	Available      bool     `json:"available"`
	Ready          bool     `json:"-"`
	Cover          *Asset   `json:"cover"`
	Assets         []Asset  `json:"assets,omitempty"`
	Raw            string   `json:"-"`
	Root           string   `json:"-"`
	MetadataPath   string   `json:"-"`
	Fingerprint    string   `json:"-"`
}

type Job struct {
	ID        int64  `json:"id,string"`
	Kind      string `json:"kind"`
	Key       string `json:"-"`
	Input     string `json:"input"`
	Status    string `json:"status"`
	Progress  int    `json:"progress"`
	Total     int    `json:"total"`
	Error     string `json:"error"`
	WorkID    *int64 `json:"workId,string,omitempty"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

type Filter struct {
	Query      string
	Platform   string
	Author     string
	Tags       []string
	Adult      bool
	Sort       string
	Page       int
	PageSize   int
	Management bool
	Years      []string
	Shapes     []string
	MinWidth   int
	MinHeight  int
}

func (f Filter) ImageFiltered() bool { return len(f.Shapes) > 0 || f.MinWidth > 0 || f.MinHeight > 0 }

func ShapeRatio(shape string) float64 {
	switch shape {
	case "4:3":
		return 4.0 / 3
	case "16:9":
		return 16.0 / 9
	case "21:9":
		return 21.0 / 9
	case "3:4":
		return 3.0 / 4
	case "9:16":
		return 9.0 / 16
	case "9:21":
		return 9.0 / 21
	}
	return 0
}

func Now() string { return time.Now().UTC().Format(time.RFC3339Nano) }
