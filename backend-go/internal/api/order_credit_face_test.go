// ============ order_credit_face_test.go · 职责说明 ============
// ★ 2026-11-04 体验批 §4.2「到账回验」的出栈面闸门（用户报的第 4 条：给租户注入资金不生效）。
//
// F1 置付成功 ⇒ 响应**必须**带 balance_points（到账后现读余额）与 order_points（本单积分）；
//
//	这两支是前端那句「点之前 + 本单 ≤ 之后」的原材料，缺任一支前端就只能"信"接口。
//
// F2 失败分支（订单不存在 ⇒ 404）⇒ 两个键**整条缺席**。
//
//	⚠️ 为什么判"缺席"而不是"为 0"：出 0 会被读成"这一单到账后余额是 0"，
//	那是给失败背书的读数（AGENTS §25-b 那条 points_free 同款教训）。
//
// F3 只建单那一支（handleOrderCreate）⇒ 同样不许出现这两个键：
//
//	建单成功 ≠ 到账，把余额挂在建单响应上＝替"还没进去的钱"做担保。
//
// 方言：自钉内存 SQLite（AGENTS §一·4，经 newF44Probe→pinSqliteDialect）。
// =============================================
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// postCreditGateJSON 直调一个 POST handler，返回状态码与解码体。
func postCreditGateJSON(t *testing.T, h func(http.ResponseWriter, *http.Request), tok string, body map[string]interface{}) (int, map[string]interface{}) {
	t.Helper()
	blob, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(string(blob)))
	req.Header.Set("Content-Type", "application/json")
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	rec := httptest.NewRecorder()
	h(rec, req)
	var out map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("响应不是 JSON（code=%d）: %s", rec.Code, rec.Body.String())
	}
	return rec.Code, out
}

// TestOrderPaySuccessCarriesCreditVerification F1：成功体必须带两只数。
func TestOrderPaySuccessCarriesCreditVerification(t *testing.T) {
	s, _, tok := newF44Probe(t)
	o, err := s.Store.CreateOrder(1, 40000, 0, 1)
	if err != nil {
		t.Fatalf("建订单失败: %v", err)
	}
	code, body := postCreditGateJSON(t, s.handleOrderPay, tok, map[string]interface{}{"id": o.ID, "tenant_id": 1})
	if code != http.StatusOK {
		t.Fatalf("置付应 200，实得 %d: %v", code, body)
	}
	if ok, _ := body["success"].(bool); !ok {
		t.Fatalf("成功体缺 success:true: %v", body)
	}
	bal, hasBal := body["balance_points"]
	op, hasOrder := body["order_points"]
	if !hasBal || !hasOrder {
		t.Fatalf("成功体必须带 balance_points 与 order_points（前端回验的原材料），实得键 %v", keysOf(body))
	}
	if _, isNum := bal.(float64); !isNum {
		t.Fatalf("balance_points 必须是数字，实得 %T", bal)
	}
	if orderPts, _ := op.(float64); orderPts <= 0 {
		t.Fatalf("order_points 应为本单积分（>0），实得 %v", op)
	}
}

// TestOrderPayFailureOmitsCreditKeys F2：失败分支两个键整条缺席。
func TestOrderPayFailureOmitsCreditKeys(t *testing.T) {
	s, _, tok := newF44Probe(t)
	code, body := postCreditGateJSON(t, s.handleOrderPay, tok, map[string]interface{}{"id": 999999, "tenant_id": 1})
	if code == http.StatusOK {
		t.Fatalf("不存在的订单不该回 200: %v", body)
	}
	for _, k := range []string{"balance_points", "order_points"} {
		if v, ok := body[k]; ok {
			t.Fatalf("失败分支出了 %s=%v——出 0 会被读成「到账后余额是 0」，必须整条缺席", k, v)
		}
	}
}

// TestOrderCreateDoesNotPromiseCredit F3：只建单那一支不许出现到账读数（建单成功≠到账）。
func TestOrderCreateDoesNotPromiseCredit(t *testing.T) {
	s, _, tok := newF44Probe(t)
	code, body := postCreditGateJSON(t, s.handleOrderCreate, tok, map[string]interface{}{"tenant_id": 1, "points": 100, "money": 0})
	if code != http.StatusOK {
		t.Fatalf("建单应 200，实得 %d: %v", code, body)
	}
	for _, k := range []string{"balance_points", "order_points"} {
		if _, ok := body[k]; ok {
			t.Fatalf("建单响应出现了 %s——那一支还没置付，把余额挂上去就是替还没进去的钱做担保", k)
		}
	}
	if _, ok := body["order"]; !ok {
		t.Fatalf("建单响应必须回订单对象（前端拿 id/单号走第二步），实得键 %v", keysOf(body))
	}
}

// keysOf 报错用：把响应体的键名列出来（不打印值，值里可能有客户数据）。
func keysOf(m map[string]interface{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
