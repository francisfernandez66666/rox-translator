// ============ balancehook.go · 职责说明 ============
// store 包内部实现文件（★ P1 多实例闭环 2026-09-15，见《P0P2待办核实报告_20260915.md》）。
//
// 租户余额变动钩子：
//   背景：billing.UsageSink 维护每租户「影子余额」内存缓存（实时中止判定用），
//   余额变动（充值/发放/退款/欠费清零等）后必须让影子失效重 seed，否则出现
//   「已充值仍提示 token 已耗尽」的误中止。此前仅注册发放一处调用失效，
//   支付确认/退款/任务奖励/KB 奖励/包续费等变动点全部遗漏——单实例即有误中止
//   窗口，多实例更会被永久放大。
//
// 本文件提供集中式钩子：store 包在所有双桶余额写点（Charge/chargePermanentTx/
// createQuotaGrantTx/CreateQuotaGrant/RefundOrder/SettleExhausted…）统一调用
// notifyTenantBalanceChanged，由 billing 在 InitGlobalSink 时接线实现
// （清本进程影子 + Redis 广播他实例失效）。
//
// 设计约束：
//   - 钩子为函数变量，仅在启动时（billing.InitGlobalSink）赋值一次，运行期只读——
//     无锁安全；未接线（billing 未初始化/单测）时为 nil，通知静默跳过；
//   - store 不 import billing（依赖方向 billing→store 单向，防环）；
//   - 通知时机在「写点执行处」而非「事务提交后」：极端情况下事务回滚会造成一次
//     多余的影子失效——仅多一次 DB 回读，无正确性风险；漏通知场景由影子 TTL
//     自愈（sink.shadowReseedTTL=5s）兜底。
// =============================================

package store

// OnTenantBalanceChanged 租户双桶余额变动回调（billing.InitGlobalSink 启动期接线）。
// 参数 tid=发生余额变动的租户 ID。nil 表示未接线（静默跳过）。
var OnTenantBalanceChanged func(tid int64)

// notifyTenantBalanceChanged 统一通知入口：余额写点调用（nil 安全）。
// 参数 tid=租户 ID；tid<=0（平台级）无影子概念，直接跳过。
func notifyTenantBalanceChanged(tid int64) {
	if tid <= 0 || OnTenantBalanceChanged == nil {
		return
	}
	OnTenantBalanceChanged(tid)
}
