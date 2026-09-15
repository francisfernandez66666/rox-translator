// 改进2回归测试：文件/文本工单建单前余额预检
//   - estimateTicketTokens：按源字符数估算 token 消耗（纯函数）
//   - 边界：空字符、无语言、markup<=0、超大文本
package api

import "testing"

// TestEstimateTicketTokens 估算 token 消耗
func TestEstimateTicketTokens(t *testing.T) {
	cases := []struct {
		name      string
		chars     int64
		langCount int
		markup    float64
		wantMin   int64
		wantMax   int64
	}{
		{"中文10w字2语言markup1.5", 100000, 2, 1.5, 230000, 231500}, // 100000/1.3*2*1.5 ≈ 230769
		{"单字段落", 1, 1, 1.0, 0, 2},
		{"无语言不估算", 5000, 0, 1.5, 0, 0},
		{"无字符不估算", 0, 2, 1.5, 0, 0},
		{"markup为0按基础", 3000, 1, 0, 2000, 2500},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := estimateTicketTokens(c.chars, c.langCount, c.markup)
			if got < c.wantMin || got > c.wantMax {
				t.Fatalf("estimateTicketTokens(%d, %d, %v) = %d，期望介于 [%d, %d]",
					c.chars, c.langCount, c.markup, got, c.wantMin, c.wantMax)
			}
		})
	}
}

// TestEstimateTicketTokensMonotonic 值越大消耗越大（单调性 sanity）
func TestEstimateTicketTokensMonotonic(t *testing.T) {
	small := estimateTicketTokens(1000, 1, 1.5)
	larger := estimateTicketTokens(10000, 1, 1.5)
	if larger <= small {
		t.Fatalf("更大文本估算应更大，small=%d larger=%d", small, larger)
	}
	if many := estimateTicketTokens(1000, 3, 1.5); many <= small {
		t.Fatalf("更多语言估算应更大，single=%d multi=%d", small, many)
	}
}

// TestEstimateFileSourceChars 校验按文件类型分档的字节→字符估算（任务1，2026-09-15）。
// 核心回归点：二进制文档（PDF/Office）不得按“整包皆文本”高估（旧口径 size/3 会误拦 700KB PDF）。
func TestEstimateFileSourceChars(t *testing.T) {
	size := int64(700 * 1024)
	txt := estimateFileSourceChars("a.txt", size)
	pdf := estimateFileSourceChars("a.pdf", size)
	docx := estimateFileSourceChars("a.docx", size)
	unk := estimateFileSourceChars("a", size)
	if pdf >= txt {
		t.Fatalf("PDF(二进制)估算字符数应远小于同体积纯文本：pdf=%d txt=%d", pdf, txt)
	}
	if pdf > size {
		t.Fatalf("PDF 估算不应超过字节数：pdf=%d", pdf)
	}
	if docx >= txt || docx < pdf {
		t.Fatalf("docx 应介于纯文本与 pdf 之间：txt=%d docx=%d pdf=%d", txt, docx, pdf)
	}
	if unk <= 0 {
		t.Fatalf("未知扩展名应有折中估算，got=%d", unk)
	}
	if estimateFileSourceChars("x.pdf", 0) != 0 {
		t.Fatal("0 字节应返回 0")
	}
	// 关键：700KB PDF 折成 token 预估后应明显低于旧口径（/3）
	old := size / 3
	if pdf*2 > old { // 新口径至少比旧口径小一半以上
		t.Fatalf("PDF 估算未显著收敛：new=%d old(/3)=%d", pdf, old)
	}
}
