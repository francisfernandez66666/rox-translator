// ============ coupons_api_test.go · 职责说明 ============
// 优惠券 HTTP 层（★ #41 商业洞三，2026-09-21 实装）后端测试，走真实 handler：
//
//	A) 超管券 CRUD：新建→列表→更新→核销流水→删除；参数非法（折扣值 0）当场拒绝；
//	B) 权限面：租户管理员不得进超管券管理口（403）；租户管理员可用预览；
//	C) 预览：折前金额一律服务端按下单口径重算（前端塞 money 字段无效），折让与实付同算法；
//	D) 下单核销：/api/pay/create 与 /api/package/subscribe 带券 → orders.amount_money 折后、
//	   券码与流水落库；券不可用 → success=false + order_no（不留「券失效却仍可扫码」的单）；
//	E) #41 收款空洞回归：pay_mode=sdk 但商户资质缺失时，订阅单必须显式报错且订单保持 pending
//	   （旧实现静默返回 success=true 却没有二维码，收银台永远空转）。
//
// ★ 单测自钉 sqlite 方言（AGENTS.md §4），避免 run_uat 的 PG 模式泄漏给同包内存库用例。
// 运行：env DB_DRIVER=sqlite go test -count=1 ./internal/api/ -run Coupon
// ==========================================
package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"translator/internal/auth"
	"translator/internal/config"
	"translator/internal/store"
	"translator/internal/tenant"

	_ "modernc.org/sqlite"
)

// couponAPIFixture 券接口测试环境（一家企业租户 + 租户管理员 + 超管 + 一个上架付费包）。
type couponAPIFixture struct {
	srv      *Server
	raw      *sql.DB
	token    string // 租户管理员
	superTok string // 超管
	tid      int64
	pkgID    int64
}

// newCouponAPIFixture 建栈：内存 SQLite → store/tenant → 租户（注册回拨 31 天避开首月半价）→ 账号 → 套餐。
func newCouponAPIFixture(t *testing.T) *couponAPIFixture {
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
	ten, err := tenant.NewStore(raw)
	if err != nil {
		t.Fatalf("创建 tenant.Store 失败: %v", err)
	}
	co, err := ten.Create("t_coupon", "券测试公司", "", "{}")
	if err != nil {
		t.Fatalf("创建租户失败: %v", err)
	}
	if _, err := raw.Exec(`UPDATE tenants SET created_at=? WHERE id=?`,
		time.Now().Add(-31*24*time.Hour).Format(time.RFC3339), co.ID); err != nil {
		t.Fatalf("回拨注册时间失败: %v", err)
	}
	if _, err := st.CreateUser(co.ID, "coupon_tadmin", "hash", "券测试租管", store.RoleTenantAdmin, 1, 0); err != nil {
		t.Fatalf("创建租户管理员失败: %v", err)
	}
	u, err := st.GetUserByUsername(co.ID, "coupon_tadmin")
	if err != nil {
		t.Fatalf("查询租户管理员失败: %v", err)
	}
	tk, serr := auth.Sign(u, time.Hour)
	if serr != nil {
		t.Fatalf("签发租管 JWT 失败: %v", serr)
	}
	super, err := st.CreateUser(0, "coupon_super", "hash", "券测试超管", store.RoleSuperAdmin, 0, 0)
	if err != nil {
		t.Fatalf("创建超管失败: %v", err)
	}
	stok, serr2 := auth.Sign(super, time.Hour)
	if serr2 != nil {
		t.Fatalf("签发超管 JWT 失败: %v", serr2)
	}
	pkg, err := st.CreatePackage(&store.Package{
		Code: "cp_api_pack", Name: "券接口包月", PType: store.PackagePaid,
		Sentences: 1000, PriceMoney: 200, DurationDays: 30, Enabled: 1,
	})
	if err != nil {
		t.Fatalf("创建套餐失败: %v", err)
	}
	return &couponAPIFixture{srv: &Server{Store: st, Ten: ten}, raw: raw, token: tk, superTok: stok, tid: co.ID, pkgID: pkg.ID}
}

