// ============ kb_lang_migrate.go · 职责说明 ============
// 三期 W4：assist 独立库 kb_entries / scripts / flows 三表补 lang 列（§0.4 第 3 条，断言 A15）。
//
//   - KBLangMigrate() 是 assist 库**自己**的迁移入口，与主库 ProductFactI18nMigrate 挂点不同、
//     绝不写成一个文件：主库走 store.go 域链，assist 库走自己的 migrate/Open 链（由主 agent 挂接）。
//   - 用 db.EnsureColumns 幂等补列（双方言兼容）；存量行经 DEFAULT 'zh' 回填且不覆盖运营改过的 lang。
//     三表共用一张 lang 口径，缺 lang 的 KB 不许跨语种放行（localized_claim_gate 的取段判据）。
//   - ★ 五段系统提示词（persona/tone_rules/promise_rules/现值/能力）全中文的问题，本批只落 lang 列
//     与「按 lang 取译文、取不到回落中文并留痕」的纯函数判据（localized_claim_gate.PickSegmentByLang）；
//     把判据接进 buildSystemPrompt 属主 agent 在共享 engine.go 的挂接，本文件不碰提示词装配。
//
// 只用 internal/observability 的 slog，禁 log.Printf；DB 句柄为 SQLite（assist 独立库）。
// ================================================
package store

import (
	"context"
	"fmt"

	"translator/internal/db"
	"translator/internal/observability"
)

// kbLangTables 需要补 lang 列的三张表（assist 库独立、与主库 product_facts 分开挂）。
var kbLangTables = []string{"kb_entries", "scripts", "flows"}

// KBLangMigrate 给 kb/scripts/flows 三表幂等补 lang 列（存量回填 'zh'，不覆盖已有值）。
// 调用位置：由主 agent 挂进 assist store 的 migrate()/Open() 域链（与主库挂点分离，A15 M0245）。
// 参数：无（方法接收者持有连接）；返回迁移错误。
func (d *DB) KBLangMigrate() error {
	dialect := db.CurrentDialect()
	for _, table := range kbLangTables {
		// EnsureColumns：SQLite 走 PRAGMA 判存在再 ALTER，PG 走 ADD COLUMN IF NOT EXISTS；
		// 重复调用对已有列零动作 ⇒ 幂等、且不覆盖运营把某行 lang 改成非 zh 的手工档。
		if err := db.EnsureColumns(d.sql, dialect, table, map[string]string{
			"lang": "TEXT NOT NULL DEFAULT 'zh'",
		}); err != nil {
			return fmt.Errorf("assist 库 %s 补 lang 列失败: %w", table, err)
		}
	}
	observability.Info(context.Background(), "assist 库 KB/话术/流程 lang 列迁移完成", "tables", len(kbLangTables))
	return nil
}
