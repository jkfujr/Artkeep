package store

import (
	"collection/internal/model"
	"database/sql"
	"errors"
	"fmt"
	_ "modernc.org/sqlite"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
)

type Store struct{ DB *sql.DB }

func Open(dataDir string) (*Store, error) {
	databasePath := filepath.ToSlash(filepath.Join(dataDir, "collection.db"))
	if filepath.VolumeName(dataDir) != "" {
		databasePath = "/" + databasePath
	}
	u := url.URL{Scheme: "file", Path: databasePath}
	q := u.Query()
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "journal_mode(WAL)")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{DB: db}
	if _, err = db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

const schema = `
CREATE TABLE IF NOT EXISTS authors(id INTEGER PRIMARY KEY, platform TEXT NOT NULL, source_id TEXT NOT NULL, name TEXT NOT NULL, url TEXT NOT NULL, UNIQUE(platform,source_id));
CREATE TABLE IF NOT EXISTS works(id INTEGER PRIMARY KEY, platform TEXT NOT NULL, source_kind TEXT NOT NULL, source_id TEXT NOT NULL, source_url TEXT NOT NULL, title TEXT NOT NULL, description TEXT NOT NULL, author INTEGER NOT NULL REFERENCES authors(id), published_at TEXT NOT NULL, added_at TEXT NOT NULL, source_rating INTEGER NOT NULL DEFAULT 0, rating_override INTEGER CHECK(rating_override BETWEEN 0 AND 2), available INTEGER NOT NULL DEFAULT 1, ready INTEGER NOT NULL DEFAULT 1, raw_metadata TEXT NOT NULL, root TEXT NOT NULL, metadata_path TEXT NOT NULL, fingerprint TEXT NOT NULL, UNIQUE(platform,source_kind,source_id));
CREATE INDEX IF NOT EXISTS works_published ON works(ready,available,published_at DESC,id DESC);
CREATE INDEX IF NOT EXISTS works_author ON works(author);
CREATE TABLE IF NOT EXISTS assets(id INTEGER PRIMARY KEY, work_id INTEGER NOT NULL REFERENCES works(id), root TEXT NOT NULL, path TEXT NOT NULL, position INTEGER NOT NULL, kind TEXT NOT NULL, format TEXT NOT NULL, width INTEGER NOT NULL, height INTEGER NOT NULL, duration REAL NOT NULL DEFAULT 0, size INTEGER NOT NULL, modified INTEGER NOT NULL, available INTEGER NOT NULL DEFAULT 1, UNIQUE(work_id,root,path));
CREATE INDEX IF NOT EXISTS assets_work ON assets(work_id,position,id);
CREATE TABLE IF NOT EXISTS tags(id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE);
CREATE TABLE IF NOT EXISTS work_tags(work_id INTEGER NOT NULL REFERENCES works(id), tag_id INTEGER NOT NULL REFERENCES tags(id), origin TEXT NOT NULL CHECK(origin IN ('source','local')), PRIMARY KEY(work_id,tag_id,origin));
CREATE INDEX IF NOT EXISTS work_tags_tag ON work_tags(tag_id,work_id);
CREATE TABLE IF NOT EXISTS jobs(id INTEGER PRIMARY KEY, kind TEXT NOT NULL, job_key TEXT NOT NULL, input TEXT NOT NULL, status TEXT NOT NULL, progress INTEGER NOT NULL DEFAULT 0, total INTEGER NOT NULL DEFAULT 0, error TEXT NOT NULL DEFAULT '', work_id INTEGER REFERENCES works(id), created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS jobs_status ON jobs(status,id);
CREATE UNIQUE INDEX IF NOT EXISTS jobs_active ON jobs(kind,job_key) WHERE status IN ('queued','running');
CREATE TABLE IF NOT EXISTS settings(key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS sessions(token_hash TEXT PRIMARY KEY, scope TEXT NOT NULL, expires INTEGER NOT NULL, version TEXT NOT NULL);
PRAGMA user_version=1;
`

