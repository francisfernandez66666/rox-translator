// ============ 本文件职责中文说明 ============
// 支付渠道真实协议层（gateway_sdk.go）的单元测试，全部走 httptest 本地假网关，
// 不触碰真实商户接口。覆盖：
//   - 微信 Native v3：Authorization 五要素格式、签名可被商户公钥验签、报文字段口径、
//     渠道 4xx 错误码透传、异常 code_url 拒绝出单、资质缺失/非 https 回调 fail-closed；
//   - 支付宝当面付：RSA2 请求签名（网关侧用应用公钥反验）、分→元两位小数、biz_content 口径、
//     响应验签（篡改即拒）、业务码非 10000 拒绝、out_trade_no 串单拒绝；
//   - 工具：私钥多形态解析（PKCS#8/PKCS#1/裸 base64/字面 \n）、回调地址回落、
//     JSON 原文节点提取、待签名串排序、错误文案清洗与截断。
//
// ==========================================
package payment

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// ============ 测试夹具 ============

// sdkKeys 两套密钥：商户/应用侧（我们持有私钥、渠道持有公钥）与
// 渠道侧（支付宝持有私钥签响应、我们配置其公钥验签），互不共用以贴近真实。
type sdkKeys struct {
	appPrivPEM    string         // 商户 API 私钥 / 支付宝应用私钥（PKCS#8 PEM）
	appPub        *rsa.PublicKey // 与上配对的公钥（假网关用来验我们的请求签名）
	channelPriv   *rsa.PrivateKey
	channelPubPEM string // 支付宝公钥 PEM（我们配置它来验响应签名）
}

// newSDKKeys 生成测试密钥对（2048 位足够，只验协议正确性不评强度）。
func newSDKKeys(t *testing.T) *sdkKeys {
	t.Helper()
	mk := func() (*rsa.PrivateKey, string, string) {
		k, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatal(err)
		}
		pk8, err := x509.MarshalPKCS8PrivateKey(k)
		if err != nil {
			t.Fatal(err)
		}
		pubDER, err := x509.MarshalPKIXPublicKey(&k.PublicKey)
		if err != nil {
			t.Fatal(err)
		}
		return k,
			string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pk8})),
			string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}))
	}
	appKey, appPrivPEM, _ := mk()
	chKey, _, chPubPEM := mk()
	return &sdkKeys{appPrivPEM: appPrivPEM, appPub: &appKey.PublicKey, channelPriv: chKey, channelPubPEM: chPubPEM}
}

// apiV3Key 32 字节合法 APIv3 密钥（长度不合规会被资质校验拦下）。
const apiV3Key = "01234567890123456789012345678901"

// authRe 解析微信 v3 Authorization 头五要素（mchid/nonce/timestamp/serial/signature）。
var authRe = regexp.MustCompile(`WECHATPAY2-SHA256-RSA2048 mchid="([^"]*)",nonce_str="([^"]*)",timestamp="([^"]*)",serial_no="([^"]*)",signature="([^"]*)"`)

// wechatCfg 构造一套资质齐全的微信配置，BaseURL 指向假网关、HTTPClient 用其客户端。
func wechatCfg(k *sdkKeys, srv *httptest.Server) *Config {
	return &Config{
		HTTPClient: srv.Client(),
		Wechat: WechatConfig{
			AppID: "wxapp0001", MchID: "1900000109", APIv3Key: apiV3Key,
			SerialNo: "SERIAL2026", PrivateKey: k.appPrivPEM,
			NotifyURL: "https://app.example.com/api/pay/notify/wechat",
			BaseURL:   srv.URL,
		},
	}
}

// alipayCfg 构造一套资质齐全的支付宝配置，Gateway 指向假网关。
func alipayCfg(k *sdkKeys, srv *httptest.Server) *Config {
	return &Config{
		HTTPClient: srv.Client(),
		Alipay: AlipayConfig{
			AppID: "2021000000000001", PrivateKey: k.appPrivPEM, PublicKey: k.channelPubPEM,
			Gateway:   srv.URL + "/gateway.do",
			NotifyURL: "https://app.example.com/api/pay/notify/alipay",
		},
	}
}

