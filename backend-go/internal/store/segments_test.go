// ============ segments_test.go · 职责说明 ============
// ticket_segments 逐段对照真值表数据层单元测试。
// 守护 2026-09-18「大量块不匹配」修复的落库契约：
//  1. 往返一致：按提取顺序写入，读出段序号升序、源文/译文一一对应；
//  2. 重跑幂等：段数变少后不得残留尾部孤儿行（否则对照里会「多出一截不存在的段」）；
//  3. 多文件隔离：同一工单同一语言下不同源文件的段互不覆盖；
//  4. 未译出段：target 允许为空（保持段序与源文一侧严格对齐，不跳号）；
//  5. 无数据：GetTicketSegments 返回 (nil, nil) 而非错误（调用方据此回退旧口径）。
//
// 使用内存 SQLite（:memory:）构建独立 Store 实例，不依赖业务数据。
// ========================================
package store

import (
	"testing"
)

func TestTicketSegmentsRoundTrip(t *testing.T) {
	s := newTestStore(t)
	segs := []TicketSegment{
		{SegIndex: 0, Source: "翻译助手 2.0 · 业务集成方案", Target: "Translation Assistant 2.0 · Business Integration Solutions"},
		{SegIndex: 1, Source: "0.3 接进哪四个业务系统", Target: "0.3 Which four business systems should be integrated?"},
		{SegIndex: 2, Source: "门店随查随用，不靠邮件发 PDF", Target: "Stores can access and use the information immediately; no need to rely on email PDF"},
		{SegIndex: 3, Source: "#", Target: ""}, // 未译出：target 允许为空
	}
	if err := s.SaveTicketSegments(7, 42, "/data/in.pdf", "en", segs); err != nil {
		t.Fatalf("写入逐段对照失败: %v", err)
	}
	got, err := s.GetTicketSegments(42, "/data/in.pdf", "en")
	if err != nil {
		t.Fatalf("读取逐段对照失败: %v", err)
	}
	if len(got) != len(segs) {
		t.Fatalf("段数不符: got=%d want=%d", len(got), len(segs))
	}
	for i, g := range got {
		if g.SegIndex != i {
			t.Errorf("第%d条 seg_index=%d，应为 %d（必须按提取顺序连续）", i, g.SegIndex, i)
		}
		if g.Source != segs[i].Source || g.Target != segs[i].Target {
			t.Errorf("第%d条内容不符:\n  got  src=%q tgt=%q\n  want src=%q tgt=%q",
				i, g.Source, g.Target, segs[i].Source, segs[i].Target)
		}
		if g.TenantID != 7 || g.TicketID != 42 || g.Lang != "en" {
			t.Errorf("第%d条归属字段不符: tenant=%d ticket=%d lang=%s", i, g.TenantID, g.TicketID, g.Lang)
		}
		if g.FilePath != "/data/in.pdf" {
			t.Errorf("第%d条 file_path=%q，应为源文件路径", i, g.FilePath)
		}
	}
}

// 回归守护：重跑同一工单（段落变少）后不得残留旧轮次的尾部孤儿行。
// 旧实现若用「只插不删」，对照里会出现「多出一截不存在的段」。
func TestTicketSegmentsOverwriteRemovesOrphans(t *testing.T) {
	s := newTestStore(t)
	first := []TicketSegment{
		{SegIndex: 0, Source: "甲", Target: "A"},
		{SegIndex: 1, Source: "乙", Target: "B"},
		{SegIndex: 2, Source: "丙", Target: "C"},
	}
	if err := s.SaveTicketSegments(1, 9, "/f.pdf", "en", first); err != nil {
		t.Fatalf("首轮写入失败: %v", err)
	}
	// 重跑：重新解析后只有 2 段（比如上一轮是识别误判出的多余块）
	second := []TicketSegment{
		{SegIndex: 0, Source: "甲", Target: "A2"},
		{SegIndex: 1, Source: "乙", Target: "B2"},
	}
	if err := s.SaveTicketSegments(1, 9, "/f.pdf", "en", second); err != nil {
		t.Fatalf("重跑写入失败: %v", err)
	}
	got, err := s.GetTicketSegments(9, "/f.pdf", "en")
	if err != nil {
		t.Fatalf("重跑后读取失败: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("重跑后残留孤儿行: 段数=%d，应为 2（旧轮次的 seg_index=2 必须被清掉）", len(got))
	}
	if got[0].Target != "A2" || got[1].Target != "B2" {
		t.Errorf("重跑后内容未更新: %+v", got)
	}
}

// 多文件工单：同一工单同一语言下，不同源文件的段互不覆盖。
func TestTicketSegmentsIsolatedPerFile(t *testing.T) {
	s := newTestStore(t)
	a := []TicketSegment{{SegIndex: 0, Source: "文件A第1段", Target: "FileA seg 1"}}
	b := []TicketSegment{
		{SegIndex: 0, Source: "文件B第1段", Target: "FileB seg 1"},
		{SegIndex: 1, Source: "文件B第2段", Target: "FileB seg 2"},
	}
	if err := s.SaveTicketSegments(1, 5, "/a.pdf", "en", a); err != nil {
		t.Fatalf("写文件A失败: %v", err)
	}
	if err := s.SaveTicketSegments(1, 5, "/b.pdf", "en", b); err != nil {
		t.Fatalf("写文件B失败: %v", err)
	}
	ga, _ := s.GetTicketSegments(5, "/a.pdf", "en")
	gb, _ := s.GetTicketSegments(5, "/b.pdf", "en")
	if len(ga) != 1 || ga[0].Source != "文件A第1段" {
		t.Errorf("文件A 的段被覆盖: %+v", ga)
	}
	if len(gb) != 2 {
		t.Errorf("文件B 的段数异常: %d", len(gb))
	}
	// 不带 file_path 条件读取：应能取到该工单该语言全部文件段（供调用方自审）
	all, _ := s.GetTicketSegments(5, "", "en")
	if len(all) != 3 {
		t.Errorf("不限文件读取应得 3 段，实际 %d", len(all))
	}
}

// 无数据不是错误：返回 (nil, nil)，调用方据此回退旧口径。
func TestTicketSegmentsMissingReturnsNilNotError(t *testing.T) {
	s := newTestStore(t)
	got, err := s.GetTicketSegments(999, "/none.pdf", "en")
	if err != nil {
		t.Fatalf("无数据不应返回错误: %v", err)
	}
	if got != nil {
		t.Errorf("无数据应返回 nil 切片，实际 %+v", got)
	}
}
