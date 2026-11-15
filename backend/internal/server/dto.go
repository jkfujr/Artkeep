package server

import (
	"collection/internal/model"
	"fmt"
)

type assetDTO struct {
	model.Asset
	URL          string `json:"url"`
	Thumbnail    string `json:"thumbnail"`
	Preview      string `json:"preview"`
	PlaybackURL  string `json:"playbackUrl,omitempty"`
	PlaybackMime string `json:"playbackMime,omitempty"`
}
type workDTO struct {
	model.Work
	Cover  *assetDTO  `json:"cover"`
	Assets []assetDTO `json:"assets,omitempty"`
}

func assetResponse(a model.Asset) assetDTO {
	base := fmt.Sprintf("/api/v1/assets/%d", a.ID)
	version := fmt.Sprintf("?v=%d-%d", a.Size, a.Modified)
	v := assetDTO{Asset: a, URL: base + "/original", Thumbnail: base + "/thumbnail" + version, Preview: base + "/preview" + version}
	if a.Format == "ugoira" {
		v.PlaybackURL = base + "/playback" + version
		v.PlaybackMime = "video/mp4"
	} else if a.Kind == "video" || a.Format == "mp4" || a.Format == "webm" {
		v.PlaybackURL = v.URL
		v.PlaybackMime = "video/" + a.Format
	}
	return v
}
func workResponse(w model.Work) workDTO {
	v := workDTO{Work: w}
	if w.Cover != nil {
		a := assetResponse(*w.Cover)
		v.Cover = &a
	}
	if w.Assets != nil {
		v.Assets = make([]assetDTO, len(w.Assets))
		for i, a := range w.Assets {
			v.Assets[i] = assetResponse(a)
		}
	}
	return v
}
