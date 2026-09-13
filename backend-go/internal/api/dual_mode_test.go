// ============ dual_mode_test.go · 职责说明 ============
// 工单双模式（2026-09-13）api 侧纯函数单测：交付方式归一化。
// =============================================
package api

import "testing"

func TestNormalizeTaskDelivery(t *testing.T) {
	cases := map[string]string{
		"":        "restore", // 历史工单/未显式选择
		"restore": "restore",
		"text":    "text",
		"TEXT":    "text",
		" text ":  "text",
		"garbage": "restore", // 非法值一律归默认
	}
	for in, want := range cases {
		if got := normalizeTaskDelivery(in); got != want {
			t.Errorf("normalizeTaskDelivery(%q)=%q, want %q", in, got, want)
		}
	}
}
