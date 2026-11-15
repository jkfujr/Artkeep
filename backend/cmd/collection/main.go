package main

import (
	"collection/internal/config"
	"collection/internal/media"
	"collection/internal/queue"
	"collection/internal/server"
	"collection/internal/store"
	frontend "collection/web"
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	configFile := flag.String("config", "", "配置文件路径")
	reset := flag.Bool("set-admin-password", false, "从 ARCHIVE_ADMIN_PASSWORD 环境变量更新管理员密码后退出")
	flag.Parse()
	c, err := config.Load(*configFile)
	if err != nil {
		log.Fatal(err)
	}
	st, err := store.Open(c.DataDir)
	if err != nil {
		log.Fatal(err)
	}
	defer st.DB.Close()
	if *reset {
		if err = server.ResetAdmin(st); err != nil {
			log.Fatal(err)
		}
		log.Print("管理员密码已更新")
		return
	}
	m := media.New(c)
	q := queue.New(st, m, c)
	app, err := server.New(st, m, q, c, frontend.Files())
	if err != nil {
		log.Fatal(err)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	workerDone := make(chan struct{})
	go func() { defer close(workerDone); q.Run(ctx) }()
	httpServer := &http.Server{Addr: c.Listen, Handler: app.Handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 20}
	go func() {
		<-ctx.Done()
		shutdown, done := context.WithTimeout(context.Background(), 15*time.Second)
		defer done()
		httpServer.Shutdown(shutdown)
	}()
	log.Printf("作品收藏已启动: http://%s", c.Listen)
	if err = httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		cancel()
		log.Print(err)
	}
	cancel()
	<-workerDone
}
