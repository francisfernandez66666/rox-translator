// ============ kbscrape.go · 职责说明 ============
// store 包「行业包/语言文化包自动采集」数据访问层（2026-09-01 新功能）。
// 三张待审/源管理表：
//   kb_pack_sources   数据源（official_api / limited_web / llm_gen，可启停/限频/记状态）
//   kb_staged_entries 增量待审条目（术语/TM/碎片），超管审批通过后落 kb_entries + tm_segments
//   kb_staged_phrases 增量待审语言文化安全句，审批通过后落 kb_safety_phrases
// 断点续传经 system_config 持久化（kb_scrape_checkpoint_<date>_<source_id> 等）。
// =============================================
package store

import (
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"strconv"
	"strings"
	"time"

	"translator/internal/db"
)

// KBScrapeSource 数据源（采集引擎按此表驱动每日任务）
type KBScrapeSource struct {
	ID         int64  `json:"id"`         // 源主键 ID
	Kind       string `json:"kind"`       // official_api / limited_web / llm_gen
	Name       string `json:"name"`       // 源名称
	BaseURL    string `json:"base_url"`   // 入口 URL（llm_gen 可为空）
	Lang       string `json:"lang"`       // 目标语言（空=不限/全量）
	Industry   string `json:"industry"`   // 行业 code（空=不限/语言文化包）
	PackType   string `json:"pack_type"`  // industry / locale
	Enabled    int    `json:"enabled"`    // 1=启用参与采集 0=停用
	FreqHours  int    `json:"freq_hours"` // 采集频次（小时）
	Tier       int    `json:"tier"`       // 数据可信度：1官方 / 2受限抓取 / 3LLM
	LastRunAt  string `json:"last_run_at"`  // 最近采集时间
	LastStatus string `json:"last_status"`  // 最近状态：ok / error:<msg>
	CreatedAt  string `json:"created_at"`
}

// KBStagedEntry 增量待审条目（术语/TM/碎片）
type KBStagedEntry struct {
	ID           int64  `json:"id"`           // 待审主键
	TargetPackID int64  `json:"target_pack_id"` // 审批通过后落入的正式包 ID
	PackType     string `json:"pack_type"`    // industry / locale
	SourceID     int64  `json:"source_id"`    // 来源数据源 ID（0=手动投喂）
	Tier         int    `json:"tier"`         // 1官方 / 2受限抓取 / 3LLM
	Layer        int    `json:"layer"`        // 1术语/2TM/3安全句/4碎片
	SrcLang      string `json:"src_lang"`     // 源语言
	SrcText      string `json:"src_text"`     // 源文本
	TgtLang      string `json:"tgt_lang"`     // 目标语言
	TgtText      string `json:"tgt_text"`     // 目标译文
	SourceURL    string `json:"source_url"`   // 出处（抓取/API 溯源；LLM 为提示描述）
	SrcHash      string `json:"src_hash"`     // md5 去重键
	Status       string `json:"status"`       // pending / approved / rejected
	CreatedAt    string `json:"created_at"`
	AppliedAt    string `json:"applied_at"`
}

// KBStagedPhrase 增量待审语言文化安全句
type KBStagedPhrase struct {
	ID          int64  `json:"id"`
	PackageID   int64  `json:"package_id"` // 审批后落入的语言文化包 ID
	Lang        string `json:"lang"`
	Phrase      string `json:"phrase"`
	Kind        string `json:"kind"` // style / forbidden / replace
	Replacement string `json:"replacement"`
	Tier        int    `json:"tier"`
	SrcHash     string `json:"src_hash"` // md5(lang|kind|phrase|replacement)
	Status      string `json:"status"`
	CreatedAt   string `json:"created_at"`
	AppliedAt   string `json:"applied_at"`
}

// ScrapeStagedSummary 待审池汇总（管理面板一屏概览）
type ScrapeStagedSummary struct {
	PendingEntries int `json:"pending_entries"` // 待审条目数
	PendingPhrases int `json:"pending_phrases"` // 待审安全句数
	SourcesTotal   int `json:"sources_total"`   // 数据源总数
	SourcesEnabled int `json:"sources_enabled"` // 启用源数
	LastDaily      string `json:"last_daily"`   // 最近完成采集日（YYYY-MM-DD）
}

