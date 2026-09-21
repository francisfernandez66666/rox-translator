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
//   - sdk：走 wechat/alipay 适配器（★ #41 2026-09-21 已实装真实下单协议，需商户资质；
//     资质不全时适配器 fail-closed 拒绝出单，本口明确报错不回退 mock）
//   - static_qr：返回超管配置的静态收款码图片（static_qr_image），人工确认到账
//   - mock：模拟支付（测试）
// 金额：入参为 token 数量，按 system_config price_fen_per_million_tokens（★ S1 口径修复：
//
//	分/百万 token，缺省 29900＝¥299/百万，与充值包尺子价一致）换算人民币分。
//	旧键 price_fen_per_token（分/token、实值 10）为按次时代遗留，已废弃不再读取。
// ========================================

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"

	"crypto/subtle"

	"translator/internal/payment"
	"translator/internal/store"
)

// constantTimeTokenEqual 恒定时间比较两个令牌（消除时序侧信道）。任一为空直接不等。
func constantTimeTokenEqual(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// payProviderFor ★ A5（2026-09-12）：按「订单/回调渠道」构建支付提供商。
// 旧实现只按全局 pay_mode 构建单一 provider——/api/pay/notify/alipay 回调也交给
// wechat 适配器验签，alipay 渠道事实上不可用；sdk 历史别名映射 wechat。
// channel 为空时回退全局模式（兼容旧调用）。
func (s *Server) payProviderFor(channel string) payment.Provider {
	cfg := &payment.Config{}
	mode := channel
	if mode == "" {
		mode = s.effPayMode(0)
		if v, _ := s.Store.GetConfig("pay_mode"); v != "" {
			mode = v
		}
	}
	if mode == "sdk" {
		mode = "wechat"
	}
	cfg.Mode = mode
	// 环境变量覆盖（商户号到位后配置，全部为 fail-closed 必备资质，缺项见 payment/gateway_sdk.go 文件头）
	cfg.NotifyBase = os.Getenv("PAY_NOTIFY_BASE")
	cfg.Wechat.AppID = os.Getenv("PAY_WECHAT_APP_ID")
	cfg.Wechat.MchID = os.Getenv("PAY_WECHAT_MCH_ID")
	cfg.Wechat.APIv3Key = os.Getenv("PAY_WECHAT_APIv3_KEY")
	cfg.Wechat.SerialNo = os.Getenv("PAY_WECHAT_SERIAL_NO")
	cfg.Wechat.PrivateKey = os.Getenv("PAY_WECHAT_PRIVATE_KEY")
	cfg.Wechat.NotifyURL = os.Getenv("PAY_WECHAT_NOTIFY_URL")
	cfg.Alipay.AppID = os.Getenv("PAY_ALIPAY_APP_ID")
	cfg.Alipay.PrivateKey = os.Getenv("PAY_ALIPAY_PRIVATE_KEY")
	cfg.Alipay.PublicKey = os.Getenv("PAY_ALIPAY_PUBLIC_KEY")
	cfg.Alipay.SellerID = os.Getenv("PAY_ALIPAY_SELLER_ID")
	cfg.Alipay.Gateway = os.Getenv("PAY_ALIPAY_GATEWAY")
	cfg.Alipay.NotifyURL = os.Getenv("PAY_ALIPAY_NOTIFY_URL")
	return payment.NewProvider(cfg)
}

// payQRExpireMinutes ★ #41：渠道收款码有效期（分钟），与本地 pending 单超时同读
// order_pending_timeout_min（缺省 15 分钟，见 store.CloseStalePendingOrders）。
// 上限 1440 防误配成「隔天还能扫」的长窗。
func (s *Server) payQRExpireMinutes() int {
	minutes := 15
	if v, _ := s.Store.GetConfig("order_pending_timeout_min"); v != "" {
		if x, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && x > 0 && x <= 1440 {
			minutes = x
		}
	}
	return minutes
}

// handlePayCreate 发起在线支付：为当前租户创建充值订单并生成收款二维码。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（body 含 points/channel，points 为积分数）。
// 返回: success=true 时携带 order（amount_points 积分口径，含 qr_content 二维码内容）与 channel。
// 支付模式说明：
//   - pay_mode=static_qr：订单 channel=manual，二维码内容为超管配置的 static_qr_image（URL/base64）
//   - pay_mode=sdk：走 wechat/alipay 适配器下单
//   - 其余回退 mock
func (s *Server) handlePayCreate(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": publicErrMessage(r.Context(), err)})
		return
	}
	var req struct {
		Points    int64  `json:"points"`     // ★ 积分口径唯一入参：充值积分数（内部折算 token 落库）
		Channel   string `json:"channel"`    // 支付渠道：mock/wechat/alipay/usdt（缺省按 pay_mode）
		USDTChain string `json:"usdt_chain"` // ★ USDT：指定链 trc20/erc20/bep20（缺省取配置首链）
		Coupon    string `json:"coupon"`     // ★ 优惠券（#41）：券码，空=不用券
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Points <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "points 必须大于 0"})
		return
	}
	{
		// ★ P1-15 修复（2026-09-14）：积分上限防 int64 溢出——旧实现 points×rate 无上限，
		//   超大值可溢出为负 tokens 落库成负金额订单（脏数据污染对账链）。
		const maxPoints = int64(1) << 40 // ≈1.1 万亿积分，远超任何真实充值
		if req.Points > maxPoints || req.Points > (math.MaxInt64-1)/int64(s.Store.PointsTokensRate()) {
			writeJSON(w, 400, map[string]interface{}{"success": false, "message": "points 超出允许范围"})
			return
		}
	}
	tokens := s.Store.TokensFromPoints(req.Points)
	tid := s.effTenant(r, u)
	// 确定支付模式：优先请求指定渠道，否则按最终运营策略 payment.mode（默认 mock）。
	// ★ 2026-09：支付模式收敛到运营策略引擎（payment.mode），存量 system_config pay_mode
	//   经 applyLegacyConfig 并入最终策略，二者统一由 effPayMode 输出。
	payMode := s.effPayMode(tid)
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
	if req.Channel != "mock" && req.Channel != "wechat" && req.Channel != "alipay" && req.Channel != "manual" && req.Channel != "usdt" {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "不支持的支付渠道"})
		return
	}
	// 创建订单（先落 pending，再取二维码回填）
	o, err := s.Store.CreateOrderChannel(tid, tokens, 0, u.ID, req.Channel, "")
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": publicErrMessage(r.Context(), err)})
		return
	}
	// ★ 应收金额落库（评审整改 B1）：amount_money=token 数×定价（元）——
	//   此前恒 0，导致回调核对无单一事实源、发票开出 0 元单。
	money := float64(s.Store.TokensToFen(tokens)) / 100.0
	_ = s.Store.UpdateOrderMoney(o.OrderNo, money)
	o.AmountMoney = money
	// ★ 优惠券（#41 商业洞三，2026-09-21）：券在「建单之后、渠道出码之前」核销。
	//   ApplyCouponToOrder 把 orders.amount_money 直接改写为折后实付，于是回调金额核对、
	//   退款、开票三条链路的「应收单一事实源」自动对齐，无需任何专门的折扣分支。
	//   核销失败时订单留 pending（由 order_pending_timeout_min 超时收敛）并当场回错——
	//   绝不在券已失效的状态下继续给出可扫的收款码。
	if code := store.NormalizeCouponCode(req.Coupon); code != "" {
		paid, disc, cerr := s.couponApply(o.ID, tid, code, store.CouponKindRecharge, money)
		if cerr != nil {
			s.replyCouponFailure(w, r, tid, u.ID, o.OrderNo, code, cerr)
			return
		}
		money, o.AmountMoney = paid, paid
		s.Store.LogAudit(tid, u.ID, "coupon_redeem", "orders",
			o.OrderNo+" code="+code+" discount="+strconv.FormatFloat(disc, 'f', 2, 64))
	}
	// ★ USDT 收款（2026-09-15）：独立分支——链上无回调，出收款要素快照（地址+含尾数精确金额+
	//   汇率快照+24h 窗口），到账走「人工核销（M1）」或「reconciler 自动对账（M2，默认关）」。
	if req.Channel == "usdt" {
		s.handlePayCreateUSDT(w, r, u, tid, o, money, req.USDTChain)
		return
	}
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
		writeJSON(w, 200, map[string]interface{}{"success": true, "order": s.orderViewJSON(o), "qr_content": qrContent, "channel": "manual", "manual_confirm": true})
		return
	}
	// 调用渠道下单获取二维码（mock 直接生成；真实渠道需商户号）
	// 定价单一事实源：应收分 = 订单落库 amount_money×100（B1 回填值；有券则为折后实付）
	qrContent, channel, err := s.payChannelQR(o, money, "能言积分充值")
	if err != nil {
		// ★ 显式失败（2026-09-16 整改）：旧实现静默回退 mock 二维码并把订单渠道改写为
		//   mock——pay_mode=sdk 但商户配置缺失时，用户扫到 mockpay:// 废码，且 mock 回调
		//   在非 mock 模式下被拒（handlePayNotify 三道闸），订单只能挂 pending 等超时。
		//   宁可当场报错让用户/运维感知渠道未就绪，不给出不可支付的收款页。
		// ★ #41 + #37：资质缺失提示原样透传（运维照做即可开启收款）；网络/协议类只回通用文案，
		//   细节（url.Error、DNS、证书、渠道原文）只进日志，不吐给客户端。
		log.Printf("[pay] 渠道 %s 下单失败（订单 %s 保持 pending 待人工处理）: %v", o.Channel, o.OrderNo, err)
		writeJSON(w, 200, map[string]interface{}{"success": false,
			"message":  payChannelQRErrorMessage(channel, err),
			"order_no": o.OrderNo})
		return
	}
	s.Store.LogAudit(tid, u.ID, "pay_create", "orders", o.OrderNo)
	writeJSON(w, 200, map[string]interface{}{"success": true, "order": s.orderViewJSON(o), "qr_content": qrContent, "channel": channel})
}

