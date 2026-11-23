package main

import (
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"

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

func md5Hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

// loadCleanEntries 拉待清洗条目：module='scrape:<id>' 且源 tier=3(llm_gen)。
// 严格排除：module=brand/migrated/imported/中文业务名（用户上传）、tier1/tier2 官方来源。
// doneIDs 里的 id 跳过（断点续传）；limit=0 不限。
func loadCleanEntries(raw *sql.DB, lang string, limit int, doneIDs map[int64]bool) ([]cleanEntry, error) {
	var args []interface{}
	// module 形如 scrape:314，数字从第 8 位起；substr 在 PG/SQLite 都可用（保留方言可移植）。
	q := `SELECT e.id, e.tenant_id, e.package_id, e.layer, e.source_lang, e.source_text, e.target_lang, e.target_text, e.module
		FROM kb_entries e
		JOIN kb_pack_sources s ON CAST(substr(e.module, 8) AS INTEGER) = s.id
		WHERE e.module LIKE 'scrape:%' AND s.tier=3`
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
			continue
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

// clearTMSegment 删除条目后清翻译检索层对应语种列（与 store.kbscrape.go:948 同形态）。
// 用 module+zh_hash 定位，避免误伤同包其它来源的同源文本。
func clearTMSegment(raw *sql.DB, e cleanEntry) {
	if !isValidLangColumn(e.TgtLang) {
		return // 列名白名单：tgtLang 拼进 SQL 标识符位置，必须校验
	}
	// zh_hash = md5(zh)（与 store.SaveEntry 同算法）
	h := md5Hex(e.SrcText)
	_, _ = db.Exec(raw, db.CurrentDialect(),
		fmt.Sprintf("UPDATE tm_segments SET %s='' WHERE zh_hash=? AND tenant_id=? AND pack_id=? AND module=?", e.TgtLang),
		h, 0, e.PackageID, e.Module)
}