// couponMux 券与下单相关 handler 的最小真实路由（与 server.go 注册口径一致）。
func (s *Server) couponMux() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("/api/coupon/preview", s.handleCouponPreview)
	m.HandleFunc("/api/admin/coupons", s.handleAdminCoupons)
	m.HandleFunc("/api/admin/coupons/save", s.handleAdminCouponSave)
	m.HandleFunc("/api/admin/coupons/delete", s.handleAdminCouponDelete)
	m.HandleFunc("/api/admin/coupons/redemptions", s.handleAdminCouponRedemptions)
	m.HandleFunc("/api/pay/create", s.handlePayCreate)
	m.HandleFunc("/api/package/subscribe", s.handlePackageSubscribe)
	return m
}

// call 发一个带 Bearer 的 JSON 请求（GET 传 body=nil）。
func (f *couponAPIFixture) call(t *testing.T, method, path string, body interface{}, tok ...string) *httptest.ResponseRecorder {
	t.Helper()
	bearer := f.token
	if len(tok) > 0 {
		bearer = tok[0]
	}
	var r *http.Request
	if body == nil {
		r = httptest.NewRequest(method, path, nil)
	} else {
		b, _ := json.Marshal(body)
		r = httptest.NewRequest(method, path, bytes.NewReader(b))
		r.Header.Set("Content-Type", "application/json")
	}
	r.Header.Set("Authorization", "Bearer "+bearer)
	rec := httptest.NewRecorder()
	f.srv.couponMux().ServeHTTP(rec, r)
	return rec
}

// decode 解析响应为 map（用例里按需取字段，避免为每个接口定义一份结构体）。
func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("解析响应失败: %v (%s)", err, rec.Body.String())
	}
	return m
}

// orderRow 读订单三列（金额/券码/状态）。
func (f *couponAPIFixture) orderRow(t *testing.T, orderNo string) (money float64, coupon, status string) {
	t.Helper()
	var disc float64
	if err := f.raw.QueryRow(`SELECT amount_money, coupon_code, status, discount_money FROM orders WHERE order_no=?`, orderNo).
		Scan(&money, &coupon, &status, &disc); err != nil {
		t.Fatalf("读订单 %s 失败: %v", orderNo, err)
	}
	return money, coupon, status
}

// createCoupon 超管建券（走真实 handler，返回券 ID）。
func (f *couponAPIFixture) createCoupon(t *testing.T, body map[string]any) float64 {
	t.Helper()
	rec := f.call(t, http.MethodPost, "/api/admin/coupons/save", body, f.superTok)
	m := decode(t, rec)
	if m["success"] != true {
		t.Fatalf("建券失败: %s", rec.Body.String())
	}
	c, _ := m["coupon"].(map[string]any)
	if c == nil {
		t.Fatalf("建券响应缺 coupon: %s", rec.Body.String())
	}
	return c["id"].(float64)
}

