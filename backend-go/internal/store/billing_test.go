// ============ billing_test.go · 职责说明 ============
// G18（2026-10-19）「退款收回权益的告警不许静默失败」装配闸。
// 缺陷形态：RefundOrder 尾部 `s.CreateAlert(...)` 的返回值被丢弃——退款已提交，
// 但那条对账/人工核对告警若写库失败，屏幕上一切正常、告警链却断了，没有任何读数能指向它。
// 本文件静态钉住：RefundOrder 里每一处 CreateAlert 都必须写成
// `if err := s.CreateAlert(...); err != nil { observability.Warn(...) }` 的受检形态。
// 反向自证：合成一段裸调用源码必须判红（免得扫描器恒绿）。
// 只读源码，不连库。
// =============================================
package store

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// alertCheckScan 在指定函数里数 CreateAlert 的「总调用数」与「被 if ...; err != nil 受检且出 Warn 的数」。
func alertCheckScan(t *testing.T, name string, src []byte, fnName string) (total, checked int, found bool) {
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

		checkedCalls := map[*ast.CallExpr]bool{}
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			ifs, ok := n.(*ast.IfStmt)
			if !ok || ifs.Init == nil {
				return true
			}
			init, ok := ifs.Init.(*ast.AssignStmt)
			if !ok || len(init.Rhs) != 1 {
				return true
			}
			call, ok := init.Rhs[0].(*ast.CallExpr)
			if !ok || selectorName(call) != "CreateAlert" {
				return true
			}
			if !condMentionsIdentLocal(ifs.Cond, errIdentOf(init)) {
				return true
			}
			if bodyCallsSelector(ifs.Body, "observability.Warn") {
				checkedCalls[call] = true
			}
			return true
		})

		ast.Inspect(fd.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || selectorName(call) != "CreateAlert" {
				return true
			}
			total++
			if checkedCalls[call] {
				checked++
			}
			return true
		})
		return false
	})
	return
}

// selectorName 取调用尾名：s.CreateAlert → "CreateAlert"；observability.Warn → "Warn"。
func selectorName(call *ast.CallExpr) string {
	if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
		return sel.Sel.Name
	}
	return ""
}

// bodyCallsSelector 块内是否出现指定调用（用点号全名比对，如 observability.Warn）。
func bodyCallsSelector(blk *ast.BlockStmt, full string) bool {
	found := false
	ast.Inspect(blk, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if x, ok := sel.X.(*ast.Ident); ok && x.Name+"."+sel.Sel.Name == full {
			found = true
		}
		return !found
	})
	return found
}

// errIdentOf 取 if init 里被赋值的标识符名（`if aerr := ...` → "aerr"）。
func errIdentOf(init *ast.AssignStmt) string {
	if len(init.Lhs) == 1 {
		if id, ok := init.Lhs[0].(*ast.Ident); ok {
			return id.Name
		}
	}
	return ""
}

// condMentionsIdentLocal 条件子树里是否出现指定标识符。
func condMentionsIdentLocal(cond ast.Expr, name string) bool {
	if name == "" {
		return false
	}
	found := false
	ast.Inspect(cond, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && id.Name == name {
			found = true
		}
		return !found
	})
	return found
}

// TestRefundOrderAlertsAreErrorChecked 主判据：RefundOrder 里 CreateAlert 必须逐处受检出声。
func TestRefundOrderAlertsAreErrorChecked(t *testing.T) {
	src, err := os.ReadFile("billing.go")
	if err != nil {
		t.Fatalf("读 billing.go 失败: %v", err)
	}
	total, checked, found := alertCheckScan(t, "billing.go", src, "RefundOrder")
	if !found {
		t.Fatal("未找到 RefundOrder——静态闸失明，需同步调整扫描器")
	}
	if total < 2 {
		t.Fatalf("RefundOrder 里 CreateAlert 只数到 %d 处（warning+info 应至少 2 处），闸门可能失明", total)
	}
	if checked != total {
		t.Fatalf("RefundOrder 有 %d/%d 处 CreateAlert 被丢返回值——告警写失败将静默", total-checked, total)
	}
}

