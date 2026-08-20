// ============ 本文件职责中文说明 ============
// 支付网关单元测试：适配器选择 / mock 全链路 / wechat 与 alipay 骨架校验 /
// 金额解析（表单与 JSON、元与分）/ 工具函数（HMAC 签名 / JSON 提取 / 键排序）。
// ========================================
package payment

import (
	"strings"
	"testing"
)

// NewProvider 模式选择：未知回退 mock，wechat/alipay 正确创建。
func TestNewProviderMode(t *testing.T) {
	cases := []struct {
		mode string
		want string
	}{
		{"", "mock"},
		{"unknown", "mock"},
		{"wechat", "wechat"},
		{"alipay", "alipay"},
	}
	for _, c := range cases {
		p := NewProvider(&Config{Mode: c.mode})
		var got string
		switch p.(type) {
		case *MockProvider:
			got = "mock"
		case *WechatProvider:
			got = "wechat"
		case *AlipayProvider:
			got = "alipay"
		}
		if got != c.want {
			t.Errorf("mode=%q 适配器应为 %s, got %s", c.mode, c.want, got)
		}
	}
	// nil 配置回退 mock
	if _, ok := NewProvider(nil).(*MockProvider); !ok {
		t.Fatal("nil 配置应回退 mock")
	}
}

// mock 全链路：下单→回调→验签。
func TestMockProviderFullFlow(t *testing.T) {
	p := &MockProvider{}
	res, err := p.CreateOrder(&PayRequest{OrderNo: "RO123", Amount: 5000, Subject: "充值"})
	if err != nil || res.Channel != "mock" {
		t.Fatalf("mock 下单失败: %v %+v", err, res)
	}
	if !strings.HasPrefix(res.QRContent, "mockpay://RO123") {
		t.Fatalf("mock 二维码内容异常: %s", res.QRContent)
	}
	// JSON 报文回调（amount 为数字）
	n, err := p.VerifyNotify([]byte(`{"order_no":"RO123","trade_no":"T1","amount":5000}`), nil)
	if err != nil || n.OrderNo != "RO123" || n.Amount != 5000 || !n.Verified {
		t.Fatalf("mock 回调解析失败: %v %+v", err, n)
	}
	// 表单报文回调
	n2, err := p.VerifyNotify([]byte("order_no=RO123&trade_no=T2&amount=3000"), nil)
	if err != nil || n2.Amount != 3000 {
		t.Fatalf("mock 表单回调失败: %v %+v", err, n2)
	}
	// 缺少订单号应报错
	if _, err := p.VerifyNotify([]byte(`{"amount":100}`), nil); err == nil {
		t.Fatal("缺少 order_no 应报错")
	}
}

// mock 下单缺订单号应报错。
func TestMockProviderCreateMissingOrderNo(t *testing.T) {
	p := &MockProvider{}
	if _, err := p.CreateOrder(&PayRequest{Amount: 100}); err == nil {
		t.Fatal("缺订单号应报错")
	}
	if _, err := p.CreateOrder(nil); err == nil {
		t.Fatal("nil 请求应报错")
	}
}

// wechat 未配置资质应报错（引导回退 mock）；已配置可下单。
func TestWechatProvider(t *testing.T) {
	p := &WechatProvider{cfg: &Config{}}
	if _, err := p.CreateOrder(&PayRequest{OrderNo: "RO1", Amount: 100}); err == nil {
		t.Fatal("未配置微信资质应报错")
	}
	// 已配置
	p2 := &WechatProvider{cfg: &Config{Wechat: WechatConfig{AppID: "app", MchID: "mch", APIv3Key: "key"}}}
	res, err := p2.CreateOrder(&PayRequest{OrderNo: "RO1", Amount: 100})
	if err != nil || res.Channel != "wechat" {
		t.Fatalf("已配置微信下单失败: %v", err)
	}
	// 回调验签（骨架）
	n, err := p2.VerifyNotify([]byte(`{"order_no":"RO1","amount":100}`), nil)
	if err != nil || n.OrderNo != "RO1" || !n.Verified {
		t.Fatalf("微信回调失败: %v %+v", err, n)
	}
}

