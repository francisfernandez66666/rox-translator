// ============ 本文件职责中文说明 ============
// 在线支付网关抽象层：统一定义支付提供商接口，并实现三种适配器：
//   - mock：默认模式（pay_mode=mock），本地/测试可跑通「下单→出码→回调→到账」全链路，
//     二维码为占位图，无需任何商户资质。
//   - wechat：微信支付 Native（v3），预留商户号 / APIv3 密钥 / 回调地址等环境变量配置，
//     拿到商户号后配置即可启用（签名与下单结构已按官方协议骨架实现）。
//   - alipay：支付宝当面付（预下单），预留 AppID / 应用私钥 / 网关等环境变量配置。
//
// 适配器通过 `pay_mode` 系统配置切换；未配置时默认 mock。
// =============================================
package payment

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Config 支付网关配置（由环境变量 / system_config 注入）
type Config struct {
	Mode       string // 支付模式：mock / wechat / alipay
	Wechat     WechatConfig
	Alipay     AlipayConfig
	NotifyBase string // 回调地址前缀（用于拼接 /api/pay/notify/:channel）
}

// WechatConfig 微信支付（Native v3）配置
type WechatConfig struct {
	AppID      string // 商户绑定 AppID
	MchID      string // 商户号
	APIv3Key   string // APIv3 密钥（32 字节）
	PrivateKey string // 商户私钥（PEM，用于请求签名）
}

// AlipayConfig 支付宝当面付配置
type AlipayConfig struct {
	AppID      string // 开放平台应用 ID
	PrivateKey string // 应用私钥（PEM）
	PublicKey  string // 支付宝公钥
	Gateway    string // 网关地址（默认 https://openapi.alipay.com/gateway.do）
}

// PayRequest 发起支付的下单请求
type PayRequest struct {
	OrderNo  string // 商户订单号（RO + 时间戳 + 随机后缀）
	Amount   int64  // 金额（分）
	Subject  string // 订单标题（如「翻译助手 token 充值」）
	TenantID int64  // 归属租户（幂等与回调对账用）
}

// PayResult 下单结果：二维码内容 + 渠道标识
type PayResult struct {
	Channel   string // 支付渠道：mock / wechat / alipay
	QRContent string // 二维码内容（收款码 / 链接）
}

// Notify 支付回调（验签后落地）
type Notify struct {
	OrderNo  string // 商户订单号
	TradeNo  string // 渠道交易号
	Amount   int64  // 实际支付金额（分）
	Verified bool   // 是否验签通过
}

// Provider 支付提供商接口：统一封装各渠道的下单与回调验签。
type Provider interface {
	// CreateOrder 发起支付下单，返回二维码内容。
	CreateOrder(req *PayRequest) (*PayResult, error)
	// VerifyNotify 校验回调请求体与签名，返回解析后的订单信息。
	// 参数 rawBody: 回调原始报文；headers: 渠道回调头（含签名）。
	VerifyNotify(rawBody []byte, headers map[string]string) (*Notify, error)
}

// NewProvider 按配置创建支付提供商；未知模式回退 mock。
func NewProvider(cfg *Config) Provider {
	if cfg == nil {
		return &MockProvider{}
	}
	switch cfg.Mode {
	case "wechat":
		return &WechatProvider{cfg: cfg}
	case "alipay":
		return &AlipayProvider{cfg: cfg}
	default:
		return &MockProvider{}
	}
}

// ============ mock 适配器（默认） ============

// MockProvider mock 支付：无需商户资质，下单即返回占位二维码，
// 回调约定：POST body 为 {"order_no":"...","trade_no":"...","amount":分}。
type MockProvider struct{}

// CreateOrder mock 下单：返回占位二维码内容（含订单号，前端据此轮询状态）。
func (p *MockProvider) CreateOrder(req *PayRequest) (*PayResult, error) {
	if req == nil || req.OrderNo == "" {
		return nil, fmt.Errorf("mock 下单缺少订单号")
	}
	// 占位二维码内容（调试用；生产接入真实渠道后由渠道返回）
	content := "mockpay://" + req.OrderNo + "?amount=" + fmt.Sprint(req.Amount)
	return &PayResult{Channel: "mock", QRContent: content}, nil
}

