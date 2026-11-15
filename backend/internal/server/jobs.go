package server

import (
	"collection/internal/importer"
	"collection/internal/model"
	"collection/internal/queue"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type jobInput struct {
	Kind     string   `json:"kind"`
	Source   string   `json:"source,omitempty"`
	URLs     []string `json:"urls,omitempty"`
	Path     string   `json:"path,omitempty"`
	UploadID string   `json:"uploadId,omitempty"`
	Rating   *int     `json:"rating,omitempty"`
	Format   string   `json:"format,omitempty"`
}

func (s *Server) createJob(w http.ResponseWriter, r *http.Request) {
	if !s.requireWriter(w, r) {
		return
	}
	s.uploadMu.Lock()
	defer s.uploadMu.Unlock()
	s.idempotent(w, r, 128*1024, func(tx *sql.Tx, raw []byte) (int, string, any, error) {
		var in jobInput
		if e := parseBody(raw, &in); e != nil {
			return 0, "", nil, e
		}
		if in.Kind != "import" && in.Kind != "migration" {
			return 0, "", nil, problem(422, "invalid_kind", "任务类型无效")
		}
		if in.Format != "" && in.Format != "auto" && in.Format != "fanbox" {
			return 0, "", nil, problem(422, "invalid_format", "导入格式无效")
		}
		rating := 0
		if in.Format == "fanbox" {
			rating = 1
		}
		if in.Rating != nil {
			rating = *in.Rating
		}
		if rating < 0 || rating > 2 {
			return 0, "", nil, problem(422, "invalid_rating", "分级无效")
		}
		b := queue.BatchInput{Source: in.Source, URLs: in.URLs, Path: in.Path, UploadID: in.UploadID, Rating: rating, Format: in.Format}
		owner := identity(r)
		if owner != "admin" && (in.Kind != "import" || in.Source != "upload") {
			return 0, "", nil, problem(403, "forbidden", "采集令牌仅允许导入自己的上传")
		}
		if in.Kind == "migration" {
			if in.Source != "" || len(in.URLs) > 0 || in.Path != "" || in.UploadID != "" {
				return 0, "", nil, problem(422, "invalid_source", "迁移任务无需来源")
			}
			b.Source = "migration"
		} else {
			switch in.Source {
			case "url":
				if len(in.URLs) < 1 || len(in.URLs) > 1000 || in.Path != "" || in.UploadID != "" {
					return 0, "", nil, problem(422, "invalid_urls", "每批支持 1 至 1000 个链接")
				}
				for i, u := range b.URLs {
					_, _, _, canonical, e := queue.NormalizeURL(strings.TrimSpace(u))
					if e != nil {
						return 0, "", nil, problem(422, "invalid_url", "作品链接无效")
					}
					b.URLs[i] = canonical
				}
			case "server":
				if len(in.URLs) > 0 || in.UploadID != "" {
					return 0, "", nil, problem(422, "invalid_source", "来源字段冲突")
				}
				path, e := importer.Directory(in.Path, s.Config.DataDir)
				if e != nil {
					return 0, "", nil, problem(422, "invalid_directory", e.Error())
				}
				b.Path = path
			case "upload":
				if in.UploadID == "" || len(in.URLs) > 0 || in.Path != "" {
					return 0, "", nil, problem(422, "invalid_source", "上传来源无效")
				}
				var manifest, status string
				var job sql.NullInt64
				e := tx.QueryRow("SELECT manifest,status,job_id FROM uploads WHERE id=?", in.UploadID).Scan(&manifest, &status, &job)
				if errors.Is(e, sql.ErrNoRows) {
					return 0, "", nil, problem(404, "not_found", "上传不存在")
				}
				if e != nil {
					return 0, "", nil, e
				}
				if owner != "admin" {
					var stored string
					if tx.QueryRow("SELECT owner FROM upload_owners WHERE upload_id=?", in.UploadID).Scan(&stored) != nil || stored != owner {
						return 0, "", nil, problem(404, "not_found", "上传不存在")
					}
				}
				if job.Valid {
					return 0, "", nil, problem(409, "upload_closed", "该上传已创建导入任务")
				}
				if status != "uploading" {
					return 0, "", nil, problem(409, "upload_closed", "上传已结束")
				}
				var files []uploadFile
				if e = json.Unmarshal([]byte(manifest), &files); e != nil {
					return 0, "", nil, e
				}
				for _, f := range files {
					path := s.uploadPath(in.UploadID, f.Path)
					info, e := os.Stat(path)
					if f.Size == 0 && os.IsNotExist(e) {
						if e = os.MkdirAll(filepath.Dir(path), 0700); e == nil {
							e = os.WriteFile(path, nil, 0600)
						}
						if e != nil {
							return 0, "", nil, e
						}
						continue
					}
					if e != nil || info.Size() != f.Size {
						return 0, "", nil, problem(409, "upload_incomplete", "还有文件尚未上传完成")
					}
				}
			default:
				return 0, "", nil, problem(422, "invalid_source", "导入来源无效")
			}
		}
		encoded, _ := json.Marshal(b)
		now := model.Now()
		result, e := tx.Exec("INSERT INTO jobs(kind,job_key,input,status,created_at,updated_at) VALUES('batch',?,?,'queued',?,?)", owner+":"+r.Header.Get("Idempotency-Key"), string(encoded), now, now)
		if e != nil {
			return 0, "", nil, e
		}
		id, e := result.LastInsertId()
		if e != nil {
			return 0, "", nil, e
		}
		if _, e = tx.Exec("INSERT INTO job_owners(job_id,owner) VALUES(?,?)", id, owner); e != nil {
			return 0, "", nil, e
		}
		if b.Source == "upload" {
			if _, e = tx.Exec("UPDATE uploads SET status='queued',job_id=?,updated_at=? WHERE id=?", id, now, in.UploadID); e != nil {
				return 0, "", nil, e
			}
		}
		j := model.Job{ID: id, Kind: "batch", Input: string(encoded), Status: "queued", CreatedAt: now, UpdatedAt: now}
		return 202, "/api/v1/jobs/" + strconv.FormatInt(id, 10), jobResponse(j), nil
	})
}
func (s *Server) listJobs(w http.ResponseWriter, r *http.Request) {
	if !s.requireWriter(w, r) {
		return
	}
	page, size, e := pagination(r)
	if e != nil {
		s.report(w, e)
		return
	}
	where := ""
	args := []any{}
	if identity(r) != "admin" {
		where = " WHERE id IN (SELECT job_id FROM job_owners WHERE owner=?)"
		args = append(args, identity(r))
	}
	var total int
	if e = s.Store.DB.QueryRow("SELECT count(*) FROM jobs"+where, args...).Scan(&total); e != nil {
		s.internal(w, e)
		return
	}
	rows, e := s.Store.DB.Query("SELECT id FROM jobs"+where+" ORDER BY id DESC LIMIT ? OFFSET ?", append(args, size, (page-1)*size)...)
	if e != nil {
		s.internal(w, e)
		return
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if e = rows.Scan(&id); e != nil {
			break
		}
		ids = append(ids, id)
	}
	if e == nil {
		e = rows.Err()
	}
	rows.Close()
	if e != nil {
		s.internal(w, e)
		return
	}
	items := []any{}
	for _, id := range ids {
		j, e := s.Store.Job(id)
		if e != nil {
			s.internal(w, e)
			return
		}
		items = append(items, jobResponse(j))
	}
	respond(w, 200, collectionPage(items, total, page, size))
}
func (s *Server) authorizedJob(w http.ResponseWriter, r *http.Request) (model.Job, bool) {
	if !s.requireWriter(w, r) {
		return model.Job{}, false
	}
	id, ok := pathID(w, r)
	if !ok {
		return model.Job{}, false
	}
	if !s.owns(r, "job", strconv.FormatInt(id, 10)) {
		fail(w, 404, "not_found", "任务不存在")
		return model.Job{}, false
	}
	j, e := s.Store.Job(id)
	if errors.Is(e, sql.ErrNoRows) {
		fail(w, 404, "not_found", "任务不存在")
		return j, false
	}
	if e != nil {
		s.internal(w, e)
		return j, false
	}
	return j, true
}
func (s *Server) getJob(w http.ResponseWriter, r *http.Request) {
	j, ok := s.authorizedJob(w, r)
	if ok {
		respond(w, 200, jobResponse(j))
	}
}
func (s *Server) jobItems(w http.ResponseWriter, r *http.Request) {
	j, ok := s.authorizedJob(w, r)
	if !ok {
		return
	}
	page, size, e := pagination(r)
	if e != nil {
		s.report(w, e)
		return
	}
	total := len(j.Items)
	a := min((page-1)*size, total)
	b := min(a+size, total)
	respond(w, 200, collectionPage(j.Items[a:b], total, page, size))
}
func (s *Server) patchJob(w http.ResponseWriter, r *http.Request) {
	j, ok := s.authorizedJob(w, r)
	if !ok {
		return
	}
	s.idempotent(w, r, 1024, func(tx *sql.Tx, raw []byte) (int, string, any, error) {
		var in struct {
			Status string `json:"status"`
		}
		if e := parseBody(raw, &in); e != nil {
			return 0, "", nil, e
		}
		if in.Status != "queued" {
			return 0, "", nil, problem(422, "invalid_status", "仅支持重新排队")
		}
		var status string
		if e := tx.QueryRow("SELECT status FROM jobs WHERE id=?", j.ID).Scan(&status); e != nil {
			return 0, "", nil, e
		}
		if status != "failed" && status != "partial" && status != "interrupted" {
			return 0, "", nil, problem(409, "retry_failed", "任务当前不能重试")
		}
		if _, e := tx.Exec("UPDATE job_items SET status='queued',error='' WHERE job_id=? AND status IN ('failed','running')", j.ID); e != nil {
			return 0, "", nil, e
		}
		j.Status = "queued"
		j.Error = ""
		j.UpdatedAt = model.Now()
		_, e := tx.Exec("UPDATE jobs SET status='queued',error='',updated_at=? WHERE id=?", j.UpdatedAt, j.ID)
		return 202, "/api/v1/jobs/" + strconv.FormatInt(j.ID, 10), jobResponse(j), e
	})
}
