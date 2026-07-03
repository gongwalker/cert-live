package scheduler

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"cert-live/internal/model"
	"cert-live/internal/notify"
	"cert-live/internal/probe"
	"cert-live/internal/store"
)

// Scheduler 跑两个独立循环：
//   Run          —— 证书探测（间隔由 settings.check_interval 控制，默认 6h）
//   RunNotify    —— 通知推送（固定 5min 扫一次库，命中条件 A 或 B 就推）
type Scheduler struct {
	st *store.Store
}

func New(st *store.Store) *Scheduler {
	return &Scheduler{st: st}
}

// Run 证书探测循环：开机 5s 跑一次，之后按 check_interval 周期跑。
func (s *Scheduler) Run(ctx context.Context) {
	go func() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
			s.CheckAll()
		}
	}()
	for {
		settings := readSettings(s.st)
		interval := time.Duration(settings.CheckIntervalMin) * time.Minute
		if interval <= 0 {
			interval = 6 * time.Hour
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
			s.CheckAll()
		}
	}
}

// RunNotify 通知推送循环：固定 5 分钟扫一次。
func (s *Scheduler) RunNotify(ctx context.Context) {
	// 开机先等 30s 再首次扫描，给探测循环一点时间填数据
	go func() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(30 * time.Second):
			s.ScanAndPush()
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Minute):
			s.ScanAndPush()
		}
	}
}

// CheckAll 探测所有域名，把证书 / HTTP 结果写回 domains 表。不发推送。
func (s *Scheduler) CheckAll() {
	ids, err := s.st.ListAllDomainIDs()
	if err != nil {
		log.Printf("scheduler: list domains: %v", err)
		return
	}
	for _, id := range ids {
		s.checkOne(id)
	}
}

// CheckOne 立即探测单个域名（API 手动触发用），结果写库。
func (s *Scheduler) CheckOne(domainID int64) {
	s.checkOne(domainID)
}

func (s *Scheduler) checkOne(domainID int64) {
	dom, err := s.st.GetDomain(domainID)
	if err != nil || dom == nil {
		log.Printf("scheduler: get domain %d: %v", domainID, err)
		return
	}
	now := time.Now().Unix()
	res, err := probe.Probe(dom.Host, dom.Port)
	if err != nil {
		rec := *dom
		rec.LastChecked = now
		rec.LastError = err.Error()
		if e := s.st.UpdateDomainProbe(rec); e != nil {
			log.Printf("scheduler: update probe for %s: %v", dom.Host, e)
		}
		return
	}

	rec := *dom
	rec.Subject = res.Subject
	rec.Issuer = res.Issuer
	rec.IssuerOrg = res.IssuerOrg
	rec.SANs = res.SANs
	rec.SerialNumber = res.SerialNumber
	rec.NotBefore = res.NotBefore.Unix()
	rec.NotAfter = res.NotAfter.Unix()
	rec.IsWildcard = res.IsWildcard
	rec.DaysRemaining = res.DaysRemaining
	rec.LastChecked = now

	httpRes := probe.HTTPProbe(dom.Host, dom.Port, dom.Path)
	if httpRes != nil {
		rec.HTTPStatus = httpRes.StatusCode
		rec.HTTPError = httpRes.Error
		rec.HTTPChecked = now
	}
	if e := s.st.UpdateDomainProbe(rec); e != nil {
		log.Printf("scheduler: update probe for %s: %v", dom.Host, e)
	}
}

// ScanAndPush 扫所有域名，命中条件 A 或 B 且未推送过的，立即推。
// 同一张证书最多推一次（用 alert_log(domain_id, cert_serial) 去重）。
func (s *Scheduler) ScanAndPush() {
	settings := readSettings(s.st)
	if !settings.NotifyCondAEnabled && !settings.NotifyCondBEnabled {
		return // 至少要有一个条件
	}
	// 至少得有一个 webhook 配置
	feishuReady := settings.NotifyFeishuWebhook != ""
	wecomReady := settings.NotifyWeComWebhook != ""
	if !feishuReady && !wecomReady {
		return
	}

	// 当前激活平台必须有 webhook
	var ch notify.Channel
	if settings.NotifyChannel == "wecom" {
		if !wecomReady {
			return
		}
		ch = notify.Channel{Platform: notify.WeCom, Webhook: settings.NotifyWeComWebhook, Format: settings.NotifyWeComFormat}
	} else {
		if !feishuReady {
			return
		}
		ch = notify.Channel{Platform: notify.Feishu, Webhook: settings.NotifyFeishuWebhook, Format: settings.NotifyFeishuFormat}
	}

	domains, err := s.st.ListDomains("", nil)
	if err != nil {
		log.Printf("scheduler: list domains: %v", err)
		return
	}

	httpOK := parseHTTPWhitelist(settings.NotifyCondBCodes)

	for _, d := range domains {
		// 没探测过 / 探测失败，跳过（避免对未知状态误报）
		if d.LastError != "" || d.NotAfter == 0 {
			continue
		}
		// 命中条件 A 或 B 就推，不去重 —— 5 分钟一次，每次扫到都推
		hitA := settings.NotifyCondAEnabled && d.DaysRemaining < settings.NotifyCondADays
		hitB := settings.NotifyCondBEnabled && !httpOK[d.HTTPStatus]
		if !hitA && !hitB {
			continue
		}

		// 选模板：当前激活平台对应的 text。变量替换。
		tmpl := settings.NotifyFeishuText
		if settings.NotifyChannel == "wecom" {
			tmpl = settings.NotifyWeComText
		}
		rendered := notify.Render(tmpl, buildVars(d))

		if err := ch.Send(rendered); err != nil {
			log.Printf("notify: send %s: %v", d.Host, err)
		}
	}
}

