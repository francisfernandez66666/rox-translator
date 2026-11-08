// ============ scope.go · 职责说明 ============
// S2 引擎迁入的公共底座：替代 SCRM 那族从 web 上下文取租户的入口
// （EffectiveTenantIDFromGin / failClosedOnMissingTenant / PQ / RQ / DataScope）。
// 引擎层一律收 ctx 与显式 tid；tid<=0 必须 fail-closed 报错，
// ★ 不许依赖能言 API 层 effTenant 的「兜底租户 1」语义——那会把越权读成默认租户，
//
//	正是 AGENTS 反复记的「静默降级把死腿兜住」那一族。
//
// ================================================
package salesflow

import (
	"context"
	"errors"
)

// ErrInvalidTenant 引擎作用域缺少有效租户（tid<=0）；收到即拒绝，不回落默认租户。
var ErrInvalidTenant = errors.New("引擎作用域缺少有效租户")

// Scope 一次引擎调用的作用域：ctx 携带 trace，tenantID 是唯一数据边界。
type Scope struct {
	ctx      context.Context
	tenantID int64
}

// NewScope 构造引擎作用域；tid<=0 一律返回 ErrInvalidTenant（fail-closed）。
// 参数：ctx=请求上下文（nil 时落 context.Background）；tid=显式租户 ID。
// 返回：作用域与错误。
func NewScope(ctx context.Context, tid int64) (Scope, error) {
	if tid <= 0 {
		return Scope{}, ErrInvalidTenant
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return Scope{ctx: ctx, tenantID: tid}, nil
}

// Context 返回作用域上下文（永不为空）。
// 返回：作用域绑定的 context.Context。
func (s Scope) Context() context.Context {
	if s.ctx == nil {
		return context.Background()
	}
	return s.ctx
}

// TenantID 返回作用域租户号。
// 返回：显式传入且已通过 NewScope 校验的租户 ID。
func (s Scope) TenantID() int64 { return s.tenantID }
