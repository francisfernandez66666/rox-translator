// ============ 本文件职责中文说明 ============
// 对照编辑器「逐段真值表回退」的静态锁（★ 2026-10-27 工单 T20261027152356CMY）。
//
// 缺陷本体：`extractSegments` 对非 Office/PDF 的文件工单只走 `parseAlignedFile`（xlsx/csv 对照表），
// 解析失败即 `return nil, false`。图片/音频/纯文案工单拿一张 .png/.mp3 去解析必然失败，
// 于是对照编辑器永远空白——而写侧 `persistTicketSegments` 是**全格式落库**的，数据一直在库里，
// 只是读侧从没问过它（PDF 分支 2026-09-18 就接了，图片这一档漏了）。
// 现网实证：工单 136 的 `GET /api/tickets/segments?id=136&lang=en` 返回 `segments: []`，
// 排障时只能靠交付 .md 与 gate_warnings 反推"哪几行没译出"。
//
// 为什么用源码扫描而不是真跑：`extractSegments` 需要一个带真实 store 的 Server，
// 而 internal/api 的测试里没有造 store 的先例（本包测试全是纯函数）。
// 本仓同类先例＝`model_for_stage_test.go` 对 `resolveModelForStage(` 出现次数的扫描锁。
// 代价：函数被整体重写（改名/搬包）时这条会红——那时请连同本注释一起更新，别直接删。
// ============================================
package api

import (
	"os"
	"strings"
	"testing"
)

// extractFuncBody 从 editor.go 里截出 `func (s *Server) extractSegments(` 的函数体源码。
// 截到下一个行首 `func ` 为止（本包函数之间不嵌套，够用）。
func extractFuncBody(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("editor.go")
	if err != nil {
		t.Fatalf("读取 editor.go 失败（本锁依赖包目录为工作目录）：%v", err)
	}
	src := string(raw)
	const sig = "func (s *Server) extractSegments("
	i := strings.Index(src, sig)
	if i < 0 {
		t.Fatalf("editor.go 里找不到 %s —— 函数被改名/搬走了？本锁需要同步更新", sig)
	}
	rest := src[i:]
	if j := strings.Index(rest[len(sig):], "\nfunc "); j >= 0 {
		rest = rest[:len(sig)+j]
	}
	return rest
}

// TestExtractSegmentsFallsBackToTicketSegments 图片/音频工单必须能回退读 ticket_segments 真值表。
func TestExtractSegmentsFallsBackToTicketSegments(t *testing.T) {
	body := extractFuncBody(t)

	call := strings.Index(body, "s.segmentsFromStore(t, lang)")
	if call < 0 {
		t.Fatalf("extractSegments 里没有 segmentsFromStore 调用 ⇒ 图片/音频/纯文案工单的逐段对照又变成空表（工单 136 的复发形态）")
	}
	// 位置判据：回退必须落在「最后的 return nil, false」**之前**——
	// 写在后面就是死代码（前面已经 return 了），而源码扫描看不出死活，只能靠位置钉。
	lastBail := strings.LastIndex(body, "return nil, false")
	if lastBail < 0 {
		t.Fatalf("extractSegments 里找不到 `return nil, false` 兜底出口 —— 函数被重写了？本锁需要同步更新")
	}
	if call > lastBail {
		t.Fatalf("segmentsFromStore 出现在最后的 `return nil, false` 之后 ⇒ 是死代码，图片工单仍然拿不到逐段真值")
	}
	// 反向对照：对照表解析（parseAlignedFile）必须仍然**排在**真值回退之前。
	// 对照表是客户实际拿到的产物（可能被人工改过），能解析就以它为准；
	// 若哪天有人把真值回退提到最前面，这条会红——那是行为变更，得显式决策而不是顺手。
	aligned := strings.Index(body, "parseAlignedFile(")
	if aligned < 0 {
		t.Fatalf("extractSegments 里找不到 parseAlignedFile 调用 ⇒ 对照表口径被整体拆掉了")
	}
	if aligned > call {
		t.Errorf("parseAlignedFile 必须排在 segmentsFromStore 之前（对照表优先、真值兜底），实得 aligned=%d fallback=%d", aligned, call)
	}
}
