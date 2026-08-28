// ============ pay.go · 职责说明 ============
// api 包内部实现文件。
// =============================================
package api

// ============ 本文件职责中文说明 ============
// 在线支付接口：发起支付下单、查询支付状态、渠道回调（验签→确认到账）、
// 静态码支付「我已付费」人工确认（通知超管审核开通）。
//   - handlePayCreate（/api/pay/create）：租户管理员为本租户创建在线充值订单，返回收款二维码。
//   - handlePayStatus（/api/pay/status）：轮询订单支付状态（前端收银台自动刷新）。
//   - handlePayNotify（/api/pay/notify/:channel）：支付渠道异步回调，验签后确认到账。
//   - handlePayManualConfirm（/api/pay/manual-confirm）：静态码支付用户扫码后点「我已付费」→
//     订单标记待人工确认 + 写入告警 + 邮件通知超管尽快查看开通。
// 渠道：mock（默认）/ wechat / alipay / static_qr（静态收款码，人工确认），
// 由 system_config pay_mode（或环境变量 PAY_MODE）决定：
//   - sdk：走 wechat/alipay 适配器（需商户号）
//   - static_qr：返回超管配置的静态收款码图片（static_qr_image），人工确认到账
//   - mock：模拟支付（测试）
// 金额：入参为 token 数量，按 1 元 = 1000 token 换算人民币分（可配置 RATE_CARD）。
// ========================================

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	"crypto/subtle"

	"translator/internal/payment"
)