// signAlipayResponse 用渠道私钥对业务响应原文 RSA2 签名，拼装网关返回体。
func signAlipayResponse(t *testing.T, k *sdkKeys, rawBiz string) string {
	t.Helper()
	digest := sha256.Sum256([]byte(rawBiz))
	sig, err := rsa.SignPKCS1v15(rand.Reader, k.channelPriv, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return `{"alipay_trade_precreate_response":` + rawBiz + `,"sign":"` + base64.StdEncoding.EncodeToString(sig) + `"}`
}

// ============ 微信 Native v3 ============

// 真实下单全链路：Authorization 可验签 + 报文口径正确 + 返回 code_url。
func TestWechatNativeCreateReal(t *testing.T) {
	k := newSDKKeys(t)
	var gotBody, gotAuth, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf, _ := io.ReadAll(r.Body)
		gotBody, gotAuth, gotPath = string(buf), r.Header.Get("Authorization"), r.URL.Path
		m := authRe.FindStringSubmatch(gotAuth)
		if m == nil {
			t.Errorf("Authorization 头格式非法: %q", gotAuth)
			w.WriteHeader(401)
			return
		}
		if m[1] != "1900000109" || m[4] != "SERIAL2026" {
			t.Errorf("Authorization 商户号/序列号错误: %v", m)
		}
		// 待签名串：方法\n路径\n时间戳\n随机串\n报文体\n（路径不含域名与查询串）
		msg := r.Method + "\n" + r.URL.Path + "\n" + m[3] + "\n" + m[2] + "\n" + gotBody + "\n"
		sig, err := base64.StdEncoding.DecodeString(m[5])
		if err != nil {
			t.Errorf("signature 非 base64: %v", err)
			w.WriteHeader(401)
			return
		}
		digest := sha256.Sum256([]byte(msg))
		if err := rsa.VerifyPKCS1v15(k.appPub, crypto.SHA256, digest[:], sig); err != nil {
			t.Errorf("请求签名无法用商户公钥验证: %v", err)
			w.WriteHeader(401)
			return
		}
		if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Errorf("Content-Type 应为 application/json，实际 %q", ct)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"code_url":"weixin://wxpay/bizpayurl?pr=ABC123"}`)
	}))
	defer srv.Close()

	res, err := (&WechatProvider{cfg: wechatCfg(k, srv)}).CreateOrder(&PayRequest{
		OrderNo: "RO42", Amount: 29900, Subject: "能言 积分\n充值", ExpireMinutes: 15,
	})
	if err != nil {
		t.Fatalf("资质齐全时微信下单应成功: %v", err)
	}
	if res.Channel != "wechat" || res.QRContent != "weixin://wxpay/bizpayurl?pr=ABC123" {
		t.Fatalf("出码异常: %+v", res)
	}
	if gotPath != wechatNativePath {
		t.Fatalf("请求路径应为 %s，实际 %s", wechatNativePath, gotPath)
	}
	var body wechatNativeRequest
	if err := json.Unmarshal([]byte(gotBody), &body); err != nil {
		t.Fatalf("下单报文非合法 JSON: %v / %s", err, gotBody)
	}
	if body.AppID != "wxapp0001" || body.MchID != "1900000109" || body.OutTradeNo != "RO42" {
		t.Fatalf("下单报文标识字段错误: %+v", body)
	}
	if body.Amount.Total != 29900 || body.Amount.Currency != "CNY" {
		t.Fatalf("下单金额口径错误（必须分为整数、币种 CNY）: %+v", body.Amount)
	}
	if body.NotifyURL != "https://app.example.com/api/pay/notify/wechat" {
		t.Fatalf("回调地址错误: %s", body.NotifyURL)
	}
	if body.TimeExpire == "" {
		t.Fatal("ExpireMinutes>0 时应传 time_expire")
	}
	if strings.Contains(body.Description, "\n") {
		t.Fatalf("标题应清洗换行: %q", body.Description)
	}
	// 不传有效期时 time_expire 应省略（微信按默认 7 天）
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf, _ := io.ReadAll(r.Body)
		if strings.Contains(string(buf), "time_expire") {
			t.Errorf("ExpireMinutes=0 不应带 time_expire: %s", buf)
		}
		fmt.Fprint(w, `{"code_url":"weixin://wxpay/bizpayurl?pr=Z"}`)
	}))
	defer srv2.Close()
	if _, err := (&WechatProvider{cfg: wechatCfg(k, srv2)}).CreateOrder(&PayRequest{OrderNo: "RO43", Amount: 1}); err != nil {
		t.Fatalf("最小金额下单应成功: %v", err)
	}
}

// 渠道侧拒绝（4xx）：不得吐码，错误里要带渠道 code 便于运维定位。
func TestWechatNativeCreatePropagatesChannelError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"code":"PARAM_ERROR","message":"参数错误"}`)
	}))
	defer srv.Close()
	if _, err := (&WechatProvider{cfg: wechatCfg(newSDKKeys(t), srv)}).CreateOrder(&PayRequest{OrderNo: "RO1", Amount: 100}); err == nil {
		t.Fatal("渠道 4xx 必须报错")
	} else if !strings.Contains(err.Error(), "PARAM_ERROR") {
		t.Fatalf("错误应包含渠道 code: %v", err)
	}
}

