// ============ 本文件职责中文说明 ============
// W3（2026-10-26，BUG-01「12 段译 10 段」/ BUG-06「源 12 译 10 ＋数字不一致驳回」的顺序锁）：
// `runTextTicket` 里「要不要整单重排」这一问，必须问在 `return err` **之前**。
//
// 为什么用源码顺序而不是行为用例（同 ticket_requeue_gate_test.go 的取法）：
// 缺陷本体就是两句的先后——`if err != nil { return err }` 挡在 `StructureRetryNeeded` 前面时，
// 产物、日志、计费全都"正常"，只有一件事悄悄坏了：**结构失败那一支永远走不到重排预算**。
// 而结构失败（漏段）恰恰是**以 err 收尾**的那一类，所以这条腿在现网一次都没生效过。
// 跑真链路要连库、连假上游、还要恰好让硬闸判失败；顺序在源码里就已经是可判的事实，
// 且这条判据不会因运行时抖动而假绿。
//
// 反向自证：把改动前那个顺序原样合成一份喂进同一个判据函数，必须判红——
// 否则本文件测的是"扫描器有没有读到东西"，不是这一句顺序。
// ============================================
package service

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// repairOrder 记 runTextTicket 里两个锚点的相对位置。
type repairOrder struct {
	bodyFound  bool
	askPos     token.Pos // 第一次 StructureRetryNeeded( 调用
	retErrPos  token.Pos // 第一处「err 非空即 return err」的 return 语句
	returnsErr bool      // 函数体里确有把原始错误报出去的那一支（吞错误＝另一类缺陷，另判）
}

// violated 返回「是否违规」与一句读数。
// 三种违规形态分开报，因为它们的病因不同：
// ① 没找到函数体＝锁失明；② 没有重排那一问＝预算被整体摘掉（锁失去对象）；
// ③ 问在 return err 之后＝本轮修复要防的那一形态；④ 根本不 return err＝错误被吞（更坏，
//
//	非结构失败会被写成"完成"，客户以为翻好了）。
func (o repairOrder) violated() (bool, string) {
	if !o.bodyFound {
		return true, "找不到 runTextTicket 函数体 ⇒ 本条锁失明（被改名或搬走，判据读不到任何东西）"
	}
	if !o.returnsErr {
		return true, "runTextTicket 里没有「err 非空 ⇒ return err」那一支 ⇒ 流水线错误被吞，坏单会被写成完成态"
	}
	if o.askPos == 0 {
		return true, "runTextTicket 里没有 StructureRetryNeeded 这一问 ⇒ 整单重排预算被摘掉，本条锁失去对象"
	}
	if o.askPos > o.retErrPos {
		return true, "StructureRetryNeeded 问在 return err **之后** ⇒ 结构失败（以 err 收尾那一类）永远走不到重排预算（W3 的缺陷本体）"
	}
	return false, ""
}

// scanRepairOrder 在给定源码文本里找 runTextTicket 方法体并按位置判顺序。
// 参数：src=整个 ticket.go 的源码，fset=同一个文件集（调用方保证 pos 可比）。
func scanRepairOrder(t *testing.T, src string, fset *token.FileSet) repairOrder {
	t.Helper()
	f, err := parser.ParseFile(fset, "ticket.go", src, parser.AllErrors)
	if err != nil {
		t.Fatalf("解析源码失败: %v", err)
	}
	out := repairOrder{}
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "runTextTicket" || fn.Body == nil {
			continue
		}
		out.bodyFound = true
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			if n == nil {
				return false
			}
			if ce, ok := n.(*ast.CallExpr); ok {
				if se, ok := ce.Fun.(*ast.SelectorExpr); ok && se.Sel.Name == "StructureRetryNeeded" && out.askPos == 0 {
					out.askPos = ce.Pos()
				}
			}
			// `return err` 语句（返回的就是那个 error 变量本身，不是 nil）
			if rs, ok := n.(*ast.ReturnStmt); ok && len(rs.Results) == 1 {
				if id, ok := rs.Results[0].(*ast.Ident); ok && id.Name == "err" {
					if !out.returnsErr {
						out.returnsErr = true
						out.retErrPos = rs.Pos()
					}
				}
			}
			return true
		})
		break
	}
	return out
}

// TestTextTicketRepairAskedBeforeReturnErr W3 主锁＋反证。
func TestTextTicketRepairAskedBeforeReturnErr(t *testing.T) {
	b, err := os.ReadFile("ticket.go")
	if err != nil {
		t.Fatalf("读 ticket.go 失败: %v", err)
	}
	fset := token.NewFileSet()
	if bad, why := scanRepairOrder(t, string(b), fset).violated(); bad {
		t.Errorf("现网形态判红：%s", why)
	}
	// 反证：把顺序改回**改动前那一份**必须判红。
	// 用一份语法完整的合成源码（同一个判据函数、同一套 AST 走法），
	// 形态就是本轮修掉的那两句先后：先 `if err != nil { return err }`，后 `StructureRetryNeeded`。
	const oldOrder = `package service

func (s *TicketService) runTextTicket(ctx context.Context, t *store.Ticket) error {
	err := wf.Executor.Execute(ctx, t, nil)
	if err != nil {
		return err
	}
	if wf.StructureRetryNeeded(t) {
		return errTicketRequeued
	}
	return nil
}
`
	fset2 := token.NewFileSet()
	bad, why := scanRepairOrder(t, oldOrder, fset2).violated()
	if !bad {
		t.Error("反证失败：改动前那一份顺序仍判绿 ⇒ 本条锁抓不到 W3 那一缺陷，属空转锁")
	} else if !strings.Contains(why, "return err") {
		t.Errorf("反证读数没指向顺序那一支，实得 %q（可能抓到的是别的违规，射程不对）", why)
	}
	// 反证②：把「错误被吞」那一形态也钉一次（去掉 return err 那一支）。
	swallowed := strings.Replace(oldOrder, "\tif err != nil {\n\t\treturn err\n\t}\n", "", 1)
	if swallowed == oldOrder {
		t.Fatal("反证②夹具没生效：没能摘掉 return err 那一段（锚漂了，这一条是空转）")
	}
	if bad2, why2 := scanRepairOrder(t, swallowed, token.NewFileSet()).violated(); !bad2 {
		t.Errorf("反证②失败：去掉 return err（错误被吞）仍判绿 ⇒ ④ 那一支判据是摆设（读数 %q）", why2)
	}
	// 反证③：把「重排那一问」整体摘掉也必须判红（预算被摘＝锁失去对象，不能绿灯）。
	noAsk := strings.Replace(oldOrder, "\tif wf.StructureRetryNeeded(t) {", "\tif false {", 1)
	if bad3, why3 := scanRepairOrder(t, noAsk, token.NewFileSet()).violated(); !bad3 {
		t.Errorf("反证③失败：摘掉 StructureRetryNeeded 仍判绿 ⇒ ② 那一支判据是摆设（读数 %q）", why3)
	}
}
