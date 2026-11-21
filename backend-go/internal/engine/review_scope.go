// ============ review_scope.go · 职责说明 ============
// 复核链路（复核模型＋复核 evals）的运营开关解析。
//
// 背景（★ 2026-11-21 用户令）：复核腿是「译得出来的那个模型有没有被问到」的唯一通道，
// 但它每轮都要白烧一次模型调用。运营需要能**分档**控制：
//   - 即时翻译（高频短文本）——降本时先关这一档；
//   - 翻译工单（客户付费的成品）——提质时留这一档。
//
// 两档拆成两个独立指针布尔，落在 ops_policy 的 quality 段（平台级、仅超管可写）。
//
// 作用域判定只有一处：**由入口在 ctx 上打标**（翻译工单在 service/ticket.go 打 ticket，
// 其余一律默认 instant）。这样引擎里 6+ 个复核调用点不必各自注入标记——漏一处就是
// 「关了还在跑」或「开着却不跑」的静默形态，而屏幕上没有任何读数会指出这件事。
//
// 读不到策略（无 Store／库读失败／键不存在）一律按**开**处理：宁可多花一次模型调用，
// 也不因为一次库抖动把全站交付质量悄悄降一档（AGENTS §一·3 的同一取向）。
// ============================================
package engine

import (
	"context"
	"sync"

	"translator/internal/ops"
)

// 复核链路作用域（ctx 标记的取值，只此两种）。
const (
	// ReviewScopeInstant 即时翻译（/api/translate、/api/translate/file 等直接调用）——缺省档。
	ReviewScopeInstant = "instant"
	// ReviewScopeTicket 翻译工单（文本工单流水线与文件工单）——由 service/ticket.go 显式打标。
	ReviewScopeTicket = "ticket"
)

// reviewScopeKey ctx 键：复核链路作用域。
type reviewScopeKey struct{}

// WithReviewScope 在 ctx 上标记复核链路作用域（只认 ticket，其余归一成 instant）。
func WithReviewScope(ctx context.Context, scope string) context.Context {
	if scope != ReviewScopeTicket {
		scope = ReviewScopeInstant
	}
	return context.WithValue(ctx, reviewScopeKey{}, scope)
}

// reviewScopeFrom 读 ctx 上的作用域标记；缺省（含 nil ctx）＝即时翻译。
func reviewScopeFrom(ctx context.Context) string {
	if ctx == nil {
		return ReviewScopeInstant
	}
	if v, ok := ctx.Value(reviewScopeKey{}).(string); ok && v == ReviewScopeTicket {
		return ReviewScopeTicket
	}
	return ReviewScopeInstant
}

// ReviewEnabled 读运营策略里的复核链路档位（按 ctx 作用域分档）。
// 导出给 orchestrator 的工单流水线直接调用（它不在 HandleText/HandleFile 的调用链里）。
func (e *Engine) ReviewEnabled(ctx context.Context) bool {
	if e == nil {
		return true
	}
	q := qualityOfCached(e.opsPolicyRaw())
	if reviewScopeFrom(ctx) == ReviewScopeTicket {
		return q.TicketReviewEnabled
	}
	return q.InstantReviewEnabled
}

// opsPolicyRaw 读平台 ops_policy 原文；读不到返回空串（QualityOf 会回落出厂默认）。
func (e *Engine) opsPolicyRaw() string {
	if e == nil || e.St == nil {
		return ""
	}
	v, err := e.St.GetConfig("ops_policy")
	if err != nil {
		return ""
	}
	return v
}

// qualityParseCache 解析结果缓存（键＝ops_policy 原文）。
//
// 为什么需要：ReviewEnabled 在文件链的逐段/逐语种循环里被调用（一份 200 行 × 10 语种的
// 工单是数千次），每次都 json.Unmarshal 一遍整段策略是纯浪费；而 ops_policy 原文一变，
// 键就变，缓存自然失效——不需要任何显式失效钩子（少一个钩子就少一处漏挂的可能）。
// 只缓存「原文 → 两个布尔」，不缓存时间/窗口类因子（那些本来就该现读）。
var qualityParseCache struct {
	mu   sync.Mutex
	raw  string
	val  ops.QualityEffective
	seen bool
}

// qualityOfCached 带缓存地解析 ops_policy 的复核链路档位。
func qualityOfCached(raw string) ops.QualityEffective {
	qualityParseCache.mu.Lock()
	if qualityParseCache.seen && qualityParseCache.raw == raw {
		v := qualityParseCache.val
		qualityParseCache.mu.Unlock()
		return v
	}
	qualityParseCache.mu.Unlock()

	v := ops.QualityOf(raw)

	qualityParseCache.mu.Lock()
	qualityParseCache.raw, qualityParseCache.val, qualityParseCache.seen = raw, v, true
	qualityParseCache.mu.Unlock()
	return v
}
