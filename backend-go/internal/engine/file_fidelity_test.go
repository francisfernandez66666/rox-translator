// ============ file_fidelity_test.go 职责说明 ============
// 结构指纹保真闸门（P1 整改：漏译与格式破坏静默通过）的纯函数断言：
//   - structureFidelityWarnings：构造「行数变了 / 表格 '|' 少了 / 残留 87 行中文」的产物，
//     断言三类警告齐全且带具体差值；同文产物必须零告警（防噪声淹没真信号）；
//   - CJK 目标语（zh/ja/ko）不做残留中文判定（译文本就是汉字，无从区分）；
//   - limitUntranslatedSegments：未译段清单上限生效；
//   - segmentTextView：二进制产物回落视图「视图行数==段数」，未译段回填原文。
//
// 全部离线喂字符串，不读真实产物文件。
// ========================================
package engine

import (
	"strings"
	"testing"
)

// TestStructureFidelityWarningsCoversAllRegressions 复刻实测工单形态：产物比原文少 14 行、
// 表格竖线丢失、末尾仍有 87 行中文未译 ⇒ 三条警告一条都不能少，且必须带具体数值。
func TestStructureFidelityWarningsCoversAllRegressions(t *testing.T) {
	var srcB, outB strings.Builder
	// 原文：90 段正文 + 10 行两列表格（每行 3 个 '|'）+ 87 行未译中文 = 187 行
	for i := 0; i < 90; i++ {
		srcB.WriteString("第" + itoaTest(i) + "段正文内容，说明产品能力。\n")
	}
	for i := 0; i < 10; i++ {
		srcB.WriteString("| 指标 | 数值 |\n")
	}
	for i := 0; i < 87; i++ {
		srcB.WriteString("未翻译的中文段落" + itoaTest(i) + "。\n")
	}
	// 产物：正文被吞掉 14 行（行数差 -14）、表格降级成散文（'|' 全丢）、87 行中文原样残留
	for i := 0; i < 76; i++ {
		outB.WriteString("Paragraph " + itoaTest(i) + " describing product capability.\n")
	}
	for i := 0; i < 10; i++ {
		outB.WriteString("指标 数值\n")
	}
	for i := 0; i < 87; i++ {
		outB.WriteString("未翻译的中文段落" + itoaTest(i) + "。\n")
	}

	w := structureFidelityWarnings("en", srcB.String(), outB.String())
	joined := strings.Join(w, "\n")
	for _, want := range []string{
		"产物行数与原文不一致：原文 188 行 / 产物 174 行（差 -14）",
		"表格 '|' 数与原文不一致：原文 30 / 产物 0（差 -30）",
		// 97 = 87 行漏译 + 10 行「降级成中文散文」的表格行，都属残留源语
		"产物仍有 97 行含中文（占产物非空行 56.1%",
		"疑似漏译或 KB 脏行命中",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("警告缺少期望片段 %q，实际：\n%s", want, joined)
		}
	}
	if len(w) < 3 {
		t.Fatalf("三类问题至少三条警告，got %d：\n%s", len(w), joined)
	}
}

// TestStructureFidelityWarningsSilentOnFaithfulOutput 忠实产物（行数/标记/无中文残留）必须零告警：
// 本闸门是「差值不为 0 就提示」，任何误报都会让 100 条警告里混进真信号变得无从阅读。
func TestStructureFidelityWarningsSilentOnFaithfulOutput(t *testing.T) {
	src := "# 标题\n\n**加粗**说明与 `代码` 引用。\n\n| A | B |\n|---|---|\n| 1 | 2 |\n\n```go\nfmt.Println(1)\n```\n\n~~删除~~\n"
	out := "# Title\n\n**Bold** text with `code` refs.\n\n| A | B |\n|---|---|\n| 1 | 2 |\n\n```go\nfmt.Println(1)\n```\n\n~~strike~~\n"
	if w := structureFidelityWarnings("en", src, out); len(w) != 0 {
		t.Fatalf("忠实产物不应告警：%v", w)
	}
}

// TestStructureFidelityWarningsSkipsCJKTarget 中文/日文目标语的译文本就是汉字，
// 残留中文判定必须跳过（否则每份中文工单都会误报漏译）。
func TestStructureFidelityWarningsSkipsCJKTarget(t *testing.T) {
	src := strings.Repeat("English source paragraph text.\n", 50)
	out := strings.Repeat("英文原文照抄未翻译的中文段落。\n", 50)
	for _, lang := range []string{"ja", "ko", "zh_hant"} {
		if w := structureFidelityWarnings(lang, src, out); len(w) != 0 {
			t.Fatalf("%s 目标语不应出残留中文告警：%v", lang, w)
		}
	}
	if w := structureFidelityWarnings("en", src, out); len(w) == 0 {
		t.Fatal("en 目标语下的中文残留必须告警")
	}
}

// TestLimitUntranslatedSegmentsCap 未译段清单上限：超限只留前 N 条（防 payload 爆炸），
// 未超限必须原样返回（不能截掉 QA 需要的最后一段）。
func TestLimitUntranslatedSegmentsCap(t *testing.T) {
	list := make([]string, untranslatedSegmentsCap+50)
	for i := range list {
		list[i] = "段" + itoaTest(i)
	}
	got := limitUntranslatedSegments(list)
	if len(got) != untranslatedSegmentsCap {
		t.Fatalf("上限未生效：%d != %d", len(got), untranslatedSegmentsCap)
	}
	if got[0] != list[0] || got[untranslatedSegmentsCap-1] != list[untranslatedSegmentsCap-1] {
		t.Fatal("截断必须保序取前 N 条")
	}
	short := []string{"a", "b"}
	if out := limitUntranslatedSegments(short); len(out) != 2 {
		t.Fatalf("未超限时应原样返回：%v", out)
	}
}

// TestSegmentTextViewFlattensAndKeepsSourceForMissing 段级视图：
// 行数严格等于段数（段内换行折成空格，避免二进制格式的假阳性行数告警）、
// 未译段回填原文（让残留中文检查抓到它）。
func TestSegmentTextViewFlattensAndKeepsSourceForMissing(t *testing.T) {
	texts := []string{"第一行\n第二行", "缺失段"}
	tr := map[string]string{"第一行\n第二行": "First line\nsecond line"}
	srcView, outView := segmentTextView(texts, tr)
	if strings.Count(srcView, "\n") != 1 || strings.Count(outView, "\n") != 1 {
		t.Fatalf("视图行数必须等于段数：src=%q out=%q", srcView, outView)
	}
	if !strings.Contains(outView, "缺失段") {
		t.Fatalf("未译段应回填原文以便告警抓到：%q", outView)
	}
}

// itoaTest 测试内自用的整数转串（不复用被测包内私有实现，避免断言与被测代码同源于同一 bug）。
func itoaTest(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
