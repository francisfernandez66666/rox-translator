// ============================================================================
// fileproc_pagecount_test.go · 〇-AF 补丁四：PDF 页数读数的闸门账
//
// 这一族用例的存在理由是一条现网事故链（★ 2026-10-01，开闸当天 fpdprobe 首跑判红）：
//
//	`DispatchEligible` 要「体积 ≥MIN_MB **且** 页数 ≥MIN_PAGES」两条腿同时成立才派。
//	旧 `pdfPageCountFast` 只有两道**手写启发式**：① 尾部 64KB 找最后一个 "/Count N"；
//	② 找不到就数 "/Type /Page"（**带空格那一种**）。
//	对 40 份现网真件复跑旧判据：**8 份读成 0 页**（mupdf 系写的是不带空格的 "/Type/Page"，
//	页树的 /Count 又不在尾部 64KB 里），**12 份多算**（页树节点 "/Type /Pages" 被前缀命中）。
//	读 0 不是显示问题——那份件**永远不派**，而旧 TryDispatch 在资格判 false 时一条日志都不打。
//	"闸门全绿、健康面 online、派发从没真跑过" 这个 09-28 / 09-29 栽过两次的形态会第三次长出来。
//
// 因此下面每条锁都写明「★反证：把 X 改坏 ⇒ 本用例立刻红」，并且**三道读数各自钉**：
// 只测最终值不够，最终值对也可能是两道错数恰好互相抵消（字体描述符的 /Count 255 就是这么顶掉真页数的）。
//
// 运行：go test -race ./internal/fileproc/ -run 'PageCount|PageMarkers|TrailerCount|SizeLeg|PagesUnreadable'
// ============================================================================
package fileproc

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// ---------------- 造件夹具 ----------------

// writePdfSample 把给定字节写成临时 PDF 并返回路径（TempDir 随用例销毁）。
func writePdfSample(t *testing.T, name string, body []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, body, 0o600); err != nil {
		t.Fatalf("写 %s 失败：%v", name, err)
	}
	return p
}

