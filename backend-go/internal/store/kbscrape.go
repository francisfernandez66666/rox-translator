// ============ kbscrape.go · 职责说明 ============
// store 包「行业包/语言文化包自动采集」数据访问层（2026-09-01 新功能）。
// 三张待审/源管理表：
//
//	kb_pack_sources   数据源（official_api / limited_web / llm_gen，可启停/限频/记状态）
//	kb_staged_entries 增量待审条目（术语/TM/碎片），超管审批通过后落 kb_entries + tm_segments
//	kb_staged_phrases 增量待审语言文化安全句，审批通过后落 kb_safety_phrases
//
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
	ID         int64  `json:"id"`          // 源主键 ID
	Kind       string `json:"kind"`        // official_api / limited_web / llm_gen
	Name       string `json:"name"`        // 源名称
	BaseURL    string `json:"base_url"`    // 入口 URL（llm_gen 可为空）
	Lang       string `json:"lang"`        // 目标语言（空=不限/全量）
	Industry   string `json:"industry"`    // 行业 code（空=不限/语言文化包）
	PackType   string `json:"pack_type"`   // industry / locale
	Enabled    int    `json:"enabled"`     // 1=启用参与采集 0=停用
	FreqHours  int    `json:"freq_hours"`  // 采集频次（小时）
	Tier       int    `json:"tier"`        // 数据可信度：1官方 / 2受限抓取 / 3LLM
	LastRunAt  string `json:"last_run_at"` // 最近采集时间
	LastStatus string `json:"last_status"` // 最近状态：ok / error:<msg>
	CreatedAt  string `json:"created_at"`
}

