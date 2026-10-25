// ============ file_leak_disposition_gate_test.go · 职责说明 ============
// F-01（2026-10-24 用户令：漏译阈值 50%→5%）的**处置纪律闸**：
// 方案 §六 的验收项写的是「补译后仍 >5% → completed + untranslated + quality_flagged，
// **不返回 Error、不退款**」。那条判据有两个半边，缺任一半都会静默走偏：
//
//	① 判据半边（已有锁）：engine.LeakRatioExceeded / leakedLang 的阈值与边界；
//	② 处置半边（本文件）：service/ticket.go 在超限那一刻**到底做了什么**。
//
// ②被写坏时没有任何症状：客户照样拿到产物、界面照样"翻译完成"，只是
//
//	· 少打「质检存疑」标记 ⇒ 审批台/前端徽标看不到这一单有未译段；
//	· 或者反过来走了失败/退款分支 ⇒ 客户为一份**已交付**的产物被退款，账对不上。
//
// 所以这里用 AST 问**位置与内容**（同 ticket_requeue_gate_test.go 的形状）：
// `LeakRatioExceeded(...)` 这个 if 的块里必须调 `SetTicketQualityFlagged`，
// 且不许出现退款调用 / 失败态写点 / 带返回值的 return。
// 反向自证：把那一行调用从源码里摘掉喂给同一个判定函数，必须判红。
// 本文件只读源码、不连库。
// =============================================
package service

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// leakDispositionVerdict 是「超限分支处置」的读数。
type leakDispositionVerdict struct {
	found    bool     // 找到 LeakRatioExceeded 判据（没找到＝闸门失明）
	hasFlag  bool     // 分支内调了 SetTicketQualityFlagged
	banned   []string // 分支内出现的不允许调用（退款/失败态写点）
	returned bool     // 分支内出现带返回值的 return（"不返回 Error"那一半边）
}

// verdictOK 把读数收敛成一句结论；ok=false 时 reason 直接说明哪一半坏了。
func (v leakDispositionVerdict) verdictOK() (bool, string) {
	if !v.found {
		return false, "找不到 LeakRatioExceeded 判据（service/ticket.go 的漏译处置链已失明）"
	}
	if !v.hasFlag {
		return false, "超限分支里没有 SetTicketQualityFlagged ⇒ 客户/审批台看不到「质检存疑」"
	}
	if v.returned {
		return false, "超限分支里出现带返回值的 return ⇒ 违反「不返回 Error」（已交付的产物不该判失败）"
	}
	if len(v.banned) > 0 {
		return false, "超限分支里出现不允许的写点：" + strings.Join(v.banned, ", ") + "（退款/失败态会让账与交付对不上）"
	}
	return true, ""
}

// scanLeakDisposition 按 AST 读源码里「LeakRatioExceeded 分支」的处置内容。
// 参数：src=service/ticket.go 源码。
// 返回：读数与解析错误。
func scanLeakDisposition(src []byte) (leakDispositionVerdict, error) {
	var v leakDispositionVerdict
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "ticket.go", src, 0)
	if err != nil {
		return v, err
	}
	ast.Inspect(f, func(n ast.Node) bool {
		ifs, ok := n.(*ast.IfStmt)
		if !ok {
			return true
		}
		if !condCalls(ifs.Cond, "LeakRatioExceeded") {
			return true
		}
		v.found = true
		ast.Inspect(ifs.Body, func(inner ast.Node) bool {
			switch x := inner.(type) {
			case *ast.CallExpr:
				name := calleeName(x.Fun)
				switch {
				case name == "SetTicketQualityFlagged":
					v.hasFlag = true
				case strings.Contains(name, "Refund"):
					v.banned = append(v.banned, name)
				case name == "SetTicketStatus":
					v.banned = append(v.banned, name)
				}
			case *ast.ReturnStmt:
				if len(x.Results) > 0 {
					v.returned = true
				}
			}
			return true
		})
		return true
	})
	return v, nil
}

// condCalls 判断条件表达式里是否调用了指定函数名。
func condCalls(e ast.Expr, want string) bool {
	hit := false
	ast.Inspect(e, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok && calleeName(c.Fun) == want {
			hit = true
		}
		return true
	})
	return hit
}

// calleeName 取调用表达式的最后一段名字（支持 s.Store.X / s.Engine.X 这类选择子）。
func calleeName(fun ast.Expr) string {
	switch f := fun.(type) {
	case *ast.Ident:
		return f.Name
	case *ast.SelectorExpr:
		return f.Sel.Name
	}
	return ""
}

// TestFileLeakDispositionWiring 守卫级：现网源码必须同时满足「打标 + 不失败 + 不退款」。
func TestFileLeakDispositionWiring(t *testing.T) {
	src, err := os.ReadFile("ticket.go")
	if err != nil {
		t.Fatalf("读不到 ticket.go: %v", err)
	}
	v, err := scanLeakDisposition(src)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if ok, reason := v.verdictOK(); !ok {
		t.Fatalf("%s", reason)
	}
}

// TestLeakDispositionGateReddensOnRemovedFlag 反证：摘掉 SetTicketQualityFlagged 必须判红。
// （正向腿若换成一个"永远返回 true"的实现，这条会跟着绿——所以它必须喂真源码的变异体。）
func TestLeakDispositionGateReddensOnRemovedFlag(t *testing.T) {
	src, err := os.ReadFile("ticket.go")
	if err != nil {
		t.Fatalf("读不到 ticket.go: %v", err)
	}
	// 变异体必须**语法合法**：直接抠掉调用会变成 `if ferr := s.Store.; ...`，解析期就崩、测不到判据
	mutated := strings.ReplaceAll(string(src), "s.Store.SetTicketQualityFlagged(t.ID)", "error(nil)")
	if mutated == string(src) {
		t.Fatal("变异体与原文件相同 ⇒ 改动锚点已漂，反证失去射程")
	}
	v, err := scanLeakDisposition([]byte(mutated))
	if err != nil {
		t.Fatalf("变异体解析失败: %v", err)
	}
	if ok, _ := v.verdictOK(); ok {
		t.Fatal("摘掉打标调用后仍判绿 ⇒ 这条闸门对「少了什么」完全无感")
	}
}
