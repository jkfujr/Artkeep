package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type Config struct {
	Listen            string `json:"listen"`
	DataDir           string `json:"data_dir"`
	PixivDir          string `json:"pixiv_dir"`
	GalleryDL         string `json:"gallery_dl"`
	FFmpeg            string `json:"ffmpeg"`
	FFprobe           string `json:"ffprobe"`
	CookiesFile       string `json:"cookies_file"`
	Proxy             string `json:"proxy"`
	PixivRefreshToken string `json:"pixiv_refresh_token"`
}

func Load(path string) (Config, error) {
	c := Config{Listen: "127.0.0.1:8080", DataDir: "./data", GalleryDL: "gallery-dl", FFmpeg: "ffmpeg", FFprobe: "ffprobe"}
	if path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return c, err
		}
		if err = json.Unmarshal(b, &c); err != nil {
			return c, fmt.Errorf("配置文件格式错误: %w", err)
		}
	}
	for key, ptr := range map[string]*string{"ARCHIVE_LISTEN": &c.Listen, "ARCHIVE_DATA_DIR": &c.DataDir, "ARCHIVE_PIXIV_DIR": &c.PixivDir, "ARCHIVE_GALLERY_DL": &c.GalleryDL, "ARCHIVE_FFMPEG": &c.FFmpeg, "ARCHIVE_FFPROBE": &c.FFprobe, "ARCHIVE_COOKIES_FILE": &c.CookiesFile, "ARCHIVE_PROXY": &c.Proxy} {
		if v, ok := os.LookupEnv(key); ok {
			*ptr = v
		}
	}
	var err error
	if v, ok := os.LookupEnv("ARCHIVE_PIXIV_REFRESH_TOKEN"); ok {
		c.PixivRefreshToken = v
	}
	if c.DataDir, err = filepath.Abs(c.DataDir); err != nil {
		return c, err
	}
	if c.PixivDir != "" {
		if c.PixivDir, err = filepath.Abs(c.PixivDir); err != nil {
			return c, err
		}
	}
	if c.CookiesFile != "" {
		if c.CookiesFile, err = filepath.Abs(c.CookiesFile); err != nil {
			return c, err
		}
	}
	for _, dir := range []string{c.DataDir, c.DownloadDir(), c.CacheDir(), filepath.Join(c.DataDir, "staging")} {
		if err = os.MkdirAll(dir, 0700); err != nil {
			return c, err
		}
	}
	return c, nil
}

func (c Config) DownloadDir() string { return filepath.Join(c.DataDir, "collection") }
func (c Config) CacheDir() string    { return filepath.Join(c.DataDir, "previews") }
func (c Config) Roots() map[string]string {
	return map[string]string{"pixiv": c.PixivDir, "collection": c.DownloadDir()}
}
