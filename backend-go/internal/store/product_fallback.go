// ============ product_fallback.go · 职责说明 ============
// 四期 W3 / KN11 收口（2026-11-15）：「切读开关 off 时，主库自建产品四表」。
//
// ★ 缺陷本体（审计 P0）：四期把四条 CREATE TABLE 从主库摘走，只在
// internal/knowledge/store 建；而 KNOWLEDGE_SERVICE_READ 默认 off，
// kcCutover() 在 off 档回落主库 SQL ⇒ 全新部署的主库里没有 products 表，
// 产品域第一条查询就是 no such table: products，而**启动链不报错**。
// 同一根因还让 product_knowledge.go 文件头声称的回滚路径
// （「翻回 off＋重建主库表即可」）**从来没有实现过**。
//
// ★ 判据只有一条：kcCutover() 是否为 nil。
//
//	off 档（未注入客户端 / ServiceRead=false）⇒ 主库建表（影子期与回滚档的唯一依靠）；
//	on 档（切读态）        ⇒ **一个 DDL 都不执行**，主库保持零产品表（KN11 终态）。
//
// ★ DDL 文本不在这里：它住在 internal/knowledge/schema（唯一事实源），
// 知识服务独立库与主库 off 档共用同一份 —— 两份各写一遍正是本次事故的根因形态。
// ================================================
package store

import (
	"context"
	"fmt"

	"translator/internal/db"
	"translator/internal/knowledge"
	"translator/internal/knowledge/schema"
	"translator/internal/observability"
)

// productDomainExternal 判「产品域这一回是不是归外部知识服务」。
//
// ★ 判据必须**先看开关、再看客户端**：主服务的装配顺序是 store.New()（跑迁移）
//
//	→ api.NewServer() → SetKnowledgeClient()。也就是说 New() 那一刻客户端**必然还没注入**，
//	若只看 kcCutover()，切读态也会在主库建出四表（KN11 当场失效）。
//	开关是运营的显式决定，与「客户端对象有没有被塞进来」是两件事，前者才是 DDL 归属的依据。
//
// 参数：无；返回 true＝产品表不在主库建。
func (s *Store) productDomainExternal() bool {
	if s.kcCutover() != nil {
		return true
	}
	return knowledge.LoadConfig().ServiceRead
}

// ProductTablesFallback 在「未切读」时于主库幂等建产品四表 + 九索引，并补旧库后补列。
//
// 调用位置：store.go New() 的域补丁链，紧跟 ProductMigrate() 之后。
// 切读态（KNOWLEDGE_SERVICE_READ=on 或客户端已注入且切读）直接返回 nil —— 不得在主库留下任何产品表。
// 参数：无；返回迁移错误（不静默降级：缺表会让产品域在运行时才炸）。
func (s *Store) ProductTablesFallback() error {
	if s.productDomainExternal() {
		// 切读态：产品表归知识服务所有，主库零表是硬判据（KN11）。
		return nil
	}
	d := db.CurrentDialect()
	if err := schema.EnsureProductTables(s.db, d); err != nil {
		return fmt.Errorf("主库兜底建产品四表失败（切读开关 off）: %w", err)
	}
	if err := schema.EnsureLegacyColumns(s.db, d); err != nil {
		return fmt.Errorf("主库兜底补产品后补列失败（切读开关 off）: %w", err)
	}
	// 一行读数：这条腿存在本身就是「切读开关 off」的证据，排障时不必去猜表是谁建的。
	observability.Info(context.Background(), "主库兜底建产品四表完成（KNOWLEDGE_SERVICE_READ 未开）",
		"tables", len(schema.TableDDL), "indexes", len(schema.IndexDDL))
	return nil
}
