package api

import (
	"context"
	"net/http"
	"time"

	"cert-live/internal/config"
	"cert-live/internal/scheduler"
	"cert-live/internal/store"
)

// Server 持有运行期依赖。routes() 在 ListenAndServe 时构建路由树。
type Server struct {
	cfg       *config.Config
	st        *store.Store
	scheduler *scheduler.Scheduler
	http      *http.Server
}

func New(cfg *config.Config, st *store.Store) *Server {
	return &Server{
		cfg:       cfg,
		st:        st,
		scheduler: scheduler.New(st),
	}
}

// StartScheduler 后台运行定时证书探测
func (s *Server) StartScheduler(ctx context.Context) {
	go s.scheduler.Run(ctx)
}

// StartNotifyScheduler 后台运行通知推送扫描（5 分钟一次）
func (s *Server) StartNotifyScheduler(ctx context.Context) {
	go s.scheduler.RunNotify(ctx)
}

// Run 监听 addr，阻塞直至出错
func (s *Server) Run(addr string) error {
	s.http = &http.Server{
		Addr:              addr,
		Handler:           s.routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	return s.http.ListenAndServe()
}

// Shutdown 优雅关闭
func (s *Server) Shutdown(ctx context.Context) error {
	if s.http == nil {
		return nil
	}
	return s.http.Shutdown(ctx)
}