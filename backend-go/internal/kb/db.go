// ============ 本文件职责中文说明 ============
// 知识库（tm_segments 表）数据库访问层：打开 SQLite 缓存库、翻译记忆条目的
// 精确/模糊/语义检索辅助查询、按租户隔离读写（upsert）、多租户迁移
// （tenant_id 列与复合唯一索引重建）、知识库统计与全量遍历。
// 语言列固定 16 列（en/ru/ar/.../sv），与 config.AllLangs 一一对应。
// =============================================
package kb

import (
	"crypto/md5"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"translator/internal/config"
)

// AllLangs DB 语言列（16 列）
var AllLangs = config.AllLangs

// KBDatabase 知识库封装
type KBDatabase struct {
	db     *sql.DB // 底层 SQLite 连接
	dbPath string  // 数据库文件路径（用于日志/重建）
}

// Row 翻译记忆条目
type Row struct {
	ID       int64             // 条目主键 ID
	Zh       string            // 中文原文
	Module   string            // 来源模块（如 manual/approved）
	TenantID int64             // 所属租户 ID
	PackID   int64             // 归属知识库包 ID（0=历史无归属数据）
	Langs    map[string]string // 语言代码 → 译文（缺失语言为空串）
}

// cjkRe 匹配 CJK 统一表意文字（汉字）的正则
var cjkRe = regexp.MustCompile("[\u4e00-\u9fff\u3400-\u4dbf]")

// ExtractCJK 抽出全部汉字（按原顺序拼接）。
// 参数：text=输入文本；返回仅含汉字的字符串。
func ExtractCJK(text string) string {
	return strings.Join(cjkRe.FindAllString(text, -1), "")
}

// MD5Hex 计算字符串的 MD5 十六进制（用于 zh_hash 唯一键）。
// 参数：s=输入字符串；返回 32 位 MD5 十六进制串。
func MD5Hex(s string) string {
	h := md5.Sum([]byte(s))
	return fmt.Sprintf("%x", h)
}

// Open 打开数据库并确保表存在。
// 参数：dbPath=SQLite 文件路径；返回知识库对象（含已建立的连接与表结构）。
func Open(dbPath string) (*KBDatabase, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}
	if err := ensureTables(db); err != nil {
		return nil, err
	}
	return &KBDatabase{db: db, dbPath: dbPath}, nil
}

// EnsureTenantMigration 确保 tm_segments 有 tenant_id 列，并把既有数据归入默认租户 rox（id=1）。
// 实现：先查后加列；历史数据归入租户 1；重建 zh_hash 唯一索引为 (zh_hash, tenant_id) 复合唯一。
func (k *KBDatabase) EnsureTenantMigration() error {
	// 加列（SQLite: 无 IF NOT EXISTS 加列，先查后加）
	cols, err := k.db.Query("PRAGMA table_info(tm_segments)")
	if err != nil {
		return err
	}
	defer cols.Close()
	hasTenant := false
	// 遍历现有列，判断是否已有 tenant_id
	for cols.Next() {
		var cid, notnull, pk int
		var name, ctype string
		var dflt interface{}
		if err := cols.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			continue
		}
		if name == "tenant_id" {
			hasTenant = true
		}
	}
	if !hasTenant {
		// 老库无 tenant_id 列 → 补列
		if _, err := k.db.Exec("ALTER TABLE tm_segments ADD COLUMN tenant_id INTEGER DEFAULT 1"); err != nil {
			return err
		}
	}
	// 历史数据统一归入租户 1（rox）
	if _, err := k.db.Exec("UPDATE tm_segments SET tenant_id=1 WHERE tenant_id IS NULL OR tenant_id=0"); err != nil {
		return err
	}
	// 将 zh_hash 的唯一约束重建为 (zh_hash, tenant_id) 复合唯一，
	// 保证不同租户可存相同中文句。重建会导致 zh_hash 唯一索引被替换。
	if err := k.rebuildUniqueIndex(); err != nil {
		return err
	}
	return nil
}