func (s *Store) Setting(key string) (string, error) {
	var v string
	err := s.DB.QueryRow("SELECT value FROM settings WHERE key=?", key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}
func (s *Store) SetSetting(key, value string) error {
	_, err := s.DB.Exec("INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", key, value)
	return err
}

func (s *Store) Find(platform, kind, id string) (int64, string, bool, error) {
	var workID int64
	var fp string
	var ready bool
	err := s.DB.QueryRow("SELECT id,fingerprint,ready FROM works WHERE platform=? AND source_kind=? AND source_id=?", platform, kind, id).Scan(&workID, &fp, &ready)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", false, nil
	}
	return workID, fp, ready, err
}

func (s *Store) UpsertWork(w *model.Work, assets []model.Asset) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.Exec("INSERT INTO authors(platform,source_id,name,url) VALUES(?,?,?,?) ON CONFLICT(platform,source_id) DO UPDATE SET name=excluded.name,url=excluded.url", w.Platform, w.AuthorID, w.AuthorName, w.AuthorURL)
	if err != nil {
		return err
	}
	var author int64
	if err = tx.QueryRow("SELECT id FROM authors WHERE platform=? AND source_id=?", w.Platform, w.AuthorID).Scan(&author); err != nil {
		return err
	}
	err = tx.QueryRow(`INSERT INTO works(platform,source_kind,source_id,source_url,title,description,author,published_at,added_at,source_rating,available,ready,raw_metadata,root,metadata_path,fingerprint) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(platform,source_kind,source_id) DO UPDATE SET source_url=excluded.source_url,title=excluded.title,description=excluded.description,author=excluded.author,published_at=excluded.published_at,source_rating=excluded.source_rating,available=excluded.available,ready=excluded.ready,raw_metadata=excluded.raw_metadata,root=excluded.root,metadata_path=excluded.metadata_path,fingerprint=excluded.fingerprint RETURNING id`, w.Platform, w.SourceKind, w.SourceID, w.SourceURL, w.Title, w.Description, author, w.PublishedAt, model.Now(), w.SourceRating, len(assets) > 0, w.Ready, w.Raw, w.Root, w.MetadataPath, w.Fingerprint).Scan(&w.ID)
	if err != nil {
		return err
	}
	if _, err = tx.Exec("UPDATE assets SET available=0 WHERE work_id=?", w.ID); err != nil {
		return err
	}
	for i := range assets {
		a := &assets[i]
		a.WorkID = w.ID
		err = tx.QueryRow(`INSERT INTO assets(work_id,root,path,position,kind,format,width,height,duration,size,modified,available) VALUES(?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(work_id,root,path) DO UPDATE SET position=excluded.position,kind=excluded.kind,format=excluded.format,width=excluded.width,height=excluded.height,duration=excluded.duration,size=excluded.size,modified=excluded.modified,available=excluded.available RETURNING id`, w.ID, a.Root, a.Path, a.Position, a.Kind, a.Format, a.Width, a.Height, a.Duration, a.Size, a.Modified, a.Available).Scan(&a.ID)
		if err != nil {
			return err
		}
	}
	if err = replaceTags(tx, w.ID, "source", w.SourceTags); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	w.Assets = assets
	return nil
}

func replaceTags(tx *sql.Tx, id int64, origin string, tags []string) error {
	if _, err := tx.Exec("DELETE FROM work_tags WHERE work_id=? AND origin=?", id, origin); err != nil {
		return err
	}
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		if _, err := tx.Exec("INSERT OR IGNORE INTO tags(name) VALUES(?)", tag); err != nil {
			return err
		}
		if _, err := tx.Exec("INSERT OR IGNORE INTO work_tags(work_id,tag_id,origin) SELECT ?,id,? FROM tags WHERE name=?", id, origin, tag); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) EditWork(id int64, tags []string, rating *int) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	r, err := tx.Exec("UPDATE works SET rating_override=? WHERE id=?", rating, id)
	if err != nil {
		return err
	}
	n, _ := r.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	if err = replaceTags(tx, id, "local", tags); err != nil {
		return err
	}
	return tx.Commit()
}

const workSelect = `SELECT w.id,w.platform,w.source_kind,w.source_id,w.source_url,w.title,w.description,a.source_id,a.name,a.url,w.published_at,w.added_at,w.source_rating,w.rating_override,COALESCE(w.rating_override,w.source_rating),w.available,w.ready,w.root,w.metadata_path FROM works w JOIN authors a ON a.id=w.author `

