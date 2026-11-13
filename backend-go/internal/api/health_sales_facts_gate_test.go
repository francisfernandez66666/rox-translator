// ============ health_sales_facts_gate_test.go · 职责说明 ============
// 四期 W1（施工图 §4.1.6 的 F9）：`/api/health` 的 `sales_facts` 段。
//
// 证什么（三条腿，缺一条都不算锁住）：
//
//	① **计数随真请求变化**：一次匿名产品页转发之后 scope_anon 与 injected 各 +1
//	   （写死 0 的实现会让这一腿红——M0276 就是那个坏法）；
//	② **负向：匿名健康面不许出现任何事实值**。为了让这条负向不是空转，先正向对照
//	   "那个唯一串确实在库里"（M0275 那族"库里根本没有这个值"的白锁在这里被显式排除）；
//	③ **接线腿（源码）**：health map 里必须有 `"sales_facts": s.salesFactsHealth()`——
//	   函数写得再好，没人调它等于这一面不存在（AGENTS §三「判据级代替不了守卫级」）。
//
// ★ 本端点匿名可达（监控与发版验收都要打），所以它只出「状态词 + 五个计数」：
// 不出事实值、产品名、租户名，也不出 product_id（那会把公开面变成枚举器）。
// ================================================
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// TestHealthSalesFactsCounters F9：计数随请求变化 + 响应体不含事实值。
func TestHealthSalesFactsCounters(t *testing.T) {
	fx := newW1Fixture(t)
	seedW1Facts(t, fx)

	// 正向对照：哨兵串真的在库里（否则下面那条负向扫描是空转）。
	var canary string
	if err := fx.srv.Store.DB().QueryRow(
		`SELECT value FROM product_facts WHERE tenant_id=7 AND key='retail_price'`).Scan(&canary); err != nil {
		t.Fatalf("读受控事实失败: %v", err)
	}
	if canary != "CTRL-A-SECRET" {
		t.Fatalf("哨兵串正向对照失效：库里读到 %q", canary)
	}

	before := fx.srv.salesFactsHealth()
	anonBefore := before["scope_anon"].(int64)
	injBefore := before["injected"].(int64)

	// 一次匿名产品页转发（greet/chat 任一，都会走同一个作用域解析口）。
	w1DoProxy(fx.srv, "", map[string]string{productPageHeader: w1PagePath(fx.A)})

	// 打一次真健康面（与生产同一入口，不走 salesFactsHealth 直调——那样测不到接线）。
	rec := httptest.NewRecorder()
	fx.srv.handleHealth(rec, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	if rec.Code != 200 {
		t.Fatalf("handleHealth 应 200，实际 %d", rec.Code)
	}
	body := rec.Body.String()

	var doc map[string]interface{}
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("健康响应不是 JSON: %v", err)
	}
	seg, ok := doc["sales_facts"].(map[string]interface{})
	if !ok {
		t.Fatalf("健康响应缺 sales_facts 段（计数面整段不存在）: %s", body)
	}
	// ① 计数随请求变化（写死 0 ⇒ 这里红）。
	if got := int64(seg["scope_anon"].(float64)); got-anonBefore != 1 {
		t.Fatalf("scope_anon 增量应为 1，实际 %d（before=%d after=%d）", got-anonBefore, anonBefore, got)
	}
	if got := int64(seg["injected"].(float64)); got-injBefore != 1 {
		t.Fatalf("injected 增量应为 1（下发了 1 条事实），实际 %d", got-injBefore)
	}
	if seg["gate"] != "on" {
		t.Fatalf("gate 状态词应为 on（两把开关全开），实际 %v", seg["gate"])
	}

	// ② 负向：事实值与受控值都不得出现。
	for _, leak := range []string{"CTRL-A-SECRET", "MOQ-A-50", "MOQ-B-99", "产品 A"} {
		if strings.Contains(body, leak) {
			t.Fatalf("健康面泄露了 %q：%s", leak, body)
		}
	}
	// 也不许出 product_id / 租户名（公开面不能变枚举器）。
	if strings.Contains(body, fx.A.ID) {
		t.Fatalf("健康面泄露了产品主键 %q：%s", fx.A.ID, body)
	}
}

// TestHealthSalesFactsWired 源码接线腿：health map 里必须有 sales_facts 段。
// 只测 salesFactsHealth 的返回值测不到"没人调它"（函数级判据代替不了接线级）。
func TestHealthSalesFactsWired(t *testing.T) {
	b, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatalf("读 server.go 失败: %v", err)
	}
	src := string(b)
	if !strings.Contains(src, `"sales_facts": s.salesFactsHealth()`) {
		t.Fatal("health map 里找不到 sales_facts 接线——计数会算但永远不出栈（M0276 同族）")
	}
}
