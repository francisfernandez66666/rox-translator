// ============ 本文件职责中文说明 ============
// 语言识别与指令剥离（StripLangInstruction 等）的单元测试。
// ============================================
package engine

import "testing"

// TestStrip 验证 StripLangInstruction 在各类句式下正确剥离语言指令并返回目标语言。
func TestStrip(t *testing.T) {
	inputs := []string{
		`把"你好，世界"翻译成英语`,
		`翻译成英语：你好，世界`,
		`你好，世界`,
		`请把"翻译"这个词翻译成日语`,
	}
	wants := []string{`你好，世界`, `你好，世界`, `你好，世界`, `翻译`}
	wantLangs := [][]string{{"en"}, {"en"}, nil, {"ja"}}
	for i, in := range inputs {
		c, l := StripLangInstruction(in)
		if c != wants[i] {
			t.Errorf("in=%q got clean=%q want=%q", in, c, wants[i])
		}
		if len(l) != len(wantLangs[i]) {
			t.Errorf("in=%q langs=%v want %v", in, l, wantLangs[i])
		}
	}
}

// TestParse 验证 ParseTargetLangs 从用户输入中识别目标语言代码（按出现顺序）。
func TestParse(t *testing.T) {
	cases := map[string][]string{
		`把"你好，世界"翻译成英语`: {"en"},
		`翻译成英语：你好`:      {"en"},
		`ru: hello`:     {"ru"},
		`你好，世界`:         nil,
		`把"你好"翻译成日语`:    {"ja"},
	}
	for in, want := range cases {
		got := ParseTargetLangs(in)
		if len(got) != len(want) {
			t.Errorf("parse %q got %v want %v", in, got, want)
		}
	}
}

// TestDetectSourceLangFullWidth 成本表根因：含全角数字/标点的中文单元格
// （如「单价￥１２３．４５」「合计：５００元」）不得因全角字符稀释被误判为 en。
func TestDetectSourceLangFullWidth(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"单价￥１２３．４５", "zh"},
		{"合计：５００元", "zh"},
		{"产品名称：ＶＩＰ会员（￥１，２３４）", "zh"},
		{"成本 100.00 元", "zh"},
		{"2024-08-28 成本分析表", "zh"},
		{"100.00", "en"},     // 纯 ASCII 数字：不翻译，回退英文
		{"Total Cost", "en"}, // 纯英文
		{"안녕하세요", "ko"},
		{"こんにちは", "ja"},
	}
	for _, c := range cases {
		if got := DetectSourceLang(c.in); got != c.want {
			t.Errorf("DetectSourceLang(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestParseTargetLangsZh ★ #23（外语→简体中文）：目标语言识别必须认「中文/简体中文/
// Chinese」等别名，并与 zh_hant 撞词时稳定去重（"繁体中文" 字面含 "中文"，
// map 遍历序随机，不去重则目标语言在 zh/zh_hant 间漂移——本测试同时锁两个方向）。
func TestParseTargetLangsZh(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{`把这段话翻译成简体中文`, []string{"zh"}},
		{"please translate this into Chinese", []string{"zh"}},
		{`翻成繁体中文：你好`, []string{"zh_hant"}}, // 去重：不得同时冒出 zh
		{`translate to traditional chinese`, []string{"zh_hant"}},
		{`中国制造 出口合同`, nil}, // 「中国」不是别名：正文含国名不得误触发
	}
	for _, c := range cases {
		got := ParseTargetLangs(c.in)
		if len(got) != len(c.want) {
			t.Errorf("ParseTargetLangs(%q) = %v, want %v", c.in, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("ParseTargetLangs(%q) = %v, want %v", c.in, got, c.want)
				break
			}
		}
	}
}

// TestSplitOptionsZhTarget ★ #23：zh 刻意不进 TranslateLangs（KB/TM 列契约口径），
// 目标语言 zh 必须落入 directOther 纯模型直翻链，而非 KB 检索链。
func TestSplitOptionsZhTarget(t *testing.T) {
	kb, other, hasOther := SplitOptions([]string{"zh", "en"})
	if len(kb) != 1 || kb[0] != "en" {
		t.Errorf("kbTarget = %v, want [en]", kb)
	}
	if len(other) != 1 || other[0] != "zh" {
		t.Errorf("directOther = %v, want [zh]", other)
	}
	if hasOther {
		t.Errorf("hasOther should be false")
	}
}

// TestTranslateInstructionZh ★ #23：目标 zh 的提示词双链（中文界面→中文指令、
// 外语界面→英文指令）都已存在，这里锁死不回退——外语用户翻回简体中文是最常用方向。
// （contains 辅助复用同包 cjk_overlap_test.go 的既有实现）
func TestTranslateInstructionZh(t *testing.T) {
	if got := translateInstruction("ru", "zh", "ru"); got == "" || !contains(got, "Simplified Chinese") {
		t.Errorf("英文界面 zh 指令异常: %q", got)
	}
	if got := translateInstruction("ru", "zh", "zh"); !contains(got, "简体中文") {
		t.Errorf("中文界面 zh 指令异常: %q", got)
	}
}
