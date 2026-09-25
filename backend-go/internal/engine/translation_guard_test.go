// ============ translation_guard_test.go 职责说明 ============
// 译文可用性单一判定的回归断言（P0 整改 RC-2 + F-38 批 D）：
//   - IsTranslationUsable：空/失败占位/同文（含空白折叠）/指令回显残留/短格长度爆炸五类必判不可用；
//     源文与译文**都**含 '<' 的合法数学式必须判可用（防把 "A<B" 误杀）；
//   - collectKBPass：KB「源文=译文」脏行与「6 字格×2100 字邮件」爆炸行都不得算命中，
//     必须进模型补漏队列；
//   - missingSegments：脏行未写入 langTranslations ⇒ 最终计入未译清单（不再被硬闸永久跳过）。
//
// ========================================
package engine

import (
	"strings"
	"testing"
)

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

// ---------- F-38（2026-09-25 批 D）第 5 判据「短格长度爆炸」 ----------

// f38MailGarbage 生产实测形态的合成样本：6 字表格单元格被上游吐回 2,100+ 字符的英文邮件
// （账本留证的脏串开头即 "Dear Valued Customer"，本守卫上线后交付物里不应再出现任何 "Dear "）。
var f38MailGarbage = "Dear Valued Customer, " + strings.Repeat("thank you very much for your recent purchase, your order is confirmed. ", 35)

// TestIsTranslationUsableRejectsLengthExplosion 修复文档的字节级基准断言：6 字源 × 2100 字邮件 = false。
func TestIsTranslationUsableRejectsLengthExplosion(t *testing.T) {
	if len([]rune(f38MailGarbage)) <= 2100 {
		t.Fatalf("样本体积失真（应 ≥2100 rune 才能复现生产形态）: %d", len([]rune(f38MailGarbage)))
	}
	if IsTranslationUsable("产品方案书", f38MailGarbage) {
		t.Fatal("6 字短格 × 2100 字邮件必须判不可用（F-38 正是被前四判据全放行才进了交付物）")
	}
	// collectKBPass 同口径：爆炸候选不算命中、进补漏队列、未写入即计入未译清单。
	texts := []string{"产品方案书", "支持多格式文件翻译"}
	kbVal := []string{f38MailGarbage, "Multi-format file translation is supported"}
	accepted, needModelIdx := collectKBPass(texts, []bool{true, true}, kbVal, nil)
	if len(accepted) != 1 || accepted[0] != 1 {
		t.Fatalf("爆炸候选不得算命中：accepted=%v", accepted)
	}
	if len(needModelIdx) != 1 || needModelIdx[0] != 0 {
		t.Fatalf("爆炸候选必须进模型补漏队列：needModelIdx=%v", needModelIdx)
	}
	tr := map[string]string{texts[1]: kbVal[1]}
	missing := missingSegments(texts, tr)
	if len(missing) != 1 || missing[0] != texts[0] {
		t.Fatalf("爆炸段必须浮出水面计入未译（而不是被硬闸永久跳过），got %q", missing)
	}
}

// TestLengthExplosionBoundaries 判据边界等值锁：下限 80、8×src 缩放、>12 rune 源文不生效。
// 钉的是「只对短格生效避免误杀缩写展开/长语系」这条设计约束——把上限改成一刀切会在这里红灯。
func TestLengthExplosionBoundaries(t *testing.T) {
	cases := []struct {
		name string
		src  string
		out  string
		want bool // IsTranslationUsable 期望
	}{
		{"5字源_80rune贴线可用", "产品方案书", strings.Repeat("a", 80), true},
		{"5字源_81rune越界不可用", "产品方案书", strings.Repeat("a", 81), false},
		{"12字源按8倍缩放96线", "一二三四五六七八九十甲乙", strings.Repeat("b", 96), true},
		{"12字源_100rune不可用", "一二三四五六七八九十甲乙", strings.Repeat("b", 100), false},
		{"13字源超短格范围不生效", "一二三四五六七八九十甲乙丙", strings.Repeat("c", 5000), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsTranslationUsable(c.src, c.out); got != c.want {
				t.Fatalf("src=%d字 out=%d字: got %v want %v", len([]rune(c.src)), len([]rune(c.out)), got, c.want)
			}
		})
	}
	// 空源文不参与长度判据（由第 1 判据的空串口径负责），反向锁 hasLengthExplosion 本身。
	if hasLengthExplosion("", strings.Repeat("d", 500)) {
		t.Fatal("空源文不应触发长度爆炸判据（职责归第 1 判据）")
	}
	// 缩写展开反向锁（修复文档「避免误杀」的实测样例）：AWS→亚马逊云科技 必须可用
	if !IsTranslationUsable("AWS", "亚马逊云科技") {
		t.Fatal("合法缩写展开被长度判据误杀")
	}
}

// TestLengthExplosionThresholdsEnvOverridable 阈值 env 可配（修复文档「阈值 8×/300rune env 可配」）。
// 非法值（0/负数/非数字）必须回退默认——配置手滑不能把闸门拆成放行。
func TestLengthExplosionThresholdsEnvOverridable(t *testing.T) {
	t.Setenv("LC_GUARD_SHORT_RATIO", "1")
	t.Setenv("LC_GUARD_SHORT_FLOOR_RUNES", "10")
	if !hasLengthExplosion("一二三四五六七八九十甲乙", strings.Repeat("x", 13)) {
		t.Fatal("ratio=1/floor=10 未生效（12 字源上限应为 12）")
	}
	t.Setenv("LC_GUARD_SHORT_MAX_SRC_RUNES", "3")
	if hasLengthExplosion("一二三四五", strings.Repeat("y", 9000)) {
		t.Fatal("max_src=3 后 5 字源不应再进短格判据")
	}
	t.Setenv("LC_GUARD_SHORT_RATIO", "abc")
	t.Setenv("LC_GUARD_SHORT_FLOOR_RUNES", "-5")
	if got := shortGuardLimit(5); got != 80 {
		t.Fatalf("非法 env 应回退默认（5 字源→max(8×5,80)=80），got %d", got)
	}
	// absCap 兜底：ratio 被调巨大时短格仍封顶 300
	t.Setenv("LC_GUARD_SHORT_RATIO", "100000")
	t.Setenv("LC_GUARD_SHORT_FLOOR_RUNES", "1")
	if got := shortGuardLimit(12); got != 300 {
		t.Fatalf("absCap 兜底失效（应封顶 300），got %d", got)
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