// rebuildUniqueIndex 检查并重建复合唯一约束（zh_hash 单列唯一 → (zh_hash, tenant_id)）。
// 实现：检查 tm_segments 的唯一索引是否仍为单列 zh_hash；若是则走重建表方案。
func (k *KBDatabase) rebuildUniqueIndex() error {
	idxRows, err := k.db.Query("PRAGMA index_list(tm_segments)")
	if err != nil {
		return err
	}
	defer idxRows.Close()
	hasComposite := false
	var idxNames []string
	// 遍历索引列表，识别隐式唯一索引 sqlite_autoindex_tm_segments_1
	for idxRows.Next() {
		var seq int
		var name, origin, partial string
		var unique int
		if err := idxRows.Scan(&seq, &name, &unique, &origin, &partial); err != nil {
			continue
		}
		idxNames = append(idxNames, name)
		if name == "sqlite_autoindex_tm_segments_1" {
			// 单列唯一索引，检查其列
			cols, err := k.db.Query("PRAGMA index_info(" + name + ")")
			if err == nil {
				var cname string
				var cseq, cid int
				for cols.Next() {
					if err := cols.Scan(&cseq, &cid, &cname); err == nil {
						if cname == "tenant_id" {
							hasComposite = true // 已是复合索引
						}
					}
				}
				cols.Close()
			}
		}
	}
	_ = hasComposite
	_ = idxNames
	// SQLite 无法直接改 UNIQUE 约束，采用"重建表"方案：
	// 若 autoindex 仍是单列 (zh_hash)，则重建。
	isSingle, err := k.isSingleUniqueOnZhHash()
	if err != nil {
		return err
	}
	if !isSingle {
		return nil // 已是复合唯一，无需重建
	}
	return k.rebuildTableWithCompositeUnique()
}

// isSingleUniqueOnZhHash 判断 tm_segments 的隐式唯一索引是否只含 zh_hash 一列。
// 返回：true 表示需要重建（单列 zh_hash 唯一）。
func (k *KBDatabase) isSingleUniqueOnZhHash() (bool, error) {
	rows, err := k.db.Query("PRAGMA index_list(tm_segments)")
	if err != nil {
		return false, err
	}
	defer rows.Close()
	// 遍历全部唯一且 origin='u'（隐式）的索引
	for rows.Next() {
		var seq int
		var name, origin, partial string
		var unique int
		if err := rows.Scan(&seq, &name, &unique, &origin, &partial); err != nil {
			continue
		}
		if unique != 1 || origin != "u" {
			continue
		}
		cols, err := k.db.Query("PRAGMA index_info(" + name + ")")
		if err != nil {
			continue
		}
		var colNames []string
		for cols.Next() {
			var cseq, cid int
			var cname string
			if err := cols.Scan(&cseq, &cid, &cname); err == nil {
				colNames = append(colNames, cname)
			}
		}
		cols.Close()
		// 单列且为 zh_hash → 需要重建
		if len(colNames) == 1 && colNames[0] == "zh_hash" {
			return true, nil
		}
	}
	return false, nil
}

