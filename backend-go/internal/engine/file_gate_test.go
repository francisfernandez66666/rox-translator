// 漏译率硬闸与写回交付形态的纯函数测试（2026-09-09 可靠性改造回归）：
//   - leakedLang：某语言未译出段数 >50% 返回语言代码（工单失败），否则 ""
//   - writebackDelivery：srt/vtt/json/yaml/yml 走 xlsx 对照表，其余走原格式写回
package engine

import "testing"

// TestProtectSourceByLang 翻译前品牌保护：源文品牌名替换为规定译法（长词优先防拆残），
// 无术语匹配时不改动；返回第二个值标记是否发生过替换。
func TestProtectSourceByLang(t *testing.T) {
	terms := map[string]string{"极石汽车": "ROX", "极石": "ROX"}
	cases := []struct {
		name  string
		in    []string
		terms map[string]string
		want  []string
	}{
		{"长词优先不拆残", []string{"极石汽车驰骋全球山海。"}, terms, []string{"ROX驰骋全球山海。"}},
		{"短词单独替换", []string{"极石是高端品牌。"}, terms, []string{"ROX是高端品牌。"}},
		{"不含品牌原样", []string{"今天天气不错。"}, terms, []string{"今天天气不错。"}},
		{"无术语映射原样", []string{"极石汽车。"}, nil, []string{"极石汽车。"}},
		{"多段各自替换", []string{"极石汽车A", "极石B"}, terms, []string{"ROXA", "ROXB"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, _ := protectSourceByLang(c.in, c.terms)
			for i := range c.want {
				if got[i] != c.want[i] {
					t.Fatalf("protectSourceByLang(%v)[%d] = %q, want %q", c.in, i, got[i], c.want[i])
				}
			}
		})
	}
}

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
