// ============ pdfwrite.go · 职责说明 ============
// fileproc 包 PDF 译文排版回写实现。
// 优先通过 Python fpdf2/pdf2docx 子进程重建版式，回退 go-pdf/fpdf 纯 Go 实现。
//   - 版式策略：A4 页面；首段启发式标题（≤60 字符时加大居左）；正文段落流式排版自动分页；页脚页码
//   - 字体解析顺序（ResolvePDFFont）：system_config/env pdf_font_path → 常见系统 TTF → 内置
//     assets/fonts/DroidSansFallbackFull.ttf（Apache 2.0，覆盖中日韩英）
//   - 子进程控制：受 nice 低优先级 + 资源闸 + context 超时/取消约束
//   - 说明：PDF 原生内容流无法安全替换文字（字体子集/CID 编码），业界通行做法即版式重建；
//     产物为可读性优先的译文 PDF，源文对照另有 xlsx 通道兜底
// =============================================
package fileproc

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/go-pdf/fpdf"
)

// 常见系统 CJK TTF 候选（ttc 字体集合 fpdf 不支持，仅列单文件 TTF）
var systemFontCandidates = []string{
	"/usr/share/fonts/truetype/droid/DroidSansFallbackFull.ttf", // Debian/Ubuntu fonts-droid-fallback
	"/usr/share/fonts/droid/DroidSansFallbackFull.ttf",
	"/System/Library/Fonts/Supplemental/Songti.ttc", // macOS（ttc 会失败，占位提示用）
	"C:/Windows/Fonts/msyh.ttc",
}

// bundledFontProbe 测试注入点：非空时覆盖内置字体路径（单测模拟无字体环境置空串无效，用专用变量控制）。
var bundledFontProbe = "" // 置 "skip" 时跳过内置字体探测

// bundledFontPath 内置字体路径（编译期相对本文件定位，随二进制分发无需安装）。
func bundledFontPath() string {
	if bundledFontProbe == "skip" {
		return ""
	}
	_, self, _, _ := runtime.Caller(0)
	if self != "" && !strings.HasPrefix(self, "/") && !strings.Contains(self, ":\\") {
		self = "/" + self // 部分构建环境 Caller 丢失根斜杠
	}
	return filepath.Join(filepath.Dir(self), "assets", "fonts", "DroidSansFallbackFull.ttf")
}

// ResolvePDFFont 解析可用 CJK 字体：
//  1. 环境变量 PDF_FONT_PATH（优先级最高，指向任意可嵌入的 TTF）
//  2. 常见系统路径逐个探测
//  3. 内置 DroidSansFallbackFull.ttf
//
// 返回空串表示无可用字体（调用方应降级 xlsx 对照表）。
func ResolvePDFFont() string {
	if p := os.Getenv("PDF_FONT_PATH"); p != "" {
		if isUsableTTF(p) {
			return p
		}
	}
	for _, p := range systemFontCandidates {
		if isUsableTTF(p) && !strings.HasSuffix(strings.ToLower(p), ".ttc") {
			return p
		}
	}
	if p := bundledFontPath(); isUsableTTF(p) {
		return p
	}
	return ""
}

// isUsableTTF 判断字体文件存在且可用（Python fpdf2 支持 TTC，Go fpdf 仅支持单 TTF）。
func isUsableTTF(p string) bool {
	if p == "" {
		return false
	}
	st, err := os.Stat(p)
	return err == nil && !st.IsDir() && st.Size() > 1024
}

// WriteTranslatedPDF 生成译文 PDF（版式重建）。
// 参数：ctx=上下文（子进程超时/取消）；outPath=输出文件路径；srcTexts=按阅读顺序排列的源文段落；
// translations=原文→译文映射（与引擎 langTranslations 同构：未命中的段落回退显示原文）。
// 返回错误：无可用字体或写出失败；调用方应降级 xlsx 对照表。
func WriteTranslatedPDF(ctx context.Context, outPath string, srcTexts []string, translations map[string]string) error {
	fontPath := ResolvePDFFont()
	if fontPath == "" {
		return fmt.Errorf("无可用 CJK TTF 字体（可配置环境变量 PDF_FONT_PATH 指向 .ttf 文件）")
	}
	// 优先使用 Python fpdf2（更稳定地支持 CJK 字体嵌入）
	if err := writePDFViaPython(ctx, outPath, fontPath, srcTexts, translations); err == nil {
		return nil
	}
	// 回退 Go fpdf
	return writePDFViaGoFpdf(outPath, fontPath, srcTexts, translations)
}

