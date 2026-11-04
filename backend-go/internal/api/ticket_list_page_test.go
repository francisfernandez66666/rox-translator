// ============ ticket_list_page_test.go · 职责说明 ============
// ★ 2026-11-04 体验批 A5：工单列表分页出参的行为闸门（打真 handler，不比字面量）。
//
// 四条射程：
//
//	P1 缺省档逐字等于改造前那条写死的 LIMIT 200（老客户端不带参数 ⇒ 行集一字不变）；
//	P2 出栈的 limit/offset 是**真正生效那一档**，不是回显入参（AGENTS §一·5 分页口径）；
//	P3 脏参（非数字／0／负数／超上限）折回缺省档、不报错、行照常回；
//	P4 翻页不重不漏（第一页与第二页 id 交集为空、并集条数＝limit×2），
//	   越界页 ⇒ `tickets` 必须是 `[]` 不是 `null`（空页在 HTTP 层归一的硬口径）。
//
// 方言：自钉内存 SQLite（AGENTS §一·4），夹具走 newF44Probe（超管上下文 tid=0）。
// =============================================
package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"translator/internal/store"
)

// callTicketsListRaw 直调 GET /api/tickets?<query>，返回状态码与**未剥壳的**响应体 map。
// 刻意保留原始 map（不解成结构体）：P4 要判 `tickets` 到底是 `[]` 还是 `null`，
// 反序列化成 []any 之后两者都变成 nil slice，判据就瞎了。
func callTicketsListRaw(t *testing.T, s *Server, tok, query string) (int, map[string]interface{}) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/tickets"+query, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	s.handleTickets(rec, req)
	var out map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("工单列表响应不是 JSON（code=%d）: %s", rec.Code, rec.Body.String())
	}
	return rec.Code, out
}

// ticketIDsFromRaw 把响应里的 tickets 摊成 id 列表；顺带判"空页必须是 [] 不是 null"。
func ticketIDsFromRaw(t *testing.T, body map[string]interface{}) []interface{} {
	t.Helper()
	raw, ok := body["tickets"]
	if !ok {
		t.Fatalf("响应缺 tickets 键: %v", body)
	}
	if raw == nil {
		t.Fatal("tickets 出栈成 null——空页在 HTTP 层必须归一成 []（AGENTS §一·5 空值形态口径）")
	}
	arr, ok := raw.([]interface{})
	if !ok {
		t.Fatalf("tickets 不是数组: %T", raw)
	}
	return arr
}

// idsOf 取一页第列 id（夹具按 id 倒序，所以第一页是最新那几张）。
func idsOf(t *testing.T, rows []interface{}) []int64 {
	t.Helper()
	var out []int64
	for _, r := range rows {
		m, ok := r.(map[string]interface{})
		if !ok {
			t.Fatalf("tickets 元素不是对象: %T", r)
		}
		idf, ok := m["id"].(float64)
		if !ok {
			t.Fatalf("tickets 元素缺 id: %v", m)
		}
		out = append(out, int64(idf))
	}
	return out
}

// seedTickets 建 n 张工单（同租户），返回创建顺序的 id。
func seedTickets(t *testing.T, s *Server, n int) []int64 {
	t.Helper()
	var ids []int64
	for i := 0; i < n; i++ {
		tk, err := s.Store.CreateTicket(0, 1, fmt.Sprintf("A5 分页夹具 %d", i), fmt.Sprintf("第 %d 段原文。", i), "", "en")
		if err != nil {
			t.Fatalf("建工单 %d 失败: %v", i, err)
		}
		ids = append(ids, tk.ID)
	}
	return ids
}

// TestTicketListDefaultTierUnchanged P1＋P2：不带参数 ⇒ 生效档就是改造前那条 200，且出栈报的是生效档。
func TestTicketListDefaultTierUnchanged(t *testing.T) {
	s, _, tok := newF44Probe(t)
	seedTickets(t, s, 3)
	code, body := callTicketsListRaw(t, s, tok, "")
	if code != http.StatusOK {
		t.Fatalf("应 200，实得 %d: %v", code, body)
	}
	if got := idsOf(t, ticketIDsFromRaw(t, body)); len(got) != 3 {
		t.Fatalf("3 张单应全回，实得 %d 条", len(got))
	}
	if l, _ := body["limit"].(float64); int(l) != store.TicketPageDefault {
		t.Fatalf("缺省档必须逐字等于改造前写死的 200（老客户端行为不能变），实得 %v", body["limit"])
	}
	if o, _ := body["offset"].(float64); int(o) != 0 {
		t.Fatalf("缺省 offset 应为 0，实得 %v", body["offset"])
	}
	if tot, _ := body["total"].(float64); int(tot) != 3 {
		t.Fatalf("total 应为同谓词下的真值 3，实得 %v", body["total"])
	}
	if hm, ok := body["has_more"].(bool); !ok || hm {
		t.Fatalf("3 条没打满档位，has_more 应为 false（实得 %v）", body["has_more"])
	}
}

