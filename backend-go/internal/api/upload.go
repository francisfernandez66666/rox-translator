// ============ upload.go · 职责说明 ============
// 文件上传接口：大小/类型校验、落盘命名（雪花ID_原文名）、返回引用路径。
// =============================================
package api

// ============ 本文件职责中文说明 ============
// 上传校验工具：统一校验上传文件的扩展名白名单与大小上限，防止恶意/超大文件耗尽磁盘与内存。
//  - validateUploadExt：校验扩展名是否在白名单内
//  - parseUpload：解析 multipart 表单并做大小/扩展名校验（供翻译文件与 KB 导入共用）
// ========================================

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	ledong "github.com/ledongthuc/pdf"
)

// 翻译文件支持的类型白名单（与 fileproc.ExtractTexts 提取逻辑一致）
var translateExtWhitelist = map[string]bool{
	".docx": true,
	".pptx": true,
	".xlsx": true,
	".pdf":  true, // 提取后产物降级 xlsx 对照表
	".txt":  true,
	".csv":  true,
	".srt":  true,
	".vtt":  true,
	".md":   true,
	".json": true,
	".yaml": true,
	".yml":  true,
}

// kbExtWhitelist 知识库导入支持的类型白名单（与 kb/parse.go 解析逻辑一致）
var kbExtWhitelist = map[string]bool{
	".xlsx": true,
	".xls":  true,
	".csv":  true,
}

// 上传大小上限（字节）
const (
	translateUploadMax = 40 << 20 // 翻译文件 40MB（原 200MB，过大文件易撑爆 1.6G 内存服务器）
	kbUploadMax        = 20 << 20 // KB 导入 20MB（表格文件通常远小于此）

	// ★ 性能优化（不换库 Phase A1）：PDF 在低配机器（1G 内存）上走 pdf2docx+LibreOffice
	// 转换极易 OOM/超时。前置拦截：体积或页数超限则直接友好拒绝，提示先转 docx 再上传。
	pdfUploadSizeLimit = 40 << 20 // 40MB：与翻译文件总上限对齐（PDF 转换高峰期仍可能顶满内存）
	pdfPageHardLimit   = 120       // 120 页：超出建议先转为 docx
)

// checkPdfLimits 对 PDF 做前置安全拦截：过大或页数过多会在文件转换阶段（pdf2docx+LibreOffice）
// 触发 OOM / 超时。提前友好拒绝并提示转 docx，避免后台工单卡死。
// 非 PDF 或无 read 权限/解析失败（加密件等）一律放行，不误伤正常请求。
func checkPdfLimits(path, filename string) error {
	if strings.ToLower(filepath.Ext(filename)) != ".pdf" {
		return nil
	}
	if fi, err := os.Stat(path); err == nil && fi.Size() > pdfUploadSizeLimit {
		return &apiErr{fmt.Sprintf("PDF 文件过大（%.1fMB，上限 %dMB）。低配环境转换容易失败/超时，请先转存 docx 再上传",
			float64(fi.Size())/1024/1024, pdfUploadSizeLimit/1024/1024)}
	}
	n, err := pdfPageCount(path)
	if err == nil && n > pdfPageHardLimit {
		return &apiErr{fmt.Sprintf("PDF 页数过多（%d 页，上限 %d 页）。低配环境转换容易失败/超时，请先转存 docx 再上传", n, pdfPageHardLimit)}
	}
	return nil
}

// pdfPageCount 读取 PDF 页数（纯 Go，无需外部依赖）。解析失败返回 error 由调用方放行。
func pdfPageCount(path string) (int, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	r, err := ledong.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		return 0, err
	}
	return r.NumPage(), nil
}

// validateUploadExt 校验文件扩展名是否在允许列表内（小写归一化）。
// 参数 filename: 上传文件名；whitelist: 允许的扩展名集合。
// 返回: true 表示允许上传。
func validateUploadExt(filename string, whitelist map[string]bool) bool {
	ext := strings.ToLower(filepath.Ext(filename))
	return whitelist[ext]
}

// parseUpload 解析 multipart 表单并校验大小与扩展名。
// 参数 r: HTTP 请求；maxBytes: 上传大小上限；whitelist: 扩展名白名单。
// 返回: 解析成功返回 nil；失败返回错误（已含中文提示）。
func parseUpload(r *http.Request, maxBytes int64, whitelist map[string]bool) error {
	// 用 MaxBytesReader 硬性限制请求体大小（ParseMultipartForm 超限部分会落临时文件，
	// 并不会报错，因此必须用 MaxBytesReader 拦截超大上传，防止撑爆磁盘/内存）
	r.Body = http.MaxBytesReader(nil, r.Body, maxBytes)
	if err := r.ParseMultipartForm(maxBytes); err != nil {
		return &apiErr{"文件过大（超过上限）"}
	}
	f, h, err := r.FormFile("file")
	if err != nil {
		return &apiErr{"缺少文件"}
	}
	defer f.Close()
	if !validateUploadExt(h.Filename, whitelist) {
		return &apiErr{"不支持的文件类型，仅支持 " + strings.Join(whitelistKeys(whitelist), " / ")}
	}
	return nil
}

// whitelistKeys 返回白名单扩展名列表（用于错误提示）。
func whitelistKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	// 收集白名单中的所有扩展名
	for k := range m {
		out = append(out, k)
	}
	return out
}
