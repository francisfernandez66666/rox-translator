// 漏译率硬闸与写回交付形态的纯函数测试（2026-09-09 可靠性改造回归）：
//   - leakedLang：某语言未译出段数 >50% 返回语言代码（工单失败），否则 ""
//   - writebackDelivery：srt/vtt/json/yaml/yml 走 xlsx 对照表，其余走原格式写回
package engine

import "testing"

// TestLeakedLang 漏译率硬闸判定：超 50% 触发、恰 50% 不触发、0 不触发、负数不触发
func TestLeakedLang(t *testing.T) {
	cases := []struct {
		name   string
		lang   string
		remain int
		total  int
		want   string
	}{
		{"全部未译出触发", "en", 18, 18, "en"},
		{"超过50%触发", "ar", 11, 20, "ar"},
		{"恰50%不触发", "ru", 10, 20, ""},
		{"低于50%不触发", "ja", 9, 20, ""},
		{"remain为0不触发", "ko", 0, 10, ""},
		{"total为0不触发", "en", 0, 0, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := leakedLang(c.lang, c.remain, c.total); got != c.want {
				t.Errorf("leakedLang(%q, %d, %d) = %q, want %q", c.lang, c.remain, c.total, got, c.want)
			}
		})
	}
}

// TestWritebackDelivery 写回交付形态：无回写能力的格式走 xlsx，其余走原格式
func TestWritebackDelivery(t *testing.T) {
	xlsxFormats := []string{".srt", ".vtt", ".json", ".yaml", ".yml"}
	for _, ext := range xlsxFormats {
		if got := writebackDelivery(ext); got != "xlsx" {
			t.Errorf("writebackDelivery(%q) = %q, want \"xlsx\"", ext, got)
		}
	}
	inplaceFormats := []string{".pdf", ".docx", ".pptx", ".txt", ".csv", ".md", ".xlsx", ""}
	for _, ext := range inplaceFormats {
		if got := writebackDelivery(ext); got != "inplace" {
			t.Errorf("writebackDelivery(%q) = %q, want \"inplace\"", ext, got)
		}
	}
}
