// ============ file_extract_reason_test.go · 职责说明 ============
// 锁住 2026-10-15 OCR/ASR 批的一条出栈口径：**图片/音频两条新腿的失败理由必须原样带出**，
// 其余格式的既有文案一字不改。
//
// 三层射程，缺一不可：
//
//	R-1 判据级（extractFailureReason 直调）：格式集合 × 有无 error 的组合各钉一次，
//	      并先做一次"判定集合认得 .png/.wav"的正向对照——集合漂了要报「闸门已失明」，
//	      不能报成"产品行为差异"（AGENTS §一·14 的 P0 同族）。
//	R-2 守卫级（读 file.go 源码）：HandleFile 里那段 `if err != nil || len(texts) == 0` 必须
//	      真的调用这一个判据函数、入参是**实际被提取的那个路径**，并且**旧那句仍留在原地**。
//	      为什么判据级不够：把调用点整行删掉，extractFailureReason 的单测照样全绿，
//	      而客户屏幕上退回那句「无法从文件提取文本或文件为空」——这正是 AGENTS §三
//	      「判据级断言代替不了守卫级」点名的那一族假绿。
//	R-3 反证（喂合成源码给同一个判据函数）：摘掉调用 / 入参退回原始 filePath /
//	      纯文案模式不再跟随 mdAlignPath，三种破坏都必须判红，证明 R-2 真的看得见源码。
//
// 真机那一条端到端的腿在 UAT 矩阵 T71（建一张 png 工单 → 终态 rejected → 理由里必须出现
// OCR 与 assets/ocr），因为本机没有 OCR 权重，恰好让「能力缺失」那一支成为**实跑支**而不是摆设。
// ==========================================================
package engine

import (
	"errors"
	"os"
	"strings"
	"testing"

	"translator/internal/fileproc"
)

// ocrCapSentence / asrCapSentence 取 fileproc 真写的那两句里的一段做靶子。
// 刻意只抄**开头片段**而不是整句：整句逐字等值锁的是 fileproc 的文案（那边已有自己的锁），
// 本文件只管"这句有没有被原样送出去"，抄整句会让改文案的人误以为要来改这里。
const (
	ocrCapSentence   = "图片文字识别（OCR）不可用：服务器未安装 onnxruntime/numpy/Pillow 或未部署 OCR 模型资产（assets/ocr/"
	asrCapSentence   = "ASR 未识别出语音内容（音频可能无人声、纯音乐或已损坏）"
	legacyExtractErr = "无法从文件提取文本或文件为空" // 既有对外文案，本批一字未改
)

// readEngineSource 读本包源码（go test 的工作目录＝包目录，用相对路径，别写绝对路径）。
func readEngineSource(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("读取 %s 失败：%v ⇒ 本闸连射程都没有", name, err)
	}
	return string(b)
}

// dropGoComments 剥掉 Go 源码里的注释（行注释与块注释），**字符串与原始串里的 "//" 不算注释**。
// 为什么必须先剥：本文件与 file.go 的说明注释里都会出现「无法从文件提取文本或文件为空」
// 这一串（正是本批记录事故用的），按原始源码判"这句还在"会命中注释 ⇒ 把实现删干净也恒绿
// （AGENTS §一·9 那条「负向锁会被自己新写的注释再犯」的同形）。
func dropGoComments(src string) string {
	var out strings.Builder
	inLine, inBlock, inStr, inRaw := false, false, false, false
	for i := 0; i < len(src); {
		c := src[i]
		var next byte
		if i+1 < len(src) {
			next = src[i+1]
		}
		switch {
		case inLine:
			if c == '\n' {
				inLine = false
				out.WriteByte(c)
			}
			i++
		case inBlock:
			if c == '*' && next == '/' {
				inBlock = false
				i += 2
			} else {
				if c == '\n' {
					out.WriteByte(c) // 保行号形状，便于读数对照
				}
				i++
			}
		case inStr:
			out.WriteByte(c)
			if c == '\\' {
				if next != 0 && i+1 < len(src) {
					out.WriteByte(next)
					i += 2
					continue
				}
			}
			if c == '"' {
				inStr = false
			}
			i++
		case inRaw:
			out.WriteByte(c)
			if c == '`' {
				inRaw = false
			}
			i++
		default:
			switch {
			case c == '/' && next == '/':
				inLine = true
				i += 2
			case c == '/' && next == '*':
				inBlock = true
				i += 2
			case c == '"':
				inStr = true
				out.WriteByte(c)
				i++
			case c == '`':
				inRaw = true
				out.WriteByte(c)
				i++
			default:
				out.WriteByte(c)
				i++
			}
		}
	}
	return out.String()
}

