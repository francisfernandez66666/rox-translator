package engine

import (
	"testing"

	"translator/internal/kb"
)

func TestCJKOverlap(t *testing.T) {
	input := "激活蓝牙钥匙，即可将手机当作车钥匙使用。您后续也可以在“全部功能”板块中添加“蓝牙钥匙”并激活钥匙。"
	cases := []struct {
		name string
		kb   string
		want bool // true=视为同一句直接采用
	}{
		{
			name: "非同一句（带编号的流程条目）",
			kb:   "4.点击车控页面“蓝牙钥匙”功能入口,按照指引激活蓝牙钥匙,激活后手机即是车钥匙。",
			want: false,
		},
		{
			name: "同一句标点变体",
			kb:   "激活蓝牙钥匙即可将手机当作车钥匙使用您后续也可以在全部功能板块中添加蓝牙钥匙并激活钥匙",
			want: true,
		},
		{
			name: "同一句",
			kb:   input,
			want: true,
		},
	}
	for _, c := range cases {
		got := cjkOverlap(input, c.kb) >= 0.55
		if got != c.want {
			t.Errorf("%s: overlap=%.3f, 期望视为同一句=%v, 实际=%v",
				c.name, cjkOverlap(input, c.kb), c.want, got)
		}
	}
}

func TestBuildExamplesPrompt(t *testing.T) {
	zh := "激活蓝牙钥匙，即可将手机当作车钥匙使用。"
	rows := []*kb.Row{
		{Zh: "蓝牙钥匙", Langs: map[string]string{"de": "Bluetooth-Schlüssel", "en": "Bluetooth key"}},
		{Zh: "激活蓝牙钥匙", Langs: map[string]string{"de": "Bluetooth-Schlüssel aktivieren"}},
		{Zh: "蓝牙钥匙", Langs: map[string]string{"de": "重复行"}}, // 应去重
		{Zh: "无译文行", Langs: map[string]string{"en": "no de"}}, // de 缺失，应跳过
		{Zh: "4.点击车控页面“蓝牙钥匙”功能入口,按照指引激活蓝牙钥匙,激活后手机即是车钥匙。", Langs: map[string]string{"de": "长流程句"}}, // 超 30 字，应跳过
	}
	out := buildExamplesPrompt(zh, "de", rows)
	for _, want := range []string{"Bluetooth-Schlüssel", "Bluetooth-Schlüssel aktivieren", "重复行"} {
		if contains(out, want) {
			if want == "重复行" {
				t.Errorf("重复的 zh 不应被注入: %s", want)
			}
		}
	}
	if !contains(out, "Bluetooth-Schlüssel aktivieren") {
		t.Errorf("期望注入 激活蓝牙钥匙 参考，实际:\n%s", out)
	}
	if contains(out, "no de") {
		t.Errorf("无目标语言译文的行不应注入")
	}
	if contains(out, "长流程句") {
		t.Errorf("超 30 字的长流程句不应注入为术语参考")
	}
	if !contains(out, "知识库术语参考") {
		t.Errorf("缺少参考块标题:\n%s", out)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
