// ============ file_token_budget.go · 职责说明 ============
// ★ N-01（2026-10-25 对账补腿）：单工单**累计 token** 成本闸门——修法第 3 条里的第二条
//
//	「单工单累计 token 超过估算的 N 倍 → 停止重试、按现有降级链交付并出声」。
//
// 现网实证（《修改文档_UAT云端E2E核实与修法_20261024》§N-01 账本）：
// 0.2KB／4 行文件工单 115 实扣 **1,037 积分**（公开预估约 44 积分，约 24 倍），
// 根因是硬闸轮次（事故当时 5 轮；★ 2026-10-28 计价批已收敛为 3 轮）＋补漏逐段兜底
// **每轮都是整块重发**，而链路只有一把墙钟预算（FILE_HARDGATE_MAX_SEC，默认 600s）——
// **时间预算拦不住 token 爆炸**：一次调用再贵也只花几秒，5 轮 × 逐段兜底能在 600s 内烧掉二十次整块调用。
// ⚠️ 轮次降到 3 只是把爆炸的台阶压低一档，**这条 token 闸仍然是必需的**：
// 逐段兜底与逐行补译各自还能把同一篇重发很多遍，与轮次无关（本批真踩过的放大形态都在那一侧）。
//
// 三条设计取向（改动这块前必读）：
//
//	① 基准取**首翻实际用量**，不取「按源文字数估的公式值」。
//	   文件链是逐段/逐批调用的，整篇公式（EstimateChatTokens）会**结构性低估**它
//	   （每批都重发一遍指令），拿它当预算会把正常单子拦腰截断；
//	   首翻实测值天然按真实结构、真实语种数、真实提示词长度算，**不存在第二把尺子**。
//	② 只约束**重试/补译腿**，绝不约束首翻本身——首翻被拦＝客户拿不到译文，
//	   成本失控的代价不许转嫁给交付质量（AGENTS §一·10「新增防护不拿可用性交押金」）。
//	③ 无计量口径 ⇒ **fail-open 不判**（返回 -1）。拿不到用量读数时「不知道花了多少」
//	   不等于「花超了」；把一次收集器缺失打成"停止补译"＝把观测腿故障变成产品缺陷。
//	   与 llmsource「库读不到就沿用上一份快照」、防薅计数「故障放行」同一条方向。
//
// 停止时**必须出声**两处：一行 WARN（运维排障）＋一条 gateWarning（进审批台与用户面，
// 由调用方负责并入），因为「按现有降级链交付」在界面上与"全部译出"长得一样。
// =========================================================
package engine

import (
	"context"
	"os"
	"strconv"
	"strings"

	"translator/internal/llm"
	"translator/internal/observability"
)

// DefaultRepairTokenBudgetMultiple 补译/重试腿的 token 预算倍数默认值（相对首翻实际用量）。
// 取 4 的依据：UAT 那一单首翻 2 行 × 约 2.06 万 token ≈ 4.1 万，
// 而硬闸 5 轮（当时上限）+ 逐段兜底实际烧到约 41 万（首翻的 10 倍）——4 倍这条线能在爆炸段
// 拦下其中约九成，同时给正常单子留足余量（现网补译轮的**正常**放大在 1~2 倍之间）。
const DefaultRepairTokenBudgetMultiple = 4

// repairTokenBudgetMultiple 现读倍数：env > system_config > 默认（与 fileRepairMaxCalls 同口径）。
// 0/负数/脏值一律折回默认——倍数配成 0 等于"一次补译都不许跑"，属误配不是意图。
func (e *Engine) repairTokenBudgetMultiple() int64 {
	if v := strings.TrimSpace(os.Getenv("FILE_REPAIR_TOKEN_BUDGET_MULTIPLE")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return int64(n)
		}
	}
	if e != nil && e.St != nil {
		if v, _ := e.St.GetConfig("file_repair_token_budget_multiple"); strings.TrimSpace(v) != "" {
			if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
				return int64(n)
			}
		}
	}
	return DefaultRepairTokenBudgetMultiple
}

