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
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"translator/internal/auth"
	"translator/internal/billing"
	"translator/internal/payment"
	"translator/internal/store"
	"translator/internal/tenant"
)

// ★ C12（2026-09-12）：非超管用量「展示膨胀倍数」从代码常量（5 倍双口径）改为
//
//	system_config usage_display_factor 配置驱动，默认 1.0=关闭——
//	真实账本与响应口径分叉且响应字段无任何标识，租户据此对账必然失真；
//	若业务确需对外放大口径，超管显式配置并在响应 display_factor 字段留痕。
func (s *Server) usageDisplayFactor() float64 {
	if v, _ := s.Store.GetConfig("usage_display_factor"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 1 {
			return f
		}
	}
	return 1.0
}

// ============ 计费/充值/用量 ============

// handleBalance 余额查询（★ 双桶口径，评审整改 A1：永久余额 + 未过期台账 + 可用总额）。
// ★ 2026-09-19 积分口径：token 裸值出参下线，一律折积分（approx_sentences 保留句数估算）。
//
// ★ #42（2026-09-22 P2 技术债收尾）僵尸路由标注——**保留不删，仅打废弃信号**：
//
//	前端已无调用方（selfservice.tsx 的「我的余额」改走 /api/me/package，账单页走
//	/api/billing/my/overview，见 frontend-react/src/api/mybilling.ts:47），但
//	scripts/uat/api_uat.sh A4/A13/B2/B8、api_uat_txn.sh T14/T16/T21 仍在打本接口，
//	外部/运维脚本无法在此仓库内穷举核实；按「实装优先、禁止删除既有功能」的硬约定，
//	**删除必须先取得用户确认**，本轮只做废弃声明。
//	正式替代路径：/api/billing/my/overview（租户自服务余额+账单一体化口径）。
//
// 废弃信号口径（RFC 8594）：Deprecation 响应头 + Link 指向后继资源，
//
//	在鉴权分支之前设置，保证 403/错误响应同样带信号（表头先于 WriteHeader 写）。
func (s *Server) handleBalance(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Deprecation", "true")
	w.Header().Set("Link", `</api/billing/my/overview>; rel="successor-version"`)
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": publicErrMessage(r.Context(), err)})
		return
	}
	tid := s.effTenant(r, u)
	// ★ P3 修复：余额查询前冲刷计量缓冲，返回即时余额
	billing.Flush()
	_, err = s.Store.GetBalance(tid)
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": publicErrMessage(r.Context(), err)})
		return
	}
	grants, permanent, total, approx := s.balancePayload(tid)
	// ★ #42：响应体同为 JSON 对象且 UAT 只按 key 取值/断言子串（A4-balance-shape 判 points_available、
	//   api_uat.sh:254 等用 .get("points_available")），追加键不会破任何既有断言，故补显式废弃字段，
	//   让只看 body 的调用方也能感知迁移目标。
	writeJSON(w, 200, map[string]interface{}{
		"success": true,
		// 双桶明细（积分口径）：permanent=永久、grants=未过期台账、total=可用总额
		"points_permanent":   s.Store.PointsFromTokens(permanent),
		"points_grants_left": s.Store.PointsFromTokens(grants),
		"points_available":   s.Store.PointsFromTokens(total),
		"approx_sentences":   approx,
		"deprecated":         true,                       // ★ #42 废弃声明（配合 Deprecation 响应头）
		"replacement":        "/api/billing/my/overview", // ★ #42 正式替代路径
	})
}

