// ============ product_migrate.go · 职责说明 ============
// 一期《确定的多语言能力》的**现网表补列与产品包唯一索引**，随 Store.New 调用（幂等）。
//
// ★ 三张新表（products / product_facts / product_assets）统一加进 store.go migrate() 的
//
//	stmts（唯一建表执行口 db.ExecDDL），本文件**不再建表**——双入口会让施工者二选一写错。
//
// ★ 新建表不需要 EnsureColumns：SQLite 下 PRAGMA table_info(不存在的表) 返回空集，
//
//	接着 ALTER TABLE <不存在的表> 会直接报错；PG 的 ADD COLUMN IF NOT EXISTS 同样对
//	不存在的表报错。⇒ 补列只服务「表已存在但列还没长出来」的存量库。
//
// ★ 部分唯一索引 ux_pkg_product 双方言**同一条**（现成先例：billing.go:2278、
//
//	task_rewards.go:120、multi_identity_migrate.go:69），不许写「PG 条件索引 / SQLite
//	普通索引＋应用侧查重」两套尺子。WHERE product_id <> '' 让存量空串包不参与约束。
//
// ★ 迁移失败直接返回错误（不静默降级）：缺列会让产品包/留资归属查询在运行时才炸，
//
//	那种「启动看着正常、第一个客户就 500」的坏法比启动失败更难排查。
//
// 唯一迁移入口＝ db.EnsureColumns / db.ExecDDL（db/guard_test.go 的影子 Runner 闸要求）。
// ================================================
package store

import (
	"fmt"

	"translator/internal/db"
)

// ProductMigrate 补 kb_packages.product_id、feedbacks 四列，并建产品包部分唯一索引。
// 调用位置：store.go New() 的域补丁链（migrate() 之后，读路径之前）。
// 参数：无；返回迁移错误。
func (s *Store) ProductMigrate() error {
	d := db.CurrentDialect()

	// ⓪ 产品行的目标对外语言（target_langs）已迁知识侧：由 KnowledgeDomainMigrate 在
	//   「主库仍有 products 表」的存量库上幂等补列（带 tableExists 保护，新库无表直接 nil）。
	//   本函数不再碰 products 表——KN11 后主库零产品表是硬判据。

	// ① 已批准说法复用 kb_packages：产品归属列。kbPkgCols 刻意不带它，89 处 SELECT 零改。
	if err := db.EnsureColumns(s.db, d, "kb_packages", map[string]string{
		"product_id": "TEXT NOT NULL DEFAULT ''",
	}); err != nil {
		return fmt.Errorf("kb_packages 补 product_id 列失败: %w", err)
	}

	// ② 留资存产品上下文（V3）：三段解析 handle → 产品 → 租户，外加来源标记。
	//    四列都可空串，老 feedbacks 行不受影响；lead-product: 前缀的配额记账在 API 层。
	if err := db.EnsureColumns(s.db, d, "feedbacks", map[string]string{
		"product_id":  "TEXT NOT NULL DEFAULT ''",
		"tenant_code": "TEXT NOT NULL DEFAULT ''",
		"page_code":   "TEXT NOT NULL DEFAULT ''",
		"lead_source": "TEXT NOT NULL DEFAULT ''",
	}); err != nil {
		return fmt.Errorf("feedbacks 补产品上下文列失败: %w", err)
	}

	// ③ 同一产品只允许一 source 包 + 一 gate 包；product_id='' 的存量包不参与。
	if err := db.ExecDDL(s.db, d,
		`CREATE UNIQUE INDEX IF NOT EXISTS ux_pkg_product
		 ON kb_packages(product_id, pack_type, role) WHERE product_id <> ''`); err != nil {
		return fmt.Errorf("建 ux_pkg_product 部分唯一索引失败: %w", err)
	}
	return nil
}
