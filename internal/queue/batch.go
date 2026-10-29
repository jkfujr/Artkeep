package queue

import (
	"collection/internal/importer"
	"collection/internal/model"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type BatchInput struct {
	Source   string   `json:"source"`
	URLs     []string `json:"urls,omitempty"`
	Path     string   `json:"path,omitempty"`
	UploadID string   `json:"uploadId,omitempty"`
	Rating   int      `json:"rating"`
}

func (q *Queue) collection() *importer.Collection {
	return &importer.Collection{Store: q.Store, Media: q.Media, Config: q.Config}
}
func (q *Queue) batch(ctx context.Context, j *model.Job) error {
	var input BatchInput
	if err := json.Unmarshal([]byte(j.Input), &input); err != nil {
		return errors.New("导入任务内容无效")
	}
	items, err := q.Store.JobItems(j.ID)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		switch input.Source {
		case "url":
			seen := map[string]bool{}
			for _, raw := range input.URLs {
				raw = strings.TrimSpace(raw)
				if raw == "" {
					continue
				}
				_, _, _, canonical, e := NormalizeURL(raw)
				if e == nil {
					raw = canonical
				}
				if seen[raw] {
					continue
				}
				seen[raw] = true
				items = append(items, model.JobItem{Input: raw, Payload: raw})
			}
		case "migration":
			ids, e := q.Store.MigrationIDs()
			if e != nil {
				return e
			}
			for _, id := range ids {
				v := strconv.FormatInt(id, 10)
				items = append(items, model.JobItem{Input: "作品 " + v, Payload: v})
			}
		case "server", "upload":
			root := input.Path
			if input.Source == "server" {
				root, err = importer.Directory(root, q.Config.DataDir)
				if err != nil {
					return err
				}
			} else {
				root = filepath.Join(q.Config.DataDir, "staging", "upload-"+input.UploadID, "files")
			}
			q.Store.Transfer(j.ID, "parsing", 0, 0)
			candidates, e := q.collection().Discover(ctx, root, input.Rating)
			if e != nil {
				return e
			}
			for _, v := range candidates {
				raw, e := json.Marshal(v)
				if e != nil {
					return e
				}
				name := v.Work.Title
				if name == "" {
					name = v.Work.SourceID
				}
				items = append(items, model.JobItem{Input: name, Payload: string(raw)})
			}
		default:
			return errors.New("不支持的导入来源")
		}
		if err = q.Store.AddJobItems(j.ID, items); err != nil {
			return err
		}
		items, err = q.Store.JobItems(j.ID)
		if err != nil {
			return err
		}
	}
	j.Total = len(items)
	j.Progress = 0
	for _, v := range items {
		if v.Status == "completed" || v.Status == "existing" {
			j.Progress++
		}
	}
	if err = q.Store.UpdateJob(*j); err != nil {
		return err
	}
	failed := 0
	for _, v := range items {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if v.Status == "completed" || v.Status == "existing" {
			continue
		}
		v.Status = "running"
		v.Error = ""
		if err = q.Store.UpdateItem(v); err != nil {
			return err
		}
		var id int64
		var exists bool
		var done int64
		q.Store.Transfer(j.ID, "copying", 0, 0)
		progress := func(n int64) {
			done += n
			if done%(8*1024*1024) < n {
				q.Store.Transfer(j.ID, "copying", done, 0)
			}
		}
		switch input.Source {
		case "migration":
			id, err = strconv.ParseInt(v.Payload, 10, 64)
			if err == nil {
				err = q.collection().Migrate(ctx, id, progress)
			}
		case "url":
			id, exists, err = q.downloadURL(ctx, j.ID, v.Payload, input.Rating, progress)
		default:
			var candidate importer.Candidate
			err = json.Unmarshal([]byte(v.Payload), &candidate)
			if err == nil {
				id, exists, err = q.collection().Import(ctx, candidate, progress)
			}
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		q.Store.Transfer(j.ID, "copying", done, done)
		if err != nil {
			v.Status = "failed"
			v.Error = err.Error()
			failed++
		} else {
			v.Status = "completed"
			if exists {
				v.Status = "existing"
			}
			v.WorkID = &id
		}
		if e := q.Store.UpdateItem(v); e != nil {
			return e
		}
		j.Progress++
		if e := q.Store.UpdateJob(*j); e != nil {
			return e
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d 部作品失败", failed)
	}
	if input.Source == "upload" {
		dir := filepath.Join(q.Config.DataDir, "staging", "upload-"+input.UploadID)
		if !importer.Within(filepath.Join(q.Config.DataDir, "staging"), dir) {
			return errors.New("上传路径无效")
		}
		if err = os.RemoveAll(dir); err != nil {
			return err
		}
		_, err = q.Store.DB.Exec("UPDATE uploads SET status='imported',updated_at=? WHERE id=?", model.Now(), input.UploadID)
		if err != nil {
			return err
		}
	}
	return nil
}
