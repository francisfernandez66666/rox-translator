// ============ 本文件职责中文说明 ============
// 微信 Native v3 / 支付宝当面付的「真实协议调用层」（★ #41 商业洞一：自动到账，2026-09-21 实装）。
// payment.go 的两个 CreateOrder 只做资质校验与转发，本文件负责：
//   - 请求签名：微信 WECHATPAY2-SHA256-RSA2048（Authorization 头）、支付宝 RSA2（SHA256withRSA）
//   - HTTP 调用：api.mch.weixin.qq.com/v3/pay/transactions/native、openapi.alipay.com/gateway.do
//   - 响应解析与（支付宝）响应验签，并复核 out_trade_no 与本地订单一致（防串单）
//
// 人工开启真实收款的步骤（拿到商户资质后照做，无需改代码）：
//  1. 微信商户平台（pay.weixin.qq.com）→ 产品中心 → 开通「Native 支付」，拿到
//     商户号 MCHID、AppID、APIv3 密钥（32 位）、API 证书（apiclient_key.pem）与证书序列号。
//  2. 支付宝开放平台（open.alipay.com）→ 创建应用 → 签约「当面付」，上传应用公钥后拿到
//     APPID、应用私钥（与上面公钥配对）、支付宝公钥。
//  3. 服务器环境变量（.env / systemd / docker-compose 任一注入方式）：
//     PAY_NOTIFY_BASE=https://你的域名（回调前缀；也可用下面两个完整地址替代）
//     pay_mode / payment.mode 运营策略置为 sdk（或 wechat、alipay）
//     PAY_WECHAT_APP_ID、PAY_WECHAT_MCH_ID、PAY_WECHAT_SERIAL_NO、
//     PAY_WECHAT_PRIVATE_KEY（apiclient_key.pem 全文）、PAY_WECHAT_APIv3_KEY，
//     可选 PAY_WECHAT_NOTIFY_URL
//     PAY_ALIPAY_APP_ID、PAY_ALIPAY_PRIVATE_KEY、PAY_ALIPAY_PUBLIC_KEY，
//     可选 PAY_ALIPAY_NOTIFY_URL、PAY_ALIPAY_SELLER_ID、PAY_ALIPAY_GATEWAY
//  4. 重启后端：任一项缺失时 /api/pay/create 仍明确拒绝出单（fail-closed，报「资质未配置：xxx」）；
//     配齐后同一请求即返回渠道真实收款码，客户付款 → 渠道回调 /api/pay/notify/渠道 → 自动到账。
//  5. 反代（Caddy）需继续为 /api/pay/notify/* 注入 X-Admin-Token（handlePayNotify 的第一道闸）。
//
// ==========================================
package payment

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	// wechatDefaultBase 微信 v3 接口域名（可用 WechatConfig.BaseURL 覆盖，测试注入 httptest）
	wechatDefaultBase = "https://api.mch.weixin.qq.com"
	// wechatNativePath Native 下单接口路径（参与签名的 URL 部分，不含查询串）
	wechatNativePath = "/v3/pay/transactions/native"
	// alipayDefaultGateway 支付宝开放平台网关
	alipayDefaultGateway = "https://openapi.alipay.com/gateway.do"
	// alipayPrecreateMethod 当面付预下单接口
	alipayPrecreateMethod = "alipay.trade.precreate"
	// payHTTPTimeout 渠道调用超时：出码接口是用户同步等待路径，宁短勿长（失败可重试下单）
	payHTTPTimeout = 15 * time.Second
	// payRespLimit 渠道响应体读取上限，防异常上游撑爆内存
	payRespLimit = 64 << 10
)

// ============ 资质校验（fail-closed 的入口） ============

