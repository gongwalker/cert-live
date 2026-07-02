package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"cert-live/internal/auth"
	"cert-live/internal/captcha"
	"cert-live/internal/model"
)

// 统一响应信封：{ code, message, data }
// 成功：code=200，data 承载实际数据
// 失败：code=HTTP 状态码（400/401/404/500...），data=null
func ok(c *gin.Context, data any) {
	c.JSON(http.StatusOK, gin.H{"code": 200, "message": "ok", "data": data})
}

func fail(c *gin.Context, code int, msg string) {
	c.JSON(code, gin.H{"code": code, "message": msg, "data": nil})
}

// ---------------- 页面 ----------------

// LoginPage 渲染登录页（已登录则跳首页 = /domains）
func (s *Server) LoginPage(c *gin.Context) {
	if auth.CurrentUser(c) != "" {
		c.Redirect(http.StatusFound, "/domains")
		return
	}
	c.HTML(http.StatusOK, "login.html", gin.H{})
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
		fail(c, http.StatusInternalServerError, "验证码生成失败")
		return
	}
	ok(c, gin.H{"id": id, "img": b64})
}

// LoginSubmit 处理登录表单提交：校验验证码 → 校验账号密码 → 下发 cookie
func (s *Server) LoginSubmit(c *gin.Context) {
	captchaID := c.PostForm("captchaId")
	captchaCode := c.PostForm("captcha")
	if !captcha.Verify(captchaID, captchaCode) {
		fail(c, http.StatusBadRequest, "验证码错误或已过期")
		return
	}

	user := c.PostForm("username")
	pass := c.PostForm("password")
	u, err := s.st.GetUserByUsername(user)
	if err != nil {
		fail(c, http.StatusInternalServerError, "数据库错误")
		return
	}
	if u == nil || auth.CheckPassword(u.PasswordHash, pass) != nil {
		fail(c, http.StatusUnauthorized, "用户名或密码错误")
		return
	}
	auth.SetLogin(c, u.Username)
	ok(c, gin.H{"username": u.Username})
}

// Logout 清除登录态
func (s *Server) Logout(c *gin.Context) {
	auth.Logout(c)
	c.Redirect(http.StatusFound, "/login")
}

func (s *Server) handleMe(c *gin.Context) {
	ok(c, gin.H{"username": auth.CurrentUser(c)})
}

// ---------------- domains ----------------

func (s *Server) handleListDomains(c *gin.Context) {
	search := c.Query("search")
	domains, err := s.st.ListDomains(search)
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, domains)
}

type domainReq struct {
	Host  string `json:"host"`
	Port  int    `json:"port"`
	Notes string `json:"notes"`
}

// normalizeHost 去掉 scheme / path / 查询串，仅保留 host[:port]
// 例："https://example.com:8443/path" → "example.com:8443"
func normalizeHost(s string) string {
	s = strings.TrimSpace(s)
	lower := strings.ToLower(s)
	switch {
	case strings.HasPrefix(lower, "https://"):
		s = s[len("https://"):]
	case strings.HasPrefix(lower, "http://"):
		s = s[len("http://"):]
	}
	for i, r := range s {
		if r == '/' || r == '?' || r == '#' {
			s = s[:i]
			break
		}
	}
	return strings.TrimSpace(s)
}

func (s *Server) handleCreateDomain(c *gin.Context) {
	var req domainReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "请求体格式错误")
		return
	}
	req.Host = normalizeHost(req.Host)
	if req.Host == "" {
		fail(c, http.StatusBadRequest, "host 不能为空")
		return
	}
	if req.Port == 0 {
		req.Port = 443
	}
	d, err := s.st.CreateDomain(req.Host, req.Port, req.Notes)
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, d)
}

func (s *Server) handleUpdateDomain(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		fail(c, http.StatusBadRequest, "无效的 id")
		return
	}
	var req domainReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "请求体格式错误")
		return
	}
	req.Host = normalizeHost(req.Host)
	if req.Host == "" {
		fail(c, http.StatusBadRequest, "host 不能为空")
		return
	}
	if req.Port == 0 {
		req.Port = 443
	}
	if err := s.st.UpdateDomain(id, req.Host, req.Port, req.Notes); err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	// 返回更新后的资源
	if d, err := s.st.GetDomain(id); err == nil {
		ok(c, d)
	} else {
		ok(c, gin.H{"id": id})
	}
}

func (s *Server) handleDeleteDomain(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		fail(c, http.StatusBadRequest, "无效的 id")
		return
	}
	if err := s.st.DeleteDomain(id); err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, nil)
}

func (s *Server) handleCheckDomain(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		fail(c, http.StatusBadRequest, "无效的 id")
		return
	}
	s.scheduler.CheckOne(id)
	d, err := s.st.GetDomain(id)
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, d)
}

func (s *Server) handleCheckAll(c *gin.Context) {
	go s.scheduler.CheckAll()
	ok(c, gin.H{"triggered": true})
}

// ---------------- settings ----------------

func (s *Server) handleGetSettings(c *gin.Context) {
	m, err := s.st.GetAll()
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
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
	ok(c, out)
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
		fail(c, http.StatusBadRequest, "请求体格式错误")
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
			fail(c, http.StatusInternalServerError, err.Error())
			return
		}
	}
	ok(c, nil)
}

// ---------------- backup / restore ----------------

func (s *Server) handleBackup(c *gin.Context) {
	data, err := s.st.Backup()
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	name := fmt.Sprintf("certlive-backup-%s.db", time.Now().Format("20060102-150405"))
	c.Header("Content-Disposition", `attachment; filename="`+name+`"`)
	c.Data(http.StatusOK, "application/octet-stream", data)
}

func (s *Server) handleRestore(c *gin.Context) {
	file, err := c.FormFile("file")
	if err != nil {
		fail(c, http.StatusBadRequest, "未上传文件")
		return
	}
	src, err := file.Open()
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	defer src.Close()
	buf, err := io.ReadAll(src)
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.st.ReplaceDB(buf); err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.st.EnsureSchema(); err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, gin.H{"restored": true})
}

func jsonCompact(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "[30,7,1]"
	}
	return string(b)
}