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
	"time"

	_ "modernc.org/sqlite"
)

// DB 数据库句柄：封装 *sql.DB，所有表存取方法均挂在 DB 上。
type DB struct {
	sql *sql.DB // 底层 SQLite 连接（已设 WAL + 单写者，见 Open）
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
	d := &DB{sql: s}
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

	// ── 存量补齐：sessions → sessions_base（迁移旧数据并重命名）───────────
	hasSessionTable, _ := d.tableExists("sessions")
	hasBaseTable, _ := d.tableExists("sessions_base")
	if hasSessionTable && !hasBaseTable {
		// 旧 sessions 存在但 sessions_base 不存在 → 从旧表复制到新表
		d.migrateSessionsToBase()
		d.dropTable("sessions") // 旧表不再需要
	}

	// ── 存量补齐：messages → messages_base ────────────
	hasMsgTable, _ := d.tableExists("messages")
	hasMsgBase, _ := d.tableExists("messages_base")
	if hasMsgTable && !hasMsgBase {
		d.migrateMessagesToBase()
		d.dropTable("messages")
	}

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

// migrateSessionsToBase 将旧 sessions 数据迁移到 sessions_base。
func (d *DB) migrateSessionsToBase() {
	rows, err := d.sql.Query("SELECT id, page_url, in_flow, flow_step, msg_count, created_at, last_at FROM sessions")
	if err != nil {
		return // 失败则保持原状
	}
	defer rows.Close()
	tx, _ := d.sql.Begin()
	defer func() { tx.Commit() }()
	stmt, _ := tx.Prepare("INSERT OR IGNORE INTO sessions_base(id,page_url,in_flow,flow_step,msg_count,created_at,last_at) VALUES(?,?,?,?,?,?,?)")
	defer stmt.Close()
	for rows.Next() {
		var id, pageURL, inFlow, created, last string
		var flowStep, msgCount int
		rows.Scan(&id, &pageURL, &inFlow, &flowStep, &msgCount, &created, &last)
		stmt.Exec(id, pageURL, inFlow, flowStep, msgCount, created, last)
	}
	tx.Commit()
}

// migrateMessagesToBase 将旧 messages 数据迁移到 messages_base。
func (d *DB) migrateMessagesToBase() {
	rows, err := d.sql.Query("SELECT id, session_id, role, content, actions, created_at FROM messages")
	if err != nil {
		return
	}
	defer rows.Close()
	tx, _ := d.sql.Begin()
	defer func() { tx.Commit() }()
	stmt, _ := tx.Prepare("INSERT INTO messages_base(id,session_id,role,content,actions,created_at) VALUES(?,?,?,?,?,?)")
	defer stmt.Close()
	for rows.Next() {
		var id int
		var sessionID, role, content, actions, created string
		rows.Scan(&id, &sessionID, &role, &content, &actions, &created)
		stmt.Exec(id, sessionID, role, content, actions, created)
	}
	tx.Commit()
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
	if table != "sessions" && table != "messages" && table != "configs" && table != "sessions_base" && table != "messages_base" {
		q += ",updated_at=CURRENT_TIMESTAMP"
	}
	q += " WHERE id=?"
	args = append(args, id)
	_, err := d.sql.Exec(q, args...)
	return err
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

// AllConfigs 全部配置
func (d *DB) AllConfigs() ([]Row, error) { return d.List("configs", false) }

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
	// 删除空会话
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
