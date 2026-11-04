// ============ ticket_artifact_summary_test.go · 职责说明 ============
// ★ 2026-11-04 体验批 §5.4-B2 的出栈面闸门（行内两颗下载钮的后端半边）。
//
// C1 列表每一行都必须带 artifact_summary，且 **md / original 两个键恒在**；
// C2 一张还没有产物的单 ⇒ `{"md":null,"original":null}`，**不是整块缺席、也不是整块 null**
//
//	（Go 的 nil map 出栈是 null，那是这一条存在的唯一理由——§一·5「空值形态必须归一」在这一行的落点）；
//
// C3 有产物时摘要里的 id/name/lang 必须与该单**详情接口**回的同一条逐值等值（同源锁，防两处各取各的）；
// C4 primary 决定选哪一组（多语种单行内只出主语种那一组，多语种全集仍留在详情弹窗）；
// C5 接线：handleTickets 只许批量取一次产物，不许退回逐单 ListTicketArtifacts（N+1 的第二条腿）。
//
// 方言：自钉内存 SQLite（AGENTS §一·4，newF44Probe）。
// =============================================
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"translator/internal/store"
)

// callTicketsJSON 直调 GET /api/tickets，返回解码后的响应体。
func callTicketsJSON(t *testing.T, s *Server, tok string) map[string]interface{} {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/tickets", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	s.handleTickets(rec, req)
	var out map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("工单列表响应不是 JSON: %s", rec.Body.String())
	}
	return out
}

// rowOf 从列表里按 ticket_no 找到那一行。
func rowOf(t *testing.T, body map[string]interface{}, no string) map[string]interface{} {
	t.Helper()
	rows, _ := body["tickets"].([]interface{})
	for _, r := range rows {
		m, _ := r.(map[string]interface{})
		if m != nil && m["ticket_no"] == no {
			return m
		}
	}
	t.Fatalf("列表里没有 ticket_no=%s 这一行（共 %d 行）", no, len(rows))
	return nil
}

// mkTicket 建一张文本工单并返回。
func mkTicket(t *testing.T, s *Server, title string) *store.Ticket {
	t.Helper()
	tk, err := s.Store.CreateTicket(0, 1, title, title+" 原文", "", "en")
	if err != nil {
		t.Fatalf("建工单失败: %v", err)
	}
	return tk
}

// putArtifact 登记一件产物（kind 用 store 侧常量，禁止在这里抄字面量）。
func putArtifact(t *testing.T, s *Server, tk *store.Ticket, lang, kind, name string, primary bool) *store.TicketArtifact {
	t.Helper()
	a := &store.TicketArtifact{
		TenantID: tk.TenantID, TicketID: tk.ID, FileID: 0, Lang: lang, Kind: kind,
		Path: "/tmp/" + name, Name: name, Size: 12, Primary: primary,
		CreatedAt: time.Now().Format(time.RFC3339),
	}
	got, err := s.Store.UpsertTicketArtifact(a)
	if err != nil {
		t.Fatalf("登记产物失败: %v", err)
	}
	return got
}

// TestTicketListRowsCarryArtifactSummary C1＋C2：摘要形状恒定，空产物出两个 null 而不是缺席。
func TestTicketListRowsCarryArtifactSummary(t *testing.T) {
	s, _, tok := newF44Probe(t)
	tk := mkTicket(t, s, "摘要形状锁")
	body := callTicketsJSON(t, s, tok)
	row := rowOf(t, body, tk.TicketNo)
	raw, ok := row["artifact_summary"]
	if !ok {
		t.Fatal("行里没有 artifact_summary 键（新字段可忽略，但不能不出）")
	}
	if raw == nil {
		t.Fatal("artifact_summary 整块出 null——前端要各判一次 obj?.md，那是空值形态没归一")
	}
	sum, isMap := raw.(map[string]interface{})
	if !isMap {
		t.Fatalf("artifact_summary 不是对象: %T", raw)
	}
	for _, k := range []string{"md", "original"} {
		v, present := sum[k]
		if !present {
			t.Fatalf("摘要缺 %s 键（两个键必须恒在，没有产物时出 null）: %v", k, sum)
		}
		if v != nil {
			t.Fatalf("这张单还没有产物，%s 应为 null，实得 %v", k, v)
		}
	}
}

