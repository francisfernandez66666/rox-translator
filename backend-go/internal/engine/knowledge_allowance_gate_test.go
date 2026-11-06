// ============ 本文件职责中文说明 ============
// 一期《确定的多语言能力》§6.1：知识层问答的 allowance 必须**按构造恒 0**。
// 反证＝把某处塞成 >0 必须红（证明锁有射程，不是空函数），由 M0148 变异体执行。
// ========================================
package engine

import (
	"os"
	"strings"
	"testing"
)

// TestKnowledgeQueryAllowanceIsZeroByConstruction 喂任意 hits/langs 必须 0。
func TestKnowledgeQueryAllowanceIsZeroByConstruction(t *testing.T) {
	langSets := [][]string{nil, {}, {"zh"}, {"zh", "en"}, strings.Split("a,b,c,d,e,f,g,h,i,j,k,l", ",")}
	for _, hits := range []int{-1, 0, 1, 7, 1000000} {
		for _, langs := range langSets {
			if got := KnowledgeQueryAllowanceTokens(hits, langs); got != 0 {
				t.Fatalf("知识层问答零模型调用 ⇒ allowance 必须恒 0（hits=%d langs=%v）, got %d", hits, langs, got)
			}
		}
	}
	// 静态反证：函数体里不许出现任何非零返回或按 hits/langs 计算 token 的形态。
	raw, err := os.ReadFile("knowledge_guard.go")
	if err != nil {
		t.Fatalf("读不到 knowledge_guard.go: %v", err)
	}
	_ = raw
	src, err := os.ReadFile("costmodel.go")
	if err != nil {
		t.Fatalf("读不到 costmodel.go: %v", err)
	}
	body := string(src)
	i := strings.Index(body, "func KnowledgeQueryAllowanceTokens")
	if i < 0 {
		t.Fatal("第 6 支 allowance 缺席")
	}
	seg := body[i:]
	if j := strings.Index(seg, "\n}\n"); j > 0 {
		seg = seg[:j]
	}
	if strings.Contains(seg, "ScriptTokens(") || strings.Contains(seg, "*") {
		t.Fatal("知识层 allowance 不许按提示词或系数算 token（该路径零模型调用）")
	}
}
