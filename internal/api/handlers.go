package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"cert-live/internal/auth"
	"cert-live/internal/captcha"
	"cert-live/internal/model"
)

// ---------------- 页面 ----------------

// LoginPage 渲染登录页（已登录则跳转首页）
func (s *Server) LoginPage(c *gin.Context) {
	if auth.CurrentUser(c) != "" {
		c.Redirect(http.StatusFound, "/")
		return
	}
	c.HTML(http.StatusOK, "login.html", gin.H{})
}

// DashboardPage 渲染后台首页（占位）
func (s *Server) DashboardPage(c *gin.Context) {
	c.HTML(http.StatusOK, "dashboard.html", gin.H{
		"user": auth.CurrentUser(c),
	})
}

// DomainsPage 渲染域名证书监控列表页
func (s *Server) DomainsPage(c *gin.Context) {
	c.HTML(http.StatusOK, "domains.html", gin.H{
		"user": auth.CurrentUser(c),
	})
}

// ---------------- 登录 / 登出 / 验证码 ----------------

// Captcha 生成图形验证码（公开接口）
func (s *Server) Captcha(c *gin.Context) {
	id, b64, err := captcha.Generate()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1, "msg": "验证码生成失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"msg":  "ok",
		"data": gin.H{"id": id, "img": b64},
	})
}

// LoginSubmit 处理登录表单提交：校验验证码 → 校验账号密码 → 下发 cookie
func (s *Server) LoginSubmit(c *gin.Context) {
	captchaID := c.PostForm("captchaId")
	captchaCode := c.PostForm("captcha")
	if !captcha.Verify(captchaID, captchaCode) {
		c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "验证码错误或已过期"})
		return
	}

	user := c.PostForm("username")
	pass := c.PostForm("password")
	u, err := s.st.GetUserByUsername(user)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1, "msg": "数据库错误"})
		return
	}
	if u == nil || auth.CheckPassword(u.PasswordHash, pass) != nil {
		c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "用户名或密码错误"})
		return
	}
	auth.SetLogin(c, u.Username)
	c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok"})
}

// Logout 清除登录态
func (s *Server) Logout(c *gin.Context) {
	auth.Logout(c)
	c.Redirect(http.StatusFound, "/login")
}

func (s *Server) handleMe(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"username": auth.CurrentUser(c)})
}

// ---------------- domains ----------------

func (s *Server) handleListDomains(c *gin.Context) {
	search := c.Query("search")
	groupID, _ := strconv.ParseInt(c.Query("group_id"), 10, 64)
	domains, err := s.st.ListDomains(search, groupID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, domains)
}

type domainReq struct {
	Host    string `json:"host"`
	Port    int    `json:"port"`
	GroupID *int64 `json:"group_id"`
	Notes   string `json:"notes"`
}

func (s *Server) handleCreateDomain(c *gin.Context) {
	var req domainReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	if req.Host == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "host 不能为空"})
		return
	}
	if req.Port == 0 {
		req.Port = 443
	}
	d, err := s.st.CreateDomain(req.Host, req.Port, req.GroupID, req.Notes)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, d)
}

func (s *Server) handleUpdateDomain(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad id"})
		return
	}
	var req domainReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	if req.Port == 0 {
		req.Port = 443
	}
	if err := s.st.UpdateDomain(id, req.Host, req.Port, req.GroupID, req.Notes); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (s *Server) handleDeleteDomain(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad id"})
		return
	}
	if err := s.st.DeleteDomain(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (s *Server) handleCheckDomain(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad id"})
		return
	}
	s.scheduler.CheckOne(id)
	d, err := s.st.GetDomain(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, d)
}

func (s *Server) handleCheckAll(c *gin.Context) {
	go s.scheduler.CheckAll()
	c.JSON(http.StatusOK, gin.H{"ok": true, "message": "后台巡检已触发"})
}

// ---------------- groups ----------------

func (s *Server) handleListGroups(c *gin.Context) {
	groups, err := s.st.ListGroups()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, groups)
}

type groupReq struct {
	Name string `json:"name"`
}

func (s *Server) handleCreateGroup(c *gin.Context) {
	var req groupReq
	if err := c.ShouldBindJSON(&req); err != nil || req.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name 不能为空"})
		return
	}
	g, err := s.st.CreateGroup(req.Name)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, g)
}

func (s *Server) handleDeleteGroup(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad id"})
		return
	}
	if err := s.st.DeleteGroup(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// ---------------- settings ----------------

func (s *Server) handleGetSettings(c *gin.Context) {
	m, err := s.st.GetAll()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	def := model.DefaultSettings()
	out := gin.H{
		"feishu_webhook":  m["feishu_webhook"],
		"feishu_secret":   m["feishu_secret"],
		"wecom_webhook":   m["wecom_webhook"],
		"alert_tiers":     def.AlertTiersJSON,
		"check_interval":  def.CheckIntervalMin,
	}
	if v, ok := m["alert_tiers"]; ok {
		out["alert_tiers"] = v
	}
	if v, ok := m["check_interval"]; ok && v != "" {
		out["check_interval"] = v
	}
	c.JSON(http.StatusOK, out)
}

func (s *Server) handleUpdateSettings(c *gin.Context) {
	var req struct {
		FeishuWebhook string `json:"feishu_webhook"`
		FeishuSecret  string `json:"feishu_secret"`
		WeComWebhook  string `json:"wecom_webhook"`
		AlertTiers    any    `json:"alert_tiers"`
		CheckInterval any    `json:"check_interval"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	setting := map[string]string{
		"feishu_webhook": req.FeishuWebhook,
		"feishu_secret":  req.FeishuSecret,
		"wecom_webhook":  req.WeComWebhook,
	}
	switch v := req.AlertTiers.(type) {
	case string:
		setting["alert_tiers"] = v
	case []any:
		setting["alert_tiers"] = jsonCompact(v)
	default:
		setting["alert_tiers"] = "[30,7,1]"
	}
	switch v := req.CheckInterval.(type) {
	case float64:
		if v > 0 {
			setting["check_interval"] = strconv.Itoa(int(v))
		}
	case string:
		setting["check_interval"] = v
	}
	for k, v := range setting {
		if err := s.st.SetSetting(k, v); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// ---------------- backup / restore ----------------

func (s *Server) handleBackup(c *gin.Context) {
	data, err := s.st.Backup()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	name := fmt.Sprintf("certlive-backup-%s.db", time.Now().Format("20060102-150405"))
	c.Header("Content-Disposition", `attachment; filename="`+name+`"`)
	c.Data(http.StatusOK, "application/octet-stream", data)
}

func (s *Server) handleRestore(c *gin.Context) {
	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "未上传文件"})
		return
	}
	src, err := file.Open()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer src.Close()
	buf, err := io.ReadAll(src)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := s.st.ReplaceDB(buf); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := s.st.EnsureSchema(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "message": "恢复成功"})
}

func jsonCompact(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "[30,7,1]"
	}
	return string(b)
}
