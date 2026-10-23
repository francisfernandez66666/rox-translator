// ============ scoped_tables.go · 职责说明 ============
// 「删一个租户」要清哪些表 —— **唯一**一份清单（★ 2026-10-23 收敛）。
//
// 为什么清单放在 tenant 包而不是 store 包：
//
//	删除租户原本有两套实现：tenant.Store.Delete（事务内、失败即回滚，用于注册失败后的补偿）
//	与 store.PurgeTenantCascade（逐表删、带回读数，用于超管后台删除）。两套各抄了一份表清单，
//	于是「新增一张带 tenant_id 的表」需要改两个地方 —— 漏一处就留下孤儿行，而且没人会立刻发现。
//	Go 的包依赖方向是 store → tenant（store/packages.go 已 import tenant），tenant 反向 import
//	store 会成环，所以清单只能放在依赖链的**下游**：tenant。两边都从这份读，漂移就消失了。
//
// 清单口径：
//
//	**显式**列全，不在运行时扫 information_schema 动态删。动态删看着省事，但它会把
//	「新加一张带 tenant_id 的表」变成「悄悄多删一张表」—— 删数据这件事上，
//	清单写死、漏了看得见（下一批补），比自动覆盖安全得多。
//	清单来源：`select table_name from information_schema.columns where column_name='tenant_id'`
//	在现网两库（sqlite / postgres）上逐表核对过；删除时用 `IF EXISTS` 语义（失败即记录、继续）。
//
// ⚠️ 棘轮：新增带 tenant_id 列的表时**必须**补进这里，否则删租户会留下孤儿行。
// 已有单测（internal/api/admin_purge_test.go 的 A 段）在删完后逐表读库复验清零。
// =============================================
package tenant

// ScopedTables 租户级联清理的显式表清单（全部带 tenant_id 列）。
//
// 顺序：两库都刻意**没有外键约束**（schema 是 FK-free 的，见 store 包各文件说明），
// 所以顺序不影响正确性；这里按业务域分组排列，方便人工核对「漏了哪一块」。
// `tenants` 行本身**不在**这份清单里 —— 它由调用方在清完数据后单独删（见两处实现）。
var ScopedTables = []string{
	// 组织与账号
	"users",
	"orgs",
	// 计费与账务
	"orders", "payments", "invoices", "usdt_orders", "renewal_attempts",
	"balance_accounts", "quota_grants", "usage_ledger", "usage_daily",
	"coupon_redemptions", "registration_attribution",
	// 套餐与知识库
	"packages", "kb_packages", "kb_entries", "kb_pack_grants",
	"kb_safety_phrases", "kb_staged_entries", "kb_upload_rewards",
	// 工单与产物
	"tickets", "ticket_segments", "ticket_files", "ticket_state", "output_artifacts",
	// 术语库
	"tm_segments", "tm_review", "tm_hit_count", "translation_edits",
	// 会话、评测、反馈
	"chat_conversations", "eval_records", "feedbacks",
	// 任务与激励
	"user_task_claims", "user_task_rewards", "user_trial_grants",
	// 开放能力与集成
	"webhooks", "webhook_deliveries", "scim_config", "s7_watchlist",
	"api_keys", "invite_codes",
	// 通知、告警、后台任务与租户级配置
	"notifications", "alerts", "jobs", "system_config",
	// 审计（放最后：它是「这个租户存在过」的业务侧证据，清完别的再清它）
	"audit_logs",
}
