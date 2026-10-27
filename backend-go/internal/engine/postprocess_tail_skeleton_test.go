// ============ 本文件职责中文说明 ============
// 行尾中文标点骨架剥离的锁（`stripTrailingCJKNotes` / `trimCJKSkeletonTail`）。
//
// 缺陷形态（工单 T20261027152356CMY 交付 md 第 39 行的指纹）：
//
//	源文 `当前：全部允许…；"0" 。`
//	→ 模型把中文尾巴也带进译文：`Current: All allowed ：；""0，。`
//	→ StripChineseInNonZh 删掉汉字，剩下一串全角标点 + 引号 + 数字的"骨架"挂在正常英文句子后面
//	→ 整行含拉丁字母、不以全角标点开头 ⇒ 旧的两条判据都不命中 ⇒ 一路写进交付物。
//
// 判据四道（见 trimCJKSkeletonTail 注释），本文件逐道配正反腿：
//
//	A 现网样本：交付 md 那种尾巴必须被削掉，英文正文一个字不动；
//	B 多形态：弯引号/全角括号/数字混排/全角句号 都要削干净（不许剩半截 `：；""`）；
//	C 不许误杀：正文里的数字、引号、括号、省略号一律不动；
//	D CJK 目标语：中文/日文译文行尾的 `。` 是正文，**不许**被削（这一档根本不该进来）；
//	E 既有行为不回退：整行骨架、行首骨架仍然按行截断（老的两条判据不能因为加了行尾修剪而失效）。
//
// ============================================
package engine

import "testing"

// TestTrimCJKSkeletonTailDeliveredSample A 腿：交付 md 里那条真实形态。
func TestTrimCJKSkeletonTailDeliveredSample(t *testing.T) {
	const want = `Current: All allowed; Position left blank/0 = Use the global single ticket amount`
	for _, in := range []string{
		// 现网字节：`：；` + ASCII 引号 + `0，。`
		want + ` ：；""0，。`,
		// 弯引号变体（同一句、不同模型输出）
		want + ` ：；“”0，。`,
		// 尾巴后面还带空格（模型常吐一个尾随空格）
		want + ` ：；""0，。 `,
	} {
		got := PostProcessTranslation(in, "en")
		if got != want {
			t.Errorf("行尾骨架未削干净：\n in=%q\n got=%q\n want=%q", in, got, want)
		}
	}

	// 含汉字的尾巴：必须先由 StripChineseInNonZh 去掉汉字、再由本档削骨架（两步的顺序不能反，
	// 否则会在中文中间切开）。全角括号包着中文是现网"（见附表）。"那一类。
	if got := PostProcessTranslation(`Note ：（见附表）。`, "en"); got != `Note` {
		t.Errorf("含汉字的尾巴未削干净，got=%q want=%q", got, `Note`)
	}
}

// TestTrimCJKSkeletonTailShapes B 腿：多种尾巴形态，必须削干净且不留半截。
func TestTrimCJKSkeletonTailShapes(t *testing.T) {
	cases := []struct{ in, want string }{
		{`Total assets 1,200 ：；`, `Total assets 1,200`}, // 千位逗号后的数字属正文，不许切在逗号后
		{`Refresh every 30s 。`, `Refresh every 30s`},
		{`Amount ：１００，。`, `Amount`},                        // 全角数字属骨架，跟着一起削
		{`Realized = replay ；浮动`, `Realized = replay ；浮动`}, // 尾巴是汉字（还没被洗）⇒ 不是骨架，不动
	}
	for _, c := range cases {
		if got := trimCJKSkeletonTail(c.in); got != c.want {
			t.Errorf("trimCJKSkeletonTail(%q)=%q, want %q", c.in, got, c.want)
		}
	}
}

// TestTrimCJKSkeletonTailDoesNotKillContent C 腿：正文里的数字/引号/括号/省略号，一个都不许动。
func TestTrimCJKSkeletonTailDoesNotKillContent(t *testing.T) {
	for _, line := range []string{
		`Chapter 3`,       // 只有数字，没有句读标点 ⇒ 不构成骨架
		`Total: 3`,        // 半角冒号不是触发特征
		`He said "0"`,     // 引号+数字，但没有全角句读
		`See （Appendix）`,  // 全角括号是正文的一部分（削掉会留下不闭合的左括号）
		`and so on…`,      // 省略号是英文正文自己的用法
		`Points 1,200.50`, // 半角标点
		`=；=-×；30s`,       // 整行骨架：本函数不负责（交给整行判据），不许在这里清成空串
		``,                // 空行
	} {
		if got := trimCJKSkeletonTail(line); got != line {
			t.Errorf("正文被误削：in=%q got=%q", line, got)
		}
	}
}

// TestTrimCJKSkeletonTailCJKTargets D 腿：CJK 目标语的句号是正文，不许被削。
// 这一档在 PostProcessTranslation 里走的是 stripTrailingCJKNotesStrict，根本不到 stripTrailingCJKNotes；
// 这里同时钉住"直接调也不许动中文行"，防止将来有人把两条腿合并。
func TestTrimCJKSkeletonTailCJKTargets(t *testing.T) {
	for _, line := range []string{
		`当前：全部允许。`,
		`已实现 = 加权成本重放；`,
		`実現済み = 加重コストの再生。`,
	} {
		if got := trimCJKSkeletonTail(line); got != line {
			t.Errorf("CJK 目标语行被误削：in=%q got=%q", line, got)
		}
		if got := PostProcessTranslation(line, "zh"); got != line {
			t.Errorf("zh 目标语经后处理被改：in=%q got=%q", line, got)
		}
	}
}

// TestStripTrailingCJKNotesKeepsWholeLineRules E 腿：既有两条整行判据不回退。
func TestStripTrailingCJKNotesKeepsWholeLineRules(t *testing.T) {
	// 整行纯骨架（无字母无数字）⇒ 从该行起截断（老行为）
	got := stripTrailingCJKNotes("Hello world\n（：，：\nMore")
	if got != "Hello world" {
		t.Errorf("整行骨架未按老行为截断，got=%q", got)
	}
	// 行首全角标点（讲解式残留）⇒ 从该行起截断（老行为）
	got = stripTrailingCJKNotes("Hello world\n：1. \"a\"；2. \"b\"")
	if got != "Hello world" {
		t.Errorf("行首骨架未按老行为截断，got=%q", got)
	}
	// 正常多行 ⇒ 原样返回
	if got := stripTrailingCJKNotes("Hello\nWorld"); got != "Hello\nWorld" {
		t.Errorf("正常多行被改动，got=%q", got)
	}
	// 截断点之前的行也要修剪尾巴（截断与修剪同时生效时不许漏）
	if got := stripTrailingCJKNotes("Hello world ：；\n（：，："); got != "Hello world" {
		t.Errorf("截断点之前的行未修剪，got=%q", got)
	}
}
