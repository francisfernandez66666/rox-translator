// ============ 本文件职责中文说明 ============
// ★★ 2026-11-03 复核批新立案：日限额的**量纲混用**（配额闸上的真缺陷）。
//
// 病灶（三条都是实读）：
//
//	· 计数器累加的是**成本**——store/billing.go 的 LogUsage/RecordUsage 走
//	  `incrementDailyUsage(tid, cost)`，`cost` 由 rate_card 的 单价×倍率 算出；
//	· 上限却**回退读字符**——billing_api.go 旧形态在 `max_daily_tokens` 未配置时
//	  直接拿 `perms.MaxDailyChars` 当同一把尺子的另一侧；
//	· 比较处 `used >= maxDaily` 是**裸比**（billing/quota.go 的 CheckDailyQuota）。
//	⇒ 只配了 `max_daily_chars` 的租户，是在拿"成本"比"字符预算"，两个量纲之间隔着
//	  计价系数（每字符 token）与 rate_card 的单价×倍率。后果不是报错，而是**双向误判**：
//	  有的租户被提前拦死（体验受损、像产品坏了），有的被晚拦（成本失控）。
//
// 本批的处理取向（写成注释是为了下一位不再改回猜测形态）：
//  1. `max_daily_tokens` 在位 ⇒ 照旧判定，行为**一字不变**（这条是等值锁的对象）。
//  2. 只有 `max_daily_chars` ⇒ **不参与判定**（回 0＝不限），并出一行 WARN 与一个可数读数。
//     为什么不折算：字符→成本的精确折算必须吃 rate_card 的**现值**，而单价与倍率是运营可配的
//     ⇒ 把折算系数写进代码或写进一次性的迁移，就是在本仓最敏感的配额闸上造出**第二把尺子**
//     （AGENTS §一·11「三把尺子」那一条说的正是这件事——尺子的常数不许再抄第二份）。
//     更要紧的是：折算要选一个模式档（pro K=160／fast K=60），而这一档在建单时才确定；
//     在闸门里替客户选档＝替客户花钱或替客户挡门，两个方向都不该由这里定。
//  3. 失败方向取**放宽**而不是收紧：
//     这一支的两侧代价不对称——误拦＝客户当场可见的故障（"已达到今日用量上限"），
//     而放宽＝少了一道墙，但**余额墙、QPS、并发名额、部门预算墙四条腿都还在**（§5.4 的闸门形状）。
//     把"读不出量纲"当成"额度为 0"是本项目反复出过的"把不确定的事做成拦截"的形态。
//
// 待办（不是遗忘，是要一次现读现算的运营动作）：给仍只配 `max_daily_chars` 的租户补
// `max_daily_tokens`——先只读数列出这批租户与它们近 30 天的真实日成本，再逐户定档。
// 读数与判据见 daily_budget_gate_test.go 的 W4。
// ==========================================
package api

import (
	"context"
	"sync/atomic"

	"translator/internal/observability"
	"translator/internal/tenant"
)

// dailyBudgetFromPerms 把租户权限里的日限额折成**与日计数器同一量纲**的那一侧读数。
// 返回 budget：0＝不限（CheckDailyQuota 对 ≤0 的既有语义就是直接放行，不新造第二个"不限"值）；
// unit 是口径名，给日志与管理台读数用，**不参与判定**。
//
// ★ 这一对返回值是**纯函数**（不写日志、不碰全局态）：闸门与配额读写面两处都要用它，
// 而"出声"只该发生在**判定那一条腿**上（否则运营每打开一次配额面板就刷一条 WARN，
// 而那条 WARN 说的其实是"有人正在被放行"——两件事不同形）。
func dailyBudgetFromPerms(perms *tenant.Perms) (int64, string) {
	if perms.MaxDailyTokens > 0 {
		return perms.MaxDailyTokens, dailyWallUnitCost
	}
	if perms.MaxDailyChars > 0 {
		// 旧字符口径：与计数器的成本口径不同量纲 ⇒ 不参与判定。
		// 这里**不许**悄悄回退成 MaxDailyChars（那正是本缺陷本体，锁 W2 会红）。
		return 0, dailyWallUnitLegacyCharsIgnored
	}
	return 0, dailyWallUnitUnlimited
}

// 日限额生效口径的三个读数名（对外契约：管理台配额面 `daily_wall_unit` 出栈的就是它们，
// 前端按词分派文案，**不许按数字形状猜**——同 §25-b 那条"按形态标记分派、不按关键词猜"）。
const (
	dailyWallUnitCost               = "cost"                 // tokens 档在位，按成本口径拦
	dailyWallUnitLegacyCharsIgnored = "legacy_chars_ignored" // 只配旧字符口径：本档当前不拦，需补 tokens
	dailyWallUnitUnlimited          = "unlimited"            // 两档都没配
)

// dailyCharsLegacyWarned 把闸门那行 WARN 收成"每进程至多一条"——
// 它是配置形状问题，不是每请求事件；逐请求刷屏会把真正要看的日志埋掉
// （同 §一·13 那条"告警去重"的取向）。用原子标记而不是 map，避免加锁与并发 map 写
// （§三 那条"并发回写共享 map 必须先快照"是同一家族的事故）。
var dailyCharsLegacyWarned int32

// noteDailyBudgetUnit 闸门读侧的出声腿：只有真的走到"旧字符口径被忽略"这一档才说一次。
func (s *Server) noteDailyBudgetUnit(unit string) {
	if unit != dailyWallUnitLegacyCharsIgnored {
		return
	}
	if !atomic.CompareAndSwapInt32(&dailyCharsLegacyWarned, 0, 1) {
		return
	}
	observability.Warn(context.Background(),
		"日限额配置是旧字符口径，与日计数器的成本口径不同量纲：本档暂不拦截",
		"key", "max_daily_chars", "需要动作", "给该租户补 max_daily_tokens（现值见管理台租户配额面）")
}