// payChannelQR ★ #41（2026-09-21）：向渠道下单取收款码并回填订单——pay/create 与
// package/subscribe、package/upgrade 三条链路共用同一取码口径与失败文案。
// 抽出来不只是去重：此前订阅链路在 pay_mode=sdk 下只落 pending 单、不调渠道，
// 弹窗拿不到二维码，等于「有价格没收款」。
// 参数 o=已落库的 pending 单（用 o.Channel 选适配器）；money=应收（元，券折让后的实付）；
// subject=渠道侧订单标题。返回 (二维码内容, 实际渠道, 错误)；错误由调用方经
// payChannelQRErrorMessage → payPublicHint 收敛对外文案。
func (s *Server) payChannelQR(o *store.Order, money float64, subject string) (string, string, error) {
	res, err := s.payProviderFor(o.Channel).CreateOrder(&payment.PayRequest{
		OrderNo:  o.OrderNo,
		Amount:   int64(money*100 + 0.5),
		Subject:  subject,
		TenantID: o.TenantID,
		// ★ #41：把收款码有效期传给渠道，与本地 pending 单超时（order_pending_timeout_min）同口径，
		//   避免渠道侧仍可支付、本地单已 cancelled 的「付了钱没到账」窗口。
		ExpireMinutes: s.payQRExpireMinutes(),
	})
	if err != nil {
		return "", o.Channel, err
	}
	_ = s.Store.UpdateOrderPrepay(o.OrderNo, "", res.QRContent)
	o.QRContent = res.QRContent
	o.Channel = res.Channel
	return res.QRContent, res.Channel, nil
}

