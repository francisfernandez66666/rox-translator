// ============ h1_terms_strict_test.go · 职责说明 ============
// T-C（2026-10-29 第十一轮）严格标签档：源段逐字等于术语源文、≤8 字、唯一命中时，
// 交付值直接采用规定译法（不再靠"重翻一次 + 警告"这种概率路径）。
//
// 五条腿：逐字命中 / 包含不命中 / ≤8 字门 / 两条同源不猜 / 接线（applyOutputGates 真走到）。
// ============================================
package engine

import (
	"context"
	"strings"
	"testing"

	"translator/internal/config"
	"translator/internal/gate"
	"translator/internal/store"
	"translator/internal/tenant"
)

// TestStrictLabelTerm 判据本身（纯函数）。
func TestStrictLabelTerm(t *testing.T) {
	terms := []gate.TermRequirement{{Source: "卖出胜负", Target: "Win/Loss (sell)"}}
	if v, ok := strictLabelTerm("卖出胜负", terms); !ok || v != "Win/Loss (sell)" {
		t.Fatalf("逐字命中应返回规定译法，实得 %q/%v", v, ok)
	}
	if v, ok := strictLabelTerm(" 卖出胜负 ", terms); !ok || v != "Win/Loss (sell)" {
		t.Fatalf("首尾空白应被 trim 后命中，实得 %q/%v", v, ok)
	}
	if _, ok := strictLabelTerm("卖出胜负的统计", terms); ok {
		t.Fatalf("包含关系不该走严格标签档（会改坏整句）")
	}
	long := "一二三四五六七八九" // 9 字
	if _, ok := strictLabelTerm(long, []gate.TermRequirement{{Source: long, Target: "X"}}); ok {
		t.Fatalf("超过 8 字不该走严格标签档")
	}
	two := []gate.TermRequirement{{Source: "卖出胜负", Target: "A"}, {Source: "卖出胜负", Target: "B"}}
	if _, ok := strictLabelTerm("卖出胜负", two); ok {
		t.Fatalf("两条同源不同译法不许猜")
	}
	if _, ok := strictLabelTerm("卖出胜负", nil); ok {
		t.Fatalf("无术语要求时不该命中")
	}
}

// TestStrictLabelForcedInGate 接线：走到 check 的最终失败分支时必须采用规定译法。
func TestStrictLabelForcedInGate(t *testing.T) {
	st := newTestStore(t)
	pkg, err := st.CreateKBPackage(1, 0, "labels", "标签包", store.PackTenant, store.PackRoleSource)
	if err != nil {
		t.Fatalf("建包失败: %v", err)
	}
	if _, err := st.SaveEntry(1, pkg.ID, 1, "zh", "卖出胜负", "en", "Win/Loss (sell)", "term"); err != nil {
		t.Fatalf("写术语失败: %v", err)
	}
	e := &Engine{St: st, Cfg: config.Default()}
	ctx := tenant.WithTenant(context.Background(), 1)

	tr := map[string]string{"en": "sell victory/defeat bets"}
	ws := e.applyOutputGates(ctx, "卖出胜负", tr, false)
	if tr["en"] != "Win/Loss (sell)" {
		t.Fatalf("固定标签应直接采用规定译法，实得 %q（warnings=%v）", tr["en"], ws)
	}
	if !strings.Contains(strings.Join(ws, "|"), "固定标签按术语表") {
		t.Fatalf("应采用时必须出声，实得 %v", ws)
	}

	// 反向：包含关系不强制（整句仍走原来的重翻/警告路径）。
	tr2 := map[string]string{"en": "sell victory/defeat bets"}
	ws2 := e.applyOutputGates(ctx, "卖出胜负的统计", tr2, false)
	if tr2["en"] != "sell victory/defeat bets" {
		t.Fatalf("包含关系被强制改写：%q（warnings=%v）", tr2["en"], ws2)
	}
	if strings.Contains(strings.Join(ws2, "|"), "固定标签按术语表") {
		t.Fatalf("包含关系不该出现严格标签档的警告：%v", ws2)
	}
}
