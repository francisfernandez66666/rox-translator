// ============================================================================
// fileproc_pagecount.go · 职责说明
// PDF **页数读数**的唯一事实源（★ 2026-10-01 〇-AF 补丁四）。
//
// 这一批把读数收进来，是因为开闸当天 fpdprobe 首跑暴露了一个静默死分支：
//
//	`pdfPageCountFast`（派发分流用的那一把尺子）旧实现只有两道**手写启发式**——
//	① 文件尾部 64KB 里找 "/Count N"；② 找不到就数字符串 "/Type /Page"（**带空格**）。
//	现网 40 份 PDF 实测：8 份读成 **0 页**（含一份 28.7MB 的真客户件），12 份**多算**
//	（"/Type /Pages" 节点被 "/Type /Page" 前缀命中，再加跨块 carry 窗口重复计数）。
//	读 0 的后果不是显示问题：`DispatchEligible` 要求「体积 ≥20MiB **且** 页数 ≥30」，
//	页数读 0 ⇒ 这份件**永远不派**，而且旧 TryDispatch 在资格判 false 时**一条日志都不打**
//	（它只在实际拨远端失败时 WARN）。于是「闸开了、健康面 online、一单都没派出去」这个
//	09-28/09-29 已经栽过两次的形态，会第三次长出来——只是这次藏在页数腿里。
//
// 收口后的读法（顺序即优先级）：
//
//	① `PdfPageCount`：真解析（github.com/ledongthuc/pdf，流式 ReaderAt，只留 xref 与缓冲），
//	   与 `internal/api` 那道「PDF 页数前置拦截」**同一个函数、同一把尺子**；
//	② 解析不出来（加密件／坏 xref／库不认的形态）才退到手写启发式，且启发式已修：
//	   "/Count" 窗口照旧，"/Type" 标记**两种形态**（带／不带空格）都认，
//	   并在名字边界上排除 "/Pages"、"/PageLabel" 这类同前缀对象；跨块命中**只数一次**；
//	③ 两道都拿不到 ⇒ 返回 0，由调用侧决定；派发侧保持「宁窄勿宽」（不派），
//	   但从这一批起**必须打一条 WARN**，把「读数取不到」从静默变成可查。
//
// ★ 顺带在本批**写反证夹具时当场撞出一个新缺陷**（不是猜的，是 panic 栈打出来的）：
//
//	ledongthuc/pdf 的 `NewReaderEncrypted` 剥尾部空白那行少了一对括号
//	（`len(buf) > 0 && buf[len-1]=='\n' || buf[len-1]=='\r'`，`||` 优先级低于 `&&`），
//	⇒ **尾部 100 字节全是换行**的畸形件会把缓冲剥到长度 0，下一次 `buf[len(buf)-1]` 直接
//	   `index out of range [-1]` **panic**。这条腿跑在上传请求的同步路径上（checkPdfLimits），
//	   也跑在派发资格判定上，所以解析器入口现在一律套 recover：**崩溃＝读不动＝按 error 出栈**。
//
// 反证（同文件底部的 *_test 里都有对应用例）：
//
//	· 把 ① 摘掉 ⇒ mupdf 形态（"/Type/Page" 不带空格＋页树不在尾部 64KB）立刻读回 0；
//	· 把名字边界判据放宽成 bytes.Count("/Type/Page") ⇒ "/Type/Pages" 被多算一页当场红；
//	· 把跨块归属判据写回「整个 carry 窗口重数一遍」⇒ 边界那几页翻倍红。
//
// 运行：go test -race ./internal/fileproc/ -run 'PageCount|PageMarkers|DispatchTier'
// ============================================================================
package fileproc

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strconv"

	ledong "github.com/ledongthuc/pdf"
)

// markerSpan 一个页面对象标记最长可能占的字节数："/Type" + 定界符 + "/Page"。
// 跨块衔接窗口取它 +1（多出的一字节是**名字终止符**，用来区分 "/Page" 与 "/Pages"）。
const markerSpan = len("/Type") + 16 + len("/Page") + 1

