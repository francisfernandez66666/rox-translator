// ============ chat_image_test.go · 职责说明 ============
// 即时翻译**图片翻译**（POST /api/chat/image）的回归锁（★ 2026-10-17 用户令新增）。
//
// 覆盖范围（与 chat_audio_test.go 同口径：只测 HTTP 契约与纯函数，不依赖部署机的 OCR 权重）：
//
//	① 登录/方法闸门：GET→405、匿名 POST→401（不给匿名脚本按次烧本地 OCR+LLM 算力的口子）。
//	② 错误档位归并（imageReason）：fileproc 抛出的话术 → 前端可用的三档
//	   capability_missing / no_text / failed，前端据此取 12 语种词典，故判据要逐字钉住。
//	③ 目标语种解析（parseChatImageLangs）：去空、去重、保序——首个语种决定译后图那张图。
//	④ 译后图降级的**安全方向**：译文为空 / 行数对不上时，宁可不出图（ImageError 置原因、
//	   Image 留空），也绝不把译文盖到错误的框上（与 ocr.go parseOcrJSON 同一条纪律）。
//	⑤ 路由接线：server.go 必须把 /api/chat/image 挂上 handleChatImage（漏挂＝接口 404，
//	   而单测直调 handler 是看不见这一层的，故另立一条源码级接线锁）。
//	⑥ ★ 2026-10-19 新增：**暂存写盘必须真件跑一遍**——此前全文件无一条喂真 multipart，
//	   stageChatImage 里「先 io.Copy 再 Close」那段代码在 CI 里从没执行过，
//	   于是"往已关闭句柄写"这个 100% 失败的缺陷活了整整两天（有锁而射程不足）。
//	   ⇒ 真 multipart 逐字节往返锁＋AST 源码顺序锁（Close 挪回 Copy 之前必须红）。
//	   为什么不写"handler 交真件断 200"：那要 OCR 权重在位才走得到暂存，
//	   CI 无权重时它会以 capability_missing 早退＝结构性空转，正是本条要治的射程病。
//
// =============================================
package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// minPNGBase64 1×1 像素的最小合法 PNG（真实图片字节，用于"真件"往返锁）。
const minPNGBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="

// TestChatImageRequireLoginAndMethod ① 方法闸门与方法顺序：GET→405、匿名 POST→401。
// 判据顺序也钉住：方法先于登录判（否则 GET 会先被登录闸拦成 401，客户以为"接口不存在"）。
func TestChatImageRequireLoginAndMethod(t *testing.T) {
	s := &Server{}
	rec := httptest.NewRecorder()
	s.handleChatImage(rec, httptest.NewRequest(http.MethodPost, "/api/chat/image", strings.NewReader("")))
	if rec.Code != 401 {
		t.Fatalf("匿名 POST 应返回 401，实际 %d（body=%s）", rec.Code, rec.Body.String())
	}
	rec2 := httptest.NewRecorder()
	s.handleChatImage(rec2, httptest.NewRequest(http.MethodGet, "/api/chat/image", nil))
	if rec2.Code != 405 {
		t.Fatalf("GET 访问应返回 405，实际 %d", rec2.Code)
	}
}

// TestChatImageReasonBands ② 错误话术 → 档位归并（前端按档位取词典，档名即对外排障契约）。
func TestChatImageReasonBands(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"无错误", nil, ""},
		{"依赖缺失", errors.New("图片文字识别（OCR）不可用：服务器未安装 onnxruntime/numpy/Pillow 或未部署 OCR 模型资产（assets/ocr/*.onnx），取权重步骤见 assets/ocr/README.md"), "capability_missing"},
		{"脚本缺失", errors.New("OCR 脚本缺失：ocr_recognize.py 未随二进制部署到 /opt/translator/bin"), "capability_missing"},
		{"模型资产缺失", errors.New("OCR 模型资产缺失或加载失败：assets/ocr/*.onnx 未随包落盘，取权重步骤见 assets/ocr/README.md"), "capability_missing"},
		{"图里没字", errors.New("OCR 未识别出文字（图片可能无文字内容、或为手写/艺术字）"), "no_text"},
		{"其余失败", errors.New("OCR 识别失败: exit status 1\nboom"), "failed"},
	}
	for _, c := range cases {
		if got := imageReason(c.err); got != c.want {
			t.Errorf("%s：imageReason=%q，期望 %q", c.name, got, c.want)
		}
	}
}