// 网络不可达：返回错误而非 panic / 空码。
func TestWechatNativeCreateNetworkFailure(t *testing.T) {
	k := newSDKKeys(t)
	cfg := wechatCfg(k, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})))
	cfg.Wechat.BaseURL = "http://127.0.0.1:1" // 端口必然无监听
	cfg.HTTPClient = &http.Client{}
	if _, err := (&WechatProvider{cfg: cfg}).CreateOrder(&PayRequest{OrderNo: "RO1", Amount: 100}); err == nil {
		t.Fatal("网络失败应报错")
	} else if !strings.Contains(err.Error(), "网络请求失败") {
		t.Fatalf("应归类为网络请求失败: %v", err)
	}
}

// 脏响应（异常 code_url 形态）不得出单。
func TestWechatNativeCreateRejectsBadCodeURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"code_url":"javascript:alert(1)"}`)
	}))
	defer srv.Close()
	if _, err := (&WechatProvider{cfg: wechatCfg(newSDKKeys(t), srv)}).CreateOrder(&PayRequest{OrderNo: "RO1", Amount: 100}); err == nil {
		t.Fatal("异常 code_url 应拒绝出单")
	}
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"code":"NOAUTH"}`)
	}))
	defer srv2.Close()
	if _, err := (&WechatProvider{cfg: wechatCfg(newSDKKeys(t), srv2)}).CreateOrder(&PayRequest{OrderNo: "RO1", Amount: 100}); err == nil {
		t.Fatal("未返回 code_url 应报错")
	}
}

// 资质缺失 fail-closed：逐项列出环境变量名，绝不返回本地拼装码。
func TestWechatMissingCredsListsEnvVars(t *testing.T) {
	_, err := (&WechatProvider{cfg: &Config{}}).CreateOrder(&PayRequest{OrderNo: "RO1", Amount: 100})
	if err == nil {
		t.Fatal("空配置应拒绝出单")
	}
	for _, want := range []string{"PAY_WECHAT_APP_ID", "PAY_WECHAT_MCH_ID", "PAY_WECHAT_SERIAL_NO", "PAY_WECHAT_PRIVATE_KEY", "PAY_WECHAT_APIv3_KEY", "PAY_NOTIFY_BASE"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("缺项提示应包含 %s，实际: %v", want, err)
		}
	}
	// APIv3 密钥长度不对（回调解密会失败）同样视为缺资质
	cfg := &Config{Wechat: WechatConfig{AppID: "a", MchID: "m", SerialNo: "s", PrivateKey: "p", APIv3Key: "short"}}
	if !containsAll(wechatMissingCreds(cfg), "PAY_WECHAT_APIv3_KEY") {
		t.Fatal("APIv3 密钥长度不合规应报缺项")
	}
	// nil 配置不得 panic
	if got := wechatMissingCreds(nil); len(got) == 0 {
		t.Fatal("nil 配置应返回缺项清单")
	}
}

// 回调地址必须是 https 公网地址（微信硬要求，http 会被拒单）。
func TestWechatNotifyURLMustBeHTTPS(t *testing.T) {
	cfg := &Config{Wechat: WechatConfig{
		AppID: "a", MchID: "m", SerialNo: "s", APIv3Key: apiV3Key,
		PrivateKey: "bad", NotifyURL: "http://insecure.example.com/notify", BaseURL: "https://api.mch.weixin.qq.com",
	}}
	if _, err := (&WechatProvider{cfg: cfg}).CreateOrder(&PayRequest{OrderNo: "RO1", Amount: 100}); err == nil {
		t.Fatal("http 回调地址应被拒绝")
	} else if !strings.Contains(err.Error(), "https") {
		t.Fatalf("提示应说明必须 https: %v", err)
	}
}

