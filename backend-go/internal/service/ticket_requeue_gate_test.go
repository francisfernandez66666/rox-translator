// ============ ticket_requeue_gate_test.go · 职责说明 ============
// DEF-02（2026-10-24 云端 UAT）「文本工单结构失败自动整单重发」的收尾纪律闸。
//
// 缺陷形态（如果这一腿写错）：runTextTicket 把工单重新入队之后返回 errTicketRequeued，
// 而 runTicket 若在 return nil 之前先走了完成态收尾，那一轮就会
//
//	① 按本轮真实 token 再扣一次费（chargeTokens），新 job 认领后**再扣一次** ⇒ 客户为同一单付两遍；
//	② 投一条「工单失败」站内信（失败支）或「翻译完成」通知（完成支），而工单其实是 queued。
//
// 这两件事在界面上都只是"多一条通知/多一笔扣费"，产物完全正常，所以必须用**源码顺序**钉死：
// 重发早退必须在任何计费与任何通知写点**之前**。
//
// 判据形状用 AST 而不是字符串 grep：本文件要问的是「这条 if 在 runTicket 函数体里的位置」
// 以及「它的块里真的 return nil 了吗」，裸 grep 两句都能答，但把 return 挪进 else 也照样"命中"。
// 反向自证：合成一段「先扣费再判重发」的源码必须判红（免得扫描器只会恒绿）。
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

// requeueGate 记录 runTicket 里「重发早退」与「计费/通知写点」的相对位置。
type requeueGate struct {
	methodFound    bool // 没找到 runTicket 方法体＝闸门失明
	earlyReturnNil bool // 重发分支里确有 return nil
	requeuePos     token.Pos
	chargePos      token.Pos // 第一次 s.chargeTokens(...)
	notifyPos      token.Pos // 第一次通知写点（FinishTicket / CreateNotification）
}

// violated 判定「计费或通知发生在重发早退之前」。
// 没找到任何计费/通知写点也视为违规——那说明收尾链被整体摘掉，顺序判据失去对象。
func (g requeueGate) violated() (bool, string) {
	if !g.methodFound {
		return true, "没能在 ticket.go 里定位 (*TicketService).runTicket 的方法体，本闸门失明"
	}
	if !g.earlyReturnNil {
		return true, "runTicket 里 errors.Is(runErr, errTicketRequeued) 分支没有 return nil：重发会继续走完成态收尾"
	}
	if !g.chargePos.IsValid() && !g.notifyPos.IsValid() {
		return true, "runTicket 里既找不到 chargeTokens 也找不到通知写点：收尾链被摘，顺序判据无对象"
	}
	if g.chargePos.IsValid() && g.chargePos < g.requeuePos {
		return true, "s.chargeTokens 出现在重发早退之前 ⇒ 重发那一轮照样扣费，新 job 认领后再扣一遍（双扣费）"
	}
	if g.notifyPos.IsValid() && g.notifyPos < g.requeuePos {
		return true, "通知写点出现在重发早退之前 ⇒ 工单还在 queued 就投了「完成/失败」站内信"
	}
	return false, ""
}

// scanRequeueGate 解析源码并量出 runTicket 里上述三个位置。
func scanRequeueGate(t *testing.T, name string, src []byte) requeueGate {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, src, 0)
	if err != nil {
		t.Fatalf("解析 %s 失败: %v", name, err)
	}
	var g requeueGate
	// firstPos 在方法体里找第一个满足 pred 的表达式位置（取最小者＝最早出现）。
	var body *ast.BlockStmt
	ast.Inspect(f, func(n ast.Node) bool {
		fd, ok := n.(*ast.FuncDecl)
		if !ok || fd.Recv == nil || fd.Name.Name != "runTicket" {
			return true
		}
		if fd.Recv.List[0].Type == nil {
			return false
		}
		if !strings.Contains(recvTypeName(fd.Recv.List[0].Type), "TicketService") {
			return true
		}
		body = fd.Body
		g.methodFound = true
		return false
	})
	if body == nil {
		return g
	}
	ast.Inspect(body, func(n ast.Node) bool {
		switch nd := n.(type) {
		case *ast.IfStmt:
			if !condMentionsIdent(nd.Cond, "errTicketRequeued") {
				return true
			}
			if !g.requeuePos.IsValid() {
				g.requeuePos = nd.Pos()
			}
			ast.Inspect(nd.Body, func(m ast.Node) bool {
				rs, ok := m.(*ast.ReturnStmt)
				if !ok {
					return true
				}
				if len(rs.Results) == 1 {
					if id, is := rs.Results[0].(*ast.Ident); is && id.Name == "nil" {
						g.earlyReturnNil = true
					}
				}
				return false
			})
		case *ast.CallExpr:
			switch callName(nd) {
			case "s.chargeTokens":
				if !g.chargePos.IsValid() || nd.Pos() < g.chargePos {
					g.chargePos = nd.Pos()
				}
			case "s.Store.CreateNotification", "s.Store.FinishTicket":
				if !g.notifyPos.IsValid() || nd.Pos() < g.notifyPos {
					g.notifyPos = nd.Pos()
				}
			}
		}
		return true
	})
	return g
}

