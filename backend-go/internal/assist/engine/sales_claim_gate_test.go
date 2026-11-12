// ============ sales_claim_gate_test.go · 职责说明 ============
// A13：销售声称守卫行为表 + fail-closed 边界两枚反证 + 灰度（缺省关/半套不生效）+ 挂点顺序锁。
//
//	两枚反证钉住「本轮是否销售场景」边界：
//	  ① 非销售页（翻译/客服老链路）即便灰度全开、即便 ctx 带事实集 ⇒ 逐字节等价放行，不误伤；
//	  ② 销售页无事实头/验签失败 ⇒ fail-closed 回避（不是零改动放行、不是 fail-open）。
//
// ================================================
package engine

import (
	"context"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	assiststore "translator/internal/assist/store"
	mainengine "translator/internal/engine"
)

// newClaimGuardEngine 建临时 assist 库 + 只挂 db 的 Engine，并种入转话题安全入口三行。
func newClaimGuardEngine(t *testing.T) (*Engine, *assiststore.DB) {
	t.Helper()
	adb, err := assiststore.Open(filepath.Join(t.TempDir(), "assist.db"))
	if err != nil {
		t.Fatalf("assist 库建连失败: %v", err)
	}
	t.Cleanup(func() { _ = adb.Close() })
	for _, k := range []string{"chat", "pricing", "register"} {
		if _, err := adb.Create("feature_links", map[string]any{
			"key": k, "name": k, "url": "/" + k, "ftype": "route", "icon": "",
		}); err != nil {
			t.Fatalf("种入 feature_links 失败: %v", err)
		}
	}
	return &Engine{db: adb}, adb
}

