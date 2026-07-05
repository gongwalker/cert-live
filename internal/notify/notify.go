// Package notify 实现证书到期 / HTTP 异常 的消息推送。
//
// 推送目标支持飞书和企业微信群机器人，二选一（由 Settings.NotifyChannel 决定）。
// 推送内容支持 text / markdown 两种格式，模板里可用 {$host} {$url} 等占位符。
//
// 频率限制（官方文档）：
//   企业微信：20 条 / 分钟
//   飞书：100 次 / 分钟，且 5 次 / 秒
//
// 限制策略：每次发送前按平台睡眠（feishu 600ms、wecom 3s），失败按 1s/2s/4s 退避重试 3 次。
package notify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

type Platform string

const (
	Feishu Platform = "feishu"
	WeCom  Platform = "wecom"
)

// Channel 单次推送目标。一个 Channel 对应一个平台 + 一个 Webhook。
type Channel struct {
	Platform Platform
	Webhook  string
	Format   string // "text" | "markdown"
}

// Vars 模板渲染时替换的变量集合。零值字段会被替换成空串。
type Vars struct {
	Host       string
	URL        string
	Notes      string
	Tags       string
	Days       string
	HTTPStatus string
	Subject    string
	Issuer     string
	ExpireDate string
	Time       string
	ViewURL    string // 详情查看 URL，形如 /view/<token>?id=<share_id>；公开访问未开启时为空
}

var (
	// 全局发送锁 + 上次发送时间，保证全局速率限制（多 goroutine 共用）
	sendMu       sync.Mutex
	lastSendAt   time.Time
	rateLimitGap = map[Platform]time.Duration{
		Feishu: 600 * time.Millisecond, // 100/min → 600ms/条
		WeCom:  3 * time.Second,        // 20/min  → 3s/条
	}
)

// Render 把模板里的 {$xxx} 替换成 vars 对应字段。
func Render(tmpl string, v Vars) string {
	repl := map[string]string{
		"{$host}":        v.Host,
		"{$url}":         v.URL,
		"{$notes}":       v.Notes,
		"{$tags}":        v.Tags,
		"{$days}":        v.Days,
		"{$http_status}": v.HTTPStatus,
		"{$subject}":     v.Subject,
		"{$issuer}":      v.Issuer,
		"{$expire_date}": v.ExpireDate,
		"{$time}":        v.Time,
		"{$viewurl}":     v.ViewURL,
	}
	out := tmpl
	for k, val := range repl {
		out = strings.ReplaceAll(out, k, val)
	}
	return out
}

// Send 渲染并发送一次推送。返回最后一次错误（成功返回 nil）。
//
// 流程：渲染 → 平台 payload → 限速等待 → POST → 失败按 1s/2s/4s 退避重试，最多 3 次。
func (c Channel) Send(text string) error {
	if c.Webhook == "" {
		return fmt.Errorf("webhook 为空，跳过发送")
	}
	payload, err := c.buildPayload(text)
	if err != nil {
		return err
	}

	backoffs := []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(backoffs[attempt-1])
		}
		waitRateLimit(c.Platform)
		err := postJSON(c.Webhook, payload)
		if err == nil {
			return nil
		}
		lastErr = err
		// 平台返回的业务错误（errcode != 0）也视作失败重试
	}
	return fmt.Errorf("重试 3 次仍失败: %w", lastErr)
}

// buildPayload 根据平台 + 格式生成请求体字节串。
func (c Channel) buildPayload(text string) ([]byte, error) {
	switch c.Platform {
	case Feishu:
		return feishuPayload(c.Format, text)
	case WeCom:
		return wecomPayload(c.Format, text)
	}
	return nil, fmt.Errorf("未知平台: %s", c.Platform)
}

func feishuPayload(format, text string) ([]byte, error) {
	if format == "markdown" {
		return json.Marshal(map[string]any{
			"msg_type": "interactive",
			"card": map[string]any{
				"elements": []map[string]any{
					{"tag": "markdown", "content": text},
				},
			},
		})
	}
	return json.Marshal(map[string]any{
		"msg_type": "text",
		"content":  map[string]string{"text": text},
	})
}

func wecomPayload(format, text string) ([]byte, error) {
	if format == "markdown" {
		return json.Marshal(map[string]any{
			"msgtype": "markdown",
			"markdown": map[string]string{
				"content": text,
			},
		})
	}
	return json.Marshal(map[string]any{
		"msgtype": "text",
		"text":    map[string]string{"content": text},
	})
}

// waitRateLimit 阻塞到满足平台速率窗口（全局锁内执行）。
func waitRateLimit(p Platform) {
	sendMu.Lock()
	defer sendMu.Unlock()
	gap := rateLimitGap[p]
	if gap == 0 {
		gap = time.Second
	}
	if !lastSendAt.IsZero() {
		wait := gap - time.Since(lastSendAt)
		if wait > 0 {
			time.Sleep(wait)
		}
	}
	lastSendAt = time.Now()
}

func postJSON(url string, body []byte) error {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncate(string(respBody), 200))
	}
	// 平台业务错误码：飞书 errcode!=0、企业微信 errcode!=0
	if errCode := extractErrCode(respBody); errCode != 0 {
		return fmt.Errorf("平台返回 errcode=%d: %s", errCode, truncate(string(respBody), 200))
	}
	return nil
}

// extractErrCode 从响应里抠 errcode 字段（飞书/企业微信都用这个字段名）。
func extractErrCode(body []byte) int {
	var m struct {
		ErrCode int `json:"errcode"`
		Code    int `json:"code"` // 飞书某些接口用 code
	}
	if json.Unmarshal(body, &m) != nil {
		return 0
	}
	if m.ErrCode != 0 {
		return m.ErrCode
	}
	return m.Code
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}