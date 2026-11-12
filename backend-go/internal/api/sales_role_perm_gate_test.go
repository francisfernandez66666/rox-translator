// ============ sales_role_perm_gate_test.go · 职责说明 ============
// 三期 W3 / A09b：锁 store.PermForEndpoint() 的**单一真值**。
// ① 权限项枚举 10 个逐项有映射（已登记 + 超管档有放行 + 至少一个端点映射到它），mapped<10 Fatal；
// ② 源码腿：销售域 handler 源码不得硬写第二份权限清单 / 角色清单（M0222 反证）；
// ③ 删掉一个权限项映射的内存副本必红（M0223 反证），防「一律判绿」。
// 与 A10 是「源码 + 行为」两条腿：本文件管真值不被复制，A10 管四端点 × 四档状态码全表。
// ================================================
package api

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"translator/internal/store"
)

// a09bStripGoComments 去整行注释与块注释（判据只落在代码字面，不落在说明文字）。
func a09bStripGoComments(src string) string {
	src = regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(src, "")
	return regexp.MustCompile(`(?m)^\s*//[^\n]*$`).ReplaceAllString(src, "")
}

// a09bDuplicateHits 扫某目录下销售域源码是否硬写第二份权限项 / 角色清单。
// 返回违规清单与扫描文件数；scanned==0 由调用方判射程失明。
func a09bDuplicateHits(dir string) ([]string, int, error) {
	files, err := filepath.Glob(filepath.Join(dir, "sales_*.go"))
	if err != nil {
		return nil, 0, err
	}
	permLit := regexp.MustCompile(`"(?:bot_toggle|kb_edit|script_edit|lead_view|lead_route|quote_issue|quote_approve|outbound_config|consent_manage|ops_view)"`)
	roleList := regexp.MustCompile(`(?s)\[\]string\{[^}]*"dept_admin"[^}]*"tenant_admin"[^}]*\}`)
	var hits []string
	scanned := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		raw, e := os.ReadFile(f)
		if e != nil {
			return nil, 0, e
		}
		code := a09bStripGoComments(string(raw))
		scanned++
		if permLit.MatchString(code) {
			hits = append(hits, f+": 硬写权限项字面")
		}
		if roleList.MatchString(code) {
			hits = append(hits, f+": 硬写角色清单")
		}
	}
	return hits, scanned, nil
}

// a09bPermMapped 从路径子集现推「哪些权限项有端点落点」（内存副本，供 M0223 反证）。
func a09bPermMapped(paths []string) map[string]bool {
	out := map[string]bool{}
	for _, p := range paths {
		out[store.PermForEndpoint(p)] = true
	}
	return out
}

// a09bAllMapped 判定权限项全集是否逐项都有端点落点。
func a09bAllMapped(keys []string, mapped map[string]bool) bool {
	for _, k := range keys {
		if !mapped[k] {
			return false
		}
	}
	return true
}

// TestPermForEndpointSingleTruth A09b 正向：逐项有映射 + 源码无第二份清单。
func TestPermForEndpointSingleTruth(t *testing.T) {
	keys := store.AllSalesPermKeys()
	if len(keys) < 10 {
		t.Fatalf("A09b 射程自证失败：权限项枚举 mapped<10，实得 %d", len(keys))
	}
	mapped := a09bPermMapped(store.SalesEndpointPaths())
	for _, k := range keys {
		if !store.SalesPermExists(k) {
			t.Fatalf("A09b：权限项 %s 未登记", k)
		}
		if !store.DefaultSalesRoleAllowed(4, k) {
			t.Fatalf("A09b：权限项 %s 超管档无放行映射", k)
		}
		if store.DefaultSalesRoleAllowed(1, k) {
			t.Fatalf("A09b：权限项 %s 普通用户档不该有放行映射", k)
		}
		if !mapped[k] {
			t.Fatalf("A09b：权限项 %s 没有任何端点映射（M0223 靶）", k)
		}
	}
	hits, scanned, err := a09bDuplicateHits(".")
	if err != nil {
		t.Fatalf("A09b 源码扫描失败: %v", err)
	}
	if scanned == 0 {
		t.Fatal("A09b 射程自证失败：一个销售域源码都没扫到")
	}
	if len(hits) != 0 {
		t.Fatalf("A09b：销售域 handler 出现第二份权限/角色清单: %v", hits)
	}
}

// TestPermForEndpointGateHasTeeth A09b 反证：M0222 硬写清单必红、M0223 删映射必红。
func TestPermForEndpointGateHasTeeth(t *testing.T) {
	// M0222：造一份硬写权限项 + 角色清单的源码，扫描器必须命中（不许恒绿）。
	dir := t.TempDir()
	bad := "package api\nfunc h(){\n\t_ = []string{\"bot_toggle\",\"kb_edit\",\"ops_view\"}\n\t_ = []string{\"user\",\"dept_admin\",\"tenant_admin\",\"super_admin\"}\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "sales_bad.go"), []byte(bad), 0o644); err != nil {
		t.Fatalf("M0222 反证夹具写入失败: %v", err)
	}
	hits, scanned, err := a09bDuplicateHits(dir)
	if err != nil {
		t.Fatalf("M0222 反证扫描失败: %v", err)
	}
	if scanned != 1 || len(hits) == 0 {
		t.Fatalf("M0222 反证：硬写清单必须命中，实得 scanned=%d hits=%v", scanned, hits)
	}

	// M0223：删掉覆盖某一权限项的全部端点（内存副本），逐项验映射必须判红。
	paths := store.SalesEndpointPaths()
	if !a09bAllMapped(store.AllSalesPermKeys(), a09bPermMapped(paths)) {
		t.Fatal("A09b 正向对照：全集端点应逐项全映射")
	}
	var reduced []string
	for _, p := range paths {
		if store.PermForEndpoint(p) != store.PermLeadRoute {
			reduced = append(reduced, p)
		}
	}
	if a09bAllMapped(store.AllSalesPermKeys(), a09bPermMapped(reduced)) {
		t.Fatal("M0223 反证：删掉一个权限项映射后必须判红")
	}
}
