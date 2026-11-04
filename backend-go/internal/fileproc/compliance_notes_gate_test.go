// ============ 本文件职责中文说明 ============
// ★ 2026-11-03 立（用户裁决②：「交付面写明跳过的禁词」的落盘腿）。
// `fileproc/text_formats.go` 的 `AppendComplianceNotes` 是文件通道交付文档末尾那一节的唯一写点，
// 本文件钉住它的四条不变式：
//
//	E1 空清单 ⇒ **什么都不写**（连标题都不出）。与 AppendUntranslatedNotes 同一条理由：
//	   "合规跳过：0 处"是一句没有对象的话，而这一节的用途是让人定位到具体那一段。
//	E2 逐段点名：每一段的行号与命中词项都要出现（用户令「写明跳过的禁词」的落盘面）。
//	E3 处置说明**由调用方传入并逐字落盘**，本包不造句——
//	   旧形态在这里有一句 `lead = "内容合规审核：该处未翻译"` 的兜底，它与
//	   `engine.SensitiveBlockedLeadForFile` 只差半句，是**第二份措辞**（改话术只会改到一处）。
//	   E3b 因此配一条负向锁：本包源码里不许出现那句处置话术的字面量。
//	E4 与「未译出内容」**分两节**，不许合并（两族成因不同、处置动作不同，
//	   混成一节就是把设计给客户看成缺陷）。
//
// ⚠️ E3b 是"判据自伤"高危区，写法上刻意避开两处：
//   - 不在报错消息里拼那串字面量（本仓踩过：负向锁被自己新写的注释/消息喂饱）；
//   - 只扫**代码行**（剥整行注释），因为 text_formats.go 的注释里正解释着"旧形态写了什么"，
//     把那句记录删掉等于让下一个人不知道为什么不能加回去。
//
// ============================================
package fileproc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cnTempMD 写一份"已交付的译文"当底稿。
func cnTempMD(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "out-en.md")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("写底稿失败: %v", err)
	}
	return p
}

