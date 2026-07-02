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
	r.GET("/", func(c *gin.Context) { c.Redirect(http.StatusFound, "/domains") })
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

		api.GET("/tags", s.handleListTags)
		api.POST("/tags", s.handleCreateTag)
		api.PUT("/tags/reorder", s.handleReorderTags)
		api.DELETE("/tags/:id", s.handleDeleteTag)

		api.GET("/settings", s.handleGetSettings)
		api.PUT("/settings", s.handleUpdateSettings)

		api.GET("/backup", s.handleBackup)
		api.POST("/restore", s.handleRestore)
	}

	return r
}