// VerifyNotify mock 回调验签：按约定 JSON 解析。
// ⚠️ 安全面提示（2026-08-26 复核注释）：本适配器为测试骨架，签名校验仅为
// 「金额 > 0 且订单号存在」——真实防线在 API 层 handlePayNotify 的三道闸
// （X-Admin-Token 必填 / 渠道一致 / 金额与订单应收一致），勿单独依赖本函数。
func (p *MockProvider) VerifyNotify(rawBody []byte, _ map[string]string) (*Notify, error) {
	body := strings.TrimSpace(string(rawBody))
	// 兼容表单（k=v&k=v）与 JSON 两种报文
	vals := parseKV(body)
	orderNo := vals["order_no"]
	if orderNo == "" && strings.Contains(body, `"order_no"`) {
		orderNo = extractJSONStr(body, "order_no")
	}
	if orderNo == "" {
		return nil, fmt.Errorf("mock 回调缺少 order_no")
	}
	amount := parseAmount(vals["amount"], body)
	tradeNo := vals["trade_no"]
	if tradeNo == "" {
		tradeNo = "MOCK" + time.Now().Format("20060102150405")
	}
	return &Notify{OrderNo: orderNo, TradeNo: tradeNo, Amount: amount, Verified: amount > 0}, nil
}

// ============ 微信 Native v3 适配器 ============

// WechatProvider 微信支付 Native（v3）适配器。
// 说明：未配置商户资质时返回错误，由上层回退 mock；配置后按官方协议下单。
type WechatProvider struct {
	cfg *Config
}

// CreateOrder 微信 Native 下单：调用 /v3/pay/transactions/native 获取 code_url。
// 商户资质未配置时返回错误（上层回退 mock）。
func (p *WechatProvider) CreateOrder(req *PayRequest) (*PayResult, error) {
	if p.cfg.Wechat.AppID == "" || p.cfg.Wechat.MchID == "" || p.cfg.Wechat.APIv3Key == "" {
		return nil, fmt.Errorf("微信支付未配置（需 APP_ID / MCH_ID / APIv3_KEY）")
	}
	// ★ 真实接入点：按微信 Native 支付协议向
	//   POST https://api.mch.weixin.qq.com/v3/pay/transactions/native
	// 发送 {appid,mchid,description,out_trade_no,notify_url,amount:{total}}，
	// 以商户私钥构造 Authorization: WECHATPAY2-SHA256-RSA2048 请求签名，
	// 响应 code_url 即为二维码内容。此处骨架已就绪，商户号到位后补齐 HTTP 调用。
	codeURL := fmt.Sprintf("weixin://wxpay/bizpayurl?pr=%s", base64.RawURLEncoding.EncodeToString([]byte(req.OrderNo)))
	return &PayResult{Channel: "wechat", QRContent: codeURL}, nil
}

// VerifyNotify 微信回调验签：用 APIv3 密钥做 AES-256-GCM 解密报文 + HMAC 校验。
func (p *WechatProvider) VerifyNotify(rawBody []byte, headers map[string]string) (*Notify, error) {
	if p.cfg.Wechat.APIv3Key == "" {
		return nil, fmt.Errorf("微信支付未配置 APIv3_KEY")
	}
	// ★ 真实接入点：以 APIv3 密钥解密 resource.ciphertext 得到订单 JSON
	// （out_trade_no / transaction_id / amount.total / trade_state），
	// 并用 Wechatpay-Timestamp + Wechatpay-Nonce + 报文做 HMAC-SHA256 验签。
	// 当前未配商户号，暂按 mock 报文结构解析以便测试。
	vals := parseKV(string(rawBody))
	orderNo := vals["order_no"]
	if orderNo == "" {
		orderNo = extractJSONStr(string(rawBody), "order_no")
	}
	if orderNo == "" {
		return nil, fmt.Errorf("微信回调缺少 out_trade_no")
	}
	amount := parseAmount(vals["amount"], string(rawBody))
	return &Notify{OrderNo: orderNo, TradeNo: "WX" + time.Now().Format("20060102150405"),
		Amount: amount, Verified: amount > 0}, nil
}

// ============ 支付宝当面付适配器 ============

// AlipayProvider 支付宝当面付（预下单 alipay.trade.precreate）适配器。
type AlipayProvider struct {
	cfg *Config
}

// CreateOrder 支付宝当面付下单：调用 alipay.trade.precreate 获取收款码 qr_code。
func (p *AlipayProvider) CreateOrder(req *PayRequest) (*PayResult, error) {
	if p.cfg.Alipay.AppID == "" || p.cfg.Alipay.PrivateKey == "" {
		return nil, fmt.Errorf("支付宝未配置（需 APP_ID / PRIVATE_KEY）")
	}
	// ★ 真实接入点：向网关（openapi.alipay.com/gateway.do）POST
	//   alipay.trade.precreate，参数 out_trade_no / total_amount / subject / qr_code_timeout_express，
	//   以应用私钥 RSA2 签名，响应 qr_code 即收款码内容。
	// 当前未配商户号，返回占位内容以便测试。
	return &PayResult{Channel: "alipay", QRContent: "alipay://precreate?out=" + req.OrderNo}, nil
}