// payChannelQRErrorMessage 渠道取码失败的统一对外文案（三处下单链路共用）。
func payChannelQRErrorMessage(channel string, err error) string {
	return "支付渠道暂不可用（" + channel + "）" + payPublicHint(err)
}

// payPublicHint 渠道下单错误的对外文案收敛（★ #41 + #37 脱敏口径）：
// 「资质未配置」是可执行的运维提示，原样给出；其余（网络、TLS、渠道原始报文）统一为通用提示，
// 原始错误由调用方写日志。返回值自带前缀冒号，可直接拼在「支付渠道暂不可用（wechat）」后。
func payPublicHint(err error) string {
	msg := err.Error()
	if strings.Contains(msg, "资质未配置") {
		return "：" + msg
	}
	return "：下单失败，请稍后重试或改用其他付款方式"
}

// handlePayStatus 查询订单支付状态（前端收银台轮询）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（query: order_id）。
// 返回: success=true 时携带订单状态（paid 表示已到账）。
func (s *Server) handlePayStatus(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": publicErrMessage(r.Context(), err)})
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
	resp := map[string]interface{}{"success": true, "order": s.orderViewJSON(o)}
	// ★ USDT：轮询回显收款要素与进度（pending 期展示地址/金额/窗口；paid 后带链上凭证）
	if p := s.usdtPayForOrder(o); p != nil {
		resp["usdt_pay"] = p
	}
	writeJSON(w, 200, resp)
}

