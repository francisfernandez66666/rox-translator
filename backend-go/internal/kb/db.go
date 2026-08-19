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
	db     *sql.DB
	dbPath string
}

// Row 翻译记忆条目
type Row struct {
	ID       int64
	Zh       string
	Module   string
	TenantID int64
	Langs    map[string]string // 语言代码 → 译文
}

var cjkRe = regexp.MustCompile("[\u4e00-\u9fff\u3400-\u4dbf]")

// ExtractCJK 抽出全部汉字
func ExtractCJK(text string) string {
	return strings.Join(cjkRe.FindAllString(text, -1), "")
}

// MD5Hex 计算 zh_hash
func MD5Hex(s string) string {
	h := md5.Sum([]byte(s))
	return fmt.Sprintf("%x", h)
}

// Open 打开数据库并确保表存在
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

// EnsureTenantMigration 确保 tm_segments 有 tenant_id 列，并把既有数据归入默认租户 rox（id=1）
func (k *KBDatabase) EnsureTenantMigration() error {
	// 加列（SQLite: 无 IF NOT EXISTS 加列，先查后加）
	cols, err := k.db.Query("PRAGMA table_info(tm_segments)")
	if err != nil {
		return err
	}
	defer cols.Close()
	hasTenant := false
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

// rebuildUniqueIndex 检查并重建复合唯一约束
func (k *KBDatabase) rebuildUniqueIndex() error {
	idxRows, err := k.db.Query("PRAGMA index_list(tm_segments)")
	if err != nil {
		return err
	}
	defer idxRows.Close()
	hasComposite := false
	var idxNames []string
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
							hasComposite = true
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
		return nil
	}
	return k.rebuildTableWithCompositeUnique()
}

// isSingleUniqueOnZhHash 判断 tm_segments 的隐式唯一索引是否只含 zh_hash 一列
func (k *KBDatabase) isSingleUniqueOnZhHash() (bool, error) {
	rows, err := k.db.Query("PRAGMA index_list(tm_segments)")
	if err != nil {
		return false, err
	}
	defer rows.Close()
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

// rebuildTableWithCompositeUnique 重建表：把 zh_hash UNIQUE 换成 (zh_hash, tenant_id) UNIQUE
func (k *KBDatabase) rebuildTableWithCompositeUnique() error {
	// 用事务包裹，避免中途失败导致数据不一致
	tx, err := k.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	cols := "id, zh_hash, zh, zh_short, module, tenant_id, en, ru, ar, es, pt, fr, kk, de, zh_hant, ms, id_lang, th, tr, it, pl, sv, updated_at"
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
	if _, err := tx.Exec("INSERT INTO tm_segments_new (" + cols + ") SELECT " + cols + " FROM tm_segments"); err != nil {
		return err
	}
	if _, err := tx.Exec("DROP TABLE tm_segments"); err != nil {
		return err
	}
	if _, err := tx.Exec("ALTER TABLE tm_segments_new RENAME TO tm_segments"); err != nil {
		return err
	}
	return tx.Commit()
}

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

// Close 关闭数据库
func (k *KBDatabase) Close() error { return k.db.Close() }

// RawDB 返回底层 *sql.DB（供租户存储等共享同一连接）
func (k *KBDatabase) RawDB() *sql.DB { return k.db }

const langCols = "en, ru, ar, es, pt, fr, kk, de, zh_hant, ms, id_lang, th, tr, it, pl, sv"

// FetchRow 按 id + tenant 查询整行
func (k *KBDatabase) FetchRow(id int64) (*Row, error) {
	row := k.db.QueryRow(fmt.Sprintf(
		"SELECT id, zh, COALESCE(module,''), COALESCE(tenant_id,1), %s FROM tm_segments WHERE id=?",
		langCols), id)
	return scanRow(row)
}

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
	r.Langs = map[string]string{
		"en": en.String, "ru": ru.String, "ar": ar.String, "es": es.String,
		"pt": pt.String, "fr": fr.String, "kk": kk.String, "de": de.String,
		"zh_hant": zhHant.String, "ms": ms.String, "id_lang": idLang.String,
		"th": th.String, "tr": tr.String, "it": it.String, "pl": pl.String, "sv": sv.String,
	}
	return &r, nil
}

