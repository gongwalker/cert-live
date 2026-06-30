package model

import "encoding/json"

type User struct {
	ID           int64  `json:"id"`
	Username     string `json:"username"`
	PasswordHash string `json:"-"`
}

type Group struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type Domain struct {
	ID        int64  `json:"id"`
	Host      string `json:"host"`
	Port      int    `json:"port"`
	GroupID   *int64 `json:"group_id,omitempty"`
	GroupName string `json:"group_name,omitempty"`
	Notes     string `json:"notes,omitempty"`
	CreatedAt int64  `json:"created_at"`

	// Latest probe result, joined for list views.
	Subject       string   `json:"subject,omitempty"`
	Issuer        string   `json:"issuer,omitempty"`
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
	FeishuWebhook     string `json:"feishu_webhook"`
	FeishuSecret      string `json:"feishu_secret"`
	WeComWebhook      string `json:"wecom_webhook"`
	AlertTiersJSON    string `json:"alert_tiers"`      // JSON array of ints, e.g. [30,7,1]
	CheckIntervalMin  int    `json:"check_interval"`   // minutes between full scans
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