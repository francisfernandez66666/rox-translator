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
