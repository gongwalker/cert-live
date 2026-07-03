package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"cert-live/internal/model"
)

// schema 库结构：users / domains（含探测结果）/ alert_log / settings
// - domains 合并了原 cert_records，1 条域名 1 行
// - 已移除 domain_groups（UI 不再用）
const schema = `
CREATE TABLE IF NOT EXISTS users (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  username TEXT UNIQUE NOT NULL,
  password_hash TEXT NOT NULL,
  created_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS domains (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  host TEXT NOT NULL,
  port INTEGER NOT NULL DEFAULT 443,
  path TEXT NOT NULL DEFAULT '/',
  notes TEXT,
  created_at INTEGER NOT NULL,
  sort_order INTEGER NOT NULL DEFAULT 0,
  -- 证书探测结果（首次成功探测后填充；NULL 表示尚未探测）
  subject TEXT,
  issuer TEXT,
  issuer_org TEXT,
  sans TEXT,
  serial_number TEXT,
  not_before INTEGER,
  not_after INTEGER,
  is_wildcard INTEGER,
  days_remaining INTEGER,
  last_checked INTEGER,
  last_error TEXT,
  -- 网站健康探测（HTTP 状态码）
  http_status INTEGER,
  http_error TEXT,
  http_checked INTEGER
);
CREATE INDEX IF NOT EXISTS idx_domains_host ON domains(host);
CREATE INDEX IF NOT EXISTS idx_domains_not_after ON domains(not_after);
-- idx_domains_sort 在 EnsureSchema 的 ALTER 迁移之后创建，避免老库迁移前缺列报错

CREATE TABLE IF NOT EXISTS settings (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS tags (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT UNIQUE NOT NULL,
  created_at INTEGER NOT NULL,
  sort_order INTEGER NOT NULL DEFAULT 0,
  icon TEXT,
  color TEXT
);

CREATE TABLE IF NOT EXISTS domain_tags (
  domain_id INTEGER NOT NULL REFERENCES domains(id) ON DELETE CASCADE,
  tag_id INTEGER NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
  PRIMARY KEY (domain_id, tag_id)
);
CREATE INDEX IF NOT EXISTS idx_domain_tags_tag ON domain_tags(tag_id);
`

const domainListQuery = `
SELECT id, host, port, COALESCE(path,'/'), notes, created_at,
       subject, issuer, issuer_org, sans, serial_number,
       not_before, not_after, is_wildcard, days_remaining,
       last_checked, last_error,
       COALESCE(http_status,0), COALESCE(http_error,''), COALESCE(http_checked,0)
FROM domains
WHERE (? = '%%' OR host LIKE ? OR notes LIKE ?)`

const domainListOrderBy = `
ORDER BY sort_order ASC, id DESC`

const domainGetQuery = `
SELECT id, host, port, COALESCE(path,'/'), notes, created_at,
       subject, issuer, issuer_org, sans, serial_number,
       not_before, not_after, is_wildcard, days_remaining,
       last_checked, last_error,
       COALESCE(http_status,0), COALESCE(http_error,''), COALESCE(http_checked,0)
FROM domains
WHERE id = ?`

type scanner interface {
	Scan(dest ...any) error
}

func scanDomain(row scanner) (model.Domain, error) {
	var d model.Domain
	var notes sql.NullString
	var subject, issuer, issuerOrg, serial, lastErr sql.NullString
	var httpErr sql.NullString
	var sansJSON []byte
	var notBefore, notAfter, daysRemaining sql.NullInt64
	var lastChecked, httpStatus, httpChecked sql.NullInt64
	var isWildcard sql.NullInt64

	if err := row.Scan(
		&d.ID, &d.Host, &d.Port, &d.Path, &notes, &d.CreatedAt,
		&subject, &issuer, &issuerOrg, &sansJSON, &serial,
		&notBefore, &notAfter, &isWildcard, &daysRemaining,
		&lastChecked, &lastErr,
		&httpStatus, &httpErr, &httpChecked,
	); err != nil {
		return d, err
	}
	d.Notes = notes.String
	d.Subject = subject.String
	d.Issuer = issuer.String
	d.IssuerOrg = issuerOrg.String
	d.SerialNumber = serial.String
	if len(sansJSON) > 0 {
		_ = json.Unmarshal(sansJSON, &d.SANs)
	}
	if notBefore.Valid {
		d.NotBefore = notBefore.Int64
	}
	if notAfter.Valid {
		d.NotAfter = notAfter.Int64
	}
	if isWildcard.Valid && isWildcard.Int64 == 1 {
		d.IsWildcard = true
	}
	if daysRemaining.Valid {
		d.DaysRemaining = int(daysRemaining.Int64)
	}
	if lastChecked.Valid {
		d.LastChecked = lastChecked.Int64
	}
	d.LastError = lastErr.String
	if httpStatus.Valid {
		d.HTTPStatus = int(httpStatus.Int64)
	}
	d.HTTPError = httpErr.String
	if httpChecked.Valid {
		d.HTTPChecked = httpChecked.Int64
	}
	return d, nil
}

func nowUnix() int64 { return time.Now().Unix() }

func nullableString(v string) any {
	if v == "" {
		return nil
	}
	return v
}

func nullableInt64(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// sqlQuote 转义单引号用于 VACUUM INTO 的路径字面量
func sqlQuote(s string) string {
	escaped := ""
	for _, r := range s {
		if r == '\'' {
			escaped += "''"
			continue
		}
		escaped += string(r)
	}
	return "'" + escaped + "'"
}

func randSuffix() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b) + fmt.Sprintf("%d", time.Now().UnixNano())
}