// scrapeEntryHash 待审条目去重键：md5(src_lang|src_text|tgt_lang|tgt_text)。
func scrapeEntryHash(srcLang, srcText, tgtLang, tgtText string) string {
	sum := md5.Sum([]byte(srcLang + "\x00" + srcText + "\x00" + tgtLang + "\x00" + tgtText))
	return hex.EncodeToString(sum[:])
}

// scrapePhraseHash 待审安全句去重键：md5(lang|kind|phrase|replacement)。
func scrapePhraseHash(lang, kind, phrase, replacement string) string {
	sum := md5.Sum([]byte(lang + "\x00" + kind + "\x00" + phrase + "\x00" + replacement))
	return hex.EncodeToString(sum[:])
}

// KBScrapeMigrate 建表（幂等，Store.New 迁移链调用）。
func (s *Store) KBScrapeMigrate() {
	d := db.CurrentDialect()
	db.Exec(s.db, d, `CREATE TABLE IF NOT EXISTS kb_pack_sources (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		kind TEXT NOT NULL DEFAULT 'official_api',
		name TEXT NOT NULL DEFAULT '',
		base_url TEXT DEFAULT '',
		lang TEXT DEFAULT '',
		industry TEXT DEFAULT '',
		pack_type TEXT DEFAULT 'locale',
		enabled INTEGER DEFAULT 1,
		freq_hours INTEGER DEFAULT 24,
		tier INTEGER DEFAULT 3,
		last_run_at TEXT DEFAULT '',
		last_status TEXT DEFAULT '',
		created_at TEXT DEFAULT '')`)
	db.Exec(s.db, d, `CREATE INDEX IF NOT EXISTS idx_kb_pack_sources_en ON kb_pack_sources(enabled, pack_type)`)
	db.Exec(s.db, d, `CREATE TABLE IF NOT EXISTS kb_staged_entries (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		target_pack_id INTEGER NOT NULL DEFAULT 0,
		pack_type TEXT NOT NULL DEFAULT 'locale',
		source_id INTEGER DEFAULT 0,
		tier INTEGER DEFAULT 3,
		layer INTEGER DEFAULT 1,
		src_lang TEXT NOT NULL DEFAULT '',
		src_text TEXT NOT NULL DEFAULT '',
		tgt_lang TEXT NOT NULL DEFAULT '',
		tgt_text TEXT NOT NULL DEFAULT '',
		source_url TEXT DEFAULT '',
		src_hash TEXT NOT NULL DEFAULT '',
		status TEXT DEFAULT 'pending',
		created_at TEXT DEFAULT '',
		applied_at TEXT DEFAULT '')`)
	db.Exec(s.db, d, `CREATE UNIQUE INDEX IF NOT EXISTS idx_staged_entry_hash ON kb_staged_entries(src_hash)`)
	db.Exec(s.db, d, `CREATE INDEX IF NOT EXISTS idx_staged_entry_status ON kb_staged_entries(status, pack_type, id DESC)`)
	db.Exec(s.db, d, `CREATE TABLE IF NOT EXISTS kb_staged_phrases (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		package_id INTEGER NOT NULL DEFAULT 0,
		lang TEXT NOT NULL DEFAULT '',
		phrase TEXT NOT NULL DEFAULT '',
		kind TEXT DEFAULT 'style',
		replacement TEXT DEFAULT '',
		tier INTEGER DEFAULT 3,
		src_hash TEXT NOT NULL DEFAULT '',
		status TEXT DEFAULT 'pending',
		created_at TEXT DEFAULT '',
		applied_at TEXT DEFAULT '')`)
	db.Exec(s.db, d, `CREATE UNIQUE INDEX IF NOT EXISTS idx_staged_phrase_hash ON kb_staged_phrases(src_hash)`)
	db.Exec(s.db, d, `CREATE INDEX IF NOT EXISTS idx_staged_phrase_status ON kb_staged_phrases(status, lang, id DESC)`)
}

// ============ 数据源 ============

