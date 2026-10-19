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
	"errors"
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
