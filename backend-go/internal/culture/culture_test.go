// ============ 本文件职责中文说明 ============
// 语言文化闸门（culture）单元测试：验证中性常见词豁免（control/hot 等不误拦正常译文）、
// 整词边界匹配（control 不命中 controller/controlling）、以及短语级避雷词仍生效。
// 修复 2026-09-03 工单失败：culture 闸门把 control/hot 当避雷词，自动重译 8 次仍不通过。
// ============================================
package culture

import "testing"

// TestForbiddenHitNeutralExemption 验证中性常见词（control/hot）被豁免，不拦截正常英文译文。
func TestForbiddenHitNeutralExemption(t *testing.T) {
	cases := []struct {
		target string
		text   string
		phrase string
		want   bool
	}{
		// 中性词豁免：即便配置为 forbidden 也不命中
		{"en", "vehicle control is important", "control", false},
		{"en", "the surface is hot", "hot", false},
		{"en", "hot weather driving", "hot", false},
		// 整词边界：control 不命中 controller/controlling
		{"en", "the controller unit", "control", false},
		{"en", "controlling the vehicle", "control", false},
		// 真实敏感词短语仍拦截（非豁免词）
		{"en", "this is a slur word", "slur word", true},
		// 其他语言不豁免中性词：俄语即使含 control 拉丁词也不豁免 → 命中
		{"ru", "the control system", "control", true},
		// 豁免仅限英语：IsNeutralForbidden("ru","control") 应为 false
		{"en", "the control system", "control", false},
	}
	for _, c := range cases {
		if got := ForbiddenHit(c.target, c.text, c.phrase); got != c.want {
			t.Errorf("ForbiddenHit(%q,%q,%q)=%v, want %v", c.target, c.text, c.phrase, got, c.want)
		}
	}
}

// TestForbiddenHitBoundary 验证整词边界匹配的正确性（大小写不敏感、边界字符识别、
// 词内不命中），用非豁免敏感词验证边界逻辑。
func TestForbiddenHitBoundary(t *testing.T) {
	cases := []struct {
		target string
		text   string
		phrase string
		want   bool
	}{
		{"en", "Avoid the slur word.", "slur", true},
		{"en", "Avoid the slur-word.", "slur", true}, // 边界符号仍命中
		{"en", "Avoid the slur.", "slur", true},
		{"en", "slurred speech", "slur", false}, // 词内不命中
		{"en", "slurs appear", "slur", false},   // 复数词尾不命中（整词边界）
		{"en", "a slurword example", "slur", false},
		{"en", "Brake CONTROL system", "control", false}, // 中性词豁免优先于边界命中
	}
	for _, c := range cases {
		if got := ForbiddenHit(c.target, c.text, c.phrase); got != c.want {
			t.Errorf("ForbiddenHit(%q,%q,%q)=%v, want %v", c.target, c.text, c.phrase, got, c.want)
		}
	}
}