// wechatMissingCreds 返回微信下单缺失的配置项名（含环境变量名，便于运维照做）。
// APIv3 密钥与回调地址虽不参与下单，但缺了它们「收到钱却认不出账」，
// 因此一并视为必备资质——只出码不到账比不出码更危险。
func wechatMissingCreds(cfg *Config) []string {
	var missing []string
	if cfg == nil {
		return []string{"PAY_WECHAT_APP_ID / PAY_WECHAT_MCH_ID / PAY_WECHAT_SERIAL_NO / PAY_WECHAT_PRIVATE_KEY / PAY_WECHAT_APIv3_KEY / PAY_NOTIFY_BASE"}
	}
	if strings.TrimSpace(cfg.Wechat.AppID) == "" {
		missing = append(missing, "PAY_WECHAT_APP_ID")
	}
	if strings.TrimSpace(cfg.Wechat.MchID) == "" {
		missing = append(missing, "PAY_WECHAT_MCH_ID")
	}
	if strings.TrimSpace(cfg.Wechat.SerialNo) == "" {
		missing = append(missing, "PAY_WECHAT_SERIAL_NO（商户 API 证书序列号）")
	}
	if strings.TrimSpace(cfg.Wechat.PrivateKey) == "" {
		missing = append(missing, "PAY_WECHAT_PRIVATE_KEY（apiclient_key.pem 全文）")
	}
	if len(cfg.Wechat.APIv3Key) != 32 {
		missing = append(missing, "PAY_WECHAT_APIv3_KEY（32 字节，回调解密必需）")
	}
	if resolveNotifyURL(cfg, cfg.Wechat.NotifyURL, "wechat") == "" {
		missing = append(missing, "PAY_WECHAT_NOTIFY_URL 或 PAY_NOTIFY_BASE")
	}
	return missing
}

// alipayMissingCreds 返回支付宝下单缺失的配置项名。
// 支付宝公钥为必备项：下单响应需验签，否则被劫持的响应可以把用户引向他人收款码。
func alipayMissingCreds(cfg *Config) []string {
	var missing []string
	if cfg == nil {
		return []string{"PAY_ALIPAY_APP_ID / PAY_ALIPAY_PRIVATE_KEY / PAY_ALIPAY_PUBLIC_KEY / PAY_NOTIFY_BASE"}
	}
	if strings.TrimSpace(cfg.Alipay.AppID) == "" {
		missing = append(missing, "PAY_ALIPAY_APP_ID")
	}
	if strings.TrimSpace(cfg.Alipay.PrivateKey) == "" {
		missing = append(missing, "PAY_ALIPAY_PRIVATE_KEY（应用私钥）")
	}
	if strings.TrimSpace(cfg.Alipay.PublicKey) == "" {
		missing = append(missing, "PAY_ALIPAY_PUBLIC_KEY（支付宝公钥，下单响应验签必需）")
	}
	if resolveNotifyURL(cfg, cfg.Alipay.NotifyURL, "alipay") == "" {
		missing = append(missing, "PAY_ALIPAY_NOTIFY_URL 或 PAY_NOTIFY_BASE")
	}
	return missing
}

// resolveNotifyURL 取渠道回调地址：显式配置优先，否则由 NotifyBase 拼出规范路径。
// 参数 cfg: 网关配置；explicit: 渠道专属回调地址；channel: wechat / alipay（对应 /api/pay/notify/:channel）。
func resolveNotifyURL(cfg *Config, explicit, channel string) string {
	if s := strings.TrimSpace(explicit); s != "" {
		return s
	}
	if cfg == nil {
		return ""
	}
	if base := strings.TrimRight(strings.TrimSpace(cfg.NotifyBase), "/"); base != "" {
		return base + "/api/pay/notify/" + channel
	}
	return ""
}

// ============ 微信 Native v3 下单 ============

// wechatNativeRequest 微信 Native 下单请求体（字段名严格对齐官方 v3 文档）。
type wechatNativeRequest struct {
	AppID       string `json:"appid"`
	MchID       string `json:"mchid"`
	Description string `json:"description"`
	OutTradeNo  string `json:"out_trade_no"`
	NotifyURL   string `json:"notify_url"`
	TimeExpire  string `json:"time_expire,omitempty"` // RFC3339（带时区偏移）
	Amount      struct {
		Total    int64  `json:"total"` // 分
		Currency string `json:"currency"`
	} `json:"amount"`
}

