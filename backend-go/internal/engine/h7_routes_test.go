// ============================================================================
// ★ H7 动态路由打分单测：无样本冷启动=静态权重；错误率/延迟/成本三条惩罚链。
// ============================================================================
package engine

import (
	"testing"
	"time"

	"translator/internal/config"
)

func h7Engine(routes []config.ProviderConfig) *Engine {
	cfg := config.Default()
	config.C.DatabaseDriver = "sqlite" // 防 UAT PG 矩阵 env 泄漏方言（Default 副作用同指针，一并修全局）
	cfg.DynamicRouting = true
	cfg.ModelRoutes = routes
	return &Engine{Cfg: cfg, routeStats: map[string]*routeLatency{}}
}

func h7Feed(e *Engine, base, model string, latMs float64, okRate float64, n int, tok float64) {
	for i := 0; i < n; i++ {
		var err error
		if float64(i)/float64(n) >= okRate {
			err = assertErr{}
		}
		e.observeRoute(base, model, time.Duration(latMs)*time.Millisecond, err)
	}
	e.ObserveRouteTokens(model, int64(tok*float64(n)/2), int64(tok*float64(n)/2))
}

type assertErr struct{}

func (assertErr) Error() string { return "boom" }

func TestH7DynamicColdStartEqualsStatic(t *testing.T) {
	rs := []config.ProviderConfig{
		{APIBase: "a", Model: "m1", Weight: 1},
		{APIBase: "b", Model: "m2", Weight: 9},
	}
	e := h7Engine(rs)
	if got := e.pickPrimaryRoute(); got.Model != "m2" {
		t.Fatalf("冷启动应取最高静态权重: %+v", got)
	}
}

func TestH7ErrorRatePenalty(t *testing.T) {
	rs := []config.ProviderConfig{
		{APIBase: "a", Model: "m1", Weight: 5},
		{APIBase: "b", Model: "m2", Weight: 9},
	}
	e := h7Engine(rs)
	h7Feed(e, "b", "m2", 500, 0.05, 40, 100) // 高权重但 95% 失败
	h7Feed(e, "a", "m1", 520, 0.98, 40, 100) // 健康
	if got := e.pickPrimaryRoute(); got.Model != "m1" {
		t.Fatalf("高错误率路由应被降权: %+v", got)
	}
}

func TestH7LatencyPenalty(t *testing.T) {
	rs := []config.ProviderConfig{
		{APIBase: "a", Model: "m1", Weight: 5},
		{APIBase: "b", Model: "m2", Weight: 9},
	}
	e := h7Engine(rs)
	h7Feed(e, "b", "m2", 9000, 0.98, 40, 100) // 健康但慢 18 倍
	h7Feed(e, "a", "m1", 500, 0.98, 40, 100)
	if got := e.pickPrimaryRoute(); got.Model != "m1" {
		t.Fatalf("慢路由应被降权（速度因子未顶格）: %+v", got)
	}
}

func TestH7TokensAccounting(t *testing.T) {
	e := h7Engine(nil)
	e.observeRoute("a", "m1", time.Second, nil)
	e.ObserveRouteTokens("m1", 100, 50)
	if v := e.routeAvgTokens("a", "m1"); v != 150 {
		t.Fatalf("token 均耗错误: %.1f", v)
	}
	view := e.RouteStats()
	if len(view) != 1 || view[0].TokensPerCall != 150 {
		t.Fatalf("快照错误: %+v", view)
	}
}