// parseHTTPWhitelist 解析 "200,204,304" → map[int]bool{200:true, 204:true, 304:true}。
// 解析失败的条目跳过；空串 → 全部视为不匹配（条件 B 永远命中，慎用）。
func parseHTTPWhitelist(s string) map[int]bool {
	out := map[int]bool{}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if n, err := strconv.Atoi(part); err == nil {
			out[n] = true
		}
	}
	return out
}

// buildVars 把 model.Domain 拼成 notify.Vars，用于模板渲染。
func buildVars(d model.Domain) notify.Vars {
	httpStr := ""
	if d.HTTPChecked != 0 && d.HTTPError == "" && d.HTTPStatus != 0 {
		httpStr = strconv.Itoa(d.HTTPStatus)
	} else if d.HTTPError != "" {
		httpStr = d.HTTPError
	}
	url := d.Host
	if d.Port != 0 && d.Port != 443 {
		url = fmt.Sprintf("%s:%d", d.Host, d.Port)
	}
	if d.Path != "" && d.Path != "/" {
		url += d.Path
	}
	tags := ""
	if len(d.Tags) > 0 {
		names := make([]string, 0, len(d.Tags))
		for _, t := range d.Tags {
			names = append(names, t.Name)
		}
		tags = strings.Join(names, " ")
	}
	var expireDate string
	if d.NotAfter != 0 {
		expireDate = time.Unix(d.NotAfter, 0).Format("2006-01-02 15:04:05")
	}
	return notify.Vars{
		Host:       d.Host,
		URL:        url,
		Notes:      d.Notes,
		Tags:       tags,
		Days:       strconv.Itoa(d.DaysRemaining),
		HTTPStatus: httpStr,
		Subject:    d.Subject,
		Issuer:     d.IssuerOrg + " " + d.Issuer,
		ExpireDate: expireDate,
		Time:       time.Now().Format("2006-01-02 15:04:05"),
	}
}

// readSettings 从 settings 表读出 Settings（缺字段用默认值补齐）。
func readSettings(st *store.Store) model.Settings {
	m, err := st.GetAll()
	if err != nil {
		return model.DefaultSettings()
	}
	def := model.DefaultSettings()
	s := def
	if v, ok := m["notify_channel"]; ok && v != "" {
		s.NotifyChannel = v
	}
	if v, ok := m["notify_feishu_webhook"]; ok {
		s.NotifyFeishuWebhook = v
	}
	if v, ok := m["notify_feishu_format"]; ok && v != "" {
		s.NotifyFeishuFormat = v
	}
	if v, ok := m["notify_feishu_text"]; ok && v != "" {
		s.NotifyFeishuText = v
	}
	if v, ok := m["notify_wecom_webhook"]; ok {
		s.NotifyWeComWebhook = v
	}
	if v, ok := m["notify_wecom_format"]; ok && v != "" {
		s.NotifyWeComFormat = v
	}
	if v, ok := m["notify_wecom_text"]; ok && v != "" {
		s.NotifyWeComText = v
	}
	if v, ok := m["notify_cond_a_enabled"]; ok {
		s.NotifyCondAEnabled = v == "true"
	}
	if v, ok := m["notify_cond_a_days"]; ok {
		if n, err := strconv.Atoi(v); err == nil {
			s.NotifyCondADays = n
		}
	}
	if v, ok := m["notify_cond_b_enabled"]; ok {
		s.NotifyCondBEnabled = v == "true"
	}
	if v, ok := m["notify_cond_b_codes"]; ok {
		s.NotifyCondBCodes = v
	}
	if v, ok := m["check_interval"]; ok && v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			s.CheckIntervalMin = n
		}
	}
	return s
}