package db

import (
	"database/sql"
)

// HasColumn 判断表是否存在指定列（跨方言）。
// SQLite 走 PRAGMA table_info；PostgreSQL 走 information_schema.columns。
func HasColumn(conn *sql.DB, d Dialect, table, col string) (bool, error) {
	if d != DialectSQLite {
		var n int
		err := conn.QueryRow(
			`SELECT 1 FROM information_schema.columns WHERE table_name=$1 AND column_name=$2`,
			table, col,
		).Scan(&n)
		if err == sql.ErrNoRows {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		return true, nil
	}
	rows, err := conn.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notnull, pk int
		var name, ctype string
		var dflt interface{}
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			continue
		}
		if name == col {
			return true, nil
		}
	}
	return false, nil
}

// UniqueColumnSets 返回表上所有 UNIQUE 约束/索引覆盖的列集合（跨方言）。
// 返回形如 [[zh_hash,tenant_id,pack_id], ...]，供迁移逻辑判断唯一键形态。
func UniqueColumnSets(conn *sql.DB, d Dialect, table string) ([][]string, error) {
	if d != DialectSQLite {
		rows, err := conn.Query(`
			SELECT kcu.constraint_name, kcu.column_name
			FROM information_schema.table_constraints tc
			JOIN information_schema.key_column_usage kcu
			  ON tc.constraint_name = kcu.constraint_name AND tc.table_schema = kcu.table_schema
			WHERE tc.table_name = $1 AND tc.constraint_type = 'UNIQUE'
			ORDER BY kcu.constraint_name, kcu.ordinal_position`, table)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		m := map[string][]string{}
		for rows.Next() {
			var cn, col string
			if err := rows.Scan(&cn, &col); err != nil {
				continue
			}
			m[cn] = append(m[cn], col)
		}
		out := make([][]string, 0, len(m))
		for _, cols := range m {
			out = append(out, cols)
		}
		return out, nil
	}
	rows, err := conn.Query("PRAGMA index_list(" + table + ")")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type idx struct {
		name string
		cols []string
	}
	var idxs []idx
	for rows.Next() {
		var seq, unique int
		var name, origin, partial string
		if err := rows.Scan(&seq, &name, &unique, &origin, &partial); err != nil {
			continue
		}
		if unique != 1 {
			continue
		}
		idxs = append(idxs, idx{name: name})
	}
	for i := range idxs {
		ir, err := conn.Query("PRAGMA index_info(" + idxs[i].name + ")")
		if err != nil {
			continue
		}
		for ir.Next() {
			var seqno int
			var cname string
			var desc interface{}
			if err := ir.Scan(&seqno, &cname, &desc); err == nil {
				idxs[i].cols = append(idxs[i].cols, cname)
			}
		}
		ir.Close()
	}
	out := make([][]string, 0, len(idxs))
	for _, ix := range idxs {
		out = append(out, ix.cols)
	}
	return out, nil
}
