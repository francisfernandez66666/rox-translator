// ============================================================================
// llm/free_instant.go — 「本次即时翻译走免费档」上下文标记（★ 2026-10-30 三事合一批 组4，A-1）
//
// 为什么用 ctx 标记而不是模式级 `mode_rules.<mode>.charge=false`：模式（fast/pro）是**翻译模式**，
// 文件工单与即时翻译共用同一套模式；把 charge 关掉会把工单一起免掉（收入面塌一半）。
// 本标记只在即时翻译那四个入口装载，出口仍然是**那一个** `ChargeUsageRealtime`——
// 不新造第二套扣费逻辑（§一·11 多把尺子那一族）。
//
// 形制照 platform_cost.go：私有 key 类型、空 ctx 原样返回、不做判定——判定在 api/instant_free.go。
// ============================================================================
package llm

import "context"

// freeInstantKey ctx 存取键（私有类型防碰撞）。
type freeInstantKey struct{}

// WithFreeInstant 标注「本 ctx 上的模型用量走免费档」：不扣余额、不写收集器、
// 落一条 task_type=instant_free 的留痕行（看得见成本、找不到钱）。
func WithFreeInstant(ctx context.Context) context.Context {
	if ctx == nil {
		return nil
	}
	return context.WithValue(ctx, freeInstantKey{}, true)
}

// FreeInstantFromCtx 本 ctx 是否标了免费档。
func FreeInstantFromCtx(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	v, _ := ctx.Value(freeInstantKey{}).(bool)
	return v
}
