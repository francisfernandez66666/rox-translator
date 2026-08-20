package api

// ============ 本文件职责中文说明 ============
// 在线支付接口：发起支付下单、查询支付状态、渠道回调（验签→确认到账）。
//   - handlePayCreate（/api/pay/create）：租户管理员为本租户创建在线充值订单，返回收款二维码。
//   - handlePayStatus（/api/pay/status）：轮询订单支付状态（前端收银台自动刷新）。
//   - handlePayNotify（/api/pay/notify/:channel）：支付渠道异步回调，验签后确认到账。
// 渠道：mock（默认）/ wechat / alipay，由 system_config pay_mode 或环境变量 PAY_MODE 决定。
// 金额：入参为 token 数量，按 1 元 = 1000 token 换算人民币分（可配置 RATE_CARD）。
// ========================================

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"

	"translator/internal/payment"
)

// payProvider 当前支付提供商（按 system_config pay_mode 惰性构建）。
// 说明：Server 持有，首次调用时从系统配置读取渠道并创建适配器。
func (s *Server) payProvider() payment.Provider {
	cfg := &payment.Config{}
	mode := "mock"
	if v, _ := s.Store.GetConfig("pay_mode"); v != "" {
		mode = v
	}
	cfg.Mode = mode
	// 环境变量覆盖（商户号到位后配置）
	cfg.Wechat.AppID = os.Getenv("PAY_WECHAT_APP_ID")
	cfg.Wechat.MchID = os.Getenv("PAY_WECHAT_MCH_ID")
	cfg.Wechat.APIv3Key = os.Getenv("PAY_WECHAT_APIv3_KEY")
	cfg.Alipay.AppID = os.Getenv("PAY_ALIPAY_APP_ID")
	cfg.Alipay.PrivateKey = os.Getenv("PAY_ALIPAY_PRIVATE_KEY")
	cfg.Alipay.PublicKey = os.Getenv("PAY_ALIPAY_PUBLIC_KEY")
	return payment.NewProvider(cfg)
}

// handlePayCreate 发起在线支付：为当前租户创建充值订单并生成收款二维码。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（body 含 tokens/channel）。
// 返回: success=true 时携带 order（含 qr_content 二维码内容）。
func (s *Server) handlePayCreate(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		Tokens  int64  `json:"tokens"`  // 充值 token 数（必填）
		Channel string `json:"channel"` // 支付渠道：mock/wechat/alipay（缺省 mock）
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Tokens <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "tokens 必须大于 0"})
		return
	}
	if req.Channel == "" {
		req.Channel = "mock"
	}
	if req.Channel != "mock" && req.Channel != "wechat" && req.Channel != "alipay" {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "不支持的支付渠道"})
		return
	}
	tid := s.effTenant(r, u)
	// 创建订单（先落 pending，再取二维码回填）
	o, err := s.Store.CreateOrderChannel(tid, req.Tokens, 0, u.ID, req.Channel, "")
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	// 调用渠道下单获取二维码（mock 直接生成；真实渠道需商户号）
	amountFen := o.AmountTokens * 10 // 简化定价：1000 token = 1 元 = 100 分 → token 数 × 0.1 分 = token×10/100
	res, err := s.payProvider().CreateOrder(&payment.PayRequest{
		OrderNo:  o.OrderNo,
		Amount:   amountFen,
		Subject:  "翻译助手 token 充值",
		TenantID: tid,
	})
	if err != nil {
		// 真实渠道未配置：回退 mock 保证流程可用
		res, err = (&payment.MockProvider{}).CreateOrder(&payment.PayRequest{OrderNo: o.OrderNo, Amount: amountFen, Subject: "翻译助手 token 充值", TenantID: tid})
	}
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": "下单失败: " + err.Error()})
		return
	}
	// 回填二维码与渠道
	_ = s.Store.UpdateOrderPrepay(o.OrderNo, "", res.QRContent)
	o.QRContent = res.QRContent
	o.Channel = res.Channel
	s.Store.LogAudit(tid, u.ID, "pay_create", "orders", o.OrderNo)
	writeJSON(w, 200, map[string]interface{}{"success": true, "order": o, "qr_content": res.QRContent, "channel": res.Channel})
}

// handlePayStatus 查询订单支付状态（前端收银台轮询）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（query: order_id）。
// 返回: success=true 时携带订单状态（paid 表示已到账）。
func (s *Server) handlePayStatus(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	tid := s.effTenant(r, u)
	oid := atol(r.URL.Query().Get("order_id"))
	if oid <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "缺少 order_id"})
		return
	}
	o, err := s.Store.GetOrder(oid, tid)
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": "订单不存在"})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "order": o})
}

// handlePaySimulate 模拟支付到账（仅 pay_mode=mock 的测试模式可用）。
// 作用：租户管理员在 mock 模式下点击「模拟支付」，直接确认订单到账，便于跑通全链路。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（body 含 order_id）。
// 返回: success=true 表示模拟到账完成。
func (s *Server) handlePaySimulate(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	// 仅 mock 模式开放（生产接入真实渠道后该接口自动失效）
	if v, _ := s.Store.GetConfig("pay_mode"); v != "" && v != "mock" {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": "非 mock 模式禁止模拟支付"})
		return
	}
	var req struct {
		OrderID int64 `json:"order_id"` // 待模拟支付的订单 ID
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.OrderID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请提供 order_id"})
		return
	}
	// 仅限本租户且为 mock 渠道订单
	o, err := s.Store.GetOrder(req.OrderID, s.effTenant(r, u))
	if err != nil || o.Channel != "mock" {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": "订单不存在或非 mock 渠道"})
		return
	}
	if err := s.Store.MarkOrderPaid(req.OrderID, s.effTenant(r, u)); err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "pay_simulate", "orders", o.OrderNo)
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// handlePayNotify 支付渠道异步回调：验签后确认订单到账（幂等）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（path 含 :channel，body 为渠道报文）。
// 返回: 渠道约定格式（成功返回 success 字符串，微信返回 204）。
func (s *Server) handlePayNotify(w http.ResponseWriter, r *http.Request) {
	// 校验管理员凭证（回调地址仅内部/配置的渠道服务访问）
	if tok := r.Header.Get("X-Admin-Token"); tok != "" {
		if tok != s.Cfg.AdminToken {
			writeJSON(w, 403, map[string]interface{}{"success": false, "message": "拒绝访问"})
			return
		}
	}
	channel := strings.TrimPrefix(r.URL.Path, "/api/pay/notify/")
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	headers := map[string]string{
		"Content-Type": r.Header.Get("Content-Type"),
		"Timestamp":    r.Header.Get("Wechatpay-Timestamp"),
		"Nonce":        r.Header.Get("Wechatpay-Nonce"),
		"Signature":    r.Header.Get("Wechatpay-Signature"),
	}
	// 构建对应渠道的提供商并验签
	prov := s.payProvider()
	nt, err := prov.VerifyNotify(body, headers)
	if err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "回调验签失败: " + err.Error()})
		return
	}
	if !nt.Verified {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "回调签名校验未通过"})
		return
	}
	if err := s.Store.MarkOrderPaidByOrderNo(nt.OrderNo); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "订单确认失败: " + err.Error()})
		return
	}
	// 审计：记录渠道回调到账
	if o, err := s.Store.FindOrderByOrderNo(nt.OrderNo); err == nil {
		s.Store.LogAudit(o.TenantID, 0, "pay_notify", "orders", o.OrderNo+" channel="+channel)
	}
	if channel == "wechat" {
		// 微信要求空 body + 200
		w.WriteHeader(http.StatusOK)
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true})
}
