package store

import (
	"collection/internal/model"
	"database/sql"
	"errors"
)

const jobColumns = "id,kind,job_key,input,status,progress,total,error,work_id,created_at,updated_at"

func scanJob(r scanner) (model.Job, error) {
	var j model.Job
	err := r.Scan(&j.ID, &j.Kind, &j.Key, &j.Input, &j.Status, &j.Progress, &j.Total, &j.Error, &j.WorkID, &j.CreatedAt, &j.UpdatedAt)
	return j, err
}
func (s *Store) Job(id int64) (model.Job, error) {
	j, err := scanJob(s.DB.QueryRow("SELECT "+jobColumns+" FROM jobs WHERE id=?", id))
	if err == nil {
		err = s.jobDetails(&j)
	}
	return j, err
}
func (s *Store) NewJob(kind, key, input string) (model.Job, error) {
	j, err := scanJob(s.DB.QueryRow("SELECT "+jobColumns+" FROM jobs WHERE kind=? AND job_key=? AND status IN ('queued','running') ORDER BY id DESC LIMIT 1", kind, key))
	if err == nil {
		return j, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return j, err
	}
	now := model.Now()
	result, err := s.DB.Exec("INSERT INTO jobs(kind,job_key,input,status,created_at,updated_at) VALUES(?,?,?,'queued',?,?)", kind, key, input, now, now)
	if err != nil {
		return j, err
	}
	id, _ := result.LastInsertId()
	return s.Job(id)
}
func (s *Store) Jobs() ([]model.Job, error) {
	rows, err := s.DB.Query("SELECT " + jobColumns + " FROM jobs ORDER BY id DESC LIMIT 100")
	if err != nil {
		return nil, err
	}
	out := []model.Job{}
	for rows.Next() {
		j, e := scanJob(rows)
		if e != nil {
			rows.Close()
			return nil, e
		}
		out = append(out, j)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for i := range out {
		if err = s.jobDetails(&out[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}
func (s *Store) NextJob() (model.Job, error) {
	return scanJob(s.DB.QueryRow("SELECT " + jobColumns + " FROM jobs WHERE status='queued' ORDER BY id LIMIT 1"))
}
func (s *Store) UpdateJob(j model.Job) error {
	_, err := s.DB.Exec("UPDATE jobs SET status=?,progress=?,total=?,error=?,work_id=?,updated_at=? WHERE id=?", j.Status, j.Progress, j.Total, j.Error, j.WorkID, model.Now(), j.ID)
	return err
}
func (s *Store) InterruptJobs() error {
	_, err := s.DB.Exec("UPDATE jobs SET status='interrupted',error='服务已停止，可重试继续',updated_at=? WHERE status='running'", model.Now())
	return err
}
func (s *Store) RetryJob(id int64) (model.Job, error) {
	j, err := s.Job(id)
	if err != nil {
		return j, err
	}
	if j.Status != "failed" && j.Status != "interrupted" && j.Status != "partial" {
		return j, errors.New("该任务不能重试")
	}
	_, err = s.DB.Exec("UPDATE job_items SET status='queued',error='' WHERE job_id=? AND status IN ('failed','running')", id)
	if err != nil {
		return j, err
	}
	_, err = s.DB.Exec("UPDATE jobs SET status='queued',error='',updated_at=? WHERE id=?", model.Now(), id)
	if err != nil {
		return j, err
	}
	return s.Job(id)
}

func (s *Store) jobDetails(j *model.Job) error {
	items, err := s.JobItems(j.ID)
	if err != nil {
		return err
	}
	j.Items = items
	for _, item := range items {
		switch item.Status {
		case "completed":
			j.Succeeded++
		case "existing":
			j.Existing++
		case "failed":
			j.Failed++
		}
	}
	err = s.DB.QueryRow("SELECT phase,bytes,total FROM job_transfers WHERE job_id=?", j.ID).Scan(&j.Phase, &j.Bytes, &j.BytesTotal)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	return err
}
func (s *Store) JobItems(id int64) ([]model.JobItem, error) {
	rows, err := s.DB.Query("SELECT id,job_id,input,payload,status,error,work_id FROM job_items WHERE job_id=? ORDER BY id", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.JobItem{}
	for rows.Next() {
		var v model.JobItem
		if err = rows.Scan(&v.ID, &v.JobID, &v.Input, &v.Payload, &v.Status, &v.Error, &v.WorkID); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) AddJobItems(id int64, items []model.JobItem) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, v := range items {
		if _, err = tx.Exec("INSERT INTO job_items(job_id,input,payload) VALUES(?,?,?)", id, v.Input, v.Payload); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s *Store) UpdateItem(v model.JobItem) error {
	_, err := s.DB.Exec("UPDATE job_items SET status=?,error=?,work_id=? WHERE id=?", v.Status, v.Error, v.WorkID, v.ID)
	return err
}
func (s *Store) Transfer(id int64, phase string, done, total int64) error {
	_, err := s.DB.Exec("INSERT INTO job_transfers(job_id,phase,bytes,total) VALUES(?,?,?,?) ON CONFLICT(job_id) DO UPDATE SET phase=excluded.phase,bytes=excluded.bytes,total=excluded.total", id, phase, done, total)
	return err
}
