// ============ points_view_test.go · 职责说明 ============
// 对外展示口径折算助手（points_view.go）的出参契约回归测试（★ 2026-09-19 积分口径全面上线）。
// 锁定三条铁律，任何一条被改回去即红灯：
//
//	① token 裸值键（amount_tokens/tokens_billed/cost/unit_price/tokens）绝不出现在出参视图；
//	② 折算后的积分键（amount_points/points_billed/cost_points/reward_points）值正确（默认汇率 300）；
//	③ 其余业务字段原样透传，视图不做额外增删。
//
// =============================================
package api

import (
	"encoding/json"
	"strings"
	"testing"

	"translator/internal/store"
)

// newPointsViewTestServer 构造最小可用 Server（内存 SQLite，积分汇率走默认 300 口径）。
func newPointsViewTestServer(t *testing.T) *Server {
	t.Helper()
	st := newCaptchaTestServer(t) // 复用 captcha_test 的内存 Store 引导（sql.Open + store.New）
	return st
}

// TestOrderViewJSONPointsContract 订单出参：amount_tokens → amount_points（÷300），裸值删除。
func TestOrderViewJSONPointsContract(t *testing.T) {
	s := newPointsViewTestServer(t)
	m := s.orderViewJSON(&store.Order{ID: 7, OrderNo: "RO7", AmountTokens: 90000, AmountMoney: 99, Status: "paid"})
	if _, ok := m["amount_tokens"]; ok {
		t.Fatalf("出参仍含 token 裸值键 amount_tokens: %v", m)
	}
	if m["amount_points"] != int64(300) { // 折算值由服务端直接写入 map，保持 int64 原生类型
		t.Fatalf("amount_points 应为 300（90000/300），实际 %v", m["amount_points"])
	}
	if m["order_no"] != "RO7" || m["status"] != "paid" {
		t.Fatalf("其余字段应原样透传，实际 %v", m)
	}
	if s.orderViewJSON(nil)["amount_points"] != nil {
		t.Fatal("nil 订单应返回空对象")
	}
}

// TestTicketJSONPointsContract 工单出参：tokens_billed → points_billed。
func TestTicketJSONPointsContract(t *testing.T) {
	s := newPointsViewTestServer(t)
	m := s.ticketJSON(&store.Ticket{ID: 3, TicketNo: "T3", TokensBilled: 1500})
	if _, ok := m["tokens_billed"]; ok {
		t.Fatalf("出参仍含 token 裸值键 tokens_billed: %v", m)
	}
	if m["points_billed"] != int64(5) {
		t.Fatalf("points_billed 应为 5（1500/300），实际 %v", m["points_billed"])
	}
	if m["ticket_no"] != "T3" {
		t.Fatalf("工单号应透传，实际 %v", m["ticket_no"])
	}
}

// TestLedgerRowsJSONPointsContract 用量流水行：cost → cost_points，内部单价 unit_price 不再外发。
func TestLedgerRowsJSONPointsContract(t *testing.T) {
	s := newPointsViewTestServer(t)
	rows := s.ledgerRowsJSON([]*store.UsageLedger{{ID: 1, Cost: 600, UnitPrice: 30, Quantity: 20, TaskType: "translate"}})
	if len(rows) != 1 {
		t.Fatalf("应输出 1 行，实际 %d", len(rows))
	}
	r := rows[0]
	if _, ok := r["cost"]; ok {
		t.Fatalf("出参仍含 token 裸值键 cost: %v", r)
	}
	if _, ok := r["unit_price"]; ok {
		t.Fatalf("内部单价 unit_price 不应外发: %v", r)
	}
	if r["cost_points"] != int64(2) {
		t.Fatalf("cost_points 应为 2（600/300），实际 %v", r["cost_points"])
	}
	if r["quantity"] != int64(20) {
		t.Fatalf("quantity 应透传，实际 %v", r["quantity"])
	}
}

// TestReferralRecordsJSONPointsContract 邀请记录：tokens → reward_points。
func TestReferralRecordsJSONPointsContract(t *testing.T) {
	s := newPointsViewTestServer(t)
	out := s.referralRecordsJSON([]*store.ReferralRecord{{InviteeUID: 9, Tokens: 900, Type: "trial_stack", Days: 14}})
	rec := out[0]
	if _, ok := rec["tokens"]; ok {
		t.Fatalf("出参仍含 token 裸值键 tokens: %v", rec)
	}
	if rec["reward_points"] != int64(3) {
		t.Fatalf("reward_points 应为 3（900/300），实际 %v", rec["reward_points"])
	}
	if rec["days"] != int64(14) {
		t.Fatalf("days 应透传，实际 %v", rec["days"])
	}
}

// TestPointsMapJSON 分桶合计映射逐键折积分；nil 透传 nil。
func TestPointsMapJSON(t *testing.T) {
	s := newPointsViewTestServer(t)
	if s.pointsMapJSON(nil) != nil {
		t.Fatal("nil 输入应返回 nil")
	}
	got := s.pointsMapJSON(map[string]int64{"translate": 90000, "review": 45000})
	if got["translate"] != 300 || got["review"] != 150 {
		t.Fatalf("分桶折算错误: %v", got)
	}
}

// TestPointsViewsNoRawTokenAnywhere 全量视图序列化后字符串级守护：JSON 文本中零 "token" 键名。
// （覆盖「新增列直出」路径——store 结构体 json tag 若带 token 裸值，这里立刻红灯。）
func TestPointsViewsNoRawTokenAnywhere(t *testing.T) {
	s := newPointsViewTestServer(t)
	views := []interface{}{
		s.ordersViewJSON([]*store.Order{{AmountTokens: 300000}}),
		s.ticketsViewJSON([]*store.Ticket{{TokensBilled: 300000}}),
		s.ledgerRowsJSON([]*store.UsageLedger{{Cost: 300000, UnitPrice: 1}}),
		s.referralRecordsJSON([]*store.ReferralRecord{{Tokens: 300000}}),
	}
	for i, v := range views {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("视图 %d 序列化失败: %v", i, err)
		}
		if strings.Contains(strings.ToLower(string(b)), "token") {
			t.Fatalf("视图 %d 出参仍含 token 字样: %s", i, b)
		}
	}
}
