package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

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

// EnsureSchema 把 schema 常量里的 DDL 全部跑一遍：
//   - 已存在的表/索引：IF NOT EXISTS 跳过
//   - 不存在的新建
//   - 废弃的表（schema 末尾的 DROP 语句）直接 drop
//
// 例外：share_id 是后加的字段（v1.x 升级到带 deep link 版本），老库 CREATE TABLE
// IF NOT EXISTS 不会补字段，这里 PRAGMA 检查后用一次性 ALTER 兜底。
// share_id 唯一索引也是这时候补上（partial index，允许多个 NULL）。
func (s *Store) EnsureSchema() error {
	if _, err := s.db.Exec(schema); err != nil {
		return err
	}
	// 老库兼容：share_id 字段不存在则补上
	rows, err := s.db.Query(`PRAGMA table_info(domains)`)
	if err != nil {
		return err
	}
	hasShareID := false
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			rows.Close()
			return err
		}
		if name == "share_id" {
			hasShareID = true
		}
	}
	rows.Close()
	if !hasShareID {
		if _, err := s.db.Exec(`ALTER TABLE domains ADD COLUMN share_id TEXT`); err != nil {
			return err
		}
	}
	// share_id 唯一索引：partial index，只对非 NULL 值约束唯一
	if _, err := s.db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_domains_share_id ON domains(share_id) WHERE share_id IS NOT NULL`); err != nil {
		return err
	}
	return nil
}

// EnsureLogin 首次启动 seed：settings 表里没有 login_user 时写入账号 + bcrypt 哈希。
// 之后改密码只能通过 settings 接口（或直接动 DB），不再读 .env。
func (s *Store) EnsureLogin(username, password string) error {
	existing, err := s.GetSetting("login_user")
	if err != nil {
		return err
	}
	if existing != "" {
		return nil // 已经 seed 过了
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	if err := s.SetSetting("login_user", username); err != nil {
		return err
	}
	return s.SetSetting("login_password", hash)
}

// GetLoginCredentials 取出当前登录账号 + 密码哈希。账号不存在时返回空串。
func (s *Store) GetLoginCredentials() (username, passwordHash string, err error) {
	username, err = s.GetSetting("login_user")
	if err != nil {
		return "", "", err
	}
	passwordHash, err = s.GetSetting("login_password")
	if err != nil {
		return "", "", err
	}
	return username, passwordHash, nil
}

// GetSetting 单 key 读取。
func (s *Store) GetSetting(key string) (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key=?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// ---------------- domains ----------------

// ListDomains 支持按多个标签 AND 过滤（域名必须同时拥有所有标签）
func (s *Store) ListDomains(search string, tagIDs []int64) ([]model.Domain, error) {
	q := domainListQuery
	args := []any{"%" + search + "%", "%" + search + "%", "%" + search + "%"}

	// 多标签 AND：用 GROUP BY + HAVING COUNT 实现
	// 选了 N 个标签，域名的关联记录里至少 N 条匹配
	if len(tagIDs) > 0 {
		placeholders := ""
		for i, id := range tagIDs {
			if i > 0 {
				placeholders += ","
			}
			placeholders += "?"
			args = append(args, id)
		}
		q += ` AND id IN (
			SELECT domain_id FROM domain_tags
			WHERE tag_id IN (` + placeholders + `)
			GROUP BY domain_id
			HAVING COUNT(DISTINCT tag_id) = ?
		)`
		args = append(args, len(tagIDs))
	}
	q += domainListOrderBy

	rows, err := s.db.Query(q, args...)
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) > 0 {
		ids := make([]int64, len(out))
		for i, d := range out {
			ids[i] = d.ID
		}
		tagsMap, err := s.loadTagsForDomains(ids)
		if err != nil {
			return nil, err
		}
		for i := range out {
			out[i].Tags = tagsMap[out[i].ID]
		}
	}
	return out, nil
}

func (s *Store) GetDomain(id int64) (*model.Domain, error) {
	d, err := scanDomain(s.db.QueryRow(domainGetQuery, id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	tagsMap, err := s.loadTagsForDomains([]int64{d.ID})
	if err != nil {
		return nil, err
	}
	d.Tags = tagsMap[d.ID]
	return &d, nil
}

func (s *Store) CreateDomain(host string, port int, path, notes string, tagIDs []int64) (model.Domain, error) {
	// 新域名默认排到第一位（MIN(sort_order) - 1，可能为负数）
	shareID := newShareID()
	res, err := s.db.Exec(`INSERT INTO domains(host, port, path, notes, created_at, sort_order, share_id)
		VALUES(?,?,?,?,?, COALESCE((SELECT MIN(sort_order) FROM domains), 1) - 1, ?)`,
		host, port, normalizePath(path), nullableString(notes), nowUnix(), shareID)
	if err != nil {
		return model.Domain{}, err
	}
	id, _ := res.LastInsertId()
	if err := s.SetDomainTags(id, tagIDs); err != nil {
		return model.Domain{}, err
	}
	return model.Domain{ID: id, Host: host, Port: port, Path: path, Notes: notes, CreatedAt: nowUnix(), ShareID: shareID}, nil
}

// newShareID 生成 16 字符 hex 随机串（8 字节熵）。
// 用于 /view/<token>?id=<share_id> 这种 deep link，避免暴露自增 id。
// 8 字节 = 2^64 熵，对于几千个域名来说碰撞概率 = 0。
func newShareID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand 失败几乎不可能发生；退化到时间戳避免卡死
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

func normalizePath(p string) string {
	if p == "" {
		return "/"
	}
	return p
}

// ReorderDomains 按 orderedIDs 顺序批量更新 sort_order
func (s *Store) ReorderDomains(orderedIDs []int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for i, id := range orderedIDs {
		if _, err := tx.Exec(`UPDATE domains SET sort_order=? WHERE id=?`, i, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// UpdateDomain 更新用户可编辑字段（host/port/path/notes/tags），不触碰探测结果
func (s *Store) UpdateDomain(id int64, host string, port int, path, notes string, tagIDs []int64) error {
	_, err := s.db.Exec(`UPDATE domains SET host=?, port=?, path=?, notes=? WHERE id=?`,
		host, port, normalizePath(path), nullableString(notes), id)
	if err != nil {
		return err
	}
	return s.SetDomainTags(id, tagIDs)
}

// SetDomainTags 全量替换某域名的标签关联（删除旧的，插入新的）
func (s *Store) SetDomainTags(domainID int64, tagIDs []int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM domain_tags WHERE domain_id=?`, domainID); err != nil {
		return err
	}
	for _, tagID := range tagIDs {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO domain_tags(domain_id, tag_id) VALUES(?,?)`, domainID, tagID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// loadTagsForDomains 批量查多对多关联：返回 domainID -> tags 列表（含 icon/color）
func (s *Store) loadTagsForDomains(domainIDs []int64) (map[int64][]model.Tag, error) {
	out := map[int64][]model.Tag{}
	if len(domainIDs) == 0 {
		return out, nil
	}
	placeholders := ""
	args := make([]any, 0, len(domainIDs))
	for i, id := range domainIDs {
		if i > 0 {
			placeholders += ","
		}
		placeholders += "?"
		args = append(args, id)
	}
	q := `SELECT dt.domain_id, t.id, t.name, COALESCE(t.icon,''), COALESCE(t.color,'')
	      FROM domain_tags dt
	      JOIN tags t ON t.id = dt.tag_id
	      WHERE dt.domain_id IN (` + placeholders + `)
	      ORDER BY t.sort_order ASC, t.id ASC`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var domainID, tagID int64
		var tagName, icon, color string
		if err := rows.Scan(&domainID, &tagID, &tagName, &icon, &color); err != nil {
			return nil, err
		}
		out[domainID] = append(out[domainID], model.Tag{
			ID: tagID, Name: tagName, Icon: icon, Color: color,
		})
	}
	return out, rows.Err()
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

// UpdateDomainProbe 写入一次 TLS + HTTP 探测的结果（仅探测字段，不动用户字段）
func (s *Store) UpdateDomainProbe(rec model.Domain) error {
	sans, _ := json.Marshal(rec.SANs)
	_, err := s.db.Exec(`UPDATE domains SET
		subject=?, issuer=?, issuer_org=?, sans=?, serial_number=?,
		not_before=?, not_after=?, is_wildcard=?, days_remaining=?,
		last_checked=?, last_error=?,
		http_status=?, http_error=?, http_checked=?
		WHERE id=?`,
		nullableString(rec.Subject), nullableString(rec.Issuer), nullableString(rec.IssuerOrg),
		string(sans), nullableString(rec.SerialNumber),
		nullableInt64(rec.NotBefore), nullableInt64(rec.NotAfter),
		boolToInt(rec.IsWildcard), rec.DaysRemaining,
		rec.LastChecked, nullableString(rec.LastError),
		nullableHTTPStatus(rec.HTTPStatus), nullableString(rec.HTTPError), nullableInt64(rec.HTTPChecked),
		rec.ID)
	return err
}

// nullableHTTPStatus 0 视为未探测，存 NULL
func nullableHTTPStatus(v int) any {
	if v == 0 {
		return nil
	}
	return v
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
	rows, err := s.db.Query(`SELECT id, name, COALESCE(icon,''), COALESCE(color,'') FROM tags ORDER BY sort_order ASC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]model.Tag, 0)
	for rows.Next() {
		var t model.Tag
		if err := rows.Scan(&t.ID, &t.Name, &t.Icon, &t.Color); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) CreateTag(name string) (model.Tag, error) {
	// 新标签排到最后
	res, err := s.db.Exec(`INSERT INTO tags(name, created_at, sort_order)
		VALUES(?, ?, COALESCE((SELECT MAX(sort_order) FROM tags), -1) + 1)`,
		name, nowUnix())
	if err != nil {
		return model.Tag{}, err
	}
	id, _ := res.LastInsertId()
	return model.Tag{ID: id, Name: name}, nil
}

// UpdateTag 增量更新标签字段（name / icon / color）；空串视为清空
func (s *Store) UpdateTag(id int64, name, icon, color string) error {
	_, err := s.db.Exec(`UPDATE tags SET name=?, icon=?, color=? WHERE id=?`,
		name, nullableString(icon), nullableString(color), id)
	return err
}

func (s *Store) DeleteTag(id int64) error {
	_, err := s.db.Exec(`DELETE FROM tags WHERE id=?`, id)
	return err
}

func (s *Store) GetTagByID(id int64) (*model.Tag, error) {
	var t model.Tag
	err := s.db.QueryRow(`SELECT id, name, COALESCE(icon,''), COALESCE(color,'') FROM tags WHERE id=?`, id).
		Scan(&t.ID, &t.Name, &t.Icon, &t.Color)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// ReorderTags 按 orderedIDs 顺序写入 sort_order（事务批量更新）
func (s *Store) ReorderTags(orderedIDs []int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for i, id := range orderedIDs {
		if _, err := tx.Exec(`UPDATE tags SET sort_order=? WHERE id=?`, i, id); err != nil {
			return err
		}
	}
	return tx.Commit()
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