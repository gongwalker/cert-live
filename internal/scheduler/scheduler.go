// Package scheduler 跑一个固定 5 分钟的循环：先把所有域名探测一遍，
// 紧接着扫库找命中推送条件的，直接发飞书 / 企业微信。
//
// 探测和推送串在一个 tick 里，保证推送用的永远是「刚才那几秒」探测到的最新数据。
package scheduler

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"cert-live/internal/model"
	"cert-live/internal/notify"
	"cert-live/internal/probe"
	"cert-live/internal/store"
)

const (
	defaultCycleMin = 20              // 配置缺失或非法时的兜底周期
	minCycleMin     = 1               // 配置下限
	maxCycleMin     = 60              // 配置上限
	probeParallel   = 10              // 并发探测上限，避免一次开太多 TCP 连接
)

type Scheduler struct {
	st *store.Store
}

func New(st *store.Store) *Scheduler {
	return &Scheduler{st: st}
}

// Run 单循环：开机 30s 跑首次，之后按 settings.cycle_interval_min 间隔跑。
// 每轮顺序：并发探测所有域名 → 扫库找命中 → 限速推送。
// 周期在每次循环开始时从 DB 读，用户改完设置下一轮就生效。
func (s *Scheduler) Run(ctx context.Context) {
	// 首次启动稍微等一下，让 HTTP 服务先把监听端口起来
	go func() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(30 * time.Second):
			s.probeAll(ctx)
			s.scanAndPush()
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(s.cycleInterval()):
			s.probeAll(ctx)
			s.scanAndPush()
		}
	}
}

// cycleInterval 从 settings 读 cycle_interval_min，越界回退到默认 20 min。
func (s *Scheduler) cycleInterval() time.Duration {
	min := readSettings(s.st).CycleIntervalMin
	if min < minCycleMin || min > maxCycleMin {
		min = defaultCycleMin
	}
	return time.Duration(min) * time.Minute
}

// RunOnce 一轮完整工作：探测 → 扫库 → 推送。供 API 手动触发。
func (s *Scheduler) RunOnce() {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	s.probeAll(ctx)
	s.scanAndPush()
}

// CheckOne 立即探测单个域名（API 手动触发用），结果写库；
// 满足推送条件时也触发一次通知（异步，不阻塞 API 响应）。不影响后台循环。
func (s *Scheduler) CheckOne(domainID int64) {
	rec, err := s.probeOne(domainID)
	if err != nil || rec.ID == 0 {
		return
	}
	go s.maybePushOne(rec)
}

// maybePushOne 单条记录的推送判定：读 settings → 解渠道 → 判条件 → 命中就推。
func (s *Scheduler) maybePushOne(d model.Domain) {
	settings := readSettings(s.st)
	ch, tmpl, ok := resolveChannel(settings)
	if !ok {
		return
	}
	if !evalConds(d, settings, parseHTTPWhitelist(settings.NotifyCondBCodes)) {
		return
	}
	rendered := notify.Render(tmpl, buildVars(d))
	if err := ch.Send(rendered); err != nil {
		log.Printf("notify: send %s: %v", d.Host, err)
	}
}

// probeAll 并发探测所有域名，并发度受 probeParallel 控制。
// 单域名超时由 probe 包内部保证（TLS 握手 + HTTP 各自带 timeout）。
func (s *Scheduler) probeAll(ctx context.Context) {
	ids, err := s.st.ListAllDomainIDs()
	if err != nil {
		log.Printf("scheduler: list domains: %v", err)
		return
	}
	sem := make(chan struct{}, probeParallel)
	var wg sync.WaitGroup
	for _, id := range ids {
		select {
		case <-ctx.Done():
			return
		case sem <- struct{}{}:
		}
		wg.Add(1)
		go func(domainID int64) {
			defer wg.Done()
			defer func() { <-sem }()
			_, _ = s.probeOne(domainID)
		}(id)
	}
	wg.Wait()
}

