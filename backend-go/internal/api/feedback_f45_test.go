// ============ feedback_f45_test.go · 职责说明 ============
// api 包反馈上下文归一的单元测试（★ 〇-U 批 I-2 · 缺陷 F-45）。
// 纯函数测试：钉死「任何输入都不可能把 JSON 字面量 null 送进 feedbacks.translations」，
// 因为 "null" 是合法 JSON，能穿过写侧校验、穿过读侧 try/catch，
// 最后在前端 Object.entries(null) 处抛错，让超管的反馈详情整块白屏。
// =============================================
package api

import "testing"

// TestTranslationsJSONForFeedback 归一口径的三态：空→{}、有值→原映射、非法输入不产 "null"。
func TestTranslationsJSONForFeedback(t *testing.T) {
	cases := []struct {
		name string
		in   map[string]string
		want string
	}{
		{"nil 映射（旧写法在这里产出 \"null\"）", nil, "{}"},
		{"空映射", map[string]string{}, "{}"},
		{"单语种", map[string]string{"en": "Hello"}, `{"en":"Hello"}`},
		{"多语种按键排序（Go marshal 对 map 键有序，等值锁可写死）",
			map[string]string{"ar": "مرحبا", "en": "Hello"}, `{"ar":"مرحبا","en":"Hello"}`},
	}
	for _, c := range cases {
		got := translationsJSONForFeedback(c.in)
		if got != c.want {
			t.Errorf("%s: 应得 %q，实得 %q", c.name, c.want, got)
		}
		// 负向锁：任何输入都不得产出会让前端判崩的字面量
		if got == "null" || got == "" {
			t.Errorf("%s: 产出了前端无法安全解析的 %q", c.name, got)
		}
	}
	// 常量自身也要守住（写侧各分支直接引用它）
	if emptyTranslationsJSON != "{}" {
		t.Errorf("空上下文常量应为 {}，实得 %q", emptyTranslationsJSON)
	}
}
