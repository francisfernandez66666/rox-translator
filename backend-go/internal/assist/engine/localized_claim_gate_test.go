// ============ localized_claim_gate_test.go · 职责说明 ============
// A14：译文锚点归一化等值（描述类不逐字比对）、译文改数/译丢否定词必红、
// 模型输出声称锚点能与任一已准入译文对齐才放行、对不上走回避；
// PickSegmentByLang 按 lang 取译文、取不到回落中文并留痕。
// ================================================
package engine

import (
	"testing"

	mainengine "translator/internal/engine"
)

// claimFacts 用译文值拼一个已准入事实集（只填判据会读的 Value）。
func claimFacts(values ...string) mainengine.SalesFactsSet {
	set := mainengine.SalesFactsSet{Facts: []mainengine.SalesFact{}}
	for _, v := range values {
		set.Facts = append(set.Facts, mainengine.SalesFact{Value: v})
	}
	return set
}

// TestLocalizedClaimAnchorsAlign 锚点等值 + 否定词不丢；描述类同义不同措辞放行。
func TestLocalizedClaimAnchorsAlign(t *testing.T) {
	cases := []struct {
		name       string
		original   string
		translated string
		wantOK     bool
		wantReason string
	}{
		{"同值不同措辞放行", "质保 30 天", "Warranty 30 days", true, LocalizedReasonOK},
		{"译文改数必红(M0242)", "质保 30 天", "Warranty 20 days", false, LocalizedReasonAnchorAltered},
		{"译文多出数值必红", "质保 30 天", "30-day warranty, moq 5", false, LocalizedReasonAnchorAltered},
		{"否定词对等放行", "不含铅", "lead-free", true, LocalizedReasonOK},
		{"否定句译丢必红(M0243)", "不含铅", "contains lead", false, LocalizedReasonNegationLost},
	}
	for _, c := range cases {
		ok, reason := LocalizedAnchorsAlign(c.original, c.translated)
		if ok != c.wantOK || reason != c.wantReason {
			t.Errorf("%s: LocalizedAnchorsAlign(%q,%q)=(%v,%q)，期望 (%v,%q)",
				c.name, c.original, c.translated, ok, reason, c.wantOK, c.wantReason)
		}
	}
}

// TestLocalizedClaimGuardAgainstFacts 输出锚点须在任一已准入译文找到来源；对不上走回避。
func TestLocalizedClaimGuardAgainstFacts(t *testing.T) {
	cases := []struct {
		name       string
		text       string
		set        mainengine.SalesFactsSet
		wantOK     bool
		wantReason string
	}{
		{"锚点对齐放行", "价格是 12 元", claimFacts("12 CNY"), true, LocalizedReasonOK},
		{"认证编号对齐放行", "通过 ISO9001 认证", claimFacts("ISO9001 certified"), true, LocalizedReasonOK},
		{"无来源数字回避", "价格是 99 元", claimFacts("12 CNY"), false, LocalizedReasonUnsourcedAnchor},
		{"描述类无锚点放行", "欢迎咨询，稍后回复", claimFacts("12 CNY"), true, LocalizedReasonOK},
		{"任一译文命中即可", "交期 7 天", claimFacts("12 CNY", "lead time 7 days"), true, LocalizedReasonOK},
	}
	for _, c := range cases {
		ok, reason := GuardLocalizedClaim(c.text, c.set)
		if ok != c.wantOK || reason != c.wantReason {
			t.Errorf("%s: GuardLocalizedClaim=(%v,%q)，期望 (%v,%q)",
				c.name, ok, reason, c.wantOK, c.wantReason)
		}
	}
}

// TestLocalizedClaimPickSegmentByLang 按 lang 取译文；缺档回落中文并置 fallback 留痕。
func TestLocalizedClaimPickSegmentByLang(t *testing.T) {
	segs := map[string]string{"en": "Welcome", "zh": "欢迎"}
	cases := []struct {
		lang         string
		fallbackZH   string
		wantText     string
		wantFallback bool
	}{
		{"en", "兜底", "Welcome", false},
		{"EN", "兜底", "Welcome", false}, // 大小写归一
		{"fr", "兜底", "兜底", true},       // 缺档回落中文并留痕
		{"zh_hant", "兜底", "欢迎", true},  // 繁体缺档回落简体（同语种另一书写档）
	}
	for _, c := range cases {
		got, fb := PickSegmentByLang(segs, c.lang, c.fallbackZH)
		if got != c.wantText || fb != c.wantFallback {
			t.Errorf("PickSegmentByLang(%q)=(%q,%v)，期望 (%q,%v)",
				c.lang, got, fb, c.wantText, c.wantFallback)
		}
	}
}
