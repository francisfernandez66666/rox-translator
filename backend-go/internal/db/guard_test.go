// guard_test.go — 方言纪律静态守卫（★ P1-5 整改断言，2026-09-18）。
// 背景：internal/engine 等处曾直接 `xxx.DB().Query(...)` / `RawDB().Exec(..., ?)` 裸用
// *sql.DB——SQLite 下正常、PG（lib/pq，$N 占位符）下必错且常被吞掉，造成文化闸等整链
// 静默失效（本次审计 P1-5）。AGENTS.md 一-4 要求所有 SQL 同时兼容两种方言，唯一安全入口
// 是本包的 db.Exec/db.Query/db.QueryRow 包装。本测试全仓扫描源码，发现新的裸调用即红灯。
package db

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// rawSQLPatterns 裸 *sql.DB 调用形态（允许出现的白名单见 isExempt）：
//   - `.DB().Query(` / `.DB().QueryRow(` / `.DB().QueryRowContext(` / `.DB().Exec(`
//   - `RawDB().` 同上任意方法
var rawSQLPatterns = regexp.MustCompile(`(\.DB\(\)|RawDB\(\))\s*\.\s*(Query|QueryRow|QueryRowContext|QueryContext|Exec|ExecContext|Prepare)\(`)

// TestNoRawSQLOutsideDialectWrapper 扫描仓库 Go 源码，除 db 包自身与明确豁免外，
// 禁止绕过方言包装的裸连接调用。
func TestNoRawSQLOutsideDialectWrapper(t *testing.T) {
	root := findRepoGoRoot(t)
	var hits []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // 个别目录无权限直接跳过，不影响其余扫描
		}
		if info.IsDir() {
			name := info.Name()
			if name == "vendor" || name == ".git" || name == "node_modules" || name == "assist" {
				return filepath.SkipDir // assist 子服务独立 SQLite 库，不适用双方言纪律
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		if rel == "internal/db/query.go" || rel == "internal/db/db.go" {
			return nil // 包装层自身合法
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		for i, line := range strings.Split(string(b), "\n") {
			if rawSQLPatterns.MatchString(line) {
				hits = append(hits, fmt.Sprintf("%s:%d: %s", rel, i+1, strings.TrimSpace(line)))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("扫描失败: %v", err)
	}
	if len(hits) > 0 {
		t.Errorf("发现 %d 处绕过方言包装的裸 SQL 调用（PG 下会静默失效，改走 db.Exec/db.Query/db.QueryRow）:\n%s",
			len(hits), strings.Join(hits, "\n"))
	}
}

// findRepoGoRoot 从测试工作目录（internal/db）上溯到 module 根 backend-go。
func findRepoGoRoot(t *testing.T) string {
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
	t.Fatal("未找到 go.mod 所在仓库根")
	return ""
}

// shadowMigrationPatterns 影子迁移框架形态（★ P2-2 防回归断言，2026-09-18）：
// db/migrate.go 的 Runner + schema_migrations 版本表曾是生产零调用的第二套迁移机制，
// 与 store.go 幂等补列（db.EnsureColumns）双轨并存、误导贡献者，已整体删除。
// 本守卫扫描全仓非测试源码，发现复活迹象即红灯——迁移唯一入口是 EnsureColumns/ExecDDL（AGENTS.md 一-4）。
var shadowMigrationPatterns = regexp.MustCompile(`func\s+NewRunner\(|schema_migrations|RegisteredMigrations\(`)

// TestNoShadowMigrationRunner 防止已删除的 Runner 影子迁移框架被重新引入。
func TestNoShadowMigrationRunner(t *testing.T) {
	root := findRepoGoRoot(t)
	var hits []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			name := info.Name()
			if name == "vendor" || name == ".git" || name == "node_modules" || name == "assist" {
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
		for i, line := range strings.Split(string(b), "\n") {
			// 行注释不算复活迹象（如本包 rewrite.go 的历史说明注释）
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			if shadowMigrationPatterns.MatchString(line) {
				hits = append(hits, fmt.Sprintf("%s:%d: %s", rel, i+1, strings.TrimSpace(line)))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("扫描失败: %v", err)
	}
	if len(hits) > 0 {
		t.Errorf("发现 %d 处影子迁移框架（Runner/schema_migrations）复活迹象，P2-2 已删除该机制，新列一律走 db.EnsureColumns:\n%s",
			len(hits), strings.Join(hits, "\n"))
	}
}
