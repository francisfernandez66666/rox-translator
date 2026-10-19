// ============ ticket_test.go · 职责说明 ============
// G18（2026-10-19）「多语言产物打包失败不许静默丢产物」的装配闸。
// 缺陷形态：zipOutputs 报错时旧代码把 zipPath 留空、回落 res.Files[0]，其余语言的产物
// 就这样悄悄丢了——用户以为工单只产出了一份文件，轨迹里既没有 warning 也没有通知。
// 本文件用 AST 钉住那一个 else 分支必须同时具备三件事：出声（observability.Warn）、
// 站内信（CreateNotification）、计数（zipFailCount 自增，收尾写回步骤据此置 warning）。
// 反向自证：合成一段缺通知的源码必须判红（免得扫描器只会恒绿）。
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

// zipFailSurfacing 扫描 zipOutputs 失败分支（zerr 的 else 块）里的三件事。
type zipFailSurfacing struct {
	elseFound bool
	warn      bool
	notify    bool
	counter   bool
}

// scanZipFailSurfacing 在源码里定位「zipOutputs 的 zerr != nil / == nil 分支」的 else 块并检查三件事。
func scanZipFailSurfacing(t *testing.T, name string, src []byte) zipFailSurfacing {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, src, 0)
	if err != nil {
		t.Fatalf("解析 %s 失败: %v", name, err)
	}
	var got zipFailSurfacing
	ast.Inspect(f, func(n ast.Node) bool {
		ifs, ok := n.(*ast.IfStmt)
		if !ok || ifs.Else == nil {
			return true
		}
		if !condMentionsIdent(ifs.Cond, "zerr") {
			return true
		}
		blk, ok := ifs.Else.(*ast.BlockStmt)
		if !ok {
			return true
		}
		got.elseFound = true
		ast.Inspect(blk, func(n ast.Node) bool {
			switch nd := n.(type) {
			case *ast.CallExpr:
				switch callName(nd) {
				case "observability.Warn":
					got.warn = true
				case "CreateNotification":
					got.notify = true
				}
			case *ast.IncDecStmt:
				if id, ok := nd.X.(*ast.Ident); ok && id.Name == "zipFailCount" {
					got.counter = true
				}
			}
			return true
		})
		return false
	})
	return got
}

// condMentionsIdent 条件子树里是否出现指定标识符（用于识别 zerr 判断）。
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

// callName 取调用名（支持 sel 形态）：CreateNotification / observability.Warn。
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

// TestZipFailureIsSurfaced 主判据：zipOutputs 失败分支必须同时出声、发站内信、计数；
// 且收尾的写回 warning 文案必须引用 zipFailCount（计数存在但没人读＝白计）。
func TestZipFailureIsSurfaced(t *testing.T) {
	src, err := os.ReadFile("ticket.go")
	if err != nil {
		t.Fatalf("读 ticket.go 失败: %v", err)
	}
	got := scanZipFailSurfacing(t, "ticket.go", src)
	if !got.elseFound {
		t.Fatal("未找到 zipOutputs 的失败分支（else 块）——本闸门失明，需同步调整扫描器")
	}
	if !got.warn {
		t.Error("zipOutputs 失败分支缺 observability.Warn：打包失败没有任何日志")
	}
	if !got.notify {
		t.Error("zipOutputs 失败分支缺 CreateNotification：用户/审批台看不到其余语言产物被丢")
	}
	if !got.counter {
		t.Error("zipOutputs 失败分支未自增 zipFailCount：收尾的写回 warning 无从置位")
	}
	// 计数必须被收尾读取（写回步骤 warning 文案里点名 zip_failed）。
	if !strings.Contains(string(src), "zip_failed=%d") {
		t.Error("收尾写回 warning 未引用 zipFailCount（应出现 zip_failed 计数）：计数形同虚设")
	}
}

// TestZipFailureScannerDetectsMissingNotify 反向自证：缺通知的合成源码必须判红（notify=false）。
func TestZipFailureScannerDetectsMissingNotify(t *testing.T) {
	const good = `package service

func handle(ctx context.Context, zerr error) {
	if zerr == nil {
		zipPath = zp
	} else {
		observability.Warn(ctx, "打包失败", "error", zerr.Error())
		zipFailCount++
		s.Store.CreateNotification(uid, "标题", "正文", 1, tid)
	}
}
`
	got := scanZipFailSurfacing(t, "good.go", []byte(good))
	if !got.elseFound || !got.warn || !got.notify || !got.counter {
		t.Fatalf("合规样本应三件事齐全，实得 %+v", got)
	}

	const bad = `package service

func handle(ctx context.Context, zerr error) {
	if zerr == nil {
		zipPath = zp
	} else {
		observability.Warn(ctx, "打包失败", "error", zerr.Error())
		zipFailCount++
	}
}
`
	gotBad := scanZipFailSurfacing(t, "bad.go", []byte(bad))
	if !gotBad.elseFound || gotBad.notify {
		t.Fatalf("缺通知的样本应判 notify=false，实得 %+v ⇒ 反证失败，扫描器可能恒绿", gotBad)
	}
}