// CreateScrapeSource 新增数据源。
// 参数：src=数据源字段（ID 忽略，返回带新 ID 的源对象）。
func (s *Store) CreateScrapeSource(src *KBScrapeSource) (*KBScrapeSource, error) {
	if src.Kind == "" {
		src.Kind = "official_api"
	}
	if src.PackType == "" {
		src.PackType = "locale"
	}
	if src.Tier <= 0 {
		src.Tier = 3
	}
	if src.FreqHours <= 0 {
		src.FreqHours = 24
	}
	if src.Enabled == 0 {
		src.Enabled = 1
	}
	now := time.Now().Format(time.RFC3339)
	id, err := db.InsertID(s.db, db.CurrentDialect(), "id",
		"INSERT INTO kb_pack_sources (kind, name, base_url, lang, industry, pack_type, enabled, freq_hours, tier, last_run_at, last_status, created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)",
		src.Kind, src.Name, src.BaseURL, src.Lang, src.Industry, src.PackType, src.Enabled, src.FreqHours, src.Tier, "", "", now)
	if err != nil {
		return nil, err
	}
	src.ID = id
	src.CreatedAt = now
	return src, nil
}

// UpdateScrapeSource 更新数据源基础字段（名称/URL/语言/行业/类型/频次/tier）。
// 参数：id=源 ID；src=待更新字段；返回错误。
func (s *Store) UpdateScrapeSource(id int64, src *KBScrapeSource) error {
	_, err := db.Exec(s.db, db.CurrentDialect(),
		"UPDATE kb_pack_sources SET name=?, base_url=?, lang=?, industry=?, pack_type=?, freq_hours=?, tier=? WHERE id=?",
		src.Name, src.BaseURL, src.Lang, src.Industry, src.PackType, src.FreqHours, src.Tier, id)
	return err
}