// TestChatImageParseLangs ③ 逗号分隔目标语种：去空、trim、去重、保序（首个用于译后图，顺序有意义）。
func TestChatImageParseLangs(t *testing.T) {
	got := parseChatImageLangs(" en , zh ,, en , ja ")
	want := []string{"en", "zh", "ja"}
	if len(got) != len(want) {
		t.Fatalf("parseChatImageLangs=%v，期望 %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("parseChatImageLangs=%v，期望 %v", got, want)
		}
	}
	if n := len(parseChatImageLangs("   , ,")); n != 0 {
		t.Fatalf("全空串应解析出 0 个语种，实际 %d", n)
	}
}

// TestRenderChatImageNeverPaintsOnMismatch ④ 译后图降级的安全方向：
// 译文为空 / 行数与 OCR 行数不等时，ImageError 必须置原因且 Image 必须为空（保留原文更安全）。
func TestRenderChatImageNeverPaintsOnMismatch(t *testing.T) {
	// 这里刻意用一个不存在的源图路径：两条早退分支都**不该**碰到文件系统，
	// 一旦实现把它们挪到 RenderTranslatedImage 之后，本用例会以"文件不存在"的形式现形。
	r := httptest.NewRequest(http.MethodPost, "/api/chat/image", nil)

	empty := &chatImageResp{}
	renderChatImage(r, "/nonexistent/src.png", "en", []string{"a", "b"}, "   ", empty)
	if empty.ImageError != "no_translation" || empty.Image != "" {
		t.Fatalf("空译文应回 no_translation 且不出图，实际 image_error=%q image_len=%d", empty.ImageError, len(empty.Image))
	}

	mismatch := &chatImageResp{}
	renderChatImage(r, "/nonexistent/src.png", "en", []string{"a", "b", "c"}, "只有一行", mismatch)
	if mismatch.ImageError != "line_mismatch" || mismatch.Image != "" {
		t.Fatalf("行数不等应回 line_mismatch 且不出图，实际 image_error=%q image_len=%d", mismatch.ImageError, len(mismatch.Image))
	}
}

// TestChatImageRouteWired ⑤ 路由接线锁：源码级断言 server.go 把 /api/chat/image 挂上 handleChatImage。
// 为什么另立一条：单测直调 handler 看不见"有没有挂到 mux 上"，漏挂会让线上接口 404 而全部单测仍绿。
func TestChatImageRouteWired(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("server.go"))
	if err != nil {
		t.Fatalf("读取 server.go 失败: %v", err)
	}
	want := `s.mux.HandleFunc("/api/chat/image", s.handleChatImage)`
	if !strings.Contains(string(src), want) {
		t.Fatalf("server.go 未接线 %s（图片翻译接口会 404）", want)
	}
	// 负向对照：同族两条语音路由仍在（避免"整段被删/被注释"也判绿）。
	for _, p := range []string{`"/api/chat/asr"`, `"/api/chat/tts"`} {
		if !strings.Contains(string(src), p) {
			t.Fatalf("server.go 缺少既有路由 %s（本判据的射程自证腿）", p)
		}
	}
}

