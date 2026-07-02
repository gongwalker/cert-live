package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	_ "modernc.org/sqlite"

	"cert-live/internal/auth"
	"cert-live/internal/model"
)

type Store struct {
	db   *sql.DB
	path string
	mu   sync.Mutex
}

func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create data dir: %w", err)
		}
	}
	dsn := path + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // sqlite single-writer
	return &Store{db: db, path: path}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) EnsureSchema() error {
	_, err := s.db.Exec(schema)
	if err != nil {
		return err
	}
	for k, v := range map[string]string{
		"alert_tiers":    "[30,7,1]",
		"check_interval": "360",
	} {
		_, _ = s.db.Exec(`INSERT OR IGNORE INTO settings(key,value) VALUES(?,?)`, k, v)
	}
	return nil
}

func (s *Store) EnsureAdmin(username, password string) error {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(1) FROM users`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO users(username, password_hash, created_at) VALUES(?,?,?)`,
		username, hash, nowUnix())
	return err
}

func (s *Store) GetUserByUsername(username string) (*model.User, error) {
	u := &model.User{}
	err := s.db.QueryRow(`SELECT id, username, password_hash FROM users WHERE username=?`, username).
		Scan(&u.ID, &u.Username, &u.PasswordHash)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return u, nil
}

// ---------------- domains ----------------

func (s *Store) ListDomains(search string) ([]model.Domain, error) {
	rows, err := s.db.Query(domainListQuery, "%"+search+"%", "%"+search+"%", "%"+search+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]model.Domain, 0)
	for rows.Next() {
		d, err := scanDomain(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) GetDomain(id int64) (*model.Domain, error) {
	d, err := scanDomain(s.db.QueryRow(domainGetQuery, id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &d, nil
}

func (s *Store) CreateDomain(host string, port int, notes string) (model.Domain, error) {
	res, err := s.db.Exec(`INSERT INTO domains(host, port, notes, created_at) VALUES(?,?,?,?)`,
		host, port, nullableString(notes), nowUnix())
	if err != nil {
		return model.Domain{}, err
	}
	id, _ := res.LastInsertId()
	return model.Domain{ID: id, Host: host, Port: port, Notes: notes, CreatedAt: nowUnix()}, nil
}

// UpdateDomain 更新用户可编辑字段（host/port/notes），不触碰探测结果
func (s *Store) UpdateDomain(id int64, host string, port int, notes string) error {
	_, err := s.db.Exec(`UPDATE domains SET host=?, port=?, notes=? WHERE id=?`,
		host, port, nullableString(notes), id)
	return err
}

func (s *Store) DeleteDomain(id int64) error {
	_, err := s.db.Exec(`DELETE FROM domains WHERE id=?`, id)
	return err
}

func (s *Store) ListAllDomainIDs() ([]int64, error) {
	rows, err := s.db.Query(`SELECT id FROM domains`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]int64, 0)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// UpdateDomainProbe 写入一次 TLS 探测的结果（仅探测字段，不动用户字段）
func (s *Store) UpdateDomainProbe(rec model.Domain) error {
	sans, _ := json.Marshal(rec.SANs)
	_, err := s.db.Exec(`UPDATE domains SET
		subject=?, issuer=?, issuer_org=?, sans=?, serial_number=?,
		not_before=?, not_after=?, is_wildcard=?, days_remaining=?,
		last_checked=?, last_error=?
		WHERE id=?`,
		nullableString(rec.Subject), nullableString(rec.Issuer), nullableString(rec.IssuerOrg),
		string(sans), nullableString(rec.SerialNumber),
		nullableInt64(rec.NotBefore), nullableInt64(rec.NotAfter),
		boolToInt(rec.IsWildcard), rec.DaysRemaining,
		rec.LastChecked, nullableString(rec.LastError),
		rec.ID)
	if err != nil {
		return err
	}
	s.purgeStaleAlerts(rec.ID, rec.SerialNumber, rec.LastError != "")
	return nil
}

// ---------------- alert log ----------------

func (s *Store) HasAlerted(domainID int64, serial string, tier int) (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(1) FROM alert_log WHERE domain_id=? AND cert_serial=? AND tier=?`,
		domainID, serial, tier).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func (s *Store) RecordAlert(domainID int64, serial string, tier int) error {
	_, err := s.db.Exec(`INSERT OR IGNORE INTO alert_log(domain_id, cert_serial, tier, alerted_at) VALUES(?,?,?,?)`,
		domainID, serial, tier, nowUnix())
	return err
}

func (s *Store) purgeStaleAlerts(domainID int64, currentSerial string, hasError bool) {
	if hasError {
		return // 不可达域名：保留旧告警，避免它恢复时同张证书再次噪声
	}
	_, _ = s.db.Exec(`DELETE FROM alert_log WHERE domain_id=? AND cert_serial<>?`, domainID, currentSerial)
}

// ---------------- settings ----------------

func (s *Store) GetAll() (map[string]string, error) {
	rows, err := s.db.Query(`SELECT key, value FROM settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(`INSERT INTO settings(key,value) VALUES(?,?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

// ---------------- tags ----------------

func (s *Store) ListTags() ([]model.Tag, error) {
	rows, err := s.db.Query(`SELECT id, name FROM tags ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]model.Tag, 0)
	for rows.Next() {
		var t model.Tag
		if err := rows.Scan(&t.ID, &t.Name); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) CreateTag(name string) (model.Tag, error) {
	res, err := s.db.Exec(`INSERT INTO tags(name, created_at) VALUES(?,?)`, name, nowUnix())
	if err != nil {
		return model.Tag{}, err
	}
	id, _ := res.LastInsertId()
	return model.Tag{ID: id, Name: name}, nil
}

func (s *Store) DeleteTag(id int64) error {
	_, err := s.db.Exec(`DELETE FROM tags WHERE id=?`, id)
	return err
}

// ---------------- backup / restore ----------------

// Backup 通过 VACUUM INTO 拿到一致快照
func (s *Store) Backup() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tmp := s.path + ".backup-" + randSuffix()
	defer os.Remove(tmp)
	stmt := fmt.Sprintf(`VACUUM INTO %s`, sqlQuote(tmp))
	if _, err := s.db.Exec(stmt); err != nil {
		return nil, err
	}
	return os.ReadFile(tmp)
}

// ReplaceDB 关闭连接、替换 .db 文件、再开。调用方需再调 EnsureSchema。
func (s *Store) ReplaceDB(data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.db.Close(); err != nil {
		return err
	}
	tmp := s.path + ".restoring-" + randSuffix()
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return err
	}
	db, err := sql.Open("sqlite", s.path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		return err
	}
	s.db = db
	return nil
}

func (s *Store) DB() *sql.DB { return s.db }