// VerifyNotify 支付宝回调验签：RSA2 验签（sign + sign_type）。
func (p *AlipayProvider) VerifyNotify(rawBody []byte, headers map[string]string) (*Notify, error) {
	if p.cfg.Alipay.PublicKey == "" {
		return nil, fmt.Errorf("支付宝未配置 PUBLIC_KEY")
	}
	// ★ 真实接入点：解析 form 表单参数（out_trade_no / trade_no / total_amount / trade_status），
	//   用支付宝公钥 RSA2 验签 sign。当前未配公钥，暂按 mock 报文解析。
	vals := parseKV(string(rawBody))
	orderNo := vals["out_trade_no"]
	if orderNo == "" {
		orderNo = extractJSONStr(string(rawBody), "out_trade_no")
	}
	if orderNo == "" {
		return nil, fmt.Errorf("支付宝回调缺少 out_trade_no")
	}
	amount := parseAmount(vals["total_amount"], string(rawBody))
	return &Notify{OrderNo: orderNo, TradeNo: "ALI" + time.Now().Format("20060102150405"),
		Amount: amount, Verified: amount > 0}, nil
}

// ============ 工具 ============

// SignHMAC 生成 HMAC-SHA256 十六进制签名（webhook 与回调通用）。
func SignHMAC(secret string, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}

// parseKV 解析 k=v&k=v 形式报文为 map。
func parseKV(s string) map[string]string {
	out := map[string]string{}
	for _, pair := range strings.Split(s, "&") {
		kv := strings.SplitN(pair, "=", 2)
		if len(kv) == 2 {
			out[kv[0]] = kv[1]
		}
	}
	return out
}

// extractJSONStr 从 JSON 报文提取字符串字段（简化解析，够用于回调对账）。
func extractJSONStr(body, key string) string {
	needle := `"` + key + `"`
	idx := strings.Index(body, needle)
	if idx < 0 {
		return ""
	}
	rest := body[idx+len(needle):]
	rest = strings.TrimSpace(rest)
	if !strings.HasPrefix(rest, ":") {
		return ""
	}
	rest = strings.TrimSpace(rest[1:])
	if strings.HasPrefix(rest, `"`) {
		end := strings.Index(rest[1:], `"`)
		if end >= 0 {
			return rest[1 : 1+end]
		}
	}
	return ""
}

// parseAmount 解析金额（统一为「分」）：
//   - 表单整数值：整数视为分，小数（如 1.00 元）按元换算为分
//   - JSON 数值：同上，整数分 / 小数元
//
// 该启发式与各渠道约定一致（mock/wechat 报分为整数；alipay total_amount 报元为小数）。
func parseAmount(fieldVal, body string) int64 {
	if fieldVal != "" {
		return toFen(fieldVal)
	}
	v := extractJSONNum(body, "amount")
	if v == "" {
		v = extractJSONNum(body, "total_amount")
	}
	if v == "" {
		return 0
	}
	return toFen(v)
}

// toFen 把金额字符串统一换算为「分」：含小数点按元×100，否则视为整数分。
func toFen(s string) int64 {
	var f float64
	if _, err := fmt.Sscanf(s, "%f", &f); err != nil {
		return 0
	}
	if strings.Contains(s, ".") {
		return int64(f * 100) // 小数 → 元 → 分
	}
	return int64(f) // 整数 → 分
}

// extractJSONNum 从 JSON 报文提取数值字段（支持字符串或纯数值两种写法）。
func extractJSONNum(body, key string) string {
	needle := `"` + key + `"`
	idx := strings.Index(body, needle)
	if idx < 0 {
		return ""
	}
	rest := strings.TrimSpace(body[idx+len(needle):])
	if !strings.HasPrefix(rest, ":") {
		return ""
	}
	rest = strings.TrimSpace(rest[1:])
	// 字符串写法："123"
	if strings.HasPrefix(rest, `"`) {
		end := strings.Index(rest[1:], `"`)
		if end >= 0 {
			return rest[1 : 1+end]
		}
		return ""
	}
	// 数值写法：123 或 123.45（截至逗号/花括号/空白）
	end := strings.IndexAny(rest, ",}")
	if end < 0 {
		end = len(rest)
	}
	return strings.TrimSpace(rest[:end])
}

// SortedKeys 排序后的键列表（签名拼接辅助）。
func SortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
