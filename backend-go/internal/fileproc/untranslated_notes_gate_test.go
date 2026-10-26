// ============ 本文件职责中文说明 ============
// 2026-10-26 裁决 7「交付文档只记录未翻译内容」那一节的写法锁。
//
// 这一节是客户在交付物里**唯一**能看见"哪些行没翻出来"的面：
// 少写一节＝图上还是中文而文档里一行提示都没有（本仓「有自动降级的链路，产物能打开不算验收」那一族）；
// 多写一节＝空清单也印一个"未译出：0 行"，把没有对象的话摆给客户看。
// 三档判据＋两枚自证（判据函数被喂错形状时必须判红，否则恒绿属空转）。
// ============================================
package fileproc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// notesSectionCount 现算交付文档里「未译出内容」那一节的标题出现次数。
// 参数：md=文档正文；wantHead=是否期望出现标题（自证用同一把尺子量两侧）。
func notesSectionCount(md string) int {
	return strings.Count(md, "## 未译出内容（")
}

func TestUntranslatedNotesEmptyListWritesNothing(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a-en.md")
	body := "The first note is here.\n\nThe second note is here."
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("写底稿: %v", err)
	}
	if err := AppendUntranslatedNotes(p, nil); err != nil {
		t.Fatalf("空清单不该报错: %v", err)
	}
	got, _ := os.ReadFile(p)
	if string(got) != body {
		t.Errorf("空清单时交付文档必须逐字节不变（不许凭空多一节「未译出：0 行」），实得 %q", got)
	}
	if n := notesSectionCount(string(got)); n != 0 {
		t.Errorf("标题出现 %d 次，期望 0", n)
	}
}

func TestUntranslatedNotesListsEachLineWithCause(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "b-en.md")
	// 刻意让底稿**末尾没有换行**：标题若与最后一句译文粘在同一行，markdown 里就不再是标题（客户看不见这一节）。
	body := "The first note is here."
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("写底稿: %v", err)
	}
	items := []UntranslatedItem{
		{Line: 3, Source: "规格表 A-100", Reason: "上游未返回可用译文（补译 3 轮仍为空），已按原样保留"},
		{Line: 7, Source: "请勿拆卸", Reason: "模型补译 3 轮仍无可用输出，已按原样保留"},
	}
	if err := AppendUntranslatedNotes(p, items); err != nil {
		t.Fatalf("追加那一节: %v", err)
	}
	got := string(func() []byte { b, _ := os.ReadFile(p); return b }())
	if n := notesSectionCount(got); n != 1 {
		t.Fatalf("标题应恰好出现 1 次，实得 %d 次，正文 %q", n, got)
	}
	if !strings.Contains(got, "共 2 行") {
		t.Errorf("标题必须点名共几行（与计数同一把尺子），实得 %q", got)
	}
	for _, it := range items {
		if !strings.Contains(got, it.Source) || !strings.Contains(got, it.Reason) {
			t.Errorf("第 %d 行的原文或成因没落进那一节 ⇒ 客户定位不到具体哪一行: %q", it.Line, got)
		}
	}
	// 标题必须**独立成行**，且与最后一句译文之间隔开（粘行＝markdown 里出不了标题）。
	lines := strings.Split(got, "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, "## 未译出内容") {
			if i == 0 || strings.TrimSpace(lines[i-1]) != "" {
				t.Errorf("标题前必须有一行空白隔开，实得上一行 %q", lines[i-1])
			}
			break
		}
	}
	if !strings.HasPrefix(got, body) {
		t.Errorf("原有译文一字不许动（只追加不重写），实得 %q", got)
	}
}

// TestUntranslatedNotesCounterIsReal 判据自证：同一把尺子喂"该出现"与"不该出现"两侧。
// 少了这一条，上面两腿可能测的都是一个恒 0 的计数函数（本仓反复踩过的空转锁形态）。
func TestUntranslatedNotesCounterIsReal(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "c-en.md")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatalf("写底稿: %v", err)
	}
	if err := AppendUntranslatedNotes(p, []UntranslatedItem{{Line: 1, Source: "甲", Reason: "乙"}}); err != nil {
		t.Fatalf("追加: %v", err)
	}
	b, _ := os.ReadFile(p)
	if notesSectionCount(string(b)) != 1 {
		t.Fatalf("自证失败：真写了一节而计数回 %d ⇒ 标题锚漂了，前两条腿全在空转", notesSectionCount(string(b)))
	}
	if err := AppendUntranslatedNotes(p, nil); err != nil {
		t.Fatalf("空清单: %v", err)
	}
	b2, _ := os.ReadFile(p)
	if notesSectionCount(string(b2)) != 1 {
		t.Errorf("自证失败：空清单让计数变成 %d（期望仍是 1）⇒ 那一节被重复追加或整体重写", notesSectionCount(string(b2)))
	}
}