// wechatNativeCreate 调用微信 Native 下单接口换取 code_url（weixin://wxpay/bizpayurl?pr=…）。
// 失败一律返回错误（上层 handlePayCreate 保持订单 pending 并明确告知渠道不可用）。
func wechatNativeCreate(cfg *Config, req *PayRequest) (*PayResult, error) {
	if req == nil || req.OrderNo == "" {
		return nil, fmt.Errorf("微信下单缺少订单号")
	}
	if req.Amount <= 0 {
		return nil, fmt.Errorf("微信下单金额必须大于 0")
	}
	notifyURL := resolveNotifyURL(cfg, cfg.Wechat.NotifyURL, "wechat")
	if !strings.HasPrefix(notifyURL, "https://") {
		// 微信强制 https 公网回调地址；http 会被拒单，这里提前给出可执行提示
		return nil, fmt.Errorf("微信回调地址必须为 https 公网地址（当前 %q）", notifyURL)
	}
	priv, err := parseRSAPrivateKey(cfg.Wechat.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("微信商户私钥不可用: %w", err)
	}
	var body wechatNativeRequest
	body.AppID = cfg.Wechat.AppID
	body.MchID = cfg.Wechat.MchID
	body.Description = truncateRunes(cleanSubject(req.Subject), 120)
	body.OutTradeNo = req.OrderNo
	body.NotifyURL = notifyURL
	if req.ExpireMinutes > 0 {
		body.TimeExpire = time.Now().Add(time.Duration(req.ExpireMinutes) * time.Minute).Format(time.RFC3339)
	}
	body.Amount.Total = req.Amount
	body.Amount.Currency = "CNY"
	payload, err := json.Marshal(&body)
	if err != nil {
		return nil, fmt.Errorf("微信下单报文构造失败: %w", err)
	}

	base := strings.TrimRight(strings.TrimSpace(cfg.Wechat.BaseURL), "/")
	if base == "" {
		base = wechatDefaultBase
	}
	u, err := url.Parse(base + wechatNativePath)
	if err != nil {
		return nil, fmt.Errorf("微信接口地址非法: %w", err)
	}
	auth, err := wechatAuthorization(cfg.Wechat.MchID, cfg.Wechat.SerialNo, priv, http.MethodPost, u.Path, string(payload))
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequest(http.MethodPost, u.String(), bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("微信下单请求构造失败: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("User-Agent", "langcross-pay/1.0")
	httpReq.Header.Set("Authorization", auth)

	respBody, status, err := doPayRequest(cfg, httpReq)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		// 微信错误体为 {"code":"...","message":"..."}；截断后透传，便于运维定位（不含本地信息）
		return nil, fmt.Errorf("微信下单被拒（HTTP %d）%s", status, wechatErrHint(respBody))
	}
	var out struct {
		CodeURL string `json:"code_url"`
	}
	if err := json.Unmarshal(respBody, &out); err != nil {
		return nil, fmt.Errorf("微信下单响应解析失败: %w", err)
	}
	if out.CodeURL == "" {
		return nil, fmt.Errorf("微信下单未返回 code_url")
	}
	// 出码形态自检：真实 code_url 形如 weixin://wxpay/bizpayurl?pr=xxx
	if !strings.HasPrefix(out.CodeURL, "weixin://") && !strings.HasPrefix(out.CodeURL, "https://") {
		return nil, fmt.Errorf("微信返回的 code_url 形态异常，已拒绝出单")
	}
	return &PayResult{Channel: "wechat", QRContent: out.CodeURL}, nil
}

// wechatAuthorization 构造微信 v3 Authorization 头（WECHATPAY2-SHA256-RSA2048）。
// 待签名串固定五段：方法\nURL(含路径不含域名与查询串)\n时间戳\n随机串\n报文体\n。
func wechatAuthorization(mchID, serialNo string, priv *rsa.PrivateKey, method, path, body string) (string, error) {
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	nonce, err := randomHex(16) // 32 字符
	if err != nil {
		return "", fmt.Errorf("微信签名随机数生成失败: %w", err)
	}
	message := method + "\n" + path + "\n" + ts + "\n" + nonce + "\n" + body + "\n"
	signature, err := signRSA256(priv, []byte(message))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`WECHATPAY2-SHA256-RSA2048 mchid="%s",nonce_str="%s",timestamp="%s",serial_no="%s",signature="%s"`,
		mchID, nonce, ts, serialNo, signature), nil
}

