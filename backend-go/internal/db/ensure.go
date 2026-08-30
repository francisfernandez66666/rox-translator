// ============ ensure.go · 职责说明 ============
// 幂等补列工具：按方言为已有表补充缺失列，支持 SQLite 与 PostgreSQL 双方言。
// PostgreSQL 使用 ALTER TABLE ADD COLUMN IF NOT EXISTS（本身幂等）；
// SQLite 使用 PRAGMA table_info 查询现有列后按需补列（旧版不支持 IF NOT EXISTS）。
// =============================================
package db

import (
	"database/sql"
)

// EnsureColumns 按方言幂等补列。
//   - PostgreSQL：直接 ALTER TABLE ADD COLUMN IF NOT EXISTS（本身幂等，无需先查）。
//   - SQLite：用 PRAGMA table_info 查现有列，缺失才 ALTER ADD COLUMN（旧版 SQLite 不支持 IF NOT EXISTS）。
//
// cols 为 列名 -> 列定义（含类型与约束，例如 "TEXT NOT NULL DEFAULT '{}'"）。
// 调用方传入的 SQL 均为各方言原生语句，本函数不经由占位符改写层，避免二次翻译。
// 参数：conn=数据库连接；d=方言；table=表名；cols=需要补充的列定义映射；返回执行错误。
func EnsureColumns(conn *sql.DB, d Dialect, table string, cols map[string]string) error {
	if d != DialectSQLite {
		// PostgreSQL 路径：直接使用 IF NOT EXISTS 语法，幂等补列
		for col, spec := range cols {
			if _, err := conn.Exec("ALTER TABLE " + table + " ADD COLUMN IF NOT EXISTS " + col + " " + spec); err != nil {
				return err
			}
		}
		return nil
	}
	// SQLite 路径：先查询现有列，再按需补列
	rows, err := conn.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return err
	}
	have := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt interface{}
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			continue
		}
		have[name] = true
	}
	_ = rows.Close()
	// 遍历需要补充的列，仅对缺失列执行 ALTER TABLE ADD COLUMN
	for col, spec := range cols {
		if have[col] {
			continue
		}
		if _, err := conn.Exec("ALTER TABLE " + table + " ADD COLUMN " + col + " " + spec); err != nil {
			return err
		}
	}
	return nil
}
