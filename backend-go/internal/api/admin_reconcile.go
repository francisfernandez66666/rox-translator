// ============ admin_reconcile.go · 职责说明 ============
// 超级管理员对账视图 API（★ F9）：orders ↔ payments 三表勾稽。
// 规则集在内存实现（数据装载见 store.ReconcileLoad）：
//
//	missing_payment   订单已支付但无 paid 流水（收款缺账）
//	status_mismatch   流水 paid/refunded 与订单状态矛盾
//	fen_mismatch      流水金额（分）与 tokens×单价 偏差 >1 分
//	refund_no_flow    订单已退款但无 refunded 冲正流水（A3 前历史数据可能误报，detail 已注）
//
// =============================================
package api

import (
	"fmt"
	"net/http"
	"strconv"
)

// ReconIssue 一条勾稽异常。
type ReconIssue struct {
	OrderID   int64  `json:"order_id"`
	OrderNo   string `json:"order_no"`
	TenantID  int64  `json:"tenant_id"`
	Rule      string `json:"rule"`
	Detail    string `json:"detail"`
	CreatedAt string `json:"created_at"`
}

// handleAdminReconcile 对账报告（仅超管）。参数 days=回看窗口（默认 30，上限 365）。
func (s *Server) handleAdminReconcile(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireAdminUser(r)
	if err != nil || u.Role != "super_admin" {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": "仅超级管理员可对账"})
		return
	}
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	orders, pays, err := s.Store.ReconcileLoad(days)
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	rate := s.Store.PriceFenPerToken()
	byOrder := map[int64][]int64{} // order_id → payments 下标
	ordByID := map[int64]*ReconOrderLite{}
	var issues []ReconIssue
	for i := range pays {
		byOrder[pays[i].OrderID] = append(byOrder[pays[i].OrderID], int64(i))
	}
	for _, o := range orders {
		ordByID[o.ID] = &ReconOrderLite{Status: o.Status, Tokens: o.AmountTokens}
	}
	// R1/R3/R4：逐订单
	for _, o := range orders {
		var paidRows, refundRows int
		for _, idx := range byOrder[o.ID] {
			p := pays[idx]
			switch p.Status {
			case "paid":
				paidRows++
				if rate > 0 && o.AmountTokens > 0 {
					want := o.AmountTokens * rate
					diff := p.AmountFen - want
					if diff < 0 {
						diff = -diff
					}
					if diff > 1 {
						issues = append(issues, ReconIssue{o.ID, o.OrderNo, o.TenantID, "fen_mismatch",
							fmt.Sprintf("fen=%d want=%d", p.AmountFen, want), o.CreatedAt})
					}
				}
			case "refunded":
				refundRows++
			}
		}
		switch o.Status {
		case "paid":
			if paidRows == 0 {
				issues = append(issues, ReconIssue{o.ID, o.OrderNo, o.TenantID, "missing_payment", "", o.CreatedAt})
			}
			if refundRows > 0 {
				issues = append(issues, ReconIssue{o.ID, o.OrderNo, o.TenantID, "status_mismatch", "paid+refunded", o.CreatedAt})
			}
		case "refunded":
			if refundRows == 0 {
				issues = append(issues, ReconIssue{o.ID, o.OrderNo, o.TenantID, "refund_no_flow", "A3 前历史退款可能无冲正行", o.CreatedAt})
			}
		case "cancelled":
			if paidRows > 0 {
				issues = append(issues, ReconIssue{o.ID, o.OrderNo, o.TenantID, "status_mismatch", "cancelled 但有支付流水", o.CreatedAt})
			}
		}
	}
	// R2：流水指向窗口外/不存在订单（孤儿流水提示，不计异常严重度）
	for _, p := range pays {
		if _, ok := ordByID[p.OrderID]; !ok {
			issues = append(issues, ReconIssue{p.OrderID, strconv.FormatInt(p.OrderID, 10), p.TenantID, "orphan_payment", p.Status, p.CreatedAt})
		}
	}
	counts := map[string]int{}
	for _, x := range issues {
		counts[x.Rule]++
	}
	writeJSON(w, 200, map[string]interface{}{
		"success": true, "days": days, "orders_count": len(orders), "payments_count": len(pays),
		"counts": counts, "issues": issues,
	})
}

// ReconOrderLite 勾稽用订单摘要。
type ReconOrderLite struct {
	Status string
	Tokens int64
}