// handlePaySimulate 模拟支付到账（仅 pay_mode=mock 的测试模式可用）。
// 作用：租户管理员在 mock 模式下点击「模拟支付」，直接确认订单到账，便于跑通全链路。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（body 含 order_id）。
// 返回: success=true 表示模拟到账完成。
func (s *Server) handlePaySimulate(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": publicErrMessage(r.Context(), err)})
		return
	}
	// 仅 mock 模式开放（★ 整改 A6 + 2026-09 运营策略：payment.mode 未显式为 mock 时一律拒绝——
	// 此前「非空且≠mock 才拦」的写法让全新部署（空配置）处于可模拟充值状态）
	if s.effPayMode(s.effTenant(r, u)) != "mock" {
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
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": publicErrMessage(r.Context(), err)})
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
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": publicErrMessage(r.Context(), err)})
		return
	}
	var req struct {
		OrderID int64  `json:"order_id"` // 待人工确认的订单 ID
		TxHash  string `json:"tx_hash"`  // ★ USDT（2026-09-15）：链上交易哈希（usdt 渠道必填，仅线索展示，以链上查证为准）
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.OrderID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请提供 order_id"})
		return
	}
	tid := s.effTenant(r, u)
	// ★ USDT（2026-09-15）：tx_hash 入口校验（格式校验，链上真实性以后台/对账器查证为准）
	if existing, _ := s.Store.GetOrder(req.OrderID, tid); existing != nil && existing.Channel == "usdt" {
		meta, mErr := s.Store.GetUSDTOrderMeta(req.OrderID)
		if mErr != nil || req.TxHash == "" {
			writeJSON(w, 400, map[string]interface{}{"success": false, "message": "USDT 订单需提交交易哈希（到账以平台链上查证为准）"})
			return
		}
		if !payment.ValidUSDTTxHash(meta.Chain, req.TxHash) {
			writeJSON(w, 400, map[string]interface{}{"success": false, "message": "USDT 交易哈希格式非法（链 " + meta.Chain + "，需对应链的合法哈希）"})
			return
		}
	}
	rebateNote := ""
	if err := s.Store.MarkOrderManualConfirm(req.OrderID, tid); err != nil {
		// ★ C19（2026-09-12）：原单已被超时任务取消时不再死路——按原单重建补审单
		origID := req.OrderID
		if no, e2 := s.Store.ReopenManualOrder(origID, tid); e2 == nil {
			req.OrderID = no.ID
			rebateNote = fmt.Sprintf("（原订单 #%d 超时取消，已自动重建补审单）", origID)
		} else {
			writeJSON(w, 200, map[string]interface{}{"success": false, "message": publicErrMessage(r.Context(), err)})
			return
		}
	}
	o, _ := s.Store.GetOrder(req.OrderID, tid)
	// ★ USDT（2026-09-15）：落客户声明的交易哈希（线索+后台展示；C19 补审单不携带）
	txNote := ""
	if o != nil && o.Channel == "usdt" && req.TxHash != "" && rebateNote == "" {
		if derr := s.Store.SetUSDTDeclaredTxHash(req.OrderID, req.TxHash); derr != nil {
			log.Printf("[pay-manual-confirm] usdt 声明哈希落库失败 order=%d: %v", req.OrderID, derr)
		} else {
			txNote = " tx=" + req.TxHash
		}
	}
	// 写入 critical 级告警（前台告警面板 + 超管可见）
	msg := "静态码支付待人工确认：租户 #" + strconv.FormatInt(tid, 10) + " 订单 " + o.OrderNo + rebateNote + txNote + " 用户已付款，请尽快查看并开通"
	if o != nil && o.Channel == "usdt" {
		msg = "USDT 收款待核销：租户 #" + strconv.FormatInt(tid, 10) + " 订单 " + o.OrderNo + rebateNote + txNote + " 用户已声明链上转账，请核对交易哈希后确认到账"
	}
	_ = s.Store.CreateAlert(0, "critical", "pay_manual", msg)
	// ★ 站内信通知平台超管（tenant_id=0, role=admin）：超管铃铛即时可见待确认订单
	//   注意：CreateNotification 依赖自增序列取主键；若序列失步（如 pg 迁移/回放后
	//   seq 落后于实际行数）会撞主键失败——务必打日志而非静默吞错，便于及时发现。
	for _, sa := range s.Store.ListUsersByRole(0, "admin") {
		if err := s.Store.CreateNotification(sa.ID, "静态码支付待人工确认",
			fmt.Sprintf("租户 #%d 订单 %s%s 用户已付款，请尽快查看并开通", tid, o.OrderNo, rebateNote), "pay_manual", req.OrderID); err != nil {
			log.Printf("[pay-manual-confirm] 站内信通知超管(id=%d)失败: %v", sa.ID, err)
		}
	}
	// 邮件通知超管（收件人 alert_email，抄送 alert_email_cc）
	s.notifyAlert("静态码支付待人工确认（请尽快开通）", msg)
	s.Store.LogAudit(tid, u.ID, "pay_manual_confirm", "orders", o.OrderNo)
	writeJSON(w, 200, map[string]interface{}{"success": true, "message": "已通知管理员审核开通，请等待到账"})
}

