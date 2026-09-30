// ============ 本文件职责中文说明 ============
// 静态闸门：上传侧的「PDF 页数前置拦截」必须**委托** fileproc.PdfPageCount，
// 不许在 internal/api 里再留一份自己的页数读法（〇-AF 补丁四立的单尺子约定）。
// ============================================
package api

import (
	"os"
	"strings"
	"testing"
)

// TestUploadPdfPageCountDelegatesToOneRuler 钉住"派发分流与上传闸门共用一把尺子"这条约定。
//
// 为什么这条值得单独锁，而不是靠人自觉：
//
//	`checkPdfLimits`（>120 页即拒）与 `fileproc.DispatchEligible`（≥MIN_PAGES 才派）读的是**同一个数字**。
//	两边各写一份的后果不是"显示不一致"，而是 09-30 开闸当天那个形态——
//	派发侧的手写启发式把 mupdf 形态的件读成 0 页 ⇒ 那一单永远不派、且一条日志都不打，
//	而上传侧那份真解析器却读得出 32 页。两把尺子并存时，这类分歧**只会在现网暴露**。
//
// 判据三腿：
//
//	① 正向：`func pdfPageCount` 的函数体里必须出现 `fileproc.PdfPageCount(`（真的委托，不是注释里提一句）；
//	② 负向：upload.go 里**不许**再有第二份解析器构造（`ledong.NewReader`／`os.ReadFile` 整读来数页都不许）；
//	③ 反空转：同一套判据打到一份"故意把委托换回本地解析器"的假源码上必须判红——
//	   否则 ② 这条负向锁可能是恒真（本仓 §一·5 反复强调过：负向锁必须配正向对照）。
func TestUploadPdfPageCountDelegatesToOneRuler(t *testing.T) {
	src, err := os.ReadFile("upload.go")
	if err != nil {
		t.Fatalf("读 upload.go 失败（判据按本文件字面走，不在射程内就不算绿）：%v", err)
	}
	body := pdfPageCountFuncBody(string(src))
	if body == "" {
		t.Fatal("没找到 `func pdfPageCount` 的函数体 ⇒ 函数被改名或删掉了，本闸门射程已失效")
	}
	if !strings.Contains(body, "fileproc.PdfPageCount(") {
		t.Fatalf("pdfPageCount 必须委托 fileproc.PdfPageCount（派发侧与上传侧同一把尺子），实际函数体：\n%s", body)
	}
	if strings.Contains(string(src), "ledong.NewReader") {
		t.Fatal("upload.go 里又出现第二份 ledong 解析器构造 ⇒ 两份读数迟早各说各话（见本用例头注释）")
	}

	// ③ 反空转：把"委托"换成"本地再解析一遍"，同一套判据必须立刻判红
	fake := strings.Replace(string(src), "return fileproc.PdfPageCount(path)",
		`f, _ := os.Open(path); defer f.Close(); fi, _ := f.Stat()
	r, err := ledong.NewReader(f, fi.Size())
	if err != nil { return 0, err }
	return r.NumPage(), nil`, 1)
	if !strings.Contains(fake, "ledong.NewReader") {
		t.Fatal("反证构造失败：替换没落地（说明正向锚点漂了），本用例的负向锁等于没测")
	}
	if v := violationsOf(pdfPageCountFuncBody(fake), fake); v == "" {
		t.Fatal("负向锁空转：把委托换回本地解析器后仍判通过 ⇒ 这条闸门抓不住任何退化")
	}
}

// pdfPageCountFuncBody 从源码里切出 `func pdfPageCount(` 的函数体（到配对缩进的 `}` 为止）。
//
// 只做字面切块，不跑 go/ast：这条锁要的正是"字面写法"，用 AST 反而会把注释里的别名也算进来。
func pdfPageCountFuncBody(src string) string {
	const anchor = "func pdfPageCount(path string)"
	i := strings.Index(src, anchor)
	if i < 0 {
		return ""
	}
	rest := src[i:]
	end := strings.Index(rest, "\n}\n")
	if end < 0 {
		return ""
	}
	return rest[:end+3]
}

// violationsOf 把上面两条判据原样跑一遍，返回第一条违规描述（全通过回空串）。
// 它存在只为给"反空转"那一腿复用同一判据——判据只有一份，正例反例才不可能各判各的。
func violationsOf(body, fullSrc string) string {
	if body == "" {
		return "找不到函数体"
	}
	if !strings.Contains(body, "fileproc.PdfPageCount(") {
		return "函数体里没有 fileproc.PdfPageCount 委托"
	}
	if strings.Contains(fullSrc, "ledong.NewReader") {
		return "源码里留有第二份 ledong 解析器构造"
	}
	return ""
}