// TestStageChatImageRoundTrip ⑥ 真件往返：真 multipart + 真 PNG 字节 ⇒ 落盘件逐字节等值。
//
// 这条锁的射程就是 G1 缺陷本体：只要 stageChatImage 里 Close 排在 io.Copy 之前，
// io.Copy 会退 "file already closed"，本用例以"暂存失败"现形（不是"文案不对"）。
func TestStageChatImageRoundTrip(t *testing.T) {
	png, err := base64.StdEncoding.DecodeString(minPNGBase64)
	if err != nil {
		t.Fatalf("夹具 PNG 解码失败（自证腿坏了）: %v", err)
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", "in.png")
	if err != nil {
		t.Fatalf("构造 multipart 失败: %v", err)
	}
	if _, err := fw.Write(png); err != nil {
		t.Fatalf("写 multipart 体失败: %v", err)
	}
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/chat/image", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if err := req.ParseMultipartForm(chatImageMaxBytes); err != nil {
		t.Fatalf("解析 multipart 失败: %v", err)
	}
	f, hdr, err := req.FormFile("file")
	if err != nil {
		t.Fatalf("取 multipart 文件失败: %v", err)
	}
	defer f.Close()
	ext := strings.ToLower(filepath.Ext(hdr.Filename))

	tmpPath, cleanup, err := stageChatImage(f, ext)
	if err != nil {
		t.Fatalf("暂存失败（G1 缺陷本体：Close 必须排在 io.Copy 之后）: %v", err)
	}
	defer cleanup()
	if _, statErr := os.Stat(tmpPath); statErr != nil {
		t.Fatalf("暂存成功后临时文件应存在: %v", statErr)
	}
	got, err := os.ReadFile(tmpPath)
	if err != nil {
		t.Fatalf("读回暂存件失败: %v", err)
	}
	if !bytes.Equal(got, png) {
		t.Fatalf("暂存件与上传件不逐字节等值：got=%d want=%d", len(got), len(png))
	}
	// 清理腿必须真删掉（防句柄/磁盘泄漏：失败分支也要 Close 的那条纪律）。
	cleanup()
	if _, statErr := os.Stat(tmpPath); !os.IsNotExist(statErr) {
		t.Fatalf("cleanup 后临时文件应已删除: statErr=%v", statErr)
	}
}

// imageStageOrder 在源码里定位 stageChatImage 的「首个 io.Copy 行号」与「首个 tmp.Close 行号」。
// 返回 0 表示没找到（判据靶子漂了，调用方必须判红而不是当"通过"）。
func imageStageOrder(src string) (copyLine, closeLine int) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "chat_image.go", src, 0)
	if err != nil {
		return 0, 0
	}
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "stageChatImage" || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			recv, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			if recv.Name == "io" && sel.Sel.Name == "Copy" && copyLine == 0 {
				copyLine = fset.Position(call.Pos()).Line
			}
			if recv.Name == "tmp" && sel.Sel.Name == "Close" && closeLine == 0 {
				closeLine = fset.Position(call.Pos()).Line
			}
			return true
		})
	}
	return copyLine, closeLine
}

// TestChatImageStageClosesAfterCopy ⑥ 源码顺序锁：stageChatImage 必须「先 io.Copy 再 tmp.Close」。
// 反证（同一判据喂反向顺序的合成源码）必须判红，证明这条锁抓的是顺序本身。
func TestChatImageStageClosesAfterCopy(t *testing.T) {
	src, err := os.ReadFile("chat_image.go")
	if err != nil {
		t.Fatalf("读取 chat_image.go 失败: %v", err)
	}
	cl, kl := imageStageOrder(string(src))
	if cl == 0 || kl == 0 {
		t.Fatalf("未定位到 io.Copy / tmp.Close（判据靶子已漂，闸门失明 copy=%d close=%d）", cl, kl)
	}
	if cl >= kl {
		t.Fatalf("stageChatImage 必须先 io.Copy 再 tmp.Close（copy=%d close=%d）：往已关闭句柄写必退 file already closed",
			cl, kl)
	}

	// 反证：把 Close 挪到 Copy 之前，同一条判据必须判红。
	bad := `package api

import (
	"io"
	"os"
)

func stageChatImage(f io.Reader, ext string) (string, func(), error) {
	tmp, err := os.CreateTemp("", "x*"+ext)
	if err != nil {
		return "", nil, err
	}
	tmpPath := tmp.Name()
	tmp.Close()
	if _, cerr := io.Copy(tmp, io.LimitReader(f, 1)); cerr != nil {
		tmp.Close()
		return "", nil, cerr
	}
	return tmpPath, func() {}, nil
}
`
	bcl, bkl := imageStageOrder(bad)
	if bcl == 0 || bkl == 0 {
		t.Fatalf("反证夹具本身没解析到靶子（copy=%d close=%d）", bcl, bkl)
	}
	if bcl < bkl {
		t.Fatalf("反证失效：反向顺序竟被判绿（copy=%d close=%d）", bcl, bkl)
	}
}

