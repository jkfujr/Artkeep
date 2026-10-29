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
	return scanJob(s.DB.QueryRow("SELECT "+jobColumns+" FROM jobs WHERE id=?", id))
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
	defer rows.Close()
	out := []model.Job{}
	for rows.Next() {
		j, e := scanJob(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, j)
	}
	return out, rows.Err()
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
	if j.Status != "failed" && j.Status != "interrupted" {
		return j, errors.New("该任务不能重试")
	}
	_, err = s.DB.Exec("UPDATE jobs SET status='queued',error='',updated_at=? WHERE id=?", model.Now(), id)
	if err != nil {
		return j, err
	}
	return s.Job(id)
}
