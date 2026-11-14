// ============ sales_page_shape_gate_test.go · 职责说明 ============
// 四期 W2 · R8（施工图 §4.2.2 第 0 条 / §4.2.3 / §5）：/p/{tc}/{pc} **算销售页**，
// 且**不误伤** /x/p/y 与工作台路径。两条腿各配一枚互不覆盖的变异体：
//   - M0306（腿①）：删掉形状判据 ⇒ /p/acme/... 不再判销售页 ⇒ 腿① 红、腿② 仍绿；
//   - M0308（腿②）：把 "p" 塞进 salesPageSegs ⇒ /x/p/y 被误判 ⇒ 腿② 红、腿① 仍绿。
//
// ★ 夹具口径（施工图 §4.2.3 / §4.2.2-0）：租户短码一律用 **acme**（非整词）。
//
//	绝不用 demo —— demo 本就在 salesPageSegs 里，会让「修复前」的旧判据也判绿，
//	测出来的是运气不是修复。下面的射程自证会把这一点现读现证。
//
// 同时喂四条输入（§4.2.3 R8 夹具口径）：/p/acme/9f3a（真）、/x/p/y（假）、
// /workspace/tickets（假）、https://主站/p/acme/9f3a?a=1#t（带协议与查询串，真）。
// ================================================
package engine

import "testing"

// TestSalesPageShapeProductPathJudgedSales R8 腿①：/p/{tc}/{pc} 必须判销售页（声称守卫真会跑）。
// 变异 M0306（摘掉形状判据）会让这两条 URL 全部回落 false ⇒ 本腿判红。
func TestSalesPageShapeProductPathJudgedSales(t *testing.T) {
	// 射程自证：夹具 acme 必须**不在**整词表、demo 必须在——否则「修复前」旧判据也能命中，本腿空转。
	if salesPageSegs["acme"] {
		t.Fatal("夹具自伤：acme 竟在 salesPageSegs 里，旧整词判据会假绿（改用非整词短码）")
	}
	if !salesPageSegs["demo"] {
		t.Fatal("夹具口径漂了：demo 应仍在整词表里（这正是必须用 acme 而非 demo 当短码的理由）")
	}
	cases := []string{
		"/p/acme/9f3a",
		"https://shop.example.com/p/acme/9f3a?a=1#t", // 带协议 + 查询串 + 锚点：验「去协议/去查询串」那一段没被改坏
	}
	for _, u := range cases {
		if !isSalesPageURL(u) {
			t.Fatalf("R8 腿① /p/ 托管产品页没判成销售页（形状判据被删/失效，守卫会空转）：%s", u)
		}
	}
}

// TestSalesPageShapeNoFalsePositiveOnAppPaths R8 腿②：非销售页（含 /x/p/y 与工作台）不许被判销售页。
// 变异 M0308（把 "p" 塞进 salesPageSegs）会让 /x/p/y 命中整词表 ⇒ 本腿判红。
func TestSalesPageShapeNoFalsePositiveOnAppPaths(t *testing.T) {
	cases := []string{
		"/x/p/y",             // 单字母段 p 出现在中间：塞 "p" 进整词表就会被误判成销售页
		"/workspace/tickets", // 翻译工作台/工单类 app 路径，绝不能被守卫改写成回避话术
	}
	for _, u := range cases {
		if isSalesPageURL(u) {
			t.Fatalf("R8 腿② 非销售页被误判成销售页（把 p 塞进整词表就会红，误伤付费主链）：%s", u)
		}
	}
}
