// ============ 本文件职责中文说明 ============
// PDF 译文排版回写：把翻译后的文本按阅读版式重建为 PDF（go-pdf/fpdf 纯 Go 实现，无外部依赖）。
//   - 版式策略：A4 页面；首段启发式标题（≤60 字符时加大居左）；正文段落流式排版自动分页；页脚页码
//   - 字体解析顺序（ResolvePDFFont）：system_config/env pdf_font_path → 常见系统 TTF → 内置
//     assets/fonts/DroidSansFallbackFull.ttf（Apache 2.0，覆盖中日韩英）
//   - 说明：PDF 原生内容流无法安全替换文字（字体子集/CID 编码），业界通行做法即版式重建；
//     产物为可读性优先的译文 PDF，源文对照另有 xlsx 通道兜底
// =============================================
package fileproc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
// 参数：outPath=输出文件路径；srcTexts=按阅读顺序排列的源文段落；translations=原文→译文映射
// （与引擎 langTranslations 同构：未命中的段落回退显示原文）。
// 返回错误：无可用字体或写出失败；调用方应降级 xlsx 对照表。
func WriteTranslatedPDF(outPath string, srcTexts []string, translations map[string]string) error {
	fontPath := ResolvePDFFont()
	if fontPath == "" {
		return fmt.Errorf("无可用 CJK TTF 字体（可配置环境变量 PDF_FONT_PATH 指向 .ttf 文件）")
	}
	// 优先使用 Python fpdf2（更稳定地支持 CJK 字体嵌入）
	if err := writePDFViaPython(outPath, fontPath, srcTexts, translations); err == nil {
		return nil
	}
	// 回退 Go fpdf
	return writePDFViaGoFpdf(outPath, fontPath, srcTexts, translations)
}

// WriteTranslatedPDFviaDocx PDF→DOCX→翻译→DOCX→PDF（保留排版+图表+图片OCR）
func WriteTranslatedPDFviaDocx(outPath, inPath string, translations map[string]string, lang string) error {
	payload, _ := json.Marshal(map[string]interface{}{
		"translations": translations,
	})
	return runDocxScriptStdin([]string{"legacy", inPath, outPath, lang}, payload)
}

// ExtractTextsPdfDocx 经 pdf2docx 提取段落文本（键与替换目标完全一致，表格必中）。
// 返回文本列表与缓存的 DOCX 路径（供后续 apply 复用，调用方负责删除）。
func ExtractTextsPdfDocx(pdfPath string) ([]string, string, error) {
	cache := filepath.Join(os.TempDir(), fmt.Sprintf("pdfdocx_%d.docx", time.Now().UnixNano()))
	out, err := runDocxScript([]string{"extract", pdfPath, cache})
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
func ApplyTranslatedPdfFromDocx(outPath, cacheDocx string, translations map[string]string, lang string) error {
	payload, _ := json.Marshal(map[string]interface{}{
		"translations": translations,
	})
	return runDocxScriptStdin([]string{"apply", cacheDocx, outPath, lang}, payload)
}

func docxScriptPath() string {
	return filepath.Join(filepath.Dir(os.Args[0]), "docx_translate.py")
}

func runDocxScript(args []string) ([]byte, error) {
	pyBin := "python3"
	if _, err := os.Stat("/opt/translator/.venv/bin/python3"); err == nil {
		pyBin = "/opt/translator/.venv/bin/python3"
	}
	cmd := exec.Command(pyBin, append([]string{docxScriptPath()}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("docx_translate %v 失败: %w\n%s", args, err, string(out))
	}
	// extract 的 stdout 是 JSON；CombinedOutput 可能混入 stderr 日志，取最后一行 JSON
	idx := bytes.LastIndexByte(out, '{')
	if idx >= 0 {
		return out[idx:], nil
	}
	return out, nil
}

func runDocxScriptStdin(args []string, payload []byte) error {
	pyBin := "python3"
	if _, err := os.Stat("/opt/translator/.venv/bin/python3"); err == nil {
		pyBin = "/opt/translator/.venv/bin/python3"
	}
	cmd := exec.Command(pyBin, append([]string{docxScriptPath()}, args...)...)
	cmd.Stdin = bytes.NewReader(payload)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docx_translate %v 失败: %w\n%s", args[0], err, string(output))
	}
	return nil
}

func writePDFViaPython(outPath string, fontPath string, srcTexts []string, translations map[string]string) error {
	payload, _ := json.Marshal(map[string]interface{}{
		"srcTexts":     srcTexts,
		"translations": translations,
	})
	// 优先使用 venv 内的 Python（fpdf2 安装于虚拟环境）
	pyBin := "python3"
	if _, err := os.Stat("/opt/translator/.venv/bin/python3"); err == nil {
		pyBin = "/opt/translator/.venv/bin/python3"
	}
	scriptPath := filepath.Join(filepath.Dir(os.Args[0]), "pdfwrite.py")
	cmd := exec.Command(pyBin, scriptPath, outPath, fontPath)
	cmd.Stdin = strings.NewReader(string(payload))
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("python pdfwrite 失败: %w\n%s", err, string(output))
	}
	return nil
}

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
