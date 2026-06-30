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

const schema = `
CREATE TABLE IF NOT EXISTS users (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  username TEXT UNIQUE NOT NULL,
  password_hash TEXT NOT NULL,
  created_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS domain_groups (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL,
  created_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS domains (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  host TEXT NOT NULL,
  port INTEGER NOT NULL DEFAULT 443,
  group_id INTEGER REFERENCES domain_groups(id) ON DELETE SET NULL,
  notes TEXT,
  created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_domains_group ON domains(group_id);

CREATE TABLE IF NOT EXISTS cert_records (
  domain_id INTEGER PRIMARY KEY REFERENCES domains(id) ON DELETE CASCADE,
  subject TEXT,
  issuer TEXT,
  sans TEXT,
  serial_number TEXT,
  not_before INTEGER,
  not_after INTEGER,
  is_wildcard INTEGER,
  days_remaining INTEGER,
  last_checked INTEGER NOT NULL,
  last_error TEXT
);

CREATE TABLE IF NOT EXISTS alert_log (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  domain_id INTEGER NOT NULL REFERENCES domains(id) ON DELETE CASCADE,
  cert_serial TEXT NOT NULL,
  tier INTEGER NOT NULL,
  alerted_at INTEGER NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_alert_unique ON alert_log(domain_id, cert_serial, tier);

CREATE TABLE IF NOT EXISTS settings (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
`

const domainListQuery = `
SELECT d.id, d.host, d.port, d.group_id, g.name, d.notes, d.created_at,
       c.subject, c.issuer, c.sans, c.serial_number, c.not_before, c.not_after,
       c.is_wildcard, c.days_remaining, c.last_checked, c.last_error
FROM domains d
LEFT JOIN domain_groups g ON g.id = d.group_id
LEFT JOIN cert_records c ON c.domain_id = d.id
WHERE (? = '%%' OR d.host LIKE ? OR d.notes LIKE ?)
  AND (? = 0 OR d.group_id = ?)
ORDER BY d.created_at DESC`

const domainGetQuery = `
SELECT d.id, d.host, d.port, d.group_id, g.name, d.notes, d.created_at,
       c.subject, c.issuer, c.sans, c.serial_number, c.not_before, c.not_after,
       c.is_wildcard, c.days_remaining, c.last_checked, c.last_error
FROM domains d
LEFT JOIN domain_groups g ON g.id = d.group_id
LEFT JOIN cert_records c ON c.domain_id = d.id
WHERE d.id = ?`

type scanner interface {
	Scan(dest ...any) error
}

func scanDomain(row scanner) (model.Domain, error) {
	var d model.Domain
	var groupID sql.NullInt64
	var groupName, notes sql.NullString
	var subject, issuer, serial, lastErr sql.NullString
	var notBefore, notAfter, daysRemaining sql.NullInt64
	var lastChecked sql.NullInt64
	var isWildcard sql.NullInt64
	var sansJSON []byte

	if err := row.Scan(
		&d.ID, &d.Host, &d.Port, &groupID, &groupName, &notes, &d.CreatedAt,
		&subject, &issuer, &sansJSON, &serial, &notBefore, &notAfter,
		&isWildcard, &daysRemaining, &lastChecked, &lastErr,
	); err != nil {
		return d, err
	}
	if groupID.Valid {
		gid := groupID.Int64
		d.GroupID = &gid
	}
	d.GroupName = groupName.String
	d.Notes = notes.String
	d.Subject = subject.String
	d.Issuer = issuer.String
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
	return d, nil
}

func nowUnix() int64 { return time.Now().Unix() }

func nullableInt(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

func nullableInt64(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

func nullableString(v string) any {
	if v == "" {
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

func sqlQuote(s string) string {
	// escape single quotes for a VACUUM INTO path literal
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