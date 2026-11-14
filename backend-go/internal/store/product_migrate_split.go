// ============ product_migrate_split.go · 职责说明 ============
// 四期 W3（§4.3.3）：ProductMigrate 三域拆出的「销售域」与「知识域」独立函数。
//
//   - SalesDomainMigrate：feedbacks 四列（product_id/tenant_code/page_code/lead_source），
//     feedbacks 仍在主库，挂 store.New() 启动链。
//   - KnowledgeDomainMigrate：products.target_langs 补列（知识域）。产品四表已迁去
//     独立知识库，主库默认无 products 表；本函数供迁移/回滚期对「存量同构库」显式调用，
//     用 tableExists 做存在性保护：表不在就直接返回 nil。
//     ⇒ 既不会因「对不存在的表 EnsureColumns」让主服务启动即失败（§4.3.3「不拆就起不来」），
//       也不会在空主库里建出知识表（KN11）。
//     ⚠️ 它**不挂** store.New() 启动链；新库的目标语言列由知识侧 CREATE TABLE 直接带。
//
// ★ 唯一迁移入口＝ db.EnsureColumns / db.ExecDDL；双方言兼容。
// ================================================
package store

import (
	"database/sql"
	"fmt"

	"translator/internal/db"
)

// SalesDomainMigrate 销售域补列：feedbacks 四列（留资产品上下文，V3）。
// 四列都可空串，老 feedbacks 行不受影响；失败直接返回错误。
func (s *Store) SalesDomainMigrate() error {
	if err := db.EnsureColumns(s.db, db.CurrentDialect(), "feedbacks", map[string]string{
		"product_id":  "TEXT NOT NULL DEFAULT ''",
		"tenant_code": "TEXT NOT NULL DEFAULT ''",
		"page_code":   "TEXT NOT NULL DEFAULT ''",
		"lead_source": "TEXT NOT NULL DEFAULT ''",
	}); err != nil {
		return fmt.Errorf("feedbacks 补产品上下文列失败: %w", err)
	}
	return nil
}

// KnowledgeDomainMigrate 知识域补列：products.target_langs（目标对外语言）。
// ★ 表不存在（W3 起主库常态）直接返回 nil；只有存量同构库显式调用时才补列。
//   「存量行补列后是空串＝还没表态」，读侧按「未设定」处理，不许当成「只支持中文」。
// 本函数不挂 store.New()：主库零知识表是 KN11 的硬判据。
func (s *Store) KnowledgeDomainMigrate() error {
	d := db.CurrentDialect()
	exists, err := tableExists(s.db, d, "products")
	if err != nil {
		return fmt.Errorf("检查 products 表是否存在失败: %w", err)
	}
	if !exists {
		return nil // 主库无知识表（KN11），知识侧 KnowledgeMigrate 已自带该列
	}
	if err := db.EnsureColumns(s.db, d, "products", map[string]string{
		"target_langs": "TEXT NOT NULL DEFAULT ''",
	}); err != nil {
		return fmt.Errorf("products 补 target_langs 列失败: %w", err)
	}
	return nil
}

// tableExists 按方言判断表是否存在（SQLite: sqlite_master；PG: information_schema.tables）。
// 供 KnowledgeDomainMigrate 在主库可能没有知识表时做存在性保护（不建表、不报错）。
func tableExists(conn *sql.DB, d db.Dialect, table string) (bool, error) {
	var q string
	if d == db.DialectPostgres {
		q = "SELECT COUNT(1) FROM information_schema.tables WHERE table_schema='public' AND table_name=?"
	} else {
		q = "SELECT COUNT(1) FROM sqlite_master WHERE type='table' AND name=?"
	}
	var n int
	if err := db.QueryRow(conn, d, q, table).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}
