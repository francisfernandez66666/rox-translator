// ============ sales_facts_guard.go · 职责说明 ============
// 销售事实集的 **assist 侧薄适配**（三期 W2，§0.4 三段链路第②段）。
//
// 本文件只做一件事：把主服务签名下发的事实集放进本轮 ctx，供 W4 的出站守卫
// （sales_claim_guard.go）消费。事实集的结构 / 编解码 / 验签判据都在 internal/engine/sales_facts.go
// （两侧共用、无 store 依赖），assist 这里**不抄第二份**。
//
// 为什么单独留这个 seam：W4 的 guardSalesClaims 挂在 Respond 咽喉，吃的就是
// engine.SalesFactsFromContext(ctx)；本适配层保证「验签通过的事实集」与「守卫消费的 ctx」
// 之间只有一跳，且无头/验签失败时 ctx 里什么都没有 ⇒ 守卫按「无事实头」fail-closed 回避。
// ================================================
package engine

import (
	"context"

	mainengine "translator/internal/engine"
)

// InjectSalesFacts 把已验签的事实集注入本轮接待 ctx（薄委托，唯一注入点）。
// 参数 ctx=上下文；set=已验签事实集；返回派生 ctx。
func InjectSalesFacts(ctx context.Context, set mainengine.SalesFactsSet) context.Context {
	return mainengine.WithSalesFacts(ctx, set)
}
