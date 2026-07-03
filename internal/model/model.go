package model

type User struct {
	ID           int64  `json:"id"`
	Username     string `json:"username"`
	PasswordHash string `json:"-"`
}

// Tag 标签（独立实体，目前不与 domains 关联，后续可扩展）
type Tag struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Icon  string `json:"icon,omitempty"`  // Font Awesome 图标类名，如 "fa-server"
	Color string `json:"color,omitempty"` // hex 色值，如 "#22C55E"
}

// Domain 一条域名 = 一行；包含用户编辑字段和最近一次探测结果
type Domain struct {
	ID        int64  `json:"id"`
	Host      string `json:"host"`
	Port      int    `json:"port"`
	Path      string `json:"path,omitempty"`
	Notes     string `json:"notes,omitempty"`
	CreatedAt int64  `json:"created_at"`

	// 最近一次证书探测结果（零值表示尚未探测）
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

	// 网站健康探测（HTTP 状态码）
	HTTPStatus  int    `json:"http_status,omitempty"`
	HTTPError   string `json:"http_error,omitempty"`
	HTTPChecked int64  `json:"http_checked,omitempty"`

	// 多对多标签关联（查询时 JOIN 出来）
	Tags []Tag `json:"tags,omitempty"`
}

// Settings 通知推送相关的全部配置。所有字段在 settings 表里都以 notify_ 前缀的 key 存储。
type Settings struct {
	NotifyChannel string `json:"notify_channel"` // feishu | wecom

	NotifyFeishuWebhook string `json:"notify_feishu_webhook"`
	NotifyFeishuFormat  string `json:"notify_feishu_format"` // text | markdown
	NotifyFeishuText    string `json:"notify_feishu_text"`

	NotifyWeComWebhook string `json:"notify_wecom_webhook"`
	NotifyWeComFormat  string `json:"notify_wecom_format"`
	NotifyWeComText    string `json:"notify_wecom_text"`

	NotifyCondAEnabled bool `json:"notify_cond_a_enabled"`
	NotifyCondADays    int  `json:"notify_cond_a_days"`
	NotifyCondBEnabled bool `json:"notify_cond_b_enabled"`
	NotifyCondBCodes   string `json:"notify_cond_b_codes"` // 逗号分隔，如 "200,204,304"

	// 通用设置
	CycleIntervalMin int `json:"cycle_interval_min"` // 探测 + 推送 整体周期（分钟）
}

// DefaultSettings 返回首次启动时各字段的兜底值，跟前端默认保持一致。
func DefaultSettings() Settings {
	return Settings{
		NotifyChannel: "feishu",

		NotifyFeishuWebhook: "",
		NotifyFeishuFormat:  "markdown",
		NotifyFeishuText:    defaultFeishuText,

		NotifyWeComWebhook: "",
		NotifyWeComFormat:  "text",
		NotifyWeComText:    defaultWeComText,

		NotifyCondAEnabled: true,
		NotifyCondADays:    30,
		NotifyCondBEnabled: false,
		NotifyCondBCodes:   "200,204,304",

		CycleIntervalMin: 20,
	}
}

const defaultFeishuText = `## 🔔 证书到期提醒
- **主机**：{$host}
- **网址**：{$url}
- **说明**：{$notes}
- **剩余**：**{$days} 天**
- **到期**：{$expire_date}
- **签发 CA**：{$issuer}
- **HTTP**：{$http_status}

> 提醒时间 {$time}`

const defaultWeComText = `# 证书到期提醒
主机：{$host}
网址：{$url}
说明：{$notes}
剩余天数：{$days} 天
到期日期：{$expire_date}
签发 CA：{$issuer}
HTTP 状态：{$http_status}
提醒时间：{$time}`