// rebuildTableWithCompositeUnique 重建表：把 zh_hash UNIQUE 换成 (zh_hash, tenant_id) UNIQUE。
// 实现：新建临时表→拷贝数据→删旧表→改名，全程事务包裹保证一致性。
func (k *KBDatabase) rebuildTableWithCompositeUnique() error {
	// 用事务包裹，避免中途失败导致数据不一致
	tx, err := k.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	cols := "id, zh_hash, zh, zh_short, module, tenant_id, en, ru, ar, es, pt, fr, kk, de, zh_hant, ms, id_lang, th, tr, it, pl, sv, updated_at"
	// 建新表：UNIQUE(zh_hash, tenant_id) 复合唯一
	if _, err := tx.Exec(`CREATE TABLE tm_segments_new (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		"zh_hash" TEXT,
		"zh" TEXT,
		"zh_short" TEXT,
		"module" TEXT,
		"tenant_id" INTEGER DEFAULT 1,
		"en" TEXT, "ru" TEXT, "ar" TEXT, "es" TEXT, "pt" TEXT, "fr" TEXT,
		"kk" TEXT, "de" TEXT, "zh_hant" TEXT,
		"ms" TEXT, "id_lang" TEXT, "th" TEXT, "tr" TEXT, "it" TEXT, "pl" TEXT, "sv" TEXT,
		"updated_at" TEXT,
		UNIQUE(zh_hash, tenant_id)
	)`); err != nil {
		return err
	}
	// 拷贝旧数据到新表
	if _, err := tx.Exec("INSERT INTO tm_segments_new (" + cols + ") SELECT " + cols + " FROM tm_segments"); err != nil {
		return err
	}
	// 删旧表并改名新表
	if _, err := tx.Exec("DROP TABLE tm_segments"); err != nil {
		return err
	}
	if _, err := tx.Exec("ALTER TABLE tm_segments_new RENAME TO tm_segments"); err != nil {
		return err
	}
	return tx.Commit()
}