// onStr 布尔 → 配置字面（"1"/"0"）。
func onStr(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// armGuard 写两把灰度开关（不写＝缺省关）。
func armGuard(t *testing.T, adb *assiststore.DB, inj, guard bool) {
	t.Helper()
	if err := adb.SetConfig(cfgSalesFactsInjection, onStr(inj)); err != nil {
		t.Fatalf("写注入开关失败: %v", err)
	}
	if err := adb.SetConfig(cfgSalesClaimGuard, onStr(guard)); err != nil {
		t.Fatalf("写守卫开关失败: %v", err)
	}
}

// factsCtx 注入一份已验签事实集；nil ⇒ 返回不带事实集的 ctx（模拟无头/验签失败）。
func factsCtx(set *mainengine.SalesFactsSet) context.Context {
	if set == nil {
		return context.Background()
	}
	return InjectSalesFacts(context.Background(), *set)
}

// assertAvoided 断言「回避 + 转其他话题 + PendingHuman」四要素齐全且不含事实值。
func assertAvoided(t *testing.T, rep *Reply, lang string) {
	t.Helper()
	if rep.Source != "fallback" {
		t.Errorf("回避后 Source 应为 fallback，实际 %q", rep.Source)
	}
	if !rep.PendingHuman {
		t.Error("守卫命中必须把 rep.PendingHuman 置真")
	}
	if len(rep.Actions) == 0 {
		t.Error("「转其他话题」腿：Actions 不可为空")
	}
	want, _ := AvoidPhrase(lang)
	if rep.Content != want {
		t.Errorf("回避句未按 %s 选: got=%q want=%q", lang, rep.Content, want)
	}
	if regexp.MustCompile(`\d`).MatchString(rep.Content) {
		t.Errorf("回避句不得含任何事实值: %q", rep.Content)
	}
}

// assertUntouched 断言逐字节等价放行（同一指针、字段一字未动）。
func assertUntouched(t *testing.T, before, got *Reply) {
	t.Helper()
	if got != before {
		t.Error("放行必须原样返回同一 *Reply，不许替换指针")
	}
	if before.Content != "价格 99 元" || before.Source != "llm" || before.PendingHuman {
		t.Errorf("放行态字段被改动: %+v", before)
	}
}

// TestSalesClaimGuardDefaultOff 两把开关缺省关 ⇒ 逐字节等价放行。
func TestSalesClaimGuardDefaultOff(t *testing.T) {
	eng, _ := newClaimGuardEngine(t) // 不写任何开关＝缺省关
	rep := &Reply{Content: "价格 99 元", Source: "llm"}
	got := eng.guardSalesClaims(factsCtx(nil), "zh", "/pricing", rep)
	assertUntouched(t, rep, got)
}

// TestSalesClaimGuardHalfConfigNotArmed A15：只开一把（半套）不算全开 ⇒ 逐字节等价放行。
func TestSalesClaimGuardHalfConfigNotArmed(t *testing.T) {
	for _, half := range [][2]bool{{true, false}, {false, true}} {
		eng, adb := newClaimGuardEngine(t)
		armGuard(t, adb, half[0], half[1])
		rep := &Reply{Content: "价格 99 元", Source: "llm"}
		got := eng.guardSalesClaims(factsCtx(nil), "zh", "/pricing", rep)
		assertUntouched(t, rep, got)
	}
}

// TestSalesClaimGuardNonSalesPageNoTouch 反证①：非销售页（翻译/客服老链路）零改动，不误伤。
// 即便灰度全开、即便 ctx 带着事实集，也不许把老链路改写成回避话术。
func TestSalesClaimGuardNonSalesPageNoTouch(t *testing.T) {
	eng, adb := newClaimGuardEngine(t)
	armGuard(t, adb, true, true)
	set := claimFacts("12 CNY")
	for _, page := range []string{"/app/translate", "https://x.com/editor/doc", "/workbench/ticket/9", "/help"} {
		rep := &Reply{Content: "价格 99 元", Source: "llm"}
		got := eng.guardSalesClaims(factsCtx(&set), "zh", page, rep)
		assertUntouched(t, rep, got)
	}
}

// TestSalesClaimGuardNoFactsHeadFailClosed 反证②：销售页 + 无事实头 ⇒ fail-closed 回避，不是放行。
func TestSalesClaimGuardNoFactsHeadFailClosed(t *testing.T) {
	eng, adb := newClaimGuardEngine(t)
	armGuard(t, adb, true, true)
	rep := &Reply{Content: "价格 99 元", Source: "llm"}
	got := eng.guardSalesClaims(factsCtx(nil), "zh", "/pricing", rep)
	if got != rep {
		t.Fatal("守卫应就地改写回复")
	}
	assertAvoided(t, rep, "zh")
}

// TestSalesClaimGuardEmptyFactsAvoid 销售页 + 验签通过但 facts 为空 ⇒ 回避。
func TestSalesClaimGuardEmptyFactsAvoid(t *testing.T) {
	eng, adb := newClaimGuardEngine(t)
	armGuard(t, adb, true, true)
	empty := mainengine.SalesFactsSet{Facts: []mainengine.SalesFact{}}
	rep := &Reply{Content: "欢迎咨询", Source: "llm"}
	eng.guardSalesClaims(factsCtx(&empty), "en", "/product/42", rep)
	assertAvoided(t, rep, "en")
}

// TestSalesClaimGuardControlledKeyAvoid 模型输出触及受控键 ⇒ 回避（受控值绝不进集）。
func TestSalesClaimGuardControlledKeyAvoid(t *testing.T) {
	eng, adb := newClaimGuardEngine(t)
	armGuard(t, adb, true, true)
	set := claimFacts("12 CNY")
	set.Controlled = []mainengine.ControlledKey{{Kind: "cert", Key: "认证编号"}}
	rep := &Reply{Content: "认证编号是 12", Source: "llm"}
	eng.guardSalesClaims(factsCtx(&set), "zh", "/pricing", rep)
	assertAvoided(t, rep, "zh")
}

// TestSalesClaimGuardLegalClaimPass M0235 正向对照：准入事实可对上 ⇒ 一字不动放行。
func TestSalesClaimGuardLegalClaimPass(t *testing.T) {
	eng, adb := newClaimGuardEngine(t)
	armGuard(t, adb, true, true)
	set := claimFacts("12 CNY")
	set.Controlled = []mainengine.ControlledKey{{Kind: "cert", Key: "认证编号"}}
	rep := &Reply{Content: "价格是 12 元", Source: "llm"}
	got := eng.guardSalesClaims(factsCtx(&set), "zh", "/pricing", rep)
	if got != rep || rep.Content != "价格是 12 元" || rep.PendingHuman || rep.Source != "llm" {
		t.Fatalf("合法声称应放行且 PendingHuman=false: %+v", rep)
	}
}

// TestSalesClaimGuardUnsourcedNumberAvoid 声称数值不在任一已准入译文 ⇒ 回避。
func TestSalesClaimGuardUnsourcedNumberAvoid(t *testing.T) {
	eng, adb := newClaimGuardEngine(t)
	armGuard(t, adb, true, true)
	set := claimFacts("12 CNY")
	rep := &Reply{Content: "价格是 99 元", Source: "llm"}
	eng.guardSalesClaims(factsCtx(&set), "zh", "/pricing", rep)
	assertAvoided(t, rep, "zh")
}

// TestSalesClaimGuardAvoidPhraseByLang 三语种回避句按本轮语言选、均不含事实值、PendingHuman 置真。
func TestSalesClaimGuardAvoidPhraseByLang(t *testing.T) {
	for _, lang := range []string{"zh", "en", "ja"} {
		eng, adb := newClaimGuardEngine(t)
		armGuard(t, adb, true, true)
		rep := &Reply{Content: "价格 99 元", Source: "llm"}
		eng.guardSalesClaims(factsCtx(nil), lang, "/pricing", rep)
		assertAvoided(t, rep, lang)
	}
}

// TestSalesClaimGuardChainOrder 挂点锁：guardSalesClaims 必须在 brand 之后、recharge 之前。
func TestSalesClaimGuardChainOrder(t *testing.T) {
	src := readOutboundChain(t)
	brand := strings.Index(src, "rep = e.guardReplyBrand(ctx, answer, rep)")
	guard := strings.Index(src, "rep = e.guardSalesClaims(ctx, answer, pageURL, rep)")
	recharge := strings.Index(src, "rep = e.applyRechargeIntent(ctx, sessionID, input, rep)")
	if brand < 0 || guard < 0 || recharge < 0 {
		t.Fatal("出站链缺少 brand / 销售声称守卫 / recharge 之一")
	}
	if !(brand < guard && guard < recharge) {
		t.Fatalf("守卫挂点错误：必须 brand(%d) < guard(%d) < recharge(%d)", brand, guard, recharge)
	}
}