// collectorTokens 读本次工单已累计的真实 token（输入+输出）。
// 第二返回值 false＝ctx 里没有用量收集器（未走 WithUsageRecorder 的调用面），
// 调用方据此区分「没花钱」与「没有计量口径可用」，两者绝不混同（同 UsageCollector.BilledTotal 的 -1 口径）。
func collectorTokens(ctx context.Context) (int64, bool) {
	uc := llm.CollectorFrom(ctx)
	if uc == nil {
		return 0, false
	}
	return uc.Total(), true
}

// repairTokenBudgetCap 由首翻实际用量算出补译/重试腿的累计 token 上限。
// 参数 ctx=工单上下文（倍数从这里读不到，仅为与调用点保持同一条链的签名），
// firstPassTokens=首翻（含 KB/批量/校对腿）结束时的累计真实 token。
// 返回 -1＝不启用（无口径可用），其余值为「累计用量达到即停止追加调用」的线。
// 下限取 firstPassTokens 本身：倍数被配成小于 1 时也不许把补译压成零机会。
func (e *Engine) repairTokenBudgetCap(ctx context.Context, firstPassTokens int64) int64 {
	if _, ok := collectorTokens(ctx); !ok {
		return -1 // fail-open：观测腿缺失不该摘掉客户的补译机会
	}
	if firstPassTokens <= 0 {
		// 首翻零用量＝全部命中缓存/术语直出。此时**追加的补译调用本身就是新成本**，
		// 拿 0 乘倍数会恒判超限、把一次正常的缓存未命中打成"预算耗尽"。
		// 取向：给一档绝对余量（一档默认文化块上限下的一次整块调用），只拦真正的失控重复。
		return defaultSingleCallTokensFloor
	}
	m := e.repairTokenBudgetMultiple()
	limit := firstPassTokens * m
	if limit < firstPassTokens {
		limit = firstPassTokens // 倍数被配成 0/负数已在取数处折回默认，这里只兜溢出
	}
	return limit
}

// defaultSingleCallTokensFloor 首翻零用量时的绝对余量档（token）。
// 依据：文化块按上限 2,000 字符封顶后，一次整块专业档调用的输入约数千 token；
// 给 20,000 相当于允许追加数次整块补译——足够救回零星缺失段，又远小于 UAT 那单的 41 万爆炸量级。
const defaultSingleCallTokensFloor int64 = 20000

// repairBudgetSpent 判断补译/重试腿是否已越过成本预算线。
// 参数 ctx=工单上下文，budget=repairTokenBudgetCap 算出的上限（<0＝不启用）。
// 返回 (是否超限, 当前累计用量)——读数一并返回，供出声时带出「花到哪一线」。
// 判据用 `>=`（到线即停），且 budget<0 一律放行：不启用与未超限在行为上必须同形。
func (e *Engine) repairBudgetSpent(ctx context.Context, budget int64) (bool, int64) {
	if budget < 0 {
		return false, 0
	}
	spent, ok := collectorTokens(ctx)
	if !ok {
		return false, 0 // 收集器中途消失（理论上不会）：同 fail-open，不拦交付
	}
	return spent >= budget, spent
}

// warnRepairBudgetStopped 预算越线时的出声腿：一行 WARN（运维）＋一句 gateWarning 文案（客户/审批面）。
// 参数 lc=目标语言，spent/budget=读数，remaining=仍缺失的段数。返回可直接进 gateWarnings 的一句文案。
// 日志**只出读数不出原文**（工单内容属客户资料，同 file.go 极简兜底腿那条纪律）。
func (e *Engine) warnRepairBudgetStopped(ctx context.Context, lc string, spent, budget int64, remaining int) string {
	observability.Warn(ctx, "file_repair_token_budget_stopped",
		"lang", lc, "spent_tokens", spent, "budget_tokens", budget, "remaining_segments", remaining)
	return "语言 " + lc + " 的剩余 " + strconv.FormatInt(int64(remaining), 10) +
		" 段未补译：本单累计 token 用量已达成本预算上限（" +
		strconv.FormatInt(spent, 10) + "/" + strconv.FormatInt(budget, 10) + " token），" +
		"系统按成本护栏停止自动补译并交付现有译文，请人工检查缺失段。"
}