// 私钥不可用（PEM 内容损坏）时报错而不是 panic 或吐码。
func TestWechatBadPrivateKey(t *testing.T) {
	cfg := &Config{Wechat: WechatConfig{
		AppID: "a", MchID: "m", SerialNo: "s", APIv3Key: apiV3Key,
		PrivateKey: "-----BEGIN PRIVATE KEY-----\n!!!!not base64!!!!\n-----END PRIVATE KEY-----",
		NotifyURL:  "https://app.example.com/api/pay/notify/wechat", BaseURL: "https://api.mch.weixin.qq.com",
	}}
	if _, err := (&WechatProvider{cfg: cfg}).CreateOrder(&PayRequest{OrderNo: "RO1", Amount: 100}); err == nil {
		t.Fatal("私钥不可用应报错")
	}
}

// ============ 支付宝当面付 ============

// 真实下单全链路：网关侧反验我们的 RSA2 签名 + 我们验响应签名 + 返回官方收款码。
func TestAlipayPrecreateReal(t *testing.T) {
	k := newSDKKeys(t)
	var gotForm string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf, _ := io.ReadAll(r.Body)
		gotForm = string(buf)
		params := parseKV(gotForm) // 与真实网关一致：form-urlencoded（值经百分号编码）
		if r.Method != http.MethodPost {
			t.Errorf("应为 POST，实际 %s", r.Method)
		}
		if params["method"] != alipayPrecreateMethod || params["sign_type"] != "RSA2" ||
			params["app_id"] != "2021000000000001" || params["charset"] != "utf-8" || params["version"] != "1.0" {
			t.Errorf("公共参数错误: %v", params)
		}
		if params["notify_url"] != "https://app.example.com/api/pay/notify/alipay" {
			t.Errorf("回调地址未随请求下发: %v", params)
		}
		// 网关侧验签：验不过说明客户端签名串构造有偏差（历史踩坑：百分号编码未还原）。
		// 这里独立复算待签名串，不复用客户端函数——请求签名含 sign_type（与异步通知验签口径不同）。
		if err := verifyAlipayRawSign(k.appPubPEMForVerify(), canonicalFromForm(params), params["sign"]); err != nil {
			t.Errorf("请求 RSA2 验签失败: %v", err)
			w.WriteHeader(400)
			return
		}
		var biz map[string]string
		if err := json.Unmarshal([]byte(params["biz_content"]), &biz); err != nil {
			t.Errorf("biz_content 非合法 JSON: %v", err)
		}
		if biz["out_trade_no"] != "RO9" || biz["total_amount"] != "299.00" ||
			biz["product_code"] != "FACE_TO_FACE_PAYMENT" || biz["timeout_express"] != "15m" {
			t.Errorf("biz_content 字段错误: %v", biz)
		}
		if strings.Contains(biz["subject"], "\n") {
			t.Errorf("subject 应清洗换行: %q", biz["subject"])
		}
		w.Header().Set("Content-Type", "application/json;charset=utf-8")
		fmt.Fprint(w, signAlipayResponse(t, k, `{"code":"10000","msg":"Success","out_trade_no":"RO9","qr_code":"https://qr.alipay.com/bax0abcd"}`))
	}))
	defer srv.Close()

	res, err := (&AlipayProvider{cfg: alipayCfg(k, srv)}).CreateOrder(&PayRequest{
		OrderNo: "RO9", Amount: 29900, Subject: "能言 积分\n充值", ExpireMinutes: 15,
	})
	if err != nil {
		t.Fatalf("资质齐全时支付宝下单应成功: %v", err)
	}
	if res.Channel != "alipay" || res.QRContent != "https://qr.alipay.com/bax0abcd" {
		t.Fatalf("收款码异常: %+v", res)
	}
	if !strings.Contains(gotForm, "sign=") {
		t.Fatalf("请求缺少 sign: %s", gotForm)
	}
}

