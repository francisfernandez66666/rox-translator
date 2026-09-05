// ============ admin_billing.go · 职责说明 ============
// api 包内部实现文件。
// =============================================
package api

// ============ 本文件职责中文说明 ============
// 计费/充值/用量：余额查询、用量统计、订单（充值/支付/退款）、发票开具（handleBalance / handleUsage / handleOrders / handleInvoices 系列）
// 安全要点：所有写操作均记录审计日志（LogAudit）；API Key 密钥仅明文返回一次，前端立即保存。
// ========================================

import (
	"encoding/json"
	"net/http"

	"translator/internal/auth"
	"translator/internal/billing"
	"translator/internal/store"
	"translator/internal/tenant"
)

// nonSuperDisplayFactor 非超管用量报表的展示膨胀倍数（仅展示口径，账本真实值不变）。
const nonSuperDisplayFactor = 5

// ============ 计费/充值/用量 ============

// handleBalance 余额查询（★ 双桶口径，评审整改 A1：balance 为永久余额，
// 另附 sub_grants_left 未过期台账与 total_available 可用总额）
func (s *Server) handleBalance(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	tid := s.effTenant(r, u)
	// ★ P3 修复：余额查询前冲刷计量缓冲，返回即时余额
	billing.Flush()
	b, err := s.Store.GetBalance(tid)
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	grants, _, _, _ := s.balancePayload(tid)
	writeJSON(w, 200, map[string]interface{}{
		"success": true, "balance": b,
		"sub_grants_left": grants, "total_available": b.Balance + grants,
	})
}

// handleUsage 查询当前租户计费用量统计（调用次数 / Token 消耗 / 余额趋势等）。参数 w/r：标准 HTTP；鉴权：租户管理员及以上；按 effTenant 租户隔离；返回 usage 明细与 total 汇总。
func (s *Server) handleUsage(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	usage, total, err := s.Store.UsageStats(s.effTenant(r, u))
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	// 多供应商成本核算：按 provider 拆分用量（★ 仅超管可见；非超管不暴露供应商维度）
	super := auth.IsSuperAdmin(u)
	var providerUsage map[string]int64
	if super {
		providerUsage, err = s.Store.UsageStatsByProvider(s.effTenant(r, u))
		if err != nil {
			providerUsage = map[string]int64{}
		}
	}
	// ★ 非超管展示口径：token 消耗按展示系数放大（默认 5 倍），账本真实值不变
	factor := 1.0
	if !super {
		factor = nonSuperDisplayFactor
	}
	scale := func(n int64) int64 { return int64(float64(n)*factor + 0.5) }
	// 用量趋势（最近 7 天）
	trend, err := s.Store.UsageTrend(s.effTenant(r, u), 7)
	if err != nil {
		trend = map[string]int64{}
	}
	// 用量明细（分页）
	ledger, err := s.Store.UsageLedgerList(s.effTenant(r, u), atoiDef(r.URL.Query().Get("limit"), 50), int(atol(r.URL.Query().Get("offset"))))
	if err != nil {
		ledger = []*store.UsageLedger{}
	}
	// ★ 非超管数值放大与供应商/模型脱敏（账本真实值不变，仅响应口径）
	total = scale(total)
	for k, v := range usage {
		usage[k] = scale(v)
	}
	for k, v := range trend {
		trend[k] = scale(v)
	}
	if !super {
		for _, row := range ledger {
			row.Provider = "*"
			row.Model = "*"
			row.Quantity = scale(row.Quantity)
			row.Cost = scale(row.Cost)
		}
	}
	writeJSON(w, 200, map[string]interface{}{
		"success": true, "usage": usage, "total": total,
		"provider_usage": providerUsage, "trend": trend, "ledger": ledger,
	})
}

// handleManualConfirmOrders 待人工确认订单列表（super_admin）：静态码支付用户点「我已付费」后待审核。
// 参数 w: HTTP 响应写入器；r: HTTP 请求。
// 返回: success=true 时携带 orders 数组（仅 manual 渠道 + manual_confirm=1 + pending）。
func (s *Server) handleManualConfirmOrders(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireAdminUser(r); err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	orders, err := s.Store.ListManualConfirmOrders()
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "orders": orders})
}

// handleOrders 查询当前租户的充值 / 订单列表（状态、金额、渠道、时间）。参数 w/r：标准 HTTP；鉴权：租户管理员及以上；按 effTenant 租户隔离；返回 orders 数组。
func (s *Server) handleOrders(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	orders, err := s.Store.ListOrders(s.effTenant(r, u))
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "orders": orders})
}

