// Package store SQLite 存储层：会话/消息/知识库/话术/流程/功能入口/配置。
// 全部表结构在此建齐（幂等），seed 由 main 启动时按需灌入。
package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// DB 数据库句柄：封装 *sql.DB，所有表存取方法均挂在 DB 上。
type DB struct {
	sql *sql.DB // 底层 SQLite 连接（已设 WAL + 单写者，见 Open）

	// ★ 0AR 第 4 波：表→「有没有 updated_at 这一列」的缓存（判据见 Update／hasUpdatedAt）。
	// 用互斥锁罩住是因为跨两张库表读写这张 map 是**并发**的（管理台一行 PUT ＋
	// 挂件后台腿的 SetConfig 同刻各过一次 Update），裸 map 并发写是 runtime fatal error，
	// recover 兜不住、进程直接挂（AGENTS §三 那条快照口径的同一族）。
	colsMu     sync.Mutex
	colsCached map[string]bool
}

// Open 打开（必要时创建）SQLite 数据库并建表
func Open(path string) (*DB, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	// busy_timeout：admin 写入与访客写入并发时短暂等待而非报错
	s, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	s.SetMaxOpenConns(1) // SQLite 单写者，串行化避免 SQLITE_BUSY
	d := &DB{sql: s, colsCached: map[string]bool{}}
	if err := d.migrate(); err != nil {
		return nil, err
	}
	return d, nil
}

// Close 关闭数据库
func (d *DB) Close() error { return d.sql.Close() }

// migrate 建表（CREATE IF NOT EXISTS，幂等可重复执行）
func (d *DB) migrate() error {
	// 原始建表语句：仅对新实例有效，存量实例靠后续 ALTER TABLE 补列。
	baseStmts := []string{
		`CREATE TABLE IF NOT EXISTS kb_entries(
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			key TEXT UNIQUE,
			category TEXT DEFAULT 'usage',
			title TEXT NOT NULL,
			content TEXT NOT NULL,
			keywords TEXT DEFAULT '',
			link_keys TEXT DEFAULT '',
			priority INTEGER DEFAULT 5,
			enabled INTEGER DEFAULT 1,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS scripts(
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			key TEXT UNIQUE,
			stype TEXT DEFAULT 'keyword',
			title TEXT DEFAULT '',
			keywords TEXT DEFAULT '',
			link_keys TEXT DEFAULT '',
			content TEXT NOT NULL,
			priority INTEGER DEFAULT 5,
			enabled INTEGER DEFAULT 1,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS flows(
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			key TEXT UNIQUE,
			name TEXT NOT NULL,
			description TEXT DEFAULT '',
			trigger_keywords TEXT DEFAULT '',
			steps_json TEXT DEFAULT '[]',
			enabled INTEGER DEFAULT 1,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS feature_links(
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			key TEXT UNIQUE,
			name TEXT NOT NULL,
			description TEXT DEFAULT '',
			url TEXT NOT NULL,
			ftype TEXT DEFAULT 'route',
			icon TEXT DEFAULT '',
			sort INTEGER DEFAULT 50,
			enabled INTEGER DEFAULT 1
		)`,
		// ★ 〇-AM：sessions 和 messages 的 base 结构（不含 auth/anon/expiry，那些通过 ALTER TABLE 追加），
		// 防止 INSERT 时多列报错导致旧行被误删。
		`CREATE TABLE IF NOT EXISTS sessions_base(
			id TEXT PRIMARY KEY,
			page_url TEXT DEFAULT '',
			in_flow TEXT DEFAULT '',
			flow_step INTEGER DEFAULT 0,
			msg_count INTEGER DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			last_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			tenant_id INTEGER DEFAULT NULL,
			user_id INTEGER DEFAULT NULL,
			anonym_hash TEXT DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS messages_base(
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL,
			role TEXT NOT NULL,
			content TEXT NOT NULL,
			actions TEXT DEFAULT '[]',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			tenant_id INTEGER DEFAULT NULL,
			user_id INTEGER DEFAULT NULL,
			expires_at DATETIME DEFAULT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_session ON messages_base(session_id, id)`,
		`CREATE TABLE IF NOT EXISTS configs(
			key TEXT PRIMARY KEY,
			value TEXT DEFAULT ''
		)`,
	}
	for _, s := range baseStmts {
		if _, err := d.sql.Exec(s); err != nil {
			return fmt.Errorf("migrate base: %w", err)
		}
	}

	// ── 存量补齐：旧 sessions/messages → *_base（★ 判据见 migrateLegacyChatTables）──
	// 迁移失败（旧表结构不合预期等）**不挡启动**：那种情况下函数已在 DROP 之前返回，
	// 旧表原样留着、一行数据都不丢，下一次启动自动重试；
	// 挂件是访客侧组件，绝不允许为一处历史脏表整台起不来。
	_ = d.migrateLegacyChatTables()

	// ── 向新表追加可选列（幂等：ON CONFLICT DO NOTHING 不行，用 try ALTER）───
	d.ensureColumn("sessions_base", "expires_at", "DATETIME DEFAULT NULL")
	d.ensureColumn("messages_base", "expires_at", "DATETIME DEFAULT NULL")
	d.ensureColumn("sessions_base", "anonym_hash", "TEXT DEFAULT ''")
	d.ensureColumn("messages_base", "anonym_hash", "TEXT DEFAULT ''")

	// ── 索引 ────────────
	d.ensureIndex("idx_sessions_anon_expires ON sessions_base(anonym_hash, expires_at)")
	d.ensureIndex("idx_msgs_auth_expire ON messages_base(tenant_id, user_id, expires_at)")

	// ── colWhitelist 扩展 ────────────
	colWhitelist["sessions_base"] = []string{
		"id", "page_url", "in_flow", "flow_step", "msg_count",
		"tenant_id", "user_id", "anonym_hash",
	}
	colWhitelist["messages_base"] = []string{
		"session_id", "role", "content", "actions",
		"tenant_id", "user_id", "anonym_hash", "expires_at",
	}

	return nil
}