// WriteTranslatedPDFviaDocx PDF→DOCX→翻译→DOCX→PDF（保留排版/图表；图片内容按产品策略不翻译）。
// 参数：ctx=子进程超时/取消上下文；outPath=输出 PDF 路径；inPath=输入 PDF 路径；
//       translations=原文→译文映射；lang=目标语言代码。
// 返回错误：子进程失败时返回带尾部 stderr 详情的错误。
func WriteTranslatedPDFviaDocx(ctx context.Context, outPath, inPath string, translations map[string]string, lang string) error {
	payload, _ := json.Marshal(map[string]interface{}{
		"translations": translations,
	})
	bin, argv := wrapNice(pyBin(), append([]string{docxScriptPath()}, "legacy", inPath, outPath, lang))
	_, _, err := runSubprocess(ctx, fileprocTimeout(), bin, argv, payload)
	return err
}

// ExtractTextsPdfDocx 经 pdf2docx 提取段落文本（键与替换目标完全一致，表格必中）。
// 参数：ctx=子进程超时/取消上下文；pdfPath=输入 PDF 路径。
// 返回 (文本键列表, 缓存 DOCX 路径, 错误)；调用方负责删除缓存 DOCX。
// 图片型文档按产品策略不做内容翻译——提取失败时由调用方降级 pdftotext/xlsx 对照表。
// 上下文超时/取消贯通 + 创建缓存前清扫 >24h 崩溃残留。
func ExtractTextsPdfDocx(ctx context.Context, pdfPath string) ([]string, string, error) {
	sweepStalePdfDocxCache()
	cache := filepath.Join(os.TempDir(), fmt.Sprintf("pdfdocx_%d.docx", time.Now().UnixNano()))
	out, err := runDocxScript(ctx, []string{"extract", pdfPath, cache})
	if err != nil {
		return nil, "", err
	}
	var r struct {
		Success bool     `json:"success"`
		Texts   []string `json:"texts"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return nil, "", err
	}
	if !r.Success || len(r.Texts) == 0 {
		return nil, "", fmt.Errorf("extract 无文本")
	}
	return r.Texts, cache, nil
}

// ApplyTranslatedPdfFromDocx 在已缓存 DOCX 副本上应用译文并转 PDF（含图片 OCR）。
// 参数：ctx=子进程超时/取消上下文；outPath=输出 PDF 路径；cacheDocx=ExtractTextsPdfDocx 生成的缓存 DOCX；
//       translations=原文→译文映射；lang=目标语言代码。
// 返回错误：子进程失败时返回错误。
func ApplyTranslatedPdfFromDocx(ctx context.Context, outPath, cacheDocx string, translations map[string]string, lang string) error {
	payload, _ := json.Marshal(map[string]interface{}{
		"translations": translations,
	})
	if err := runDocxScriptStdin(ctx, []string{"apply", cacheDocx, outPath, lang}, payload); err != nil {
		return err
	}
	return nil
}

// docxScriptPath 定位 docx_translate.py 脚本路径（与可执行文件同目录）。
func docxScriptPath() string {
	return filepath.Join(filepath.Dir(os.Args[0]), "docx_translate.py")
}

// pyBin 解析 Python 解释器：优先生产 venv（fpdf2/pdf2docx 所在），回退 PATH python3。
func pyBin() string {
	if _, err := os.Stat("/opt/translator/.venv/bin/python3"); err == nil {
		return "/opt/translator/.venv/bin/python3"
	}
	return "python3"
}

// truncateTail 错误上下文取尾部 4KB（防超长 stderr 撑爆日志/错误字段）
func truncateTail(b []byte) string {
	s := string(b)
	if len(s) > 4096 {
		s = s[len(s)-4096:]
	}
	return s
}

// runDocxScript 调用 Python docx 管线脚本（extract 子命令）。
// 经资源闸串行化 + nice 低优先级运行；runSubprocess 受控执行——
// stdout/stderr 分离后 JSON 直接取自 stdout，不再依赖「最后一个 '{'」的脆弱启发式。
// 参数：args=脚本子命令与参数；返回：stdout 内容（JSON）与错误。
func runDocxScript(ctx context.Context, args []string) ([]byte, error) {
	bin, argv := wrapNice(pyBin(), append([]string{docxScriptPath()}, args...))
	stdout, stderr, err := runSubprocess(ctx, fileprocTimeout(), bin, argv, nil)
	if err != nil {
		return stdout, fmt.Errorf("docx_translate %v 失败: %w\n%s", args, err, truncateTail(stderr))
	}
	return stdout, nil
}

// runDocxScriptStdin 以 stdin 传入大 payload（docx 字节流等）调用 docx 管线脚本，
// 规避命令行长度限制。资源闸 + nice 低优先级运行 + 受控执行。
func runDocxScriptStdin(ctx context.Context, args []string, payload []byte) error {
	bin, argv := wrapNice(pyBin(), append([]string{docxScriptPath()}, args...))
	_, stderr, err := runSubprocess(ctx, fileprocTimeout(), bin, argv, payload)
	if err != nil {
		return fmt.Errorf("docx_translate %s 失败: %w\n%s", args[0], err, truncateTail(stderr))
	}
	return nil
}

// writePDFViaPython 走 Python(fpdf2) 管线写出 PDF：payload 含源句与译文映射，脚本路径与可执行文件同目录。
// 资源闸 + nice 低优先级运行 + 受控执行。
func writePDFViaPython(ctx context.Context, outPath string, fontPath string, srcTexts []string, translations map[string]string) error {
	payload, _ := json.Marshal(map[string]interface{}{
		"srcTexts":     srcTexts,
		"translations": translations,
	})
	scriptPath := filepath.Join(filepath.Dir(os.Args[0]), "pdfwrite.py")
	bin, argv := wrapNice(pyBin(), []string{scriptPath, outPath, fontPath})
	_, stderr, err := runSubprocess(ctx, fileprocTimeout(), bin, argv, payload)
	if err != nil {
		return fmt.Errorf("python pdfwrite 失败: %w\n%s", err, truncateTail(stderr))
	}
	return nil
}

// writePDFViaGoFpdf 纯 Go(fpdf) 兜底写出 PDF（Python 管线不可用时）：注册 CJK UTF8 字体逐段排版。
// 参数：outPath=输出 PDF 路径；fontPath=CJK 字体路径；srcTexts=源句序列；translations=句→译文映射；返回错误。
func writePDFViaGoFpdf(outPath string, fontPath string, srcTexts []string, translations map[string]string) error {
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.SetMargins(18, 18, 18)
	pdf.SetAutoPageBreak(true, 20)
	fontBytes, err := os.ReadFile(fontPath)
	if err != nil {
		return fmt.Errorf("读取字体失败 %s: %w", fontPath, err)
	}
	pdf.AddUTF8FontFromBytes("cjk", "", fontBytes)
	pdf.AddPage()
	pdf.SetFooterFunc(func() {
		pdf.SetY(-14)
		pdf.SetFont("cjk", "", 8)
		pdf.SetTextColor(130, 130, 130)
		pdf.CellFormat(0, 8, fmt.Sprintf("%d / %d", pdf.PageNo(), pdf.PageCount()), "", 0, "C", false, 0, "")
	})

	const (
		titleSize = 15.0
		bodySize  = 10.5
		lineGap   = 6.2
	)

	first := true
	for _, src := range srcTexts {
		src = strings.TrimSpace(src)
		if src == "" {
			continue
		}
		text := src
		if t := strings.TrimSpace(translations[src]); t != "" {
			text = t
		}
		if first && len([]rune(src)) <= 60 {
			pdf.SetFont("cjk", "", titleSize)
			pdf.MultiCell(0, titleSize*0.62, text, "", "L", false)
			pdf.Ln(3.5)
			first = false
			continue
		}
		first = false
		pdf.SetFont("cjk", "", bodySize)
		pdf.MultiCell(0, lineGap, text, "", "L", false)
		pdf.Ln(1.6)
	}

	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return err
	}
	return pdf.OutputFileAndClose(outPath)
}

// PdfImageHeavy 判定图片型 PDF：平均每页文本层字符 < 200（引导用户改传 Word 源文件）。
// 参数：pdfPath=待检测 PDF 路径。
// 返回 true 表示文本层稀薄，疑似扫描件/图片型 PDF。
// 外部工具调用加 30s 超时（FILEPROC_SUB_TIMEOUT_SEC 可调）。
func PdfImageHeavy(pdfPath string) bool {
	sctx, scancel := context.WithTimeout(context.Background(), subTimeout())
	defer scancel()
	out, err := exec.CommandContext(sctx, "pdftotext", pdfPath, "-").Output()
	if err != nil {
		return false
	}
	pages := 1
	if info, ierr := exec.CommandContext(sctx, "pdfinfo", pdfPath).Output(); ierr == nil {
		for _, ln := range strings.Split(string(info), "\n") {
			if strings.HasPrefix(ln, "Pages:") {
				if v, e := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(ln, "Pages:"))); e == nil && v > 0 {
					pages = v
				}
			}
		}
	}
	chars := len(strings.TrimSpace(string(out)))
	return chars/pages < 200
}
