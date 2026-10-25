// ============ 本文件职责中文说明 ============
// 语言文化闸门（culture）单元测试：验证中性常见词豁免（control/hot 等不误拦正常译文）、
// 整词边界匹配（control 不命中 controller/controlling）、以及短语级避雷词仍生效。
// 修复 2026-09-03 工单失败：culture 闸门把 control/hot 当避雷词，自动重译 8 次仍不通过。
// ============================================
package culture

import (
	"testing"

	"translator/internal/store"
)

// TestForbiddenHitNeutralExemption 验证中性常见词（control/hot）被豁免，不拦截正常英文译文。
func TestForbiddenHitNeutralExemption(t *testing.T) {
	cases := []struct {
		target string
		text   string
		phrase string
		want   bool
	}{
		// 中性词豁免：即便配置为 forbidden 也不命中
		{"en", "vehicle control is important", "control", false},
		{"en", "the surface is hot", "hot", false},
		{"en", "hot weather driving", "hot", false},
		// 整词边界：control 不命中 controller/controlling
		{"en", "the controller unit", "control", false},
		{"en", "controlling the vehicle", "control", false},
		// 真实敏感词短语仍拦截（非豁免词）
		{"en", "this is a slur word", "slur word", true},
		// 其他语言不豁免中性词：俄语即使含 control 拉丁词也不豁免 → 命中
		{"ru", "the control system", "control", true},
		// 豁免仅限英语：IsNeutralForbidden("ru","control") 应为 false
		{"en", "the control system", "control", false},
	}
	for _, c := range cases {
		if got := ForbiddenHit(c.target, c.text, c.phrase); got != c.want {
			t.Errorf("ForbiddenHit(%q,%q,%q)=%v, want %v", c.target, c.text, c.phrase, got, c.want)
		}
	}
}

// TestForbiddenHitBoundary 验证整词边界匹配的正确性（大小写不敏感、边界字符识别、
// 词内不命中），用非豁免敏感词验证边界逻辑。
func TestForbiddenHitBoundary(t *testing.T) {
	cases := []struct {
		target string
		text   string
		phrase string
		want   bool
	}{
		{"en", "Avoid the slur word.", "slur", true},
		{"en", "Avoid the slur-word.", "slur", true}, // 边界符号仍命中
		{"en", "Avoid the slur.", "slur", true},
		{"en", "slurred speech", "slur", false}, // 词内不命中
		{"en", "slurs appear", "slur", false},   // 复数词尾不命中（整词边界）
		{"en", "a slurword example", "slur", false},
		{"en", "Brake CONTROL system", "control", false}, // 中性词豁免优先于边界命中
	}
	for _, c := range cases {
		if got := ForbiddenHit(c.target, c.text, c.phrase); got != c.want {
			t.Errorf("ForbiddenHit(%q,%q,%q)=%v, want %v", c.target, c.text, c.phrase, got, c.want)
		}
	}
}

// TestEnglishCommonWordsNotBlocked ★ 2026-10-24 用户令：正常沟通交流的词不作避雷。
// 这些词在英语里是语法骨架/日常词；无论 source=manual 还是 llm 都不应拦截。
func TestEnglishCommonWordsNotBlocked(t *testing.T) {
	words := []string{"must", "should", "can", "could", "will", "would", "may", "might", "need", "help", "use", "work"}
	for _, w := range words {
		sp := &store.KBSafetyPhrase{Lang: "en", Phrase: w, Kind: "forbidden", Source: "manual", Status: "approved"}
		if IsBlockingForbiddenRule("en", sp) {
			t.Errorf("日常词 %q 仍被当成拦截级避雷词", w)
		}
		if ForbiddenHitRule("en", "we must do this", sp) {
			t.Errorf("日常词 %q 仍命中正常英文译文", w)
		}
	}
}

// TestAutoSingleWordOnlyObserves 自动采集的单字只观测、不拦截；多词短语仍拦截。
func TestAutoSingleWordOnlyObserves(t *testing.T) {
	single := &store.KBSafetyPhrase{Lang: "en", Phrase: "foobar", Kind: "forbidden", Source: "llm", Status: "approved"}
	if IsBlockingForbiddenRule("en", single) {
		t.Fatal("自动采集的单字不应拦截")
	}
	phrase := &store.KBSafetyPhrase{Lang: "en", Phrase: "sensitive phrase", Kind: "forbidden", Source: "llm", Status: "approved"}
	if !IsBlockingForbiddenRule("en", phrase) {
		t.Fatal("自动采集的多词短语仍应拦截")
	}
}

// TestStyleAndReplaceNotTreatedAsForbidden style/replace 不是避雷词，不得进入词命中。
func TestStyleAndReplaceNotTreatedAsForbidden(t *testing.T) {
	style := &store.KBSafetyPhrase{Lang: "en", Phrase: "must", Kind: "style", Source: "manual", Status: "approved"}
	replace := &store.KBSafetyPhrase{Lang: "en", Phrase: "must", Kind: "replace", Replacement: "should", Source: "manual", Status: "approved"}
	if IsBlockingForbiddenRule("en", style) || IsBlockingForbiddenRule("en", replace) {
		t.Fatal("style/replace 不应参与避雷拦截")
	}
}

// TestRunCultureGateUsesRulePolicy 端到端：Run 只对真正的 forbidden 规则报命中。
func TestRunCultureGateUsesRulePolicy(t *testing.T) {
	safety := []*store.KBSafetyPhrase{
		{Lang: "en", Phrase: "must", Kind: "forbidden", Source: "manual", Status: "approved"},
		{Lang: "en", Phrase: "must", Kind: "style", Source: "manual", Status: "approved"},
		{Lang: "en", Phrase: "sensitive phrase", Kind: "forbidden", Source: "manual", Status: "approved"},
	}
	res := Run("en", "we must finish the sensitive phrase", safety)
	if res.Pass {
		t.Fatal("真实多词避雷词应被拦截")
	}
	if got := res.Checks[0].Detail; got != "sensitive phrase" {
		t.Fatalf("只应命中多词敏感词，实得 %q", got)
	}
}