// TestChatImageStagingWired ⑥ 接线锁：handler 必须经 stageChatImage 落盘（否则上面两条锁的是孤儿代码）。
func TestChatImageStagingWired(t *testing.T) {
	src, err := os.ReadFile("chat_image.go")
	if err != nil {
		t.Fatalf("读取 chat_image.go 失败: %v", err)
	}
	if !strings.Contains(string(src), "stageChatImage(f, ext)") {
		t.Fatalf("handleChatImage 未接 stageChatImage 落盘（真件乘车的路径与锁不在同一条线上）")
	}
}

// ============ 多行分腿（2026-10-19）============
// 现网实证：主站 tencent/Hunyuan-MT-7B 对「多行 join 成一段」的输入会丢行——
// 4 行稳定只回 1 行 "and"、2 行偶发丢首行，行数对不上 ⇒ 过去直接 line_mismatch 不出图。
// 下面这组锁要做的是：**把"分行还原"这件事本身钉死**（逐行内容、顺序、条数），
// 而不是只断言"没报错"——只问 error 的断言在模型把译文位置写错时照样绿。

// TestChatImageLegsRestoresLineStructure 模拟现网丢行形态：多行只回 1 行 / 两行丢首行。
// 分腿后必须逐行还原（内容、顺序、条数三者一起判），且积分按真实调用次数累计。
func TestChatImageLegsRestoresLineStructure(t *testing.T) {
	lines := []string{"已同步", "产品说明书", "请在首次使用前完整阅读"}
	var calls int
	fake := func(text string) (string, int64, error) {
		calls++
		parts := strings.Split(text, "\n")
		switch {
		case len(parts) >= 3:
			return "and", 1, nil // 现网读数：3/3 次只回这一行
		case len(parts) == 2:
			return parts[1] + "_en", 1, nil // 两行输入丢首行
		default:
			return parts[0] + "_en", 1, nil
		}
	}
	got, points, err := chatImageLegs(lines, fake)
	if err != nil {
		t.Fatalf("分腿失败: %v", err)
	}
	want := []string{"已同步_en", "产品说明书_en", "请在首次使用前完整阅读_en"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("逐行结果不对（顺序/内容）：\n got=%q\nwant=%q", got, want)
	}
	if calls <= 1 {
		t.Fatalf("分腿没发生（calls=%d）：这条锁退化成了单枪成功路径", calls)
	}
	if points != int64(calls) {
		t.Fatalf("积分累计不对：points=%d calls=%d（每腿 1 分）", points, calls)
	}
}

// TestChatImageLegsFastPathSingleCall 模型保持行数时只发一枪（成本闸：正常图片不为兜底付钱）。
func TestChatImageLegsFastPathSingleCall(t *testing.T) {
	lines := []string{"甲", "乙", "丙", "丁"}
	var calls int
	fake := func(text string) (string, int64, error) {
		calls++
		parts := strings.Split(text, "\n")
		for i := range parts {
			parts[i] += "_en"
		}
		return strings.Join(parts, "\n"), 2, nil
	}
	got, points, err := chatImageLegs(lines, fake)
	if err != nil {
		t.Fatalf("快路径失败: %v", err)
	}
	if calls != 1 || points != 2 {
		t.Fatalf("行数已对齐时不该二分：calls=%d points=%d", calls, points)
	}
	if strings.Join(got, "|") != "甲_en|乙_en|丙_en|丁_en" {
		t.Fatalf("快路径逐行结果不对: %q", got)
	}
}

