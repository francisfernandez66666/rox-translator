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

// logPrintfBaselines 各扫描根的存量基线。调低方式：删掉一批 log.Printf 后把对应数改为新实测值。
// 历史：2026-09-18 建立，初始 179（口径=非测试源码、非行注释内的 log.Printf( 出现行）。
// 2026-09-21 → 178：删除 api/pay.go handlePayCreate 里一段不可达的重复错误分支（原带 1 处），
// 同日新增的订阅自动续费（#41）与微信/支付宝协议层全部走 observability slog，未抬高存量。
// 2026-09-21 → 176：#41 评审收尾顺带迁移 api/plans_api.go 两处（订阅下单失败、免费包发放失败）
// 到 observability.Error，走 JSON+trace_id 口径。
//
// ★ #55 缺口批（2026-09-22，报告 §4.1-9 / §6 P0-5）：**cmd/ 此前是棘轮盲区**。
//
//	原实现 filepath.Walk 硬编码只走 internal/，实测真实生产存量 = internal 176 + cmd 69 = 245，
//	闸门只守住 176/245 ≈ 72%——往 cmd/ 里加 log.Printf 不会有任何红灯。
//	现把 cmd/ 纳入扫描并**以当前实测值 69 立为基线**（只减不增）。
//	注意：cmd/server/main.go 独占 40 处且多为启动期 fail-fast 校验与停机日志
//	（observability 需要 context/trace，启动失败路径拿不到请求上下文），
//	迁移须逐条评估、不得为迁日志改动 fail-fast 语义；大头 cmd/auto-approve(10)、
//	cmd/dbtimecheck(6)、cmd/migrate-sqlite-to-pg(5)、cmd/rebuild-kb-index(5) 属工具型，可随改动顺带清。
var logPrintfBaselines = map[string]int{
	"internal": 176,
	"cmd":      69, // 2026-09-22 纳入盲区时实测值：server 40 / auto-approve 10 / dbtimecheck 6 / migrate-sqlite-to-pg 5 / rebuild-kb-index 5 / backfill-embeddings 3
}

// TestLogPrintfRatchet 扫描 internal/ 与 cmd/ 全部非测试源码统计 log.Printf 出现次数，超过基线即失败。
// 分根设基线（不按总数）：防止「cmd/ 新增被 internal/ 迁移的下降量掩盖」——两处的账要分开算。
func TestLogPrintfRatchet(t *testing.T) {
	root := moduleRoot(t)
	for _, scanRoot := range []string{"internal", "cmd"} {
		baseline, ok := logPrintfBaselines[scanRoot]
		if !ok {
			t.Fatalf("扫描根 %s 缺少基线配置（新增扫描目录必须同时立基线）", scanRoot)
		}
		count, byPkg := countLogPrintf(t, root, scanRoot)
		if count > baseline {
			t.Errorf("%s/ 下 log.Printf 存量上升：%d > 基线 %d（明细 %v）。AGENTS.md 二要求新代码走 observability slog；如确需上调基线须在评审中说明",
				scanRoot, count, baseline, byPkg)
		}
		if count < baseline {
			t.Logf("%s/ log.Printf 存量已降至 %d（基线 %d），请把 logPrintfBaselines[%q] 下调并继续迁移（大头：engine/fileproc/orchestrator、cmd/server）",
				scanRoot, count, baseline, scanRoot)
		}
	}
}

// countLogPrintf 统计 scanRoot（相对模块根的子目录）下非测试源码的 log.Printf 行数，
// 返回总数与按包分布（包名取根下第一层目录，便于红灯时直接看到大头）。
func countLogPrintf(t *testing.T, root, scanRoot string) (int, map[string]int) {
	t.Helper()
	count := 0
	byPkg := map[string]int{}
	err := filepath.Walk(filepath.Join(root, scanRoot), func(path string, info os.FileInfo, err error) error {
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
		parts := strings.Split(filepath.ToSlash(rel), "/")
		pkg := rel
		if len(parts) > 2 { // internal/<pkg>/... 或 cmd/<tool>/...
			pkg = parts[1]
		}
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
	return count, byPkg
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