// TestCouponAdminCRUDRoundTrip 超管券 CRUD 全链路 + 参数校验 + 权限面。
func TestCouponAdminCRUDRoundTrip(t *testing.T) {
	f := newCouponAPIFixture(t)

	// ① 非法折扣值当场拒绝（不落库，防「20 到底是 20 元还是 20%」这类误配进生产）
	bad := f.call(t, http.MethodPost, "/api/admin/coupons/save", map[string]any{
		"code": "BAD", "kind": "any", "discount_type": "percent", "discount_value": 0, "enabled": 1,
	}, f.superTok)
	if m := decode(t, bad); m["success"] == true {
		t.Fatalf("折扣值为 0 的券应被拒绝: %s", bad.Body.String())
	}

	// ② 新建（小写券码应归一为大写）+ 列表回显
	id := f.createCoupon(t, map[string]any{
		"code": "half10", "name": "十%促销", "kind": "recharge", "discount_type": "percent",
		"discount_value": 10, "max_discount": 50, "min_amount": 100, "max_uses": 100,
		"per_tenant_limit": 2, "note": "双十一", "enabled": 1,
	})
	list := decode(t, f.call(t, http.MethodGet, "/api/admin/coupons", nil, f.superTok))
	arr, _ := list["coupons"].([]any)
	if len(arr) != 1 {
		t.Fatalf("列表应 1 条，实际 %s", rec2str(list))
	}
	c0 := arr[0].(map[string]any)
	if c0["code"] != "HALF10" {
		t.Errorf("券码应归一为大写，实际 %v", c0["code"])
	}
	if c0["remaining"].(float64) != 100 || c0["used_count"].(float64) != 0 {
		t.Errorf("剩余/已用口径不符: %v %v", c0["remaining"], c0["used_count"])
	}

	// ③ 更新（同 id 走更新分支；改名与调额）
	upd := f.call(t, http.MethodPost, "/api/admin/coupons/save", map[string]any{
		"id": id, "code": "HALF10", "name": "改名副标题", "kind": "any", "discount_type": "percent",
		"discount_value": 20, "max_discount": 0, "min_amount": 0, "max_uses": 0,
		"per_tenant_limit": 0, "enabled": 1,
	}, f.superTok)
	um := decode(t, upd)
	if um["success"] != true {
		t.Fatalf("更新券失败: %s", upd.Body.String())
	}
	uc := um["coupon"].(map[string]any)
	if uc["name"] != "改名副标题" || uc["discount_value"].(float64) != 20 || uc["remaining"].(float64) != -1 {
		t.Errorf("更新后回显不符: %v", uc)
	}

	// ④ 核销流水（未核销应为空数组而非 null，前端表格直接映射）
	red := decode(t, f.call(t, http.MethodGet, "/api/admin/coupons/redemptions", nil, f.superTok))
	if rl, ok := red["redemptions"].([]any); !ok || len(rl) != 0 {
		t.Errorf("空流水应为 []，实际 %v", red["redemptions"])
	}

	// ⑤ 删除（模板删掉，流水表保留）
	del := f.call(t, http.MethodPost, "/api/admin/coupons/delete", map[string]any{"id": id}, f.superTok)
	if m := decode(t, del); m["success"] != true {
		t.Fatalf("删券失败: %s", del.Body.String())
	}
	if after := decode(t, f.call(t, http.MethodGet, "/api/admin/coupons", nil, f.superTok)); len(after["coupons"].([]any)) != 0 {
		t.Errorf("删券后列表应为空，实际 %v", after["coupons"])
	}
	// 缺 ID 与不存在的 ID 都必须出声
	if m := decode(t, f.call(t, http.MethodPost, "/api/admin/coupons/delete", map[string]any{}, f.superTok)); m["success"] == true {
		t.Error("缺券 ID 的删除请求应失败")
	}
	if m := decode(t, f.call(t, http.MethodPost, "/api/admin/coupons/delete", map[string]any{"id": 99999}, f.superTok)); m["success"] == true {
		t.Error("删除不存在的券应报「券不存在」")
	}

	// ⑥ 权限面：租户管理员不得进超管券口
	if rec := f.call(t, http.MethodGet, "/api/admin/coupons", nil); rec.Code != http.StatusForbidden {
		t.Errorf("租管访问超管券列表应 403，实际 %d", rec.Code)
	}
	if rec := f.call(t, http.MethodPost, "/api/admin/coupons/save", map[string]any{"code": "X"}, f.token); rec.Code != http.StatusForbidden {
		t.Errorf("租管建券应 403，实际 %d", rec.Code)
	}
}

