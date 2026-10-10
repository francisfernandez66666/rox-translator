// ============ ticket_notify_title_test.go · 职责说明 ============
// ★ 2026-10-10 批次 ⑬ 配套断言：工单失败通知标题的工单号兜底。
//
//	背景：前端建单时标题留空会把「未命名工单」i18n 兜底词当真标题入库，
//	失败通知出现「翻译工单失败：未命名工单」。修后两态：
//	① 标题空白（空串/纯空白）→ 用工单号（「翻译工单失败：T20261010132330UEM」形态）；
//	② 标题非空 → 原标题。不清洗存量库行（本断言只验标题生成，不触库）。
//
// ========================================
package service

import (
	"testing"

	"translator/internal/store"
)

// TestFailureNotifyTitle 工单失败通知标题两态断言（纯函数，不触 DB）。
func TestFailureNotifyTitle(t *testing.T) {
	cases := []struct {
		name  string
		title string
		want  string
	}{
		{"空标题用工单号兜底", "", "翻译工单失败：T20261010132330UEM"},
		{"纯空白标题同样兜底", "   ", "翻译工单失败：T20261010132330UEM"},
		{"非空标题用原标题", "产品手册季度更新", "翻译工单失败：产品手册季度更新"},
		{"首尾空白的标题取净值", "  营销文案  ", "翻译工单失败：营销文案"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tk := &store.Ticket{TicketNo: "T20261010132330UEM", Title: c.title}
			if got := failureNotifyTitle(tk); got != c.want {
				t.Fatalf("failureNotifyTitle(Title=%q) = %q, want %q", c.title, got, c.want)
			}
		})
	}
	// 口径说明：库里真存了「未命名工单」四个字属**存量非空标题**，按本修口径（不清洗存量行）
	// 仍原样输出——兜底只对「空白标题」生效，刻意不做「未命名工单」字面特判（避免误伤同名真标题）。
	tk := &store.Ticket{TicketNo: "T20261010132330UEM", Title: "未命名工单"}
	if got := failureNotifyTitle(tk); got != "翻译工单失败：未命名工单" {
		t.Fatalf("存量非空标题应原样输出（不清洗存量行），实得 %q", got)
	}
}
