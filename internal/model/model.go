package model

import "encoding/json"

type User struct {
	ID           int64  `json:"id"`
	Username     string `json:"username"`
	PasswordHash string `json:"-"`
}

// Tag 标签（独立实体，目前不与 domains 关联，后续可扩展）
type Tag struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// Domain 一条域名 = 一行；包含用户编辑字段和最近一次探测结果
type Domain struct {
	ID        int64  `json:"id"`
	Host      string `json:"host"`
	Port      int    `json:"port"`
	Notes     string `json:"notes,omitempty"`
	CreatedAt int64  `json:"created_at"`

	// 最近一次探测结果（零值表示尚未探测）
	Subject       string   `json:"subject,omitempty"`
	Issuer        string   `json:"issuer,omitempty"`      // 中间证书 CN
	IssuerOrg     string   `json:"issuer_org,omitempty"`  // 签发 CA 组织名
	SANs          []string `json:"sans,omitempty"`
	SerialNumber  string   `json:"serial_number,omitempty"`
	NotBefore     int64    `json:"not_before,omitempty"`
	NotAfter      int64    `json:"not_after,omitempty"`
	IsWildcard    bool     `json:"is_wildcard,omitempty"`
	DaysRemaining int      `json:"days_remaining,omitempty"`
	LastChecked   int64    `json:"last_checked,omitempty"`
	LastError     string   `json:"last_error,omitempty"`
}

type Settings struct {
	FeishuWebhook    string `json:"feishu_webhook"`
	FeishuSecret     string `json:"feishu_secret"`
	WeComWebhook     string `json:"wecom_webhook"`
	AlertTiersJSON   string `json:"alert_tiers"`    // JSON array of ints, e.g. [30,7,1]
	CheckIntervalMin int    `json:"check_interval"` // minutes between full scans
}

func (s Settings) Tiers() []int {
	var tiers []int
	if s.AlertTiersJSON == "" {
		return DefaultTiers()
	}
	if err := json.Unmarshal([]byte(s.AlertTiersJSON), &tiers); err != nil {
		return DefaultTiers()
	}
	if len(tiers) == 0 {
		return DefaultTiers()
	}
	return tiers
}

func DefaultTiers() []int {
	return []int{30, 7, 1}
}

func DefaultSettings() Settings {
	return Settings{
		AlertTiersJSON:   "[30,7,1]",
		CheckIntervalMin: 360,
	}
}