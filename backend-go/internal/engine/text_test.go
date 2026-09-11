// ============ text_test.go · 职责说明 ============
// 文本翻译纯函数单元测试：覆盖 TargetLangsFromOptions、ModeFromOptions、SplitOptions
// 三个纯函数的各分支场景（nil/空值/不同类型输入）。
// =============================================
package engine

import "testing"

// TestTargetLangsFromOptions 覆盖语言列表提取的五种输入场景：
// nil options、缺 key、[]string、[]interface{}、逗号分隔 string。
func TestTargetLangsFromOptions(t *testing.T) {
	cases := []struct {
		name    string
		options map[string]interface{}
		want    []string
	}{
		{"nil options", nil, nil},
		{"缺 target_langs key", map[string]interface{}{}, nil},
		{"[]string 类型", map[string]interface{}{"target_langs": []string{"en", "ru"}}, []string{"en", "ru"}},
		{"[]interface{} 类型", map[string]interface{}{"target_langs": []interface{}{"en", "ru"}}, []string{"en", "ru"}},
		{"逗号分隔 string", map[string]interface{}{"target_langs": "en, ru, ar"}, []string{"en", "ru", "ar"}},
		{"空字符串过滤", map[string]interface{}{"target_langs": "en,,ru,"}, []string{"en", "ru"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := TargetLangsFromOptions(c.options)
			if len(got) != len(c.want) {
				t.Fatalf("len=%d, want %d; got=%v", len(got), len(c.want), got)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("[%d] = %q, want %q", i, got[i], c.want[i])
				}
			}
		})
	}
}

// TestModeFromOptions 覆盖翻译模式提取：nil/空值/fast/pro。
func TestModeFromOptions(t *testing.T) {
	cases := []struct {
		name    string
		options map[string]interface{}
		want    string
	}{
		{"nil options", nil, ""},
		{"无 mode", map[string]interface{}{}, ""},
		{"fast 模式", map[string]interface{}{"mode": "fast"}, "fast"},
		{"pro 模式", map[string]interface{}{"mode": "pro"}, "pro"},
		{"大小写归一", map[string]interface{}{"mode": "FAST"}, "fast"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ModeFromOptions(c.options); got != c.want {
				t.Fatalf("ModeFromOptions() = %q, want %q", got, c.want)
			}
		})
	}
}

// TestSplitOptions 覆盖三路分离：KB 语言、其他语言、other 占位符。
func TestSplitOptions(t *testing.T) {
	cases := []struct {
		name         string
		langs        []string
		wantKB       []string
		wantOther    []string
		wantHasOther bool
	}{
		{"纯 KB 语言", []string{"en", "ru"}, []string{"en", "ru"}, nil, false},
		{"纯其他语言（非 KB）", []string{"sw", "yo"}, nil, []string{"sw", "yo"}, false},
		{"含 other 占位符", []string{"en", "other"}, []string{"en"}, nil, true},
		{"混合", []string{"en", "sw", "other"}, []string{"en"}, []string{"sw"}, true},
		{"空列表", []string{}, nil, nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			kb, other, hasOther := SplitOptions(c.langs)
			if len(kb) != len(c.wantKB) {
				t.Fatalf("kb=%v, want %v", kb, c.wantKB)
			}
			if len(other) != len(c.wantOther) {
				t.Fatalf("other=%v, want %v", other, c.wantOther)
			}
			if hasOther != c.wantHasOther {
				t.Fatalf("hasOther=%v, want %v", hasOther, c.wantHasOther)
			}
		})
	}
}
