package api

import (
	"context"
	"embed"
	"html/template"
	"io/fs"
	"net/http"
	"time"

	"cert-live/internal/auth"
	"cert-live/internal/config"
	"cert-live/internal/scheduler"
	"cert-live/internal/store"
)

// Server 持有运行期依赖。routes() 在 ListenAndServe 时构建路由树。
type Server struct {
	cfg       *config.Config
	st        *store.Store
	scheduler *scheduler.Scheduler
	limiter   *auth.LoginLimiter
	assets    embed.FS
	http      *http.Server
}

func New(cfg *config.Config, st *store.Store, assets embed.FS) *Server {
	ll := auth.NewLoginLimiter()
	ll.StartCleanup()
	return &Server{
		cfg:       cfg,
		st:        st,
		assets:    assets,
		scheduler: scheduler.New(st),
		limiter:   ll,
	}
}

// loadTemplates 从 embed.FS 解析 templates/ 下的所有 *.html
func (s *Server) loadTemplates() *template.Template {
	t := template.New("").Funcs(template.FuncMap{
		"abs": func(v int) int { // 已过期天数显示用，避免模板里出现 "-8 天"
			if v < 0 {
				return -v
			}
			return v
		},
	})
	return template.Must(t.ParseFS(s.assets, "templates/*.html"))
}

// staticFS 返回去一层 "static/" 前缀的子 FS，gin 可以直接 StaticFS 挂载
func (s *Server) staticFS() fs.FS {
	sub, err := fs.Sub(s.assets, "static")
	if err != nil {
		panic(err) // embed 编译期就该能确定，到这里错就是程序 bug
	}
	return sub
}

// StartScheduler 启动后台循环：每 5 分钟一轮「探测所有域名 → 扫库 → 推送」
func (s *Server) StartScheduler(ctx context.Context) {
	go s.scheduler.Run(ctx)
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