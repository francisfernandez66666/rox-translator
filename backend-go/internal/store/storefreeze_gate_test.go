// ============ 本文件职责中文说明 ============
// 把 AGENTS §一·1 的 store 冻结规则变成机械断言：billing.go / kbpackages.go 只减不增。
// ★ 口径（一期审阅修正）：硬判据只锁「导出方法数」，不锁行数——billing.go 从 2379 涨到 2448
//
//	全部来自原地改既有方法体与写约束注释，那是规则明确允许的；按行数判红会误伤合法改动。
//	行数上升只出提示并点名新增的 func，越界与否由方法数判据裁决。
//
// ========================================
package store

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// storeFreezeMethodBaselines 导出方法数基线（2026-11-05 现读：billing 51 / kbpackages 46）。
var storeFreezeMethodBaselines = map[string]int{
	"billing.go":    51,
	"kbpackages.go": 46,
}

// storeFreezeRequiredFiles 按批次递增的窄域文件清单：第一批 product.go，
// 第二批补齐事实项／物料／产品包／齐备度／迁移五个窄域文件。
// ★ 清单本身不许变空，否则「文件都存在」恒真＝空转锁；后续每批补齐时同步追加。
// ★ 改这一行必须同步改 scripts/gate_mutants.tsv 的 M0144 的 find 锚点（逐字等值纪律）。
var storeFreezeRequiredFiles = []string{"product.go", "product_fact.go", "product_asset.go", "kbpackages_product.go", "product_readiness.go", "product_migrate.go"}

var storeFreezeExportedMethodRe = regexp.MustCompile(`^func \([^)]+\) [A-Z]`)

// S2：导出方法数只减不增（唯一硬判据）。
func TestStoreFreezeExportedMethodCount(t *testing.T) {
	for name, base := range storeFreezeMethodBaselines {
		f, err := os.Open(name)
		if err != nil {
			t.Fatalf("冻结对象 %s 读不到（扫描集搬家会让本闸失明）: %v", name, err)
		}
		n := 0
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			if storeFreezeExportedMethodRe.MatchString(sc.Text()) {
				n++
			}
		}
		_ = f.Close()
		if err := sc.Err(); err != nil {
			t.Fatalf("扫描 %s 失败: %v", name, err)
		}
		if n > base {
			t.Errorf("%s 导出方法数 %d 超基线 %d：新方法必须按域新建文件", name, n, base)
		}
	}
}

// S3：窄域新文件必须存在（防「把方法塞回冻结文件」）。
func TestStoreFreezeDomainFilesExist(t *testing.T) {
	if len(storeFreezeRequiredFiles) == 0 {
		t.Fatal("必建文件清单为空 ⇒ 本闸恒绿")
	}
	for _, name := range storeFreezeRequiredFiles {
		if _, err := os.Stat(name); err != nil {
			t.Errorf("一期窄域文件 %s 不存在: %v", name, err)
		}
	}
}

// S4：射程自证——扫描集不能小到让闸失明。
func TestStoreFreezeScanReach(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("枚举 store 目录失败: %v", err)
	}
	n := 0
	for _, f := range files {
		if !strings.HasSuffix(f, "_test.go") {
			n++
		}
	}
	if n < 50 {
		t.Fatalf("store 非测试源文件仅 %d 个，扫描目录疑似搬家：本闸会结构性失明", n)
	}
}