// TestAppendComplianceNotesEmptyWritesNothing E1：空清单 ⇒ 文件逐字节不变。
func TestAppendComplianceNotesEmptyWritesNothing(t *testing.T) {
	const orig = "# 说明书\n\n第一段。\n第二段。\n"
	p := cnTempMD(t, orig)
	if err := AppendComplianceNotes(p, nil); err != nil {
		t.Fatalf("空清单不该报错: %v", err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("读回失败: %v", err)
	}
	if string(b) != orig {
		t.Fatalf("空清单必须**逐字节不动**（连标题都不写），实得:\n%s", string(b))
	}
	if strings.Contains(string(b), "合规跳过") {
		t.Errorf("空清单出现了「合规跳过」标题 ⇒ 客户会读到一句没有对象的话")
	}
}

// TestAppendComplianceNotesNamesEveryItem E2＋E3：逐段点名，且处置说明逐字落盘。
func TestAppendComplianceNotesNamesEveryItem(t *testing.T) {
	// 处置说明刻意用一段**不像产品文案**的串：它只可能来自调用方，
	// 所以一旦本包又自己造句，这条断言必然抓不住——E3b 的源码负向锁负责那一半。
	const lead = "LEAD-FROM-CALLER：该处未翻译，原文已按原样保留"
	p := cnTempMD(t, "# 说明书\n\n第一段。\n第二段。\n第三段。\n")
	items := []ComplianceItem{
		{Line: 2, Terms: []string{"枪支"}, Lead: lead},
		{Line: 4, Terms: []string{"弹药", "爆炸物"}, Lead: lead},
	}
	if err := AppendComplianceNotes(p, items); err != nil {
		t.Fatalf("追加失败: %v", err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("读回失败: %v", err)
	}
	out := string(b)

	// 底稿必须原样保留在前面（这一节是**追加**，不许重写全文）。
	if !strings.HasPrefix(out, "# 说明书\n") {
		t.Errorf("底稿被改写了：这一节只能是追加，重写会把已交付译文弄丢。实得:\n%s", out)
	}
	if !strings.Contains(out, "## 合规跳过（共 2 处）") {
		t.Errorf("标题必须带处数（共 2 处），实得:\n%s", out)
	}
	for _, want := range []string{"第 2 行", "第 4 行", "枪支", "弹药", "爆炸物"} {
		if !strings.Contains(out, want) {
			t.Errorf("缺少 %q：用户令「写明跳过的禁词」要求逐段点名，实得:\n%s", want, out)
		}
	}
	// 处置说明逐字（两句都来自同一次调用，故计数为 2）。
	if n := strings.Count(out, lead); n != 2 {
		t.Errorf("处置说明应逐字出现 2 次（每条一处），实得 %d 次:\n%s", n, out)
	}
	// 行号顺序必须与底稿一致（第 2 行在前）——否则客户按行号找不到对应段落。
	if strings.Index(out, "第 2 行") > strings.Index(out, "第 4 行") {
		t.Errorf("点名顺序必须按行号升序，实得:\n%s", out)
	}
}

// TestAppendComplianceNotesNoOwnWording E3b：本包**不许**自己写处置话术（源码负向锁）。
//
// 这条锁防的是"兜底造句"重新长回来：一句 `if lead == "" { lead = "……" }` 看起来体贴，
// 实际是在 fileproc 里存了第二份措辞——而 import 方向是 fileproc ← engine，
// 真值只能住在 engine（同 §21「占位符常量都住 engine」那一条）。
//
// 判据口径：
//   - 只扫**代码行**（剥掉整行 `//` 注释）：注释里记录"旧形态写了什么"是必要的，不算违规；
//   - 针脚用"内容合规审核"这一族前缀，它在 engine 的两条 Lead 里都在、在别处不该出现。
func TestAppendComplianceNotesNoOwnWording(t *testing.T) {
	b, err := os.ReadFile("text_formats.go")
	if err != nil {
		t.Fatalf("读 text_formats.go 失败（这一腿没跑到，不许当通过）: %v", err)
	}
	n := 0
	for i, ln := range strings.Split(string(b), "\n") {
		trimmed := strings.TrimSpace(ln)
		if strings.HasPrefix(trimmed, "//") {
			continue
		}
		if strings.Contains(trimmed, "内容合规审核") {
			n++
			t.Errorf("text_formats.go:%d 出现了处置话术字面量：%s\n"+
				"→ 措辞的唯一事实源在 engine（SensitiveSkippedLead / SensitiveBlockedLeadForFile），"+
				"在 fileproc 里再写一份就是第二份，下一批改话术只会改到其中一处", i+1, trimmed)
		}
	}
	if n > 0 {
		t.Logf("共 %d 处：本包不造句，处置说明由调用方传入（ComplianceItem.Lead）", n)
	}

	// 正向对照：同一个文件里**必须**存在本函数的标题句，
	// 否则上面那条"零命中"可能只是扫错了文件（判据失明的经典形态）。
	if !strings.Contains(string(b), "## 合规跳过（共 %d 处）") {
		t.Fatalf("连本函数的标题行都扫不到 ⇒ 这条负向锁扫错了靶子（判据失明，比红灯更坏）")
	}
}

// TestAppendComplianceNotesSeparateFromUntranslated E4：两节必须**各自独立**、不许合并。
//
// 为什么单独立一条：两族成因的处置动作完全不同（一族等上游/抬预算，一族改措辞或核词包），
// 合并之后客户只看到一坨"有些没翻"，而运营拿不到可行动的归因（AGENTS §19 三档互斥那条同族）。
func TestAppendComplianceNotesSeparateFromUntranslated(t *testing.T) {
	p := cnTempMD(t, "第一段。\n第二段。\n")
	// 先按「未译出」写一节，再按「合规跳过」写一节（顺序即真实交付链的顺序：
	// engine/file.go 先 append 未译出、再 append 合规跳过）。
	if err := AppendUntranslatedNotes(p, []UntranslatedItem{{Line: 1, Source: "第一段。", Reason: "上游未返回可用译文"}}); err != nil {
		t.Fatalf("追加未译出节失败: %v", err)
	}
	if err := AppendComplianceNotes(p, []ComplianceItem{{Line: 2, Terms: []string{"枪支"}, Lead: "L"}}); err != nil {
		t.Fatalf("追加合规跳过节失败: %v", err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("读回失败: %v", err)
	}
	out := string(b)
	if !strings.Contains(out, "## 未译出内容（共 1 行）") {
		t.Errorf("「未译出内容」那一节丢了，实得:\n%s", out)
	}
	if !strings.Contains(out, "## 合规跳过（共 1 处）") {
		t.Errorf("「合规跳过」那一节丢了，实得:\n%s", out)
	}
	// 两节各自的内容不许互相吞并：第一行只属于未译出、第二行只属于合规跳过。
	un := out[strings.Index(out, "## 未译出内容"):strings.Index(out, "## 合规跳过")]
	if strings.Contains(un, "第 2 行") {
		t.Errorf("「未译出内容」一节里混进了合规跳过的行（两节合流）:\n%s", un)
	}
}
