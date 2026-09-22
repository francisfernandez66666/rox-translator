// ============ pay_channels_test.go · 职责说明 ============
// 支付渠道凭据「管理台可配 + 加密落库」改造（2026-09-22）的 api 层断言，钉死三条口径：
//
//	① 取值优先级 = 环境变量 > 数据库配置：env 有值时压住库值（应急/灰度闸门），
//	   env 置空后同一字段自动回落到管理台配置；
//	② 配置不全 fail-closed：库里有半套凭据时下单必须报错，且渠道适配器不得被静默
//	   降级成 mock（给出可扫的 mockpay:// 废码比不出码更贵，见 #41 整改）；
//	③ 管理台保存链路不把掩码写回库：原样回提 "********" 后真值仍可解密读出。
//
// ★ 单测自钉 sqlite 方言（AGENTS.md §4）；环境变量用 t.Setenv 以便自动还原，
//
//	需要「显式置空」的用例走 os.Setenv + defer 还原（t.Setenv 无法置空）。
//
// 运行：env DB_DRIVER=sqlite go test -count=1 ./internal/api/ -run TestPayChannel
// ==========================================
package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"translator/internal/auth"
	"translator/internal/config"
	"translator/internal/payment"
	"translator/internal/store"

	_ "modernc.org/sqlite"
)

// payCfgPayEnvs 本组用例会碰到的支付环境变量（逐个置空，防宿主 shell 残留值干扰断言）。
var payCfgPayEnvs = []string{
	"PAY_NOTIFY_BASE", "PAY_WECHAT_APP_ID", "PAY_WECHAT_MCH_ID", "PAY_WECHAT_SERIAL_NO",
	"PAY_WECHAT_APIv3_KEY", "PAY_WECHAT_PRIVATE_KEY", "PAY_WECHAT_NOTIFY_URL",
	"PAY_ALIPAY_APP_ID", "PAY_ALIPAY_PRIVATE_KEY", "PAY_ALIPAY_PUBLIC_KEY",
	"PAY_ALIPAY_SELLER_ID", "PAY_ALIPAY_GATEWAY", "PAY_ALIPAY_NOTIFY_URL",
}

// clearPayEnvs 把全部支付环境变量置空并在用例结束后还原。
func clearPayEnvs(t *testing.T) {
	t.Helper()
	for _, k := range payCfgPayEnvs {
		old := os.Getenv(k)
		_ = os.Setenv(k, "")
		t.Cleanup(func() { _ = os.Setenv(k, old) })
	}
}

// newPayCfgServer 内存 SQLite + 平台超管 JWT 的最小服务栈（不接 Ten，支付配置口不用租户表）。
func newPayCfgServer(t *testing.T) (*Server, string) {
	t.Helper()
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })

	raw, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("打开内存数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	st, err := store.New(raw)
	if err != nil {
		t.Fatalf("创建 Store 失败: %v", err)
	}
	super, err := st.CreateUser(0, "paych_super", "hash", "支付配置超管", store.RoleSuperAdmin, 0, 0)
	if err != nil {
		t.Fatalf("创建超管失败: %v", err)
	}
	tok, err := auth.Sign(super, time.Hour)
	if err != nil {
		t.Fatalf("签发 JWT 失败: %v", err)
	}
	return &Server{Store: st}, tok
}

// payCfgMux 本次改造新增的两个管理台路由（与生产注册同 handler）。
func (s *Server) payCfgMux() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("/api/admin/pay/channels", s.handleAdminPayChannels)
	m.HandleFunc("/api/admin/pay/channels/save", s.handleAdminPayChannelsSave)
	return m
}

// TestPayGatewayConfigEnvBeatsDatabase 断言①：环境变量 > 数据库配置。
func TestPayGatewayConfigEnvBeatsDatabase(t *testing.T) {
	clearPayEnvs(t)
	srv, _ := newPayCfgServer(t)
	if err := srv.Store.SetPayConfigField(store.PayConfigWechatMchID, "db-mch"); err != nil {
		t.Fatalf("写库配置失败: %v", err)
	}
	if err := srv.Store.SetPayConfigField(store.PayConfigWechatAppID, "db-appid"); err != nil {
		t.Fatalf("写库配置失败: %v", err)
	}
	// env 只给商户号：其余字段必须继续回落库值（不能因为 env 存在就整体忽略库配置）
	t.Setenv("PAY_WECHAT_MCH_ID", "env-mch")
	if got := srv.payGatewayConfig().Wechat.MchID; got != "env-mch" {
		t.Fatalf("env 应压住库配置，got=%q", got)
	}
	if got := srv.payGatewayConfig().Wechat.AppID; got != "db-appid" {
		t.Fatalf("未被 env 覆盖的字段应取库值，got=%q", got)
	}
	// env 置空（运维撤掉应急注入）后，同一字段自动回到管理台配置——无需改代码或重启流程外的动作
	_ = os.Setenv("PAY_WECHAT_MCH_ID", "")
	if got := srv.payGatewayConfig().Wechat.MchID; got != "db-mch" {
		t.Fatalf("env 清空后应回落库配置，got=%q", got)
	}
}

