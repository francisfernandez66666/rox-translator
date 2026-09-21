// ============ gofmt_gate_test.go · 职责说明 ============
// gofmt 零格式门禁（★ 2026-09-22 缺口批 #55，报告《全量UAT实测与架构商业评价_20260921》§1-3、§6 P0-2）。
//
// 背景：全仓检索曾证实 gofmt **从未被任何闸门约束**（ci.yml / AGENTS.md / scripts 均零命中），
// 结果 HEAD 上就有 7 个文件格式不合格、工作区最多 9 个，且因本分支不进 CI 长期无人察觉。
// 本测试把「整仓 .go 必须 gofmt 合格」变成 `go test ./...` 的硬断言：
//   - 遍历 backend-go 全部 .go（排除 vendor / .git / node_modules / testdata / `Code generated` 生成码）；
//   - 用 go/format.Source 重排后与原文逐字节比较，不一致即红灯并列出**明确文件清单**；
//   - 允许临时豁免，但豁免必须写进同目录 `gofmt_allowlist.txt`（可维护、可审计、禁止整仓跳过），
//     且**已合格的残留条目同样红灯**——防止 allowlist 变成第二个无人清理的死角。
//
// 为什么豁免存在：并行开发期同事正在大改 internal/engine、internal/fileproc，
// 其未提交中间态可能短暂不合格；与其要求全仓排队格式化，不如把豁免显式化并强制其收敛。
// =============================================
package internal

import (
	"go/format"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// gofmtAllowlistFile 豁免清单文件名（与本测试同目录，逐行一个相对 backend-go 的路径）。
const gofmtAllowlistFile = "gofmt_allowlist.txt"

// TestGofmtGateZeroViolations 断言整仓 .go 文件 gofmt 合格（豁免条目除外）。
func TestGofmtGateZeroViolations(t *testing.T) {
	root := moduleRootForGofmt(t)
	allow, err := loadGofmtAllowlist(filepath.Join(root, "internal", gofmtAllowlistFile))
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("读取 %s 失败: %v", gofmtAllowlistFile, err)
	}

	var offenders []string           // 不合格且未在豁免内的文件（红灯清单）
	unformatted := map[string]bool{} // 不合格文件全集（含被豁免的，用于校验豁免条目是否已失效）
	parseErrs := []string{}          // 连 format.Source 都跑不动的文件（语法错误，必须单独报出）
	err = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // 个别目录无权限/竞态删除不阻断整仓扫描，漏扫由「豁免条目失效」断言兜底
		}
		if info.IsDir() {
			switch info.Name() {
			case "vendor", ".git", "node_modules", "testdata", "dist", "build":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if isGeneratedGo(path) {
			return nil // 生成代码（go:generate / protoc 等）由生成器负责格式，不做人工门禁
		}
		src, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		out, ferr := format.Source(src)
		if ferr != nil {
			parseErrs = append(parseErrs, rel+": "+ferr.Error())
			return nil
		}
		if string(out) != string(src) {
			unformatted[rel] = true
			if !allow.match(rel) {
				offenders = append(offenders, rel)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("扫描失败: %v", err)
	}
	sort.Strings(offenders)
	if len(offenders) > 0 {
		t.Errorf("gofmt 门禁：以下文件未格式化（`cd backend-go && gofmt -w %s`）：\n  %s",
			strings.Join(offenders, " "), strings.Join(offenders, "\n  "))
	}
	if len(parseErrs) > 0 {
		sort.Strings(parseErrs)
		t.Errorf("gofmt 门禁：以下文件无法解析（格式修复不了，需先修语法）：\n  %s", strings.Join(parseErrs, "\n  "))
	}
	// 豁免条目必须仍然「确有必要」：文件已合格却仍留在清单里，说明清单在腐化。
	for _, e := range allow {
		if e.dir { // 目录级豁免：该目录下仍有不合格文件才算有效
			found := false
			for f := range unformatted {
				if strings.HasPrefix(f, e.path+"/") {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("gofmt 豁免条目 %s/** 已无对应不合格文件，请从 %s 删除（防止清单腐化）", e.path, gofmtAllowlistFile)
			}
			continue
		}
		if !unformatted[e.path] {
			t.Errorf("gofmt 豁免条目 %s 已合格，请从 %s 删除（防止清单腐化）", e.path, gofmtAllowlistFile)
		}
	}
}

// gofmtExempt 一条豁免：path 为相对 backend-go 的路径；dir=true 表示 `xxx/**` 整目录豁免。
type gofmtExempt struct {
	path string
	dir  bool
}

// gofmtExempts 豁免集合与匹配逻辑。
type gofmtExempts []gofmtExempt

// match 判断文件是否被任一豁免条目覆盖。
func (ex gofmtExempts) match(rel string) bool {
	for _, e := range ex {
		if e.dir {
			if strings.HasPrefix(rel, e.path+"/") {
				return true
			}
			continue
		}
		if e.path == rel {
			return true
		}
	}
	return false
}

// loadGofmtAllowlist 解析豁免清单：`#` 开头为注释，空行忽略，`xxx/**` 为目录级豁免。
func loadGofmtAllowlist(path string) (gofmtExempts, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out gofmtExempts
	for _, ln := range strings.Split(string(b), "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" || strings.HasPrefix(ln, "#") {
			continue
		}
		if strings.HasSuffix(ln, "/**") {
			out = append(out, gofmtExempt{path: strings.TrimSuffix(ln, "/**"), dir: true})
			continue
		}
		out = append(out, gofmtExempt{path: ln})
	}
	return out, nil
}

// isGeneratedGo 判断是否 Go 生成代码（标准标记：文件前部 `// Code generated ... DO NOT EDIT.`）。
// 生成器产物的格式问题应由生成器/模板修，人工 gofmt 会被下次生成覆盖，故排除门禁。
func isGeneratedGo(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	head := make([]byte, 4096)
	n, _ := f.Read(head)
	for _, ln := range strings.Split(string(head[:n]), "\n") {
		if strings.HasPrefix(strings.TrimSpace(ln), "//") &&
			strings.Contains(ln, "Code generated") && strings.Contains(ln, "DO NOT EDIT") {
			return true
		}
		// 生成标记按约定出现在文件极靠前处；遇到 package 声明后即可停止扫描
		if strings.HasPrefix(strings.TrimSpace(ln), "package ") {
			return false
		}
	}
	return false
}

// moduleRootForGofmt 从测试工作目录（backend-go/internal）上溯到模块根 backend-go（以 go.mod 为锚）。
func moduleRootForGofmt(t *testing.T) string {
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
