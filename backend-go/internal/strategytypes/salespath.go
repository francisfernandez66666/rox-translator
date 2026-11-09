// ============ salespath.go · 职责说明 ============
// 二期六包平移第三刀：销售路径决策的 context 载体（flow 写、llm 读，两侧只依赖本包）。
// ★ 迁入即换：外仓字段里的中文阶段名改成稳定编码（文案在词典层），
// 决策本身是只读判定，不携带任何「改客户阶段」的指令。
// 开关关闭/失败路径下 ctx 不带值，下游输出逐字节等价（零行为变化）。
// ================================================
package strategytypes

import "context"

// salesPathCtxKey 私有 context 键类型，防止与别的包键碰撞。
type salesPathCtxKey struct{}

// SalesPathDecision 销售路径机每轮输出（全部为稳定编码，不写行业文案）。
type SalesPathDecision struct {
	// CurrentStage 当前阶段码。
	CurrentStage string
	// TargetStage 下一个待推进的阶段码。
	TargetStage string
	// ConversionSignal 该边绑定的真实信号名（lead_captured/human_assigned 等）。
	ConversionSignal string
	// NextBestAction 本轮建议动作（供提示词消费）。
	NextBestAction string
	// HookedRecently 近窗内是否有接钩记录。
	HookedRecently bool
	// PromptDirective 组装好的注入段（开关关闭时整条链路为 nil）。
	PromptDirective string
}

// WithSalesPath 把路径决策挂到 ctx 上；decision 为 nil 或 ctx 为 nil 时原样返回。
// 参数：ctx=原上下文；decision=只读决策；返回：挂载后的上下文。
func WithSalesPath(ctx context.Context, decision *SalesPathDecision) context.Context {
	if ctx == nil || decision == nil {
		return ctx
	}
	return context.WithValue(ctx, salesPathCtxKey{}, decision)
}

// SalesPathFromContext 读取 ctx 上的路径决策；未挂载返回 nil（消费方按现状走）。
// 参数：ctx=上下文；返回：只读决策或 nil。
func SalesPathFromContext(ctx context.Context) *SalesPathDecision {
	if ctx == nil {
		return nil
	}
	d, _ := ctx.Value(salesPathCtxKey{}).(*SalesPathDecision)
	return d
}