// alipay 未配置资质应报错；已配置可下单。
func TestAlipayProvider(t *testing.T) {
	p := &AlipayProvider{cfg: &Config{}}
	if _, err := p.CreateOrder(&PayRequest{OrderNo: "RO1", Amount: 100}); err == nil {
		t.Fatal("未配置支付宝资质应报错")
	}
	p2 := &AlipayProvider{cfg: &Config{Alipay: AlipayConfig{AppID: "app", PrivateKey: "pk", PublicKey: "pubkey"}}}
	res, err := p2.CreateOrder(&PayRequest{OrderNo: "RO1", Amount: 100})
	if err != nil || res.Channel != "alipay" {
		t.Fatalf("已配置支付宝下单失败: %v", err)
	}
	n, err := p2.VerifyNotify([]byte(`{"out_trade_no":"RO1","total_amount":"1.00"}`), nil)
	if err != nil || n.OrderNo != "RO1" || n.Amount != 100 {
		t.Fatalf("支付宝回调失败: %v %+v", err, n)
	}
}

// parseAmount：整数分 / 小数元 / JSON 数字与字符串。
func TestParseAmount(t *testing.T) {
	cases := []struct {
		fieldVal, body string
		want           int64
	}{
		{"5000", "", 5000},                     // 表单：整数分
		{"1.00", "", 100},                      // 表单：元 → 分
		{"", `{"amount":5000}`, 5000},          // JSON：数字整数分
		{"", `{"amount":"5000"}`, 5000},        // JSON：字符串整数分
		{"", `{"total_amount":"10.00"}`, 1000}, // JSON：元 → 分
		{"", `{}`, 0},                          // 无金额
	}
	for _, c := range cases {
		if got := parseAmount(c.fieldVal, c.body); got != c.want {
			t.Errorf("parseAmount(%q,%q)=%d want %d", c.fieldVal, c.body, got, c.want)
		}
	}
}

// extractJSONStr 字符串字段提取。
func TestExtractJSONStr(t *testing.T) {
	body := `{"order_no":"RO123","amount":5000}`
	if got := extractJSONStr(body, "order_no"); got != "RO123" {
		t.Fatalf("extractJSONStr 结果异常: %q", got)
	}
	if got := extractJSONStr(body, "missing"); got != "" {
		t.Fatal("缺失字段应返回空串")
	}
}

// extractJSONNum 数值字段提取（数字 / 字符串 / 缺失）。
func TestExtractJSONNum(t *testing.T) {
	if got := extractJSONNum(`{"amount":5000}`, "amount"); got != "5000" {
		t.Fatalf("数字提取异常: %q", got)
	}
	if got := extractJSONNum(`{"amount":"5000"}`, "amount"); got != "5000" {
		t.Fatalf("字符串数字提取异常: %q", got)
	}
	if got := extractJSONNum(`{"other":1}`, "amount"); got != "" {
		t.Fatal("缺失字段应返回空串")
	}
}

// SignHMAC 稳定性 + SortedKeys 排序。
func TestSignAndKeys(t *testing.T) {
	if SignHMAC("k", []byte("payload")) != SignHMAC("k", []byte("payload")) {
		t.Fatal("相同输入签名应一致")
	}
	if SignHMAC("k1", []byte("p")) == SignHMAC("k2", []byte("p")) {
		t.Fatal("不同密钥签名应不同")
	}
	keys := SortedKeys(map[string]string{"b": "1", "a": "2", "c": "3"})
	if len(keys) != 3 || keys[0] != "a" || keys[1] != "b" || keys[2] != "c" {
		t.Fatalf("SortedKeys 未排序: %v", keys)
	}
}