// TestExtractFailureReasonSurfacesImageAndAudio 是 R-1：图片/音频 ⇒ 原样带出，其余 ⇒ 空串。
func TestExtractFailureReasonSurfacesImageAndAudio(t *testing.T) {
	// 正向对照先钉"闸门没失明"：判定集合必须真的认得这两个扩展名。
	//   集合若漂了（改名/搬走），下面图片那一组会退回空串，读数看起来像"配置差异"；
	//   这一条把它先判成「清单与判据脱钩」，指错方向的人少一个。
	if !fileproc.IsImageFormat(".png") || !fileproc.IsAudioFormat(".wav") {
		t.Fatalf("fileproc 的图片/音频判定集合认不出 .png/.wav ⇒ 本闸已失明（不是产品差异），检查 formats.go")
	}
	cases := []struct {
		name    string
		path    string
		err     error
		wantEq  string // 非空＝必须逐字等于该串（原样带出）
		wantEmp bool   // true＝必须回空串（由调用方保留既有文案）
	}{
		{"图片-能力缺失", "/tmp/a.png", errors.New(ocrCapSentence), ocrCapSentence, false},
		{"图片-大写扩展名", "/tmp/A.PNG", errors.New(asrCapSentence), asrCapSentence, false},
		{"音频-无内容", "/tmp/a.wav", errors.New(asrCapSentence), asrCapSentence, false},
		{"音频-mp3", "/tmp/a.mp3", errors.New("ASR 转写失败"), "ASR 转写失败", false},
		{"文档-错误不外投", "/tmp/a.docx", errors.New("panic: runtime error: index out of range [0]"), "", true},
		{"PDF-错误不外投", "/tmp/a.pdf", errors.New(ocrCapSentence), "", true},
		{"纯文本-错误不外投", "/tmp/a.txt", errors.New(asrCapSentence), "", true},
		{"无扩展名", "/tmp/blob", errors.New(ocrCapSentence), "", true},
		{"错误为空-真空文件", "/tmp/a.png", nil, "", true},
	}
	for _, c := range cases {
		got := extractFailureReason(c.path, c.err)
		if c.wantEmp {
			if got != "" {
				t.Errorf("%s: 期望空串（把既有文案留给调用方），实得 %q", c.name, got)
			}
			continue
		}
		if got != c.wantEq {
			t.Errorf("%s: 期望原样带出 %q，实得 %q", c.name, c.wantEq, got)
		}
	}
}

// TestExtractFailureKeepsLegacyCopyForOtherFormats 是 R-1 的第二半：
// 把"其余格式仍走旧文案"这条承诺钉在**旧那句本身**上，而不只钉判据函数的空串分支——
// 旧那句是对外文案（客户与既有工单轨迹都读它），改它＝改对外契约，必须与本批的放宽明确区分。
func TestExtractFailureKeepsLegacyCopyForOtherFormats(t *testing.T) {
	src := dropGoComments(readEngineSource(t, "file.go"))
	if !strings.Contains(src, `Error: "`+legacyExtractErr+`"`) {
		t.Fatalf("file.go 里那句既有对外文案 %q 不见了 ⇒ 本批把改动面外溢到 Office/PDF 了", legacyExtractErr)
	}
}

