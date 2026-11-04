// ============ 本文件职责中文说明 ============
// ★ 2026-11-04 超时与慢查询批：**只建索引，不改任何查询语义**。
//
// 病灶（四条都是"进后台/翻列表"的必经路，逐条实读）：
//
//	· notifications：`WHERE user_id=? ORDER BY id DESC LIMIT ? OFFSET ?`，而建表只有主键，
//	  user_id **从来没有过索引**（铃铛每 30s 轮询一次，每次全表扫＋深 OFFSET）；
//	· tickets：`WHERE tenant_id=? ORDER BY id DESC LIMIT 200`（＋审批台 `status IN (…)` 同型），
//	  tenant_id / status 两列都没索引；
//	· audit_logs：平台视角是 `LEFT JOIN tenants/users … ORDER BY created_at DESC, id DESC`，
//	  表上只有 `idx_audit_tenant(tenant_id, created_at)` —— 首列是租户，**平台视角用不上**，
//	  于是每次进后台都做一次全表排序；
//	· usage_ledger：`WHERE tenant_id=? ORDER BY id DESC LIMIT ? OFFSET ?` 与同谓词的 COUNT(*)，
//	  现有索引是 (tenant_id, created_at) 与 (tenant_id, user_id[, created_at])，
//	  取"最近 N 条"这条形状仍要回表排序；
//	· api_keys / orders / users 的平台视角：按租户过滤或全表按 (tenant_id,id) 排，同样缺形。
//
// 三条纪律（缺一条就会变成"下一批又退化"或"索引建了没人用"）：
//  1. **建法只走幂等 DDL**（`db.ExecDDL` + `CREATE INDEX IF NOT EXISTS`）；
//     禁止重建版本表 Runner 影子框架（AGENTS §一·4，`internal/db/guard_test.go` 有红灯）。
//  2. **每条索引都必须被点名过射程**——由 indexperf_gate_test.go 钉"建法在位＋列形状对得上
//     那条真实查询"。只建不钉，下一位删掉一条没人知道它护的是哪条查询。
//  3. **DESC 是判据的一部分**：这几条查询全是"按 id/时间倒序取前 N"，
//     建成升序索引时 PG 虽然能反向扫，但 SQLite 的查询计划会实打实排序；
//     两方言都真驱动跑过才算数（AGENTS §一·4：判"某句 SQL 在两种方言都能跑"靠真驱动，不靠语法直觉）。
//
// ⚠️ 现网执行影响（属运维动作，不是代码缺口）：生产 PG 上对 audit_logs / usage_ledger / tickets
//
//	这类几十万次写过的表建索引会占一段写锁——本批**不用** `CONCURRENTLY`（它不能在事务里、
//	SQLite 无此语法，会造出方言特例），改为：先在演示站计时 → 低峰窗口上主站 →
//	跑后按 `pg_indexes` / `PRAGMA index_list` 逐站回验建成。
//
// ============================================
package store

import (
	"context"

	"translator/internal/db"
	"translator/internal/observability"
)

// indexperfStatements 要建的索引清单（唯一事实源：迁移与闸门锁都从这张表读，禁止两处各写一份）。
//
// `why` 字段是给人看的——它写的是"哪条真实查询的哪个形状靠它"，
// 而不是"性能优化"这种无法核对的话（同 §一·14 那条「注释点名的锚点必须存在」）。
type indexSpec struct {
	name string
	ddl  string
	why  string
}

