// ============ pdf_page_limit_gate_test.go · PDF 页数墙 400 页门禁 ============
// 2026-11-26 工单动态派发批：PDF 前置拦截的页数硬墙从 120 页放到 400 页
// （大件走分块派发，旧档是为 1G 低配机设的）。
//
// 本文件钉住三件事：
//   ① 现读 pdfPageHardLimit 恰为 400；
//   ② 边界判据：400 页放行、401 页拒绝（n > limit 的语义）；
//   ③ checkPdfLimits 的判据必须委托常量（n > pdfPageHardLimit），不许内联回 400/120 字面量。
//
// 反证腿：把假源码的常量改回 120，同一套判据必须判红。
// ============================================
package api

import (
	"regexp"
	"strings"
	"testing"
)

// pdfGateStripComments 丢掉整行 // 注释，只留会被编译的代码。
// 必须先剥：upload.go 的说明注释里就写着「>120 页即拒」，拿裸文本判会把注释当实现。
func pdfGateStripComments(src string) string {
	out := make([]string, 0)
	for _, ln := range strings.Split(src, "\n") {
		if strings.HasPrefix(strings.TrimSpace(ln), "//") {
			continue
		}
		out = append(out, ln)
	}
	return strings.Join(out, "\n")
}

// pageAllowed 模拟 checkPdfLimits 的页数判据：恰好等于上限放行，超出才拒。
func pageAllowed(n, limit int) bool {
	return n <= limit
}

// pdfPageHardLiteralRe 认常量赋值形态（只认十进制 400，不认 0x190 等绕写法）。
var pdfPageHardLiteralRe = regexp.MustCompile(`pdfPageHardLimit\s*=\s*(\d+)\b`)

// pdfInlineRe 认"判据里直接写死页数"的退化形态：n > 400 / n > 120。
var pdfInlineRe = regexp.MustCompile(`n\s*>\s*(400|120)\b`)

// pdfPageGateOK 判一段源码是否同时满足「常量=400」与「判据委托常量而非字面量」。
// 正例喂真实 upload.go 必须 true；反例喂常量改回 120 的夹具必须 false。
func pdfPageGateOK(src string) bool {
	code := pdfGateStripComments(src)
	m := pdfPageHardLiteralRe.FindStringSubmatch(code)
	if m == nil || m[1] != "400" {
		return false
	}
	if pdfInlineRe.MatchString(code) {
		return false
	}
	return strings.Contains(code, "n > pdfPageHardLimit")
}

// TestPDFPageLimitGate 钉住 400 页抬档、边界语义与"判据只认常量"的源码形态。
func TestPDFPageLimitGate(t *testing.T) {
	// ① 现读常量：恰好 400。
	if pdfPageHardLimit != 400 {
		t.Fatalf("pdfPageHardLimit = %d，期望 400（120 页旧档未抬或被改回）", pdfPageHardLimit)
	}

	// ② 边界判据：400 放行、401 拒绝（纯函数模拟 checkPdfLimits 的 n > limit）。
	if !pageAllowed(400, pdfPageHardLimit) {
		t.Errorf("400 页应放行（上限本意是 > 才拒），limit=%d", pdfPageHardLimit)
	}
	if pageAllowed(401, pdfPageHardLimit) {
		t.Errorf("401 页应拒绝，limit=%d", pdfPageHardLimit)
	}

	// ③ 源码形态：checkPdfLimits 必须委托常量，不能把 400/120 内联进判据。
	src := boundaryReadFile(t, "upload.go")
	if !pdfPageGateOK(src) {
		t.Error("upload.go 没通过「常量=400 且判据委托 pdfPageHardLimit」的门禁 ⇒ 判据漂移或内联了字面量")
	}

	// ④ 反证：把常量改回 120 的假源码必须判红。
	fake := strings.Replace(src, "pdfPageHardLimit = 400", "pdfPageHardLimit = 120", 1)
	if fake == src {
		t.Fatal("反证构造失败：upload.go 里没有 `pdfPageHardLimit = 400`（锚点漂了，③ 已扫不到东西）")
	}
	if pdfPageGateOK(fake) {
		t.Error("反证落空：常量写回 120 仍判通过 ⇒ 这条门禁抓不住回退")
	}

	// ⑤ 反证 2：判据内联成 `n > 400` 也必须判红（常量还在但判据不再委托它）。
	inlined := strings.Replace(src, "n > pdfPageHardLimit {", "n > 400 {", 1)
	if inlined == src {
		t.Fatal("反证 2 构造失败：源码里没有 `n > pdfPageHardLimit {`")
	}
	if pdfPageGateOK(inlined) {
		t.Error("反证 2 落空：判据内联 400 仍判通过 ⇒ 常量与判据可以各说各话")
	}
}