// handleUsage 查询当前租户计费用量统计（调用次数 / Token 消耗 / 余额趋势等）。参数 w/r：标准 HTTP；鉴权：租户管理员及以上；按 effTenant 租户隔离；返回 usage 明细与 total 汇总。
func (s *Server) handleUsage(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": publicErrMessage(r.Context(), err)})
		return
	}
	usage, total, err := s.Store.UsageStats(s.effTenant(r, u))
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": publicErrMessage(r.Context(), err)})
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
	// ★ 非超管展示口径：系数可配（C12，默认 1=不放大）
	factor := 1.0
	if !super {
		factor = s.usageDisplayFactor()
	}
	scale := func(n int64) int64 { return int64(float64(n)*factor + 0.5) }
	// ★ P2 报表导出（2026-09-15，见《P0P2待办核实报告_20260915.md》P2-2）：
	//   ?export=csv&from=YYYY-MM-DD&to=YYYY-MM-DD —— 用量明细 CSV 流式下载（财务/对账取数）。
	//   鉴权与 JSON 口径完全一致（租户管理员+effTenant 隔离）；非超管同样脱敏供应商/模型、
	//   应用展示系数放大（与面板所见数字对齐，避免「导出比页面多」的口径争议）。
	if r.URL.Query().Get("export") == "csv" {
		tid := s.effTenant(r, u)
		from := r.URL.Query().Get("from")
		to := r.URL.Query().Get("to")
		limit := atoiDef(r.URL.Query().Get("limit"), 20000)
		if limit > 100000 {
			limit = 100000
		}
		recs, uerr := s.Store.UsageLedgerForExport(tid, from, to, limit)
		if uerr != nil {
			writeJSON(w, 200, map[string]interface{}{"success": false, "message": uerr.Error()})
			return
		}
		name := fmt.Sprintf("usage_%d_%s_%s.csv", tid, strings.ReplaceAll(from, "-", ""), strings.ReplaceAll(to, "-", ""))
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", "attachment; filename="+name)
		// UTF-8 BOM：Excel 直开不乱码（与审计导出同口径）
		w.Write([]byte{0xEF, 0xBB, 0xBF})
		fmt.Fprintf(w, "id,tenant_id,user_id,task_type,provider,model,quantity,cost_points,biz_kind,biz_mode,charge_kind,created_at\n")
		for _, row := range recs {
			p, m := row.Provider, row.Model
			qty, cost := row.Quantity, row.Cost
			if !super {
				p, m = "*", "*" // 供应商/模型脱敏（与 JSON 口径一致）
				qty, cost = scale(qty), scale(cost)
			}
			// ★ 2026-09-19 积分口径：费用列折积分出参，内部单价（token/单位）不再外发
			fmt.Fprintf(w, "%d,%d,%d,%s,%s,%s,%d,%d,%s,%s,%s,%s\n",
				row.ID, row.TenantID, row.UserID, csvEscape(row.TaskType), csvEscape(p), csvEscape(m),
				qty, s.Store.PointsFromTokens(cost), csvEscape(row.BizKind), csvEscape(row.BizMode), csvEscape(row.ChargeKind), csvEscape(row.CreatedAt))
		}
		return
	}
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
	// ★ 2026-09-19 积分口径：token 费用类数字全部折积分出参（quantity 仍为字符/句数）
	writeJSON(w, 200, map[string]interface{}{
		"success": true, "usage": s.pointsMapJSON(usage), "total": s.Store.PointsFromTokens(total),
		"display_factor": factor, "provider_usage": s.pointsMapJSON(providerUsage),
		"trend": s.pointsMapJSON(trend), "ledger": s.ledgerRowsJSON(ledger),
	})
}

// handleManualConfirmOrders 待人工确认订单列表（super_admin）：静态码支付用户点「我已付费」后待审核。
// 参数 w: HTTP 响应写入器；r: HTTP 请求。
// 返回: success=true 时携带 orders 数组（仅 manual 渠道 + manual_confirm=1 + pending）。
func (s *Server) handleManualConfirmOrders(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireAdminUser(r); err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": publicErrMessage(r.Context(), err)})
		return
	}
	orders, err := s.Store.ListManualConfirmOrders()
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": publicErrMessage(r.Context(), err)})
		return
	}
	// ★ USDT：附交易哈希线索（客户声明 + 已结算凭证）与浏览器外链，供财务「四项核对」
	type txInfo struct {
		Declared string `json:"declared"`
		Settled  string `json:"settled"`
		URL      string `json:"url"`
		Amount   string `json:"amount"` // USDT 精确应得（含尾数）
		Chain    string `json:"chain"`
	}
	info := map[string]txInfo{}
	for _, o := range orders {
		meta, merr := s.Store.GetUSDTOrderMeta(o.ID)
		if merr != nil {
			continue
		}
		item := txInfo{Declared: meta.ClientTxHash, Settled: meta.MatchedTxHash, Chain: meta.Chain,
			Amount: payment.FormatUSDTMicro(meta.AmountMicro)}
		if meta.ClientTxHash != "" {
			item.URL = payment.ExplorerURL(meta.Chain, meta.ClientTxHash)
		}
		info[fmt.Sprint(o.ID)] = item
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "orders": s.ordersViewJSON(orders), "usdt_info": info})
}

// handleOrders 查询当前租户的充值 / 订单列表（状态、金额、渠道、时间）。参数 w/r：标准 HTTP；鉴权：租户管理员及以上；按 effTenant 租户隔离；返回 orders 数组。
func (s *Server) handleOrders(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": publicErrMessage(r.Context(), err)})
		return
	}
	orders, err := s.Store.ListOrders(s.effTenant(r, u))
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": publicErrMessage(r.Context(), err)})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "orders": s.ordersViewJSON(orders)})
}

