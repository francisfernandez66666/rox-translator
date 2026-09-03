// ============ detect_test.go · 职责说明 ============
// crawler 包「源语言自动检测」单元测试：脚本识别（中文/日文/韩文/西里尔/拉丁/阿拉伯/泰文/天城文）
// 与中文简体/繁体细分。锁定 2026-09-02 源语言清洗需求：英文源文本不得误标 zh。
// =============================================
package crawler

import "testing"

// TestDetectSourceLang 验证各脚本 → 语言代码映射。
func TestDetectSourceLang(t *testing.T) {
	cases := []struct {
		text string
		want string
	}{
		{"blended learning", "en"},        // 英文源文本：不得误标 zh
		{"汽车维修服务", "zh"},               // 简体中文
		{"欢迎，您好，谢谢", "zh"},             // 中文常用语
		{"emoji 😀 符号", "zh"},              // 汉字占优（符号忽略）
		{"コンニチハ世界", "ja"},              // 假名 → 日语（即便混汉字）
		{"안녕하세요 세계", "ko"},             // 谚文 → 韩语
		{"привет мир", "ru"},              // 西里尔 → 俄语
		{"السلام عليكم", "ar"},             // 阿拉伯字母 → 阿拉伯语
		{"สวัสดีครับ", "th"},                // 泰文
		{"नमस्ते दुनिया", "hi"},             // 天城文 → 印地语
		{"", ""},                            // 空 → 无法判定
		{"12345 !!!", ""},                   // 纯符号 → 无法判定
	}
	for _, c := range cases {
		if got := DetectSourceLang(c.text); got != c.want {
			t.Errorf("DetectSourceLang(%q) = %q, 期望 %q", c.text, got, c.want)
		}
	}
}

// TestDetectHanVariant 验证中文简体/繁体细分。
func TestDetectHanVariant(t *testing.T) {
	cases := []struct {
		text string
		want string
	}{
		{"汽车维修服务", "zh"},
		{"歡迎來到臺灣，我們很高興", "zh_hant"}, // 繁体独用字密集
		{"繁體中文測試", "zh_hant"},
	}
	for _, c := range cases {
		if got := detectHanVariant(c.text); got != c.want {
			t.Errorf("detectHanVariant(%q) = %q, 期望 %q", c.text, got, c.want)
		}
	}
}