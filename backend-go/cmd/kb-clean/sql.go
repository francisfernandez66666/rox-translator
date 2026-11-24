package main

import (
	"context"
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"translator/internal/config"
	"translator/internal/db"
)

// isValidLangColumn 与 store.isValidLangColumn 同口径：tgtLang 拼进 SQL 列名必须白名单。
func isValidLangColumn(lang string) bool {
	for _, l := range config.AllLangs {
		if l == lang {
			return true
		}
	}
	return false
}

// md5Hex 生成与 TM 唯一键一致的源文哈希。
func md5Hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

// loadCleanEntries 拉待清洗条目：module='scrape:<id>'、源 tier=3(llm_gen) 且源语言为中文。
// 严格排除：module=brand/migrated/imported/中文业务名（用户上传）、tier1/tier2 官方来源、非中文源。
// doneIDs 里的 id 跳过（断点续传）；limit=0 不限。
func loadCleanEntries(raw *sql.DB, lang string, limit int, doneIDs map[int64]bool) ([]cleanEntry, error) {
	var args []interface{}
	// module 形如 scrape:314，数字从第 8 位起；substr 在 PG/SQLite 都可用（保留方言可移植）。
	q := `SELECT e.id, e.tenant_id, e.package_id, e.layer, e.source_lang, e.source_text, e.target_lang, e.target_text, e.module
		FROM kb_entries e
		JOIN kb_pack_sources s ON CAST(substr(e.module, 8) AS INTEGER) = s.id
		WHERE e.module LIKE 'scrape:%' AND s.tier=3 AND e.source_lang='zh'`
	if lang != "" {
		q += " AND e.target_lang=?"
		args = append(args, lang)
	}
	// 不在 SQL 层加 LIMIT：断点跳过的行数不定，SQL LIMIT 补偿容易算错；
	// Go 侧按有效条数 break 更稳（全量扫描只读不贵，判定才是成本大头）。
	q += " ORDER BY e.id"
	rows, err := db.Query(raw, db.CurrentDialect(), q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []cleanEntry
	for rows.Next() {
		var e cleanEntry
		if err := rows.Scan(&e.ID, &e.TenantID, &e.PackageID, &e.Layer, &e.SrcLang, &e.SrcText, &e.TgtLang, &e.TgtText, &e.Module); err != nil {
			return nil, fmt.Errorf("待清洗行扫描失败: %w", err)
		}
		if doneIDs[e.ID] {
			continue
		}
		if strings.TrimSpace(e.TgtText) == "" {
			continue
		}
		out = append(out, e)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, rows.Err()
}

// syncTMSegment 回写后同步翻译检索层对应语种列。
// ★ 必须显式调用：store.SaveEntry 命中已存在条目时只 UPDATE kb_entries 就 return（kbpackages.go:525-529），
//
//	不同步 tm_segments。清洗只改 kb_entries 会让 TM 检索仍召回旧错译——等于没洗干净。
//
// 定位用 tm_segments 唯一键 (zh_hash, tenant_id=0, pack_id)，不用 module
// （实测同词在 kb_entries 是 scrape:542、在 tm_segments 却是 scrape:454/534/446，module 对不上）。
func syncTMSegment(raw db.Execer, e cleanEntry, fixed string) error {
	if !isValidLangColumn(e.TgtLang) {
		return fmt.Errorf("非法目标语列名: %q", e.TgtLang)
	}
	res, err := db.Exec(raw, db.CurrentDialect(),
		fmt.Sprintf("UPDATE tm_segments SET %s=?, module=? WHERE zh_hash=? AND tenant_id=? AND pack_id=?", e.TgtLang),
		fixed, e.Module, md5Hex(e.SrcText), e.TMHostTenant, e.PackageID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("TM 同步命中 %d 行，期望 1", n)
	}
	return nil
}

// clearTMSegment 删除条目后清翻译检索层对应语种列（与 store.kbscrape.go:948 同形态）。
// 同样按唯一键 (zh_hash, tenant_id=0, pack_id) 定位，不按 module（理由见 syncTMSegment）。
func clearTMSegment(raw db.Execer, e cleanEntry) error {
	if !isValidLangColumn(e.TgtLang) {
		return fmt.Errorf("非法目标语列名: %q", e.TgtLang)
	}
	res, err := db.Exec(raw, db.CurrentDialect(),
		fmt.Sprintf("UPDATE tm_segments SET %s='' WHERE zh_hash=? AND tenant_id=? AND pack_id=?", e.TgtLang),
		md5Hex(e.SrcText), e.TMHostTenant, e.PackageID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("TM 清理命中 %d 行，期望 1", n)
	}
	return nil
}

// rowSnapshot 保存一条 kb_entries 的完整原始行，供删除后按原 id 恢复。
// vector 与时间戳也进入备份；只存 src/before 无法重建完整记录。
type rowSnapshot struct {
	ID         int64  `json:"id"`
	TenantID   int64  `json:"tenant_id"`
	PackageID  int64  `json:"package_id"`
	Layer      int    `json:"layer"`
	SourceLang string `json:"source_lang"`
	SourceText string `json:"source_text"`
	TargetLang string `json:"target_lang"`
	TargetText string `json:"target_text"`
	Module     string `json:"module"`
	Vector     []byte `json:"vector,omitempty"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
}

// loadCleanEntrySnapshot 在写库前现读完整原始行；id/租户/原文译文任一变化都由后续条件 UPDATE 拒绝。
func loadCleanEntrySnapshot(raw *sql.DB, e cleanEntry) (rowSnapshot, error) {
	var s rowSnapshot
	var created, updated sql.NullString
	err := db.QueryRow(raw, db.CurrentDialect(),
		`SELECT id,tenant_id,package_id,layer,source_lang,source_text,target_lang,target_text,module,vector,created_at,updated_at
		 FROM kb_entries WHERE id=? AND tenant_id=?`, e.ID, e.TenantID).
		Scan(&s.ID, &s.TenantID, &s.PackageID, &s.Layer, &s.SourceLang, &s.SourceText,
			&s.TargetLang, &s.TargetText, &s.Module, &s.Vector, &created, &updated)
	if err != nil {
		return s, err
	}
	s.CreatedAt, s.UpdatedAt = created.String, updated.String
	if s.SourceText != e.SrcText || s.TargetText != e.TgtText || s.TargetLang != e.TgtLang {
		return s, fmt.Errorf("待清洗行已变化，拒绝覆盖")
	}
	return s, nil
}

// updateKBEntryTx 按主键更新当前清洗行，并检查恰好命中一行；不复用自然键 upsert，避免改到重复行。
func updateKBEntryTx(tx *sql.Tx, e cleanEntry, fixed string) error {
	res, err := db.Exec(tx, db.CurrentDialect(),
		`UPDATE kb_entries SET target_text=?,updated_at=? WHERE id=? AND tenant_id=? AND package_id=? AND source_lang=? AND source_text=? AND target_lang=? AND target_text=? AND module=?`,
		fixed, time.Now().UTC().Format(time.RFC3339), e.ID, e.TenantID, e.PackageID, e.SrcLang, e.SrcText, e.TgtLang, e.TgtText, e.Module)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("KB 回写命中 %d 行，期望 1", n)
	}
	return nil
}

// deleteKBEntryTx 按主键删除当前清洗行，并检查恰好命中一行；0 行不能冒充删除成功。
func deleteKBEntryTx(tx *sql.Tx, e cleanEntry) error {
	res, err := db.Exec(tx, db.CurrentDialect(),
		`DELETE FROM kb_entries WHERE id=? AND tenant_id=? AND package_id=? AND source_lang=? AND source_text=? AND target_lang=? AND target_text=? AND module=?`,
		e.ID, e.TenantID, e.PackageID, e.SrcLang, e.SrcText, e.TgtLang, e.TgtText, e.Module)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("KB 删除命中 %d 行，期望 1", n)
	}
	return nil
}

// applyCleanChangeTx 把 TM 与 KB 的变更放进同一事务；任一步失败整体回滚，提交成功才允许落断点。
func applyCleanChangeTx(ctx context.Context, raw *sql.DB, e cleanEntry, fixed string, deleteMode bool) error {
	tx, err := raw.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if deleteMode {
		if err := clearTMSegment(tx, e); err != nil {
			return err
		}
		if err := deleteKBEntryTx(tx, e); err != nil {
			return err
		}
	} else {
		if err := syncTMSegment(tx, e, fixed); err != nil {
			return err
		}
		if err := updateKBEntryTx(tx, e, fixed); err != nil {
			return err
		}
	}
	return tx.Commit()
}
