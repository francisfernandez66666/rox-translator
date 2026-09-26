// ============ 本文件职责中文说明 ============
// 多币种报价 API（★ #75，2026-09-23，api/quote_currency.go + plans_api.go 出参）单测：
//
//	① 超管配置口 POST/GET /api/admin/config/quote-currency：
//	   币种白名单 + 倍率防呆（>0 且 <1000）+「选了就必须有汇率」的联动校验，
//	   非法值整体拒绝并回可读中文 message；鉴权只放平台超管（L4 + IsSuperAdmin）。
//	② 报价出参：GET /api/plans 增 quote_currency / fx_rates_snapshot，
//	   每套餐增 price_display（本币，2 位）与 price_cny；★ 老字段一个不少
//	   （price_money/code/ptype/points... 原样保留，前端与 e2e 依赖）。
//	③ 下单汇率快照：stampOrderQuote——CNY 单 rate=1；USD 单 money_cny 与
//	   amount_money 一致（人民币事实源不漂）；未知币种回落 CNY 不留脏值。
//	④ ★ 2026-09-22 决策后出厂态为关闭封存：①–③ 用例内显式翻开 store 报价开关验证
//	   「重开后」链路；TestQuoteConfigClosedRejectsForeign 钉死关闭态行为
//	   （外币保存 400「暂未开放」、GET feature_open=false 且白名单仅 CNY）。
//
// ★ 单测自钉 sqlite 方言（AGENTS.md §4）+ t.Setenv 钉死报价环境变量：
//
//	config.Default() 读 DB_DRIVER 且副作用写全局 config.C，run_uat 的 PG 模式下
//	会把方言泄漏给同包内存 SQLite 用例（历史两次踩坑），必须钉底。
//	运行：env DB_DRIVER=sqlite go test -count=1 ./internal/api/ -run TestQuoteCurrency
//
// ==========================================
package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"translator/internal/auth"
	"translator/internal/config"
	"translator/internal/db"
	"translator/internal/store"

	_ "modernc.org/sqlite"
)

// newQuoteServer 内存 SQLite + 平台超管 JWT 的最小服务栈（同 pay_channels_test 思路）。
// 返回 Server 与超管 token；t.Setenv 钉死报价环境变量为空（防宿主 shell 残留值翻红）。
func newQuoteServer(t *testing.T) (*Server, string) {
	t.Helper()
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })
	t.Setenv("QUOTE_CURRENCY", "")
	t.Setenv("FX_RATES", "")

	raw, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("打开内存数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	st, err := store.New(raw)
	if err != nil {
		t.Fatalf("创建 Store 失败: %v", err)
	}
	super, err := st.CreateUser(0, "quote_super", "hash", "报价配置超管", store.RoleSuperAdmin, 0, 0)
	if err != nil {
		t.Fatalf("创建超管失败: %v", err)
	}
	tok, err := auth.Sign(super, time.Hour)
	if err != nil {
		t.Fatalf("签发 JWT 失败: %v", err)
	}
	return &Server{Store: st}, tok
}

// quoteMux 报价配置口 + 公开定价页（与生产注册同 handler）。
func (s *Server) quoteMux() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("/api/admin/config/quote-currency", s.handleAdminQuoteCurrency)
	m.HandleFunc("/api/plans", s.handlePlans)
	return m
}