// TestChatImageLegsBlankLinesKeptAndSkipped 空行不送模型，但输出条数必须与 OCR 行等长（下标一一对应）。
// ★ 夹具用**真单词**而不是 `a`/`b`：单个 ASCII 字母按 M9 的判据属「无字母行」（不需要翻译），
// 会被 Go 侧预填、根本不送模型——那会让本用例测不到"空行跳过"这件事（夹具自己换了靶子）。
func TestChatImageLegsBlankLinesKeptAndSkipped(t *testing.T) {
	lines := []string{"alpha", "", "beta"}
	var inputs []string
	fake := func(text string) (string, int64, error) {
		inputs = append(inputs, text)
		parts := strings.Split(text, "\n")
		for i := range parts {
			parts[i] += "_en"
		}
		return strings.Join(parts, "\n"), 1, nil
	}
	got, _, err := chatImageLegs(lines, fake)
	if err != nil {
		t.Fatalf("空行场景失败: %v", err)
	}
	if len(got) != len(lines) || got[1] != "" {
		t.Fatalf("输出必须与 OCR 行等长（空行占位）：%q", got)
	}
	if strings.Join(got, "|") != "alpha_en||beta_en" {
		t.Fatalf("空行两侧的行错位: %q", got)
	}
	if len(inputs) != 1 || inputs[0] != "alpha\nbeta" {
		t.Fatalf("空行不该送模型：inputs=%q", inputs)
	}
}

// TestChatImageLegsSplitsOnEmptyCell 行数对得上、但某一行译文为空时**不能采用**
// （那一行会在图上消失），要继续二分；模型对单行正常时每一行都必须救回来。
// ★ 夹具用真单词（单个 ASCII 字母属 M9 的「无字母行」，会被预填不送模型，见上一条）。
func TestChatImageLegsSplitsOnEmptyCell(t *testing.T) {
	lines := []string{"Alpha", "Beta", "Gamma", "Delta"}
	fake := func(text string) (string, int64, error) {
		parts := strings.Split(text, "\n")
		out := make([]string, len(parts))
		for i, p := range parts {
			if p == "Beta" && len(parts) > 1 {
				out[i] = "" // 整段时 Beta 这一行丢内容。
			} else {
				out[i] = p + "_en"
			}
		}
		return strings.Join(out, "\n"), 1, nil
	}
	got, points, err := chatImageLegs(lines, fake)
	if err != nil {
		t.Fatalf("有空行时应继续二分而不是失败: %v", err)
	}
	if strings.Join(got, "|") != "Alpha_en|Beta_en|Gamma_en|Delta_en" {
		t.Fatalf("空行没被救回（逐行结果错）: %q", got)
	}
	if points != 5 {
		t.Fatalf("应恰好 5 枪（整段 1 + [Alpha,Beta] 1 + 单句 2 + [Gamma,Delta] 1）：points=%d", points)
	}
}

// TestChatImageLegsRejectsUnusableSingleLine 单行是二分终点：仍回空/多行 ⇒ 报错（宁可不出图，不画空字）。
func TestChatImageLegsRejectsUnusableSingleLine(t *testing.T) {
	cases := []struct {
		name string
		out  string
	}{
		{"回空", "  "},
		{"回多行", "x\ny"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, err := chatImageLegs([]string{"原文"}, func(string) (string, int64, error) {
				return c.out, 1, nil
			})
			if err == nil {
				t.Fatalf("单行不可用译文竟被判成功（out=%q）", c.out)
			}
		})
	}
	_, _, err := chatImageLegs([]string{"原文"}, func(string) (string, int64, error) {
		return "", 1, errors.New("upstream boom")
	})
	if err == nil {
		t.Fatalf("上游失败必须向上传递（不许当成空译文吞掉）")
	}
}

