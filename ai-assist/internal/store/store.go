// Package store SQLite 存储层：会话/消息/知识库/话术/流程/功能入口/配置。
// 全部表结构在此建齐（幂等），seed 由 main 启动时按需灌入。
package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// DB 数据库句柄
type DB struct {
	sql *sql.DB
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
	stmts := []string{
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
		`CREATE TABLE IF NOT EXISTS sessions(
			id TEXT PRIMARY KEY,
			page_url TEXT DEFAULT '',
			in_flow TEXT DEFAULT '',
			flow_step INTEGER DEFAULT 0,
			msg_count INTEGER DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			last_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS messages(
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL,
			role TEXT NOT NULL,
			content TEXT NOT NULL,
			actions TEXT DEFAULT '[]',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_session ON messages(session_id, id)`,
		`CREATE TABLE IF NOT EXISTS configs(
			key TEXT PRIMARY KEY,
			value TEXT DEFAULT ''
		)`,
	}
	for _, s := range stmts {
		if _, err := d.sql.Exec(s); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	return nil
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
	if table != "sessions" && table != "messages" && table != "configs" {
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
// 会话与消息
// ============================================================

// SessionRow 按 id 查会话
func (d *DB) SessionRow(id string) (Row, error) {
	rows, err := d.sql.Query("SELECT id,page_url,in_flow,flow_step,msg_count,created_at,last_at FROM sessions WHERE id=?", id)
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
	if err := rows.Scan(&sid, &pageURL, &inFlow, &flowStep, &msgCount, &created, &last); err != nil {
		return nil, err
	}
	return Row{"id": sid, "page_url": pageURL, "in_flow": inFlow, "flow_step": flowStep, "msg_count": msgCount, "created_at": created, "last_at": last}, nil
}

// TouchSession 更新会话活跃时间与消息数
func (d *DB) TouchSession(id string) error {
	_, err := d.sql.Exec("UPDATE sessions SET last_at=CURRENT_TIMESTAMP, msg_count=msg_count+1 WHERE id=?", id)
	return err
}

// SetFlow 设置会话流程状态（key 为空表示退出流程）
func (d *DB) SetFlow(id, flowKey string, step int) error {
	_, err := d.sql.Exec("UPDATE sessions SET in_flow=?, flow_step=? WHERE id=?", flowKey, step, id)
	return err
}

// EnsureSession 幂等插入会话（INSERT OR IGNORE）
func (d *DB) EnsureSession(id, pageURL string) error {
	_, err := d.sql.Exec("INSERT OR IGNORE INTO sessions(id,page_url) VALUES(?,?)", id, pageURL)
	return err
}

// ListSessions 会话列表（管理端）
func (d *DB) ListSessions(limit int) ([]Row, error) {
	rows, err := d.sql.Query("SELECT id,page_url,in_flow,msg_count,created_at,last_at FROM sessions ORDER BY last_at DESC LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Row{}
	cols := []string{"id", "page_url", "in_flow", "msg_count", "created_at", "last_at"}
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

// AddMessage 追加消息
func (d *DB) AddMessage(sessionID, role, content string, actions []map[string]string) error {
	aj := "[]"
	if len(actions) > 0 {
		if b, err := json.Marshal(actions); err == nil {
			aj = string(b)
		}
	}
	_, err := d.sql.Exec("INSERT INTO messages(session_id,role,content,actions) VALUES(?,?,?,?)", sessionID, role, content, aj)
	return err
}

// History 取会话最近消息（正序），limit 为条数
func (d *DB) History(sessionID string, limit int) ([]Row, error) {
	rows, err := d.sql.Query("SELECT id,role,content,actions,created_at FROM (SELECT id,role,content,actions,created_at FROM messages WHERE session_id=? ORDER BY id DESC LIMIT ?) ORDER BY id ASC", sessionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Row{}
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

// SessionCount 统计
func (d *DB) SessionCount() int {
	var n int
	_ = d.sql.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&n)
	return n
}

// MessageCount 统计
func (d *DB) MessageCount() int {
	var n int
	_ = d.sql.QueryRow("SELECT COUNT(*) FROM messages").Scan(&n)
	return n
}