type scanner interface{ Scan(...any) error }

func scanWork(r scanner) (model.Work, error) {
	var w model.Work
	err := r.Scan(&w.ID, &w.Platform, &w.SourceKind, &w.SourceID, &w.SourceURL, &w.Title, &w.Description, &w.AuthorID, &w.AuthorName, &w.AuthorURL, &w.PublishedAt, &w.AddedAt, &w.SourceRating, &w.RatingOverride, &w.Rating, &w.Available, &w.Ready, &w.Root, &w.MetadataPath)
	return w, err
}

func filters(f model.Filter) (string, []any) {
	where := []string{"w.ready=1"}
	args := []any{}
	if !f.Management {
		where = append(where, "w.available=1")
	}
	if !f.Adult {
		where = append(where, "COALESCE(w.rating_override,w.source_rating)=0")
	}
	if f.Platform != "" {
		where = append(where, "w.platform=?")
		args = append(args, f.Platform)
	}
	if f.Author != "" {
		where = append(where, "a.source_id=?")
		args = append(args, f.Author)
	}
	if f.Query != "" {
		where = append(where, `(instr(lower(w.title),lower(?))>0 OR instr(lower(w.description),lower(?))>0 OR instr(lower(a.name),lower(?))>0 OR w.source_id=? OR EXISTS(SELECT 1 FROM work_tags wt JOIN tags t ON t.id=wt.tag_id WHERE wt.work_id=w.id AND instr(lower(t.name),lower(?))>0))`)
		args = append(args, f.Query, f.Query, f.Query, f.Query, f.Query)
	}
	for _, tag := range f.Tags {
		where = append(where, "EXISTS(SELECT 1 FROM work_tags wt JOIN tags t ON t.id=wt.tag_id WHERE wt.work_id=w.id AND t.name=?)")
		args = append(args, tag)
	}
	if len(f.Years) > 0 {
		years := []string{}
		for _, year := range f.Years {
			if year == "unknown" {
				years = append(years, yearSQL+"='unknown'")
			} else {
				years = append(years, yearSQL+"=?")
				args = append(args, year)
			}
		}
		where = append(where, "("+strings.Join(years, " OR ")+")")
	}
	if f.ImageFiltered() {
		condition, values := imageCondition(f)
		where = append(where, "EXISTS(SELECT 1 FROM assets im WHERE im.work_id=w.id AND "+condition+")")
		args = append(args, values...)
	}
	return " WHERE " + strings.Join(where, " AND "), args
}