// doJSON 发一次带 JWT 的请求并返回响应解析结果。
func doJSON(t *testing.T, h http.Handler, method, path, tok string, body interface{}) (int, map[string]interface{}) {
	t.Helper()
	var r *http.Request
	if body != nil {
		b, _ := json.Marshal(body)
		r = httptest.NewRequest(method, path, bytes.NewReader(b))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	if tok != "" {
		r.Header.Set("Authorization", "Bearer "+tok)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var out map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

// ① 配置口：校验先行、整批落库；非法值整体拒绝并回中文 message。
// （验证开放态链路——★ 2026-09-22 决策后出厂关闭，本用例显式翻开 store 开关。）
func TestQuoteCurrencyConfigRoundTrip(t *testing.T) {
	t.Cleanup(store.SetQuoteFeatureOpen(true))
	srv, tok := newQuoteServer(t)
	mux := srv.quoteMux()

	// 合法保存：币种 + 倍率一次配齐
	code, resp := doJSON(t, mux, http.MethodPost, "/api/admin/config/quote-currency", tok,
		map[string]interface{}{"currency": "USD", "rates": map[string]float64{"USD": 7.2}})
	if code != 200 {
		t.Fatalf("合法保存应 200，got %d %+v", code, resp)
	}
	// GET 回显生效值
	code, resp = doJSON(t, mux, http.MethodGet, "/api/admin/config/quote-currency", tok, nil)
	if code != 200 {
		t.Fatalf("回显应 200，got %d", code)
	}
	if resp["currency"] != "USD" {
		t.Fatalf("回显币种错误: %+v", resp)
	}

	// 非白名单币种 → 400 + 中文可读 message
	code, resp = doJSON(t, mux, http.MethodPost, "/api/admin/config/quote-currency", tok,
		map[string]interface{}{"currency": "BTC", "rates": map[string]float64{"BTC": 1}})
	if code != 400 {
		t.Fatalf("非白名单币种应 400，got %d", code)
	}
	if msg, _ := resp["message"].(string); msg == "" || !containsChinese(msg) {
		t.Fatalf("拒绝 message 应为可读中文，got=%q", msg)
	}
	// 倍率防呆：0 / 负数 / ≥1000 一律拒绝
	for _, bad := range []float64{0, -1, 1000, 99999} {
		code, _ = doJSON(t, mux, http.MethodPost, "/api/admin/config/quote-currency", tok,
			map[string]interface{}{"currency": "USD", "rates": map[string]float64{"USD": bad}})
		if code != 400 {
			t.Fatalf("倍率 %v 应被拒绝（>0 且 <1000 防呆），got %d", bad, code)
		}
	}
	// 选了非 CNY 却没配倍率 → 整体拒绝（不留「配了不用」的死配置）
	code, _ = doJSON(t, mux, http.MethodPost, "/api/admin/config/quote-currency", tok,
		map[string]interface{}{"currency": "EUR", "rates": map[string]float64{"USD": 7.2}})
	if code != 400 {
		t.Fatalf("币种缺倍率应 400，got %d", code)
	}
	// 非法请求整批拒绝后，库内配置不应被半写（上一步的 USD 7.2 仍应完整生效）
	if cur := srv.Store.QuoteCurrencyCfg(); cur.Currency != "USD" || cur.Rates["USD"] != 7.2 {
		t.Fatalf("非法保存不应污染已有配置: %+v", cur)
	}
	// 回落 CNY（清空倍率 + 币种切回）
	code, _ = doJSON(t, mux, http.MethodPost, "/api/admin/config/quote-currency", tok,
		map[string]interface{}{"currency": "CNY", "rates": map[string]float64{}})
	if code != 200 {
		t.Fatalf("切回 CNY 应 200")
	}
	if cur := srv.Store.QuoteCurrencyCfg(); cur.Currency != "CNY" {
		t.Fatalf("应已切回 CNY，got=%+v", cur)
	}
	// 鉴权闸门分流：匿名＝401、越权＝403（★ 批 I-10 收尾订正：旧实现两支都回 403，
	// 与本批「未登录/无权限必须分流」的口径冲突——前端只在 401 走重登录）
	if code, _ = doJSON(t, mux, http.MethodGet, "/api/admin/config/quote-currency", "", nil); code != 401 {
		t.Fatalf("匿名读取报价配置应 401，got %d", code)
	}
}

// ④ 关闭态（出厂封存，2026-09-22 决策）：外币保存必须 400 且话术讲明"暂未开放"；
// GET 回显 feature_open=false、币种恒 CNY、白名单只露 CNY（管理台据此隐藏报价区块）。
// 鉴权口不受开关影响：无论开放/封存，匿名都是 401、非超管都是 403（分流见上面那条锁）。
func TestQuoteConfigClosedRejectsForeign(t *testing.T) {
	t.Cleanup(store.SetQuoteFeatureOpen(false))
	srv, tok := newQuoteServer(t)
	mux := srv.quoteMux()

	code, resp := doJSON(t, mux, http.MethodPost, "/api/admin/config/quote-currency", tok,
		map[string]interface{}{"currency": "USD", "rates": map[string]float64{"USD": 7.2}})
	if code != 400 {
		t.Fatalf("关闭态保存外币币种应 400，got %d %+v", code, resp)
	}
	if msg, _ := resp["message"].(string); !strings.Contains(msg, "暂未开放") {
		t.Fatalf("拒绝话术应讲明功能未开放，got=%q", msg)
	}
	// CNY 表态仍放行（关闭≠把接口打死；保存动作不该无意义报错）
	if code, _ = doJSON(t, mux, http.MethodPost, "/api/admin/config/quote-currency", tok,
		map[string]interface{}{"currency": "CNY", "rates": map[string]float64{}}); code != 200 {
		t.Fatalf("关闭态保存 CNY 应 200，got %d", code)
	}
	// GET 回显：生效 CNY + feature_open=false + 白名单仅 CNY
	code, resp = doJSON(t, mux, http.MethodGet, "/api/admin/config/quote-currency", tok, nil)
	if code != 200 || resp["currency"] != "CNY" || resp["feature_open"] != false {
		t.Fatalf("关闭态回显错误: %d %+v", code, resp)
	}
	if sup, _ := resp["supported_currencies"].([]interface{}); len(sup) != 1 || sup[0] != "CNY" {
		t.Fatalf("关闭态白名单应仅 CNY，got=%v", sup)
	}
	// 公开定价出参同步收敛：quote_currency 恒 CNY，price_display==price_money（不漂数）
	if _, err := srv.Store.CreatePackage(&store.Package{
		TenantID: 0, Code: "pro_month", Name: "包月专业版", PType: store.PackagePaid,
		Points: 1000, PriceMoney: 299.0, DurationDays: 30, Enabled: 1,
	}); err != nil {
		t.Fatalf("建套餐失败: %v", err)
	}
	code, resp = doJSON(t, mux, http.MethodGet, "/api/plans", "", nil)
	if code != 200 || resp["quote_currency"] != "CNY" {
		t.Fatalf("关闭态 /api/plans 应回 CNY 报价: %d %+v", code, resp)
	}
	row, _ := resp["plans"].([]interface{})[0].(map[string]interface{})
	if row["price_display"] != 299.0 || row["price_cny"] != 299.0 {
		t.Fatalf("关闭态 price_display 应恒等于人民币原价: %+v", row)
	}
}

// ② 报价出参：/api/plans 增 price_display / price_cny / quote_currency / fx_rates_snapshot，
// 老字段一个不少、语义不变（★ 红线：price_money 仍是人民币元）。
// （验证开放态链路——用例内显式翻开 store 开关。）
func TestPlansQuoteDisplayFields(t *testing.T) {
	t.Cleanup(store.SetQuoteFeatureOpen(true))
	srv, tok := newQuoteServer(t)
	if _, err := srv.Store.CreatePackage(&store.Package{
		TenantID: 0, Code: "pro_month", Name: "包月专业版", PType: store.PackagePaid,
		Points: 1000, PriceMoney: 299.0, DurationDays: 30, Enabled: 1,
	}); err != nil {
		t.Fatalf("建套餐失败: %v", err)
	}
	// 切 USD 报价（1 USD = 7.2 CNY）
	if err := srv.Store.SetFxRates(map[string]float64{"USD": 7.2}); err != nil {
		t.Fatalf("写倍率失败: %v", err)
	}
	if err := srv.Store.SetQuoteCurrency(0, "USD"); err != nil {
		t.Fatalf("写币种失败: %v", err)
	}
	code, resp := doJSON(t, srv.quoteMux(), http.MethodGet, "/api/plans", "", nil)
	if code != 200 || resp["success"] != true {
		t.Fatalf("/api/plans 应 200+success，got %d %+v", code, resp)
	}
	if resp["quote_currency"] != "USD" {
		t.Fatalf("顶层 quote_currency 应为 USD: %+v", resp["quote_currency"])
	}
	snap, _ := resp["fx_rates_snapshot"].(map[string]interface{})
	if snap["USD"] != 7.2 {
		t.Fatalf("fx_rates_snapshot 应含 USD=7.2: %+v", snap)
	}
	plans, _ := resp["plans"].([]interface{})
	if len(plans) != 1 {
		t.Fatalf("应有 1 个套餐，got %d", len(plans))
	}
	row, _ := plans[0].(map[string]interface{})
	// 老字段原样在（前端/e2e 依赖）：price_money 仍是人民币元 299，code/ptype 不变
	if row["price_money"] != 299.0 {
		t.Fatalf("老字段 price_money 语义被破坏: %+v", row["price_money"])
	}
	if row["code"] != "pro_month" || row["ptype"] != "paid" {
		t.Fatalf("老字段 code/ptype 丢失: %+v", row)
	}
	// 新字段：price_cny=299（人民币事实源），price_display=299/7.2=41.53（2 位）
	if row["price_cny"] != 299.0 {
		t.Fatalf("price_cny 应等于人民币原价: %+v", row["price_cny"])
	}
	if d, _ := row["price_display"].(float64); d < 41.52 || d > 41.53 {
		t.Fatalf("price_display 应为 41.53（299/7.2），got %v", d)
	}
	// CNY 默认态：price_display == price_money（回落不漂数）
	if err := srv.Store.SetQuoteCurrency(0, "CNY"); err != nil {
		t.Fatalf("切回 CNY 失败: %v", err)
	}
	_, resp = doJSON(t, srv.quoteMux(), http.MethodGet, "/api/plans", "", nil)
	_ = tok
	row2, _ := resp["plans"].([]interface{})[0].(map[string]interface{})
	if row2["price_display"] != 299.0 || row2["quote_currency"] != "CNY" {
		t.Fatalf("CNY 态 display 应恒等于原价: %+v", row2)
	}
}

// ③ 下单快照：stampOrderQuote 落 orders 三列；未知币种脏入参不落库。
// （验证开放态链路——用例内显式翻开 store 开关；关闭态下单恒落 CNY 快照由 store 层 F 组用例兜底。）
func TestStampOrderQuoteSnapshot(t *testing.T) {
	t.Cleanup(store.SetQuoteFeatureOpen(true))
	srv, _ := newQuoteServer(t)
	readCols := func(oid int64) (string, float64, float64) {
		var cur string
		var rate, cny float64
		if err := db.QueryRow(srv.Store.DB(), db.CurrentDialect(),
			"SELECT currency, fx_rate, money_cny FROM orders WHERE id=?", oid).
			Scan(&cur, &rate, &cny); err != nil {
			t.Fatalf("读快照失败: %v", err)
		}
		return cur, rate, cny
	}
	// CNY 单（默认）：currency=CNY、fx_rate=1、money_cny=amount_money
	o1, err := srv.Store.CreateOrderChannel(1, 1000, 299.0, 0, "offline", "")
	if err != nil {
		t.Fatalf("建单失败: %v", err)
	}
	srv.stampOrderQuote(context.Background(), o1)
	if cur, rate, cny := readCols(o1.ID); cur != "CNY" || rate != 1 || cny != 299.0 {
		t.Fatalf("CNY 单快照错误: %q/%v/%v", cur, rate, cny)
	}
	// USD 单：currency=USD、fx_rate=7.2（配置值），money_cny 仍与 amount_money 一致
	if err := srv.Store.SetFxRates(map[string]float64{"USD": 7.2}); err != nil {
		t.Fatalf("写倍率失败: %v", err)
	}
	if err := srv.Store.SetQuoteCurrency(1, "USD"); err != nil {
		t.Fatalf("写币种失败: %v", err)
	}
	o2, err := srv.Store.CreateOrderChannel(1, 1000, 299.0, 0, "manual", "")
	if err != nil {
		t.Fatalf("建单失败: %v", err)
	}
	srv.stampOrderQuote(context.Background(), o2)
	cur, rate, cny := readCols(o2.ID)
	if cur != "USD" || rate != 7.2 {
		t.Fatalf("USD 单快照错误: %q/%v", cur, rate)
	}
	if cny != o2.AmountMoney || cny != 299.0 {
		t.Fatalf("money_cny 必须=amount_money（人民币事实源）: %v", cny)
	}
	// 配了币种但没汇率 → 解析回落 CNY，不写脏快照
	if err := srv.Store.SetQuoteCurrency(1, "EUR"); err != nil {
		t.Fatalf("写币种失败: %v", err)
	}
	o3, err := srv.Store.CreateOrderChannel(1, 1000, 100.0, 0, "offline", "")
	if err != nil {
		t.Fatalf("建单失败: %v", err)
	}
	srv.stampOrderQuote(context.Background(), o3)
	if cur, rate, _ := readCols(o3.ID); cur != "CNY" || rate != 1 {
		t.Fatalf("缺倍率币种应回落 CNY 快照: %q/%v", cur, rate)
	}
}

// containsChinese 粗判 message 是否含中文字符（确保拒绝原因是给人看的中文，不是裸英文错误码）。
func containsChinese(s string) bool {
	for _, r := range s {
		if r >= 0x4E00 && r <= 0x9FFF {
			return true
		}
	}
	return false
}