// wechatErrHint 从微信错误体提取 code/message（限长、去换行），解析失败则回落原始片段。
func wechatErrHint(respBody []byte) string {
	var e struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if json.Unmarshal(respBody, &e) == nil && (e.Code != "" || e.Message != "") {
		return "（" + sanitizeHint(e.Code+" "+e.Message) + "）"
	}
	if s := sanitizeHint(string(respBody)); s != "" {
		return "（" + s + "）"
	}
	return ""
}

// ============ 支付宝当面付（precreate）下单 ============

// alipayPrecreateResponse 网关响应外层（内层为业务响应 + 顶层 sign）。
type alipayPrecreateResponse struct {
	Response struct {
		Code       string `json:"code"`
		Msg        string `json:"msg"`
		SubCode    string `json:"sub_code"`
		SubMsg     string `json:"sub_msg"`
		OutTradeNo string `json:"out_trade_no"`
		QRCode     string `json:"qr_code"`
	} `json:"alipay_trade_precreate_response"`
	Sign string `json:"sign"`
}

// alipayPrecreate 调用 alipay.trade.precreate 换取收款码 qr_code（https://qr.alipay.com/…）。
// 响应必须用支付宝公钥验签（配置校验已保证公钥存在），验签不过一律拒绝出单。
func alipayPrecreate(cfg *Config, req *PayRequest) (*PayResult, error) {
	if req == nil || req.OrderNo == "" {
		return nil, fmt.Errorf("支付宝下单缺少订单号")
	}
	if req.Amount <= 0 {
		return nil, fmt.Errorf("支付宝下单金额必须大于 0")
	}
	priv, err := parseRSAPrivateKey(cfg.Alipay.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("支付宝应用私钥不可用: %w", err)
	}
	biz := map[string]string{
		"out_trade_no": req.OrderNo,
		"total_amount": fenToYuan(req.Amount),
		"subject":      truncateRunes(cleanSubject(req.Subject), 200),
		"product_code": "FACE_TO_FACE_PAYMENT", // 当面付固定产品码
	}
	if req.ExpireMinutes > 0 {
		biz["timeout_express"] = strconv.Itoa(req.ExpireMinutes) + "m"
	}
	bizJSON, err := json.Marshal(biz)
	if err != nil {
		return nil, fmt.Errorf("支付宝业务参数构造失败: %w", err)
	}
	params := map[string]string{
		"app_id":      cfg.Alipay.AppID,
		"method":      alipayPrecreateMethod,
		"format":      "JSON",
		"charset":     "utf-8",
		"sign_type":   "RSA2",
		"timestamp":   time.Now().Format("2006-01-02 15:04:05"),
		"version":     "1.0",
		"notify_url":  resolveNotifyURL(cfg, cfg.Alipay.NotifyURL, "alipay"),
		"biz_content": string(bizJSON),
	}
	signStr := alipayCanonicalQuery(params)
	digest := sha256.Sum256([]byte(signStr))
	sig, err := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, digest[:])
	if err != nil {
		return nil, fmt.Errorf("支付宝请求签名失败: %w", err)
	}
	params["sign"] = base64.StdEncoding.EncodeToString(sig)

	form := url.Values{}
	for k, v := range params {
		if v != "" {
			form.Set(k, v)
		}
	}
	gateway := strings.TrimSpace(cfg.Alipay.Gateway)
	if gateway == "" {
		gateway = alipayDefaultGateway
	}
	httpReq, err := http.NewRequest(http.MethodPost, gateway, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("支付宝下单请求构造失败: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded;charset=utf-8")
	httpReq.Header.Set("User-Agent", "langcross-pay/1.0")

	respBody, status, err := doPayRequest(cfg, httpReq)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("支付宝下单被拒（HTTP %d）%s", status, sanitizeHint(string(respBody)))
	}
	var out alipayPrecreateResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		return nil, fmt.Errorf("支付宝下单响应解析失败: %w", err)
	}
	if out.Sign == "" {
		return nil, fmt.Errorf("支付宝下单响应缺少 sign（无法验签，拒绝出单）")
	}
	// 验签对象是「业务响应节点的原始 JSON 串」，必须用未经反序列化改写的原文
	raw := extractJSONRawObject(string(respBody), "alipay_trade_precreate_response")
	if raw == "" {
		return nil, fmt.Errorf("支付宝下单响应缺少业务节点（无法验签，拒绝出单）")
	}
	if err := verifyAlipayRawSign(cfg.Alipay.PublicKey, raw, out.Sign); err != nil {
		return nil, fmt.Errorf("支付宝下单响应验签失败（拒绝出单）: %w", err)
	}
	if out.Response.Code != "10000" {
		return nil, fmt.Errorf("支付宝下单未成功：%s", sanitizeHint(out.Response.Code+" "+out.Response.Msg+" "+out.Response.SubCode+" "+out.Response.SubMsg))
	}
	if out.Response.OutTradeNo != "" && out.Response.OutTradeNo != req.OrderNo {
		return nil, fmt.Errorf("支付宝下单响应 out_trade_no 与本地订单不符，拒绝出单")
	}
	if out.Response.QRCode == "" {
		return nil, fmt.Errorf("支付宝下单未返回 qr_code")
	}
	if !strings.HasPrefix(out.Response.QRCode, "https://") && !strings.HasPrefix(out.Response.QRCode, "http://") {
		return nil, fmt.Errorf("支付宝返回的 qr_code 形态异常，已拒绝出单")
	}
	return &PayResult{Channel: "alipay", QRContent: out.Response.QRCode}, nil
}