// TestRevokePaidReferralNeverDiscardsScan 静态闸：revokePaidReferralIfAllRefunded 不许吞 Scan 错。
// 三处（付费校验 COUNT／读余额 SELECT）任一被吞都会造出错扣或重放窗口（G18）。
func TestRevokePaidReferralNeverDiscardsScan(t *testing.T) {
	src, err := os.ReadFile("billing.go")
	if err != nil {
		t.Fatalf("读 billing.go 失败: %v", err)
	}
	discarded, checked, found := scanDiscardedScans(t, "billing.go", src, "revokePaidReferralIfAllRefunded")
	if !found {
		t.Fatal("未找到 revokePaidReferralIfAllRefunded——静态闸失明")
	}
	if discarded != 0 {
		t.Fatalf("revokePaidReferralIfAllRefunded 有 %d 处 Scan 结果被丢进 `_`（吞错＝错扣/重放窗口）", discarded)
	}
	if checked < 3 {
		t.Fatalf("受检 Scan 只数到 %d 处（付费校验＋读余额应至少 3 处含 EnsureBalance 判错），闸门可能失明", checked)
	}
}

// TestRevokePaidReferralComputesBeforeDelete 静态闸：必须「先算应扣金额、后删行」。
// 旧形态先 DELETE 再读余额，读失败即造出重放窗口（奖励流水已消失、余额未扣）。
func TestRevokePaidReferralComputesBeforeDelete(t *testing.T) {
	src, err := os.ReadFile("billing.go")
	if err != nil {
		t.Fatalf("读 billing.go 失败: %v", err)
	}
	body := funcBodySource(t, "billing.go", src, "revokePaidReferralIfAllRefunded")
	iBalance := indexOfAll(body, "SELECT COALESCE(balance,0) FROM balance_accounts")
	iDelete := indexOfAll(body, "DELETE FROM referral_rewards WHERE id=?")
	if iBalance < 0 || iDelete < 0 {
		t.Fatalf("锚点未命中（读余额=%d 删行=%d）：闸门失明，需同步调整锚点", iBalance, iDelete)
	}
	if iBalance > iDelete {
		t.Fatalf("读余额(offset %d) 排在删行(offset %d) 之后——读失败会造出重放窗口，必须先算后删", iBalance, iDelete)
	}
}

// funcBodySource 取指定函数体的源码片段（用于按偏移量断言语句先后顺序）。
func funcBodySource(t *testing.T, name string, src []byte, fnName string) string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, src, 0)
	if err != nil {
		t.Fatalf("解析 %s 失败: %v", name, err)
	}
	start, end := -1, -1
	ast.Inspect(f, func(n ast.Node) bool {
		fd, ok := n.(*ast.FuncDecl)
		if !ok || fd.Name.Name != fnName || fd.Body == nil {
			return true
		}
		start = fset.Position(fd.Body.Pos()).Offset
		end = fset.Position(fd.Body.End()).Offset
		return false
	})
	if start < 0 || end < start {
		t.Fatalf("未定位到函数 %s", fnName)
	}
	return string(src[start:end])
}

// indexOfAll 子串首次出现位置（取第一处，够本判据用）。
func indexOfAll(s, sub string) int {
	return strings.Index(s, sub)
}

// TestAlertCheckScannerDetectsBareCall 反向自证：裸调用（吞返回值）必须判红。
func TestAlertCheckScannerDetectsBareCall(t *testing.T) {
	const bad = `package store

func RefundOrder(x int) {
	s.CreateAlert(1, "warning", "k", "m")
	s.CreateAlert(1, "info", "k", "m")
}
`
	total, checked, found := alertCheckScan(t, "bad.go", []byte(bad), "RefundOrder")
	if !found || total != 2 || checked != 0 {
		t.Fatalf("裸调用样本应判 total=2 checked=0，实得 t=%d c=%d found=%v ⇒ 反证失败", total, checked, found)
	}

	const good = `package store

func RefundOrder(x int) {
	if aerr := s.CreateAlert(1, "info", "k", "m"); aerr != nil {
		observability.Warn(context.Background(), "告警写入失败", "error", aerr.Error())
	}
}
`
	total2, checked2, _ := alertCheckScan(t, "good.go", []byte(good), "RefundOrder")
	if total2 != 1 || checked2 != 1 {
		t.Fatalf("受检样本应判 total=1 checked=1，实得 t=%d c=%d", total2, checked2)
	}
}
