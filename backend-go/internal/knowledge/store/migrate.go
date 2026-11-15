// ============ migrate.go · 职责说明 ============
// 四期 W3-2：KnowledgeMigrate() 在**独立 DSN** 上建四张知识表（§4.3.2 / §4.3.3 第 4 条）。
//
// ★ 2026-11-15 收口：四条 CREATE TABLE 与九条索引的**文本已搬到
// internal/knowledge/schema（唯一事实源）**，本文件只做薄委托。
//
//	理由：KNOWLEDGE_SERVICE_READ 默认 off 时主库必须能自建这四表（影子期/回滚档），
//	而「两份 DDL 各写一遍」正是四期出现「新库产品域 500」的根因之一。
//
//	- 知识服务进程（切读态）：本函数建独立库四表；
//	- 主服务 off 档：internal/store/product_fallback.go 调同一个 schema.EnsureProductTables；
//	- 回滚/收尾：schema.DropProductTablesForRollback（只由 cmd/knowledge-migrate 显式触发）。
//
// 方言口径不变：CREATE 写 SQLite 方言，由 db.ExecDDL 经 db.ToDialect 在 PG 下翻译；
// 禁止直写 SERIAL/BIGSERIAL（唯一迁移入口＝ db.ExecDDL / db.EnsureColumns）。
// ================================================
package store

import (
	"database/sql"
	"fmt"

	"translator/internal/db"
	"translator/internal/knowledge/schema"
)

// KnowledgeMigrate 在独立知识库上建四表 + 索引，并对旧库幂等补后补列。
// 参数：conn=知识库连接；d=知识库方言；返回迁移错误。
func KnowledgeMigrate(conn *sql.DB, d db.Dialect) error {
	if conn == nil {
		return fmt.Errorf("knowledge: KnowledgeMigrate 收到 nil 连接")
	}
	if err := schema.Migrate(conn, d); err != nil {
		return fmt.Errorf("knowledge: 四表迁移失败: %w", err)
	}
	return nil
}
