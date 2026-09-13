// ============ dual_mode_test.go · 职责说明 ============
// 工单双模式（2026-09-13）engine 侧纯函数单测：
// optionString 读取、anydocSourceExt 判定。
// =============================================
package engine

import (
	"testing"

	"translator/internal/fileproc"
)

func TestOptionString(t *testing.T) {
	if got := optionString("text"); got != "text" {
		t.Errorf("optionString(string)=%q", got)
	}
	if got := optionString(nil); got != "" {
		t.Errorf("optionString(nil)=%q, want empty", got)
	}
	if got := optionString(42); got != "" {
		t.Errorf("optionString(int)=%q, want empty", got)
	}
}

func TestAnydocSourceExt(t *testing.T) {
	for _, e := range []string{".doc", ".odt", ".rtf", ".epub", ".pdf"} {
		if !anydocSourceExt(e) {
			t.Errorf("纯文案模式应经 anydoc 提取: %s", e)
		}
	}
	for _, e := range []string{".docx", ".pptx", ".xlsx", ".md", ".txt", ".csv", ".srt", ".json", ".yaml", ".yml", ".vtt"} {
		if anydocSourceExt(e) {
			t.Errorf("原生管线格式不应走 anydoc: %s", e)
		}
	}
	// 引擎判定与 fileproc 独占表一致性（pdf 为引擎侧额外纳入）
	if fileproc.AnydocFormats[".pdf"] {
		t.Errorf(".pdf 不应在 fileproc 独占白名单（Go 原生链已支持，引擎侧单独判定）")
	}
}