// TestAlignChatImageTranslationsSplitsOnlyBrokenLangs 只重译行数对不上的语种；
// 已对齐的语种零额外调用、原文不动（多语种请求不许为一条坏腿全场重翻）。
func TestAlignChatImageTranslationsSplitsOnlyBrokenLangs(t *testing.T) {
	lines := []string{"一", "二"}
	got := map[string]string{"en": "and", "ja": "一\n二"}
	calls := map[string]int{}
	translate := func(lang, text string) (string, int64, error) {
		calls[lang]++
		if lang != "en" {
			t.Fatalf("已对齐的语种不该被重译：lang=%s text=%q", lang, text)
		}
		parts := strings.Split(text, "\n")
		if len(parts) > 1 {
			return "and", 3, nil
		}
		return parts[0] + "_en", 3, nil
	}
	out, split, points, err := alignChatImageTranslations(lines, []string{"en", "ja"}, got, translate)
	if err != nil {
		t.Fatalf("对齐失败: %v", err)
	}
	if strings.Join(split, ",") != "en" {
		t.Fatalf("只应重译 en：split=%v", split)
	}
	if out["en"] != "一_en\n二_en" {
		t.Fatalf("en 分腿结果不对: %q", out["en"])
	}
	if out["ja"] != "一\n二" {
		t.Fatalf("ja 原文被改动: %q", out["ja"])
	}
	if calls["ja"] != 0 {
		t.Fatalf("ja 被多打了一枪: calls=%d", calls["ja"])
	}
	if points != 9 {
		t.Fatalf("附加积分应为 3 枪 × 3：points=%d", points)
	}

	// 快路径：全部对齐 ⇒ 零调用、零积分。
	calls = map[string]int{}
	out, split, points, err = alignChatImageTranslations(lines, []string{"en", "ja"},
		map[string]string{"en": "one\ntwo", "ja": "一\n二"}, translate)
	if err != nil || len(split) != 0 || points != 0 || len(calls) != 0 {
		t.Fatalf("全部对齐却重译了：split=%v points=%d calls=%v err=%v", split, points, calls, err)
	}
	if out["en"] != "one\ntwo" {
		t.Fatalf("快路径译文被改写: %q", out["en"])
	}
}

// TestChatImageLinePairsByPosition 多行正向配对：行数相等时每一行取**同下标**的译文（不按文本猜）。
func TestChatImageLinePairsByPosition(t *testing.T) {
	lines := []string{"已同步", "产品说明书", "已同步"}
	m, ok := chatImageLinePairs(lines, "  Synced \n Product manual \n Synced  ")
	if !ok {
		t.Fatalf("行数相等却判失败")
	}
	if len(m) != 2 || m["已同步"] != "Synced" || m["产品说明书"] != "Product manual" {
		t.Fatalf("配对结果不对: %#v", m)
	}
	if _, ok := chatImageLinePairs(lines, "Synced\nProduct manual"); ok {
		t.Fatalf("行数不等（3 vs 2）竟判成功＝会把译文盖到错误的框上")
	}
	if _, ok := chatImageLinePairs(lines, "   "); ok {
		t.Fatalf("空译文竟判成功")
	}
}

// TestChatImageLegsStopsAtCallBudget 模型对任意多行输入都只回 1 行时，分腿必须在上限内停手
// （不把一次即时翻译拖成几十枪）；错误向上传递 ⇒ 不出图，而不是画出错位的图。
func TestChatImageLegsStopsAtCallBudget(t *testing.T) {
	lines := make([]string, 64)
	for i := range lines {
		lines[i] = fmt.Sprintf("line-%02d", i)
	}
	var calls int
	got, points, err := chatImageLegs(lines, func(string) (string, int64, error) {
		calls++
		if calls > chatImageLegMaxCalls {
			t.Fatalf("越过调用上限仍在打上游：calls=%d", calls)
		}
		return "and", 1, nil
	})
	if err == nil {
		t.Fatalf("64 行整段丢行竟判成功")
	}
	if calls != chatImageLegMaxCalls {
		t.Fatalf("应在第 %d 枪停手，实际 %d", chatImageLegMaxCalls, calls)
	}
	if points != int64(calls) {
		t.Fatalf("已发生的真实用量必须照实累计：points=%d calls=%d", points, calls)
	}
	if len(got) != len(lines) {
		t.Fatalf("输出形状必须与 OCR 行等长：got=%d lines=%d", len(got), len(lines))
	}
}

