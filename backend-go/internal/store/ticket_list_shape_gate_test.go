// ============ ticket_list_shape_gate_test.go · 职责说明 ============
// ★ 2026-11-04 体验批 A5 的形态闸门：工单列表/审批台这两条 SQL 的"形状"必须锁住。
//
// 为什么是形态锁而不是行为锁：行为侧已由 api/ticket_list_page_test.go 打真 handler 验过
// 档位、翻页、空页归一。本文件锁的是**退化路径**——那些改动不会让任何一条行为断言变红，
// 但会让这一族重新长回"两份清单/写死档位/逐条查进度"：
//
//	T1 两条列表函数都不许内联列清单（必须引用唯一那份 ticketListSelectCols）；
//	T2 两条都不许出现写死的 `LIMIT 200`，必须是参数化 `LIMIT ? OFFSET ?`；
//	T3 档位判据只有一个出口（api 两个 handler 必须经 store.EffectiveTicketPage）；
//	T4 工单列表出参循环里不许再出现逐条 TicketStates／TicketFiles（N+1 的锚点），
//	   且两个批量口必须真被调用（正向对照——只测"没有逐条"会在批量口被整个摘掉时照样绿）。
//
// ⚠️ 三条判据都**先剥注释**再取函数体：这些修法旁边的说明注释里就抄着 `LIMIT 200`
//
//	和 `s.Store.TicketStates(` 那两个字面（记录改造前形态），不剥注释会把"记下了坑"读成"踩了坑"。
//
// =============================================
package store

import (
	"os"
	"strings"
	"testing"
)

// gateFuncBody 按花括号配平从源码里切出一个函数体（含签名行）。
// 为什么不能拿整份文件做子串判断：同包里有别的函数也用同名调用，整文件判会假绿。
func gateFuncBody(t *testing.T, src, signaturePrefix string) string {
	t.Helper()
	i := strings.Index(src, signaturePrefix)
	if i < 0 {
		t.Fatalf("源码里找不到 %q（签名变了就同步改这条锁，别删断言）", signaturePrefix)
	}
	open := strings.Index(src[i:], "{")
	if open < 0 {
		t.Fatalf("%q 后找不到函数体起始花括号", signaturePrefix)
	}
	depth := 0
	for k := i + open; k < len(src); k++ {
		switch src[k] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return src[i : k+1]
			}
		}
	}
	t.Fatalf("%q 的函数体花括号未配平（源码被截断？）", signaturePrefix)
	return ""
}

// TestTicketListShapeT1ColumnsAreSingleSource T1：两条列表 SQL 必须引用唯一那份列清单。
func TestTicketListShapeT1ColumnsAreSingleSource(t *testing.T) {
	src := stripGoLineComments(mustReadGoFileForShapeGate(t, "ticket_list_page.go"))
	for _, sig := range []string{
		"func (s *Store) ListTicketsPage(",
		"func (s *Store) ListPendingApprovalPage(",
	} {
		body := gateFuncBody(t, src, sig)
		if !strings.Contains(body, "ticketListSelectCols") {
			t.Fatalf("%s 没引用唯一那份列清单——又开始内联抄列串", sig)
		}
		if strings.Contains(body, "COALESCE(reject_source,") {
			t.Fatalf("%s 里出现内联列清单（那份 COALESCE 只许存在于常量里）", sig)
		}
	}
	// 射程自证：常量本体确实带着那几列（否则"引用了常量"可以是引用一个空串）
	if !strings.Contains(src, "COALESCE(reject_source,'') AS reject_source") {
		t.Fatal("ticketListSelectCols 常量里找不到 reject_source 那一列——清单被改窄了，本锁的靶子已漂")
	}
}

// TestTicketListShapeT2NoHardcodedLimit T2：不许再出现写死的 LIMIT 200，必须参数化。
func TestTicketListShapeT2NoHardcodedLimit(t *testing.T) {
	src := stripGoLineComments(mustReadGoFileForShapeGate(t, "ticket_list_page.go"))
	for _, sig := range []string{
		"func (s *Store) ListTicketsPage(",
		"func (s *Store) ListPendingApprovalPage(",
	} {
		body := gateFuncBody(t, src, sig)
		if strings.Contains(body, "LIMIT 200") {
			t.Fatalf("%s 里还有写死的 LIMIT 200——档位没进常量（那种写法第 201 条永远取不到且不出声）", sig)
		}
		if !strings.Contains(body, "LIMIT ? OFFSET ?") {
			t.Fatalf("%s 的 SQL 不是参数化 LIMIT/OFFSET 形状: %s", sig, body)
		}
		if !strings.Contains(body, "EffectiveTicketPage") {
			t.Fatalf("%s 没经 EffectiveTicketPage 折档——档位判据长出第二处", sig)
		}
	}
	// 全仓 tickets 表读取口：不许有别处再写死 LIMIT 200（棘轮式点名，扫不到即判闸门失明）
	all := mustReadGoFileForShapeGate(t, "tickets.go")
	if strings.Contains(stripGoLineComments(all), "LIMIT 200") {
		t.Fatal("tickets.go 里又出现写死的 LIMIT 200")
	}
}

// mustReadGoFileForShapeGate 读本包源文件（cwd 即包目录；读不到就是锚点漂了，判红不判跳过——
// "文件读不到"和"这一族已经不存在"在两处都该出声，静默跳过等于闸门从未存在）。
func mustReadGoFileForShapeGate(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("读 %s 失败: %v", name, err)
	}
	return string(b)
}
