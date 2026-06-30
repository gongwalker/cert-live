package notify

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type Channel struct {
	FeishuWebhook string
	FeishuSecret  string
	WeComWebhook  string
}

type Alert struct {
	Host        string
	DaysLeft    int
	NotAfter    time.Time
	Subject     string
	Issuer      string
	Tier        int
	IsExpired   bool
}

// Send dispatches a single consolidated message to every configured channel.
// Each channel's error is returned independently so a dead bot doesn't block others.
func (c Channel) Send(alerts []Alert) []error {
	if len(alerts) == 0 {
		return nil
	}
	text := buildText(alerts)
	var errs []error
	if c.FeishuWebhook != "" {
		if err := postJSON(c.FeishuWebhook, feishuPayload(c.FeishuSecret, text)); err != nil {
			errs = append(errs, fmt.Errorf("feishu: %w", err))
		}
	}
	if c.WeComWebhook != "" {
		if err := postJSON(c.WeComWebhook, wecomPayload(text)); err != nil {
			errs = append(errs, fmt.Errorf("wecom: %w", err))
		}
	}
	return errs
}

func buildText(alerts []Alert) string {
	var b strings.Builder
	now := time.Now()
	b.WriteString("SSL 证书过期告警\n")
	b.WriteString("时间: " + now.Format("2006-01-02 15:04") + "\n")
	b.WriteString("数量: " + strconv.Itoa(len(alerts)) + "\n")
	b.WriteString("------------------------\n")
	for _, a := range alerts {
		tag := fmt.Sprintf("剩余 %d 天", a.DaysLeft)
		if a.IsExpired {
			tag = "已过期"
		}
		fmt.Fprintf(&b, "• %s  [%s]\n", a.Host, tag)
		fmt.Fprintf(&b, "  到期: %s\n", a.NotAfter.Format("2006-01-02"))
		if a.Subject != "" {
			fmt.Fprintf(&b, "  主体: %s\n", a.Subject)
		}
		if a.Issuer != "" {
			fmt.Fprintf(&b, "  签发: %s\n", a.Issuer)
		}
		fmt.Fprintf(&b, "  触发阈值: %d 天\n\n", a.Tier)
	}
	b.WriteString("— CertLive 证活")
	return b.String()
}

func feishuPayload(secret, text string) []byte {
	body := map[string]any{
		"msg_type": "text",
		"content":  map[string]string{"text": text},
	}
	if secret != "" {
		ts, sign := feishuSign(secret)
		body["timestamp"] = ts
		body["sign"] = sign
	}
	b, _ := json.Marshal(body)
	return b
}

func feishuSign(secret string) (string, string) {
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	stringToSign := ts + "\n" + secret
	mac := hmac.New(sha256.New, []byte(stringToSign))
	mac.Write(nil)
	sign := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	return ts, sign
}

func wecomPayload(text string) []byte {
	b, _ := json.Marshal(map[string]any{
		"msgtype": "text",
		"text":    map[string]string{"content": text},
	})
	return b
}

func postJSON(url string, body []byte) error {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}