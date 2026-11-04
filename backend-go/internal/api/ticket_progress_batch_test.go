// ============ ticket_progress_batch_test.go · 职责说明 ============
// ★ 2026-11-04 体验批 A5 的接线闸门（api 侧两条）。
//
//	T3 档位判据只有一个出口：handleTickets 与 handleApproveList 都必须经
//	   store.EffectiveTicketPage 折档——两处各判一次就是"两把尺子"（AGENTS §一·5）。
//	T4 N+1 的**正反两半**一起判：出参循环里不许再逐条查 TicketStates／TicketFiles，
//	   **且**两个批量口必须真被调用。
//	   ⚠️ 只测负向那一半＝空锁：把批量调用整段摘掉时"没有逐条查询"照样成立，
//	      屏幕上的表现是运行中单的进度全按档位兜底、列表一点不报错——
//	      这正是本仓反复登记的"判据级代替不了守卫级"那一族。
//
// 两条都**先剥注释**再按函数体取：说明注释里逐字抄着改造前那两句
// （`s.Store.TicketStates(t.ID)`／`LIMIT 200`），不剥就会把"记下了坑"读成"踩了坑"。
// =============================================
package api

import (
	"os"
	"strings"
	"testing"
)

// readApiSrc 读 api 包内源文件（cwd 即包目录）。读不到判红不判跳过。
func readApiSrc(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("读 %s 失败: %v", name, err)
	}
	return string(b)
}

// stripApiComments 剥 Go 行注释（与 store 侧同口径；本包已有同名用途的
// stripGoCommentsForGate，这里刻意复用它，不再造第三把尺子）。
func apiFuncBody(t *testing.T, src, sig string) string {
	t.Helper()
	return goFuncBody(t, src, sig)
}

// TestTicketListPageTierHasSingleEntryPoint T3：两个列表 handler 都只经那一个折档出口。
func TestTicketListPageTierHasSingleEntryPoint(t *testing.T) {
	src := stripGoCommentsForGate(readApiSrc(t, "tickets.go"))
	for _, sig := range []string{
		"func (s *Server) handleTickets(",
		"func (s *Server) handleApproveList(",
	} {
		body := apiFuncBody(t, src, sig)
		if !strings.Contains(body, "store.EffectiveTicketPage(") {
			t.Fatalf("%s 没走 store.EffectiveTicketPage——档位判据长出第二处（两处各判一次迟早打架）", sig)
		}
		// 不许在 handler 里再手写一次"脏值折档"（那是第二把尺子的典型形态）
		if strings.Contains(body, "> store.TicketPageMax") || strings.Contains(body, "<= 0 ||") {
			t.Fatalf("%s 里自己判了一遍档位——判档只许在 store.EffectiveTicketPage 那一个出口", sig)
		}
	}
}

// TestTicketProgressWiredToBatchNotPerTicket T4：进度取数已从逐条换成批量（正反两半）。
func TestTicketProgressWiredToBatchNotPerTicket(t *testing.T) {
	src := stripGoCommentsForGate(readApiSrc(t, "tickets.go"))
	body := apiFuncBody(t, src, "func (s *Server) handleTickets(")
	// 负向：逐条查询的两句必须都不在位
	for _, dead := range []string{"s.Store.TicketStates(", "s.Store.TicketFiles("} {
		if strings.Contains(body, dead) {
			t.Fatalf("handleTickets 仍在逐条查 %s——N+1 被写回来了（10 张运行中单＝20 次往返）", dead)
		}
	}
	// 正向对照：批量口必须真被调用，且各恰一次（两处＝两条腿各数一遍也是漂移）
	for _, live := range []string{"s.Store.TicketStatesForIDs(", "s.Store.TicketFilesForIDs("} {
		if n := strings.Count(body, live); n != 1 {
			t.Fatalf("%s 应恰调用 1 次，实得 %d 次（0＝批量口没接线，>1＝长出第二处取数）", live, n)
		}
	}
	// 运行中 id 必须先收齐再批量取（在语种/单循环里逐个 append 后立即查＝还是 N+1）
	iCollect := strings.Index(body, "running = append(running, t.ID)")
	iBatch := strings.Index(body, "s.Store.TicketStatesForIDs(running)")
	if iCollect < 0 || iBatch < 0 || iCollect > iBatch {
		t.Fatalf("接线顺序不对：必须先收齐 running 的 id 再发批量查询（collect=%d batch=%d）", iCollect, iBatch)
	}
}