// TestCouponPreviewServerSideAmount 预览：金额服务端重算 + 折让口径 + 不适用单类回错。
func TestCouponPreviewServerSideAmount(t *testing.T) {
	f := newCouponAPIFixture(t)
	f.createCoupon(t, map[string]any{
		"code": "pct25", "kind": "any", "discount_type": "percent", "discount_value": 25, "enabled": 1,
	})
	f.createCoupon(t, map[string]any{
		"code": "subonly", "kind": "subscribe", "discount_type": "amount", "discount_value": 30, "enabled": 1,
	})

	// ① 充值预览：points 折元（与 /api/pay/create 同一算法），再按券折让
	rec := f.call(t, http.MethodPost, "/api/coupon/preview", map[string]any{"code": "pct25", "points": 1000})
	m := decode(t, rec)
	if m["success"] != true {
		t.Fatalf("充值预览失败: %s", rec.Body.String())
	}
	origin := m["origin_money"].(float64)
	if origin <= 0 {
		t.Fatalf("折前金额应>0，实际 %v", origin)
	}
	if want := origin * 0.25; abs(m["discount_money"].(float64)-want) > 0.005 {
		t.Errorf("25%% 折让期望 %.2f 实际 %v", want, m["discount_money"])
	}
	if want := origin * 0.75; abs(m["pay_money"].(float64)-want) > 0.005 {
		t.Errorf("实付期望 %.2f 实际 %v", want, m["pay_money"])
	}
	// 响应不得带 token 裸值（对外积分口径）
	if strings.Contains(rec.Body.String(), "amount_tokens") {
		t.Error("预览响应含 amount_tokens，违反对外零 token 口径")
	}

	// ② 订阅预览按包定价重算，前端塞 money 字段无效（防改 body 白拿折扣）
	rec = f.call(t, http.MethodPost, "/api/coupon/preview",
		map[string]any{"code": "pct25", "package_code": "cp_api_pack", "origin_money": 0.01, "money": 0.01})
	m = decode(t, rec)
	if m["success"] != true {
		t.Fatalf("订阅预览失败: %s", rec.Body.String())
	}
	if m["origin_money"].(float64) != 200 {
		t.Errorf("订阅折前金额应取包价 200，实际 %v（前端塞值被采信？）", m["origin_money"])
	}
	if m["kind"] != store.CouponKindSubscribe {
		t.Errorf("订阅预览单类应为 subscribe，实际 %v", m["kind"])
	}

	// ③ 单类不符 / 券码不存在 / 免费包：均为可回显的业务提示
	for i, tc := range []struct {
		body map[string]any
		want string
	}{
		{map[string]any{"code": "subonly", "points": 1000}, "不适用"},
		{map[string]any{"code": "NOPE", "points": 1000}, "不存在"},
		{map[string]any{"code": "pct25"}, "请指定"},
	} {
		mm := decode(t, f.call(t, http.MethodPost, "/api/coupon/preview", tc.body))
		if mm["success"] == true {
			t.Errorf("第 %d 例预览应失败: %v", i, mm)
			continue
		}
		if msg, _ := mm["message"].(string); !strings.Contains(msg, tc.want) {
			t.Errorf("第 %d 例提示期望含 %q，实际 %q", i, tc.want, msg)
		}
	}
	// 预览不落任何流水
	if n := f.countRedemptions(t); n != 0 {
		t.Errorf("预览不得产生核销流水，实际 %d", n)
	}
}

// countRedemptions 核销流水条数。
func (f *couponAPIFixture) countRedemptions(t *testing.T) int {
	t.Helper()
	var n int
	if err := f.raw.QueryRow(`SELECT COUNT(1) FROM coupon_redemptions`).Scan(&n); err != nil {
		t.Fatalf("统计流水失败: %v", err)
	}
	return n
}

// TestCouponPayCreateRedeem 充值下单带券：金额折后落库、券码与流水齐全；券不可用时明确回错。
func TestCouponPayCreateRedeem(t *testing.T) {
	f := newCouponAPIFixture(t)
	f.createCoupon(t, map[string]any{
		"code": "half", "kind": "recharge", "discount_type": "percent", "discount_value": 50, "enabled": 1,
	})

	base := f.call(t, http.MethodPost, "/api/pay/create", map[string]any{"points": 2000, "channel": "mock"})
	bm := decode(t, base)
	if bm["success"] != true {
		t.Fatalf("无券下单应成功: %s", base.Body.String())
	}
	baseMoney := bm["order"].(map[string]any)["amount_money"].(float64)

	rec := f.call(t, http.MethodPost, "/api/pay/create", map[string]any{"points": 2000, "channel": "mock", "coupon": " half "})
	m := decode(t, rec)
	if m["success"] != true {
		t.Fatalf("带券下单应成功: %s", rec.Body.String())
	}
	orderNo := m["order"].(map[string]any)["order_no"].(string)
	money, coupon, status := f.orderRow(t, orderNo)
	if money != baseMoney/2 {
		t.Errorf("折后应收应为 %.2f，实际 %.2f", baseMoney/2, money)
	}
	if coupon != "HALF" {
		t.Errorf("订单应记录券码 HALF，实际 %q", coupon)
	}
	if status != "pending" {
		t.Errorf("下单后订单应为 pending，实际 %s", status)
	}
	if m["order"].(map[string]any)["amount_points"].(float64) != 2000 {
		t.Errorf("发放积分数量不得因折扣变动（券减钱不减货），实际 %v", m["order"].(map[string]any)["amount_points"])
	}
	if n := f.countRedemptions(t); n != 1 {
		t.Errorf("核销流水应 1 条，实际 %d", n)
	}

	// 不适用单类的券（subscribe 专用）用在充值单：失败且不留折扣单
	f.createCoupon(t, map[string]any{
		"code": "subonly", "kind": "subscribe", "discount_type": "amount", "discount_value": 10, "enabled": 1,
	})
	bad := f.call(t, http.MethodPost, "/api/pay/create", map[string]any{"points": 2000, "channel": "mock", "coupon": "SUBONLY"})
	badM := decode(t, bad)
	if badM["success"] == true {
		t.Fatalf("单类不符的券应拒绝下单: %s", bad.Body.String())
	}
	if no, _ := badM["order_no"].(string); no == "" {
		t.Error("失败响应应带 order_no（订单留 pending 可查）")
	} else if mm, cc, _ := f.orderRow(t, no); mm != baseMoney || cc != "" {
		t.Errorf("券失败后订单应保留原价未用券：money=%.2f coupon=%q", mm, cc)
	}
	if msg, _ := badM["message"].(string); !strings.Contains(msg, "不适用") {
		t.Errorf("应回显可纠正的券提示，实际 %q", msg)
	}
}

