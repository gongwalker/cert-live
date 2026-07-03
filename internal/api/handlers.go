package api

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
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
	// 支持 ?tag_ids=1&tag_ids=2&tag_ids=3（AND 关系）
	tagIDStrs := c.QueryArray("tag_ids")
	tagIDs := make([]int64, 0, len(tagIDStrs))
	for _, s := range tagIDStrs {
		if id, err := strconv.ParseInt(s, 10, 64); err == nil && id > 0 {
			tagIDs = append(tagIDs, id)
		}
	}
	domains, err := s.st.ListDomains(search, tagIDs)
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, domains)
}

type domainReq struct {
	URL    string  `json:"url"`
	Notes  string  `json:"notes"`
	TagIDs []int64 `json:"tag_ids"`
}

// parseURL 把用户输入解析成 host / port / path，强制 https scheme
// 接受 "example.com"、"example.com/login"、"https://example.com:8443/health?q=1"
func parseURL(raw string) (host string, port int, path string, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", 0, "", fmt.Errorf("URL 不能为空")
	}
	// 没有 scheme 自动补 https://
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, e := url.Parse(raw)
	if e != nil || u.Hostname() == "" {
		return "", 0, "", fmt.Errorf("URL 解析失败")
	}
	host = u.Hostname()
	port = 443
	if p := u.Port(); p != "" {
		if n, _, ok := strings.Cut(p, ""); ok {
			_ = n
		}
		if n, e := strconv.Atoi(p); e == nil && n > 0 {
			port = n
		}
	}
	// path + query（不含 fragment）
	path = u.Path
	if path == "" {
		path = "/"
	}
	if u.RawQuery != "" {
		path += "?" + u.RawQuery
	}
	return host, port, path, nil
}

func (s *Server) handleCreateDomain(c *gin.Context) {
	var req domainReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "请求体格式错误")
		return
	}
	host, port, path, err := parseURL(req.URL)
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	d, err := s.st.CreateDomain(host, port, path, req.Notes, req.TagIDs)
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
	host, port, path, err := parseURL(req.URL)
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.st.UpdateDomain(id, host, port, path, req.Notes, req.TagIDs); err != nil {
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

// 重排序域名：body {domain_ids: [3,1,2,4]}
func (s *Server) handleReorderDomains(c *gin.Context) {
	var req struct {
		DomainIDs []int64 `json:"domain_ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || len(req.DomainIDs) == 0 {
		fail(c, http.StatusBadRequest, "domain_ids 不能为空")
		return
	}
	if err := s.st.ReorderDomains(req.DomainIDs); err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, nil)
}

// ---------------- tags ----------------

func (s *Server) handleListTags(c *gin.Context) {
	tags, err := s.st.ListTags()
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, tags)
}

func (s *Server) handleCreateTag(c *gin.Context) {
	var req struct {
		Name string `json:"name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Name) == "" {
		fail(c, http.StatusBadRequest, "标签名不能为空")
		return
	}
	name := strings.TrimSpace(req.Name)
	t, err := s.st.CreateTag(name)
	if err != nil {
		// 唯一约束冲突等
		fail(c, http.StatusConflict, "标签已存在或创建失败: "+err.Error())
		return
	}
	ok(c, t)
}

func (s *Server) handleDeleteTag(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		fail(c, http.StatusBadRequest, "无效的 id")
		return
	}
	if err := s.st.DeleteTag(id); err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, nil)
}