// chatImageLegWired 源码级接线判据（剥注释后逐条点名）：
// ① handler 必须调用分腿对齐；② 对齐结果必须写回 res.Data.Translations 并累加积分；
// ③ 出图必须吃**对齐后**的译文（回退成一次性结果＝分腿白做）。
func chatImageLegWired(src string) error {
	body := stripGoCommentsForGate(src)
	for _, want := range []string{
		"aligned, splitLangs, legPoints, legErr := s.chatImageAlignTranslations(ctx, options, lines, targets, res.Data.Translations)",
		"res.Data.Translations = aligned",
		"res.PointsUsed += legPoints",
		"renderChatImage(r, tmpPath, targets[0], lines, res.Data.Translations[targets[0]], &resp)",
		"tr, ok := chatImageLinePairs(lines, translated)",
	} {
		if !strings.Contains(body, want) {
			return errors.New("缺接线: " + want)
		}
	}
	return nil
}

// TestChatImageLegSplitWired 分腿必须真接在请求链上（纯函数锁看不见"谁调用它"）。
// 反证用**变异体**：把对齐结果写回与出图入参摘掉，判据必须当场红（否则说明它只是问文件里有没有这些字）。
func TestChatImageLegSplitWired(t *testing.T) {
	b, err := os.ReadFile("chat_image.go")
	if err != nil {
		t.Fatalf("读取 chat_image.go 失败: %v", err)
	}
	src := string(b)
	if err := chatImageLegWired(src); err != nil {
		t.Fatalf("分腿未接线：%v", err)
	}
	mutations := []struct{ old, new string }{
		{"res.Data.Translations = aligned", "res.Data.Translations = res.Data.Translations"},
		{"renderChatImage(r, tmpPath, targets[0], lines, res.Data.Translations[targets[0]], &resp)",
			"renderChatImage(r, tmpPath, targets[0], lines, \"\", &resp)"},
	}
	for i, m := range mutations {
		bad := strings.Replace(src, m.old, m.new, 1)
		if bad == src || !strings.Contains(bad, m.new) {
			t.Fatalf("反证 %d 的变异体没生效（no-op 会把自己伪装成「锁抓住了破坏」）", i+1)
		}
		if chatImageLegWired(bad) == nil {
			t.Fatalf("反证 %d 失效：破坏接线后判据仍绿", i+1)
		}
	}
}

// ============ M9：非语言行（符号/箭头/数字）不送模型、交付原文 ============
// 现网实证：上下箭头图标被 OCR 读成 `1`/`<`。旧形态把这两行也拨给模型，
// 模型回 "and" 后行数照样对齐 ⇒ 「已对齐」快路径放行，英文单词盖住图标。
// 修法两处：分腿时预填（根本不送模型）＋快路径逐行钉回原文（送过也不能采用噪声）。

// TestChatImageLegsPrefillsSymbolLines ① 无字母行不进模型，译文位硬回原文；真文字照常送翻。
func TestChatImageLegsPrefillsSymbolLines(t *testing.T) {
	lines := []string{"⇅", "Hello", "¥1003.00", "1"}
	var inputs []string
	fake := func(text string) (string, int64, error) {
		inputs = append(inputs, text)
		return strings.ReplaceAll(text, "Hello", "你好"), 1, nil
	}
	got, points, err := chatImageLegs(lines, fake)
	if err != nil {
		t.Fatalf("预填场景失败: %v", err)
	}
	want := []string{"⇅", "你好", "¥1003.00", "1"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("符号行没被钉回原文：\n got=%q\nwant=%q", got, want)
	}
	if len(inputs) != 1 || inputs[0] != "Hello" {
		t.Fatalf("无字母行竟被送进模型（只应送 Hello）：inputs=%q", inputs)
	}
	if points != 1 {
		t.Fatalf("只为真文字那一腿付费，points=%d", points)
	}
}

