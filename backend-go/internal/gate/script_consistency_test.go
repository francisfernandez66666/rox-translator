// ============ script_consistency_test.go · 职责说明 ============
// gate 检查 11「目标语言脚本一致性」的单测（P2，2026-11-20）：
// 非目标脚本混入（韩文/西里尔/汉字）必须判失败并给出该检查项；品牌拉丁白名单不误杀；
// targetCharsReasonable 的 he 分支修正（正常希伯来过、纯拉丁污染不过）；短正常译文不误杀。
// ============================================
package gate

import "testing"

// TestScriptConsistencyCheck11 检查 11：非目标语言脚本混入必须判失败，
// 品牌拉丁白名单不误杀。用希伯来语（现网样本）为主，配语言无关矩阵。
func TestScriptConsistencyCheck11(t *testing.T) {
	cases := []struct {
		name string
		src  string
		lang string
		tr   string
		pass bool
	}{
		{"he_正常含品牌", "通过Facebook投放", "he", "דרך Facebook פרסום", true},
		{"he_韩文污染", "通过Facebook投放", "he", "דרך Facebook 신호 פרסום", false},
		{"he_西里尔污染", "通过渠道投放", "he", "דרך канал פרסום", false},
		{"he_汉字残留", "通过渠道投放", "he", "דרך 渠道 פרסום", false},
		{"ru_正常", "通过渠道投放", "ru", "Через канал", true},
		{"ru_韩文污染", "通过渠道投放", "ru", "Через канал 신호", false},
		{"ja_正常", "通过渠道投放", "ja", "チャネルを通じて", true},
		{"ar_正常", "通过渠道投放", "ar", "عبر القناة", true},
		{"en_拉丁目标不启用检查11", "通过渠道投放", "en", "Through the channel", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := RunWithTerms(c.src, c.lang, c.tr, nil)
			if g.Pass != c.pass {
				t.Errorf("RunWithTerms(%q, %s) Pass=%v want=%v; checks=%v", c.tr, c.lang, g.Pass, c.pass, g.Checks)
			}
			if !c.pass {
				found := false
				for _, ck := range g.Checks {
					if ck.Name == "目标语言脚本一致" && !ck.Pass {
						found = true
					}
				}
				if !found {
					t.Errorf("污染样本应有「目标语言脚本一致」失败项，实得 %v", g.Checks)
				}
			}
		})
	}
}

// TestScriptConsistencyNeutralCell 检查 11 的「脚本中立格」豁免（★ 2026-11-22 tv9）。
//
// 现网实证：`gate.ScriptConsistent("he","650,000–1,200,000<br>")` = false ——
// 表格里纯数字/纯术语格因为「拉丁不进目标占比分子」被判 0% 目标体系，
// 于是三个**正确**的数字格被送进上下文重译（白占预算），重译若丢掉 `<br>` 反而能通过。
//
// 判据方向（两侧成对，缺一半就是给缺陷发证件）：
//
//	① 两侧字母全是拉丁 ⇒ 不判（无目标语内容可写）；
//	② 只要有一侧出现非拉丁字母 ⇒ 照判——否则「中文源 → 韩文译文」「希伯来源回显拉丁」
//	   这两类真污染会被这条豁免顺手放走。
func TestScriptConsistencyNeutralCell(t *testing.T) {
	neutral := []struct{ name, src, tr string }{
		{"纯数字加HTML标签", "650,000–1,200,000<br>", "650,000–1,200,000<br>"},
		{"纯拉丁术语", "SaaS", "SaaS"},
		{"拉丁缩写加数字", "CRM 2.0", "CRM 2.0"},
		{"纯符号", "—", "—"},
	}
	for _, c := range neutral {
		if !ScriptConsistent("he", c.src, c.tr) {
			t.Errorf("%s：脚本中立格不得判不纯（src=%q tr=%q）", c.name, c.src, c.tr)
		}
	}
	// ② 反向对照：任一侧有非拉丁字母就必须照判。
	polluted := []struct{ name, src, tr string }{
		{"拉丁源但译文串韩文", "SaaS", "SaaS 신호"},
		{"中文源译文纯拉丁（回显/未译）", "通过渠道投放", "Through the channel"},
		{"希伯来源译文纯拉丁（回显）", "דרך ערוץ", "Through the channel"},
		{"中文源译文串韩文", "通过渠道投放", "채널を通じて 신호"},
	}
	for _, c := range polluted {
		if ScriptConsistent("he", c.src, c.tr) {
			t.Errorf("%s：混入非目标脚本必须判不纯（src=%q tr=%q）", c.name, c.src, c.tr)
		}
	}
}

// TestTargetCharsReasonableHebrew 检查 8 的 he 分支：正常希伯来语必须过，
// 纯拉丁污染（含 arketing）不得过——原 default 分支要求拉丁字母，两者都错。
func TestTargetCharsReasonableHebrew(t *testing.T) {
	if !targetCharsReasonable("שלום עולם", "he") {
		t.Error("正常希伯来译文必须过 targetCharsReasonable(he)")
	}
	if targetCharsReasonable("arketing", "he") {
		t.Error("纯拉丁污染不得过 targetCharsReasonable(he)（原 default 分支误放行）")
	}
	if !targetCharsReasonable("שלום Facebook", "he") {
		t.Error("含品牌拉丁的正常希伯来译文必须过")
	}
}

// TestRunNoFalsePositiveOnShortTranslations 反劣化：短正常译文不被检查 8/11 误杀。
func TestRunNoFalsePositiveOnShortTranslations(t *testing.T) {
	for _, c := range []struct{ lang, tr string }{
		{"he", "כן"}, {"ru", "Да"}, {"ar", "نعم"}, {"ja", "はい"}, {"de", "Ja"},
	} {
		g := Run("是", c.lang, c.tr)
		if !g.Pass {
			t.Errorf("短正常译文被误杀：lang=%s tr=%q checks=%v", c.lang, c.tr, g.Checks)
		}
	}
}
