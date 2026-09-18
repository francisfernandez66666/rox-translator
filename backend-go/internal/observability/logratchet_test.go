// logratchet_test.go — 日志收敛「只减不增」棘轮闸门（★ P2-3 整改起步，2026-09-18）。
// 背景：AGENTS.md 二 要求后端统一走 internal/observability 的 slog（JSON + trace_id），
// 但存量 `log.Printf` 仍有 182 处（api 45 / store 35 / engine 26 为大头）。批量改签名收益低、
// 风险高，故采取棘轮策略：基线随迁移逐次下调，新增一处即 CI 红灯；低于基线时提示更新。
// 迁移优先级（修改方案 P2-3）：engine → fileproc → orchestrator → 其余按包随改动顺带清。
package observability

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// logPrintfBaseline 当前存量基线。调低方式：删掉一批 log.Printf 后把此数改为新实测值。
// 历史：2026-09-18 建立，初始 179（口径=非测试源码、非行注释内的 log.Printf( 出现行）。
const logPrintfBaseline = 179

// TestLogPrintfRatchet 扫描 internal/ 全部非测试源码统计 log.Printf 出现次数，超过基线即失败。
func TestLogPrintfRatchet(t *testing.T) {
	root := moduleRoot(t)
	count := 0
	byPkg := map[string]int{}
	err := filepath.Walk(filepath.Join(root, "internal"), func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			name := info.Name()
			if name == "vendor" || name == ".git" || name == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		pkg := strings.Split(rel, "/")[1] // internal/<pkg>/...
		for _, line := range strings.Split(string(b), "\n") {
			tline := strings.TrimSpace(line)
			// 行注释/文档字符串中的提及不算存量
			if strings.HasPrefix(tline, "//") {
				continue
			}
			if idx := strings.Index(tline, "log.Printf("); idx >= 0 {
				count++
				byPkg[pkg]++
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("扫描失败: %v", err)
	}
	if count > logPrintfBaseline {
		t.Errorf("log.Printf 存量上升：%d > 基线 %d。AGENTS.md 二要求新代码走 observability slog；如确需上调基线须在评审中说明", count, logPrintfBaseline)
	}
	if count < logPrintfBaseline {
		t.Logf("log.Printf 存量已降至 %d（基线 %d），请把 logPrintfBaseline 下调并继续迁移（大头：engine/fileproc/orchestrator）", count, logPrintfBaseline)
	}
}

// moduleRoot 从测试工作目录（internal/observability）上溯到 module 根 backend-go。
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("未找到 go.mod 所在模块根")
	return ""
}