// constantTimeTokenEqual 恒定时间比较两个令牌（消除时序侧信道）。任一为空直接不等。
func constantTimeTokenEqual(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// payProvider 当前支付提供商（按 system_config pay_mode 惰性构建）。
// 说明：pay_mode=sdk 映射到 wechat/alipay 适配器（需商户号）；static_qr/mock 用 mock 适配器。
// Server 持有，首次调用时从系统配置读取渠道并创建适配器。
func (s *Server) payProvider() payment.Provider {
	cfg := &payment.Config{}
	mode := "mock"
	if v, _ := s.Store.GetConfig("pay_mode"); v != "" {
		mode = v
	}
	// sdk 模式映射到具体渠道（默认微信）；static_qr 无需渠道（人工确认）
	if mode == "sdk" {
		mode = "wechat"
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
// 返回: success=true 时携带 order（含 qr_content 二维码内容）与 channel。
// 支付模式说明：
//   - pay_mode=static_qr：订单 channel=manual，二维码内容为超管配置的 static_qr_image（URL/base64）
//   - pay_mode=sdk：走 wechat/alipay 适配器下单
//   - 其余回退 mock
func (s *Server) handlePayCreate(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		Tokens  int64  `json:"tokens"`  // 充值 token 数（必填）
		Channel string `json:"channel"` // 支付渠道：mock/wechat/alipay（缺省按 pay_mode）
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Tokens <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "tokens 必须大于 0"})
		return
	}
	// 确定支付模式：优先请求指定渠道，否则按 system_config pay_mode（默认 mock）
	payMode := ""
	if v, _ := s.Store.GetConfig("pay_mode"); v != "" {
		payMode = v
	}
	if req.Channel == "" {
		switch payMode {
		case "static_qr":
			req.Channel = "manual"
		case "sdk":
			req.Channel = "wechat"
		default:
			req.Channel = "mock"
		}
	}
	if req.Channel != "mock" && req.Channel != "wechat" && req.Channel != "alipay" && req.Channel != "manual" {
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
	// ★ 应收金额落库（评审整改 B1）：amount_money=token 数×定价（元）——
	//   此前恒 0，导致回调核对无单一事实源、发票开出 0 元单。
	money := float64(req.Tokens*s.Store.PriceFenPerToken()) / 100.0
	_ = s.Store.UpdateOrderMoney(o.OrderNo, money)
	o.AmountMoney = money
	// 静态码模式：返回超管配置的静态收款码图片（不调用渠道）
	if req.Channel == "manual" {
		qrContent := ""
		if v, _ := s.Store.GetConfig("static_qr_image"); v != "" {
			qrContent = v
		}
		if qrContent == "" {
			writeJSON(w, 200, map[string]interface{}{"success": false, "message": "静态收款码未配置，请联系管理员"})
			return
		}
		_ = s.Store.UpdateOrderPrepay(o.OrderNo, "", qrContent)
		o.QRContent = qrContent
		o.Channel = "manual"
		s.Store.LogAudit(tid, u.ID, "pay_create", "orders", o.OrderNo+" channel=manual")
		writeJSON(w, 200, map[string]interface{}{"success": true, "order": o, "qr_content": qrContent, "channel": "manual", "manual_confirm": true})
		return
	}
	// 调用渠道下单获取二维码（mock 直接生成；真实渠道需商户号）
	// 定价单一事实源：应收分 = 订单落库 amount_money×100（B1 回填值）
	amountFen := int64(money*100 + 0.5)
	res, err := s.payProvider().CreateOrder(&payment.PayRequest{
		OrderNo:  o.OrderNo,
		Amount:   amountFen,
		Subject:  "能言 token 充值",
		TenantID: tid,
	})
	if err != nil {
		// 真实渠道未配置：回退 mock 保证流程可用
		res, err = (&payment.MockProvider{}).CreateOrder(&payment.PayRequest{OrderNo: o.OrderNo, Amount: amountFen, Subject: "能言 token 充值", TenantID: tid})
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
	// 仅 mock 模式开放（★ 整改 A6：pay_mode 未显式配置为 mock 时一律拒绝——
	// 此前「非空且≠mock 才拦」的写法让全新部署（空配置）处于可模拟充值状态）
	if v, _ := s.Store.GetConfig("pay_mode"); v != "mock" {
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

// handlePayManualConfirm 静态码支付「我已付费」人工确认接口。
// 流程：用户扫码付款后点击「我已付费」→ 订单标记 manual_confirm=1（待超管审核）
//
//	→ 写入 critical 告警 + 邮件通知超管（复用 notifyAlert 机制，尽快查看并开通）。
//
// 参数 w: HTTP 响应写入器；r: HTTP 请求（body 含 order_id）。
// 返回: success=true 表示已通知超管审核。
func (s *Server) handlePayManualConfirm(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		OrderID int64 `json:"order_id"` // 待人工确认的静态码订单 ID
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.OrderID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请提供 order_id"})
		return
	}
	tid := s.effTenant(r, u)
	if err := s.Store.MarkOrderManualConfirm(req.OrderID, tid); err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	o, _ := s.Store.GetOrder(req.OrderID, tid)
	// 写入 critical 级告警（前台告警面板 + 超管可见）
	msg := "静态码支付待人工确认：租户 #" + strconv.FormatInt(tid, 10) + " 订单 " + o.OrderNo + " 用户已付款，请尽快查看并开通"
	_ = s.Store.CreateAlert(0, "critical", "pay_manual", msg)
	// 邮件通知超管（复用告警邮件收件人 alert_email）
	s.notifyAlert("静态码支付待人工确认（请尽快开通）", msg)
	s.Store.LogAudit(tid, u.ID, "pay_manual_confirm", "orders", o.OrderNo)
	writeJSON(w, 200, map[string]interface{}{"success": true, "message": "已通知管理员审核开通，请等待到账"})
}

// handlePayNotify 支付渠道异步回调：验签后确认订单到账（幂等）。
//
// ★ 安全止血（2026-08-26 P0-2，三层防御，商户号官方验签落地前的兜底闸门）：
//
//	① 凭证必填——X-Admin-Token 头缺失或不符合 AdminToken 一律 403。
//	   此前「带头才校验」等于匿名可确认任意订单（免费充值漏洞）。
//	   渠道服务器无法携带该头的问题由反代注入解决（见部署指南 Caddy 片段），
//	   后续接入微信/支付宝官方验签后可移除本要求；
//	② 渠道一致性——回调 channel 必须与订单落库 channel 匹配；manual 单只认人工确认流程；
//	③ mock 封禁——pay_mode≠mock 时 mock 渠道回调直接拒绝（生产防呆）。
//
// 参数 w: HTTP 响应写入器；r: HTTP 请求（path 含 :channel，body 为渠道报文）。
// 返回: 渠道约定格式（成功返回 success 字符串，微信返回 204）。
func (s *Server) handlePayNotify(w http.ResponseWriter, r *http.Request) {
	channel := strings.TrimPrefix(r.URL.Path, "/api/pay/notify/")
	// ① 凭证：mock 渠道为「管理员模拟确认」，必须带超管令牌（恒定时间比较，防时序侧信道）；
	//    wechat/alipay 真实渠道不依赖共享令牌，由渠道签名验签（VerifyNotify）保障，
	//    故无需 X-Admin-Token——把超管令牌暴露给第三方支付服务器反而是更大风险。
	if channel == "mock" {
		tok := r.Header.Get("X-Admin-Token")
		if tok == "" || !constantTimeTokenEqual(tok, s.Cfg.AdminToken) {
			writeJSON(w, 403, map[string]interface{}{"success": false, "message": "拒绝访问"})
			return
		}
	}
	// ③ mock 封禁（★ 整改 A6：与 handlePaySimulate 同口径收紧——pay_mode 未显式
	//    配置为 mock 时，mock 渠道回调一律拒绝，堵住「空配置=可模拟充值」的默认放行）
	payMode, _ := s.Store.GetConfig("pay_mode")
	if channel == "mock" && payMode != "mock" {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": "当前支付模式下禁止 mock 回调"})
		return
	}
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
	// ② 渠道一致性 + 金额一致性预检：先查单核对，再确认到账
	o, oerr := s.Store.FindOrderByOrderNo(nt.OrderNo)
	if oerr != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "订单不存在"})
		return
	}
	// 渠道匹配：mock 单只接受 mock 回调；wechat/alipay 单只接受同渠道回调；
	// manual（静态码人工确认）单不走任何回调通道，只能由超管在后台核实开通
	expectChannel := channel
	if channel == "sdk" { // sdk 为 wechat/alipay 的历史别名
		expectChannel = "wechat"
	}
	if o.Channel != expectChannel {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "回调渠道与订单渠道不符"})
		return
	}
	// 金额一致：应收分 = 订单落库金额×100（B1 单一事实源）；历史未回填单兜底 tokens×定价。
	// 回调金额为 0 或不一致即拒绝
	expectFen := int64(o.AmountMoney*100 + 0.5)
	if expectFen <= 0 {
		expectFen = o.AmountTokens * s.Store.PriceFenPerToken()
	}
	if nt.Amount <= 0 || nt.Amount != expectFen {
		writeJSON(w, 400, map[string]interface{}{"success": false,
			"message": fmt.Sprintf("回调金额不符：期望 %d 分，实收 %d 分", expectFen, nt.Amount)})
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
