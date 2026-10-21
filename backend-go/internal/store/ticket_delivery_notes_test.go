// ============================================================================
// ★ 2026-10-21 UAT 收口 M2：交付注记列（tickets.degraded_langs / gate_warnings）回归。
//
// 缺陷形态：引擎 res.Data.DegradedLangs／GateWarnings 只写站内信与日志，
// 工单表没有对应列、详情出参没有对应键；而前端早在按 detail.ticket.degraded_langs
// 渲染「降级交付」横幅——两头没接上，凡是降级类提示现网一律不可见。
//
// 本组用例守四件事：
//
//	① 补列幂等且列真在库里；
//	② 写一次 → 详情/列表/按单号三条回读路径都能拿到同一份值（SELECT/Scan 脱节即红）；
//	③ 编码口径（去空白/去重/排序）与解码口径（空值/坏数据回非 nil 空切片）稳定；
//	④ 重跑覆盖而非累加（降级已恢复的工单不能继续挂着旧提示）。
//
// ============================================================================
package store

import (
	"reflect"
	"testing"

	"translator/internal/db"
)

// TestTicketDeliveryNotesDefaultEmpty 新工单两列为空串，解码必须是非 nil 空切片（前端读 .length）。
func TestTicketDeliveryNotesDefaultEmpty(t *testing.T) {
	s := newTestStoreWithTenants(t)
	tk, err := s.CreateTicket(1, 1, "注记默认值", "你好", "", "en")
	if err != nil {
		t.Fatalf("建单失败: %v", err)
	}
	got, err := s.GetTicket(tk.ID, 1)
	if err != nil {
		t.Fatalf("GetTicket 失败: %v", err)
	}
	if got.DegradedLangsJSON != "" || got.GateWarningsJSON != "" {
		t.Fatalf("新工单注记列应为空串，实际 degraded=%q gate=%q", got.DegradedLangsJSON, got.GateWarningsJSON)
	}
	if l := DecodeTicketNotes(got.DegradedLangsJSON); l == nil || len(l) != 0 {
		t.Fatalf("空注记必须解码成非 nil 空切片（否则出参是 null），实际 %#v", l)
	}
}

// TestTicketDeliveryNotesRoundTrip 写一次 → 三条回读路径（详情/列表/按单号）值一致。
func TestTicketDeliveryNotesRoundTrip(t *testing.T) {
	s := newTestStoreWithTenants(t)
	tk, err := s.CreateTicket(1, 1, "注记往返", "你好", "", "en")
	if err != nil {
		t.Fatalf("建单失败: %v", err)
	}
	wantDegraded := []string{"ja", "en"}
	wantWarnings := []string{"refusal_blocked=2（en 的译文里模型返回拒译话术，已保留原文交付，请人工补译）"}
	if err := s.SetTicketDeliveryNotes(tk.ID, wantDegraded, wantWarnings); err != nil {
		t.Fatalf("SetTicketDeliveryNotes 失败: %v", err)
	}

	paths := map[string]*Ticket{}
	if got, err := s.GetTicket(tk.ID, 1); err == nil {
		paths["GetTicket"] = got
	} else {
		t.Fatalf("GetTicket 失败: %v", err)
	}
	if got, err := s.GetTicketGlobal(tk.ID); err == nil {
		paths["GetTicketGlobal"] = got
	} else {
		t.Fatalf("GetTicketGlobal 失败: %v", err)
	}
	if got, err := s.GetTicketByNo(tk.TicketNo); err == nil {
		paths["GetTicketByNo"] = got
	} else {
		t.Fatalf("GetTicketByNo 失败: %v", err)
	}
	list, err := s.ListTickets(1, 1, true)
	if err != nil {
		t.Fatalf("ListTickets 失败: %v", err)
	}
	for _, x := range list {
		if x.ID == tk.ID {
			paths["ListTickets"] = x
		}
	}
	if len(paths) != 4 {
		t.Fatalf("回读路径缺失，实际拿到 %d 条: %v", len(paths), paths)
	}

	for name, got := range paths {
		if d := DecodeTicketNotes(got.DegradedLangsJSON); !reflect.DeepEqual(d, []string{"en", "ja"}) {
			t.Fatalf("%s 降级语言应为排序去重后的 [en ja]，实际 %#v（raw=%q）", name, d, got.DegradedLangsJSON)
		}
		if w := DecodeTicketNotes(got.GateWarningsJSON); !reflect.DeepEqual(w, wantWarnings) {
			t.Fatalf("%s 闸门告警应原样回读，实际 %#v（raw=%q）", name, w, got.GateWarningsJSON)
		}
	}
}

