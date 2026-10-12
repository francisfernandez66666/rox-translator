// ============ review_purity_tail_digits_test.go 职责中文说明 ============
// ★ 2026-10-13 用户令⑧（任务 #32）「翻译结果仍然带回了非代码开发用的英文」的专属闸门。
//
// 缺陷本体：④ 尾段异脚本剥离的前置 3 原形态是「数字序列逐字不变」，
// 也就是**尾段只要带一个数字就不剥**。方向本来是安全的（数字可能在传型号／数量），
// 但它把「回声里复读的编号」也当成了新信息：
//
//	现网形态＝中文正文已经写了 600912.SH，尾段那句英文回译又原样念了一遍同一个号
//	⇒ 前置 3 恒假 ⇒ 整段英文**永远剥不掉**，客户在译文下面看到一行非目标语言。
//
// 放宽后的判据问**信息**而不是形状（本体＝tailDigitsAreEcho）：
// 尾段每个数字序列都必须在正文里已经出现过；有一个没出现过就按"它在传新信息"不剥。
//
// 本文件钉四件事：
//
//	① 复读数字的尾段照剥（缺陷本体，含"编号带点号后缀"那一形态）；
//	② 传新信息的尾段照旧不剥（**放宽≠拆除**：200 units 那一例、部分复读那一例都不许动）；
//	③ 比对取 digitSeqRe 的完整匹配串，不做子串（正文 1600912 不算"说过" 600912）；
//	④ 尾段没有数字时与放宽前**逐字等价**（等值锁：防"顺手把别的判据也改了"）；
//	⑤ 守卫级那一腿（同 AGENTS §三「判据级断言代替不了守卫级」）：整条出栈 PostProcessTranslation
//	   必须真的把那一行英文削掉——只测 stripTrailingForeignResidual 会漏掉接线。
//
// ==========================================
package engine

import (
	"strings"
	"testing"
)

// tailEchoReal 用户截图那条的复刻：正文含编号 600912.SH，尾段是整句英文回译（15 个拉丁词）。
const tailEchoReal = "根因在日志里可见：台账按 req.Code（带后缀，例如 600912.SH）建键，我的测试读了裸代码号。Fix the test keys to use the same withSuffix derivation so the ledger check passes"

// TestStripTailDigitsEchoes ① 复读数字的尾段照剥。
func TestStripTailDigitsEchoes(t *testing.T) {
	want := "根因在日志里可见：台账按 req.Code（带后缀，例如 600912.SH）建键，我的测试读了裸代码号。"
	got, did := stripTrailingForeignResidual("zh", tailEchoReal)
	if !did {
		t.Fatalf("现网那条（尾段回译复读正文已有编号）仍没被剥——前置 3 未放宽，实得 %q", got)
	}
	if got != want {
		t.Fatalf("剥后正文被改动\n实得 %q\n期望 %q", got, want)
	}
	// ⑤ 守卫级：整条出栈链必须也把这行英文削掉（判据函数绿≠接线绿，历史上三次同类假绿）
	out := PostProcessTranslation(tailEchoReal, "zh")
	if strings.Contains(out, "withSuffix") || strings.Contains(out, "Fix the test keys") {
		t.Fatalf("PostProcessTranslation 出栈仍带英文回声尾段：%q", out)
	}
	if !strings.Contains(out, "600912.SH") {
		t.Fatalf("守卫级那一腿把正文的编号一起削掉了（剥尾段不许动正文）：%q", out)
	}
}

