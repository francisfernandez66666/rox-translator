// ============ ticket_delivery_notes_view_test.go · 职责说明 ============
// ★ 2026-10-21 UAT 收口 M2：工单出参里的交付注记（degraded_langs / gate_warnings）契约。
//
// 锁三件事：
//
//	① 键名逐字是 degraded_langs / gate_warnings（前端 TS 类型与渲染判据按同名读取）；
//	② 空值出 [] 不出 null（前端直接读 .length，null 会把它打成白屏）；
//	③ 写入值经出参原样可见（写一次 → 出栈必须带该值；摘掉出栈那一行即红）。
//
// 反证见 frontend-react/src/api/ticketsDegradedLock.test.ts（三面等值，摘一面即红）
// 与 store 侧 TestTicketDeliveryNotesRoundTrip（SELECT/Scan 脱节即红）。
// =============================================
package api

import (
	"encoding/json"
	"reflect"
	"testing"

	"translator/internal/store"
)

// TestTicketJSONDeliveryNotesEmptyIsArray 空注记出 [] 不出 null，且键名逐字固定。
func TestTicketJSONDeliveryNotesEmptyIsArray(t *testing.T) {
	s := newPointsViewTestServer(t)
	m := s.ticketJSON(&store.Ticket{ID: 5, TicketNo: "T5"})
	for _, key := range []string{"degraded_langs", "gate_warnings"} {
		v, ok := m[key]
		if !ok {
			t.Fatalf("出参缺少键 %s（前端按同名读取，缺键会让降级提示永不显示）", key)
		}
		lst, ok := v.([]string)
		if !ok {
			t.Fatalf("%s 应为 []string，实际 %T", key, v)
		}
		if lst == nil || len(lst) != 0 {
			t.Fatalf("%s 空值必须是非 nil 空切片，实际 %#v", key, lst)
		}
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	var probe map[string]interface{}
	if err := json.Unmarshal(b, &probe); err != nil {
		t.Fatalf("反序列化失败: %v", err)
	}
	for _, key := range []string{"degraded_langs", "gate_warnings"} {
		if arr, ok := probe[key].([]interface{}); !ok || len(arr) != 0 {
			t.Fatalf("%s 序列化后应为 []，实际 %v", key, probe[key])
		}
	}
}

// TestTicketJSONDeliveryNotesRoundTrip 写一次 → 出栈必须带该值（反证：摘掉出栈那一行即红）。
func TestTicketJSONDeliveryNotesRoundTrip(t *testing.T) {
	s := newPointsViewTestServer(t)
	tk := &store.Ticket{
		ID:                6,
		TicketNo:          "T6",
		DegradedLangsJSON: store.EncodeTicketNotes([]string{"ja", "en"}),
		GateWarningsJSON:  store.EncodeTicketNotes([]string{"refusal_blocked=1"}),
	}
	m := s.ticketJSON(tk)
	if got := m["degraded_langs"]; !reflect.DeepEqual(got, []string{"en", "ja"}) {
		t.Fatalf("degraded_langs 应为 [en ja]，实际 %#v", got)
	}
	if got := m["gate_warnings"]; !reflect.DeepEqual(got, []string{"refusal_blocked=1"}) {
		t.Fatalf("gate_warnings 应原样出栈，实际 %#v", got)
	}
}
