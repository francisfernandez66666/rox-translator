// ============ tasks_test.go · 职责说明 ============
// G18（2026-10-19）「任务奖励领取不许吞错」的两条闸。
//
// 缺陷形态：ClaimUserTask 里「读租户」一句写成 `_ = ...Scan(...)`，查询报错时 tenantID
// 保持 0 ⇒ 奖励发进平台桶（孤儿），而 handler 照样回「领取成功」。行为锁（下方租户用例）
// 用「先 DROP users 表、再领取」把它钉死：必须 fail-closed、余额零变动。
//
// 去重那句（COUNT user_task_claims）在库表完好时无法与「后续 INSERT 也失败」分开观测，
// 所以不写行为用例（那样会是恒绿的假锁），改由静态闸钉住：ClaimUserTask 里任何
// `...Scan(...)` 的返回值都不许被丢进 `_`。反证＝合成一段吞错源码必须判红。
//
// 方言固定 SQLite 内存库（taskEnv 已钉 config.C，见 AGENTS.md §4）。
// =============================================
package store

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"testing"

	"translator/internal/db"
)

// manualClaimTaskID 把某出厂任务改成人工领取，并回它的 id。
func manualClaimTaskID(t *testing.T, st *Store, key string, reward int64) int64 {
	t.Helper()
	grantTaskForTest(t, st, key, "grant_mode='manual', reward_tokens="+itoa(reward))
	var id int64
	if err := db.QueryRow(st.db, db.CurrentDialect(),
		"SELECT id FROM user_tasks WHERE task_key=?", key).Scan(&id); err != nil {
		t.Fatalf("读取任务 id 失败: %v", err)
	}
	return id
}

// TestClaimUserTaskFailsClosedWhenTenantQueryFails 租户读不到 ⇒ 拒绝领取（不得退成 tenant_id=0 孤儿）。
func TestClaimUserTaskFailsClosedWhenTenantQueryFails(t *testing.T) {
	st, u := taskEnv(t)
	taskID := manualClaimTaskID(t, st, TaskKeyLoginDaily, 100)
	if _, err := db.Exec(st.db, db.CurrentDialect(), "DROP TABLE users"); err != nil {
		t.Fatalf("DROP users 失败: %v", err)
	}
	ok, tokens := st.ClaimUserTask(u.ID, u.TenantID, taskID)
	if ok || tokens != 0 {
		t.Fatalf("租户读失败必须 fail-closed，实得 ok=%v tokens=%d", ok, tokens)
	}
}

// TestClaimUserTaskPositiveControl 正锁：表完好时同一条调用链确实发放一次、第二次去重。
func TestClaimUserTaskPositiveControl(t *testing.T) {
	st, u := taskEnv(t)
	taskID := manualClaimTaskID(t, st, TaskKeyLoginDaily, 100)
	before := taskPermanentBalance(t, st, u.TenantID)
	ok, tokens := st.ClaimUserTask(u.ID, u.TenantID, taskID)
	if !ok || tokens != 100 {
		t.Fatalf("正常路径应发放 100，实得 ok=%v tokens=%d", ok, tokens)
	}
	if after := taskPermanentBalance(t, st, u.TenantID); after != before+100 {
		t.Fatalf("余额应 +100，前=%d 后=%d", before, after)
	}
	if ok2, _ := st.ClaimUserTask(u.ID, u.TenantID, taskID); ok2 {
		t.Fatal("同日第二次领取应被去重拒绝")
	}
}

// scanDiscardedScans 在指定函数的函数体里数 Scan 返回值被丢进 `_` 的次数与受检次数。
func scanDiscardedScans(t *testing.T, name string, src []byte, fnName string) (discarded, checked int, found bool) {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, src, 0)
	if err != nil {
		t.Fatalf("解析 %s 失败: %v", name, err)
	}
	ast.Inspect(f, func(n ast.Node) bool {
		fd, ok := n.(*ast.FuncDecl)
		if !ok || fd.Name.Name != fnName || fd.Body == nil {
			return true
		}
		found = true
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			as, ok := n.(*ast.AssignStmt)
			if !ok || len(as.Rhs) != 1 {
				return true
			}
			call, ok := as.Rhs[0].(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Scan" {
				return true
			}
			if id, ok := as.Lhs[0].(*ast.Ident); ok && id.Name == "_" {
				discarded++
			} else {
				checked++
			}
			return true
		})
		return false
	})
	return
}

// TestClaimUserTaskNeverDiscardsScanErrors 静态闸：ClaimUserTask 不许把 Scan 结果丢进 `_`。
func TestClaimUserTaskNeverDiscardsScanErrors(t *testing.T) {
	src, err := os.ReadFile("tasks.go")
	if err != nil {
		t.Fatalf("读 tasks.go 失败: %v", err)
	}
	discarded, checked, found := scanDiscardedScans(t, "tasks.go", src, "ClaimUserTask")
	if !found {
		t.Fatal("未找到 ClaimUserTask——静态闸失明，需同步调整扫描器")
	}
	if discarded != 0 {
		t.Fatalf("ClaimUserTask 里有 %d 处 Scan 结果被丢进 `_`（吞错会退成重复发放/发进平台桶）", discarded)
	}
	if checked < 2 {
		t.Fatalf("ClaimUserTask 受检 Scan 只有 %d 处（去重 + 读租户应至少 2 处），闸门可能失明", checked)
	}
}

// TestClaimUserTaskScanScannerDetectsDiscard 反向自证：吞错源码必须被判红。
func TestClaimUserTaskScanScannerDetectsDiscard(t *testing.T) {
	const bad = `package store

func ClaimUserTask(x int) {
	var exists int
	_ = db.QueryRow("SELECT COUNT(*)").Scan(&exists)
}
`
	d, c, found := scanDiscardedScans(t, "bad.go", []byte(bad), "ClaimUserTask")
	if !found || d != 1 || c != 0 {
		t.Fatalf("吞错样本应判 discarded=1 checked=0，实得 d=%d c=%d found=%v ⇒ 反证失败", d, c, found)
	}
}
