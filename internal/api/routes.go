package api

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"cert-live/internal/auth"
)

func (s *Server) routes() http.Handler {
	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery())

	// 模板与静态资源（与 data-board 一致：磁盘文件，不内嵌）
	r.LoadHTMLGlob("templates/*")
	r.StaticFS("/static", http.Dir("static"))

	// 登录相关（公开）
	r.GET("/login", s.LoginPage)
	r.POST("/login", s.LoginSubmit)
	r.GET("/logout", s.Logout)
	r.GET("/api/captcha", s.Captcha)

	// 后台页面（需登录）
	r.GET("/", auth.RequireAuth(), s.DashboardPage)
	r.GET("/domains", auth.RequireAuth(), s.DomainsPage)

	// 业务接口（需登录）
	api := r.Group("/api", auth.RequireAuth())
	{
		api.GET("/me", s.handleMe)

		api.GET("/domains", s.handleListDomains)
		api.POST("/domains", s.handleCreateDomain)
		api.PUT("/domains/:id", s.handleUpdateDomain)
		api.DELETE("/domains/:id", s.handleDeleteDomain)
		api.POST("/domains/:id/check", s.handleCheckDomain)
		api.POST("/domains/check-all", s.handleCheckAll)

		api.GET("/groups", s.handleListGroups)
		api.POST("/groups", s.handleCreateGroup)
		api.DELETE("/groups/:id", s.handleDeleteGroup)

		api.GET("/settings", s.handleGetSettings)
		api.PUT("/settings", s.handleUpdateSettings)

		api.GET("/backup", s.handleBackup)
		api.POST("/restore", s.handleRestore)
	}

	return r
}