// alipayCanonicalQuery 构造支付宝待签名串：除 sign 外全部参数按字典序拼 k=v&k=v（值不编码）。
// 注意与回调验签（verifyAlipayRSA2）的区别：回调排除空值，网关请求参数均非空，行为一致。
func alipayCanonicalQuery(params map[string]string) string {
	keys := make([]string, 0, len(params))
	for k, v := range params {
		if k == "sign" || v == "" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	for i, k := range keys {
		if i > 0 {
			sb.WriteString("&")
		}
		sb.WriteString(k + "=" + params[k])
	}
	return sb.String()
}

// verifyAlipayRawSign 以支付宝公钥对「业务响应原始 JSON 串」做 RSA2 验签。
// 参数 pubPEM: 支付宝公钥（PEM 或裸 base64）；raw: 业务节点原文；signB64: 响应 sign。
func verifyAlipayRawSign(pubPEM, raw, signB64 string) error {
	sig, err := base64.StdEncoding.DecodeString(signB64)
	if err != nil {
		return fmt.Errorf("sign base64 解码失败: %w", err)
	}
	pub, err := parseAlipayPublicKey(pubPEM)
	if err != nil {
		return err
	}
	digest := sha256.Sum256([]byte(raw))
	return rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], sig)
}

// ============ 共用工具 ============

// doPayRequest 执行渠道 HTTP 请求并返回（截断后的）响应体与状态码。
// 网络错误与超时统一包装为可展示的中文错误（调用方保留订单 pending，可由用户重试下单）。
func doPayRequest(cfg *Config, req *http.Request) ([]byte, int, error) {
	resp, err := payHTTPClient(cfg).Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("支付渠道网络请求失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	buf, err := io.ReadAll(io.LimitReader(resp.Body, payRespLimit))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("支付渠道响应读取失败: %w", err)
	}
	return buf, resp.StatusCode, nil
}

// payHTTPClient 取配置的 HTTP 客户端；未配置时用带超时的默认客户端（复用进程级 Transport）。
func payHTTPClient(cfg *Config) *http.Client {
	if cfg != nil && cfg.HTTPClient != nil {
		return cfg.HTTPClient
	}
	return &http.Client{Timeout: payHTTPTimeout}
}

