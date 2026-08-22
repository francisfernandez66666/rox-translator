// ============ 本文件职责中文说明 ============
// PDF 文本提取：基于 ledongthuc/pdf（纯 Go，无 CGO）逐页读取文本。
// PDF 无原格式回写能力，翻译产物由引擎层统一降级为 xlsx 对照表（见 engine/file.go 第3步）。
// 加密/扫描件（图片型）无法提取文本时返回可读错误。
// =============================================
package fileproc

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	ledong "github.com/ledongthuc/pdf"
)

// extractPdfText 提取 PDF 全文文本：逐页取纯文本，按行入提取器。
// 参数：path=PDF 文件路径，e=提取器；返回错误（加密/空文档等）。
func extractPdfText(path string, e *Extractor) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	// NewParser 要求未加密的原始字节；ReadAll 拉平全部页面文本
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
			continue // 单页失败跳过（如该页为纯图）
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
