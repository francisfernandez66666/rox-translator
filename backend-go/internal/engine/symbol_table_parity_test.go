// ============ symbol_table_parity_test.go · 职责说明 ============
// 「什么算图形/内容符号」只有一份清单的跨语言锁（★ 2026-10-21 UAT 收口 M9）。
//
// 为什么需要：M9 的三档判据在两侧各有一个消费者——
//
//	Go：`engine.IsSymbolOnlyLine`（送翻预填、即时翻译不送模型）；
//	Python：`ocr_translate_image.py` 的回填门（图形行整行剔除、原文交付不重画）。
//
// 两份清单漂一格不会报错：Go 认为 `⇅` 是图形、Python 不认，表现就是「即时翻译不画了、
// 文件回填还在画」，或者反过来「文件里被剔除、GateWarnings 却没数到」。这类静默分裂
// 正是本仓反复出事的形态，所以按 `fileproc/tts_lang_coverage_gate_test.go` 的立式，
// **现读 Python 源文件逐字比对**，而不是在 Go 里再抄一份期望值。
//
// 两条腿：
//
//	P1 图形清单等值：graphicSymbolRunes ↔ GRAPHIC_SYMBOLS；
//	P2 内容清单等值：contentSymbolRunes ↔ CONTENT_SYMBOLS。
//
// 取不到清单即判红（「锚点漂了」与「清单为空」必须分得开——前者更坏，会让整段静默失明）。
// =============================================
package engine

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// pySymbolListRe 抓 Python 侧的清单字面量（只认赋值行，注释里的同名示例不算）。
func pySymbolListRe(name string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)^` + name + `\s*=\s*"([^"]*)"`)
}

// readPythonSymbolList 现读脚本的清单；取不到就判红并点明锚点漂移。
func readPythonSymbolList(t *testing.T, script, name string) string {
	t.Helper()
	src, err := os.ReadFile(script)
	if err != nil {
		t.Fatalf("读脚本失败 %s: %v（脚本必须随仓库提交，同源锁才有被锁的对象）", script, err)
	}
	m := pySymbolListRe(name).FindSubmatch(src)
	if m == nil {
		t.Fatalf("%s 里找不到 `%s = \"...\"` ⇒ 锚点漂了，这条同源锁已失明", script, name)
	}
	if len(m[1]) == 0 {
		t.Fatalf("%s 的 %s 清单为空 ⇒ 判据形同虚设", script, name)
	}
	return string(m[1])
}

// TestSymbolTablesMatchPython P1/P2：Go 与 Python 两张清单**逐字等值**（含顺序）。
func TestSymbolTablesMatchPython(t *testing.T) {
	script := filepath.Join("..", "fileproc", "ocr_translate_image.py")

	gotGraphic := readPythonSymbolList(t, script, "GRAPHIC_SYMBOLS")
	if gotGraphic != graphicSymbolRunes {
		t.Fatalf("图形符号清单两侧不等值：\n Go=%q\n  Py=%q\n⇒ 一侧改忘另一侧（`⇅` 会在一边被剔除、另一边被画回图标上）",
			graphicSymbolRunes, gotGraphic)
	}
	gotContent := readPythonSymbolList(t, script, "CONTENT_SYMBOLS")
	if gotContent != contentSymbolRunes {
		t.Fatalf("内容符号清单两侧不等值：\n Go=%q\n  Py=%q\n⇒ 金额/日期行的「原样交付」判据在两侧不一致",
			contentSymbolRunes, gotContent)
	}

	// 形状自证：两张清单不许互相包含（同一码位既是图形又是内容＝分档无意义），
	// 且都必须非空——空表会让「P1/P2 等值」变成「两份空字符串相等」的恒真锁。
	if len(graphicSymbolRunes) == 0 || len(contentSymbolRunes) == 0 {
		t.Fatalf("清单为空：graphic=%d content=%d", len(graphicSymbolRunes), len(contentSymbolRunes))
	}
	for _, r := range graphicSymbolRunes {
		if containsRune(contentSymbolRunes, r) {
			t.Fatalf("码位 %q 同时出现在图形与内容两张清单里（分档自相矛盾）", r)
		}
	}
}

// containsRune 逐码位查表（不引入额外 import；strings.ContainsRune 在实现里已有消费者）。
func containsRune(table string, r rune) bool {
	for _, x := range table {
		if x == r {
			return true
		}
	}
	return false
}
