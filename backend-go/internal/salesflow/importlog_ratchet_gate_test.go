// ============ 本文件职责中文说明 ============
// 二期 §3.1 日志口径三条断言（迁入前先立门禁，防止 189 处裸日志搬进来才顶红）：
// LG1 迁入包生产源码 log.Printf 命中 0（按包枚举＋扫描文件数下限自证）；
// LG2 反证塞一行裸日志必红；LG3 observability 棘轮基线仍是 internal 176 / cmd 69，
// 既不许被顶红也不许「顺手下调基线」。
// ★ 位置说明：放本包而非 internal/observability，避免基础包反向 import 业务包成环。
// ========================================
package salesflow

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// LG1：迁入包零裸日志（禁词为空判失明，不判绿）。
func TestImportedPackagesHaveNoRawLogs(t *testing.T) {
	tok := RawLogToken()
	if tok == "" {
		t.Fatal("POLICY_EMPTY_LOG：裸日志禁词为空 ⇒ LG1 结构性失明")
	}
	hits, scanned, err := scanImportedSources(filepath.Join("..", ".."), []string{tok})
	if err != nil {
		t.Fatalf("LG1 扫描失败: %v", err)
	}
	if scanned < 1 {
		t.Fatal("LG1 射程自证失败：一个生产 .go 都没扫到，本闸已失明")
	}
	if len(hits) != 0 {
		t.Fatalf("LG1：迁入包仍有裸日志（迁入即换 observability，不留 TODO、不分两批）: %v", hits)
	}
}

// LG2 反证：扫描器必须真看得见一处裸日志。
func TestImportLogScanHasTeeth(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "internal", "salesflow")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatalf("反证夹具建目录失败: %v", err)
	}
	bad := "package salesflow\n\nimport \"log\"\n\nfunc f() { log.Printf(\"x\") }\n"
	if err := os.WriteFile(filepath.Join(base, "bad.go"), []byte(bad), 0o644); err != nil {
		t.Fatalf("反证夹具写入失败: %v", err)
	}
	hits, scanned, err := scanImportedSources(dir, []string{RawLogToken()})
	if err != nil {
		t.Fatalf("反证扫描失败: %v", err)
	}
	if scanned != 1 {
		t.Fatalf("反证扫描集应恰好 1 个文件，实得 %d", scanned)
	}
	if len(hits) != 1 {
		t.Fatalf("LG2 反证必须命中 1 处，实得 %d ⇒ 扫描器失明", len(hits))
	}
}

// LG3：棘轮基线不许被顶红，也不许被悄悄下调。
func TestImportLogRatchetBaselineUnchanged(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "observability", "logratchet_test.go"))
	if err != nil {
		t.Fatalf("LG3 读棘轮本体失败: %v", err)
	}
	if !regexp.MustCompile(`"internal":\s*176`).Match(raw) ||
		!regexp.MustCompile(`"cmd":\s*69`).Match(raw) {
		t.Fatal("LG3：棘轮基线不再是 176／69；迁入不许上调基线，也不许顺手下调当优化")
	}
}
