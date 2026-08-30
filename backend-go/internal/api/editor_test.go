// ============ 本文件职责中文说明 ============
// 对照编辑器段落解析相关纯函数单元测试：
//   - TestSplitLines：换行切分与空行过滤、空串保底
//   - TestLocateColumns：表头列定位（显式/回退）
//   - TestExtractTextSegments / TestExtractTextSegmentsUnaligned：文本工单逐段对齐（含译文短于源文时的空段补位）
// =============================================
package api

import (
	"testing"

	"translator/internal/store"
)

// TestSplitLines 校验 splitLines：多行符切分（\n/\r\n）、过滤空行、空串保底返回单段。
func TestSplitLines(t *testing.T) {
	got := splitLines("a\n\nb\r\nc")
	if len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Fatalf("splitLines wrong: %#v", got)
	}
	if got := splitLines(""); len(got) != 1 {
		t.Fatalf("splitLines empty should keep one: %#v", got)
	}
}

// TestLocateColumns 校验 locateColumns：显式表头定位、未命中目标语言时回退下一列、缺源文表头时默认首列。
func TestLocateColumns(t *testing.T) {
	src, tgt := locateColumns([]string{"source_text", "en", "ja"}, "en")
	if src != 0 || tgt != 1 {
		t.Fatalf("locateColumns explicit header wrong: src=%d tgt=%d", src, tgt)
	}
	src, tgt = locateColumns([]string{"源文", "en"}, "ja")
	if src != 0 || tgt != 1 {
		t.Fatalf("locateColumns fallback wrong: src=%d tgt=%d", src, tgt)
	}
	if src, _ := locateColumns([]string{"a", "b"}, "en"); src != 0 {
		t.Fatalf("locateColumns no source header should default to 0, got %d", src)
	}
}

// TestExtractTextSegments 校验 extractTextSegments：源文/译文按行对齐，逐段 Source/Target 正确映射。
func TestExtractTextSegments(t *testing.T) {
	tk := &store.Ticket{
		SourceText: "你好\n世界",
		FinalResult: `{"translations":{"en":"Hello\nWorld"}}`,
	}
	segs := extractTextSegments(tk, "en")
	if len(segs) != 2 {
		t.Fatalf("expected 2 segments, got %d", len(segs))
	}
	if segs[0].Source != "你好" || segs[0].Target != "Hello" {
		t.Fatalf("seg0 mismatch: %#v", segs[0])
	}
	if segs[1].Source != "世界" || segs[1].Target != "World" {
		t.Fatalf("seg1 mismatch: %#v", segs[1])
	}
}

// TestExtractTextSegmentsUnaligned 校验译文短于源文时，多余源文段 Target 应为空（避免越界）。
func TestExtractTextSegmentsUnaligned(t *testing.T) {
	tk := &store.Ticket{
		SourceText:  "a\nb\nc",
		FinalResult: `{"translations":{"en":"X"}}`,
	}
	segs := extractTextSegments(tk, "en")
	if len(segs) != 3 {
		t.Fatalf("expected 3 source segments, got %d", len(segs))
	}
	if segs[2].Target != "" {
		t.Fatalf("seg2 target should be empty when translation shorter, got %q", segs[2].Target)
	}
}
