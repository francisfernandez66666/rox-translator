// ============ 本文件职责中文说明 ============
// 语言识别与指令剥离（StripLangInstruction 等）的单元测试。
// ============================================
package engine

import "testing"

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
		{"100.00", "en"},    // 纯 ASCII 数字：不翻译，回退英文
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