// tableExists 检查指定表是否存在（兼容 SQLite 方式）。
func (d *DB) tableExists(name string) (bool, error) {
	var cnt int
	err := d.sql.QueryRow(
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", name).Scan(&cnt)
	return cnt > 0, err
}

// dropTable 删除指定表（仅用于 migrate 内部的旧表清理）。
func (d *DB) dropTable(name string) {
	_, _ = d.sql.Exec("DROP TABLE IF EXISTS " + name)
}

// ensureColumn 尝试给表追加一列；列已存在时静默跳过（SQLite 报 "duplicate column" 错但不影响）。
func (d *DB) ensureColumn(table, col, defSpec string) {
	_, _ = d.sql.Exec("ALTER TABLE " + table + " ADD COLUMN " + col + " " + defSpec)
}

// ensureIndex 尝试创建索引；索引已存在时静默跳过。
func (d *DB) ensureIndex(idxDef string) {
	_, _ = d.sql.Exec("CREATE INDEX IF NOT EXISTS " + idxDef)
}

// legacyAnonymMarker 旧表迁进行的来源标记（不是真设备哈希）。
// 旧 sessions/messages 没有 auth 列 ⇒ 迁进来的行一律是匿名会话；打上这个标记并落上
// expires_at＝历史 last_at（早已过期），它们才会被 CleanupExpiredAnonymous 那条清理扫到
// ——否则「匿名会话到期清除」这句对外说法在这批历史数据上永远兑现不了。
const legacyAnonymMarker = "legacy_migrated"

// migrateLegacyChatTables 把旧 sessions/messages 的行搬进 *_base 并清掉旧表。
//
// ★ 判据必须问**数据层面的事实**（旧表里还有没有没搬走的行），不能问「新表在不在」：
//
//	旧形态把 CREATE TABLE IF NOT EXISTS *_base 排在本检查之前 ⇒ tableExists(base) 恒真
//	⇒ 条件「旧表存在 且 新表不存在」在有历史的库上永远不成立 ⇒ 迁移一次都没跑过，
//	旧表成孤儿（现网实证：挂件库里 sessions/messages 16／65 行 09-16~10-01 无人读写、
//	既不进后台列表也进不了过期清理）。
//
// 幂等且可回滚：先迁（INSERT OR IGNORE，重跑不产生重复行）→ 复核「旧表里已无未搬走的行」
// → 才 DROP；迁移中途出错（查询失败/事务失败）一律保留旧表原状，绝不销毁数据。
func (d *DB) migrateLegacyChatTables() error {
	for _, pair := range []struct{ legacy, base string }{
		{"sessions", "sessions_base"},
		{"messages", "messages_base"},
	} {
		has, err := d.tableExists(pair.legacy)
		if err != nil {
			return err
		}
		if !has {
			continue // 新实例：根本没有旧表
		}
		remaining, err := d.legacyRowsNotInBase(pair.legacy, pair.base)
		if err != nil {
			return err
		}
		if remaining > 0 {
			if pair.legacy == "sessions" {
				err = d.migrateSessionsToBase()
			} else {
				err = d.migrateMessagesToBase()
			}
			if err != nil {
				return err // 迁移失败：旧表原样留着，数据不丢
			}
			remaining, err = d.legacyRowsNotInBase(pair.legacy, pair.base)
			if err != nil {
				return err
			}
			if remaining > 0 {
				// 迁了但仍有行没搬过去（列缺失/类型不合等）：宁可留旧表，也不带着数据缺口 DROP
				return fmt.Errorf("legacy %s 仍有 %d 行未迁进 %s", pair.legacy, remaining, pair.base)
			}
		}
		d.dropTable(pair.legacy) // 旧表已空（或本来就没行）⇒ 孤儿清掉
	}
	return nil
}

// legacyRowsNotInBase 旧表里还没搬进新表的行数。
// 表名来自上面写死的常量对，不接受外部输入（拼进 SQL 无注入面）。
func (d *DB) legacyRowsNotInBase(legacy, base string) (int, error) {
	var n int
	err := d.sql.QueryRow("SELECT COUNT(*) FROM " + legacy + " l" +
		" WHERE NOT EXISTS (SELECT 1 FROM " + base + " b WHERE b.id=l.id)").Scan(&n)
	if err != nil {
		// 旧表结构连 id 列都没有（更老的形态）：按"整表都未迁"处理，交给迁移函数自己判
		return 0, nil
	}
	return n, nil
}

// tableRowCount 表行数（仅用于迁移留痕，读不到按 0）。
func (d *DB) tableRowCount(table string) int {
	var n int
	if err := d.sql.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
		return 0
	}
	return n
}