// TestPayChannelFailClosedOnIncompleteConfig 断言②：配置不全/被停用时拒绝出单且不降级 mock。
func TestPayChannelFailClosedOnIncompleteConfig(t *testing.T) {
	clearPayEnvs(t)
	srv, _ := newPayCfgServer(t)
	// 只配了 AppID：商户号/序列号/私钥/APIv3 密钥/回调地址全缺——必须 fail-closed
	if err := srv.Store.SetPayConfigField(store.PayConfigWechatAppID, "wx-appid"); err != nil {
		t.Fatalf("写库配置失败: %v", err)
	}
	prov := srv.payProviderFor("wechat")
	if _, ok := prov.(*payment.MockProvider); ok {
		t.Fatal("微信渠道被静默降级成 mock——配置不全时必须仍是 WechatProvider")
	}
	res, err := prov.CreateOrder(&payment.PayRequest{OrderNo: "RO_TEST_1", Amount: 100})
	if err == nil {
		t.Fatalf("配置不全却出单成功: %+v", res)
	}
	if !strings.Contains(err.Error(), "资质未配置") {
		t.Fatalf("缺项提示缺失，运维无法照做补齐: %v", err)
	}
	// 管理台显式停用：即使凭据齐全也不得出单，且错误要能被上层识别为「可执行提示」
	if err := srv.Store.SetPayConfigField(store.PayConfigWechatEnabled, "0"); err != nil {
		t.Fatalf("停用渠道失败: %v", err)
	}
	_, err = srv.payProviderFor("wechat").CreateOrder(&payment.PayRequest{OrderNo: "RO_TEST_2", Amount: 100})
	if err == nil {
		t.Fatal("渠道停用后仍放行下单")
	}
	if !errors.Is(err, payment.ErrPayChannelDisabled) {
		t.Fatalf("停用错误应为哨兵错误，便于上层给出可执行提示: %v", err)
	}
	if hint := payPublicHint(err); !strings.Contains(hint, "停用") {
		t.Fatalf("停用提示应原样透传给前端，got=%q", hint)
	}
}

