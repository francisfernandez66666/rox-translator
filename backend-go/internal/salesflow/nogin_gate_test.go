// ============ 本文件职责中文说明 ============
// 二期 S2 守卫级门禁 §3：迁入引擎包不许带 web 框架／ORM 依赖（gin / net/http / gorm）。
// 按 ImportedPackageDirs 全集枚举目录，目录尚不存在不算命中；扫描文件数下限自证防失明；
// 反证往临时包塞一行框架引用必须命中。禁词表清空判失明，不判绿。
// 同包另两道门禁（configbranch / importlog）复用本文件的 scanImportedSources。
// ========================================
package salesflow

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// stripLineComment 剥行尾 // 注释：历史说明性注释不算违规代码。
// 参数：line=单行源码；返回剥注释后的代码部分。
func stripLineComment(line string) string {
	if i := strings.Index(line, "//"); i >= 0 {
		return line[:i]
	}
	return line
}

// scanImportedSources 扫 root/internal/<迁入包> 下全部非测试 .go，逐行剥注释后找禁词。
// 参数：root=模块根（backend-go）；banned=禁词全集；返回命中清单、扫描文件数与错误。
func scanImportedSources(root string, banned []string) ([]string, int, error) {
	var hits []string
	scanned := 0
	for _, dir := range ImportedPackageDirs() {
		base := filepath.Join(root, "internal", dir)
		entries, err := os.ReadDir(base)
		if err != nil {
			continue // 目录还没迁入：不算命中，也不算失明（由调用方的下限自证兜底）
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
				continue
			}
			p := filepath.Join(base, e.Name())
			raw, err := os.ReadFile(p)
			if err != nil {
				return nil, scanned, err
			}
			scanned++
			for i, line := range strings.Split(string(raw), "\n") {
				code := stripLineComment(line)
				for _, lit := range banned {
					if lit != "" && strings.Contains(code, lit) {
						hits = append(hits, p+":"+strconv.Itoa(i+1))
						break
					}
				}
			}
		}
	}
	return hits, scanned, nil
}

// 守卫：迁入包生产源码零 web/ORM 依赖。
func TestEnginePackagesHaveNoWebFrameworkDependency(t *testing.T) {
	banned := BannedWebFrameworkTokens()
	if len(banned) != 3 {
		t.Fatalf("POLICY_EMPTY_WEB：web 框架禁词表必须是三项全集，实得 %d ⇒ 表被清空时本闸失明", len(banned))
	}
	hits, scanned, err := scanImportedSources(filepath.Join("..", ".."), banned)
	if err != nil {
		t.Fatalf("扫描迁入包失败: %v", err)
	}
	if scanned < 1 {
		t.Fatal("射程自证失败：一个生产 .go 都没扫到 ⇒ 目录枚举写漂，本闸结构性失明")
	}
	if len(hits) != 0 {
		t.Fatalf("★ 引擎包出现 web/ORM 框架依赖（迁入必须由 Scope 收 ctx+tid）: %v", hits)
	}
}

// 反证：扫描器必须真看得见一处违规。
func TestEngineNoGinScanHasTeeth(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "internal", "salesflow")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatalf("反证夹具建目录失败: %v", err)
	}
	bad := "package salesflow\n\nvar _ = gin.Context{}\n"
	if err := os.WriteFile(filepath.Join(base, "bad.go"), []byte(bad), 0o644); err != nil {
		t.Fatalf("反证夹具写入失败: %v", err)
	}
	hits, scanned, err := scanImportedSources(dir, BannedWebFrameworkTokens())
	if err != nil {
		t.Fatalf("反证扫描失败: %v", err)
	}
	if scanned != 1 {
		t.Fatalf("反证扫描集应恰好 1 个文件，实得 %d", scanned)
	}
	if len(hits) != 1 {
		t.Fatalf("web 依赖反证必须命中 1 处，实得 %d ⇒ 扫描器失明", len(hits))
	}
}
