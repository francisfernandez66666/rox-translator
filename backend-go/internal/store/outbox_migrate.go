// ============ outbox_migrate.go · 职责说明 ============
// 四期 W4 outbox 域独立迁移入口（store 冻结纪律：新方法按域新建文件，不往 store.go 追加）。
// ★ 已挂进 store.go 的 New() 迁移链（store.go:92）；本文件可单独调用、单独验证。
//
// 薄委托到 internal/outbox.Migrate：DDL 唯一入口仍是 db.ExecDDL / db.EnsureColumns，
// store 侧不复制第二份建表真源（AGENTS §一·1）。
// ================================================
package store

import (
	"fmt"

	"translator/internal/db"
	"translator/internal/outbox"
)

// OutboxMigrate 幂等建 event_outbox / event_inbox / lead_captured_inbox 三表与唯一键；
// 失败即带上下文上抛（fail-fast 启动链，缺表不许静默降级到运行期才炸）。
// 参数：无（方法接收者持有连接）；返回：迁移错误。
func (s *Store) OutboxMigrate() error {
	if err := outbox.Migrate(s.db, db.CurrentDialect()); err != nil {
		return fmt.Errorf("OutboxMigrate: %w", err)
	}
	return nil
}