// FetchRowTenant 按 id 查询整行（并校验属于指定租户）
func (k *KBDatabase) FetchRowTenant(id, tenantID int64) (*Row, error) {
	row := k.db.QueryRow(fmt.Sprintf(
		"SELECT id, zh, COALESCE(module,''), COALESCE(tenant_id,1), %s FROM tm_segments WHERE id=? AND tenant_id=?",
		langCols), id, tenantID)
	return scanRow(row)
}

// FindExact 精确命中：SELECT * WHERE zh=?（指定租户）
func (k *KBDatabase) FindExact(zh string, tenantID int64) (*Row, error) {
	row := k.db.QueryRow(fmt.Sprintf(
		"SELECT id, zh, COALESCE(module,''), COALESCE(tenant_id,1), %s FROM tm_segments WHERE zh=? AND tenant_id=?",
		langCols), zh, tenantID)
	return scanRow(row)
}

// FuzzyHits 模糊子串命中（LIMIT n，长度差 ≤30；指定租户）
func (k *KBDatabase) FuzzyHits(zhShort string, limit int, tenantID int64) ([]*Row, error) {
	rows, err := k.db.Query("SELECT id, zh FROM tm_segments WHERE zh LIKE ? AND tenant_id=?", "%"+zhShort+"%", tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type cand struct {
		id   int64
		zh   string
		diff int
	}
	var cands []cand
	for rows.Next() {
		var id int64
		var zh string
		if err := rows.Scan(&id, &zh); err != nil {
			continue
		}
		diff := len([]rune(zh)) - len([]rune(zhShort))
		if diff >= 0 && diff <= 30 {
			cands = append(cands, cand{id: id, zh: zh, diff: diff})
		}
	}
	// 按长度差升序
	for i := 0; i < len(cands); i++ {
		for j := i + 1; j < len(cands); j++ {
			if cands[j].diff < cands[i].diff {
				cands[i], cands[j] = cands[j], cands[i]
			}
		}
	}
	if limit > len(cands) {
		limit = len(cands)
	}
	var out []*Row
	for _, c := range cands[:limit] {
		r, err := k.FetchRow(c.id)
		if err == nil {
			out = append(out, r)
		}
	}
	return out, nil
}

// GetAllRows 遍历指定租户所有条目的 id + zh + tenant（构建 CJK 缓存用）
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
		for i, lc := range AllLangs {
			if cols[i].Valid && strings.TrimSpace(cols[i].String) != "" {
				set[lc] = true
			}
		}
		if len(set) > 0 {
			out[id] = set
		}
	}
	return out, nil
}

// SaveBack upsert：INSERT ... ON CONFLICT(zh_hash, tenant_id) DO UPDATE
func (k *KBDatabase) SaveBack(zh string, translations map[string]string, module string, tenantID int64) (int64, error) {
	hash := MD5Hex(zh)
	cols := []string{"zh_hash", "zh", "tenant_id"}
	placeholders := []string{"?", "?", "?"}
	vals := []interface{}{hash, zh, tenantID}
	updates := []string{"zh=excluded.zh"}

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

// AddTerm 新增/更新单条术语（只更新指定语言列，不清空其它列）
func (k *KBDatabase) AddTerm(lang, src, dst, module string, tenantID int64) error {
	hash := MD5Hex(src)
	now := time.Now().Format("2006-01-02T15:04:05")
	if module == "" {
		module = "manual"
	}
	if _, err := k.db.Exec(
		"INSERT INTO tm_segments (zh_hash, zh, tenant_id, "+lang+", module, updated_at) VALUES (?,?,?,?,?,?) "+
			"ON CONFLICT(zh_hash, tenant_id) DO UPDATE SET "+lang+"=excluded."+lang+", updated_at=excluded.updated_at",
		hash, src, tenantID, dst, module, now); err != nil {
		return err
	}
	return nil
}

// Stats 知识库统计（指定租户）
func (k *KBDatabase) Stats(tenantID int64) (total int64, perLang map[string]int, segCount int64, err error) {
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
		for i, lc := range AllLangs {
			if vals[i].Valid && strings.TrimSpace(vals[i].String) != "" {
				perLang[lc]++
			}
		}
	}
	return total, perLang, 0, nil
}

// AllTenantIDs 返回所有租户 id（供索引构建/重建遍历）
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

// AllRowsWithTenant 遍历全部条目的 id + tenant_id（构建租户映射用）
func (k *KBDatabase) AllRowsWithTenant() ([]Row, error) {
	rows, err := k.db.Query("SELECT id, zh, COALESCE(module,''), COALESCE(tenant_id,1) FROM tm_segments")
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
