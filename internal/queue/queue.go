package queue

import (
	"collection/internal/config"
	"collection/internal/importer"
	"collection/internal/media"
	"collection/internal/model"
	"collection/internal/store"
	"context"
	"database/sql"
	"errors"
	"log"
	"sync"
	"time"
)

type Queue struct {
	Store  *store.Store
	Media  *media.Service
	Config config.Config
	wake   chan struct{}
	mu     sync.Mutex
}

func New(s *store.Store, m *media.Service, c config.Config) *Queue {
	return &Queue{Store: s, Media: m, Config: c, wake: make(chan struct{}, 1)}
}
func (q *Queue) Notify() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}
func (q *Queue) Enqueue(kind, key, input string) (model.Job, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	j, err := q.Store.NewJob(kind, key, input)
	if err == nil {
		q.Notify()
	}
	return j, err
}
func (q *Queue) Retry(id int64) (model.Job, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	j, err := q.Store.RetryJob(id)
	if err == nil {
		q.Notify()
	}
	return j, err
}

func (q *Queue) Run(ctx context.Context) {
	if err := q.Store.InterruptJobs(); err != nil {
		log.Printf("恢复任务状态失败: %v", err)
	}
	for {
		if ctx.Err() != nil {
			return
		}
		j, err := q.Store.NextJob()
		if errors.Is(err, sql.ErrNoRows) {
			select {
			case <-ctx.Done():
				return
			case <-q.wake:
			case <-time.After(5 * time.Second):
			}
			continue
		}
		if err != nil {
			log.Printf("读取任务失败: %v", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
			continue
		}
		j.Status = "running"
		j.Error = ""
		if err = q.Store.UpdateJob(j); err != nil {
			log.Printf("更新任务失败: %v", err)
			continue
		}
		switch j.Kind {
		case "pixiv":
			p := importer.Pixiv{Store: q.Store, Media: q.Media, Root: q.Config.PixivDir}
			_, err = p.Scan(ctx, func(done, total int, message string) {
				j.Progress = done
				j.Total = total
				j.Error = message
				if e := q.Store.UpdateJob(j); e != nil {
					log.Printf("更新导入进度失败: %v", e)
				}
			})
		case "x":
			var id int64
			id, err = q.downloadX(ctx, &j)
			if err == nil {
				j.WorkID = &id
			}
		default:
			err = errors.New("不支持的任务类型")
		}
		if ctx.Err() != nil {
			j.Status = "interrupted"
			j.Error = "服务已停止，可重试继续"
		} else if err != nil {
			j.Status = "failed"
			j.Error = err.Error()
		} else {
			j.Status = "completed"
			j.Error = ""
			j.Progress = j.Total
		}
		if e := q.Store.UpdateJob(j); e != nil {
			log.Printf("保存任务结果失败: %v", e)
		}
	}
}