// TestTicketDeliveryNotesOverwrite 重跑覆盖：新一次交付没有降级时，旧提示必须被清掉。
func TestTicketDeliveryNotesOverwrite(t *testing.T) {
	s := newTestStoreWithTenants(t)
	tk, _ := s.CreateTicket(1, 1, "注记覆盖", "你好", "", "en")
	if err := s.SetTicketDeliveryNotes(tk.ID, []string{"en"}, []string{"degraded_files=1"}); err != nil {
		t.Fatalf("首次写入失败: %v", err)
	}
	if err := s.SetTicketDeliveryNotes(tk.ID, nil, nil); err != nil {
		t.Fatalf("覆盖写入失败: %v", err)
	}
	got, err := s.GetTicket(tk.ID, 1)
	if err != nil {
		t.Fatalf("GetTicket 失败: %v", err)
	}
	if got.DegradedLangsJSON != "" || got.GateWarningsJSON != "" {
		t.Fatalf("重跑后旧注记应被清空，实际 degraded=%q gate=%q", got.DegradedLangsJSON, got.GateWarningsJSON)
	}
}

// TestTicketNotesEncodeDecode 编码口径：空白项丢弃、重复去重、排序稳定；解码对坏数据 fail-soft。
func TestTicketNotesEncodeDecode(t *testing.T) {
	if got := EncodeTicketNotes(nil); got != "" {
		t.Fatalf("空清单应存空串，实际 %q", got)
	}
	if got := EncodeTicketNotes([]string{" ", "", "ja", "en", "ja"}); got != `["en","ja"]` {
		t.Fatalf("编码应去空白/去重/排序，实际 %q", got)
	}
	cases := []struct{ raw string }{
		{""},
		{"not json"},
		{"null"},
		{"[1,2]"},
	}
	for _, c := range cases {
		if got := DecodeTicketNotes(c.raw); got == nil {
			t.Fatalf("坏数据 %q 必须回非 nil 空切片，实际 nil", c.raw)
		}
	}
	// 兼容逗号分隔的历史/手写形态
	if got := DecodeTicketNotes("en, ja"); !reflect.DeepEqual(got, []string{"en", "ja"}) {
		t.Fatalf("逗号分隔形态应解成 [en ja]，实际 %#v", got)
	}
}

// TestTicketDeliveryNotesMigrateIdempotentAndColumnsExist 补列幂等 + 两列真落在 schema 里。
func TestTicketDeliveryNotesMigrateIdempotentAndColumnsExist(t *testing.T) {
	s := newTestStoreWithTenants(t)
	for i := 0; i < 3; i++ {
		s.TicketDeliveryNotesMigrate() // 反复启动不应报错
	}
	d := db.CurrentDialect()
	rows, err := db.Query(s.db, d, "PRAGMA table_info(tickets)")
	if err != nil {
		t.Skipf("非 SQLite 方言跳过列枚举: %v", err)
	}
	defer rows.Close()
	have := map[string]bool{}
	for rows.Next() {
		var cid, notnull, pk int
		var name, ctype string
		var dflt interface{}
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			continue
		}
		have[name] = true
	}
	for _, col := range []string{"degraded_langs", "gate_warnings"} {
		if !have[col] {
			t.Fatalf("tickets 缺少交付注记列 %s（补列与 SELECT/Scan 脱节会静默丢掉提示）", col)
		}
	}
}
