// ============ scope.go · 职责说明 ============
// 四期 W3（KN3/KN13）：知识服务租户作用域 fail-closed。
//
//   - 四张表的每一次读都硬带 tenant_id=?；跨租户查询 ⇒ 空（不泄露存在性）。
//   - tid<=0 直接判空：非法/匿名作用域不许被当成「全租户」放行。
//   - 表名走白名单（contract.KnowledgeTableNames），不许外部拼表名（防注入）。
// ================================================
package store

import (
	"database/sql"
	"fmt"

	"translator/internal/db"
	"translator/internal/knowledge/contract"
)

// IsKnowledgeTable 表名白名单（防 SQL 注入：表名永远来自固定清单，不接外部字符串）。
func IsKnowledgeTable(t string) bool {
	for _, n := range contract.KnowledgeTableNames {
		if n == t {
			return true
		}
	}
	return false
}

// ScopedCount 某租户在某知识表的行数（四表各判一次的 KN3 判据入口）。
// ★ tenant_id 条件硬带在 SQL 里——去掉/绕过它就会把别租户的行数算进来（M0285 反证靶）。
// tid<=0 ⇒ fail-closed 返回 0，不报错、不泄露。
func ScopedCount(conn *sql.DB, d db.Dialect, table string, tid int64) (int, error) {
	if !IsKnowledgeTable(table) {
		return 0, fmt.Errorf("knowledge: 非知识表 %q（作用域查询拒绝）", table)
	}
	if tid <= 0 {
		return 0, nil
	}
	q := "SELECT COUNT(1) FROM " + table + " WHERE tenant_id=?"
	var n int
	if err := db.QueryRow(conn, d, q, tid).Scan(&n); err != nil {
		return 0, fmt.Errorf("knowledge: 作用域计数失败(%s): %w", table, err)
	}
	return n, nil
}