// 更新单个标签（name / icon / color 三选一或全改）
func (s *Server) handleUpdateTag(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		fail(c, http.StatusBadRequest, "无效的 id")
		return
	}
	var req struct {
		Name  *string `json:"name,omitempty"`
		Icon  *string `json:"icon,omitempty"`
		Color *string `json:"color,omitempty"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "请求体格式错误")
		return
	}
	// 读老值，没传的字段保持不变
	old, err := s.st.GetTagByID(id)
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	if old == nil {
		fail(c, http.StatusNotFound, "标签不存在")
		return
	}
	name := old.Name
	if req.Name != nil {
		name = strings.TrimSpace(*req.Name)
		if name == "" {
			fail(c, http.StatusBadRequest, "标签名不能为空")
			return
		}
	}
	icon := old.Icon
	if req.Icon != nil {
		icon = *req.Icon
	}
	color := old.Color
	if req.Color != nil {
		color = *req.Color
	}
	if err := s.st.UpdateTag(id, name, icon, color); err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, model.Tag{ID: id, Name: name, Icon: icon, Color: color})
}

// 重排序标签：body {tag_ids: [3,1,2,4]} → 按此顺序写 sort_order
func (s *Server) handleReorderTags(c *gin.Context) {
	var req struct {
		TagIDs []int64 `json:"tag_ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || len(req.TagIDs) == 0 {
		fail(c, http.StatusBadRequest, "tag_ids 不能为空")
		return
	}
	if err := s.st.ReorderTags(req.TagIDs); err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, nil)
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
		"notify_channel":         getStr(m, "notify_channel", def.NotifyChannel),
		"notify_feishu_webhook":  getStr(m, "notify_feishu_webhook", def.NotifyFeishuWebhook),
		"notify_feishu_format":   getStr(m, "notify_feishu_format", def.NotifyFeishuFormat),
		"notify_feishu_text":     getStr(m, "notify_feishu_text", def.NotifyFeishuText),
		"notify_wecom_webhook":   getStr(m, "notify_wecom_webhook", def.NotifyWeComWebhook),
		"notify_wecom_format":    getStr(m, "notify_wecom_format", def.NotifyWeComFormat),
		"notify_wecom_text":      getStr(m, "notify_wecom_text", def.NotifyWeComText),
		"notify_cond_a_enabled":  getStr(m, "notify_cond_a_enabled", boolStr(def.NotifyCondAEnabled)),
		"notify_cond_a_days":     getStr(m, "notify_cond_a_days", strconv.Itoa(def.NotifyCondADays)),
		"notify_cond_b_enabled":  getStr(m, "notify_cond_b_enabled", boolStr(def.NotifyCondBEnabled)),
		"notify_cond_b_codes":    getStr(m, "notify_cond_b_codes", def.NotifyCondBCodes),
		"check_interval":         getStr(m, "check_interval", strconv.Itoa(def.CheckIntervalMin)),
	}
	ok(c, out)
}

func getStr(m map[string]string, key, def string) string {
	if v, ok := m[key]; ok && v != "" {
		return v
	}
	return def
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func (s *Server) handleUpdateSettings(c *gin.Context) {
	var req map[string]any
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "请求体格式错误")
		return
	}
	// 允许的字段白名单 + 类型归一化（全部以字符串存进 settings 表）
	allowed := map[string]string{
		"notify_channel":        "string",
		"notify_feishu_webhook": "string",
		"notify_feishu_format":  "string",
		"notify_feishu_text":    "string",
		"notify_wecom_webhook":  "string",
		"notify_wecom_format":   "string",
		"notify_wecom_text":     "string",
		"notify_cond_a_enabled": "bool",
		"notify_cond_a_days":    "int",
		"notify_cond_b_enabled": "bool",
		"notify_cond_b_codes":   "string",
		"check_interval":        "int",
	}
	for key, kind := range allowed {
		raw, ok := req[key]
		if !ok {
			continue
		}
		var val string
		switch kind {
		case "string":
			if s, ok := raw.(string); ok {
				val = s
			} else {
				continue
			}
		case "bool":
			if b, ok := raw.(bool); ok {
				val = boolStr(b)
			} else {
				continue
			}
		case "int":
			if f, ok := raw.(float64); ok {
				val = strconv.Itoa(int(f))
			} else if s, ok := raw.(string); ok {
				val = s
			} else {
				continue
			}
		}
		if err := s.st.SetSetting(key, val); err != nil {
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