// ensureTables 幂等创建 tm_segments 表（zh_hash 单列唯一，建表阶段）。
func ensureTables(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS tm_segments (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		"zh_hash" TEXT UNIQUE,
		"zh" TEXT,
		"zh_short" TEXT,
		"module" TEXT,
		"tenant_id" INTEGER DEFAULT 1,
		"en" TEXT, "ru" TEXT, "ar" TEXT, "es" TEXT, "pt" TEXT, "fr" TEXT,
		"kk" TEXT, "de" TEXT, "zh_hant" TEXT,
		"ms" TEXT, "id_lang" TEXT, "th" TEXT, "tr" TEXT, "it" TEXT, "pl" TEXT, "sv" TEXT,
		"updated_at" TEXT
	)`)
	return err
}

// Close 关闭数据库连接。
func (k *KBDatabase) Close() error { return k.db.Close() }

// RawDB 返回底层 *sql.DB（供租户存储等共享同一连接）。
func (k *KBDatabase) RawDB() *sql.DB { return k.db }

// langCols 16 个语言列名（逗号分隔，供 SELECT 拼接）。
const langCols = "en, ru, ar, es, pt, fr, kk, de, zh_hant, ms, id_lang, th, tr, it, pl, sv"

// FetchRow 按 id 查询整行（跨租户，用于语义检索后回查）。
// 参数：id=条目主键 ID；返回该行记录（含全部语言译文）。
func (k *KBDatabase) FetchRow(id int64) (*Row, error) {
	row := k.db.QueryRow(fmt.Sprintf(
		"SELECT id, zh, COALESCE(module,''), COALESCE(tenant_id,1), %s FROM tm_segments WHERE id=?",
		langCols), id)
	return scanRow(row)
}

// scanRow 把查询行扫描成 Row 结构体（Nullable 语言列转字符串 map）。
// 参数：row=待扫描的单行；返回 Row 记录。
func scanRow(row *sql.Row) (*Row, error) {
	var r Row
	var zh, module string
	var en, ru, ar, es, pt, fr, kk, de, zhHant, ms, idLang, th, tr, it, pl, sv sql.NullString
	err := row.Scan(&r.ID, &zh, &module, &r.TenantID, &en, &ru, &ar, &es, &pt, &fr, &kk, &de, &zhHant,
		&ms, &idLang, &th, &tr, &it, &pl, &sv)
	if err != nil {
		return nil, err
	}
	r.Zh = zh
	r.Module = module
	// NULL 语言列统一转空串
	r.Langs = map[string]string{
		"en": en.String, "ru": ru.String, "ar": ar.String, "es": es.String,
		"pt": pt.String, "fr": fr.String, "kk": kk.String, "de": de.String,
		"zh_hant": zhHant.String, "ms": ms.String, "id_lang": idLang.String,
		"th": th.String, "tr": tr.String, "it": it.String, "pl": pl.String, "sv": sv.String,
	}
	return &r, nil
}

// FetchRowTenant 按 id 查询整行（并校验属于指定租户）。
// 参数：id=条目主键 ID，tenantID=租户 ID；返回该行记录。
// FetchRowTenant 按 id 查询整行（允许本租户或共享宿主租户1 的行业/语言文化包条目）。
func (k *KBDatabase) FetchRowTenant(id, tenantID int64) (*Row, error) {
	row := k.db.QueryRow(fmt.Sprintf(
		"SELECT tm.id, tm.zh, COALESCE(tm.module,''), COALESCE(tm.tenant_id,1), %s FROM tm_segments tm "+
			"LEFT JOIN kb_packages pkg ON pkg.id = tm.pack_id "+
			"WHERE tm.id=? AND (tm.tenant_id=? OR (tm.tenant_id=1 AND tm.priority>=2 AND pkg.pack_type IN ('industry','locale')))",
		langCols), id, tenantID)
	return scanRow(row)
}

// FindExact 精确命中：按中文原文精确查询（指定租户）。
// 参数：zh=中文原文，tenantID=租户 ID；返回命中的行。
// FindExact 精确命中（应用知识库优先级链：部门包0 > 组织包1 > 行业包2 > 语言文化包3）。
// 查询范围 = 本租户 + 租户1（行业包/语言文化包的共享宿主），按 priority 升序取最优命中。
// 共享过滤子句：租户1 行仅限 语言文化包(全放行) + 本租户注册行业的行业包（防组织包泄漏）
const sharedFilterSQL = "OR (tm.tenant_id=1 AND tm.priority>=2 AND EXISTS(SELECT 1 FROM kb_packages pkg WHERE pkg.id=tm.pack_id AND (pkg.pack_type='locale' OR (pkg.pack_type='industry' AND pkg.code=(SELECT industry FROM tenants WHERE id=?)))))"

func (k *KBDatabase) FindExact(zh string, tenantID int64) (*Row, error) {
	row := k.db.QueryRow(fmt.Sprintf(
		"SELECT tm.id, tm.zh, COALESCE(tm.module,''), COALESCE(tm.tenant_id,1), "+langCols+" FROM tm_segments tm WHERE tm.zh=? AND (tm.tenant_id=? "+sharedFilterSQL+") ORDER BY tm.priority ASC, tm.id ASC LIMIT 1"),
		zh, tenantID, tenantID)
	return scanRow(row)
}

// FuzzyHits 模糊子串命中（LIKE 子串匹配，长度差 ≤30，按长度差升序，LIMIT n；指定租户）。
// 参数：zhShort=查询子串，limit=返回条数上限，tenantID=租户 ID。
// 返回：命中的行列表（按原文长度差由近到远排序）。
func (k *KBDatabase) FuzzyHits(zhShort string, limit int, tenantID int64) ([]*Row, error) {
	// 优先级链排序：部门包 > 组织包 > 行业包 > 语言文化包（共享宿主=租户1）
	rows, err := k.db.Query("SELECT tm.id, tm.zh FROM tm_segments tm WHERE tm.zh LIKE ? AND (tm.tenant_id=? " +
		sharedFilterSQL + ") ORDER BY tm.priority ASC, tm.id ASC LIMIT ?", "%"+zhShort+"%", tenantID, tenantID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type cand struct {
		id   int64
		zh   string
		diff int // 长度差（rune 数）
	}
	var cands []cand
	for rows.Next() {
		var id int64
		var zh string
		if err := rows.Scan(&id, &zh); err != nil {
			continue
		}
		diff := len([]rune(zh)) - len([]rune(zhShort)) // 计算长度差
		if diff >= 0 && diff <= 30 {
			cands = append(cands, cand{id: id, zh: zh, diff: diff}) // 只保留长度差在 30 以内
		}
	}
	// 按长度差升序（冒泡排序，数据量小）
	for i := 0; i < len(cands); i++ {
		for j := i + 1; j < len(cands); j++ {
			if cands[j].diff < cands[i].diff {
				cands[i], cands[j] = cands[j], cands[i]
			}
		}
	}
	if limit > len(cands) {
		limit = len(cands) // 防越界
	}
	var out []*Row
	for _, c := range cands[:limit] {
		r, err := k.FetchRow(c.id)
		if err == nil {
			out = append(out, r) // 回查完整行
		}
	}
	return out, nil
}

// GetAllRows 遍历指定租户所有条目的 id + zh + module + tenant（构建 CJK 缓存用）。
// 参数：tenantID=租户 ID；返回精简行列表。
func (k *KBDatabase) GetAllRows(tenantID int64) ([]Row, error) {
	rows, err := k.db.Query("SELECT id, zh, COALESCE(module,''), COALESCE(tenant_id,1) FROM tm_segments WHERE tenant_id=?", tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Row
	for rows.Next() {
		var r Row
		if err := rows.Scan(&r.ID, &r.Zh, &r.Module, &r.TenantID); err != nil {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

// AllRowLangs 返回指定租户每行已有的语言集合（id → 非空语言代码集合）。
// 用于语义检索按目标语言过滤，避免检索不含目标语言的行。
// 参数：tenantID=租户 ID；返回 map[行ID]→语言集合。
func (k *KBDatabase) AllRowLangs(tenantID int64) (map[int64]map[string]bool, error) {
	rows, err := k.db.Query(fmt.Sprintf(
		"SELECT id, %s FROM tm_segments WHERE tenant_id=?", langCols), tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[int64]map[string]bool)
	cols := make([]sql.NullString, len(AllLangs))
	scanArgs := make([]interface{}, 0, len(AllLangs)+1)
	var id int64
	scanArgs = append(scanArgs, &id)
	for i := range cols {
		scanArgs = append(scanArgs, &cols[i])
	}
	for rows.Next() {
		if err := rows.Scan(scanArgs...); err != nil {
			continue
		}
		set := map[string]bool{}
		// 只统计非空的译文语言列
		for i, lc := range AllLangs {
			if cols[i].Valid && strings.TrimSpace(cols[i].String) != "" {
				set[lc] = true
			}
		}
		if len(set) > 0 {
			out[id] = set // 该行存在至少一种语言
		}
	}
	return out, nil
}

// SaveBack upsert 写回：INSERT ... ON CONFLICT(zh_hash, tenant_id) DO UPDATE（翻译记忆回写）。
// 参数：zh=中文原文，translations=语言代码→译文映射，module=来源模块，tenantID=租户 ID。
// 返回：条目 ID 或错误。
func (k *KBDatabase) SaveBack(zh string, translations map[string]string, module string, tenantID int64) (int64, error) {
	hash := MD5Hex(zh) // 计算唯一哈希键
	cols := []string{"zh_hash", "zh", "tenant_id"}
	placeholders := []string{"?", "?", "?"}
	vals := []interface{}{hash, zh, tenantID}
	updates := []string{"zh=excluded.zh"}

	// 动态拼接 16 个语言列（缺失语言写空串，保留已有列更新）
	for _, lc := range AllLangs {
		v, ok := translations[lc]
		if !ok {
			v = ""
		}
		cols = append(cols, lc)
		placeholders = append(placeholders, "?")
		vals = append(vals, v)
		updates = append(updates, fmt.Sprintf("%s=excluded.%s", lc, lc))
	}
	// 可选 module 列
	if module != "" {
		cols = append(cols, "module")
		placeholders = append(placeholders, "?")
		vals = append(vals, module)
		updates = append(updates, "module=excluded.module")
	}
	cols = append(cols, "updated_at")
	placeholders = append(placeholders, "?")
	vals = append(vals, time.Now().Format("2006-01-02T15:04:05"))

	sql := fmt.Sprintf(
		"INSERT INTO tm_segments (%s) VALUES (%s) ON CONFLICT(zh_hash, tenant_id) DO UPDATE SET %s",
		strings.Join(cols, ","), strings.Join(placeholders, ","), strings.Join(updates, ","))
	res, err := k.db.Exec(sql, vals...)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// AddTerm 新增/更新单条术语（只更新指定语言列，不清空其它列）。
// 参数：lang=目标语言，src=源中文，dst=译文，module=来源模块（空默认 manual），tenantID=租户 ID。
func (k *KBDatabase) AddTerm(lang, src, dst, module string, tenantID int64) error {
	hash := MD5Hex(src) // 计算唯一哈希键
	now := time.Now().Format("2006-01-02T15:04:05")
	if module == "" {
		module = "manual" // 手工录入默认标记
	}
	// ON CONFLICT 命中则只更新指定语言列
	if _, err := k.db.Exec(
		"INSERT INTO tm_segments (zh_hash, zh, tenant_id, "+lang+", module, updated_at) VALUES (?,?,?,?,?,?) "+
			"ON CONFLICT(zh_hash, tenant_id) DO UPDATE SET "+lang+"=excluded."+lang+", updated_at=excluded.updated_at",
		hash, src, tenantID, dst, module, now); err != nil {
		return err
	}
	return nil
}

// Stats 知识库统计（指定租户）。
// 参数：tenantID=租户 ID；返回总条数、各语言非空条数、段数（segCount 当前恒为 0）。
func (k *KBDatabase) Stats(tenantID int64) (total int64, perLang map[string]int, segCount int64, err error) {
	// 总条数
	if err = k.db.QueryRow("SELECT COUNT(*) FROM tm_segments WHERE tenant_id=?", tenantID).Scan(&total); err != nil {
		return
	}
	perLang = map[string]int{}
	rows, err := k.db.Query("SELECT en,ru,ar,es,pt,fr,kk,de,zh_hant,ms,id_lang,th,tr,it,pl,sv FROM tm_segments WHERE tenant_id=?", tenantID)
	if err != nil {
		return
	}
	defer rows.Close()
	var vals [16]sql.NullString
	for rows.Next() {
		if err := rows.Scan(&vals[0], &vals[1], &vals[2], &vals[3], &vals[4], &vals[5],
			&vals[6], &vals[7], &vals[8], &vals[9], &vals[10], &vals[11], &vals[12],
			&vals[13], &vals[14], &vals[15]); err != nil {
			continue
		}
		// 逐语言统计非空条数
		for i, lc := range AllLangs {
			if vals[i].Valid && strings.TrimSpace(vals[i].String) != "" {
				perLang[lc]++
			}
		}
	}
	return total, perLang, 0, nil
}

// AllTenantIDs 返回所有租户 id（供索引构建/重建遍历）。
// 返回：去重后的租户 ID 列表。
func (k *KBDatabase) AllTenantIDs() ([]int64, error) {
	rows, err := k.db.Query("SELECT DISTINCT tenant_id FROM tm_segments WHERE tenant_id IS NOT NULL")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			continue
		}
		out = append(out, id)
	}
	return out, nil
}

// AllRowsWithTenant 遍历全部条目的 id + zh + tenant_id（构建租户映射用）。
// 返回：全部精简行列表（跨租户）。
func (k *KBDatabase) AllRowsWithTenant() ([]Row, error) {
	rows, err := k.db.Query("SELECT id, zh, COALESCE(module,''), COALESCE(tenant_id,1), COALESCE(pack_id,0) FROM tm_segments")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Row
	for rows.Next() {
		var r Row
		if err := rows.Scan(&r.ID, &r.Zh, &r.Module, &r.TenantID, &r.PackID); err != nil {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}
