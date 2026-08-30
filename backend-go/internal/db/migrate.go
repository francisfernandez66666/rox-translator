package db

import (
	"database/sql"
	"strings"
	"time"
)

// Migration 单条模式迁移。SQLite 与 PostgreSQL 各自提供建表/改表语句（双 SQL），
// 由 Runner 按当前方言选择执行。Up 可为多条语句，以分号分隔。
type Migration struct {
	// ID 全局唯一标识（如 "0001_users"），用于幂等去重。
	ID string
	// SQLiteUp SQLite 方言的升级语句。
	SQLiteUp string
	// PostgresUp PostgreSQL 方言的升级语句。
	PostgresUp string
}

// UpSQL 返回当前方言应选用的升级语句。
func (m Migration) UpSQL(d Dialect) string {
	if d == DialectPostgres {
		return m.PostgresUp
	}
	return m.SQLiteUp
}

// Runner 负责按方言幂等执行迁移并记录已应用项。
type Runner struct {
	conn    *sql.DB
	dialect Dialect
}

// NewRunner 构造 Runner。conn 为已打开的连接，dialect 决定 SQL 选择。
func NewRunner(conn *sql.DB, dialect Dialect) *Runner {
	return &Runner{conn: conn, dialect: dialect}
}

// ensureTracking 创建迁移记录表（幂等）。
func (r *Runner) ensureTracking() error {
	_, err := r.conn.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		id TEXT PRIMARY KEY,
		applied_at TEXT NOT NULL DEFAULT '` + r.dialect.NowFn() + `'
	)`)
	return err
}

// applied 返回已应用的迁移 ID 集合。
func (r *Runner) applied() (map[string]bool, error) {
	if err := r.ensureTracking(); err != nil {
		return nil, err
	}
	rows, err := r.conn.Query("SELECT id FROM schema_migrations")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		m[id] = true
	}
	return m, rows.Err()
}

// Up 应用全部尚未执行的迁移。已应用的依据 schema_migrations 去重，重复调用安全。
func (r *Runner) Up(migrations []Migration) error {
	done, err := r.applied()
	if err != nil {
		return err
	}
	for _, mig := range migrations {
		if done[mig.ID] {
			continue
		}
		stmts := splitStmts(mig.UpSQL(r.dialect))
		for _, s := range stmts {
			if _, err := r.conn.Exec(s); err != nil {
				return &MigrateError{ID: mig.ID, SQL: s, Err: err}
			}
		}
		idPH := r.dialect.Placeholder(1)
		atPH := r.dialect.Placeholder(2)
		if _, err := r.conn.Exec("INSERT INTO schema_migrations (id, applied_at) VALUES ("+idPH+", "+atPH+")", mig.ID, time.Now().Format(time.RFC3339)); err != nil {
			return &MigrateError{ID: mig.ID, SQL: "record migration", Err: err}
		}
		done[mig.ID] = true
	}
	return nil
}

// Migrate 便捷函数：用给定连接与方言执行迁移列表。
func Migrate(conn *sql.DB, dialect Dialect, migrations []Migration) error {
	return NewRunner(conn, dialect).Up(migrations)
}

// MigrateError 包装迁移执行失败，附带迁移 ID 与出错的 SQL。
type MigrateError struct {
	ID  string
	SQL string
	Err error
}

// Error 实现 error 接口，返回带迁移 ID 与出错 SQL 的可读错误信息。
func (e *MigrateError) Error() string {
	return "db: 迁移 " + e.ID + " 执行失败 [" + e.SQL + "]: " + e.Err.Error()
}

// Unwrap 返回底层错误，支持 errors.Is / errors.As 链式的错误判定。
func (e *MigrateError) Unwrap() error { return e.Err }

// splitStmts 按分号拆分多语句 SQL，去除空白与行注释（-- 起始）。
func splitStmts(sqlText string) []string {
	// 先逐行剥离 -- 行注释，避免注释残留在语句中间。
	var cleaned strings.Builder
	for _, line := range strings.Split(sqlText, "\n") {
		if idx := strings.Index(line, "--"); idx >= 0 {
			line = line[:idx]
		}
		cleaned.WriteString(line)
		cleaned.WriteString("\n")
	}
	parts := strings.Split(cleaned.String(), ";")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		s := strings.TrimSpace(p)
		if s == "" {
			continue
		}
		out = append(out, s)
	}
	return out
}