// handleOrderCreate 创建充值订单（super_admin 为任意租户 / tenant_admin 为本租户自助充值）
func (s *Server) handleOrderCreate(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": publicErrMessage(r.Context(), err)})
		return
	}
	var req struct {
		TenantID int64   `json:"tenant_id"` // 充值目标租户（0=当前生效租户）
		Points   int64   `json:"points"`    // ★ 积分口径：充值积分数（必填，>0；内部折 token 记账）
		Money    float64 `json:"money"`     // 充值金额（元，可选记录）
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Points <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "points 必须大于 0"})
		return
	}
	tokens := s.Store.TokensFromPoints(req.Points)
	if req.TenantID <= 0 {
		req.TenantID = s.effTenant(r, u)
	}
	// 租户管理员只能为自己租户提交充值申请（super_admin 可代任意租户）
	if !auth.IsSuperAdmin(u) && req.TenantID != s.effTenant(r, u) {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": "权限不足：只能为本租户充值"})
		return
	}
	o, err := s.Store.CreateOrder(req.TenantID, tokens, req.Money, u.ID)
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": publicErrMessage(r.Context(), err)})
		return
	}
	// ★ 未显式给金额时按定价回填（评审整改 B1）：发票/对账取数来源
	if req.Money <= 0 && tokens > 0 {
		money := float64(s.Store.TokensToFen(tokens)) / 100.0
		_ = s.Store.UpdateOrderMoney(o.OrderNo, money)
		o.AmountMoney = money
	}
	// ★ #75（2026-09-23）多币种报价：金额最终确定后落报价币种/汇率快照（money_cny=amount_money 双写）
	s.stampOrderQuote(r.Context(), o)
	// 自助充值即时到账模式：system_config auto_charge=1 时创建订单即确认到账（内网/测试模式）
	// ★ C18（2026-09-12）：auto_charge 确认失败不再吞错返回"成功"——
	//   旧实现用户/面板见到 success 但订单实际 pending（假到账）。
	// ★ P0-4 修复（2026-09-14）：auto_charge 仅对 super_admin 生效——该开关若在生产误开，
	//   旧实现任何租户管理员可自报任意大额 tokens 零支付即时入账（无渠道/金额校验）。
	//   租户管理员的自助订单一律保持 pending 走人工/支付渠道确认。
	if v, _ := s.Store.GetConfig("auto_charge"); v == "1" && auth.IsSuperAdmin(u) {
		if perr := s.Store.MarkOrderPaid(o.ID, req.TenantID); perr != nil {
			writeJSON(w, 200, map[string]interface{}{"success": false,
				"message": "订单已创建但自动入账失败（保留待支付，可人工确认）: " + store.DebriefDBError(perr),
				"order":   s.orderViewJSON(o)})
			return
		}
		o.Status = "paid"
	}
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "order_create", "orders", o.OrderNo)
	writeJSON(w, 200, map[string]interface{}{"success": true, "order": s.orderViewJSON(o)})
}