// buildRealPdf 造一份**真解析器读得动**的最小 PDF（自己算 xref 偏移，不依赖 python／mupdf）。
//
// 参数：pages=页数；tight=true 时名字写成 mupdf 系的紧凑形态 "/Type/Page"（不带空格），
//
//	tight=false 写成 Word／LibreOffice 常见的 "/Type /Page"；
//	afterPagesTail 追加在页树对象之后、xref 之前的原始字节，
//	用来把一个**假** "/Count 255"（Type0/CIDFont 字体描述符里真实存在的那种）送进尾部窗口。
//
// ★这份夹具本身就代表要修的形态：tight 版在旧代码下两道启发式全读不动（旧代码没有解析腿，
// 而标记判据只认带空格那一族），现网那 8 份读 0 的件就是这个写法。
func buildRealPdf(pages int, tight bool, afterPagesTail string) []byte {
	sep := " "
	if tight {
		sep = ""
	}
	pageType := "/Type" + sep + "/Page"   // 页面对象（要数的就是它）
	pagesType := "/Type" + sep + "/Pages" // 页树节点（多算就是把它数进去了）
	catType := "/Type" + sep + "/Catalog" // 目录节点（同上，不算页）

	buf := &bytes.Buffer{}
	fmt.Fprintf(buf, "%%PDF-1.7\n")
	offsets := make(map[int]int, pages+2)
	obj := func(id int, body string) {
		offsets[id] = buf.Len()
		fmt.Fprintf(buf, "%d 0 obj\n%s\nendobj\n", id, body)
	}
	kids := make([]string, 0, pages)
	for i := 0; i < pages; i++ {
		kids = append(kids, fmt.Sprintf("%d 0 R", 3+i))
	}
	obj(1, fmt.Sprintf("<< %s /Pages 2 0 R >>", catType))
	obj(2, fmt.Sprintf("<< %s /Kids [%s] /Count %d >>", pagesType, strings.Join(kids, " "), pages))
	for i := 0; i < pages; i++ {
		obj(3+i, fmt.Sprintf("<< %s /Parent 2 0 R /MediaBox [0 0 595 842] >>", pageType))
	}
	buf.WriteString(afterPagesTail)

	xrefAt := buf.Len()
	total := 3 + pages // 0 号空对象 + 1..(2+pages)
	fmt.Fprintf(buf, "xref\n0 %d\n0000000000 65535 f \n", total)
	for id := 1; id < total; id++ {
		// xref 条目定长 20 字节：10 位偏移 + 空格 + 5 位代号 + 空格 + 类型 + 行尾
		fmt.Fprintf(buf, "%010d 00000 n \n", offsets[id])
	}
	fmt.Fprintf(buf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", total, xrefAt)
	return buf.Bytes()
}

// ---------------- ① 解析腿：mupdf 紧凑形态必须读得出真页数 ----------------

// TestPdfPageCountReadsTightForm 钉住现网那 8 份读 0 的件的**修后读数**。
//
// 判据：一份 32 页、页面对象写成 "/Type/Page"（不带空格）的真件，解析腿与派发腿都必须回 32。
//
// ★反证：删掉 pdfPageCountFast 的第一道（解析腿）⇒ 派发腿退到启发式，任何判据偏差立刻红；
//
//	把标记判据写回「只认带空格」⇒ 兜底腿取不到数、读 0 也红（见 ③ 的独立夹具）。
func TestPdfPageCountReadsTightForm(t *testing.T) {
	const pages = 32
	p := writePdfSample(t, "mupdf_tight.pdf", buildRealPdf(pages, true, ""))

	got, err := PdfPageCount(p)
	if err != nil {
		t.Fatalf("真解析器读这份合法件应当成功，实际报错：%v", err)
	}
	if got != pages {
		t.Fatalf("解析腿页数期望 %d 实际 %d（夹具不合法＝整族用例前提失效）", pages, got)
	}
	if fast := pdfPageCountFast(p); fast != pages {
		t.Fatalf("派发腿页数期望 %d 实际 %d ⇒ 派发侧与上传闸门又各拿一把尺子了", pages, fast)
	}
	// 正向对照：带空格那一族同样读对（旧代码对它反而是"能读但多算"）
	q := writePdfSample(t, "word_spaced.pdf", buildRealPdf(pages, false, ""))
	if fast := pdfPageCountFast(q); fast != pages {
		t.Fatalf("带空格形态期望 %d 实际 %d", pages, fast)
	}
}

// TestPageCountLegsAgreeOnRealPdf 把「解析腿」「标记腿」「尾部 /Count 腿」**分别**钉成同值。
//
// 为什么不能只测最终值：最终值对，也可能是一道错数恰好抵消另一道
//
//	（旧代码在带空格形态上就是"页树 +1、跨块 −? "这种互相掩盖）。
//
// ★反证：去掉名字边界判据 ⇒ 标记腿在 "/Type/Pages" 与 "/Type/Catalog" 上各多算一页当场红。
func TestPageCountLegsAgreeOnRealPdf(t *testing.T) {
	const pages = 32
	for _, tc := range []struct {
		name  string
		tight bool
	}{
		{"mupdf 紧凑形态", true},
		{"Word 带空格形态", false},
	} {
		p := writePdfSample(t, strings.ReplaceAll(tc.name, " ", "_")+".pdf", buildRealPdf(pages, tc.tight, ""))
		if n := pdfPageCountParser(p); n != pages {
			t.Errorf("%s：解析腿期望 %d 实际 %d", tc.name, pages, n)
		}
		f, err := os.Open(p)
		if err != nil {
			t.Fatal(err)
		}
		markers := countPageTypeMarkersStream(f)
		fi, _ := f.Stat()
		trailer := countTrailerPageCount(f, fi.Size())
		_ = f.Close()
		if markers != pages {
			t.Errorf("%s：标记腿期望 %d 实际 %d（多算＝页树/目录被数进去；少算＝只认另一族写法）", tc.name, pages, markers)
		}
		if trailer != pages {
			t.Errorf("%s：/Count 腿期望 %d 实际 %d（这份夹具页树的 /Count 就在尾部窗口里，读不到说明读法被改坏）", tc.name, pages, trailer)
		}
	}
}

// ---------------- ② 启发式腿：解析器读不动时也要数对 ----------------

// TestPdfPageCountFastNoSpaceMarkers 是这批修复的**本体**：
// 一份「解析器读不动（没有 xref）、尾部窗口里也没有 /Count」的件，
// 页面对象标记按不带空格的 mupdf 形态写 32 次 ⇒ 必须读回 32，而不是旧代码的 0。
//
// ★反证：只认 "/Type /Page" ⇒ 这里读 0（现网 8 份的形状）当场红；
//
//	用 bytes.Count 数 "/Type/Page" 前缀不做名字边界 ⇒ 页树 "/Type/Pages" 被算进去读 33 红。
func TestPdfPageCountFastNoSpaceMarkers(t *testing.T) {
	const pages = 32
	var b strings.Builder
	b.WriteString("%PDF-1.4\n")
	b.WriteString("<< /Type/Pages /Kids [ ] >>\n") // 页树节点：一次都不许数进去
	for i := 0; i < pages; i++ {
		b.WriteString("<< /Type/Page /Parent 2 0 R /MediaBox [0 0 595 842] >>\n")
	}
	p := writePdfSample(t, "no_xref.pdf", []byte(b.String()))

	if n := pdfPageCountParser(p); n != 0 {
		t.Fatalf("前提失效：这份样张应当让解析腿读不动，实际读了 %d 页（解析腿一旦能读，下面的启发式判据就被它盖住）", n)
	}
	if n := pdfPageCountFast(p); n != pages {
		t.Fatalf("不带空格的页面对象标记期望数出 %d 实际 %d —— 这正是现网 8 份读 0 的那一类件", pages, n)
	}
}

// TestCountPageTypeMarkersInNameBoundary 把名字边界判据按**取值表**钉死。
//
// ★反证：删掉 `!isNameChar(win[term])` 这半条 ⇒ Pages/PageLabel 各多算一页，本表红；
//
//	删掉分隔符跳读循环 ⇒ 带空格那一族全读 0，本表同样红。
func TestCountPageTypeMarkersInNameBoundary(t *testing.T) {
	cases := []struct {
		name string
		win  string
		want int
	}{
		{"紧凑形态一页", "/Type/Page", 1},
		{"带空格一页", "/Type /Page", 1},
		{"制表符分隔一页", "/Type\t/Page", 1},
		{"换行分隔一页", "/Type\n/Page", 1},
		{"页树节点不算页", "/Type/Pages", 0},
		{"带空格页树节点不算页", "/Type /Pages", 0},
		{"PageLabel 不算页", "/Type/PageLabel", 0},
		{"PageObject 不算页", "/Type/PageObject", 0},
		{"目录节点不算页", "/Type/Catalog", 0},
		{"三页连排", "/Type/Page /Type/Page\n/Type/Page\r", 3},
		{"页树加两页", "/Type/Pages /Type/Page /Type/Page", 2},
		{"嵌套名字（前一个不是页）", "/Type /Type/Page", 1},
		{"无类型对象", "/Length 123", 0},
	}
	for _, tc := range cases {
		// 窗口尾按文件尾处理：最后一刻没有终止符也要认（atEOF 分支）
		if got := countPageTypeMarkersIn([]byte(tc.win), 0, true); got != tc.want {
			t.Errorf("%s：期望 %d 实际 %d（窗口=%q）", tc.name, tc.want, got, tc.win)
		}
	}
}

// ---------------- ③ 跨块归属：块边界上那一次只许数一遍 ----------------

// TestCountPageTypeMarkersStreamCountsOnceAcrossChunks 把三个**方向各不相同**的跨块错误一起钉住。
// 全文按 1MiB 一块流式读，标记按字节位置精确摆在块边界上（填充必须是换行这类**非名字字符**，
// 否则"终止符"这一档根本判不出来）：
//
//	A. 标记**正好结束于块边界**（终止符是下一块的第一个字节）
//	   ⇒ 上一块看不见终止符、判不了，只能由下一块按 `term == carryLen` 补数；
//	      写回旧版的严格大于 ⇒ A 一辈子没人数的（少算）。
//	B. 标记完整落在上一块内、终止符也在上一块 ⇒ 上一块数过一次；
//	      把归属写成恒真（整个 carry 窗口重数一遍）⇒ B 数两次（多算，现网 12 份偏高就有这一路）。
//	C. 标记**被块边界劈开**（"/Typ" 在上块、"e/Page" 在下块）
//	      ⇒ 靠衔接窗口才拼得回来；谁把 carry 去掉只扫本块 ⇒ C 丢了（少算）。
//
// ★反证（三条各红一次，实跑过）：`term >= carryLen` 改成 `term > carryLen` ⇒ 读 2；
//
//	改成恒真 ⇒ 读 4；衔接窗口长度归零 ⇒ 读 2。
func TestCountPageTypeMarkersStreamCountsOnceAcrossChunks(t *testing.T) {
	const chunk = 1 << 20
	buf := bytes.Repeat([]byte("\n"), 3*chunk)
	copy(buf, "%PDF-1.4\n")
	copy(buf[chunk-10:], "/Type/Page")    // A：终止符正好落在下一块首字节（chunk）
	copy(buf[chunk-11-10:], "/Type/Page") // B：完整落在上一块内，终止符也在上一块
	copy(buf[2*chunk-4:], "/Typ")         // C：被块边界劈开的那个
	copy(buf[2*chunk:], "e/Page")
	buf[chunk] = '\n'     // A 的名字终止符：非名字字符才算 "/Page" 结束
	buf[2*chunk+6] = '\n' // B 的名字终止符
	p := writePdfSample(t, "straddle.pdf", buf)

	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if got := countPageTypeMarkersStream(f); got != 3 {
		t.Fatalf("三个块边界形态的页标记期望共数 3 实际 %d（4＝carry 窗口重复计数；2＝边界那个标记被丢掉）", got)
	}
	if n := pdfPageCountFast(p); n != 3 {
		t.Fatalf("这份跨块件派发腿期望 3 实际 %d", n)
	}
}

// TestRealParserOutranksMarkerCount 证明"为什么必须有解析腿"，而不是只证明"有它也行"。
//
// 夹具三件事同时成立，把三道读数逼成**互不相同**的数：
//
//	· 页树的 "/Count 32" 被 70KB 注水挤出**尾部 64KB 窗口** ⇒ 兜底腿读不到（0）；
//	· 明文页面对象 32 个，另外在**流内容**里再写 5 个 "/Type/Page" 字面量
//	  （现网真实来源：内嵌 XML 元数据／注释流里就可能出现这种串）⇒ 标记腿多算成 37；
//	· xref 合法 ⇒ 解析腿读到真值 32。
//
// 于是最终值必须是 32，而不是 37 也不是 0。
//
// ★反证：删掉／挪走 pdfPageCountFast 的第一道（解析腿）⇒ 这里回 37 当场红——
//
//	这条就是"两道启发式凑合能用"这种想法的对照实验。
func TestRealParserOutranksMarkerCount(t *testing.T) {
	fakeMarkers := strings.Repeat("<< /Type/Page >>\n", 5)
	pad := "<< /Length " + fmt.Sprint(70<<10) + " >>\nstream\n" + fakeMarkers +
		strings.Repeat("q", 70<<10-len(fakeMarkers)) + "\nendstream\n"
	p := writePdfSample(t, "count_out_of_window.pdf", buildRealPdf(32, true, pad))

	if n := pdfPageCountParser(p); n != 32 {
		t.Fatalf("夹具前提失效：解析腿期望 32 实际 %d", n)
	}
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	fi, _ := f.Stat()
	trailer := countTrailerPageCount(f, fi.Size())
	markers := countPageTypeMarkersStream(f)
	_ = f.Close()
	if trailer != 0 {
		t.Fatalf("夹具前提失效：注水后尾部窗口里不该有 /Count，实际读到 %d", trailer)
	}
	if markers != 37 {
		t.Fatalf("夹具读数：标记腿应被流内容里那 5 个 /Type/Page 字面量骗到 37，实际 %d（对不上说明边界判据被改过，本用例的对照关系要重看）", markers)
	}
	if got := pdfPageCountFast(p); got != 32 {
		t.Fatalf("派发腿期望走解析腿的 32 实际 %d ⇒ 解析腿被摘掉或降了优先级", got)
	}
}

// ---------------- ④ 优先级：尾部 /Count 只能当兜底 ----------------

// TestTrailerCountNeverBeatsRealParser 钉住读法顺序「解析 ＞ 尾部 /Count ＞ 标记」。
//
// 现网真实形态：Type0/CIDFont 的字体描述符里写着 "/Count 255"，它常出现在尾部窗口里
// 且排在页树那个真 /Count **后面** ⇒ `bytes.LastIndex("/Count")` 取到 255，不是页数。
// 旧实现把它当**第一**读数源，这就是"12 份多算"里最凶的一类（2 页的件读成 255 页）。
//
// ★反证：把解析腿挪到 /Count 之后（或删掉）⇒ 最终读数 255 当场红；
//
//	把 /Count 的取数改成 FirstIndex ⇒ 对照支读数变 2，本用例同样红（防止空转绿灯）。
func TestTrailerCountNeverBeatsRealParser(t *testing.T) {
	tail := "<< /Type /FontDescriptor /FontName /AAAAAA+TimesNewRomanPS /Count 255 >>\n"
	p := writePdfSample(t, "font_descriptor.pdf", buildRealPdf(2, false, tail))

	if n := pdfPageCountParser(p); n != 2 {
		t.Fatalf("夹具前提失效：解析腿应读到 2 页，实际 %d（换夹具后本用例的对照关系就没了）", n)
	}
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	fi, err := f.Stat()
	if err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	trailer := countTrailerPageCount(f, fi.Size())
	_ = f.Close()
	if trailer != 255 {
		t.Fatalf("兜底腿期望读到字体描述符的 /Count 255，实际 %d ⇒ 对照支没走到，本用例在空转", trailer)
	}
	if got := pdfPageCountFast(p); got != 2 {
		t.Fatalf("最终读数期望 2（解析腿优先）实际 %d ⇒ 尾部 /Count 又爬到解析器前面去了", got)
	}
}

// ---------------- ⑤ 排障出口：四道读数各自可见 ----------------

// TestPdfPageCountDiagnosticsSeparatesLegs 保证现场排障能**分别**看到四道读数，
// 而不是只看到一个已归一的数字（09-30 那次是靠人工 grep 数 "/Type/Page" 才定位的）。
//
// ★反证：让 used 无条件等于 markers（或无条件等于 parser）⇒ 坏件那一半（parser 必须 0）立刻红。
func TestPdfPageCountDiagnosticsSeparatesLegs(t *testing.T) {
	const pages = 5
	good := writePdfSample(t, "good.pdf", buildRealPdf(pages, true, ""))
	parser, trailer, markers, used := PdfPageCountDiagnostics(good)
	if parser != pages || markers != pages || used != pages {
		t.Fatalf("合法件期望 parser/markers/used=%d，实际 %d/%d/%d", pages, parser, markers, used)
	}
	if trailer != pages {
		t.Fatalf("合法件期望 trailer=%d（这份夹具页树的 /Count 在尾部窗口内），实际 %d", pages, trailer)
	}

	// 坏件（无 xref／无 /Count／紧凑形态）：parser=0、trailer=0，markers 与 used 必须是 3
	bad := writePdfSample(t, "bad.pdf", []byte("%PDF-1.7\n"+
		"<< /Type/Pages /Kids [3 0 R 4 0 R 5 0 R] >>\n"+
		"<< /Type/Page /Parent 2 0 R >>\n<< /Type/Page /Parent 2 0 R >>\n<< /Type/Page /Parent 2 0 R >>\n"))
	parser, trailer, markers, used = PdfPageCountDiagnostics(bad)
	if parser != 0 {
		t.Fatalf("坏件的解析腿必须读不动（期望 0 实际 %d）⇒ 夹具失效，下面的启发式判据就不成立了", parser)
	}
	if trailer != 0 {
		t.Fatalf("坏件尾部不该有 /Count（期望 0 实际 %d）", trailer)
	}
	if markers != 3 || used != 3 {
		t.Fatalf("紧凑形态标记期望 markers/used=3，实际 %d/%d", markers, used)
	}

	// 不存在的文件：四道全 0（不许把垃圾数送去撞阈值）
	if parser, trailer, markers, used = PdfPageCountDiagnostics(filepath.Join(t.TempDir(), "nope.pdf")); parser|trailer|markers|used != 0 {
		t.Fatalf("缺失文件期望四道全 0，实际 %d/%d/%d/%d", parser, trailer, markers, used)
	}
}

// ---------------- ⑦ 畸形件：第三方解析器的 panic 必须收敛成 error ----------------

// TestPdfPageCountSurvivesTrailingNewlinePanic 是本轮写反证夹具时**当场撞出来的新缺陷**，
// panic 栈打得很清楚（ledongthuc/pdf read.go 的 NewReaderEncrypted）：
//
//	它剥尾部空白那行是 `for len(buf) > 0 && buf[len-1] == '\n' || buf[len-1] == '\r'`，
//	`||` 优先级低于 `&&` ⇒ 少了一对括号 ⇒ 尾部 100 字节全是换行时 buf 被剥到长度 0，
//	下一次 `buf[len(buf)-1]` 直接 index out of range [-1] **panic**。
//
// 这条腿跑在**上传请求的同步路径**上（`internal/api` 的 checkPdfLimits 判 120 页硬墙），
// 也跑在派发资格判定上：客户传一件"头是 %PDF-1.7、尾巴一串换行"的文件就能把请求打成连接重置。
// ⇒ 解析器入口套 recover：崩溃＝读不动＝按 error 出栈，派发侧继续走兜底启发式。
//
// ★反证：删掉 PdfPageCount 里那段 defer/recover ⇒ 本用例以 panic 直接红（不是断言红，是崩）；
//
//	同时 TestExtractPdfTextLibSurvivesTrailingNewlinePanic 也会崩在同一条腿上。
func TestPdfPageCountSurvivesTrailingNewlinePanic(t *testing.T) {
	p := writePdfSample(t, "trailing_newlines.pdf", []byte("%PDF-1.7\n"+strings.Repeat("\n", 4096)))

	// 前提自证：这份件确实会让第三方解析器走进那条剥空到 0 的分支
	if _, err := os.Stat(p); err != nil {
		t.Fatal(err)
	}
	n, err := PdfPageCount(p)
	if err == nil {
		t.Fatalf("畸形件必须按 error 出栈（拿到 n=%d 且无错说明解析器不崩了，本用例的前提要重核）", n)
	}
	if n != 0 {
		t.Fatalf("崩溃/失败时不许带回任何页数读数，实际 n=%d", n)
	}
	if !strings.Contains(err.Error(), "崩溃") && !strings.Contains(err.Error(), "not a PDF") {
		t.Logf("注：本次是以常规解析错误出栈（不是 panic 恢复），错误=%v", err)
	}
	// 派发侧整条链都不许崩：读不动 ⇒ 四道读数全 0，交由 TryDispatch 打 WARN
	if got := pdfPageCountFast(p); got != 0 {
		t.Fatalf("这份件三道读数都该取不到，实际最终值 %d ⇒ 某一腿在畸形件上编出了数", got)
	}
	parser, trailer, markers, used := PdfPageCountDiagnostics(p)
	if parser|trailer|markers|used != 0 {
		t.Fatalf("排障出口期望四道全 0，实际 %d/%d/%d/%d", parser, trailer, markers, used)
	}
}

// TestExtractPdfTextLibSurvivesTrailingNewlinePanic 把同一份畸形件打到**提取回退腿**上。
//
// 这一腿更贴近客户：PDF 文本提取在 CLI 不可用时走它。崩溃必须变成 error，
// 由上层按"这份件解析失败"处理（与加密件同一条路），而不是把工单/请求打穿。
//
// ★反证：删掉 extractPdfTextLib 的 defer/recover ⇒ 本用例直接 panic 红。
func TestExtractPdfTextLibSurvivesTrailingNewlinePanic(t *testing.T) {
	p := writePdfSample(t, "extract_trailing_newlines.pdf", []byte("%PDF-1.7\n"+strings.Repeat("\n", 4096)))
	e := &Extractor{}
	if err := extractPdfTextLib(p, e); err == nil {
		t.Fatal("畸形件的提取必须返回 error（崩溃已被收敛），实际返回了成功")
	}
	if len(e.Texts) != 0 {
		t.Fatalf("失败路径不许带回半截文本，实际 %d 段", len(e.Texts))
	}
}

// ---------------- ⑧ 分档判据与「读数取不到必须吭声」 ----------------

// TestPdfSizeLegPassesOnlyForPdfOverThreshold 钉住 WARN 的分档前提：
// 「是 PDF」与「体积够档」两件事同时成立才可能发警报。
//
// ★反证：去掉非 PDF 那条 return false ⇒ 大 docx 也被判"该吭声"，现网每条正常分流刷一条 WARN。
func TestPdfSizeLegPassesOnlyForPdfOverThreshold(t *testing.T) {
	t.Setenv(envDispatchMinMB, "1")
	dir := t.TempDir()

	bigPdf := filepath.Join(dir, "big.pdf")
	if err := os.WriteFile(bigPdf, append([]byte("%PDF-1.7\n"), bytes.Repeat([]byte("x"), 2<<20)...), 0o600); err != nil {
		t.Fatal(err)
	}
	if !pdfSizeLegPasses([]string{bigPdf}) {
		t.Fatal("2MiB 的 PDF 在 MIN_MB=1 下体积腿应当成立")
	}
	smallPdf := filepath.Join(dir, "small.pdf")
	if err := os.WriteFile(smallPdf, []byte("%PDF-1.7\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if pdfSizeLegPasses([]string{smallPdf}) {
		t.Fatal("低于档位的大腿不该成立：否则每条小件分流都会打 WARN")
	}
	bigDocx := filepath.Join(dir, "big.docx")
	if err := os.WriteFile(bigDocx, bytes.Repeat([]byte("y"), 2<<20), 0o600); err != nil {
		t.Fatal(err)
	}
	if pdfSizeLegPasses([]string{bigDocx}) {
		t.Fatal("非 PDF 大件不该被判进体积腿：§7 白名单只放 PDF 链")
	}
	if pdfSizeLegPasses([]string{filepath.Join(dir, "nope.pdf")}) {
		t.Fatal("读不到尺寸的文件不许算作够档（宁窄勿宽）")
	}
}

// lockedBuf 是并发安全的日志缓冲：slog handler 写它，裸 bytes.Buffer 在 -race 下会红。
type lockedBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

// Write 实现 io.Writer。
func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

// String 返回已捕获内容。
func (l *lockedBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

// TestTryDispatchWarnsWhenPagesUnreadable 钉住这批修复的**第二条腿**：
// 体积够档、页数却三道全读不动的件，TryDispatch 必须 ① 不派（宁窄勿宽）② 留下一条可查的 WARN。
//
// 这条断言的重点不在"派不派"，而在**别再静默**：09-30 开闸首跑判红之后日志里一个字都没有，
// 只能人工数 PDF 标记；现网再出同类事，journalctl 里必须能直接看到"页数读数取不到"。
//
// ★反证：删掉 TryDispatch 里那条 WARN ⇒ 第一条断言红；
//
//	把小件也塞进 WARN 分支 ⇒ 第二条（小件必须静默）红。
func TestTryDispatchWarnsWhenPagesUnreadable(t *testing.T) {
	enableDispatchForTest(t)
	t.Setenv(envDispatchMinMB, "1")
	t.Setenv(envDispatchMinPage, "30")
	// 主机指向一个不存在的域：本用例要求它**走不到**派发；万一判据写反，也是当场失败而不是拨真机器
	t.Setenv(envDispatchHost, "no-such-host.invalid")

	prev := slog.Default()
	lb := &lockedBuf{}
	slog.SetDefault(slog.New(slog.NewJSONHandler(lb, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	dir := t.TempDir()
	pdf := filepath.Join(dir, "unreadable.pdf")
	// 体积够（2MiB）、扩展名对，但三道路数全读不动：没有 xref、没有 /Count、没有页面对象标记
	if err := os.WriteFile(pdf, append([]byte("%PDF-1.7\n"), bytes.Repeat([]byte("z"), 2<<20)...), 0o600); err != nil {
		t.Fatal(err)
	}
	if n := pdfPageCountFast(pdf); n != 0 {
		t.Fatalf("夹具前提失效：这份件应当三道全读不动，实际读 %d 页", n)
	}

	if _, ok := TryDispatch(context.Background(), "s-pages-zero", "pdf_overlay.py",
		[]string{"pdf_overlay.py", "extract", pdf}, nil, []string{pdf}, nil); ok {
		t.Fatal("页数读不动的件被判成派发成功 ⇒ 宁窄勿宽这条被改反")
	}
	if !strings.Contains(lb.String(), "页数读数取不到") {
		t.Fatalf("够档却读不出页数的件必须留一条 WARN（否则现网无法区分『读数坏了』和『这单本来不该派』）；实际日志：%s",
			tailForLog(lb.String()))
	}

	// 反向对照：小件必须**静默**不合格——不然日志被刷满，真警报反而看不见
	lb2 := &lockedBuf{}
	slog.SetDefault(slog.New(slog.NewJSONHandler(lb2, nil)))
	small := filepath.Join(dir, "small.pdf")
	if err := os.WriteFile(small, []byte("%PDF-1.7\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := TryDispatch(context.Background(), "s-small", "pdf_overlay.py",
		[]string{"pdf_overlay.py", "extract", small}, nil, []string{small}, nil); ok {
		t.Fatal("小件不该派发")
	}
	if strings.Contains(lb2.String(), "页数读数取不到") {
		t.Fatalf("小件也被打了页数警报 ⇒ 分档判据失效，日志：%s", tailForLog(lb2.String()))
	}
}

// tailForLog 截日志尾部用于断言消息（别把整段 JSON 日志刷进测试输出）。
func tailForLog(s string) string {
	s = strings.TrimSpace(s)
	const n = 400
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}