// probeOne 探测单个域名：TLS 拿证书 + HTTP 拿状态码，写回 domains 表。
// 失败时只写 last_error，下一轮照样探（不做退避，靠 probe 内部的 10s 超时兜底）。
// 返回写库后的最新记录（即便 probe 出错也会返回带 LastError 的 rec），供调用方做后续处理。
func (s *Scheduler) probeOne(domainID int64) (model.Domain, error) {
	dom, err := s.st.GetDomain(domainID)
	if err != nil || dom == nil {
		log.Printf("scheduler: get domain %d: %v", domainID, err)
		return model.Domain{}, fmt.Errorf("get domain %d: %w", domainID, err)
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
		return rec, nil
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
	rec.LastError = ""

	httpRes := probe.HTTPProbe(dom.Host, dom.Port, dom.Path)
	if httpRes != nil {
		rec.HTTPStatus = httpRes.StatusCode
		rec.HTTPError = httpRes.Error
		rec.HTTPChecked = now
	}
	if e := s.st.UpdateDomainProbe(rec); e != nil {
		log.Printf("scheduler: update probe for %s: %v", dom.Host, e)
	}
	return rec, nil
}

// scanAndPush 扫所有域名，命中条件 A 或 B 就立即推（不去重）。
func (s *Scheduler) scanAndPush() {
	settings := readSettings(s.st)
	ch, tmpl, ok := resolveChannel(settings)
	if !ok {
		return // 条件全关 / webhook 未配置
	}
	domains, err := s.st.ListDomains("", nil)
	if err != nil {
		log.Printf("scheduler: list domains: %v", err)
		return
	}
	httpWhitelist := parseHTTPWhitelist(settings.NotifyCondBCodes)
	for _, d := range domains {
		if !evalConds(d, settings, httpWhitelist) {
			continue
		}
		rendered := notify.Render(tmpl, buildVars(d))
		if err := ch.Send(rendered); err != nil {
			log.Printf("notify: send %s: %v", d.Host, err)
		}
	}
}

// resolveChannel 从 settings 解出当前激活渠道 + 对应模板。
// 返回 ok=false 表示：条件 A/B 全未启用，或激活平台的 webhook 为空。
func resolveChannel(settings model.Settings) (ch notify.Channel, tmpl string, ok bool) {
	if !settings.NotifyCondAEnabled && !settings.NotifyCondBEnabled {
		return notify.Channel{}, "", false
	}
	switch settings.NotifyChannel {
	case "wecom":
		if settings.NotifyWeComWebhook == "" {
			return notify.Channel{}, "", false
		}
		return notify.Channel{
			Platform: notify.WeCom,
			Webhook:  settings.NotifyWeComWebhook,
			Format:   settings.NotifyWeComFormat,
		}, settings.NotifyWeComText, true
	default: // feishu 或未设置都走飞书
		if settings.NotifyFeishuWebhook == "" {
			return notify.Channel{}, "", false
		}
		return notify.Channel{
			Platform: notify.Feishu,
			Webhook:  settings.NotifyFeishuWebhook,
			Format:   settings.NotifyFeishuFormat,
		}, settings.NotifyFeishuText, true
	}
}

// evalConds 判定单条域名是否命中推送条件 A 或 B。
// 探测失败 / 未探测过的直接判 false，避免误报。
func evalConds(d model.Domain, settings model.Settings, httpWhitelist map[int]bool) bool {
	if d.LastError != "" || d.NotAfter == 0 {
		return false
	}
	hitA := settings.NotifyCondAEnabled && d.DaysRemaining < settings.NotifyCondADays
	hitB := settings.NotifyCondBEnabled && !httpWhitelist[d.HTTPStatus]
	return hitA || hitB
}

// parseHTTPWhitelist "200,204,304" → map[int]bool{200:true, ...}
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
		Issuer:     strings.TrimSpace(d.IssuerOrg + " " + d.Issuer),
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
	if v, ok := m["cycle_interval_min"]; ok {
		if n, err := strconv.Atoi(v); err == nil {
			s.CycleIntervalMin = n
		}
	}
	return s
}