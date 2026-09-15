// ============ orgmigrate.go · 职责说明 ============
// store 包内部实现文件。
// =============================================
package store

// ============ 本文件职责中文说明 ============
// ★ P0-2 修复（2026-09-15，见《P0P2待办核实报告_20260915.md》）：
// orgs 表「同级同名组织」唯一约束迁移（幂等）。
//
// 背景（缺陷复现链）：
//   - 建表语句（store.go 建表段）仅有普通索引 idx_orgs_tenant(tenant_id, parent_id)，
//     双方言（SQLite/PG）均无 (tenant_id, parent_id, name) 唯一约束；
//   - API 层 handleOrgCreate 依赖 store.IsUniqueViolation(err) 拦截
//     「创建失败：同级下已存在同名组织」（orgs.go），无约束则该分支为死代码；
//   - 实机复现：同一租户根下成功并存两个「T37研发部」（PG 方言）。
//
// 迁移策略（参照 BalanceAccountMigrate 的「先去重后建唯一索引」模式，顺序不可颠倒）：
//   ① 先清理存量脏数据：同 (tenant_id, parent_id, name) 重复行仅保留 id 最小的一行，
//     其余整行删除（组织节点重复属脏数据，不涉及资金；name 归一按 TRIM 口径对齐
//     handleOrgCreate 的 TrimSpace 处理）；
//   ② 再建唯一索引 idx_orgs_sibling_unique：CREATE UNIQUE INDEX IF NOT EXISTS，
//     双方言同一条 DDL（SQLite 3.8+/PG 9.5+ 均支持该语法）；
//   ③ 建索引失败（存量存在 TRIM 归一后仍重复的行）仅记日志不阻断启动——
//     与 OneidMigrate 同样的容错口径，提示人工清理后重启。
// =============================================

import (
	"log"

	"translator/internal/db"
)

// OrgSiblingUniqueMigrate orgs 同级同名唯一约束迁移（幂等；Store.New 迁移链调用）。
// 步骤：① 存量去重（保留每组 id 最小行）→ ② 建唯一索引（双方言同构 DDL）。
func (s *Store) OrgSiblingUniqueMigrate() {
	// ---------- ① 存量去重 ----------
	// 口径：name 先 TRIM 归一（对齐 handleOrgCreate 的 TrimSpace 语义），
	// 同 (tenant_id, parent_id, TRIM(name)) 分组保留 MIN(id)，其余删除。
	// 空名行不参与去重（历史脏行可能是空占位，避免误删用户挂载节点）。
	res, err := db.Exec(s.db, db.CurrentDialect(), `DELETE FROM orgs WHERE id IN (
		SELECT id FROM (
			SELECT o.id, ROW_NUMBER() OVER (
				PARTITION BY o.tenant_id, o.parent_id, TRIM(o.name)
				ORDER BY o.id
			) rn
			FROM orgs o WHERE TRIM(COALESCE(o.name,'')) <> ''
		) t WHERE t.rn > 1)`)
	if err == nil {
		if n, _ := res.RowsAffected(); n > 0 {
			log.Printf("[migrate] orgs: 已清理 %d 行同级同名重复组织（每组保留 id 最小行）", n)
		}
	} else {
		// 去重失败不阻断建索引（可能是旧版 SQLite 无窗口函数；此时索引失败会有下一条日志兜底）
		log.Printf("[migrate] orgs: 同级同名存量去重失败（将继续尝试建唯一索引）: %v", err)
	}
	// ---------- ② 唯一索引 ----------
	// 双方言同一条 DDL：SQLite（表达式 TRIM 需 3.9+，索引函数表达式需确定性与 IMPLICIT_INDEX 语义）
	// 与 PG（函数表达式索引天然支持）均兼容。
	if _, err := db.Exec(s.db, db.CurrentDialect(),
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_orgs_sibling_unique ON orgs(tenant_id, parent_id, TRIM(name))`); err != nil {
		// 失败说明存量仍有归一后重复（去重步骤失败或并发写入所致）：记日志人工清理，不阻断启动
		log.Printf("[migrate] orgs: 同级同名唯一索引创建失败（存量存在归一后重复，请人工清理后重启）: %v", err)
		return
	}
	// ---------- ③ 普通查询索引同步补齐（幂等；老库可能只有 idx_orgs_tenant） ----------
	// 建表语句新增库自带；老库补一次，供组织树按租户+父级查询路径使用。
	if _, err := db.Exec(s.db, db.CurrentDialect(),
		`CREATE INDEX IF NOT EXISTS idx_orgs_tenant ON orgs(tenant_id, parent_id)`); err != nil {
		log.Printf("[migrate] orgs: 租户+父级普通索引补建失败（可忽略）: %v", err)
	}
}