// 响应被篡改（签名对应另一段原文）必须拒绝出单——这是防劫持换码的关键闸。
func TestAlipayPrecreateRejectsTamperedResponse(t *testing.T) {
	k := newSDKKeys(t)
	signed := `{"code":"10000","msg":"Success","out_trade_no":"RO9","qr_code":"https://qr.alipay.com/real"}`
	tampered := `{"code":"10000","msg":"Success","out_trade_no":"RO9","qr_code":"https://evil.example.com/pay"}`
	// 用「真原文」的合法签名配上「被换成他人收款码」的原文，模拟中间人改包
	validBody := signAlipayResponse(t, k, signed)
	sigOnly := validBody[strings.LastIndex(validBody, `"sign":"`)+8 : len(validBody)-2]
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"alipay_trade_precreate_response":%s,"sign":"%s"}`, tampered, sigOnly)
	}))
	defer srv.Close()
	if _, err := (&AlipayProvider{cfg: alipayCfg(k, srv)}).CreateOrder(&PayRequest{OrderNo: "RO9", Amount: 100}); err == nil {
		t.Fatal("响应验签不通过时必须拒绝出单")
	} else if !strings.Contains(err.Error(), "验签失败") {
		t.Fatalf("应报验签失败: %v", err)
	}
}

// 无 sign 的响应直接拒绝（不给「未签名即放行」留口子）。
func TestAlipayPrecreateRequiresResponseSign(t *testing.T) {
	k := newSDKKeys(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"alipay_trade_precreate_response":{"code":"10000","msg":"Success","out_trade_no":"RO9","qr_code":"https://qr.alipay.com/x"}}`)
	}))
	defer srv.Close()
	if _, err := (&AlipayProvider{cfg: alipayCfg(k, srv)}).CreateOrder(&PayRequest{OrderNo: "RO9", Amount: 100}); err == nil {
		t.Fatal("缺 sign 应拒绝")
	}
}

// 业务码非 10000（未签约 / 参数非法等）拒绝出单并带出 sub_code。
func TestAlipayPrecreateBusinessFailure(t *testing.T) {
	k := newSDKKeys(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, signAlipayResponse(t, k, `{"code":"40004","msg":"Business Failed","sub_code":"ACQ.ACCESS_FORBIDDEN","sub_msg":"未签约该协议"}`))
	}))
	defer srv.Close()
	if _, err := (&AlipayProvider{cfg: alipayCfg(k, srv)}).CreateOrder(&PayRequest{OrderNo: "RO9", Amount: 100}); err == nil {
		t.Fatal("业务失败应报错")
	} else if !strings.Contains(err.Error(), "ACQ.ACCESS_FORBIDDEN") {
		t.Fatalf("错误应带出 sub_code: %v", err)
	}
}

// 串单防护：响应 out_trade_no 与本地订单不一致时拒绝采用。
func TestAlipayPrecreateRejectsOrderMismatch(t *testing.T) {
	k := newSDKKeys(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, signAlipayResponse(t, k, `{"code":"10000","msg":"Success","out_trade_no":"OTHER","qr_code":"https://qr.alipay.com/x"}`))
	}))
	defer srv.Close()
	if _, err := (&AlipayProvider{cfg: alipayCfg(k, srv)}).CreateOrder(&PayRequest{OrderNo: "RO9", Amount: 100}); err == nil {
		t.Fatal("out_trade_no 不符应拒绝")
	}
}

// 支付宝资质缺失同样逐项列名。
func TestAlipayMissingCredsListsEnvVars(t *testing.T) {
	_, err := (&AlipayProvider{cfg: &Config{}}).CreateOrder(&PayRequest{OrderNo: "RO1", Amount: 100})
	if err == nil {
		t.Fatal("空配置应拒绝出单")
	}
	for _, want := range []string{"PAY_ALIPAY_APP_ID", "PAY_ALIPAY_PRIVATE_KEY", "PAY_ALIPAY_PUBLIC_KEY", "PAY_NOTIFY_BASE"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("缺项提示应包含 %s，实际: %v", want, err)
		}
	}
	if got := alipayMissingCreds(nil); len(got) == 0 {
		t.Fatal("nil 配置应返回缺项清单")
	}
}