// TestTicketListDirtyParamsFoldBack P3：脏参折回档位、不报错、行照常回。
func TestTicketListDirtyParamsFoldBack(t *testing.T) {
	s, _, tok := newF44Probe(t)
	seedTickets(t, s, 2)
	for _, dirty := range []string{"limit=abc", "limit=0", "limit=-9", "limit=999999", "offset=-3"} {
		code, body := callTicketsListRaw(t, s, tok, "?"+dirty)
		if code != http.StatusOK {
			t.Fatalf("%s 应折档不报错，实得 %d: %v", dirty, code, body)
		}
		if got := idsOf(t, ticketIDsFromRaw(t, body)); len(got) != 2 {
			t.Fatalf("%s 折档后行没回全: %v", dirty, got)
		}
		if l, _ := body["limit"].(float64); int(l) != store.TicketPageDefault {
			t.Fatalf("%s 出栈 limit 必须是生效档 %d（回显入参＝客户端对不上账），实得 %v",
				dirty, store.TicketPageDefault, body["limit"])
		}
	}
	// 反向对照：合法小档位必须原样生效（否则"折档"其实是"永远 200"）
	code, body := callTicketsListRaw(t, s, tok, "?limit=1")
	if code != http.StatusOK {
		t.Fatalf("limit=1 应 200")
	}
	if l, _ := body["limit"].(float64); int(l) != 1 {
		t.Fatalf("合法档位 1 被折掉了，实得 %v", body["limit"])
	}
	if hm, _ := body["has_more"].(bool); !hm {
		t.Fatalf("1 条打满 1 的档位，has_more 必须为 true（分页器据此显示还有下一页）")
	}
}

// TestTicketListPagingNoOverlapNoGap P4：翻页不重不漏＋越界页出 []。
func TestTicketListPagingNoOverlapNoGap(t *testing.T) {
	s, _, tok := newF44Probe(t)
	seedTickets(t, s, 5)
	_, p0 := callTicketsListRaw(t, s, tok, "?limit=2&offset=0")
	_, p1 := callTicketsListRaw(t, s, tok, "?limit=2&offset=2")
	ids0 := idsOf(t, ticketIDsFromRaw(t, p0))
	ids1 := idsOf(t, ticketIDsFromRaw(t, p1))
	if len(ids0) != 2 || len(ids1) != 2 {
		t.Fatalf("两页应各 2 条，实得 %d / %d", len(ids0), len(ids1))
	}
	seen := map[int64]bool{}
	for _, id := range ids0 {
		seen[id] = true
	}
	for _, id := range ids1 {
		if seen[id] {
			t.Fatalf("第 2 页与第 1 页重叠（id=%d）——offset 没进 SQL 或档位算错", id)
		}
	}
	// 越界页：tickets 必须是空数组而不是 null，total 仍是真值
	_, over := callTicketsListRaw(t, s, tok, "?limit=2&offset=99")
	if got := ticketIDsFromRaw(t, over); len(got) != 0 {
		t.Fatalf("越界页应为空数组，实得 %d 条", len(got))
	}
	if tot, _ := over["total"].(float64); int(tot) != 5 {
		t.Fatalf("空页的 total 仍须是真值 5（分页器靠它算总页数），实得 %v", over["total"])
	}
	if hm, ok := over["has_more"].(bool); !ok || hm {
		t.Fatalf("越界空页 has_more 必须为 false（真值 5 已经翻完），实得 %v", over["has_more"])
	}
}

// TestTicketProgressBatchMatchesPerTicket A5-N+1 的等值锁：批量口的分组结果
// 必须与逐条 TicketStates／TicketFiles 完全一致（治 N+1 不许改语义）。
func TestTicketProgressBatchMatchesPerTicket(t *testing.T) {
	s, _, _ := newF44Probe(t)
	tk1, err := s.Store.CreateTicket(0, 1, "批量进度对照 1", "第一段。", "", "en")
	if err != nil {
		t.Fatalf("建工单失败: %v", err)
	}
	tk2, err := s.Store.CreateTicket(0, 1, "批量进度对照 2", "第二段。", "", "en")
	if err != nil {
		t.Fatalf("建工单失败: %v", err)
	}
	if err := s.Store.SetTicketState(tk1.ID, "translate", "done", "{}"); err != nil {
		t.Fatalf("写状态失败: %v", err)
	}
	if err := s.Store.SetTicketState(tk2.ID, "extract", "running", "{}"); err != nil {
		t.Fatalf("写状态失败: %v", err)
	}
	per1, err := s.Store.TicketStates(tk1.ID)
	if err != nil {
		t.Fatalf("逐条读状态失败: %v", err)
	}
	batch, err := s.Store.TicketStatesForIDs([]int64{tk1.ID, tk2.ID})
	if err != nil {
		t.Fatalf("批量读状态失败: %v", err)
	}
	if len(batch[tk1.ID]) != len(per1) {
		t.Fatalf("批量口与逐条口行数不等：%d vs %d（分组或排序漂了）", len(batch[tk1.ID]), len(per1))
	}
	for i := range per1 {
		if batch[tk1.ID][i].Step != per1[i].Step || batch[tk1.ID][i].Status != per1[i].Status {
			t.Fatalf("第 %d 行内容不等值: 批量 %s/%s vs 逐条 %s/%s",
				i, batch[tk1.ID][i].Step, batch[tk1.ID][i].Status, per1[i].Step, per1[i].Status)
		}
	}
	// 空入参：必须回**空 map**且不发 SQL（返回 nil 会让调用方各判一次空，那是第二把尺子）
	empty, err := s.Store.TicketStatesForIDs(nil)
	if err != nil {
		t.Fatalf("空入参不该报错: %v", err)
	}
	if empty == nil || len(empty) != 0 {
		t.Fatalf("空入参应回空 map，实得 %#v", empty)
	}
	if e2, err2 := s.Store.TicketFilesForIDs(nil); err2 != nil || e2 == nil || len(e2) != 0 {
		t.Fatalf("文件批量口空入参应回空 map，实得 %#v err=%v", e2, err2)
	}
}