// reasonCallSiteMissing 是 R-2/R-3 共用的判据函数：**输入是源码文本**，返回缺项说明（空＝合格）。
// 拆成函数而不是内嵌在测试里，是为了让反证喂合成源码时走**同一份逻辑**
// （AGENTS §三：守卫拿到的输入必须由同一个函数算出来，否则反证测的是另一套判据）。
func reasonCallSiteMissing(src string) []string {
	var bad []string
	body := dropGoComments(src)
	idx := strings.Index(body, "if err != nil || len(texts) == 0 {")
	if idx < 0 {
		return []string{"找不到提取失败分支的锚点（if err != nil || len(texts) == 0）⇒ 结构漂了，本闸已失明"}
	}
	// 只截**那一个 if 块**：从锚点起到第一个函数体缩进的收尾 `}`。
	//   截到文件末尾会让"这句还在"被后面任意一处同形串满足（假绿），
	//   而"必须调用判据函数"也会被别处的同名调用满足。
	seg := body[idx:]
	if cut := strings.Index(seg, "\n\t}"); cut >= 0 {
		seg = seg[:cut]
	}
	if !strings.Contains(seg, "extractFailureReason(") {
		bad = append(bad, "失败分支里没有调用 extractFailureReason ⇒ 图片/音频的理由会被压成通用文案")
	}
	if !strings.Contains(seg, `Error: "`+legacyExtractErr+`"`) {
		bad = append(bad, "失败分支里没有保留既有通用文案 ⇒ 其余格式的对外文案被改动")
	}
	// 判据的输入必须是**实际被提取的那个路径**：纯文案模式下 anydoc 会先落一份 .md 副本再提取，
	// 拿原始 filePath 判"是不是图片"会把 Markdown 提取器的错误当图片错误投出去
	//（形态：客户上传 .epub 却被告知去装 OCR 权重）。
	if !strings.Contains(body, "extractFrom = mdAlignPath") {
		bad = append(bad, "提取源路径没有跟随 mdAlignPath ⇒ 纯文案模式下的错误归因会张冠李戴")
	}
	if !strings.Contains(seg, "extractFailureReason(extractFrom") {
		bad = append(bad, "extractFailureReason 的入参不是 extractFrom ⇒ 判的是原始上传件而非实际提取对象")
	}
	return bad
}

// TestExtractFailureReasonCallSiteIsWired 用真实源码跑一次 R-2。
func TestExtractFailureReasonCallSiteIsWired(t *testing.T) {
	for _, b := range reasonCallSiteMissing(readEngineSource(t, "file.go")) {
		t.Errorf("file.go 接线不合格：%s", b)
	}
}

// TestExtractFailureReasonCallSiteSelfProof 是 R-3：三种破坏都必须被抓住。
func TestExtractFailureReasonCallSiteSelfProof(t *testing.T) {
	real := readEngineSource(t, "file.go")
	if bad := reasonCallSiteMissing(real); len(bad) > 0 {
		t.Fatalf("反证的前提不成立：真实源码本身就不合格（%v）", bad)
	}
	mutations := []struct {
		name string
		mut  func(string) string
	}{
		{"M1 摘掉判据调用（只留旧那句）", func(s string) string {
			return strings.Replace(s, `if reason := extractFailureReason(extractFrom, err); reason != "" {`,
				`if reason := ""; reason != "" {`, 1)
		}},
		{"M2 入参退回原始 filePath（不看实际提取源）", func(s string) string {
			return strings.Replace(s, "extractFailureReason(extractFrom, err)", "extractFailureReason(filePath, err)", 1)
		}},
		{"M3 纯文案模式不再跟随 mdAlignPath", func(s string) string {
			return strings.Replace(s, "extractFrom = mdAlignPath", "_ = mdAlignPath", 1)
		}},
		{"M4 把整个失败分支的判据换掉（锚点漂走＝闸门失明的形态）", func(s string) string {
			return strings.Replace(s, "if err != nil || len(texts) == 0 {", "if err != nil {", 1)
		}},
	}
	for _, m := range mutations {
		got := reasonCallSiteMissing(m.mut(real))
		if len(got) == 0 {
			t.Errorf("反证 %s 没被抓到 ⇒ R-2 那条锁的射程是空的", m.name)
		}
	}
}
