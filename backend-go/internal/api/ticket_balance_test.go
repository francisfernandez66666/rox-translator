// 改进2回归测试：文件/文本工单建单前余额预检
//   - estimateTicketTokens：★ F-41（2026-09-25 批 D）改为按模式系数 K 估算（纯函数）
//   - 边界：空字符、无语言、非法 K 回退、超大文本
//   - 基准锁：工单 88/89 生产实测数据（est 必须覆盖实烧/外推需求，宁高勿低）
package api

import "testing"

// TestEstimateTicketTokens F-41 新口径：est = chars × langs × K(mode)。
// K 由参数注入（配置读取另有 estTokensPerChar 专项测试），本测试锁公式本身。
func TestEstimateTicketTokens(t *testing.T) {
	cases := []struct {
		name      string
		chars     int64
		langCount int
		mode      string
		kPro      float64
		kFast     float64
		want      int64 // 等值锁（F-41 起公式无方言/浮点歧义，直接钉死）
	}{
		{"pro3语5k字", 5000, 3, "pro", 160, 60, 2400000},
		{"fast单语1k字", 1000, 1, "fast", 160, 60, 60000},
		{"mode空按pro默认", 100, 1, "", 160, 60, 16000},
		{"无语言不估算", 5000, 0, "pro", 160, 60, 0},
		{"无字符不估算", 0, 2, "pro", 160, 60, 0},
		{"K非法回退保守默认160", 100, 1, "pro", 0, -5, 16000},
		{"fast非法K也回退160", 100, 1, "fast", 160, 0, 16000},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := estimateTicketTokens(c.chars, c.langCount, c.mode, c.kPro, c.kFast)
			if got != c.want {
				t.Fatalf("estimateTicketTokens(%d, %d, %q, %v, %v) = %d，期望 %d",
					c.chars, c.langCount, c.mode, c.kPro, c.kFast, got, c.want)
			}
		})
	}
}

// TestEstimateTicketTokensMonotonic 更大文本/更多语言估算更大（单调性 sanity）。
func TestEstimateTicketTokensMonotonic(t *testing.T) {
	small := estimateTicketTokens(1000, 1, "pro", 160, 60)
	larger := estimateTicketTokens(10000, 1, "pro", 160, 60)
	if larger <= small {
		t.Fatalf("更大文本估算应更大，small=%d larger=%d", small, larger)
	}
	if many := estimateTicketTokens(1000, 3, "pro", 160, 60); many <= small {
		t.Fatalf("更多语言估算应更大，single=%d multi=%d", small, many)
	}
	if fast := estimateTicketTokens(1000, 1, "fast", 160, 60); fast >= small {
		t.Fatalf("fast 估算应低于 pro，fast=%d pro=%d", fast, small)
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
