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
func EnsureColumns(conn *sql.DB, d Dialect, table string, cols map[string]string) error {
	if d != DialectSQLite {
		for col, spec := range cols {
			if _, err := conn.Exec("ALTER TABLE " + table + " ADD COLUMN IF NOT EXISTS " + col + " " + spec); err != nil {
				return err
			}
		}
		return nil
	}
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