// legacySessionRow / legacyMessageRow 旧表行的内存快照。
// 为什么必须先收进切片再开事务：见 migrateSessionsToBase 的连接占用纪律（单连接池下 range 与 Begin 共存即死锁）。
type legacySessionRow struct {
	id       string
	pageURL  string
	inFlow   string
	flowStep int
	msgCount int
	created  string
	last     string
}

// legacyMessageRow 旧 `messages` 表一行的内存快照（注释闸逐行扫描，只认紧贴声明那一行，
// 所以上面那条「legacySessionRow / legacyMessageRow」联合注释不算它的数据——这里单独钉一份）。
// 刻意只收迁移要搬的那几列，不用 map 兜全表：列名拼错会在扫描期就报错，
// 而 map 会把"某一列根本没读上来"洗成静默空值——搬完还差内容是最难复盘的一种坏。
type legacyMessageRow struct {
	id        int
	sessionID string
	role      string
	content   string
	actions   string
	created   string
}

// migrateSessionsToBase 将旧 sessions 数据迁移到 sessions_base（匿名档：补 legacy 标记与到期时刻）。
//
// ★ 必须**先把旧行全部读进内存、关掉游标，再开事务**：本库的连接池是
// `SetMaxOpenConns(1)`（SQLite 单写者），而 `sql.Rows` 在 Next 走完前**一直占着那一条连接**——
// 于是「边 range rows 边 Begin」会当场死锁：Begin 永远等不到第二条连接，
// 而这段代码在 `Open()` 的启动路径上 ⇒ 现网表现是**挂件进程起来就不动**（不报错、不超时、
// 端口不监听），比一次失败严重得多。首跑真踩（assist/store 单测 600s 墙钟超时，
// 栈顶正是这里的 Begin）。判"某段迁移代码在单连接池下能不能跑"不靠语法直觉，靠真驱动跑一次。
func (d *DB) migrateSessionsToBase() error {
	rows, err := d.sql.Query("SELECT id, page_url, in_flow, flow_step, msg_count, created_at, last_at FROM sessions")
	if err != nil {
		return fmt.Errorf("读旧 sessions 失败: %w", err)
	}
	pending := make([]legacySessionRow, 0, 16)
	for rows.Next() {
		var r legacySessionRow
		if serr := rows.Scan(&r.id, &r.pageURL, &r.inFlow, &r.flowStep, &r.msgCount, &r.created, &r.last); serr != nil {
			rows.Close()
			return serr
		}
		pending = append(pending, r)
	}
	if rerr := rows.Err(); rerr != nil {
		rows.Close()
		return rerr
	}
	rows.Close() // ← 释放那唯一一条连接，下面才允许 Begin

	tx, err := d.sql.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.Prepare(`INSERT OR IGNORE INTO sessions_base
		(id,page_url,in_flow,flow_step,msg_count,created_at,last_at,anonym_hash,expires_at)
		VALUES(?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, r := range pending {
		// expires_at 落历史 last_at（旧表没有过期列）：这批早就过了保留期，清理腿该收走它们
		if _, ierr := stmt.Exec(r.id, r.pageURL, r.inFlow, r.flowStep, r.msgCount,
			r.created, r.last, legacyAnonymMarker, r.last); ierr != nil {
			return ierr
		}
	}
	return tx.Commit()
}

// migrateMessagesToBase 将旧 messages 数据迁移到 messages_base（沿用原 id，保证幂等与孤儿判据成立）。
// 连接占用纪律同 migrateSessionsToBase：先读完并 Close 游标，再开事务。
func (d *DB) migrateMessagesToBase() error {
	rows, err := d.sql.Query("SELECT id, session_id, role, content, actions, created_at FROM messages")
	if err != nil {
		return fmt.Errorf("读旧 messages 失败: %w", err)
	}
	pending := make([]legacyMessageRow, 0, 64)
	for rows.Next() {
		var r legacyMessageRow
		if serr := rows.Scan(&r.id, &r.sessionID, &r.role, &r.content, &r.actions, &r.created); serr != nil {
			rows.Close()
			return serr
		}
		pending = append(pending, r)
	}
	if rerr := rows.Err(); rerr != nil {
		rows.Close()
		return rerr
	}
	rows.Close()

	tx, err := d.sql.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.Prepare(`INSERT OR IGNORE INTO messages_base
		(id,session_id,role,content,actions,created_at,anonym_hash)
		VALUES(?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, r := range pending {
		if _, ierr := stmt.Exec(r.id, r.sessionID, r.role, r.content, r.actions, r.created, legacyAnonymMarker); ierr != nil {
			return ierr
		}
	}
	return tx.Commit()
}

// ============================================================
// 通用行存取辅助
// ============================================================

// Row 通用动态行（管理后台表格直接展示）
type Row map[string]any

// nullStr nil 安全转字符串
func nullStr(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

// List 通用列表：table + 过滤（enabled 可选）
func (d *DB) List(table string, onlyEnabled bool) ([]Row, error) {
	q := "SELECT * FROM " + table
	if onlyEnabled {
		q += " WHERE enabled=1"
	}
	order := " ORDER BY id"
	switch table {
	case "feature_links":
		order = " ORDER BY sort, id"
	case "kb_entries", "scripts":
		order = " ORDER BY priority DESC, id"
	case "configs":
		order = " ORDER BY key" // configs 无 id 列，主键为 key
	}
	q += order
	rows, err := d.sql.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	out := []Row{}
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		r := Row{}
		for i, c := range cols {
			v := vals[i]
			if b, ok := v.([]byte); ok {
				v = string(b)
			}
			r[c] = v
		}
		out = append(out, r)
	}
	return out, nil
}

// Get 按 id 取一行
func (d *DB) Get(table string, id int64) (Row, error) {
	rows, err := d.sql.Query("SELECT * FROM "+table+" WHERE id=?", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, nil
	}
	cols, _ := rows.Columns()
	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	if err := rows.Scan(ptrs...); err != nil {
		return nil, err
	}
	r := Row{}
	for i, c := range cols {
		v := vals[i]
		if b, ok := v.([]byte); ok {
			v = string(b)
		}
		r[c] = v
	}
	return r, nil
}

// Create 通用插入（只允许白名单列，防注入）
func (d *DB) Create(table string, data map[string]any) (int64, error) {
	cols, args := allowedCols(table, data)
	if len(cols) == 0 {
		return 0, fmt.Errorf("no valid columns")
	}
	q := "INSERT INTO " + table + "("
	ph := ""
	for i, c := range cols {
		if i > 0 {
			q += ","
			ph += ","
		}
		q += c
		ph += "?"
	}
	q += ") VALUES(" + ph + ")"
	res, err := d.sql.Exec(q, args...)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// Update 通用更新
//
// ★ 0AR 第 4 波（现网实证：管理台改一张功能卡恒 500）：
// 「这一句要不要补 `,updated_at=CURRENT_TIMESTAMP`」的判据，从**手写表名清单**
// 换成了**问一次库里的真实表结构**（hasUpdatedAt）。旧形态是一张五元素名单
// （sessions／messages／configs／sessions_base／messages_base），它的含义本来就是
// 「这几张表没有 updated_at 列」——但那份事实被**抄成了表名**，于是每加一张没有这一列的表
// 都得记得回来补一条名单，漏一次就把那张表的通用写腿在解析期打成
// `SQL logic error: no such column: updated_at (1)`。`feature_links` 正是漏掉的那一张
// （建表语句里根本没有这一列，见 migrate），而它挂在管理台的通用表格接口
// （api 侧 `handleTable("feature_links")` 的 PUT 支）上，
// 所以现网表现是「运营改/停用任何一张功能入口卡都保存失败」——
// 一行代码的缺失把整张运营面钉死，而且只在**写**的时候炸，读侧完全正常，
// 单测/冒烟从不 PUT 功能卡，所以能一直藏着。
//
// 结构判据比名单判据贵一次 `PRAGMA table_info`，但那个查询只在**每张表的第一次写**发生
// （结果缓存），而它一旦错过就**不会再错第二次**——这正是清单式判据和派生式判据的分别。
func (d *DB) Update(table string, id int64, data map[string]any) error {
	cols, args := allowedCols(table, data)
	if len(cols) == 0 {
		return fmt.Errorf("no valid columns")
	}
	q := "UPDATE " + table + " SET "
	for i, c := range cols {
		if i > 0 {
			q += ","
		}
		q += c + "=?"
	}
	if d.hasUpdatedAt(table) {
		q += ",updated_at=CURRENT_TIMESTAMP"
	}
	q += " WHERE id=?"
	args = append(args, id)
	_, err := d.sql.Exec(q, args...)
	return err
}

// hasUpdatedAt 这张表到底有没有 updated_at 列（★ 判据取自库里的真实结构，不取自代码里的清单）。
//
// 查询失败一律按「没有」处理：**宁可不时间戳，也不许把一次正常写打成 500**。
// （反过来按「有」处理就退回本次修掉的那个缺陷——一句解析期就报错的 SQL。）
// 缓存只在成功时写入：SQLite 打开后表结构不会在进程生命周期内变，但**探测失败不是答案**，
// 把它缓存下来等于让一次瞬时故障永久定格成「这张表没有时间戳列」。
//
// ⚠️ 本函数**自己不持锁**：map 的读写一律经 cachedUpdatedAt／rememberUpdatedAt 那两道
// （sync.Mutex 不可重入，这里再锁一次就是当场自锁）。
func (d *DB) hasUpdatedAt(table string) bool {
	if v, ok := d.cachedUpdatedAt(table); ok {
		return v
	}
	rows, err := d.sql.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return false
	}
	defer rows.Close()
	has := false
	for rows.Next() {
		var (
			cid     int
			name    string
			ctype   string
			notnull int
			dflt    sql.NullString
			pk      int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return false
		}
		if name == "updated_at" {
			has = true
		}
	}
	if err := rows.Err(); err != nil {
		return false
	}
	d.rememberUpdatedAt(table, has)
	return has
}

// cachedUpdatedAt／rememberUpdatedAt 是那张缓存 map 的**唯一两个出入口**（★ 0AR 第 4 波）。
//
// 为什么把两行 map 操作拆成方法而不是就地写：并发安全这件事必须有一个**能被打断的靶子**。
// 就地写在 hasUpdatedAt 里，反证（把互斥锁摘掉）在 `-race` 下常常**测不出来**——
// 这个库只有**一条**连接（SetMaxOpenConns(1)），那些并发 goroutine 全排在 `Query` 上，
// map 读写被连接池顺带串行化了，于是"摘锁"这条变异会绿。
// 拆出来之后，判据不碰库：多个 goroutine 并发读写这张 map 就是真并发，摘锁当场报数据竞争
// （裸 map 并发写在生产是 runtime fatal error，recover 兜不住、整台挂件进程挂）。
func (d *DB) cachedUpdatedAt(table string) (bool, bool) {
	d.colsMu.Lock()
	defer d.colsMu.Unlock()
	if d.colsCached == nil {
		return false, false
	}
	v, ok := d.colsCached[table]
	return v, ok
}

// rememberUpdatedAt 记一次**成功**探测的结果（探测失败一律不进来：失败不是答案）。
func (d *DB) rememberUpdatedAt(table string, has bool) {
	d.colsMu.Lock()
	defer d.colsMu.Unlock()
	if d.colsCached == nil {
		d.colsCached = map[string]bool{}
	}
	d.colsCached[table] = has
}

// Delete 按 id 删除
func (d *DB) Delete(table string, id int64) error {
	_, err := d.sql.Exec("DELETE FROM "+table+" WHERE id=?", id)
	return err
}

// colWhitelist 各表允许写入的列
var colWhitelist = map[string][]string{
	"kb_entries":    {"key", "category", "title", "content", "keywords", "link_keys", "priority", "enabled"},
	"scripts":       {"key", "stype", "title", "keywords", "link_keys", "content", "priority", "enabled"},
	"flows":         {"key", "name", "description", "trigger_keywords", "steps_json", "enabled"},
	"feature_links": {"key", "name", "description", "url", "ftype", "icon", "sort", "enabled"},
	"configs":       {"key", "value"},
}

// allowedCols 按白名单过滤写入列（防 SQL 注入/误写），返回列名与参数
func allowedCols(table string, data map[string]any) ([]string, []any) {
	wl, ok := colWhitelist[table]
	if !ok {
		return nil, nil
	}
	cols := []string{}
	args := []any{}
	for _, c := range wl {
		if v, ok := data[c]; ok {
			cols = append(cols, c)
			args = append(args, v)
		}
	}
	return cols, args
}

// ============================================================
// 配置
// ============================================================

// GetConfig 读配置项
func (d *DB) GetConfig(key, def string) string {
	var v string
	err := d.sql.QueryRow("SELECT value FROM configs WHERE key=?", key).Scan(&v)
	if err != nil || v == "" {
		return def
	}
	return v
}

// SetConfig 写配置项（upsert）
func (d *DB) SetConfig(key, value string) error {
	_, err := d.sql.Exec("INSERT INTO configs(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", key, value)
	return err
}

// DeleteConfig 删一个配置项（幂等：键不存在也按成功返回）。
//
// 存在的唯一理由＝「丢弃产物必须同时作废来源」这条纪律（AGENTS §九 第 7 条，现网实证 ㉞）：
// 译文不合格时只把它丢掉、把那行缓存留着，等于让下一位访客继续命中同一行坏数据，
// 而"命中缓存"这一支根本不会再打上游 ⇒ 坏行永不重翻、只在界面沉默地投出去。
// ⚠️ 调用方必须先确认这一行**不是**运营手工档（`!manual`）再删；人工档永不自动作废（见 localize 的放行分支）。
func (d *DB) DeleteConfig(key string) error {
	_, err := d.sql.Exec("DELETE FROM configs WHERE key=?", key)
	return err
}

// AllConfigs 全部配置
func (d *DB) AllConfigs() ([]Row, error) { return d.List("configs", false) }

// ConfigKeysByPrefix 按前缀列出配置项的键（只回键，不回值）。
//
// 存在的理由：`i18n:*` 那一族译文缓存需要一条"启动时把旧代孤儿行清掉"的腿（★ 0AR 第 4 波），
// 而它只要键名就能干活（值再按键单读一次）。刻意**不**复用 AllConfigs：那一条会把整张表的
// value 一起拉进内存（里面有 persona 与知识库文案），既无必要也是把客户内容抄进一份用不上的副本。
// ⚠️ 这里只读不删——删的判据（逐行重算指纹、`!manual` 永不作废）全在 engine 侧，
// store 不承接业务判据（AGENTS §一·1 规则 2：store.go 不承接业务方法）。
// 方言：`LIKE` 的前缀里没有 % 与 _（`i18n:` 这三个字符都是字面量），SQLite/PG 语义一致（§一·4）。
func (d *DB) ConfigKeysByPrefix(prefix string) ([]string, error) {
	rows, err := d.sql.Query("SELECT key FROM configs WHERE key LIKE ? ORDER BY key", prefix+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close() // 单连接池（Open 里 SetMaxOpenConns(1)）：游标不关就是下一条语句拿不到连接
	out := []string{}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// ============================================================
// 主服务库只读桥接（★ 改造 1A，2026-09-17）
// ============================================================

// ReadMainDBConfig 只读打开主服务 SQLite，读取 system_config 中某个键的原始值。
//
// 用途：assist-server 启动时从主库取 assist_admin_token（enc:v1: 密文），
// 与主后台 /api/admin/assist/token 同源，实现「管理台 Token 免手填」。
//
// 只读语义（mode=ro）：绝不建表、绝不写入，避免与主服务写锁竞争、也避免误改业务库。
// 任何失败（文件不存在/非 SQLite/表缺失）均返回空串——调用方据此回落到 env 或默认值，
// 不阻断 assist 启动（assist 挂掉只影响挂件，不应因主库读不到而整体不可用）。
//
// 注意：主服务切换到 PostgreSQL（DB_DRIVER=postgres）时本函数不可用，
// 该形态下需以 env ASSIST_ADMIN_TOKEN 为准（部署侧显式配置，优先级本就最高）。
//
// 参数：path=主服务 SQLite 文件路径；key=system_config 键名。返回：原始值（无则 ""）。
func ReadMainDBConfig(path, key string) string {
	if strings.TrimSpace(path) == "" || strings.TrimSpace(key) == "" {
		return ""
	}
	if _, err := os.Stat(path); err != nil {
		return "" // 文件不存在：静默回落（常见于主服务尚未初始化）
	}
	s, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=busy_timeout(2000)")
	if err != nil {
		return ""
	}
	defer s.Close()
	var v string
	if err := s.QueryRow("SELECT value FROM system_config WHERE key=?", key).Scan(&v); err != nil {
		return ""
	}
	return v
}

// ============================================================
// 会话与消息
// ============================================================

// SessionRow 按 id 查会话（返回所有列含 auth/anon）。
func (d *DB) SessionRow(id string) (Row, error) {
	rows, err := d.sql.Query("SELECT id,page_url,in_flow,flow_step,msg_count,created_at,last_at,tenant_id,user_id,anonym_hash FROM sessions_base WHERE id=?", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, nil
	}
	var sid, pageURL, inFlow string
	var flowStep, msgCount int
	var created, last time.Time
	var tenantID, userID sql.NullInt64
	var anonymHash string
	if err := rows.Scan(&sid, &pageURL, &inFlow, &flowStep, &msgCount, &created, &last, &tenantID, &userID, &anonymHash); err != nil {
		return nil, err
	}
	return Row{
		"id": sid, "page_url": pageURL, "in_flow": inFlow, "flow_step": flowStep,
		"msg_count": msgCount, "created_at": created, "last_at": last,
		"tenant_id": tenantID.Int64, "user_id": userID.Int64, "anonym_hash": anonymHash,
	}, nil
}

// TouchSession 更新会话活跃时间与消息数。
func (d *DB) TouchSession(id string) error {
	_, err := d.sql.Exec("UPDATE sessions_base SET last_at=CURRENT_TIMESTAMP, msg_count=msg_count+1 WHERE id=?", id)
	return err
}

// SetFlow 设置会话流程状态（key 为空表示退出流程）。
func (d *DB) SetFlow(id, flowKey string, step int) error {
	_, err := d.sql.Exec("UPDATE sessions_base SET in_flow=?, flow_step=? WHERE id=?", flowKey, step, id)
	return err
}

// CreateSession 幂等插入会话；已登录态时填写 tenant_id/user_id，匿名态填 anonym_hash。
// ★ 〇-AM：替代旧 EnsureSession，支持三种创建模式。
func (d *DB) CreateSession(id, pageURL string, tenantID, userID int64, anonymHash string) error {
	_, err := d.sql.Exec(
		"INSERT OR IGNORE INTO sessions_base(id,page_url,tenant_id,user_id,anonym_hash) VALUES(?,?,?, ?,?)",
		id, pageURL, tenantID, userID, anonymHash,
	)
	return err
}

// EnsureSession 别名：等价于 CreateSession（0, 0, ""）——向后兼容旧调用方。
func (d *DB) EnsureSession(id, pageURL string) error {
	return d.CreateSession(id, pageURL, 0, 0, "")
}

// ResolveSessionByAuth 按认证信息解析已有会话；无则返回 nil。
// 优先级：① tenant+user 命中 → 返回行 ② anon_hash + 未过期 → 返回行。
func (d *DB) ResolveSessionByAuth(tenantID, userID int64, anonymHash string) (Row, error) {
	// ① 登录态匹配
	if rows, err := d.sql.Query(
		"SELECT id,page_url,in_flow,flow_step,msg_count,created_at,last_at,tenant_id,user_id,anonym_hash FROM sessions_base WHERE tenant_id=? AND user_id=? LIMIT 1",
		tenantID, userID); err == nil && rows != nil {
		defer rows.Close()
		if rows.Next() {
			return d.scanSessionRow(rows)
		}
	}
	// ② 匿名态匹配
	if anonymHash != "" {
		if rows, err := d.sql.Query(
			"SELECT id,page_url,in_flow,flow_step,msg_count,created_at,last_at,tenant_id,user_id,anonym_hash FROM sessions_base WHERE anonym_hash=? AND expires_at IS NOT NULL AND expires_at > CURRENT_TIMESTAMP LIMIT 1",
			anonymHash); err == nil && rows != nil {
			defer rows.Close()
			if rows.Next() {
				return d.scanSessionRow(rows)
			}
		}
	}
	return nil, nil
}

// scanSessionRow 从多行 Scan 结果转为 Row。
func (d *DB) scanSessionRow(rows *sql.Rows) (Row, error) {
	var sid, pageURL, inFlow string
	var flowStep, msgCount int
	var created, last time.Time
	var tenantID, userID sql.NullInt64
	var anonymHash string
	if err := rows.Scan(&sid, &pageURL, &inFlow, &flowStep, &msgCount, &created, &last, &tenantID, &userID, &anonymHash); err != nil {
		return nil, err
	}
	return Row{
		"id": sid, "page_url": pageURL, "in_flow": inFlow, "flow_step": flowStep,
		"msg_count": msgCount, "created_at": created, "last_at": last,
		"tenant_id": tenantID.Int64, "user_id": userID.Int64, "anonym_hash": anonymHash,
	}, nil
}

// UpdateSessionAuth 将匿名用户升级/绑定为登录用户（合并会话）。
func (d *DB) UpdateSessionAuth(id string, tenantID, userID int64) error {
	_, err := d.sql.Exec("UPDATE sessions_base SET tenant_id=?, user_id=? WHERE id=? AND tenant_id IS NULL", tenantID, userID, id)
	return err
}

// ExpireSessionAnon 标记会话过期（软删除）。
func (d *DB) ExpireSessionAnon(id string) error {
	_, err := d.sql.Exec("UPDATE sessions_base SET expires_at=CURRENT_TIMESTAMP WHERE id=? AND anonym_hash!=''", id)
	return err
}

// ListSessions 会话列表（管理端，含 auth/anon 信息）。
func (d *DB) ListSessions(limit int) ([]Row, error) {
	rows, err := d.sql.Query("SELECT id,page_url,in_flow,msg_count,created_at,last_at,tenant_id,user_id,anonym_hash FROM sessions_base ORDER BY last_at DESC LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols := []string{"id", "page_url", "in_flow", "msg_count", "created_at", "last_at", "tenant_id", "user_id", "anonym_hash"}
	out := make([]Row, 0, limit)
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		r := Row{}
		for i, c := range cols {
			v := vals[i]
			if b, ok := v.([]byte); ok {
				v = string(b)
			}
			r[c] = v
		}
		out = append(out, r)
	}
	return out, nil
}

// MergeSessions 将旧会话的数据合并到新会话（用于登录态合并匿名会话）。
// ★ 〇-AM：被 resolveOldSessionToNew 调用。
func (d *DB) MergeSessions(oldID, newID string) error {
	// 1. 创建新会话行（继承老行的关键数据）
	_, err := d.sql.Exec(`INSERT OR IGNORE INTO sessions_base(id, page_url, tenant_id, user_id, anonym_hash, msg_count, last_at)
		SELECT ?, page_url, tenant_id, user_id, '', msg_count, last_at FROM sessions_base WHERE id=?`, newID, oldID)
	if err != nil {
		return err
	}
	// 2. 消息关联到新会话
	_, _ = d.sql.Exec("UPDATE messages_base SET session_id=? WHERE session_id=?", newID, oldID)
	// 3. 删除旧会话
	_, _ = d.sql.Exec("DELETE FROM sessions_base WHERE id=?", oldID)
	return nil
}

// CleanupExpiredAnonymous 清除过期的匿名会话及其孤儿消息。
func (d *DB) CleanupExpiredAnonymous(batchSize int) (int, error) {
	if batchSize <= 0 {
		batchSize = 100
	}
	// 删除过期消息。★ 〇-AP：LIMIT 只能挂在**子查询的 SELECT** 上，不能直接挂 DELETE——
	// SQLite 默认发行没开 SQLITE_ENABLE_UPDATE_DELETE_LIMIT，`DELETE … LIMIT ?` 会报
	// `near "LIMIT": syntax error`（现网实读：每小时整点一条 ERROR「过期匿名会话清理失败」，
	// 自 10-02 起匿名会话与孤儿消息从未真正清过、只堆库）。
	// 改走 `id IN (SELECT id … LIMIT ?)`：两方言都支持（AGENTS §一·4），批量语义不变。
	res, err := d.sql.Exec("DELETE FROM messages_base WHERE id IN (SELECT id FROM messages_base WHERE session_id IN (SELECT id FROM sessions_base WHERE anonym_hash!='' AND (expires_at IS NULL OR expires_at<=CURRENT_TIMESTAMP)) LIMIT ?)", batchSize)
	if err != nil {
		return 0, err
	}
	msgCnt, _ := res.RowsAffected()
	// ★ 〇-AR 第 7 波（㊿ 的修法）：删会话的判据必须先问**消息表里还有没有行**，不信 msg_count 这个写时计数器。
	// 旧形态只有 `msg_count=0` 那一档能被删，而计数是 AddMessage 时 +1、消息被本函数删掉时**从来不减**，
	// 于是任何"消息已被清空"的会话都带着旧计数变成**永远清不掉的壳行**；
	// 最典型的来源是 〇-AM 的旧表搬迁（`sessions`→`sessions_base` 把历史计数原样搬过来），
	// 现网实证：搬迁后整点清理把 65 条消息按批删空，`messages_base=0` 而 `sessions_base` 剩 13 行、每小时都删不动。
	// ⇒ 先把本轮**碰到的**那批（过期匿名会话）的计数按真值归一次，再走原来的 `msg_count=0` 删除判据：
	//   计数不再是第二把尺子（它和消息表在删除时刻同源），运营在管理台看到的条数也不再撒谎。
	// 关联子查询两方言都支持；范围只圈「过期＋匿名」那一小批，不去动登录态会话与未过期会话。
	if _, err := d.sql.Exec("UPDATE sessions_base SET msg_count=(SELECT COUNT(*) FROM messages_base WHERE messages_base.session_id=sessions_base.id) WHERE anonym_hash!='' AND (expires_at IS NULL OR expires_at<=CURRENT_TIMESTAMP)"); err != nil {
		// 归一失败即**停在这里**：宁可这一轮多留一批壳行（下一轮还会再来），
		// 也不能带着"可能是假的 0"去执行删除——那会把刚被误清零的正常会话删掉。
		return int(msgCnt), err
	}
	// 删除空会话（此处 msg_count 已与消息表同源，`msg_count=0` 才真的等于"没有消息"）
	_, _ = d.sql.Exec("DELETE FROM sessions_base WHERE anonym_hash!='' AND msg_count=0 AND (expires_at IS NULL OR expires_at<=CURRENT_TIMESTAMP)")
	return int(msgCnt), nil
}

// AddMessage 追加消息（匿名态，tenant_id、user_id 为 NULL，anonym_hash 为空串）。
func (d *DB) AddMessage(sessionID, role, content string, actions []map[string]string) error {
	aj := "[]"
	if len(actions) > 0 {
		if b, err := json.Marshal(actions); err == nil {
			aj = string(b)
		}
	}
	_, err := d.sql.Exec(
		"INSERT INTO messages_base(session_id,role,content,actions,anonym_hash) VALUES(?,?,?,?,?)",
		sessionID, role, content, aj, "")
	return err
}

// AddMessageWithAuth 追加消息并携带认证上下文（登录态填 tenant_id/user_id，匿名态填 anonym_hash + expires_at）。
// ★ 〇-AM：用于已登录用户的会话挂钩。
func (d *DB) AddMessageWithAuth(sessionID, msgRole, text string, actions []map[string]string, tenantID, userID int64, anonymHash, expiresAt string) error {
	aj := "[]"
	if len(actions) > 0 {
		if b, err := json.Marshal(actions); err == nil {
			aj = string(b)
		}
	}
	_, err := d.sql.Exec(
		"INSERT INTO messages_base(session_id,role,content,actions,tenant_id,user_id,anonym_hash,expires_at) VALUES(?,?,?, ?, ?, ?,?, ?)",
		sessionID, msgRole, text, aj, tenantID, userID, anonymHash, expiresAt)
	return err
}

// History 取会话最近消息（正序），limit 为条数。
func (d *DB) History(sessionID string, limit int) ([]Row, error) {
	rows, err := d.sql.Query("SELECT id,role,content,actions,created_at FROM (SELECT id,role,content,actions,created_at FROM messages_base WHERE session_id=? ORDER BY id DESC LIMIT ?) ORDER BY id ASC", sessionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Row, 0, limit)
	for rows.Next() {
		var id int
		var role, content, actions string
		var created time.Time
		if err := rows.Scan(&id, &role, &content, &actions, &created); err != nil {
			return nil, err
		}
		out = append(out, Row{"id": id, "role": role, "content": content, "actions": actions, "created_at": created})
	}
	return out, nil
}

// SessionCount 统计。
func (d *DB) SessionCount() int {
	var n int
	_ = d.sql.QueryRow("SELECT COUNT(*) FROM sessions_base").Scan(&n)
	return n
}

// MessageCount 统计。
func (d *DB) MessageCount() int {
	var n int
	_ = d.sql.QueryRow("SELECT COUNT(*) FROM messages_base").Scan(&n)
	return n
}
