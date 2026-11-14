// ============ query.go · 职责说明 ============
// 四期 W3（§4.3.4）：契约端点的通用真 SQL 读口。
//
//   - 所有 SELECT 硬带 tenant_id=?（KN3 fail-closed）；列名取自 contract 固定清单、
//     表名走白名单、过滤值一律参数化 ⇒ 无 SQL 注入面。
//   - 返回 []map[string]any：端点据此组装契约响应，零 LLM、零跨域计算。
// ================================================
package store

import (
	"database/sql"
	"fmt"
	"strings"

	"translator/internal/db"
	"translator/internal/knowledge/contract"
)

// QueryScoped 通用作用域行查询（真 SQL）。
// 参数：table 必须是白名单知识表；tid 租户；extraWhere 只能是代码内常量片段（其 ? 由 args 提供）。
// tid<=0 ⇒ 返回空集（fail-closed）。
func QueryScoped(conn *sql.DB, d db.Dialect, table string, tid int64, extraWhere string, args ...interface{}) ([]map[string]any, error) {
	if !IsKnowledgeTable(table) {
		return nil, fmt.Errorf("knowledge: 非知识表 %q（作用域查询拒绝）", table)
	}
	if tid <= 0 {
		return []map[string]any{}, nil
	}
	cols := contract.ColumnsFor(table)
	if len(cols) == 0 {
		return nil, fmt.Errorf("knowledge: 表 %q 无列清单", table)
	}
	q := "SELECT " + strings.Join(cols, ",") + " FROM " + table + " WHERE tenant_id=?" + extraWhere
	allArgs := append([]interface{}{tid}, args...)
	rows, err := db.Query(conn, d, q, allArgs...)
	if err != nil {
		return nil, fmt.Errorf("knowledge: 作用域查询失败(%s): %w", table, err)
	}
	defer rows.Close()
	out := make([]map[string]any, 0)
	for rows.Next() {
		vals := make([]interface{}, len(cols))
		ptrs := make([]interface{}, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, fmt.Errorf("knowledge: 行扫描失败(%s): %w", table, err)
		}
		m := make(map[string]any, len(cols))
		for i, c := range cols {
			m[c] = normalizeScalar(vals[i])
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("knowledge: 行迭代失败(%s): %w", table, err)
	}
	return out, nil
}

// normalizeScalar 把驱动返回的标量归一成 JSON 友好值（nil→""、[]byte→string）。
func normalizeScalar(v interface{}) interface{} {
	switch x := v.(type) {
	case nil:
		return ""
	case []byte:
		return string(x)
	case int64:
		return x
	default:
		return fmt.Sprint(v)
	}
}

// CountWhere 作用域计数（带可信常量过滤片段），供 corpus/public、facts/admitted、readiness 等读数用。
func CountWhere(conn *sql.DB, d db.Dialect, table string, tid int64, extraWhere string, args ...interface{}) (int, error) {
	if !IsKnowledgeTable(table) {
		return 0, fmt.Errorf("knowledge: 非知识表 %q", table)
	}
	if tid <= 0 {
		return 0, nil
	}
	q := "SELECT COUNT(1) FROM " + table + " WHERE tenant_id=?" + extraWhere
	allArgs := append([]interface{}{tid}, args...)
	var n int
	if err := db.QueryRow(conn, d, q, allArgs...).Scan(&n); err != nil {
		return 0, fmt.Errorf("knowledge: 计数失败(%s): %w", table, err)
	}
	return n, nil
}
