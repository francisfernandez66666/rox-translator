// ============ 本文件职责中文说明 ============
// 2026-10-26 裁决 7「交付文档只记录未翻译的内容」在**两个交付口**的接线锁。
//
// 为什么两个口都要锁：图片单（原位回填 + 旁路 .md）本轮先接上了，纯文案 .md
// （srt/vtt/json/yaml 这些格式的主交付物）没接——结果就是"同一个产品两种诚实度"：
// 客户在图片单上能读到"哪几行没翻"，在字幕单上只读到"已完成"。
// 这一族最坏的地方是**它不报错**：产物能打开、译文看起来齐全，缺的只是那句真话。
//
// 五条腿：
//
//	L1 有未译段 ⇒ 清单点名行号与原文，且 .md 文末那一节真被写进去（走 fileproc 真落盘，不测字符串拼装）；
//	L2 全译出   ⇒ 清单为空 ⇒ 文件**逐字节不变**（空清单连标题都不出）；
//	L3 空段不进清单（没有可译内容，报出来是把设计当缺陷给客户看）；
//	L4 结构腿：纯文案那一支必须"出声 + 记账"成对（本仓那条「有自动降级的链路，产物能打开不算验收」）；
//	L5 反证：把失败占位那一判摘掉 ⇒ 清单不再包含 `[翻译失败]` 段 ⇒ 证明判据真在拦（不是恒真的形状检查）。
//
// ============================================
package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"translator/internal/fileproc"
)

func TestDeliveryNotesCoverBothMouths(t *testing.T) {
	texts := []string{"第一段说明。", "第二段说明。", "第三段说明。", ""}

	// L1：第二、三段没译出来（一个是空、一个是失败占位）
	tr := map[string]string{
		"第一段说明。": "Note one.",
		"第二段说明。": "",
		"第三段说明。": "[翻译失败]",
		// 第四段是空段：不进清单
	}
	notes := untranslatedNotesFromSegments(texts, tr)
	if len(notes) != 2 {
		t.Fatalf("应恰好导出 2 条未译段（空段不计、已译段不计），实得 %+v", notes)
	}
	if notes[0].Line != 2 || notes[0].Source != "第二段说明。" {
		t.Errorf("清单必须点名行号与原文（客户要能定位到具体那一段），实得 %+v", notes[0])
	}
	if notes[1].Line != 3 {
		t.Errorf("失败占位那一行没进清单 ⇒ 判据没在拦占位，实得 %+v", notes[1])
	}
	for _, n := range notes {
		if !strings.Contains(n.Reason, "上游") {
			t.Errorf("成因必须点名族（上游未返回／输出被截断），不许写一句模糊的「部分未译出」，实得 %q", n.Reason)
		}
	}
	// 真落盘一次：那一节必须出现在文末
	dir := t.TempDir()
	p := filepath.Join(dir, "a-en.md")
	if err := fileproc.WriteTranslationMd(p, texts, tr); err != nil {
		t.Fatalf("写 .md: %v", err)
	}
	if err := fileproc.AppendUntranslatedNotes(p, notes); err != nil {
		t.Fatalf("追加那一节: %v", err)
	}
	body := string(readGateFileForDelivery(t, p))
	if !strings.Contains(body, "## 未译出内容（共 2 行）") {
		t.Errorf("交付文档文末缺那一节，实得 %q", body)
	}
	if !strings.Contains(body, "第三段说明。") {
		t.Errorf("未译段的原文必须在清单里（.md 按原文兜底，客户要能认出是哪一段），实得 %q", body)
	}

	// L2：全部译出 ⇒ 空清单 ⇒ 文件逐字节不变
	clean := map[string]string{"第一段说明。": "Note one.", "第二段说明。": "Note two.", "第三段说明。": "Note three.", "": ""}
	if n := untranslatedNotesFromSegments(texts, clean); len(n) != 0 {
		t.Fatalf("全译出却导出 %d 条清单（空态伪装成检查过那一族）：%+v", len(n), n)
	}
	p2 := filepath.Join(dir, "b-en.md")
	if err := fileproc.WriteTranslationMd(p2, texts, clean); err != nil {
		t.Fatalf("写干净 .md: %v", err)
	}
	before := string(readGateFileForDelivery(t, p2))
	if err := fileproc.AppendUntranslatedNotes(p2, nil); err != nil {
		t.Fatalf("空清单追加不该报错: %v", err)
	}
	if after := string(readGateFileForDelivery(t, p2)); after != before {
		t.Errorf("空清单却改写了交付文档（凭空多一节「未译出：0 行」），前后差异 %q → %q", before, after)
	}
}

// L3：只含空段的输入 ⇒ 零清单、零上游请求（这一条同时挡"把设计当缺陷报给客户"）。
func TestDeliveryNotesIgnoreEmptySegments(t *testing.T) {
	texts := []string{"", "  ", "\n"}
	if n := untranslatedNotesFromSegments(texts, map[string]string{}); len(n) != 0 {
		t.Errorf("空段一律不许进未译出清单，实得 %+v", n)
	}
}

// L4 结构腿：纯文案那一支必须"出声 + 记账"成对，且图片那一支的两条记账都还在。
// 判据读源文件（剥注释后按字面量数）——这类"有 WARN 没 gateWarnings"的形态在界面上一行异常都没有。
func TestDeliveryWarningsArePairedWithAccounting(t *testing.T) {
	src := string(readGateFileForDelivery(t, "file.go"))
	code := stripGoCommentsForDelivery(src)
	// 纯文案口：notes 追加失败与"有 N 段未译出"两条都必须进 gateWarnings
	for _, needle := range []string{
		`gateWarnings = append(gateWarnings, fmt.Sprintf("%s 交付文档的「未译出内容」一节没能写入`,
		`gateWarnings = append(gateWarnings, fmt.Sprintf("%s 有 %d/%d 段未译出`,
		`gateWarnings = append(gateWarnings, fmt.Sprintf("%s 上游连续未返回可用译文`,
		"unresolved, imgUpstreamDead := e.retryImageSegments(ctx, texts, tr, lc)",
	} {
		if !strings.Contains(code, strings.TrimSpace(needle)) {
			t.Errorf("file.go 里缺这条接线（交付面退化必须被记账，只出声不算）：%s", needle)
		}
	}
	// 反证：那一节写成功时不许只留日志——两条 append 都得在，缺一条就是"半截诚实"
	if n := strings.Count(code, "段未译出"); n < 1 {
		t.Errorf("未译出段数的记账读不到（n=%d）⇒ 上面几条可能是字符串漂了的空转", n)
	}
}

// stripGoCommentsForDelivery 剥掉整行注释与行尾注释（负向计数不被自己的说明文字喂饱——本仓老坑）。
func stripGoCommentsForDelivery(src string) string {
	var out []string
	for _, line := range strings.Split(src, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "//") || strings.HasPrefix(t, "*") || strings.HasPrefix(t, "/*") {
			continue
		}
		if i := strings.Index(line, "//"); i >= 0 && !strings.Contains(line[:i], `"`) {
			line = line[:i]
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// readGateFileForDelivery 读文件正文（读不到即红，不把"没读到"当"没有该写法"）。
func readGateFileForDelivery(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("读 %s: %v", p, err)
	}
	return b
}
