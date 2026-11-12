// ============ sales_role_migrate.go · 职责说明 ============
// 三期 W3 销售角色权限表的**独立迁移入口**：幂等建 sales_role_perms。
// 表是行式三列 (tenant_id, role_level, perm_key, allowed) + updated_at，
// 唯一键前三列天然防重复、逐项可审计、无整行 JSON 并发覆盖（施工图 §3 W3 设计裁定）。
//
// ★ 签名固定为 func (s *Store) SalesRoleMigrate() error；本文件**不挂** New() 域链
//   （store.go 由主 agent 统一装配），故迁移入口自带一次 SCRM 映射自检，
//   保证「表建好了但映射被改坏」不会静默过。
// 双方言：db.ExecDDL 建表 / db.EnsureColumns 幂等补列；observability.Info 留痕，零裸 log.Printf。
// ================================================
package store

import (
	"context"
	"fmt"

	"translator/internal/db"
	"translator/internal/observability"
)

// SalesRoleMigrate 幂等建销售角色权限表；失败即中断并带 SQL 返回，不静默降级。
// 参数：无（方法接收者持有连接）；返回：迁移错误。
func (s *Store) SalesRoleMigrate() error {
	// 建表前先验 SCRM 双读单写映射（A09 不变式），改坏即拒启。
	if err := ValidateSCRMRoleMap(SCRMRoleMap); err != nil {
		return fmt.Errorf("SCRM 角色映射自检失败: %w", err)
	}
	d := db.CurrentDialect()
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS sales_role_perms (
			tenant_id BIGINT NOT NULL,
			role_level INTEGER NOT NULL,
			perm_key TEXT NOT NULL,
			allowed INTEGER NOT NULL DEFAULT 0,
			updated_at TEXT NOT NULL DEFAULT '',
			PRIMARY KEY (tenant_id, role_level, perm_key)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_sales_role_perms_tenant ON sales_role_perms(tenant_id)`,
	}
	for _, stmt := range stmts {
		if err := db.ExecDDL(s.db, d, stmt); err != nil {
			return fmt.Errorf("销售角色权限域建表失败: %w\nSQL: %s", err, stmt)
		}
	}
	// 老库补列（幂等、双方言）：唯一键前三列不变，只补 allowed / updated_at 两列。
	if err := db.EnsureColumns(s.db, d, "sales_role_perms", map[string]string{
		"allowed":    "INTEGER NOT NULL DEFAULT 0",
		"updated_at": "TEXT NOT NULL DEFAULT ''",
	}); err != nil {
		return fmt.Errorf("销售角色权限域补列失败: %w", err)
	}
	observability.Info(context.Background(), "销售角色权限域迁移完成", "statements", len(stmts))
	return nil
}