func (s *Store) List(f model.Filter) ([]model.Work, int, error) {
	where, args := filters(f)
	var total int
	if err := s.DB.QueryRow("SELECT count(*) FROM works w JOIN authors a ON a.id=w.author"+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	order := "w.published_at DESC,w.id DESC"
	if f.Sort == "added" {
		order = "w.added_at DESC,w.id DESC"
	}
	queryArgs := append(slices.Clone(args), f.PageSize, (f.Page-1)*f.PageSize)
	rows, err := s.DB.Query(workSelect+where+" ORDER BY "+order+" LIMIT ? OFFSET ?", queryArgs...)
	if err != nil {
		return nil, 0, err
	}
	works := []model.Work{}
	for rows.Next() {
		w, e := scanWork(rows)
		if e != nil {
			rows.Close()
			return nil, 0, e
		}
		works = append(works, w)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, 0, err
	}
	for i := range works {
		if err = s.hydrate(&works[i], false, f); err != nil {
			return nil, 0, err
		}
	}
	return works, total, nil
}

func (s *Store) Work(id int64, full bool) (model.Work, error) {
	w, err := scanWork(s.DB.QueryRow(workSelect+" WHERE w.id=?", id))
	if err != nil {
		return w, err
	}
	err = s.hydrate(&w, full, model.Filter{})
	return w, err
}

func (s *Store) hydrate(w *model.Work, full bool, f model.Filter) error {
	w.SourceTags = []string{}
	w.LocalTags = []string{}
	w.Tags = []string{}
	rows, err := s.DB.Query("SELECT t.name,wt.origin FROM work_tags wt JOIN tags t ON t.id=wt.tag_id WHERE wt.work_id=? ORDER BY t.name", w.ID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var name, origin string
		if err = rows.Scan(&name, &origin); err != nil {
			rows.Close()
			return err
		}
		if origin == "local" {
			w.LocalTags = append(w.LocalTags, name)
		} else {
			w.SourceTags = append(w.SourceTags, name)
		}
		if !slices.Contains(w.Tags, name) {
			w.Tags = append(w.Tags, name)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	assets, err := s.Assets(w.ID)
	if err != nil {
		return err
	}
	w.MediaCount = len(assets)
	for i := range assets {
		if assets[i].Available && assets[i].Kind != "document" && (!f.ImageFiltered() || matchesImage(assets[i], f)) {
			cover := assets[i]
			w.Cover = &cover
			break
		}
	}
	if full {
		w.Assets = assets
	}
	return nil
}

const assetColumns = "id,work_id,root,path,position,kind,format,width,height,duration,size,modified,available"

func scanAsset(r scanner) (model.Asset, error) {
	var a model.Asset
	err := r.Scan(&a.ID, &a.WorkID, &a.Root, &a.Path, &a.Position, &a.Kind, &a.Format, &a.Width, &a.Height, &a.Duration, &a.Size, &a.Modified, &a.Available)
	a.URL = fmt.Sprintf("/api/assets/%d/original", a.ID)
	version := fmt.Sprintf("?v=%d-%d", a.Size, a.Modified)
	a.Thumbnail = fmt.Sprintf("/api/assets/%d/thumb%s", a.ID, version)
	a.Preview = fmt.Sprintf("/api/assets/%d/preview%s", a.ID, version)
	return a, err
}
func (s *Store) Assets(id int64) ([]model.Asset, error) {
	rows, err := s.DB.Query("SELECT "+assetColumns+" FROM assets WHERE work_id=? ORDER BY position,id", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Asset{}
	for rows.Next() {
		a, e := scanAsset(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
func (s *Store) Asset(id int64) (model.Asset, int, error) {
	a, err := scanAsset(s.DB.QueryRow("SELECT "+assetColumns+" FROM assets WHERE id=?", id))
	if err != nil {
		return a, 0, err
	}
	var rating int
	err = s.DB.QueryRow("SELECT COALESCE(rating_override,source_rating) FROM works WHERE id=? AND ready=1 AND available=1", a.WorkID).Scan(&rating)
	return a, rating, err
}

type TagCount struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

func (s *Store) Tags(f model.Filter, prefix string) ([]TagCount, error) {
	where, args := filters(f)
	where += " AND instr(lower(t.name),lower(?))>0"
	args = append(args, prefix)
	rows, err := s.DB.Query("SELECT t.name,count(DISTINCT w.id) FROM work_tags wt JOIN tags t ON t.id=wt.tag_id JOIN works w ON w.id=wt.work_id JOIN authors a ON a.id=w.author"+where+" GROUP BY t.id ORDER BY count(DISTINCT w.id) DESC,t.name LIMIT 100", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TagCount{}
	for rows.Next() {
		var t TagCount
		if err = rows.Scan(&t.Name, &t.Count); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) SetReady(id int64, ready bool) error {
	_, err := s.DB.Exec("UPDATE works SET ready=? WHERE id=?", ready, id)
	return err
}
func (s *Store) MissingAsset(id int64) error {
	_, err := s.DB.Exec("UPDATE assets SET available=0 WHERE id=?", id)
	if err != nil {
		return err
	}
	_, err = s.DB.Exec("UPDATE works SET available=EXISTS(SELECT 1 FROM assets a WHERE a.work_id=works.id AND a.available=1) WHERE id=(SELECT work_id FROM assets WHERE id=?)", id)
	return err
}
func (s *Store) MarkMissingPixiv(seen map[int64]bool) error {
	rows, err := s.DB.Query("SELECT id FROM works WHERE platform='pixiv' AND root='pixiv'")
	if err != nil {
		return err
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		if !seen[id] {
			ids = append(ids, id)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, id := range ids {
		if _, err = tx.Exec("UPDATE works SET available=0 WHERE id=?", id); err != nil {
			return err
		}
	}
	return tx.Commit()
}