// PdfPageCount 用真解析器读 PDF 页数（★ 派发分流与上传闸门共用的唯一出口）。
//
// 流式实现（把 *os.File 交给解析器），**不整读进堆**：40MB 的件一次性 ReadFile
// 就是白烧 40MB 堆，而这条读数跑在资源闸之外——同 P0（改造方案 §11-P0）那条教训。
// 解析失败返回 error 由调用侧决定放行还是判红（加密件／损坏件都走这一支）。
//
// ★★ 这一批把 **panic 也收敛成 error**（〇-AF 补丁四，本轮写反证夹具时当场撞出来的新缺陷）：
//
//	github.com/ledongthuc/pdf 的 NewReaderEncrypted 在读尾部 100 字节做剥空白时写的是
//	    `for len(buf) > 0 && buf[len(buf)-1] == '\n' || buf[len(buf)-1] == '\r' { buf = buf[:len(buf)-1] }`
//	两个条件少了一对括号（`||` 优先级低于 `&&`），于是**尾 100 字节全是换行**的畸形件会把 buf 剥到长度 0，
//	下一次 `buf[len(buf)-1]` 直接 index out of range [-1] **panic**。
//	这条路径不是理论风险：`checkPdfLimits` 跑在**上传请求的同步路径**上，
//	只要文件头是 `%PDF-1.` 而尾巴是一串换行，panic 就落在 net/http 的连接协程里——
//	客户端看到的是连接被重置，主站看到的是一次 nobody 关心的 stack trace。
//	⇒ 第三方解析器的入口一律套 recover：崩溃＝"这份件读不动"，按 error 出栈，由调用侧走兜底腿。
func PdfPageCount(path string) (n int, err error) {
	defer func() {
		if r := recover(); r != nil {
			n = 0
			err = fmt.Errorf("PDF 页数解析崩溃（畸形件，第三方解析器会 panic）：%v", r)
		}
	}()
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return 0, err
	}
	r, err := ledong.NewReader(f, fi.Size())
	if err != nil {
		return 0, err
	}
	return r.NumPage(), nil
}

// pdfPageCountFast 派发阈值用的页数读数（★ 只用于判阈值，不用于计费/展示）。
//
// 读法优先级见文件头：真解析 ＞ 尾部 "/Count" ＞ 流式数页面对象标记。
// 三道全空才回 0；回 0 时调用侧（TryDispatch）必须打 WARN，不许静默当成"这单很小"。
func pdfPageCountFast(path string) int {
	if n := pdfPageCountParser(path); n > 0 {
		return n
	}
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return 0
	}
	if n := countTrailerPageCount(f, fi.Size()); n > 0 {
		return n
	}
	return countPageTypeMarkersStream(f)
}

// pdfPageCountParser 是 PdfPageCount 的"只要一个数"形态（失败回 0），
// 单独切出来是为了让单测能分别钉住「解析腿」与「启发式腿」。
func pdfPageCountParser(path string) int {
	n, err := PdfPageCount(path)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// PdfPageCountDiagnostics 把三道读数的**各自读数**原样摊开（★ 排障/验收专用，业务路径不许调）。
//
// 为什么要有这个出口：开闸当天 fpdprobe 判红之后，"这份件到底几页、旧读数为什么是 0"
// 只能靠人工 grep "/Type/Page" 数一遍——现网再出这类事，不该再要求排障的人懂 PDF 语法。
// 四道读数分别回答：
//
//	parser  真解析器（第一优先，也是上传闸门用的那把尺子）
//	trailer 尾部 64KB 最后一个 /Count（已知会被字体描述符的 /Count 顶掉，只作兜底）
//	markers 流式数页面对象标记（两种形态 + 名字边界）
//	used    pdfPageCountFast 实际采纳的那一道（按优先级算出来的最终值）
//
// 返回的四个数**故意不做归一**：0 就是 0，用来证明"某一腿确实是死的"，不许为了好看填数。
func PdfPageCountDiagnostics(path string) (parser, trailer, markers, used int) {
	parser = pdfPageCountParser(path)
	f, err := os.Open(path)
	if err != nil {
		return parser, 0, 0, 0
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return parser, 0, 0, 0
	}
	trailer = countTrailerPageCount(f, fi.Size())
	markers = countPageTypeMarkersStream(f)
	used = pdfPageCountFast(path)
	return
}

// countPageTypeMarkersIn 在一段字节窗口里数**页面对象**标记，两种形态都认：
//
//	"/Type/Page"（mupdf／多数现代生产者）与 "/Type /Page"（Word／LibreOffice 常见）。
//
// 名字边界必须判："/Type/Pages"（页树节点）、"/Type/PageLabel" 这类同前缀对象**不算**一页。
// 旧实现用 bytes.Count(window, "/Type /Page")，一是漏掉不带空格的形态（现网 8 份读 0），
// 二是把 "/Type /Pages" 一起数了进去（现网 12 份多算 +1～+4）。
//
// 参数：win=窗口；carryLen=窗口前段那截**衔接窗口**的字节数。
//
//	计数的归属看终止符位置，三档各有含义（跨块只数一次全靠它）：
//	term > carryLen ⇒ 终止符是新读进来的字节，本块该数；
//	term == carryLen ⇒ 终止符正是新块的第一个字节——上一块窗口里它等于 len(win)、
//	当时既没到文件尾也看不见终止符，**判不了**，只能由本块补上；
//	term < carryLen ⇒ 终止符在衔接窗口内部，上一块已经数过 ⇒ 跳过。
//
// atEOF=窗口尾是否文件尾（文件以 "/Page" 收尾时没有终止符，也算一次命中）。
//
// 返回：本窗口应计数的页面对象数。
func countPageTypeMarkersIn(win []byte, carryLen int, atEOF bool) int {
	total := 0
	for i := 0; i < len(win); {
		rel := bytes.Index(win[i:], []byte("/Type"))
		if rel < 0 {
			break
		}
		j := i + rel + len("/Type")
		for j < len(win) && (win[j] == ' ' || win[j] == '\t' || win[j] == '\r' || win[j] == '\n' || win[j] == 0) {
			j++
		}
		if bytes.HasPrefix(win[j:], []byte("/Page")) {
			term := j + len("/Page") // 名字终止符所在位置
			last := term >= len(win)
			if (last && atEOF) || (!last && !isNameChar(win[term])) {
				// 归属判据：终止符**位置 ≥ carryLen** 才算这一次。
				// 等号那一档不是可有可无——它正是"标记完整结束于上一块末尾、终止符落在新块首字节"
				// 那种边界：上一块的窗口里 term == len(win) 且没到文件尾，**当时判不了**（不计数）；
				// 若这里再用严格大于把它跳过，这个标记就一辈子没人数（首版反证实测：3 个标记只数出 2 个）。
				// 而完整落在 carry 里、上一块已经数过的那些，其终止符位置严格小于 carryLen ⇒ 不会被重复计数。
				if term >= carryLen {
					total++
				}
			}
		}
		// 从名字起点继续扫：j 恒大于本轮的 i（至少 +len("/Type")），所以不会原地打转，
		// 又不会像「跳过整个 /Page」那样把 "/Type /Type/Page" 这种嵌套形态吞掉。
		i = j
	}
	return total
}

// isNameChar 判断字节是否算 PDF **名字**的一部分（字母/数字与名字内常见标点）。
// 用途是把 "/Pages"、"/PageLabel" 这类同前缀名字与 "/Page" 分开。
func isNameChar(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	case c == '_' || c == '-' || c == '.' || c == '+':
		return true
	}
	return false
}