// TestTicketArtifactSummaryMatchesDetail C3：行内摘要与详情接口逐值等值（同源，不是两份各算各的）。
func TestTicketArtifactSummaryMatchesDetail(t *testing.T) {
	s, _, tok := newF44Probe(t)
	tk := mkTicket(t, s, "摘要同源锁")
	md := putArtifact(t, s, tk, "en", store.TicketArtifactKindMD, "摘要同源锁-en.md", false)
	putArtifact(t, s, tk, "en", store.TicketArtifactKindOriginal, "摘要同源锁-en.docx", true)

	row := rowOf(t, callTicketsJSON(t, s, tok), tk.TicketNo)
	sum := row["artifact_summary"].(map[string]interface{})
	got, ok := sum["md"].(map[string]interface{})
	if !ok {
		t.Fatalf("md 摘要没出对象: %v", sum["md"])
	}
	if int64(got["id"].(float64)) != md.ID || got["name"] != md.Name || got["lang"] != "en" {
		t.Fatalf("md 摘要与库里那条不等值：摘要 %+v vs 产物 id=%d name=%s", got, md.ID, md.Name)
	}
	// 详情面（同一件产物的真源）必须给出同一个 id —— 这里用 store 直读比对，
	// 因为详情 handler 还需要工单状态；两者必须同源，谁漂了这条都红。
	detail, err := s.Store.ListTicketArtifacts(tk.ID)
	if err != nil {
		t.Fatalf("详情侧读产物失败: %v", err)
	}
	var sameID bool
	for _, a := range detail {
		if a.ID == int64(got["id"].(float64)) {
			sameID = true
		}
	}
	if !sameID {
		t.Fatal("行内摘要给出的 id 在该单详情清单里找不到（两处各取各的）")
	}
	// original 也必须点名（primary 那一件）
	orig, ok2 := sum["original"].(map[string]interface{})
	if !ok2 {
		t.Fatalf("original 摘要没出对象: %v", sum["original"])
	}
	if orig["name"] != "摘要同源锁-en.docx" {
		t.Fatalf("original 摘要选错了件: %+v", orig)
	}
}

// TestTicketArtifactSummaryPicksPrimaryGroup C4：多语种时行内只出 primary 所在那一组。
func TestTicketArtifactSummaryPicksPrimaryGroup(t *testing.T) {
	s, _, tok := newF44Probe(t)
	tk := mkTicket(t, s, "主语种选择锁")
	// 两组：en（非主）与 ja（主）。md 在前、original 在后是 store 侧顺序契约，这里照该顺序登记。
	putArtifact(t, s, tk, "en", store.TicketArtifactKindMD, "a-en.md", false)
	putArtifact(t, s, tk, "en", store.TicketArtifactKindOriginal, "a-en.docx", false)
	putArtifact(t, s, tk, "ja", store.TicketArtifactKindMD, "b-ja.md", true)
	putArtifact(t, s, tk, "ja", store.TicketArtifactKindOriginal, "b-ja.docx", true)

	sum := rowOf(t, callTicketsJSON(t, s, tok), tk.TicketNo)["artifact_summary"].(map[string]interface{})
	md := sum["md"].(map[string]interface{})
	if md["lang"] != "ja" {
		t.Fatalf("行内摘要没按 primary 选组，md 取了 %v（应为 ja 那一组）", md["lang"])
	}
	if md["name"] != "b-ja.md" {
		t.Fatalf("选组对了但件选错了: %+v", md)
	}
	orig := sum["original"].(map[string]interface{})
	if orig["lang"] != "ja" || orig["name"] != "b-ja.docx" {
		t.Fatalf("original 与 md 不同组（两颗钮会指向两种语言）: %+v", orig)
	}
}

// TestTicketListArtifactsWiredToBatch C5：接线——批量口恰一次，逐单口不许回到列表里。
func TestTicketListArtifactsWiredToBatch(t *testing.T) {
	src := stripGoCommentsForGate(readApiSrc(t, "tickets.go"))
	body := goFuncBody(t, src, "func (s *Server) handleTickets(")
	if strings.Contains(body, "s.Store.ListTicketArtifacts(") {
		t.Fatal("列表 handler 里又逐单查产物了——第二个 N+1")
	}
	if n := strings.Count(body, "s.Store.ListTicketArtifactsForTickets("); n != 1 {
		t.Fatalf("批量产物口应恰调用 1 次，实得 %d", n)
	}
	// 摘要必须逐行挂上（只取不挂＝后端白查一次，前端还是拿不到）
	if n := strings.Count(body, "artifact_summary"); n < 1 {
		t.Fatal("出参里没有 artifact_summary")
	}
}