// TestAdminPayChannelsSaveKeepsMaskedSecret 断言③：管理台回提掩码不会冲掉真密钥。
func TestAdminPayChannelsSaveKeepsMaskedSecret(t *testing.T) {
	clearPayEnvs(t)
	srv, tok := newPayCfgServer(t)
	const realKey = "0123456789abcdef0123456789abcdef" // APIv3 密钥需 32 字节
	post := func(t *testing.T, body map[string]interface{}) *httptest.ResponseRecorder {
		t.Helper()
		b, _ := json.Marshal(body)
		r := httptest.NewRequest(http.MethodPost, "/api/admin/pay/channels/save", bytes.NewReader(b))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+tok)
		rec := httptest.NewRecorder()
		srv.payCfgMux().ServeHTTP(rec, r)
		return rec
	}
	// 首次保存：写真值
	if rec := post(t, map[string]interface{}{"fields": map[string]string{
		store.PayConfigWechatAPIv3Key: realKey,
		store.PayConfigWechatMchID:    "1900000001",
	}}); rec.Code != 200 {
		t.Fatalf("首次保存失败: %d %s", rec.Code, rec.Body.String())
	}
	if got := srv.Store.GetPayChannelConfig().Wechat.APIv3Key; got != realKey {
		t.Fatalf("真值未入库，got=%q", got)
	}
	// GET 回显必须是掩码（含 ****），且不含真值片段
	recGet := httptest.NewRequest(http.MethodGet, "/api/admin/pay/channels", nil)
	recGet.Header.Set("Authorization", "Bearer "+tok)
	recW := httptest.NewRecorder()
	srv.payCfgMux().ServeHTTP(recW, recGet)
	if recW.Code != 200 {
		t.Fatalf("回显接口失败: %d %s", recW.Code, recW.Body.String())
	}
	var echo struct {
		Success bool              `json:"success"`
		Fields  map[string]string `json:"fields"`
	}
	if err := json.Unmarshal(recW.Body.Bytes(), &echo); err != nil {
		t.Fatalf("回显解析失败: %v", err)
	}
	masked := echo.Fields[store.PayConfigWechatAPIv3Key]
	if !strings.Contains(masked, "****") || strings.Contains(masked, realKey) {
		t.Fatalf("敏感项未按掩码回显，got=%q", masked)
	}
	// 二次保存：表单原样回提掩码 + 只改商户号——APIv3 密钥必须保持真值
	if rec := post(t, map[string]interface{}{"fields": map[string]string{
		store.PayConfigWechatAPIv3Key: masked,
		store.PayConfigWechatMchID:    "1900000002",
	}}); rec.Code != 200 {
		t.Fatalf("二次保存失败: %d %s", rec.Code, rec.Body.String())
	}
	cfg := srv.Store.GetPayChannelConfig()
	if cfg.Wechat.APIv3Key != realKey {
		t.Fatalf("掩码被写回库，真密钥丢失: %q", cfg.Wechat.APIv3Key)
	}
	if cfg.Wechat.MchID != "1900000002" {
		t.Fatalf("非敏感字段应正常更新，got=%q", cfg.Wechat.MchID)
	}
	// 非法开关值整批拒写（校验先行，不留半套凭据）
	if rec := post(t, map[string]interface{}{"fields": map[string]string{
		store.PayConfigWechatEnabled: "yes",
	}}); rec.Code != 400 {
		t.Fatalf("非法开关值未被拒绝: %d %s", rec.Code, rec.Body.String())
	}
	// 未登录/非超管一律 403
	recAnon := httptest.NewRecorder()
	srv.payCfgMux().ServeHTTP(recAnon, httptest.NewRequest(http.MethodGet, "/api/admin/pay/channels", nil))
	if recAnon.Code != 403 {
		t.Fatalf("匿名读取未被拒绝: %d", recAnon.Code)
	}
}

// TestAdminPayChannelsSaveRejectsUnknownKeyAtomically 断言④：整批保存必须「校验先行」。
//
// 复现的缺陷：未知键的拒绝动作原先发生在 SetPayConfigField（写库循环内），于是
// 一次提交里合法字段先落了库、后面才因未知键报 400 —— 管理员看到 400 以为整单没生效，
// 实际渠道配置已被改了一半（半套凭据是收款链路最难排查的形态）。
// 键序刻意让合法键排在未知键之前（排序后 paych_wechat_* < paych_zzz_*），
// 这样一旦回归成「边写边校验」，本用例必红。
func TestAdminPayChannelsSaveRejectsUnknownKeyAtomically(t *testing.T) {
	clearPayEnvs(t)
	srv, tok := newPayCfgServer(t)
	post := func(body map[string]interface{}) *httptest.ResponseRecorder {
		b, _ := json.Marshal(body)
		r := httptest.NewRequest(http.MethodPost, "/api/admin/pay/channels/save", bytes.NewReader(b))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+tok)
		rec := httptest.NewRecorder()
		srv.payCfgMux().ServeHTTP(rec, r)
		return rec
	}
	rec := post(map[string]interface{}{"fields": map[string]string{
		store.PayConfigWechatMchID: "1900000009",       // 合法键，排序在前
		"paych_zzz_not_a_field":    "must-not-persist", // 未知键，排序在后
	}})
	if rec.Code == 200 {
		t.Fatalf("未知配置项未被拒绝: %s", rec.Body.String())
	}
	// 关键断言：整批不落写，合法键也必须是未配置态
	if got := srv.Store.GetPayChannelConfig().Wechat.MchID; got != "" {
		t.Fatalf("未知键导致整单失败时，合法键不得被写入（半套凭据），got=%q", got)
	}
	// 只提交合法键时应正常落库（证明上一条失败不是接口本身不通）
	if rec := post(map[string]interface{}{"fields": map[string]string{
		store.PayConfigWechatMchID: "1900000010",
	}}); rec.Code != 200 {
		t.Fatalf("合法保存被拒: %d %s", rec.Code, rec.Body.String())
	}
	if got := srv.Store.GetPayChannelConfig().Wechat.MchID; got != "1900000010" {
		t.Fatalf("合法字段未落库，got=%q", got)
	}
}
