// ============ translation_guard_test.go 职责说明 ============
// 译文可用性单一判定的回归断言（P0 整改 RC-2）：
//   - IsTranslationUsable：空/失败占位/同文（含空白折叠）/指令回显残留四类必判不可用；
//     源文与译文**都**含 '<' 的合法数学式必须判可用（防把 "A<B" 误杀）；
//   - collectKBPass：KB「源文=译文」脏行不得算命中，必须进模型补漏队列；
//   - missingSegments：脏行未写入 langTranslations ⇒ 最终计入未译清单（不再被硬闸永久跳过）。
//
// ========================================
package engine

import "testing"

func TestIsTranslationUsableRejectsUnusableCandidates(t *testing.T) {
	cases := []struct {
		name string
		src  string
		out  string
	}{
		{"空译文", "产品方案书", ""},
		{"纯空白译文", "产品方案书", "  \t\n "},
		{"批量失败占位", "产品方案书", translationFailureMark},
		{"同文回显", "产品方案书", "产品方案书"},
		{"同文但空白被折叠", "产品 方案\n书", "产品 方案 书"},
		{"指令回显残留（源文无尖括号·译文凭空多出）",
			"The system matches the built-in automotive database",
			"<Only output the final translated text, enclosed entirely within  and nothing else>"},
		{"闭标签残留", "#", "</t>"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if IsTranslationUsable(c.src, c.out) {
				t.Errorf("应判不可用：src=%q out=%q", c.src, c.out)
			}
		})
	}
}

// TestIsTranslationUsableKeepsLegitAngles 合法含尖括号的译文必须判可用：
// 源文本身带 '<'（数学式/代码片段）时，译文同样带 '<' 是正确交付而非指令回显。
func TestIsTranslationUsableKeepsLegitAngles(t *testing.T) {
	cases := []struct{ src, out string }{
		{"当 A<B 时不触发告警", "No alert is triggered when A<B"},
		{"if (i<n) { return }", "当 i<n 时直接返回"},
	}
	for _, c := range cases {
		t.Run(c.src, func(t *testing.T) {
			if !IsTranslationUsable(c.src, c.out) {
				t.Errorf("合法尖括号译文被误杀：src=%q out=%q", c.src, c.out)
			}
		})
	}
}

// TestIsTranslationUsableMarkdownHeading 讲「只输出译文」这类正文行不得被当指令误杀：
// 源文与译文同为正文（都不含 '<'）时判可用——这是不用短语黑名单的实测理由。
func TestIsTranslationUsableMarkdownHeading(t *testing.T) {
	src := "只输出译文，不要解释"
	out := "Output only the translation, with no explanation"
	if !IsTranslationUsable(src, out) {
		t.Fatalf("正常正文被误判为指令残留：src=%q out=%q", src, out)
	}
}

// TestCollectKBPassRejectsDirtyRow KB 脏行（源文=译文）场景：不算命中、进补漏队列，
// 且因为键未写入 langTranslations，收尾统计会把该段计入未译（而不是永久锁死硬闸）。
func TestCollectKBPassRejectsDirtyRow(t *testing.T) {
	texts := []string{"产品方案书 v5.1", "支持多格式文件翻译"}
	kbHitIdx := []bool{true, true} // 两段都「命中」了
	kbVal := []string{"产品方案书 v5.1", "Multi-format file translation is supported"}

	accepted, needModelIdx := collectKBPass(texts, kbHitIdx, kbVal, nil)
	if len(accepted) != 1 || accepted[0] != 1 {
		t.Fatalf("脏行不得算命中：accepted=%v，want [1]", accepted)
	}
	if len(needModelIdx) != 1 || needModelIdx[0] != 0 {
		t.Fatalf("脏行必须进模型补漏队列：needModelIdx=%v，want [0]", needModelIdx)
	}

	// 模拟补漏仍未译出（模型稳定回显）后的收尾统计：该段必须浮出水面计入 untranslated。
	tr := map[string]string{}
	for _, i := range accepted {
		tr[texts[i]] = kbVal[i]
	}
	missing := missingSegments(texts, tr)
	if len(missing) != 1 || missing[0] != texts[0] {
		t.Fatalf("脏行段必须计入未译清单，got %q", missing)
	}
}

// TestCollectKBPassBlockedSegmentNotRetried 敏感拦截段既不算命中也不进补漏（上游零暴露口径不变）。
func TestCollectKBPassBlockedSegmentNotRetried(t *testing.T) {
	texts := []string{"违规内容段", "正常段落"}
	accepted, needModelIdx := collectKBPass(texts, []bool{false, false}, []string{"", ""},
		map[string]bool{"违规内容段": true})
	if len(accepted) != 0 {
		t.Fatalf("无命中时 accepted 应为空，got %v", accepted)
	}
	if len(needModelIdx) != 1 || needModelIdx[0] != 1 {
		t.Fatalf("仅未拦截段进补漏队列，got %v", needModelIdx)
	}
}
