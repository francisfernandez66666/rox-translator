// ============ 本文件职责中文说明 ============
// 二期 S2 守卫级门禁 §5.3：引擎包不许按行业分支（行业差异只许经 Config 注入）。
// 禁词按全集判、清空判失明；反证塞一处分支字面必须命中。
// ========================================
package salesflow

import (
	"os"
	"path/filepath"
	"testing"
)

// 守卫：迁入包生产源码零行业分支字面（含 SCRM 的品牌硬编码）。
func TestEnginePackagesHaveNoIndustryBranches(t *testing.T) {
	banned := BannedIndustryBranchTokens()
	if len(banned) != 4 {
		t.Fatalf("POLICY_EMPTY_INDUSTRY：行业分支禁词表必须是四项全集，实得 %d ⇒ 表被清空时本闸失明", len(banned))
	}
	hits, scanned, err := scanImportedSources(filepath.Join("..", ".."), banned)
	if err != nil {
		t.Fatalf("扫描迁入包失败: %v", err)
	}
	if scanned < 1 {
		t.Fatal("射程自证失败：一个生产 .go 都没扫到 ⇒ 目录枚举写漂，本闸结构性失明")
	}
	if len(hits) != 0 {
		t.Fatalf("★ 引擎包出现按行业分支的字面判据（差异必须走 Config 注入）: %v", hits)
	}
}

// 反证：扫描器必须真看得见一处行业分支。
func TestIndustryBranchScanHasTeeth(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "internal", "salesflow")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatalf("反证夹具建目录失败: %v", err)
	}
	bad := "package salesflow\n\nfunc pick() { switch industry { } }\n"
	if err := os.WriteFile(filepath.Join(base, "bad.go"), []byte(bad), 0o644); err != nil {
		t.Fatalf("反证夹具写入失败: %v", err)
	}
	hits, scanned, err := scanImportedSources(dir, BannedIndustryBranchTokens())
	if err != nil {
		t.Fatalf("反证扫描失败: %v", err)
	}
	if scanned != 1 {
		t.Fatalf("反证扫描集应恰好 1 个文件，实得 %d", scanned)
	}
	if len(hits) != 1 {
		t.Fatalf("行业分支反证必须命中 1 处，实得 %d ⇒ 扫描器失明", len(hits))
	}
}