// handleOrderPay 确认支付（super_admin，线下转账）
func (s *Server) handleOrderPay(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireAdminUser(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": publicErrMessage(r.Context(), err)})
		return
	}
	var req struct {
		ID       int64  `json:"id"`        // 待确认支付订单 ID
		TenantID int64  `json:"tenant_id"` // 订单归属租户（0=当前生效租户）
		TxHash   string `json:"tx_hash"`   // ★ USDT（2026-09-15）：链上交易哈希（usdt 单确认必填，唯一防一笔 tx 复用到两单）
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	if req.TenantID <= 0 {
		req.TenantID = s.effTenant(r, u)
	}
	// ★ USDT：确认前核验哈希格式并预检唯一性（payments.tx_hash 唯一索引为最终防线）
	o, oerr := s.Store.GetOrder(req.ID, req.TenantID)
	if oerr == nil && o.Channel == "usdt" {
		meta, merr := s.Store.GetUSDTOrderMeta(req.ID)
		if merr != nil {
			writeJSON(w, 200, map[string]interface{}{"success": false, "message": "USDT 收款要素缺失，请先人工核对订单"})
			return
		}
		if req.TxHash == "" || !payment.ValidUSDTTxHash(meta.Chain, req.TxHash) {
			writeJSON(w, 400, map[string]interface{}{"success": false, "message": "USDT 订单确认必须提交该链合法格式的交易哈希"})
			return
		}
		if used, uerr := s.Store.PaymentTxHashUsed(req.TxHash); uerr == nil && used {
			writeJSON(w, 400, map[string]interface{}{"success": false, "message": "该交易哈希已关联其他订单（一笔链上交易只能核销一单）"})
			return
		}
	}
	if err := s.Store.MarkOrderPaid(req.ID, req.TenantID); err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": publicErrMessage(r.Context(), err)})
		return
	}
	// USDT：链上凭证落 payments.tx_hash + 结算快照（失败不回滚资金入账——critical 告警转人工补记）
	if oerr == nil && o.Channel == "usdt" {
		if perr := s.Store.SetPaymentTxHash(req.ID, req.TxHash); perr != nil {
			_ = s.Store.CreateAlert(0, "critical", "usdt_settle",
				"USDT 订单 "+o.OrderNo+" 已入账但交易哈希关联失败（可能复用/流水缺失），请人工核对: "+perr.Error())
		}
		_ = s.Store.SettleUSDTOrder(req.ID, req.TxHash)
		s.Store.LogAudit(s.effTenant(r, u), u.ID, "order_pay", "orders", o.OrderNo+" channel=usdt tx="+req.TxHash)
	} else {
		s.Store.LogAudit(s.effTenant(r, u), u.ID, "order_pay", "orders", "")
	}
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// handleOrderRefund 退款（super_admin）
func (s *Server) handleOrderRefund(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireAdminUser(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": publicErrMessage(r.Context(), err)})
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
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": publicErrMessage(r.Context(), err)})
		return
	}
	// ★ 订阅身份清理（评审整改 B3 → ★ F-37 2026-09-25 UAT 修复批重做）：
	//   旧实现「退任意 paid 包单一律清空 PackageCode」不比对身份来源，
	//   双订阅退掉非现役那一笔也会把现役身份抹掉；且只清 code 不清
	//   package_expires/subscribed_at（残留即账本观察项 10，靠 watchdog 空码豁免兜底）。
	//   新判据三段：① 被退单包 code == 当前身份 code 才动身份；② 动时三字段一并处置（收观察项 10）；
	//   ③ 若租户还有其他在期 paid 订阅，身份改挂最晚支付的那笔而非清空。
	if o, gerr := s.Store.GetOrder(req.ID, req.TenantID); gerr == nil && o.PackageID > 0 {
		if pkg, perr := s.Store.GetPackage(o.PackageID); perr == nil && pkg.PType == "paid" {
			if t, terr := s.Ten.GetByID(req.TenantID); terr == nil {
				perms := tenant.ParsePerms(t.Permissions)
				if perms.PackageCode == pkg.Code {
					if code, exp, ok, lerr := s.Store.LatestActivePaidSubscription(req.TenantID, req.ID); lerr == nil && ok {
						perms.PackageCode = code // 身份改挂剩余在期订阅（最晚支付一笔）
						perms.PackageExpires = exp
					} else {
						perms.PackageCode = ""
						perms.PackageExpires = ""
						perms.SubscribedAt = ""
					}
					pb, _ := json.Marshal(perms)
					_ = s.Ten.Update(t.ID, t.Name, t.ExpiresAt, string(pb))
				}
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
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": publicErrMessage(r.Context(), err)})
		return
	}
	inv, err := s.Store.ListInvoices(s.effTenant(r, u))
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": publicErrMessage(r.Context(), err)})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "invoices": inv})
}

// handleInvoiceCreate 为已支付订单开具发票（租户管理员，限本租户已支付订单）
func (s *Server) handleInvoiceCreate(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": publicErrMessage(r.Context(), err)})
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
		// ★ F-43（2026-09-25 UAT 修复批）：原先拼 err.Error() 把驱动原文（sql: no rows…）
		// 直出外网；store 侧已转 errTxt 可读文案，这里走 publicErrMessage 统一兜底，
		// 任何内部错误细节不再上屏（200-错误体改 4xx 归 F-21 同族统一批）。
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": publicErrMessage(r.Context(), err)})
		return
	}
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "invoice_create", "billing", inv.InvoiceNo)
	writeJSON(w, 200, map[string]interface{}{"success": true, "invoice": inv})
}

// handleInvoiceVoid ★ C16：发票作废（数据层冲红标记，作废后同单可重开）。
// 税务侧正式冲红属资质遗留项；此处保证台账状态闭环可审计。
func (s *Server) handleInvoiceVoid(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": publicErrMessage(r.Context(), err)})
		return
	}
	var req struct {
		ID int64 `json:"id"` // 发票 ID
	}
	if e := json.NewDecoder(r.Body).Decode(&req); e != nil || req.ID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请提供发票 id"})
		return
	}
	if err := s.Store.VoidInvoice(req.ID, s.effTenant(r, u)); err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": publicErrMessage(r.Context(), err)})
		return
	}
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "invoice_void", "billing", strconv.FormatInt(req.ID, 10))
	writeJSON(w, 200, map[string]interface{}{"success": true, "message": "发票已作废"})
}