// recvTypeName 取接收者的类型名（指针对 TicketService 时脱引用）。
func recvTypeName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.StarExpr:
		return recvTypeName(t.X)
	case *ast.Ident:
		return t.Name
	case *ast.SelectorExpr:
		return t.Sel.Name
	}
	return ""
}

// TestRequeueEarlyReturnPrecedesBillingAndNotify 主判据：
// runTicket 的重发早退必须排在 chargeTokens 与通知写点之前，且块里确实 return nil。
func TestRequeueEarlyReturnPrecedesBillingAndNotify(t *testing.T) {
	src, err := os.ReadFile("ticket.go")
	if err != nil {
		t.Fatalf("读 ticket.go 失败: %v", err)
	}
	got := scanRequeueGate(t, "ticket.go", src)
	if bad, why := got.violated(); bad {
		t.Error(why)
	}
	// runTextTicket 那一侧必须真的把 errTicketRequeued 抛出来（早退判据的上游来源）。
	if !strings.Contains(string(src), "return errTicketRequeued") {
		t.Error("runTextTicket 未返回 errTicketRequeued：重发信号断了，runTicket 的早退分支形同虚设")
	}
}

// TestRequeueGateScannerDetectsLateReturn 反向自证：
// 合成一段「先扣费、后判重发」的源码必须判红（否则上面那条绿灯可能只是扫描器恒绿）。
func TestRequeueGateScannerDetectsLateReturn(t *testing.T) {
	const good = `package service

func (s *TicketService) runTicket(ctx context.Context, id int64) error {
	runErr := s.runTextTicket(ctx, t)
	if errors.Is(runErr, errTicketRequeued) {
		return nil
	}
	s.chargeTokens(ctx, t)
	s.Store.CreateNotification(uid, "标题", "正文", 1, tid)
	return nil
}
`
	if bad, why := scanRequeueGate(t, "good.go", []byte(good)).violated(); bad {
		t.Fatalf("合规样本应判绿，实得：%s", why)
	}

	const badLate = `package service

func (s *TicketService) runTicket(ctx context.Context, id int64) error {
	runErr := s.runTextTicket(ctx, t)
	s.chargeTokens(ctx, t)
	if errors.Is(runErr, errTicketRequeued) {
		return nil
	}
	return runErr
}
`
	if bad, _ := scanRequeueGate(t, "bad.go", []byte(badLate)).violated(); !bad {
		t.Fatal("把重发早退挪到扣费之后必须判红（双扣费形态），实得绿灯 ⇒ 反向自证失败")
	}

	const badNoReturn = `package service

func (s *TicketService) runTicket(ctx context.Context, id int64) error {
	runErr := s.runTextTicket(ctx, t)
	if errors.Is(runErr, errTicketRequeued) {
		log.Printf("重发了")
	}
	s.chargeTokens(ctx, t)
	return nil
}
`
	if bad, _ := scanRequeueGate(t, "noreturn.go", []byte(badNoReturn)).violated(); !bad {
		t.Fatal("重发分支里没有 return nil 必须判红（会继续走完成态收尾），实得绿灯 ⇒ 反向自证失败")
	}
}

// condMentionsIdent 条件子树里是否出现指定标识符（用于识别 errors.Is(runErr, errTicketRequeued)）。
// 2026-10-30 组5 起从 ticket_test.go 迁来：那份文件已改成「不落盘 zip」的源码锁，
// 不再是 AST 闸的宿主；这两个助手只有本文件在用。
func condMentionsIdent(cond ast.Expr, name string) bool {
	found := false
	ast.Inspect(cond, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && id.Name == name {
			found = true
		}
		return !found
	})
	return found
}

// callName 取调用名（支持 sel 形态）：s.chargeTokens / s.Store.CreateNotification。
func callName(call *ast.CallExpr) string {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return fn.Name
	case *ast.SelectorExpr:
		if x, ok := fn.X.(*ast.Ident); ok {
			return x.Name + "." + fn.Sel.Name
		}
		// s.Store.CreateNotification 这类链式选择器：只取方法名。
		return fn.Sel.Name
	}
	return ""
}