// parseRSAPrivateKey 解析商户/应用私钥：兼容 PEM（PKCS#8 优先，回落 PKCS#1）与裸 base64，
// 并把环境变量里常见的字面 "\n" 还原为真换行（systemd/docker 注入多行密钥的现实约束）。
func parseRSAPrivateKey(s string) (*rsa.PrivateKey, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, fmt.Errorf("私钥为空")
	}
	s = strings.ReplaceAll(s, `\n`, "\n")
	if !strings.Contains(s, "-----BEGIN") {
		s = "-----BEGIN PRIVATE KEY-----\n" + strings.Join(wrapLines(s, 64), "\n") + "\n-----END PRIVATE KEY-----"
	}
	block, _ := pem.Decode([]byte(s))
	if block == nil {
		return nil, fmt.Errorf("私钥 PEM 解析失败（请确认填入完整内容或 base64 串）")
	}
	if pk, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		if r, ok := pk.(*rsa.PrivateKey); ok {
			return r, nil
		}
		return nil, fmt.Errorf("私钥非 RSA 类型（PKCS#8）")
	}
	if r, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return r, nil
	}
	return nil, fmt.Errorf("私钥格式不支持（需 PKCS#8 或 PKCS#1 RSA 私钥）")
}

// signRSA256 以商户私钥做 SHA256withRSA 签名并 base64 编码（微信 v3 与支付宝 RSA2 同算法）。
func signRSA256(priv *rsa.PrivateKey, message []byte) (string, error) {
	digest := sha256.Sum256(message)
	sig, err := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("签名失败: %w", err)
	}
	return base64.StdEncoding.EncodeToString(sig), nil
}

// randomHex 生成 n 字节的加密安全随机数并以十六进制返回（长度 2n）。
func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// fenToYuan 分 → 支付宝 total_amount 的元字符串（两位小数，如 29900 分 → "299.00"）。
func fenToYuan(fen int64) string {
	return fmt.Sprintf("%d.%02d", fen/100, fen%100)
}

// wrapLines 把长串按每 width 个字符切行（裸 base64 私钥补 PEM 换行）。
func wrapLines(s string, width int) []string {
	var out []string
	for len(s) > width {
		out = append(out, s[:width])
		s = s[width:]
	}
	if s != "" {
		out = append(out, s)
	}
	return out
}

// cleanSubject 去掉订单标题中的换行与多余空白（渠道对 description/subject 有格式要求）。
func cleanSubject(s string) string {
	if s == "" {
		return "能言积分充值"
	}
	return strings.Join(strings.Fields(s), " ")
}

// truncateRunes 按字符数截断（渠道字段长度为字符口径，中文按 1 字符计）。
func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}

// sanitizeHint 清洗渠道回传文本：去控制字符、限长，避免把上游整页错误塞进日志与响应。
func sanitizeHint(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	if s == "" {
		return ""
	}
	return " " + s
}

// extractJSONRawObject 从 JSON 文本中取出 `"key":` 后紧跟的对象原文（含大括号，保持原始字节）。
// 支付宝响应验签要求使用原文，不能先反序列化再重新拼装（字段顺序/空白会变）。
func extractJSONRawObject(body, key string) string {
	needle := `"` + key + `"`
	i := strings.Index(body, needle)
	if i < 0 {
		return ""
	}
	rest := body[i+len(needle):]
	colon := strings.IndexByte(rest, ':')
	if colon < 0 {
		return ""
	}
	start := colon + 1
	for start < len(rest) && (rest[start] == ' ' || rest[start] == '\t' || rest[start] == '\n' || rest[start] == '\r') {
		start++
	}
	if start >= len(rest) || rest[start] != '{' {
		return ""
	}
	depth, inStr, esc := 0, false, false
	for j := start; j < len(rest); j++ {
		c := rest[j]
		if inStr {
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return rest[start : j+1]
			}
		}
	}
	return ""
}
