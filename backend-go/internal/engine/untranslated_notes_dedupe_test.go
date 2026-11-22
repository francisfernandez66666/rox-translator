// ============ untranslated_notes_dedupe_test.go · 职责说明（2026-11-22 tv13 终轮）============
// 交付诚实性：一行表格拆多段时，清单逐段点名（不丢信息），但「共 N 行」必须按
// 物理行去重计数，使标题与正文实际回落行数同源，不再出现 52 vs 26 的记账失真。
// ================================================
package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"translator/internal/fileproc"
)

func TestUntranslatedNotesKeepEverySegment(t *testing.T) {
	texts := []string{"65–120 万", "45–80 万", "第二行原文"}
	lineMap := map[string]int{"65–120 万": 150, "45–80 万": 150, "第二行原文": 157}
	notes := untranslatedNotesFromSegments(texts, map[string]string{}, lineMap)
	if len(notes) != 3 {
		t.Fatalf("每个未译段都要点名，实得 %d 条：%+v", len(notes), notes)
	}
	if notes[0].Line != 150 || notes[1].Line != 150 || notes[2].Line != 157 {
		t.Fatalf("行号不对：%+v", notes)
	}
	for _, n := range notes {
		if n.Unit != "行" {
			t.Errorf("有映射时应标 Unit=行，实得 %+v", n)
		}
	}
}

func TestUntranslatedNotesNoMapKeepsSegments(t *testing.T) {
	texts := []string{"65–120 万", "45–80 万"}
	notes := untranslatedNotesFromSegments(texts, map[string]string{})
	if len(notes) != 2 {
		t.Fatalf("无物理行映射时按段计数，实得 %d", len(notes))
	}
	for i, n := range notes {
		if n.Unit != "段" || n.Line != i+1 {
			t.Errorf("第 %d 条=%+v", i, n)
		}
	}
}

func TestUntranslatedNotesHeaderCountsUniqueLines(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "out.md")
	if err := os.WriteFile(p, []byte("# 译文\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	items := []fileproc.UntranslatedItem{
		{Line: 150, Source: "65–120 万", Unit: "行"},
		{Line: 150, Source: "4–8 周", Unit: "行"},
		{Line: 157, Source: "45–80 万", Unit: "行"},
	}
	if err := fileproc.AppendUntranslatedNotes(p, items); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	body := string(b)
	if !strings.Contains(body, "## 未译出内容（共 2 行）") {
		t.Fatalf("标题必须按唯一物理行计数（2 行），实得：%s", body)
	}
	if !strings.Contains(body, "第 150 行：原文「4–8 周」") {
		t.Fatalf("同一物理行的第二个未译段仍须逐条点名，实得：%s", body)
	}
}
