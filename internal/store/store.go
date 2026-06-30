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
	db.SetMaxOpenConns(1) // sqlite single-writer; keeps things simple & safe
	return &Store{db: db, path: path}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) EnsureSchema() error {
	_, err := s.db.Exec(schema)
	if err != nil {
		return err
	}
	// seed default settings if missing.
	for k, v := range map[string]string{
		"alert_tiers":   "[30,7,1]",
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

// ---------------- groups ----------------

func (s *Store) ListGroups() ([]model.Group, error) {
	rows, err := s.db.Query(`SELECT id, name FROM domain_groups ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Group
	for rows.Next() {
		var g model.Group
		if err := rows.Scan(&g.ID, &g.Name); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (s *Store) CreateGroup(name string) (model.Group, error) {
	res, err := s.db.Exec(`INSERT INTO domain_groups(name, created_at) VALUES(?,?)`, name, nowUnix())
	if err != nil {
		return model.Group{}, err
	}
	id, _ := res.LastInsertId()
	return model.Group{ID: id, Name: name}, nil
}

func (s *Store) DeleteGroup(id int64) error {
	_, err := s.db.Exec(`DELETE FROM domain_groups WHERE id=?`, id)
	return err
}

// ---------------- domains ----------------

func (s *Store) ListDomains(search string, groupID int64) ([]model.Domain, error) {
	search = "%" + search + "%"
	rows, err := s.db.Query(domainListQuery, search, search, search, groupID, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Domain
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
	row := s.db.QueryRow(domainGetQuery, id)
	d, err := scanDomain(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &d, nil
}

func (s *Store) CreateDomain(host string, port int, groupID *int64, notes string) (model.Domain, error) {
	res, err := s.db.Exec(`INSERT INTO domains(host, port, group_id, notes, created_at) VALUES(?,?,?,?,?)`,
		host, port, nullableInt(groupID), notes, nowUnix())
	if err != nil {
		return model.Domain{}, err
	}
	id, _ := res.LastInsertId()
	return model.Domain{ID: id, Host: host, Port: port, GroupID: groupID, Notes: notes, CreatedAt: nowUnix()}, nil
}

func (s *Store) UpdateDomain(id int64, host string, port int, groupID *int64, notes string) error {
	_, err := s.db.Exec(`UPDATE domains SET host=?, port=?, group_id=?, notes=? WHERE id=?`,
		host, port, nullableInt(groupID), notes, id)
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
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ---------------- cert records ----------------

func (s *Store) UpsertCertRecord(rec model.Domain) error {
	sans, _ := json.Marshal(rec.SANs)
	_, err := s.db.Exec(`INSERT INTO cert_records(domain_id, subject, issuer, sans, serial_number, not_before, not_after, is_wildcard, days_remaining, last_checked, last_error)
		VALUES(?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(domain_id) DO UPDATE SET
		  subject=excluded.subject, issuer=excluded.issuer, sans=excluded.sans,
		  serial_number=excluded.serial_number, not_before=excluded.not_before,
		  not_after=excluded.not_after, is_wildcard=excluded.is_wildcard,
		  days_remaining=excluded.days_remaining, last_checked=excluded.last_checked,
		  last_error=excluded.last_error`,
		rec.ID, rec.Subject, rec.Issuer, string(sans), rec.SerialNumber,
		nullableInt64(rec.NotBefore), nullableInt64(rec.NotAfter), boolToInt(rec.IsWildcard),
		rec.DaysRemaining, rec.LastChecked, nullableString(rec.LastError))
	if err != nil {
		return err
	}
	// If the live cert changed (new serial), drop stale alert history for this domain.
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
		return // unreachable domain: leave prior alerts so we don't re-noise when it comes back at same cert
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

// ---------------- backup / restore ----------------

// Backup writes a consistent snapshot via VACUUM INTO and returns its bytes.
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

// ReplaceDB closes the connection, swaps the database file with the provided
// snapshot bytes, then reopens. Callers must call EnsureSchema afterwards.
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