// KBStagedEntry 增量待审条目（术语/TM/碎片）
type KBStagedEntry struct {
	ID           int64  `json:"id"`             // 待审主键
	TenantID     int64  `json:"tenant_id"`      // 投稿归属租户（0=采集/平台宿主；>0=共享包用户投稿，审批通过后奖励该租户）
	TargetPackID int64  `json:"target_pack_id"` // 审批通过后落入的正式包 ID
	PackType     string `json:"pack_type"`      // industry / locale
	SourceID     int64  `json:"source_id"`      // 来源数据源 ID（0=手动投喂）
	Tier         int    `json:"tier"`           // 1官方 / 2受限抓取 / 3LLM
	Layer        int    `json:"layer"`          // 1术语/2TM/3安全句/4碎片
	SrcLang      string `json:"src_lang"`       // 源语言
	SrcText      string `json:"src_text"`       // 源文本
	TgtLang      string `json:"tgt_lang"`       // 目标语言
	TgtText      string `json:"tgt_text"`       // 目标译文
	SourceURL    string `json:"source_url"`     // 出处（抓取/API 溯源；LLM 为提示描述）
	SrcHash      string `json:"src_hash"`       // md5 去重键
	Status       string `json:"status"`         // pending / approved / rejected
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

// StagedMergedRow 待审池合并行（服务端分页返回）：entries/phrases 两表 UNION 统一结构。
// 展示与审批沿用前端既有合并行语义：kind 区分来源表，复合键 key=kind:id 防两表自增撞车。
type StagedMergedRow struct {
	Key        string `json:"key"` // kind:id（前端 rowKey/选中键）
	ID         int64  `json:"id"`
	Kind       string `json:"kind"` // entries / phrases
	PhraseKind string `json:"phrase_kind"` // phrases 的 style/forbidden/replace（entries 为空）
	PackType   string `json:"pack_type"`
	Tier       int    `json:"tier"`
	Industry   string `json:"industry"` // 行业 code（来自来源数据源；phrases 恒空）
	SrcLang    string `json:"src_lang"` // entries=src_lang；phrases=lang
	SrcText    string `json:"src_text"` // entries=src_text；phrases=phrase
	TgtLang    string `json:"tgt_lang"` // entries=tgt_lang；phrases=""
	TgtText    string `json:"tgt_text"` // entries=tgt_text；phrases=replacement
	SourceURL  string `json:"source_url"`
	Status     string `json:"status"`
}

// ScrapeStagedSummary 待审池汇总（管理面板一屏概览）
type ScrapeStagedSummary struct {
	PendingEntries int    `json:"pending_entries"` // 待审条目数
	PendingPhrases int    `json:"pending_phrases"` // 待审安全句数
	SourcesTotal   int    `json:"sources_total"`   // 数据源总数
	SourcesEnabled int    `json:"sources_enabled"` // 启用源数
	LastDaily      string `json:"last_daily"`      // 最近完成采集日（YYYY-MM-DD）
}

// scrapeEntryHash 待审条目去重键：md5(src_lang|src_text|tgt_lang|tgt_text)。
func scrapeEntryHash(srcLang, srcText, tgtLang, tgtText string) string {
	sum := md5.Sum([]byte(srcLang + "\x00" + srcText + "\x00" + tgtLang + "\x00" + tgtText))
	return hex.EncodeToString(sum[:])
}

// ComputeEntryHash 导出版待审条目去重键（供一次性工具/外部计算一致性）。
func ComputeEntryHash(srcLang, srcText, tgtLang, tgtText string) string {
	return scrapeEntryHash(srcLang, srcText, tgtLang, tgtText)
}

// DeleteStagedEntry 按 ID 删除一条待审条目（数据修复工具用）。
func (s *Store) DeleteStagedEntry(id int64) (bool, error) {
	res, err := db.Exec(s.db, db.CurrentDialect(), "DELETE FROM kb_staged_entries WHERE id=?", id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
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
		tenant_id INTEGER DEFAULT 0,
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
	// ★ 幂等补列：CREATE TABLE IF NOT EXISTS 不会给已存在的旧表补列。
	//   tenant_id 为「企业包双轨行业化」新加列（用户投稿归属租户，审批通过奖励）。
	//   早期创建的表缺此列会导致 INSERT 报 column not exist，此处对双方言统一补列。
	_ = db.EnsureColumns(s.db, d, "kb_staged_entries", map[string]string{
		"tenant_id": "INTEGER DEFAULT 0",
	})
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

// SeedDefaultScrapeSources 功能③：通用行业兜底包（general）默认采集源幂等种子。
// 通用行业包为注册行业缺选/错选时的回落目标，需要常见商务英语用法数据打底。
// 这里保证共享宿主始终存在一个启用的 general 行业包数据源（官方API/Wiktionary，
// 无 URL 时抓取器回退内置通用商务种子词）；仅当不存在任何 general 源时插入。
func (s *Store) SeedDefaultScrapeSources() {
	var cnt int
	_ = db.QueryRow(s.db, db.CurrentDialect(),
		"SELECT COUNT(*) FROM kb_pack_sources WHERE pack_type='industry' AND COALESCE(industry,'')='general'").Scan(&cnt)
	if cnt > 0 {
		return
	}
	_, _ = s.CreateScrapeSource(&KBScrapeSource{
		Kind: "official_api", Name: "通用行业·商务英语基础词表",
		Lang: "en", Industry: "general", PackType: "industry",
		Enabled: 1, FreqHours: 24, Tier: 2,
	})
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
		"INSERT OR IGNORE INTO kb_staged_entries (target_pack_id, pack_type, source_id, tenant_id, tier, layer, src_lang, src_text, tgt_lang, tgt_text, source_url, src_hash, status, created_at, applied_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)",
		e.TargetPackID, e.PackType, e.SourceID, e.TenantID, e.Tier, e.Layer, e.SrcLang, e.SrcText, e.TgtLang, e.TgtText, e.SourceURL, e.SrcHash, e.Status, now, "")
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
			"INSERT OR IGNORE INTO kb_staged_entries (target_pack_id, pack_type, source_id, tenant_id, tier, layer, src_lang, src_text, tgt_lang, tgt_text, source_url, src_hash, status, created_at, applied_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)",
			e.TargetPackID, e.PackType, e.SourceID, e.TenantID, e.Tier, e.Layer, e.SrcLang, e.SrcText, e.TgtLang, e.TgtText, e.SourceURL, e.SrcHash, e.Status, now, "")
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
	q := "SELECT id, target_pack_id, pack_type, source_id, tenant_id, tier, layer, src_lang, src_text, tgt_lang, tgt_text, source_url, src_hash, status, COALESCE(created_at,''), COALESCE(applied_at,'') FROM kb_staged_entries WHERE 1=1"
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
		if err := rows.Scan(&e.ID, &e.TargetPackID, &e.PackType, &e.SourceID, &e.TenantID, &e.Tier, &e.Layer,
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

// CountStagedEntries 统计待审条目数（与 ListStagedEntries 同口径筛选，供前端分页总数）。
// 参数：packType/status/lang=原筛选；industry=行业 code（空=不过滤；经来源数据源关联）。
func (s *Store) CountStagedEntries(packType, status, lang, industry string) (int64, error) {
	q := "SELECT COUNT(*) FROM kb_staged_entries WHERE 1=1"
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
	if industry != "" {
		// 行业过滤：经来源数据源（kb_pack_sources.industry）关联，源缺失则视为非该行业
		q += " AND EXISTS (SELECT 1 FROM kb_pack_sources ps WHERE ps.id=kb_staged_entries.source_id AND ps.industry=?)"
		args = append(args, industry)
	}
	var n int64
	err := db.QueryRow(s.db, db.CurrentDialect(), q, args...).Scan(&n)
	return n, err
}

// CountStagedPhrases 统计待审安全句数（与 ListStagedPhrases 同口径筛选，供前端分页总数）。
// 参数：status/lang=原筛选（安全句无行业概念，industry 筛选时由调用方整体排除）。
func (s *Store) CountStagedPhrases(status, lang string) (int64, error) {
	q := "SELECT COUNT(*) FROM kb_staged_phrases WHERE 1=1"
	args := []interface{}{}
	if status != "" {
		q += " AND status=?"
		args = append(args, status)
	}
	if lang != "" {
		q += " AND lang=?"
		args = append(args, lang)
	}
	var n int64
	err := db.QueryRow(s.db, db.CurrentDialect(), q, args...).Scan(&n)
	return n, err
}

// ListStagedMerged 待审池合并分页（entries+phrases UNION ALL 统一行集，服务端分页）。
// 与前端既有合并行语义对齐：kind 区分来源表，key=kind:id 复合键防两表自增撞车；
// pack_type 过滤对 phrases 无效（安全句恒为 locale 包，pack_type=industry 时安全句全排除）；
// industry 过滤经来源数据源关联（kb_pack_sources.industry），同时输出 industry 列供前端展示。
// ★ 列名限定：kb_pack_sources 与 kb_staged_entries 均含 pack_type/tier/id 列，JOIN 后须 e./ps. 全量限定避免歧义。
func (s *Store) ListStagedMerged(packType, status, lang, industry string, limit, offset int) ([]*StagedMergedRow, int64, error) {
	q := `SELECT key, id, kind, phrase_kind, pack_type, tier, industry, src_lang, src_text, tgt_lang, tgt_text, source_url, status FROM (
		SELECT ('entries:' || CAST(e.id AS TEXT)) AS key, e.id AS id, 'entries' AS kind, '' AS phrase_kind, e.pack_type AS pack_type, e.tier AS tier,
		       COALESCE(ps.industry,'') AS industry, e.src_lang AS src_lang, e.src_text AS src_text, e.tgt_lang AS tgt_lang, e.tgt_text AS tgt_text, e.source_url AS source_url, e.status AS status
		  FROM kb_staged_entries e LEFT JOIN kb_pack_sources ps ON ps.id=e.source_id WHERE 1=1`
	args := []interface{}{}
	if packType != "" {
		q += " AND e.pack_type=?"
		args = append(args, packType)
	}
	if status != "" {
		q += " AND e.status=?"
		args = append(args, status)
	}
	if lang != "" {
		q += " AND (e.src_lang=? OR e.tgt_lang=?)"
		args = append(args, lang, lang)
	}
	if industry != "" {
		q += " AND ps.industry=?"
		args = append(args, industry)
	}
	q += ` UNION ALL
		SELECT ('phrases:' || CAST(id AS TEXT)) AS key, id, 'phrases' AS kind, kind AS phrase_kind, 'locale' AS pack_type, tier,
		       '' AS industry, lang AS src_lang, phrase AS src_text, '' AS tgt_lang, replacement AS tgt_text,
		       '语言文化规范' AS source_url, status
		  FROM kb_staged_phrases WHERE 1=1`
	if status != "" {
		q += " AND status=?"
		args = append(args, status)
	}
	if lang != "" {
		q += " AND lang=?"
		args = append(args, lang)
	}
	q += `) AS staged WHERE 1=1`
	if packType == "industry" {
		q += " AND kind='entries'" // 行业筛选下安全句（恒 locale）整体排除，与前端语义一致
	}
	if industry != "" {
		q += " AND kind='entries'" // 行业筛选下安全句（无行业概念）整体排除
	}
	if limit <= 0 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	// 精确总数：条目 + 安全句（pack_type/industry 过滤仅作用于条目）
	total, _ := s.CountStagedEntries(packType, status, lang, industry)
	if packType != "industry" && industry == "" {
		tp, _ := s.CountStagedPhrases(status, lang)
		total += tp
	}
	q += " ORDER BY (CASE kind WHEN 'entries' THEN 0 ELSE 1 END), id DESC LIMIT ? OFFSET ?"
	args = append(args, limit, offset)
	rows, err := db.Query(s.db, db.CurrentDialect(), q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []*StagedMergedRow
	for rows.Next() {
		var r StagedMergedRow
		if err := rows.Scan(&r.Key, &r.ID, &r.Kind, &r.PhraseKind, &r.PackType, &r.Tier, &r.Industry,
			&r.SrcLang, &r.SrcText, &r.TgtLang, &r.TgtText, &r.SourceURL, &r.Status); err == nil {
			out = append(out, &r)
		}
	}
	return out, total, nil
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

// SetStagedStatus 更新待审条目/安全句的审核状态，并记录/清空 applied_at。
// 参数：kind=entries|phrases；ids=待审主键；status=approved/rejected/pending。
//   - approved/rejected：仅 pending 可流转，记录 applied_at（通过/驳回）。
//   - pending：还原为待审——从 approved/rejected 拉回，清空 applied_at（含内容已编辑的场景）。
// 返回受影响行数。
func (s *Store) SetStagedStatus(kind string, ids []int64, status string) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	if status != "approved" && status != "rejected" && status != "pending" {
		return 0, nil
	}
	table := "kb_staged_entries"
	if kind == "phrases" {
		table = "kb_staged_phrases"
	}
	now := time.Now().Format(time.RFC3339)
	appliedAt := now
	where := "status='pending'"
	if status == "pending" {
		appliedAt = ""
		where = "status IN ('approved','rejected')"
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := []interface{}{status, appliedAt}
	for _, id := range ids {
		args = append(args, id)
	}
	res, err := db.Exec(s.db, db.CurrentDialect(),
		"UPDATE "+table+" SET status=?, applied_at=? WHERE id IN ("+ph+") AND "+where, args...)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// GetStagedEntriesAllByIDs 按 ID 取待审条目（不过滤状态；还原/编辑用）。
func (s *Store) GetStagedEntriesAllByIDs(ids []int64) ([]*KBStagedEntry, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := []interface{}{}
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := db.Query(s.db, db.CurrentDialect(),
		"SELECT id, target_pack_id, pack_type, source_id, tenant_id, tier, layer, src_lang, src_text, tgt_lang, tgt_text, source_url, src_hash, status, COALESCE(created_at,''), COALESCE(applied_at,'') FROM kb_staged_entries WHERE id IN ("+ph+")", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*KBStagedEntry
	for rows.Next() {
		var e KBStagedEntry
		if err := rows.Scan(&e.ID, &e.TargetPackID, &e.PackType, &e.SourceID, &e.TenantID, &e.Tier, &e.Layer,
			&e.SrcLang, &e.SrcText, &e.TgtLang, &e.TgtText, &e.SourceURL, &e.SrcHash, &e.Status,
			&e.CreatedAt, &e.AppliedAt); err == nil {
			out = append(out, &e)
		}
	}
	return out, nil
}

// GetStagedPhrasesAllByIDs 按 ID 取待审安全句（不过滤状态；还原/编辑用）。
func (s *Store) GetStagedPhrasesAllByIDs(ids []int64) ([]*KBStagedPhrase, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := []interface{}{}
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := db.Query(s.db, db.CurrentDialect(),
		"SELECT id, package_id, lang, phrase, kind, COALESCE(replacement,''), tier, src_hash, status, COALESCE(created_at,''), COALESCE(applied_at,'') FROM kb_staged_phrases WHERE id IN ("+ph+")", args...)
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

// UpdateStagedEntryContent 编辑待审条目内容（还原为待审前修改译文/源文，重算去重 hash）。
// 返回是否成功（hash 与既有行冲突时返回 false，避免唯一约束冲突）。
func (s *Store) UpdateStagedEntryContent(id int64, srcText, tgtText string) (bool, error) {
	if srcText == "" || tgtText == "" {
		return false, nil
	}
	var srcLang, tgtLang string
	if err := db.QueryRow(s.db, db.CurrentDialect(), "SELECT src_lang, tgt_lang FROM kb_staged_entries WHERE id=?", id).Scan(&srcLang, &tgtLang); err != nil {
		return false, err
	}
	newHash := scrapeEntryHash(srcLang, srcText, tgtLang, tgtText)
	_, err := db.Exec(s.db, db.CurrentDialect(),
		"UPDATE kb_staged_entries SET src_text=?, tgt_text=?, src_hash=? WHERE id=?", srcText, tgtText, newHash, id)
	if err != nil {
		return false, nil // 唯一约束冲突（与其它待审行重复）→ 忽略该条编辑
	}
	return true, nil
}

// UpdateStagedPhraseContent 编辑待审安全句内容（重算去重 hash）。
func (s *Store) UpdateStagedPhraseContent(id int64, phrase, replacement string) (bool, error) {
	if phrase == "" {
		return false, nil
	}
	var lang, kind string
	if err := db.QueryRow(s.db, db.CurrentDialect(), "SELECT lang, COALESCE(kind,'style') FROM kb_staged_phrases WHERE id=?", id).Scan(&lang, &kind); err != nil {
		return false, err
	}
	newHash := scrapePhraseHash(lang, kind, phrase, replacement)
	_, err := db.Exec(s.db, db.CurrentDialect(),
		"UPDATE kb_staged_phrases SET phrase=?, replacement=?, src_hash=? WHERE id=?", phrase, replacement, newHash, id)
	if err != nil {
		return false, nil
	}
	return true, nil
}

// AutoApproveEntry 自动清洗+审批条目：直接落正式库（SaveEntry）+ 记录一条已通过待审留痕。
// 流程改造（2026-09-02）：采集内容不再滞留待审池等人工审批，改为采集即自动清洗（源语言
// 已在 crawler 层纠正）+ 直接嵌入正式库 + 在待审表留一条 approved 记录供人工事后查看/驳回/改正。
// ★ 去重 hash 始终按当前字段重算（不接受外部传入的旧 hash）：调用方可能在冲洗时已更正
//   src_lang（UpdateStagedEntrySrcLang），若沿用旧 hash 会在唯一索引上插入重复行（2026-09-03 修复）。
// 返回错误（SaveEntry 失败时返回，调用方据此跳过；留痕失败不阻断落库）。
func (s *Store) AutoApproveEntry(tid int64, e *KBStagedEntry) error {
	if e.TargetPackID <= 0 || e.SrcText == "" || e.TgtText == "" || e.TgtLang == "" {
		return nil
	}
	e.SrcHash = scrapeEntryHash(e.SrcLang, e.SrcText, e.TgtLang, e.TgtText)
	module := "imported"
	if e.SourceID > 0 {
		module = "scrape:" + strconv.FormatInt(e.SourceID, 10)
	}
	if _, err := s.SaveEntry(tid, e.TargetPackID, e.Layer, e.SrcLang, e.SrcText, e.TgtLang, e.TgtText, module); err != nil {
		return err
	}
	now := time.Now().Format(time.RFC3339)
	// 留痕：hash 唯一键冲突时把既有行（可能是历史 pending）提升为 approved，
	// 保证同内容跨源/跨轮只留一条已通过记录，且能承接人工事后审批视图。
	_, _ = db.Exec(s.db, db.CurrentDialect(),
		"INSERT INTO kb_staged_entries (target_pack_id, pack_type, source_id, tenant_id, tier, layer, src_lang, src_text, tgt_lang, tgt_text, source_url, src_hash, status, created_at, applied_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,'approved',?,?) "+
			"ON CONFLICT(src_hash) DO UPDATE SET status='approved', applied_at=excluded.applied_at, src_lang=excluded.src_lang, src_text=excluded.src_text, tgt_text=excluded.tgt_text, source_id=excluded.source_id",
		e.TargetPackID, e.PackType, e.SourceID, e.TenantID, e.Tier, e.Layer, e.SrcLang, e.SrcText, e.TgtLang, e.TgtText, e.SourceURL, e.SrcHash, now, now)
	return nil
}

// AutoApprovePhrase 自动清洗+审批安全句：直接落正式库（SaveSafetyPhraseEx）+ 记录已通过留痕。
// 与 AutoApproveEntry 同语义：采集即通过，人工事后查看/驳回/改正。
// 去重 hash 始终按当前字段重算（不继承外部旧 hash，避免重复行）。
func (s *Store) AutoApprovePhrase(tid int64, p *KBStagedPhrase) error {
	if p.PackageID <= 0 || p.Phrase == "" || p.Lang == "" {
		return nil
	}
	p.SrcHash = scrapePhraseHash(p.Lang, p.Kind, p.Phrase, p.Replacement)
	if _, err := s.SaveSafetyPhraseEx(tid, p.PackageID, p.Lang, p.Phrase, p.Kind, p.Replacement); err != nil {
		return err
	}
	now := time.Now().Format(time.RFC3339)
	_, _ = db.Exec(s.db, db.CurrentDialect(),
		"INSERT INTO kb_staged_phrases (package_id, lang, phrase, kind, replacement, tier, src_hash, status, created_at, applied_at) VALUES (?,?,?,?,?,?,?,'approved',?,?) "+
			"ON CONFLICT(src_hash) DO UPDATE SET status='approved', applied_at=excluded.applied_at",
		p.PackageID, p.Lang, p.Phrase, p.Kind, p.Replacement, p.Tier, p.SrcHash, now, now)
	return nil
}

// UpdateStagedEntrySrcLang 更正待审条目的源语言并重算去重 hash（清洗历史误标 zh 数据用）。
// 返回是否成功（hash 与既有行冲突时返回 false，避免唯一约束冲突）。
func (s *Store) UpdateStagedEntrySrcLang(id int64, srcLang string) (bool, error) {
	if srcLang == "" {
		return false, nil
	}
	var srcText, tgtLang, tgtText string
	if err := db.QueryRow(s.db, db.CurrentDialect(),
		"SELECT src_text, tgt_lang, tgt_text FROM kb_staged_entries WHERE id=?", id).Scan(&srcText, &tgtLang, &tgtText); err != nil {
		return false, err
	}
	newHash := scrapeEntryHash(srcLang, srcText, tgtLang, tgtText)
	_, err := db.Exec(s.db, db.CurrentDialect(),
		"UPDATE kb_staged_entries SET src_lang=?, src_hash=? WHERE id=?", srcLang, newHash, id)
	if err != nil {
		return false, nil // 唯一约束冲突 → 忽略
	}
	return true, nil
}

// ListStagedEntriesAll 列出全部待审条目（含非 pending；一键清洗/回填/兜底用）。
// 按 id 升序分批，支持大表游标扫描。
func (s *Store) ListStagedEntriesAll(limit, offset int) ([]*KBStagedEntry, error) {
	return s.listStagedEntriesAny("", limit, offset)
}

// listStagedEntriesAny 按状态与分页列待审条目（status 空=全部）。
func (s *Store) listStagedEntriesAny(status string, limit, offset int) ([]*KBStagedEntry, error) {
	q := "SELECT id, target_pack_id, pack_type, source_id, tenant_id, tier, layer, src_lang, src_text, tgt_lang, tgt_text, source_url, src_hash, status, COALESCE(created_at,''), COALESCE(applied_at,'') FROM kb_staged_entries"
	args := []interface{}{}
	if status != "" {
		q += " WHERE status=?"
		args = append(args, status)
	}
	if limit <= 0 {
		limit = 500
	}
	if offset < 0 {
		offset = 0
	}
	q += " ORDER BY id LIMIT ? OFFSET ?"
	args = append(args, limit, offset)
	rows, err := db.Query(s.db, db.CurrentDialect(), q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*KBStagedEntry
	for rows.Next() {
		var e KBStagedEntry
		if err := rows.Scan(&e.ID, &e.TargetPackID, &e.PackType, &e.SourceID, &e.TenantID, &e.Tier, &e.Layer,
			&e.SrcLang, &e.SrcText, &e.TgtLang, &e.TgtText, &e.SourceURL, &e.SrcHash, &e.Status,
			&e.CreatedAt, &e.AppliedAt); err == nil {
			out = append(out, &e)
		}
	}
	return out, nil
}

// ListStagedPhrasesAll 列出全部待审安全句（含非 pending；清洗/回填用）。
func (s *Store) ListStagedPhrasesAll(limit, offset int) ([]*KBStagedPhrase, error) {
	if limit <= 0 {
		limit = 500
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := db.Query(s.db, db.CurrentDialect(),
		"SELECT id, package_id, lang, phrase, kind, COALESCE(replacement,''), tier, src_hash, status, COALESCE(created_at,''), COALESCE(applied_at,'') FROM kb_staged_phrases ORDER BY id LIMIT ? OFFSET ?", limit, offset)
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

// DeleteAppliedEntry 撤销已通过待审条目时，从正式库删除对应条目并清空检索层译文。
// 按 SaveEntry 的判重键（tenant_id,package_id,source_lang,source_text,target_lang）精确匹配。
func (s *Store) DeleteAppliedEntry(tid, pkgID int64, srcLang, srcText, tgtLang string) error {
	var id int64
	err := db.QueryRow(s.db, db.CurrentDialect(),
		"SELECT id FROM kb_entries WHERE tenant_id=? AND package_id=? AND source_lang=? AND source_text=? AND target_lang=?",
		tid, pkgID, srcLang, srcText, tgtLang).Scan(&id)
	if err == nil {
		if derr := s.DeleteEntry(id, tid); derr != nil {
			return derr
		}
	}
	// 检索层（tm_segments）：清空该语言列（tgtLang 已由入库白名单校验，此处防注入复检）
	if isValidLangColumn(tgtLang) {
		sum := md5.Sum([]byte(srcText))
		hash := hex.EncodeToString(sum[:])
		_, _ = db.Exec(s.db, db.CurrentDialect(),
			"UPDATE tm_segments SET "+tgtLang+"='' WHERE zh_hash=? AND tenant_id=? AND pack_id=?",
			hash, tid, pkgID)
	}
	return nil
}

// DeleteAppliedPhrase 撤销已通过待审安全句时，从正式库删除对应安全句。
func (s *Store) DeleteAppliedPhrase(tid, pkgID int64, lang, phrase, replacement string) error {
	_, err := db.Exec(s.db, db.CurrentDialect(),
		"DELETE FROM kb_safety_phrases WHERE tenant_id=? AND package_id=? AND lang=? AND phrase=? AND COALESCE(replacement,'')=?",
		tid, pkgID, lang, phrase, replacement)
	return err
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
		"SELECT id, target_pack_id, pack_type, source_id, tenant_id, tier, layer, src_lang, src_text, tgt_lang, tgt_text, source_url, src_hash, status, COALESCE(created_at,''), COALESCE(applied_at,'') FROM kb_staged_entries WHERE id IN ("+ph+") AND status='pending'", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*KBStagedEntry
	for rows.Next() {
		var e KBStagedEntry
		if err := rows.Scan(&e.ID, &e.TargetPackID, &e.PackType, &e.SourceID, &e.TenantID, &e.Tier, &e.Layer,
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
