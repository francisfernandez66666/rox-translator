// ============ introspect.go · 职责说明 ============
// 数据库元数据查询工具：提供跨方言的表结构自省能力，包括列存在性检查
// 与 UNIQUE 约束集合查询，供迁移逻辑判断唯一键形态。
// =============================================
package db

import (
	"database/sql"
)

// HasColumn 判断表是否存在指定列（跨方言）。
// SQLite 走 PRAGMA table_info；PostgreSQL 走 information_schema.columns。
// 参数：conn=数据库连接；d=方言；table=表名；col=列名；返回列是否存在及错误。
func HasColumn(conn *sql.DB, d Dialect, table, col string) (bool, error) {
	if d != DialectSQLite {
		// PostgreSQL 路径：查询 information_schema.columns
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
	// SQLite 路径：查询 PRAGMA table_info
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
// 参数：conn=数据库连接；d=方言；table=表名；返回唯一键列集合列表及错误。
func UniqueColumnSets(conn *sql.DB, d Dialect, table string) ([][]string, error) {
	if d != DialectSQLite {
		// PostgreSQL 路径：查询 information_schema.table_constraints 与 key_column_usage
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
	// SQLite 路径：查询 PRAGMA index_list 与 PRAGMA index_info
	rows, err := conn.Query("PRAGMA index_list(" + table + ")")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	// 索引结构：记录索引名及其包含的列
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
			continue // 仅收集 UNIQUE 索引
		}
		idxs = append(idxs, idx{name: name})
	}
	// 查询每个唯一索引的列信息
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
	// 组装结果：将每个唯一索引的列集合作为独立元素返回
	out := make([][]string, 0, len(idxs))
	for _, ix := range idxs {
		out = append(out, ix.cols)
	}
	return out, nil
}