// TestStripTailDigitsStillCarryingInfo ②③ 传新信息的尾段照旧不剥。
//
// 这一组就是本锁的**反证**：谁把前置 3 整个摘掉（"尾段够长就剥"），① 会绿、这里必红。
func TestStripTailDigitsStillCarryingInfo(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
	}{
		{
			// 旧用例那条，一字未改：尾段的 200 是正文没说过的新数量
			name: "全新数量",
			in:   "订单已确认。We need 200 units for the Frankfurt Motor Show next week.",
		},
		{
			// 部分复读：200 正文说过、300 没说过 ⇒ 仍属新信息（判据是"每一个都要见过"）
			name: "部分复读",
			in:   "订单 200 件已确认。We need 200 units and 300 spare parts for the Frankfurt Motor Show next week.",
		},
		{
			// ③ 完整匹配串等值，不做子串：正文是 1600912，尾段是 600912 ⇒ 两个号
			name: "子串不算复读",
			in:   "整机编号 1600912 已登记，请看随附的说明文件。Please confirm the suffixed code 600912 dot SH before the ledger check passes",
		},
		{
			// 正文一个数字都没有，尾段带小数 ⇒ 新信息
			name: "正文无数字",
			in:   "结论已经明确，请看下面的说明。The measured tolerance is 0.05 millimetres for every shaft in this batch",
		},
	} {
		got, did := stripTrailingForeignResidual("zh", tc.in)
		if did {
			t.Fatalf("%s：尾段带正文没说过的数字却被剥了（信息丢失）\n输入 %q\n实得 %q", tc.name, tc.in, got)
		}
		if got != tc.in {
			t.Fatalf("%s：判为不剥却改了出栈文本：%q → %q", tc.name, tc.in, got)
		}
	}
}

// TestStripTailDigitsEquivalenceWithOldRule ④ 尾段无数字时与放宽前逐字等价。
//
// 旧判据＝「head 的数字序列串 == 全文的数字序列串」；尾段没有数字时两者必然同串，
// 新判据在这一档恒 true ⇒ 行为一字不变。这一条锁的是"放宽只放该放的档位"。
func TestStripTailDigitsEquivalenceWithOldRule(t *testing.T) {
	for _, in := range []string{
		purityReal1, purityReal2, purityReal3,
		"结论！We need to translate the product manual for the Frankfurt auto show next week.",
		"第一行结论。\n第二行说明。\nPlease find the attached product manual for the Frankfurt Motor Show.",
	} {
		got, did := stripTrailingForeignResidual("zh", in)
		if n := len(digitSeqRe.FindAllString(extractTailForTest(in), -1)); n > 0 {
			t.Fatalf("样本选型失效：这一组必须全是「尾段无数字」的形态，实得尾段数字 %d 个：%q", n, extractTailForTest(in))
		}
		// 旧规则在这一档恒判"数字序列不变"⇒ 恒剥；新规则必须给出同一个决定（等值，不是子集）
		if !did {
			t.Fatalf("尾段无数字的既有形态被新判据拦住了（等价性破坏）：%q → %q", in, got)
		}
	}
}

// TestTailDigitsAreEchoJudge 判据函数本体（表驱动，反证与正证喂同一个函数）。
func TestTailDigitsAreEchoJudge(t *testing.T) {
	for _, tc := range []struct {
		name string
		head string
		tail string
		want bool
	}{
		{"尾段无数字", "正文 600912 已说过。", "and here is the trailing english sentence", true},
		{"正文无数字尾段也无", "纯正文。", "pure latin tail", true},
		{"全部复读", "编号 600912.SH 与批次 12 已登记。", "code 600912 batch 12 is registered here", true},
		{"有一个新数字", "编号 600912.SH 已登记。", "code 600912 plus 13 more items", false},
		{"正文空", "", "0 items", false},
	} {
		if got := tailDigitsAreEcho(tc.head, tc.tail); got != tc.want {
			t.Fatalf("%s：tailDigitsAreEcho(%q,%q)=%v 期望 %v", tc.name, tc.head, tc.tail, got, tc.want)
		}
	}
}

// extractTailForTest 复刻实现的剥点算法取尾段，只用于"样本选型自证"（④ 那条等值锁的前置）。
// 参数 text 出栈文本。返回: 最后一个目标脚本字母之后的那一段。
func extractTailForTest(text string) string {
	r := []rune(text)
	lastNative := -1
	for i := len(r) - 1; i >= 0; i-- {
		if nonLatinLetterRe.MatchString(string(r[i])) {
			lastNative = i
			break
		}
	}
	if lastNative < 0 {
		return ""
	}
	for i := lastNative + 1; i < len(r); i++ {
		if latinWordRe.MatchString(string(r[i])) {
			return string(r[i:])
		}
	}
	return ""
}