// indexperfStatements 本批要建的取活索引清单（**唯一写点**）。
// 一份清单罩七张表：通知／工单／审计／用量流水／API Key／订单／用户。
// 新增索引只能往这里加一条（附 why＝"哪条查询的什么形状需要它"）——
// 迁移函数体与门禁闸门（indexperf_gate_test.go 的 P1）都从这一份现读，
// 别处再写一条 CREATE INDEX 就会被 P1 判成"清单长出第二处写点"。
var indexperfStatements = []indexSpec{
	{
		name: "idx_notifications_user_id",
		ddl:  `CREATE INDEX IF NOT EXISTS idx_notifications_user_id ON notifications(user_id, id DESC)`,
		why:  "ListNotifications: WHERE user_id=? ORDER BY id DESC LIMIT ? OFFSET ?",
	},
	{
		name: "idx_tickets_tenant_id",
		ddl:  `CREATE INDEX IF NOT EXISTS idx_tickets_tenant_id ON tickets(tenant_id, id DESC)`,
		why:  "ListTickets: WHERE tenant_id=? ORDER BY id DESC LIMIT 200",
	},
	{
		name: "idx_tickets_status_id",
		ddl:  `CREATE INDEX IF NOT EXISTS idx_tickets_status_id ON tickets(status, id DESC)`,
		why:  "审批台列表: WHERE status IN (…) ORDER BY id DESC LIMIT 200",
	},
	{
		name: "idx_audit_created_id",
		ddl:  `CREATE INDEX IF NOT EXISTS idx_audit_created_id ON audit_logs(created_at DESC, id DESC)`,
		why:  "ListAuditFilter 平台视角(tid=0)：无租户谓词＋ORDER BY created_at DESC, id DESC（旧索引首列是 tenant_id，够不到）",
	},
	{
		name: "idx_audit_action",
		ddl:  `CREATE INDEX IF NOT EXISTS idx_audit_action ON audit_logs(action, id DESC)`,
		why:  "审计按动作过滤（含管理台导出与 UAT 取证的 action 维度）",
	},
	{
		name: "idx_ul_tenant_id",
		ddl:  `CREATE INDEX IF NOT EXISTS idx_ul_tenant_id ON usage_ledger(tenant_id, id DESC)`,
		why:  "UsageLedgerList: WHERE tenant_id=? ORDER BY id DESC LIMIT ? OFFSET ?（现有两条索引都不含 id 序）",
	},
	{
		name: "idx_ul_tenant_created",
		ddl:  `CREATE INDEX IF NOT EXISTS idx_ul_tenant_created ON usage_ledger(tenant_id, created_at)`,
		why:  "UsageTrend 与日计数回退腿的时间范围比较（created_at 是 TEXT，字典序对 RFC3339 成立）",
	},
	{
		// ★ A8（2026-11-04）补的第二条 usage_ledger 索引：MyLedgerPage 的「流水总笔数」是
		// COUNT(*) WHERE tenant_id=? AND user_id=? AND 客户面谓词——上面那条 (tenant_id,id DESC)
		// 罩不住 user_id，count 仍扫整个租户的账页。客户面「我的流水」每翻一页都跑一次它，
		// 是钱的面上的常驻开销。
		// ⚠️ 方案 §2.8 第 5 条「total 只在第一页取、翻页复用」**刻意不采纳**并登记理由：
		// 那会让第 2 页之后的 total 变 0 或陈旧值——客户对账时读到的是"总数会跳"的数，
		// §2.0 纪律 3「钱的语义不许顺手优化」优先于省一次 count。补索引才是这个方向的正解。
		name: "idx_ul_tenant_user",
		ddl:  `CREATE INDEX IF NOT EXISTS idx_ul_tenant_user ON usage_ledger(tenant_id, user_id)`,
		why:  "MyLedgerPage 的 COUNT(*)（WHERE tenant_id+user_id+客户面谓词），客户面每翻一页都跑一次",
	},
	{
		name: "idx_apikeys_tenant_id",
		ddl:  `CREATE INDEX IF NOT EXISTS idx_apikeys_tenant_id ON api_keys(tenant_id, id DESC)`,
		why:  "ListAPIKeys: WHERE tenant_id=? ORDER BY id DESC（api_keys 过去只有 key_hash 一条索引）",
	},
	{
		name: "idx_orders_status_pending",
		ddl:  `CREATE INDEX IF NOT EXISTS idx_orders_status_pending ON orders(status, manual_confirm, id DESC)`,
		why:  "ListManualConfirmOrders: WHERE channel IN (…) AND manual_confirm=1 AND status='pending'（三个谓词列此前全裸）",
	},
	{
		name: "idx_users_tenant_id",
		ddl:  `CREATE INDEX IF NOT EXISTS idx_users_tenant_id ON users(tenant_id, id)`,
		why:  "ListAllUsers 平台视图 ORDER BY tenant_id, id（超管进用户列表那条，此前无任何可用索引）",
	},
	// ⚠️ tm_segments 的租户索引**不在这张表里**：那张表由 internal/kb 建，
	//    挂在 store 清单里会得到一条"建失败但不报错"的装饰索引（首跑实测被抓）。
	//    它建在 internal/kb/db.go 的同一批索引后面，理由写在那里。
}

// IndexPerfMigrate 幂等建索引（★ 挂进 store.go 的迁移链尾部）。
//
// 失败口径：**单条失败不中断整批**，但要出声并计数——
// 建索引失败最常见的成因是"列名对不上"（列被改名/老库没这列），
// 那种情况下相关查询继续全表扫，而屏幕上没有任何症状（本仓「有自动降级的链路，能打开不算验收」那一族）。
// 因此这里把成功/失败数打进日志，并在 indexperf_gate_test.go 里按**全集**核对清单，
// 保证"少建一条"这件事在闸门上现形，而不是在客户的等待时间里现形。
func (s *Store) IndexPerfMigrate() {
	ctx := context.Background() // 迁移链在请求之外，与 MultiIdentityMigrate 同一口径
	d := db.CurrentDialect()
	ok, failed := 0, 0
	for _, spec := range indexperfStatements {
		if err := db.ExecDDL(s.db, d, spec.ddl); err != nil {
			failed++
			observability.Warn(ctx, "索引迁移失败（该查询将继续走全表扫，请核列名与老库形态）",
				"index", spec.name, "查询", spec.why, "error", err.Error())
			continue
		}
		ok++
	}
	if failed > 0 {
		observability.Warn(ctx, "索引迁移未全部建成", "建成", ok, "失败", failed, "总数", len(indexperfStatements))
		return
	}
	observability.Info(ctx, "索引迁移完成（超时与慢查询批）", "总数", ok)
}

// IndexPerfSpecs 把清单暴露给闸门与只读诊断（闸门按它逐条核"建法在位"）。
// ⚠️ 返回的是**同一份**包内切片的浅拷贝：清单只有一处，禁止在测试里再抄一份索引名列表。
func IndexPerfSpecs() []indexSpec { return append([]indexSpec(nil), indexperfStatements...) }