// countPageTypeMarkersStream 流式扫全文的页面对象标记（内存有界：1MiB 块 + markerSpan 衔接窗口）。
//
// ★ 跨块只数一次：上一块尾部 markerSpan 字节接在本块前面，**命中的归属看终止符位置**——
//
//	终止符位置 ≥ 衔接窗口长度才计数（等于号那一档＝终止符正好是新块第一个字节，
//	上一块当时无法判定，只能在这里补上）。
//	旧写法是 `bytes.Count(window, "/Type /Page")` 且 window = carry + chunk，整个 carry 重数一遍，
//	于是完整落在衔接窗口里的标记会被数两次（现网 12 份读数偏高里有这一路）；
//	而反过来把衔接窗口去掉（只扫本块）又会丢掉被块边界劈开的标记。两个错法各配一条反证，
//	见 fileproc_pagecount_test.go 的 TestCountPageTypeMarkersStreamCountsOnceAcrossChunks。
func countPageTypeMarkersStream(f *os.File) int {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return 0
	}
	const chunk = 1 << 20
	buf := make([]byte, chunk)
	var carry []byte
	total := 0
	for {
		n, rerr := f.Read(buf)
		if n > 0 {
			window := make([]byte, 0, len(carry)+n)
			window = append(window, carry...)
			window = append(window, buf[:n]...)
			total += countPageTypeMarkersIn(window, len(carry), rerr != nil)
			if len(window) > markerSpan {
				keep := append(make([]byte, 0, markerSpan), window[len(window)-markerSpan:]...)
				carry = keep
			} else {
				carry = append(make([]byte, 0, len(window)), window...)
			}
		}
		if rerr != nil {
			break
		}
	}
	return total
}

// countTrailerPageCount 从文件尾部 64KB 取最后一个 "/Count N"（页树根常写在尾部）。
//
// 保留这一道**只作解析器失败后的兜底**：它会命中字体描述符里的 /Count（Type0/CIDFont
// 的 /Count 255 就是同一个键名），所以绝不能再当第一读数源。
func countTrailerPageCount(f *os.File, size int64) int {
	tail := int64(64 << 10)
	if size < tail {
		tail = size
	}
	if tail <= 0 {
		return 0
	}
	if _, err := f.Seek(size-tail, io.SeekStart); err != nil {
		return 0
	}
	buf := make([]byte, tail)
	n, _ := io.ReadFull(f, buf)
	buf = buf[:n]
	idx := bytes.LastIndex(buf, []byte("/Count"))
	if idx < 0 {
		return 0
	}
	i := idx + len("/Count")
	for i < len(buf) && (buf[i] == ' ' || buf[i] == '\n' || buf[i] == '\r' || buf[i] == '\t') {
		i++
	}
	j := i
	for j < len(buf) && buf[j] >= '0' && buf[j] <= '9' {
		j++
	}
	if j == i {
		return 0
	}
	v, err := strconv.Atoi(string(buf[i:j]))
	if err != nil || v <= 0 {
		return 0
	}
	return v
}