// 金额与订单校验：金额为 0 / 缺订单号一律拒绝（不落渠道调用）。
func TestGatewayRequestValidation(t *testing.T) {
	k := newSDKKeys(t)
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
	}))
	defer srv.Close()
	if _, err := (&AlipayProvider{cfg: alipayCfg(k, srv)}).CreateOrder(&PayRequest{OrderNo: "", Amount: 100}); err == nil {
		t.Fatal("缺订单号应报错")
	}
	if _, err := (&AlipayProvider{cfg: alipayCfg(k, srv)}).CreateOrder(&PayRequest{OrderNo: "RO1", Amount: 0}); err == nil {
		t.Fatal("金额为 0 应报错")
	}
	if _, err := (&WechatProvider{cfg: wechatCfg(k, srv)}).CreateOrder(&PayRequest{OrderNo: "RO1", Amount: -1}); err == nil {
		t.Fatal("负金额应报错")
	}
	if calls != 0 {
		t.Fatalf("参数非法时不应发起渠道请求，实际调用 %d 次", calls)
	}
}

// ============ 工具函数 ============

func TestGatewayHelpers(t *testing.T) {
	// 分 → 元（两位小数字符串，支付宝 total_amount 口径）
	for _, c := range []struct {
		fen  int64
		want string
	}{{29900, "299.00"}, {1, "0.01"}, {10, "0.10"}, {100, "1.00"}, {123456789, "1234567.89"}} {
		if got := fenToYuan(c.fen); got != c.want {
			t.Errorf("fenToYuan(%d)=%s want %s", c.fen, got, c.want)
		}
	}
	// 回调地址：显式优先，其次 NotifyBase 拼接，都没有则空（触发缺项提示）
	cfg := &Config{NotifyBase: "https://app.example.com/"}
	if got := resolveNotifyURL(cfg, "", "wechat"); got != "https://app.example.com/api/pay/notify/wechat" {
		t.Errorf("NotifyBase 拼接异常: %s", got)
	}
	if got := resolveNotifyURL(cfg, "https://other.example.com/cb", "alipay"); got != "https://other.example.com/cb" {
		t.Errorf("显式地址应优先: %s", got)
	}
	if got := resolveNotifyURL(&Config{}, "", "alipay"); got != "" {
		t.Errorf("无配置应为空: %s", got)
	}
	if got := resolveNotifyURL(nil, "", "wechat"); got != "" {
		t.Errorf("nil 配置应为空: %s", got)
	}
	// JSON 原文节点提取（嵌套对象 + 字符串内含大括号/转义引号都不得误判边界）
	body := `{"a":{"b":{"c":"}"},"d":"x\"{"},"sign":"zz"}`
	if got := extractJSONRawObject(body, "a"); got != `{"b":{"c":"}"},"d":"x\"{"}` {
		t.Errorf("extractJSONRawObject 边界错误: %s", got)
	}
	if got := extractJSONRawObject(body, "missing"); got != "" {
		t.Errorf("不存在的键应为空: %s", got)
	}
	if got := extractJSONRawObject(`{"a":1}`, "a"); got != "" {
		t.Errorf("非对象取值应为空: %s", got)
	}
	// 待签名串排序（排除 sign 与空值，字典序；★ sign_type 参与网关请求签名，
	// 与异步通知验签「排除 sign 与 sign_type」的口径不同，改错任何一侧都会验签失败）
	q := alipayCanonicalQuery(map[string]string{
		"method": "alipay.trade.precreate", "app_id": "2021", "empty": "", "sign": "x", "charset": "utf-8",
	})
	if q != "app_id=2021&charset=utf-8&method=alipay.trade.precreate" {
		t.Errorf("待签名串错误: %q", q)
	}
	if !strings.Contains(alipayCanonicalQuery(map[string]string{"sign_type": "RSA2", "sign": "x"}), "sign_type=RSA2") {
		t.Error("网关请求待签名串必须包含 sign_type")
	}
	// 回调侧口径由 TestAlipayProvider 覆盖（signAlipayForm 排除 sign 与 sign_type），两侧不可混用
	// 私钥三种写法：PKCS#8 PEM、PKCS#1 PEM、裸 base64
	k := newSDKKeys(t)
	if _, err := parseRSAPrivateKey(k.appPrivPEM); err != nil {
		t.Errorf("PKCS#8 私钥解析失败: %v", err)
	}
	priv, err := parseRSAPrivateKey(k.appPrivPEM)
	if err != nil {
		t.Fatal(err)
	}
	pk1 := x509.MarshalPKCS1PrivateKey(priv)
	if _, err := parseRSAPrivateKey(string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: pk1}))); err != nil {
		t.Errorf("PKCS#1 私钥解析失败: %v", err)
	}
	if _, err := parseRSAPrivateKey(base64.StdEncoding.EncodeToString(pk1)); err != nil {
		t.Errorf("裸 base64（PKCS#1）私钥解析失败: %v", err)
	}
	// 环境变量注入的裸 PKCS#8 base64（支付宝控制台导出形态）
	pk8, _ := x509.MarshalPKCS8PrivateKey(priv)
	if _, err := parseRSAPrivateKey(base64.StdEncoding.EncodeToString(pk8)); err != nil {
		t.Errorf("裸 base64（PKCS#8）私钥解析失败: %v", err)
	}
	// 字面 \n（systemd / .env 常见写法）
	oneLine := strings.ReplaceAll(k.appPrivPEM, "\n", `\n`)
	if _, err := parseRSAPrivateKey(oneLine); err != nil {
		t.Errorf("字面 \\n 私钥应可还原解析: %v", err)
	}
	for _, bad := range []string{"", "   ", "not-a-key", "-----BEGIN PRIVATE KEY-----\n!!!\n-----END PRIVATE KEY-----"} {
		if _, err := parseRSAPrivateKey(bad); err == nil {
			t.Errorf("非法私钥应报错: %q", bad)
		}
	}
	// 错误文案清洗与截断
	if got := sanitizeHint("a\n\nb"); got != " a b" {
		t.Errorf("sanitizeHint: %q", got)
	}
	if got := sanitizeHint(""); got != "" {
		t.Errorf("空文本应为空: %q", got)
	}
	if got := sanitizeHint(strings.Repeat("长", 400)); len([]rune(got)) > 205 {
		t.Errorf("未限长: %d 字", len([]rune(got)))
	}
	if got := cleanSubject(""); got != "能言积分充值" {
		t.Errorf("空标题应有默认值: %q", got)
	}
	if got := cleanSubject("能言\n积分  充值"); got != "能言 积分 充值" {
		t.Errorf("标题清洗错误: %q", got)
	}
	if got := truncateRunes("能言积分充值", 4); got != "能言积分" {
		t.Errorf("应按字符截断: %q", got)
	}
	if got := truncateRunes("abc", 10); got != "abc" {
		t.Errorf("未超长应原样: %q", got)
	}
	// 随机串：长度与唯一性
	h1, err := randomHex(16)
	if err != nil || len(h1) != 32 {
		t.Fatalf("randomHex 异常: %q %v", h1, err)
	}
	h2, _ := randomHex(16)
	if h1 == h2 {
		t.Fatal("randomHex 重复")
	}
	// 默认 HTTP 客户端带超时（出码是同步等待路径，不能无限挂）
	if payHTTPClient(&Config{}).Timeout != payHTTPTimeout {
		t.Error("默认支付客户端应带超时")
	}
	c := &http.Client{}
	if payHTTPClient(&Config{HTTPClient: c}) != c {
		t.Error("应优先使用注入的 HTTPClient")
	}
}

// appPubPEMForVerify 把夹具里的应用公钥导出为 PEM（假网关侧验我们的请求签名用）。
func (k *sdkKeys) appPubPEMForVerify() string {
	der, _ := x509.MarshalPKIXPublicKey(k.appPub)
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

// canonicalFromForm 独立复算支付宝网关待签名串（除 sign 外全部非空参数按 ASCII 升序拼 k=v&k=v）。
// 刻意不 import 客户端同名逻辑，保证「我们怎么拼」与「渠道怎么验」两侧各写一遍、能真正对上。
func canonicalFromForm(params map[string]string) string {
	keys := make([]string, 0, len(params))
	for k, v := range params {
		if k == "sign" || v == "" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, k+"="+params[k])
	}
	return strings.Join(pairs, "&")
}

// containsAll 判断拼接后的清单是否包含某项。
func containsAll(items []string, want string) bool {
	for _, s := range items {
		if strings.Contains(s, want) {
			return true
		}
	}
	return false
}