// ListScrapeSources 列出全部数据源（按 pack_type、id 排序）。
func (s *Store) ListScrapeSources() ([]*KBScrapeSource, error) {
	rows, err := db.Query(s.db, db.CurrentDialect(),
		"SELECT id, kind, name, COALESCE(base_url,''), COALESCE(lang,''), COALESCE(industry,''), COALESCE(pack_type,'locale'), COALESCE(enabled,1), COALESCE(freq_hours,24), COALESCE(tier,3), COALESCE(last_run_at,''), COALESCE(last_status,''), COALESCE(created_at,'') FROM kb_pack_sources ORDER BY pack_type, id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*KBScrapeSource
	for rows.Next() {
		var p KBScrapeSource
		if err := rows.Scan(&p.ID, &p.Kind, &p.Name, &p.BaseURL, &p.Lang, &p.Industry,
			&p.PackType, &p.Enabled, &p.FreqHours, &p.Tier, &p.LastRunAt, &p.LastStatus, &p.CreatedAt); err == nil {
			out = append(out, &p)
		}
	}
	return out, nil
}

// ListEnabledScrapeSources 列出启用中的数据源（采集引擎消费）。
func (s *Store) ListEnabledScrapeSources() ([]*KBScrapeSource, error) {
	rows, err := db.Query(s.db, db.CurrentDialect(),
		"SELECT id, kind, name, COALESCE(base_url,''), COALESCE(lang,''), COALESCE(industry,''), COALESCE(pack_type,'locale'), COALESCE(enabled,1), COALESCE(freq_hours,24), COALESCE(tier,3), COALESCE(last_run_at,''), COALESCE(last_status,''), COALESCE(created_at,'') FROM kb_pack_sources WHERE COALESCE(enabled,1)=1 ORDER BY pack_type, id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*KBScrapeSource
	for rows.Next() {
		var p KBScrapeSource
		if err := rows.Scan(&p.ID, &p.Kind, &p.Name, &p.BaseURL, &p.Lang, &p.Industry,
			&p.PackType, &p.Enabled, &p.FreqHours, &p.Tier, &p.LastRunAt, &p.LastStatus, &p.CreatedAt); err == nil {
			out = append(out, &p)
		}
	}
	return out, nil
}

// GetScrapeSource 按 ID 查数据源。
func (s *Store) GetScrapeSource(id int64) (*KBScrapeSource, error) {
	row := db.QueryRow(s.db, db.CurrentDialect(),
		"SELECT id, kind, name, COALESCE(base_url,''), COALESCE(lang,''), COALESCE(industry,''), COALESCE(pack_type,'locale'), COALESCE(enabled,1), COALESCE(freq_hours,24), COALESCE(tier,3), COALESCE(last_run_at,''), COALESCE(last_status,''), COALESCE(created_at,'') FROM kb_pack_sources WHERE id=?", id)
	var p KBScrapeSource
	if err := row.Scan(&p.ID, &p.Kind, &p.Name, &p.BaseURL, &p.Lang, &p.Industry,
		&p.PackType, &p.Enabled, &p.FreqHours, &p.Tier, &p.LastRunAt, &p.LastStatus, &p.CreatedAt); err != nil {
		return nil, err
	}
	return &p, nil
}

// SetScrapeSourceEnabled 启停数据源。
// 参数：id=源 ID；enabled=1 启用 / 0 停用；返回错误。
func (s *Store) SetScrapeSourceEnabled(id int64, enabled int) error {
	if enabled != 0 {
		enabled = 1
	}
	_, err := db.Exec(s.db, db.CurrentDialect(), "UPDATE kb_pack_sources SET enabled=? WHERE id=?", enabled, id)
	return err
}

// SetScrapeSourceStatus 记录数据源最近一次采集状态（成功后清空错误）。
// 参数：id=源 ID；status=ok / error:<msg>；返回错误。
func (s *Store) SetScrapeSourceStatus(id int64, status string) error {
	_, err := db.Exec(s.db, db.CurrentDialect(),
		"UPDATE kb_pack_sources SET last_run_at=?, last_status=? WHERE id=?",
		time.Now().Format(time.RFC3339), status, id)
	return err
}

// DeleteScrapeSource 删除数据源（连带清理其待审记录，正式包不动）。
// 参数：id=源 ID；返回错误。
func (s *Store) DeleteScrapeSource(id int64) error {
	db.Exec(s.db, db.CurrentDialect(), "DELETE FROM kb_staged_entries WHERE source_id=?", id)
	_, err := db.Exec(s.db, db.CurrentDialect(), "DELETE FROM kb_pack_sources WHERE id=?", id)
	return err
}

// ============ 待审条目 ============

// StageEntry 写入一条待审条目（hash 去重，重复忽略）。
// 参数：e=待审条目字段；返回是否新增（false=已存在跳过）。
func (s *Store) StageEntry(e *KBStagedEntry) (bool, error) {
	if e.SrcText == "" || e.TgtText == "" || e.TgtLang == "" {
		return false, nil
	}
	if e.SrcHash == "" {
		e.SrcHash = scrapeEntryHash(e.SrcLang, e.SrcText, e.TgtLang, e.TgtText)
	}
	if e.Status == "" {
		e.Status = "pending"
	}
	if e.Tier <= 0 {
		e.Tier = 3
	}
	now := time.Now().Format(time.RFC3339)
	res, err := db.Exec(s.db, db.CurrentDialect(),
		"INSERT OR IGNORE INTO kb_staged_entries (target_pack_id, pack_type, source_id, tier, layer, src_lang, src_text, tgt_lang, tgt_text, source_url, src_hash, status, created_at, applied_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)",
		e.TargetPackID, e.PackType, e.SourceID, e.Tier, e.Layer, e.SrcLang, e.SrcText, e.TgtLang, e.TgtText, e.SourceURL, e.SrcHash, e.Status, now, "")
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// StageEntriesBatch 批量写入待审条目（单事务，hash 去重）；返回新增条数。
func (s *Store) StageEntriesBatch(items []*KBStagedEntry) (int, error) {
	if len(items) == 0 {
		return 0, nil
	}
	now := time.Now().Format(time.RFC3339)
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	added := 0
	for _, e := range items {
		if e.SrcText == "" || e.TgtText == "" || e.TgtLang == "" {
			continue
		}
		if e.SrcHash == "" {
			e.SrcHash = scrapeEntryHash(e.SrcLang, e.SrcText, e.TgtLang, e.TgtText)
		}
		if e.Status == "" {
			e.Status = "pending"
		}
		if e.Tier <= 0 {
			e.Tier = 3
		}
		res, err := db.Exec(tx, db.CurrentDialect(),
			"INSERT OR IGNORE INTO kb_staged_entries (target_pack_id, pack_type, source_id, tier, layer, src_lang, src_text, tgt_lang, tgt_text, source_url, src_hash, status, created_at, applied_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)",
			e.TargetPackID, e.PackType, e.SourceID, e.Tier, e.Layer, e.SrcLang, e.SrcText, e.TgtLang, e.TgtText, e.SourceURL, e.SrcHash, e.Status, now, "")
		if err != nil {
			return added, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			added++
		}
	}
	if err := tx.Commit(); err != nil {
		return added, err
	}
	return added, nil
}

// ListStagedEntries 列出待审条目（可按包/类型/状态/语言筛选，分页）。
// 参数：packType/status/lang 空=不过滤；limit<=0 取 200。
func (s *Store) ListStagedEntries(packType, status, lang string, limit, offset int) ([]*KBStagedEntry, error) {
	q := "SELECT id, target_pack_id, pack_type, source_id, tier, layer, src_lang, src_text, tgt_lang, tgt_text, source_url, src_hash, status, COALESCE(created_at,''), COALESCE(applied_at,'') FROM kb_staged_entries WHERE 1=1"
	args := []interface{}{}
	if packType != "" {
		q += " AND pack_type=?"
		args = append(args, packType)
	}
	if status != "" {
		q += " AND status=?"
		args = append(args, status)
	}
	if lang != "" {
		q += " AND (src_lang=? OR tgt_lang=?)"
		args = append(args, lang, lang)
	}
	if limit <= 0 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	q += " ORDER BY id DESC LIMIT ? OFFSET ?"
	args = append(args, limit, offset)
	rows, err := db.Query(s.db, db.CurrentDialect(), q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*KBStagedEntry
	for rows.Next() {
		var e KBStagedEntry
		if err := rows.Scan(&e.ID, &e.TargetPackID, &e.PackType, &e.SourceID, &e.Tier, &e.Layer,
			&e.SrcLang, &e.SrcText, &e.TgtLang, &e.TgtText, &e.SourceURL, &e.SrcHash, &e.Status,
			&e.CreatedAt, &e.AppliedAt); err == nil {
			out = append(out, &e)
		}
	}
	return out, nil
}

// StagePhrase 写入一条待审安全句（hash 去重）。
// 参数：p=待审安全句字段；返回是否新增。
func (s *Store) StagePhrase(p *KBStagedPhrase) (bool, error) {
	if p.Phrase == "" || p.Lang == "" {
		return false, nil
	}
	if p.Kind == "" {
		p.Kind = "style"
	}
	if p.SrcHash == "" {
		p.SrcHash = scrapePhraseHash(p.Lang, p.Kind, p.Phrase, p.Replacement)
	}
	if p.Status == "" {
		p.Status = "pending"
	}
	if p.Tier <= 0 {
		p.Tier = 3
	}
	now := time.Now().Format(time.RFC3339)
	res, err := db.Exec(s.db, db.CurrentDialect(),
		"INSERT OR IGNORE INTO kb_staged_phrases (package_id, lang, phrase, kind, replacement, tier, src_hash, status, created_at, applied_at) VALUES (?,?,?,?,?,?,?,?,?,?)",
		p.PackageID, p.Lang, p.Phrase, p.Kind, p.Replacement, p.Tier, p.SrcHash, p.Status, now, "")
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// StagePhrasesBatch 批量写入待审安全句（单事务）；返回新增条数。
func (s *Store) StagePhrasesBatch(items []*KBStagedPhrase) (int, error) {
	if len(items) == 0 {
		return 0, nil
	}
	now := time.Now().Format(time.RFC3339)
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	added := 0
	for _, p := range items {
		if p.Phrase == "" || p.Lang == "" {
			continue
		}
		if p.Kind == "" {
			p.Kind = "style"
		}
		if p.SrcHash == "" {
			p.SrcHash = scrapePhraseHash(p.Lang, p.Kind, p.Phrase, p.Replacement)
		}
		if p.Status == "" {
			p.Status = "pending"
		}
		if p.Tier <= 0 {
			p.Tier = 3
		}
		res, err := db.Exec(tx, db.CurrentDialect(),
			"INSERT OR IGNORE INTO kb_staged_phrases (package_id, lang, phrase, kind, replacement, tier, src_hash, status, created_at, applied_at) VALUES (?,?,?,?,?,?,?,?,?,?)",
			p.PackageID, p.Lang, p.Phrase, p.Kind, p.Replacement, p.Tier, p.SrcHash, p.Status, now, "")
		if err != nil {
			return added, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			added++
		}
	}
	if err := tx.Commit(); err != nil {
		return added, err
	}
	return added, nil
}

// ListStagedPhrases 列出待审安全句（可按状态/语言筛选，分页）。
// 参数：status/lang 空=不过滤；limit<=0 取 200。
func (s *Store) ListStagedPhrases(status, lang string, limit, offset int) ([]*KBStagedPhrase, error) {
	q := "SELECT id, package_id, lang, phrase, kind, COALESCE(replacement,''), tier, src_hash, status, COALESCE(created_at,''), COALESCE(applied_at,'') FROM kb_staged_phrases WHERE 1=1"
	args := []interface{}{}
	if status != "" {
		q += " AND status=?"
		args = append(args, status)
	}
	if lang != "" {
		q += " AND lang=?"
		args = append(args, lang)
	}
	if limit <= 0 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	q += " ORDER BY id DESC LIMIT ? OFFSET ?"
	args = append(args, limit, offset)
	rows, err := db.Query(s.db, db.CurrentDialect(), q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*KBStagedPhrase
	for rows.Next() {
		var p KBStagedPhrase
		if err := rows.Scan(&p.ID, &p.PackageID, &p.Lang, &p.Phrase, &p.Kind, &p.Replacement,
			&p.Tier, &p.SrcHash, &p.Status, &p.CreatedAt, &p.AppliedAt); err == nil {
			out = append(out, &p)
		}
	}
	return out, nil
}

// SetStagedStatus 更新待审条目/安全句的审批状态（approve/reject），并记录 applied_at。
// 参数：kind=entries|phrases；ids=待审主键；status=approved/rejected；返回受影响行数。
func (s *Store) SetStagedStatus(kind string, ids []int64, status string) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	if status != "approved" && status != "rejected" {
		return 0, nil
	}
	table := "kb_staged_entries"
	if kind == "phrases" {
		table = "kb_staged_phrases"
	}
	now := time.Now().Format(time.RFC3339)
	ph := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := []interface{}{status, now}
	for _, id := range ids {
		args = append(args, id)
	}
	res, err := db.Exec(s.db, db.CurrentDialect(),
		"UPDATE "+table+" SET status=?, applied_at=? WHERE id IN ("+ph+") AND status='pending'", args...)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// GetStagedEntriesByIDs 按 ID 取待审条目（审批应用用，仅取 pending）。
func (s *Store) GetStagedEntriesByIDs(ids []int64) ([]*KBStagedEntry, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := []interface{}{}
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := db.Query(s.db, db.CurrentDialect(),
		"SELECT id, target_pack_id, pack_type, source_id, tier, layer, src_lang, src_text, tgt_lang, tgt_text, source_url, src_hash, status, COALESCE(created_at,''), COALESCE(applied_at,'') FROM kb_staged_entries WHERE id IN ("+ph+") AND status='pending'", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*KBStagedEntry
	for rows.Next() {
		var e KBStagedEntry
		if err := rows.Scan(&e.ID, &e.TargetPackID, &e.PackType, &e.SourceID, &e.Tier, &e.Layer,
			&e.SrcLang, &e.SrcText, &e.TgtLang, &e.TgtText, &e.SourceURL, &e.SrcHash, &e.Status,
			&e.CreatedAt, &e.AppliedAt); err == nil {
			out = append(out, &e)
		}
	}
	return out, nil
}

// GetStagedPhrasesByIDs 按 ID 取待审安全句（审批应用用，仅取 pending）。
func (s *Store) GetStagedPhrasesByIDs(ids []int64) ([]*KBStagedPhrase, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := []interface{}{}
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := db.Query(s.db, db.CurrentDialect(),
		"SELECT id, package_id, lang, phrase, kind, COALESCE(replacement,''), tier, src_hash, status, COALESCE(created_at,''), COALESCE(applied_at,'') FROM kb_staged_phrases WHERE id IN ("+ph+") AND status='pending'", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*KBStagedPhrase
	for rows.Next() {
		var p KBStagedPhrase
		if err := rows.Scan(&p.ID, &p.PackageID, &p.Lang, &p.Phrase, &p.Kind, &p.Replacement,
			&p.Tier, &p.SrcHash, &p.Status, &p.CreatedAt, &p.AppliedAt); err == nil {
			out = append(out, &p)
		}
	}
	return out, nil
}

// ScrapeStagedSummary 汇总待审池与数据源概览（管理面板）。
func (s *Store) ScrapeStagedSummary() *ScrapeStagedSummary {
	out := &ScrapeStagedSummary{}
	_ = db.QueryRow(s.db, db.CurrentDialect(), "SELECT COUNT(*) FROM kb_staged_entries WHERE status='pending'").Scan(&out.PendingEntries)
	_ = db.QueryRow(s.db, db.CurrentDialect(), "SELECT COUNT(*) FROM kb_staged_phrases WHERE status='pending'").Scan(&out.PendingPhrases)
	_ = db.QueryRow(s.db, db.CurrentDialect(), "SELECT COUNT(*) FROM kb_pack_sources").Scan(&out.SourcesTotal)
	_ = db.QueryRow(s.db, db.CurrentDialect(), "SELECT COUNT(*) FROM kb_pack_sources WHERE COALESCE(enabled,1)=1").Scan(&out.SourcesEnabled)
	out.LastDaily, _ = s.GetConfig("kb_scrape_daily_marker")
	return out
}

// ============ 断点续传（system_config 持久化） ============

// ScrapeCheckpointKey 生成某源在某日的断点游标键。
func ScrapeCheckpointKey(date string, sourceID int64) string {
	return "kb_scrape_checkpoint_" + date + "_" + strconv.FormatInt(sourceID, 10)
}

// SourceDoneKey 生成某源在某日是否完成的标记键。
func SourceDoneKey(date string, sourceID int64) string {
	return "kb_scrape_daily_source_done_" + date + "_" + strconv.FormatInt(sourceID, 10)
}

// GetScrapeCheckpoint 读取断点游标（不存在返回空串）。
// 参数：date=YYYY-MM-DD；sourceID=数据源 ID；返回游标。
func (s *Store) GetScrapeCheckpoint(date string, sourceID int64) string {
	v, _ := s.GetConfig(ScrapeCheckpointKey(date, sourceID))
	return v
}

// SetScrapeCheckpoint 写入断点游标（覆盖）。
func (s *Store) SetScrapeCheckpoint(date string, sourceID int64, cursor string) error {
	return s.SetConfig(ScrapeCheckpointKey(date, sourceID), cursor)
}

// SourceDone 判断某源某日是否已完成。
func (s *Store) SourceDone(date string, sourceID int64) bool {
	v, _ := s.GetConfig(SourceDoneKey(date, sourceID))
	return v == "1"
}

// MarkSourceDone 标记某源某日完成。
func (s *Store) MarkSourceDone(date string, sourceID int64) error {
	return s.SetConfig(SourceDoneKey(date, sourceID), "1")
}

// DailyMarker 返回最近完成采集日（空=尚未完成过）。
func (s *Store) DailyMarker() string {
	v, _ := s.GetConfig("kb_scrape_daily_marker")
	return v
}

// SetDailyMarker 标记当日全部启用源完成。
func (s *Store) SetDailyMarker(date string) error {
	return s.SetConfig("kb_scrape_daily_marker", date)
}

// ============ 配置读取（护栏/节奏） ============

// ConfigInt 读取整数配置（带默认值兜底，用于采集节奏/硬闸护栏等）。
// 参数：key=配置键；def=默认值；返回配置值。
func (s *Store) ConfigInt(key string, def int) int {
	v, err := s.GetConfig(key)
	if err != nil || strings.TrimSpace(v) == "" {
		return def
	}
	n, e := strconv.Atoi(strings.TrimSpace(v))
	if e != nil || n <= 0 {
		return def
	}
	return n
}

// ConfigFloat 读取浮点配置（带默认值兜底，用于采集节奏/硬闸护栏等）。
// 参数：key=配置键；def=默认值；返回配置值。
func (s *Store) ConfigFloat(key string, def float64) float64 {
	v, err := s.GetConfig(key)
	if err != nil || strings.TrimSpace(v) == "" {
		return def
	}
	f, e := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if e != nil || f <= 0 {
		return def
	}
	return f
}

// 确保 sql 引用（扫描辅助）。
var _ = sql.ErrNoRows