// TestCouponSubscribeOrderDiscounted 订阅下单带券：mock 渠道即时到账，但金额按折后、发放句数按原量。
func TestCouponSubscribeOrderDiscounted(t *testing.T) {
	f := newCouponAPIFixture(t)
	f.createCoupon(t, map[string]any{
		"code": "sub50", "kind": "subscribe", "discount_type": "percent", "discount_value": 50, "enabled": 1,
	})
	rec := f.call(t, http.MethodPost, "/api/package/subscribe",
		map[string]any{"code": "cp_api_pack", "coupon": "sub50"})
	m := decode(t, rec)
	if m["success"] != true {
		t.Fatalf("订阅带券下单失败: %s", rec.Body.String())
	}
	order := m["order"].(map[string]any)
	if order["amount_money"].(float64) != 100 {
		t.Errorf("200 元包五折后应收 100，实际 %v", order["amount_money"])
	}
	money, coupon, status := f.orderRow(t, order["order_no"].(string))
	if money != 100 || coupon != "SUB50" || status != "paid" {
		t.Errorf("订阅单落库口径不符: money=%.2f coupon=%q status=%s", money, coupon, status)
	}
	// 订阅身份照常落下（折扣不影响发放额度）
	p, err := f.srv.Store.GetTenantPerms(f.tid)
	if err != nil || p == nil || p.PackageCode != "cp_api_pack" {
		t.Fatalf("订阅后权限未落库: %+v err=%v", p, err)
	}
}

// TestSubscribeSDKModeQRFailClosed #41 收款空洞回归：sdk 模式但无商户资质时必须显式失败。
// 旧实现只落 pending 单就返回 success=true，收银台永远拿不到二维码（客户点了订阅却看不到付款页）。
func TestSubscribeSDKModeQRFailClosed(t *testing.T) {
	f := newCouponAPIFixture(t)
	if err := f.srv.Store.SetConfig("pay_mode", "sdk"); err != nil {
		t.Fatalf("设置 pay_mode 失败: %v", err)
	}
	rec := f.call(t, http.MethodPost, "/api/package/subscribe", map[string]any{"code": "cp_api_pack"})
	m := decode(t, rec)
	if m["success"] == true {
		t.Fatalf("无商户资质时 sdk 订阅应显式失败，实际响应: %s", rec.Body.String())
	}
	if msg, _ := m["message"].(string); !strings.Contains(msg, "支付渠道暂不可用") {
		t.Errorf("应回渠道不可用提示，实际 %q", msg)
	}
	no, _ := m["order_no"].(string)
	if no == "" {
		t.Fatal("失败响应应带 order_no（订单留 pending 可人工处理）")
	}
	if _, _, status := f.orderRow(t, no); status != "pending" {
		t.Errorf("订单应保持 pending，实际 %s", status)
	}
	if _, err := f.raw.Exec(`UPDATE orders SET channel='wechat' WHERE order_no=?`, no); err != nil {
		t.Fatalf("回写渠道失败: %v", err)
	}
	var qr string
	if err := f.raw.QueryRow(`SELECT qr_content FROM orders WHERE order_no=?`, no).Scan(&qr); err != nil {
		t.Fatalf("读二维码失败: %v", err)
	}
	if strings.HasPrefix(qr, "mockpay://") {
		t.Error("sdk 模式失败时不得回退 mock 废码（历史静默回退缺陷）")
	}
}

// rec2str 失败信息用的小工具（避免把 interface{} 直接塞进 Errorf 的可读性问题）。
func rec2str(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "<无法序列化>"
	}
	return string(b)
}

// abs 浮点绝对值（断言容差用）。
func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
