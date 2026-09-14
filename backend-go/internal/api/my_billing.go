// ============================================================================
// api/my_billing.go — 自服务账单端点（★ F8）
// 与 admin_billing（租户管理员门槛）相对：本组端点仅要求登录用户，
// 数据口径一律收敛到「本租户 + 本人」，用于个人「账单中心」页。
// ============================================================================
package api

import (
	"net/http"
	"strconv"

	"translator/internal/billing"
)

// myPage 解析 page/size 查询参数（1 起始，size 上限 100）。
func myPage(r *http.Request) (page, size int) {
	page, _ = strconv.Atoi(r.URL.Query().Get("page"))
	size, _ = strconv.Atoi(r.URL.Query().Get("size"))
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 10
	}
	return
}

// handleMyBillingOverview 账单概览：余额 + 近 30 天日用量序列（趋势图数据）。
func (s *Server) handleMyBillingOverview(w http.ResponseWriter, r *http.Request) {
	u := s.authUser(r)
	if u == nil {
		writeJSON(w, 401, map[string]interface{}{"success": false, "message": "未登录"})
		return
	}
	billing.Flush() // 冲刷计量缓冲，保证余额即时（与 handleBalance 口径一致）
	b, err := s.Store.GetBalance(u.TenantID)
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	grants, _, total, approx := s.balancePayload(u.TenantID)
	writeJSON(w, 200, map[string]interface{}{
		"success":          true,
		"balance":          b.Balance,
		"grants_left":      grants,
		"total_available":  total,
		"approx_sentences": approx,
		// ★ S1 积分制：对外展示口径（1 积分=points_tokens_rate 内部 token；前端余额/用量只显积分）
		"points_available": s.Store.PointsFromTokens(total),
		"billing_enforced": s.Bill != nil && s.Bill.Enabled(),
		"daily":            s.Store.MyDailyUsage(u.TenantID, u.ID, 30),
	})
}

// handleMyOrders 本租户订单分页（可按状态过滤，id 倒序）。
func (s *Server) handleMyOrders(w http.ResponseWriter, r *http.Request) {
	u := s.authUser(r)
	if u == nil {
		writeJSON(w, 401, map[string]interface{}{"success": false, "message": "未登录"})
		return
	}
	all, err := s.Store.ListOrders(u.TenantID)
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	status := r.URL.Query().Get("status")
	var filtered []*orderJSON
	for _, o := range all {
		if status != "" && o.Status != status {
			continue
		}
		filtered = append(filtered, &orderJSON{
			ID: o.ID, OrderNo: o.OrderNo, Tokens: o.AmountTokens, Money: o.AmountMoney,
			Status: o.Status, Channel: o.Channel, PayMethod: o.PayMethod,
			ManualConfirm: o.ManualConfirm, CreatedAt: o.CreatedAt, PaidAt: o.PaidAt,
		})
	}
	page, size := myPage(r)
	writeJSON(w, 200, map[string]interface{}{"success": true, "total": len(filtered), "page": page, "size": size, "orders": pagedSlice(filtered, page, size)})
}

// orderJSON 用户侧订单展示行（不透出 prepay/二维码等内部字段）。
type orderJSON struct {
	ID            int64   `json:"id"`
	OrderNo       string  `json:"order_no"`
	Tokens        int64   `json:"amount_tokens"`
	Money         float64 `json:"amount_money"`
	Status        string  `json:"status"`
	Channel       string  `json:"channel"`
	PayMethod     string  `json:"pay_method"`
	ManualConfirm int     `json:"manual_confirm"`
	CreatedAt     string  `json:"created_at"`
	PaidAt        string  `json:"paid_at"`
}

// pagedSlice 内存切片分页（订单/奖励/发票小结果集共用）。
func pagedSlice[T any](v []*T, page, size int) []*T {
	start := (page - 1) * size
	if start > len(v) {
		start = len(v)
	}
	end := start + size
	if end > len(v) {
		end = len(v)
	}
	return v[start:end]
}

// handleMyLedger 本人用量台账分页（biz_kind 可选过滤；SQL 层真分页）。
func (s *Server) handleMyLedger(w http.ResponseWriter, r *http.Request) {
	u := s.authUser(r)
	if u == nil {
		writeJSON(w, 401, map[string]interface{}{"success": false, "message": "未登录"})
		return
	}
	page, size := myPage(r)
	rows, total, err := s.Store.MyLedgerPage(u.TenantID, u.ID, r.URL.Query().Get("biz_kind"), size, (page-1)*size)
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "total": total, "page": page, "size": size, "rows": rows})
}

// handleMyRewards 本人邀请奖励明细分页（复用 ListReferrals ≤100 行内存分页）。
func (s *Server) handleMyRewards(w http.ResponseWriter, r *http.Request) {
	u := s.authUser(r)
	if u == nil {
		writeJSON(w, 401, map[string]interface{}{"success": false, "message": "未登录"})
		return
	}
	all := s.Store.ListReferrals(u.ID)
	page, size := myPage(r)
	writeJSON(w, 200, map[string]interface{}{"success": true, "total": len(all), "page": page, "size": size, "rewards": pagedSlice(all, page, size)})
}

// handleMyInvoices 本租户发票分页（状态由数据直出：pending/issued/cancelled）。
func (s *Server) handleMyInvoices(w http.ResponseWriter, r *http.Request) {
	u := s.authUser(r)
	if u == nil {
		writeJSON(w, 401, map[string]interface{}{"success": false, "message": "未登录"})
		return
	}
	all, err := s.Store.ListInvoices(u.TenantID)
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	page, size := myPage(r)
	writeJSON(w, 200, map[string]interface{}{"success": true, "total": len(all), "page": page, "size": size, "invoices": pagedSlice(all, page, size)})
}
