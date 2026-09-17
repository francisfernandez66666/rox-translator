// ============================================================================
// pickroute_test.go — 主路由选择单测（改造 3，2026-09-17）：
//   - 静态分支：全 0 权重取首个、最高权重胜出（h7 此前仅覆盖动态打分函数）
//   - 动态/静态开关切换：DynamicRouting 开启时统计惩罚生效、关闭时纯静态
//   - 降级链：排除主路由 + 权重降序（3 条路由排序口径）
//
// ============================================================================
package engine

import (
	"testing"
	"time"

	"translator/internal/config"
)

// TestPickPrimaryStaticAllZeroWeights 全 0 权重 → 取配置顺序第一个。
func TestPickPrimaryStaticAllZeroWeights(t *testing.T) {
	restore := pinDynamicRouting(t, false)
	defer restore()
	e := &Engine{Cfg: config.Default()}
	e.Cfg.ModelRoutes = []config.ProviderConfig{
		{Provider: "a", APIBase: "https://a/v1", Model: "ma", Weight: 0},
		{Provider: "b", APIBase: "https://b/v1", Model: "mb", Weight: 0},
	}
	got := e.pickPrimaryRoute()
	if got.Model != "ma" {
		t.Fatalf("全 0 权重应取首个，实际: %+v", got)
	}
}

// TestPickPrimaryStaticHighestWeight 静态模式取权重最高者（并列取靠前）。
func TestPickPrimaryStaticHighestWeight(t *testing.T) {
	restore := pinDynamicRouting(t, false)
	defer restore()
	e := &Engine{Cfg: config.Default()}
	e.Cfg.ModelRoutes = []config.ProviderConfig{
		{Provider: "a", APIBase: "https://a/v1", Model: "ma", Weight: 1},
		{Provider: "b", APIBase: "https://b/v1", Model: "mb", Weight: 5},
		{Provider: "c", APIBase: "https://c/v1", Model: "mc", Weight: 5},
	}
	got := e.pickPrimaryRoute()
	if got.Model != "mb" {
		t.Fatalf("权重并列应取靠前者 mb，实际: %+v", got)
	}
}

// TestPickPrimaryDynamicToggle 动态模式按健康度惩罚选路；关闭后回纯静态。
// 场景：主路 A（高权重）持续失败，备路 B 健康——动态选 B，静态仍选 A。
func TestPickPrimaryDynamicToggle(t *testing.T) {
	routes := []config.ProviderConfig{
		{Provider: "a", APIBase: "https://a/v1", Model: "ma", Weight: 5},
		{Provider: "b", APIBase: "https://b/v1", Model: "mb", Weight: 1},
	}
	e := &Engine{Cfg: config.Default(), routeStats: map[string]*routeLatency{}}
	e.Cfg.ModelRoutes = routes
	// A 连续失败、B 连续成功（喂足样本触发健康惩罚）
	for i := 0; i < 6; i++ {
		e.observeRoute("https://a/v1", "ma", 20*time.Millisecond, errTest{})
		e.observeRoute("https://b/v1", "mb", 20*time.Millisecond, nil)
	}

	restore := pinDynamicRouting(t, true)
	got := e.pickPrimaryRoute()
	if got.Model != "mb" {
		t.Fatalf("动态模式应惩罚全失败路由选 mb，实际: %+v", got)
	}
	restore()

	restore = pinDynamicRouting(t, false)
	defer restore()
	got = e.pickPrimaryRoute()
	if got.Model != "ma" {
		t.Fatalf("关闭动态后应回静态权重选 ma，实际: %+v", got)
	}
}

// TestResolveRouteFallbacksOrderExclude 降级链：排除主路由 + 其余按权重降序。
func TestResolveRouteFallbacksOrderExclude(t *testing.T) {
	e := &Engine{Cfg: config.Default()}
	e.Cfg.ModelRoutes = []config.ProviderConfig{
		{Provider: "a", APIBase: "https://a/v1", Model: "ma", Weight: 3},
		{Provider: "b", APIBase: "https://b/v1", Model: "mb", Weight: 1},
		{Provider: "c", APIBase: "https://c/v1", Model: "mc", Weight: 4},
		{Provider: "d", APIBase: "https://d/v1", Model: "md", Weight: 2},
	}
	fb := e.resolveRouteFallbacks(e.Cfg.ModelRoutes[0])
	if len(fb) != 3 {
		t.Fatalf("应排除主路由剩 3 条，实际 %d", len(fb))
	}
	want := []string{"mc", "md", "mb"} // 权重 4>2>1
	for i, m := range want {
		if fb[i].Model != m {
			t.Fatalf("降级链第 %d 位应为 %s，实际 %s（全链: %v）", i, m, fb[i].Model, fb)
		}
	}
}

// errTest 测试用错误。
type errTest struct{}

func (errTest) Error() string { return "fail" }

// pinDynamicRouting 设置全局 config.C.DynamicRouting 并返回恢复函数
// （pickPrimaryRoute 读的是全局 config.C 而非实例 Cfg，测试须防全局泄漏）。
func pinDynamicRouting(t *testing.T, on bool) func() {
	t.Helper()
	old := config.C
	cfg := config.Default()
	cfg.DynamicRouting = on
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	return func() { config.C = old }
}
