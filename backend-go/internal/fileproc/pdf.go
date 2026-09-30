// ============ pdf.go · 职责说明 ============
// fileproc 包 PDF 文本提取实现。
// 优先使用 pdftotext CLI（poppler-utils，完美支持 CJK）；
// CLI 不可用时回退 ledongthuc/pdf（纯 Go，无 CGO）逐页读取文本。
// PDF 无原格式回写能力，翻译产物由引擎层统一降级为 xlsx 对照表（见 engine/file.go 第3步）。
// 加密/扫描件（图片型）无法提取文本时返回可读错误。
// =============================================
package fileproc

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	ledong "github.com/ledongthuc/pdf"
)

// extractPdfText 提取 PDF 全文文本。
// 策略：优先 pdftotext CLI（poppler-utils），CJK 支持完善；
//
//	CLI 不可用或提取为空时回退 ledongthuc/pdf 逐页读取。
func extractPdfText(path string, e *Extractor) error {
	if _, lookErr := exec.LookPath("pdftotext"); lookErr == nil {
		return extractPdfTextCLI(path, e)
	}
	return extractPdfTextLib(path, e)
}

// extractPdfTextCLI 通过 poppler-utils 的 pdftotext 命令行工具提取全文。
// CJK 字体编码支持远优于纯 Go 方案；-layout 保持原始排版便于段落切分。
// ★ P1-10 修复（2026-09-14）：exec.CommandContext + 120s 超时——旧实现裸
// exec.Command().Output() 无 ctx，恶意/异常 PDF 可挂死 goroutine，且绕过
// runSubprocess 的超时/进程组击杀资源闸。
func extractPdfTextCLI(path string, e *Extractor) error {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "pdftotext", "-layout", "-enc", "UTF-8", path, "-").Output()
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("pdftotext 执行超时（120s）: %s", filepath.Base(path))
	}
	if err != nil {
		return fmt.Errorf("pdftotext 执行失败: %w", err)
	}
	txt := string(out)
	if strings.TrimSpace(txt) == "" {
		return fmt.Errorf("pdftotext 未提取到文本")
	}
	for _, line := range strings.Split(txt, "\n") {
		line = strings.TrimSpace(strings.TrimRight(line, "\r"))
		if line != "" && line != "\f" {
			e.add(line)
		}
	}
	return nil
}

// extractPdfTextLib ledongthuc/pdf 回退方案（CLI 不可用时使用）。
//
// ★ 2026-10-01 〇-AF 补丁四：整个函数套 recover。
//
//	第三方解析器 `NewReaderEncrypted` 在"尾部 100 字节全是换行"这类畸形件上会
//	`index out of range [-1]` **panic**（剥空白的循环少了一对括号），
//	而这里是提取链的**回退腿**——客户上传一件畸形 PDF 就把提取协程打穿是不接受的，
//	崩溃一律收敛成 error，由上层按"这份件解析失败"处理（与加密件同一条路）。
//	同一份防护在页数读数 `PdfPageCount` 里也加了（那条更危险：它跑在上传请求的同步路径上）。
func extractPdfTextLib(path string, e *Extractor) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("PDF 解析崩溃（畸形件，第三方解析器会 panic）：%v", r)
		}
	}()
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	r, err := ledong.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		return fmt.Errorf("PDF 解析失败（可能已加密）: %w", err)
	}
	for i := 1; i <= r.NumPage(); i++ {
		p := r.Page(i)
		if p.V.IsNull() {
			continue
		}
		txt, err := p.GetPlainText(nil)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(txt, "\n") {
			line = strings.TrimSpace(strings.TrimRight(line, "\r"))
			if line != "" {
				e.add(line)
			}
		}
	}
	return nil
}