// TestAlignChatImagePinsIdentityOnFastPath ② 快路径（行数已对齐）也要钉：
// 模型把 `<` 译成 "and"，行数 2 vs 2 完全对得上——旧形态原样放行，图标被英文单词盖掉。
func TestAlignChatImagePinsIdentityOnFastPath(t *testing.T) {
	lines := []string{"<", "Hello"}
	calls := 0
	translate := func(lang, text string) (string, int64, error) {
		calls++
		return "", 0, errors.New("快路径不该打上游")
	}
	out, split, points, err := alignChatImageTranslations(lines, []string{"en"},
		map[string]string{"en": "and\nHi"}, translate)
	if err != nil {
		t.Fatalf("快路径失败: %v", err)
	}
	if out["en"] != "<\nHi" {
		t.Fatalf("快路径没把符号行钉回原文：%q", out["en"])
	}
	if calls != 0 || len(split) != 0 || points != 0 {
		t.Fatalf("快路径不许重译：calls=%d split=%v points=%d", calls, split, points)
	}
}

// TestPinChatImageIdentityLinesKeepsMismatchUnchanged ③ 行数对不上时原样返回：
// 对齐契约由 chatImageLinePairs 判，这里不替它"修好"——否则会把"对不上"悄悄改成"看起来对上了"。
func TestPinChatImageIdentityLinesKeepsMismatchUnchanged(t *testing.T) {
	in := "only one line"
	if got := pinChatImageIdentityLines([]string{"<", "Hello"}, in); got != in {
		t.Fatalf("行数不等时必须原样返回（由配对函数判失败），实际改写成 %q", got)
	}
}

// TestStartImageKeepAliveKeepsJSONParsable 长请求保活的接线锁（★ 2026-10-26）：
// 心跳写出的空白必须是**合法 JSON 前导字符**，停表之后不许再往同一个 ResponseWriter 写一个字节
// ——后者是这条修法唯一的硬风险（并发写会把最终 JSON 撕开，前端从"等很久"变成"永远解析失败"）。
func TestStartImageKeepAliveKeepsJSONParsable(t *testing.T) {
	rec := httptest.NewRecorder()
	stop := startImageKeepAlive(rec, 10*time.Millisecond)
	time.Sleep(35 * time.Millisecond) // 至少两拍心跳
	stop()
	n := rec.Body.Len()
	if n == 0 {
		t.Fatal("心跳一拍都没写出（边缘仍会按空闲超时掐断这一跳）")
	}
	// stop 之后必须静默：再等两拍，字节数不许再变。
	time.Sleep(30 * time.Millisecond)
	if rec.Body.Len() != n {
		t.Fatalf("stop 之后仍在写心跳：before=%d after=%d", n, rec.Body.Len())
	}
	fmt.Fprint(rec, `{"text":"ok"}`)
	body := strings.TrimSpace(rec.Body.String())
	if !strings.HasPrefix(body, "{") || !strings.HasSuffix(body, "}") {
		t.Fatalf("最终响应体不是以 JSON 收口：%q", body)
	}
	var out map[string]string
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("前导空白后的 JSON 解析失败（前端会报图片翻译失败）：%v body=%q", err, rec.Body.String())
	}
	if out["text"] != "ok" {
		t.Fatalf("出参被心跳污染：%v", out)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("Content-Type=%q（前端按 JSON 解析，不能被保活改掉）", ct)
	}
}

// TestStartImageKeepAliveStopIsIdempotent stop 重复调用必须安全：
// handler 里既有显式调用（写终稿前）也有 defer，重复触发不许 panic 或重复关 channel。
func TestStartImageKeepAliveStopIsIdempotent(t *testing.T) {
	rec := httptest.NewRecorder()
	stop := startImageKeepAlive(rec, 5*time.Millisecond)
	stop()
	stop()
}
