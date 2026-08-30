// ============ migrate.go · 职责说明 ============
// 数据库迁移引擎：定义 Migration 结构体与 Runner 执行器，支持 SQLite/PostgreSQL
// 双方言迁移，按 schema_migrations 表幂等去重，确保每条迁移仅执行一次。
// =============================================
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
// 参数：d=方言；返回对应方言的升级 SQL。
func (m Migration) UpSQL(d Dialect) string {
	if d == DialectPostgres {
		return m.PostgresUp
	}
	return m.SQLiteUp
}

// Runner 负责按方言幂等执行迁移并记录已应用项。
type Runner struct {
	conn    *sql.DB   // 数据库连接
	dialect Dialect   // 当前方言
}

// NewRunner 构造 Runner。conn 为已打开的连接，dialect 决定 SQL 选择。
// 参数：conn=数据库连接；d=方言；返回迁移执行器实例。
func NewRunner(conn *sql.DB, d Dialect) *Runner {
	return &Runner{conn: conn, dialect: d}
}

// ensureTracking 创建迁移记录表（幂等）。
// 返回：执行错误。
func (r *Runner) ensureTracking() error {
	_, err := r.conn.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		id TEXT PRIMARY KEY,
		applied_at TEXT NOT NULL DEFAULT '` + r.dialect.NowFn() + `'
	)`)
	return err
}

// applied 返回已应用的迁移 ID 集合。
// 返回：已应用迁移 ID 的集合及错误。
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
// 参数：migrations=迁移列表；返回执行错误。
func (r *Runner) Up(migrations []Migration) error {
	done, err := r.applied()
	if err != nil {
		return err
	}
	for _, mig := range migrations {
		if done[mig.ID] {
			continue // 已应用，跳过
		}
		// 按分号拆分多语句 SQL 并逐条执行
		stmts := splitStmts(mig.UpSQL(r.dialect))
		for _, s := range stmts {
			if _, err := r.conn.Exec(s); err != nil {
				return &MigrateError{ID: mig.ID, SQL: s, Err: err}
			}
		}
		// 记录迁移已应用
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
// 参数：conn=数据库连接；dialect=方言；migrations=迁移列表；返回执行错误。
func Migrate(conn *sql.DB, dialect Dialect, migrations []Migration) error {
	return NewRunner(conn, dialect).Up(migrations)
}

// MigrateError 包装迁移执行失败，附带迁移 ID 与出错的 SQL。
type MigrateError struct {
	ID  string // 迁移 ID
	SQL string // 出错的 SQL 语句
	Err error  // 底层错误
}

// Error 实现 error 接口，返回带迁移 ID 与出错 SQL 的可读错误信息。
func (e *MigrateError) Error() string {
	return "db: 迁移 " + e.ID + " 执行失败 [" + e.SQL + "]: " + e.Err.Error()
}

// Unwrap 返回底层错误，支持 errors.Is / errors.As 链式的错误判定。
func (e *MigrateError) Unwrap() error { return e.Err }

// splitStmts 按分号拆分多语句 SQL，去除空白与行注释（-- 起始）。
// 参数：sqlText=多语句 SQL 文本；返回拆分后的有效语句列表。
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
	// 按分号拆分并过滤空语句
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