// handleOrderCreate 创建充值订单（super_admin 为任意租户 / tenant_admin 为本租户自助充值）
func (s *Server) handleOrderCreate(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		TenantID int64   `json:"tenant_id"` // 充值目标租户（0=当前生效租户）
		Tokens   int64   `json:"tokens"`    // 充值 token 数量（必填，>0）
		Money    float64 `json:"money"`     // 充值金额（元，可选记录）
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Tokens <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "tokens 必须大于 0"})
		return
	}
	if req.TenantID <= 0 {
		req.TenantID = s.effTenant(r, u)
	}
	// 租户管理员只能为自己租户提交充值申请（super_admin 可代任意租户）
	if !auth.IsSuperAdmin(u) && req.TenantID != s.effTenant(r, u) {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": "权限不足：只能为本租户充值"})
		return
	}
	o, err := s.Store.CreateOrder(req.TenantID, req.Tokens, req.Money, u.ID)
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	// ★ 未显式给金额时按定价回填（评审整改 B1）：发票/对账取数来源
	if req.Money <= 0 && req.Tokens > 0 {
		money := float64(req.Tokens*s.Store.PriceFenPerToken()) / 100.0
		_ = s.Store.UpdateOrderMoney(o.OrderNo, money)
		o.AmountMoney = money
	}
	// 自助充值即时到账模式：system_config auto_charge=1 时创建订单即确认到账（内网/测试模式）
	if v, _ := s.Store.GetConfig("auto_charge"); v == "1" {
		if err := s.Store.MarkOrderPaid(o.ID, req.TenantID); err == nil {
			o.Status = "paid"
		}
	}
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "order_create", "orders", o.OrderNo)
	writeJSON(w, 200, map[string]interface{}{"success": true, "order": o})
}

// handleOrderPay 确认支付（super_admin，线下转账）
func (s *Server) handleOrderPay(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireAdminUser(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		ID       int64 `json:"id"`        // 待确认支付订单 ID
		TenantID int64 `json:"tenant_id"` // 订单归属租户（0=当前生效租户）
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	if req.TenantID <= 0 {
		req.TenantID = s.effTenant(r, u)
	}
	if err := s.Store.MarkOrderPaid(req.ID, req.TenantID); err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "order_pay", "orders", "")
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// handleOrderRefund 退款（super_admin）
func (s *Server) handleOrderRefund(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireAdminUser(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		ID       int64 `json:"id"`        // 待退款订单 ID
		TenantID int64 `json:"tenant_id"` // 订单归属租户（0=当前生效租户）
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	if req.TenantID <= 0 {
		req.TenantID = s.effTenant(r, u)
	}
	if err := s.Store.RefundOrder(req.ID, req.TenantID); err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	// ★ 订阅身份清理（评审整改 B3）：paid 套餐退款后撤销 PackageCode 镜像，
	//   否则「钱退了、订阅身份还在」被到期摘除前的窗口期滥用。
	if o, gerr := s.Store.GetOrder(req.ID, req.TenantID); gerr == nil && o.PackageID > 0 {
		if pkg, perr := s.Store.GetPackage(o.PackageID); perr == nil && pkg.PType == "paid" {
			if t, terr := s.Ten.GetByID(req.TenantID); terr == nil {
				perms := tenant.ParsePerms(t.Permissions)
				perms.PackageCode = ""
				pb, _ := json.Marshal(perms)
				_ = s.Ten.Update(t.ID, t.Name, t.ExpiresAt, string(pb))
			}
		}
	}
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "order_refund", "orders", "权益已回收")
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// ============ 发票 ============

// handleInvoices 发票列表（租户管理员）
func (s *Server) handleInvoices(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	inv, err := s.Store.ListInvoices(s.effTenant(r, u))
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "invoices": inv})
}

// handleInvoiceCreate 为已支付订单开具发票（租户管理员，限本租户已支付订单）
func (s *Server) handleInvoiceCreate(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		OrderID int64  `json:"order_id"` // 已支付订单 ID（仅限本租户）
		Title   string `json:"title"`    // 发票抬头
		TaxNo   string `json:"tax_no"`   // 税号
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.OrderID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请提供订单 id"})
		return
	}
	inv, err := s.Store.CreateInvoice(s.effTenant(r, u), req.OrderID, req.Title, req.TaxNo)
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": "开票失败（需订单已支付）: " + err.Error()})
		return
	}
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "invoice_create", "billing", inv.InvoiceNo)
	writeJSON(w, 200, map[string]interface{}{"success": true, "invoice": inv})
}
