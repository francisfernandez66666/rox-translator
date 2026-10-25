// ============ 本文件职责中文说明 ============
// user_scan_columns_gate_test.go · 锁「users 查询列清单」与「Scan 目标清单」的等值（★ 2026-10-24 多身份 B1 立）。
//
// 这一族的事故形态从来不是「某列取错值」，而是**两份清单各自长**：
// 2026-08-26 全仓评审 C1 抓到 ListUsersByRole 的 SELECT 13 列 / Scan 14 目标，
// 每行 Scan 必败、被 continue 吞掉 ⇒ 函数恒返回空列表、超管通知链路整体静默失效。
// 行式 Scan 曾有 5 处各抄一遍清单（store.go 4 处 + account.go 1 处），
// B1 把它们全部收敛到 (*User).assign 一处；本文件钉住收敛后的两条不变式：
//   U1 assign 的 Scan 目标数 == userCols 现算列数（加列漏改目标 ⇒ 当场红）；
//   U2 全仓 users 行式查询只许出现 userCols 这一个列清单（再手写一份 ⇒ 当场红）。
// ========================================

package iam

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// countScanTargets 现读 models.go 里 (*User).assign 函数体，数 row.Scan(...) 的实参个数。
// 用 AST 而不是正则：本仓注释里满是 `&u.` 与 "Scan" 字样（AGENTS「负向锁误伤说明注释」同族）。
func countScanTargets(t *testing.T) int {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "models.go", nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("解析 models.go 失败: %v", err)
	}
	n := -1
	ast.Inspect(f, func(node ast.Node) bool {
		fd, ok := node.(*ast.FuncDecl)
		if !ok || fd.Name.Name != "assign" || fd.Body == nil {
			return true
		}
		ast.Inspect(fd.Body, func(inner ast.Node) bool {
			call, ok := inner.(*ast.CallExpr)
			if !ok {
				return true
			}
			se, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || se.Sel.Name != "Scan" {
				return true
			}
			n = len(call.Args)
			return false
		})
		return false
	})
	if n < 0 {
		t.Fatal("models.go 里找不到 (*User).assign 的 Scan 调用 ⇒ 映射口被改写，本闸失去射程")
	}
	return n
}

// TestUserScanTargetsMatchUserCols U1：列数与 Scan 目标数等值（逐列点名由 assign 本身保证顺序）。
func TestUserScanTargetsMatchUserCols(t *testing.T) {
	cols := len(userColsColumnNames())
	targets := countScanTargets(t)
	if cols != targets {
		t.Fatalf("userCols 现算 %d 列而 User.assign 只有 %d 个 Scan 目标 ⇒ 加列没同步映射（漏一侧即静默吞行，2026-08-26 C1 同族）", cols, targets)
	}
}

// TestNoHandCopiedUserColumnList U2：除 userCols 定义本身，非测试源文件里不许出现列清单字面量。
// 判据按「清单的第一个裸列 + 第二个裸列」这一独特形状取，避免误伤 SQL 语句里的两列小查询。
func TestNoHandCopiedUserColumnList(t *testing.T) {
	anchor := "id, tenant_id, username, password_hash"
	files, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("读 iam 包目录失败: %v", err)
	}
	scanned := 0
	for _, e := range files {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		scanned++
		src, err := os.ReadFile(e.Name())
		if err != nil {
			t.Fatalf("读 %s 失败: %v", e.Name(), err)
		}
		for _, line := range strings.Split(string(src), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") {
				continue // 说明注释里的抄本不算违规，也不算命中
			}
			if strings.Contains(trimmed, anchor) && !strings.HasPrefix(trimmed, `const userCols`) {
				t.Fatalf("%s 里出现手抄的 users 列清单（应引用 userCols）：%q ⇒ 第二把尺子复活", e.Name(), trimmed)
			}
		}
	}
	if scanned == 0 {
		t.Fatal("本闸扫到 0 个源文件 ⇒ 射程为空，判据在假绿")
	}
}