// handlePayNotify 支付渠道异步回调：验签后确认订单到账（幂等）。
//
// ★ 安全止血（2026-08-26 P0-2 + 2026-08-30 修复）：
//
//	① 凭证必填（所有渠道统一）——X-Admin-Token 头缺失或不符合 AdminToken 一律 403。
//	   Caddy 反代已为 /api/pay/notify/* 注入该头，微信/支付宝服务器无需自行携带。
//	   此前仅 mock 渠道校验导致 wechat/alipay 无凭证也能探测订单存在（信息泄露）；
//	② 渠道一致性——回调 channel 必须与订单落库 channel 匹配；manual 单只认人工确认流程；
//	③ mock 封禁——pay_mode≠mock 时 mock 渠道回调直接拒绝（生产防呆）。
//
// 参数 w: HTTP 响应写入器；r: HTTP 请求（path 含 :channel，body 为渠道报文）。
// 返回: 渠道约定格式（成功返回 success 字符串，微信返回 204）。
func (s *Server) handlePayNotify(w http.ResponseWriter, r *http.Request) {
	channel := strings.TrimPrefix(r.URL.Path, "/api/pay/notify/")
	// ★ USDT（2026-09-15）：链上资产无原生回调，本口对 usdt 显式关闭（不扩攻击面）——
	//   到账只走 reconciler 链上查证或后台人工核销（防「mock 报文注入发币」路径）。
	if channel == "usdt" {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "USDT 无渠道回调，请通过链上对账或人工核销确认"})
		return
	}
	// ① 凭证（★ 2026-08-30 修复：所有渠道统一先验 X-Admin-Token，再走渠道签名）：
	//    此前仅 mock 渠道校验 Token，wechat/alipay 直接跳到签名验签→查订单，
	//    导致无凭证请求也能探测订单是否存在（信息泄露）。
	//    Caddy 反代已为 /api/pay/notify/* 路径注入 X-Admin-Token 头，
	//    故所有渠道均可统一校验，不依赖第三方支付服务器携带该头。
	tok := r.Header.Get("X-Admin-Token")
	if tok == "" || !constantTimeTokenEqual(tok, s.Cfg.AdminToken) {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": "拒绝访问"})
		return
	}
	// ③ mock 封禁（★ 整改 A6：与 handlePaySimulate 同口径收紧——支付模式未显式
	//    配置为 mock 时，mock 渠道回调一律拒绝，堵住「空配置=可模拟充值」的默认放行）。
	//    2026-09：支付模式读最终运营策略（payment.mode，平台级），存量配置经兜底并入。
	payMode := s.effPayMode(0)
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
	// 构建对应渠道的提供商并验签（★ A5：按回调 URL 渠道路由，wechat 单不再被 alipay 回调误配）
	prov := s.payProviderFor(channel)
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
		expectFen = s.Store.TokensToFen(o.AmountTokens)
	}
	if nt.Amount <= 0 || nt.Amount != expectFen {
		writeJSON(w, 400, map[string]interface{}{"success": false,
			"message": fmt.Sprintf("回调金额不符：期望 %d 分，实收 %d 分", expectFen, nt.Amount)})
		return
	}
	if err := s.Store.MarkOrderPaidByOrderNo(nt.OrderNo); err != nil {
		// ★ 结算失败必须留日志（原始错误）+ 用户侧脱敏文案（2026-09-12：此前 pq 裸错直吐客户端）
		log.Printf("[pay] 订单结算失败 order_no=%s: %v", nt.OrderNo, err)
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "订单确认失败: " + store.DebriefDBError(err)})
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
