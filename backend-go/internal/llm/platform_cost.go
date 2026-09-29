// ============================================================================
// llm/platform_cost.go — 「平台承担成本」上下文标记（★ 2026-09-29 〇-AD）
//
// 背景：实时计费钩子 ChargeUsageRealtime 只看 ctx 上的 tenant id——只要 tid>0 就按
// markup 折算后扣租户积分。而**系统/后台任务**（行业包采集、知识库向量重建）与
// **知识库 Embedding 检索**同样跑在这条钩子上（共用同一个 llm.Client），于是
// 「平台自己该吃的成本」一路扣在客户账上，用量看板里长出一条 99.8% 的
// 「系统/后台任务」行（现网实测 338,544 积分里 337,819 归在这一行）。
//
// 本文件只提供「标记」这一只咽喉，不提供判定逻辑：
//   - 调用方显式 WithPlatformCost(ctx, reason) ⇒ 该 ctx 上后续所有模型用量
//     一律**留痕不扣费**（charge_kind='log'），并把 reason 落进 task_type 供超管侧单列核算；
//   - 没有标记 = 现行扣费行为不变（**默认收费**，新增后台任务不标就照常扣费，
//     宁可多扣不误免——反向的默认会把真实客户消费伪装成平台成本，收入泄漏且无从发现）。
//
// 为什么收在 llm 包而不是 tenant 包：标记的读者是计费钩子、写点是模型调用链，
// 两侧都已 import llm；放 tenant 会让 llm 反向依赖租户上下文包。
// ============================================================================
package llm

import "context"

// platformCostKey ctx 存取键（私有类型防碰撞，与 usageCollectorKey/abortKey 同族）。
type platformCostKey struct{}

// 平台承担成本的用途标签（落 usage_ledger.task_type，超管侧按此单列拆分）。
// 新增标签必须同时出现在用量看板的拆分口径里，否则那部分成本会掉进「其他」无人可见。
const (
	// PlatformKBEmbed 知识库嵌入（检索侧单条/管线预取 + 索引重建）：
	// 对用户免费是已承诺的产品政策（行业包采集向量化同样平台承担）。
	PlatformKBEmbed = "kb_embed"
	// PlatformSystemTask 泛化的系统/后台任务用量。
	PlatformSystemTask = "system_task"
	// PlatformPackScrape 行业包/语言文化包自动采集（LLM 直配文案生产，产出物归平台）。
	PlatformPackScrape = "pack_scrape"
	// PlatformEvals LLM-as-Judge 抽样质检：客户没有下单这一步，
	// 它是平台自己的质量投入，不该出现在客户的积分消耗里。
	PlatformEvals = "evals"
)

// WithPlatformCost 标注「本 ctx 上的模型用量由平台承担」。
// 参数 reason=用途标签（Platform* 常量）；空串收敛为 PlatformSystemTask，
// 避免出现 task_type 为空、超管侧无法归类的孤儿行。
// ctx 为 nil 时原样返回（与 WithUsageCollector 一致的容错口径）。
func WithPlatformCost(ctx context.Context, reason string) context.Context {
	if ctx == nil {
		return nil
	}
	if reason == "" {
		reason = PlatformSystemTask
	}
	return context.WithValue(ctx, platformCostKey{}, reason)
}

// PlatformCostFromCtx 读取平台承担标签；返回 ""（且 ok=false）表示未标注=照常扣费。
// 调用方（计费钩子）必须把「没标注」与「标注了但金额为 0」区分开，故返回二元组。
func PlatformCostFromCtx(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	r, ok := ctx.Value(platformCostKey{}).(string)
	if !ok || r == "" {
		return "", false
	}
	return r, true
}

// selfLedgeredKey ctx 存取键（同上，私有类型防碰撞）。
type selfLedgeredKey struct{}

// WithSelfLedgeredUsage 标注「本 ctx 的平台成本由调用方自己逐租户分摊落账，
// 计费钩子**不要再补一行留痕**」。
//
// 为什么需要这一档（★ 〇-AD 补丁二）：知识库索引重建（engine.RebuildKBIndex）有自己的
// 分摊逻辑——它按「本批 token × 各租户字符占比」算出每个租户该背多少，再走
// LogUsageBatch 落 task_type='kb_embed' 的行。而 EmbedBatch 现在也打了平台标记，
// 同一笔 token 就会被记两次（钩子一行 + 分摊一行），超管看板的「平台承担」直接翻倍。
// 不删分摊那侧（它是租户归因的唯一来源，删了就看不出垫在谁身上），只让钩子在这条
// ctx 上闭嘴——**抑制位只影响留痕，不影响「不扣费」这一判定本身**。
func WithSelfLedgeredUsage(ctx context.Context) context.Context {
	if ctx == nil {
		return nil
	}
	return context.WithValue(ctx, selfLedgeredKey{}, true)
}

// IsSelfLedgeredUsage 调用方是否自备台账（缺省 false＝钩子照常落留痕行）。
func IsSelfLedgeredUsage(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	v, _ := ctx.Value(selfLedgeredKey{}).(bool)
	return v
}
