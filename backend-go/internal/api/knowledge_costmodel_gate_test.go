// ============ 本文件职责中文说明 ============
// 一期《确定的多语言能力》§9.1 C1/C2：预估必须委托 engine 的第 6 支 allowance
// （不许 handler 自算），且该支恒 0 ⇒ 预估读数与六键提交面一行不动。
// 源码级判据（不写死行号，防注释增删导致锚点漂移）。
// ========================================
package api

import (
	"os"
	"strings"
	"testing"
)

// C1：estimate.go 必须调用 engine.KnowledgeQueryAllowanceTokens，且只在「各支相加」那一处。
func TestKnowledgeEstimateDelegatesToEngineAllowance(t *testing.T) {
	raw, err := os.ReadFile("estimate.go")
	if err != nil {
		t.Fatalf("读不到 estimate.go: %v", err)
	}
	src := string(raw)
	const call = "engine.KnowledgeQueryAllowanceTokens(0, req.TargetLangs)"
	if n := strings.Count(src, call); n != 1 {
		t.Fatalf("预估侧必须且只能调一次第 6 支 allowance（单口形态）, got %d 处", n)
	}
	// 不许在 handler 里另算（token/400、就地折算那一族）
	if strings.Contains(src, "KnowledgeQuery") && strings.Contains(src, "tokens/400") {
		t.Fatal("预估侧不许出现就地折算")
	}
	// pricing_meta.go 出参必须明出 0（不带 omitempty）
	meta, err := os.ReadFile("pricing_meta.go")
	if err != nil {
		t.Fatalf("读不到 pricing_meta.go: %v", err)
	}
	if !strings.Contains(string(meta), `"points_knowledge_query": 0`) {
		t.Fatal("对外算价面必须明出 points_knowledge_query: 0（出栈缺席会被读成字段没实现）")
	}
}
