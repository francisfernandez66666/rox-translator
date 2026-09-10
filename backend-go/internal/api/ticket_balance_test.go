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
