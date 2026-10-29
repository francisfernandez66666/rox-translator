// ============ domain_profiles.go · 职责说明 ============
// 文档级领域词表的缓存表（2026-10-29 第十二轮）：按样本指纹存一份 profile（含各语种 targets）。
//
// 键含 tenant_id（品牌过滤与术语层去重依赖租户）；**不含 lang**（一次 wiki 取全语种）。
// 没有主动失效：内容哈希为键，内容变了自然换键；过期由 expires_at 判 + 定时清理。
// ============================================
package store

import (
	"database/sql"
	"errors"
	"time"

	"translator/internal/db"
)

// DomainProfileRow 一行 domain_profiles。
type DomainProfileRow struct {
	Fingerprint string
	TenantID    int64
	Code        string
	Label       string
	Confidence  float64
	TermsJSON   string
	ExpiresAt   string // RFC3339
}

// DomainProfileMigrate 建表（幂等）。
func (s *Store) DomainProfileMigrate() error {
	_, err := db.Exec(s.db, db.CurrentDialect(), `
		CREATE TABLE IF NOT EXISTS domain_profiles (
			fingerprint TEXT PRIMARY KEY,
			tenant_id   INTEGER NOT NULL,
			code        TEXT NOT NULL,
			label       TEXT NOT NULL,
			confidence  REAL NOT NULL,
			terms_json  TEXT NOT NULL,
			created_at  TEXT NOT NULL,
			expires_at  TEXT NOT NULL
		)`)
	return err
}

// GetDomainProfile 取未过期的一行；不存在返回 (nil, nil)。
func (s *Store) GetDomainProfile(fingerprint string, now time.Time) (*DomainProfileRow, error) {
	row := &DomainProfileRow{}
	err := db.QueryRow(s.db, db.CurrentDialect(),
		`SELECT fingerprint, tenant_id, code, label, confidence, terms_json, expires_at
		   FROM domain_profiles WHERE fingerprint=? AND expires_at > ?`,
		fingerprint, now.UTC().Format(time.RFC3339)).Scan(
		&row.Fingerprint, &row.TenantID, &row.Code, &row.Label, &row.Confidence, &row.TermsJSON, &row.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return row, nil
}

// PutDomainProfile upsert 一行（两种方言都认 ON CONFLICT）。
func (s *Store) PutDomainProfile(row *DomainProfileRow) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.Exec(s.db, db.CurrentDialect(), `
		INSERT INTO domain_profiles (fingerprint, tenant_id, code, label, confidence, terms_json, created_at, expires_at)
		VALUES (?,?,?,?,?,?,?,?)
		ON CONFLICT(fingerprint) DO UPDATE SET
			code=excluded.code, label=excluded.label, confidence=excluded.confidence,
			terms_json=excluded.terms_json, expires_at=excluded.expires_at`,
		row.Fingerprint, row.TenantID, row.Code, row.Label, row.Confidence, row.TermsJSON, now, row.ExpiresAt)
	return err
}

// PruneDomainProfiles 清过期行（不带尾随 LIMIT：PG/挂件 SQLite 都不认，AGENTS §一·4）。
func (s *Store) PruneDomainProfiles(now time.Time) error {
	_, err := db.Exec(s.db, db.CurrentDialect(),
		`DELETE FROM domain_profiles WHERE expires_at < ?`, now.UTC().Format(time.RFC3339))
	return err
}
