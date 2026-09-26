// ============ 本文件职责中文说明 ============
// 订阅自动续费（#41 商业洞二）后端测试：
//
//	A) 开关全链路：未订阅拒绝开启 → 订阅后开启成功 → permissions 其余字段不被覆盖
//	   （JSONPatchSet 单字段原子写，回归 B1 整改口径）→ GET 与 /api/me/package 回读一致 → 关闭；
//	B) 续费单生成：T-3 窗口内生成同包 pending 订单（金额=挂牌价、package_id 正确）、
//	   重复调用去重、窗口外/开关关闭不建单、包下架不建单但通知管理员换包；
//	C) 凭证边界：无效 Token 一律 403（不写库）。
//
// 使用内存 SQLite + tenant.Store + 签发 JWT，走 handler 全链路（与生产路由同源）。
// ★ 单测自钉 sqlite 方言（AGENTS.md §4），避免 run_uat 的 PG 模式泄漏给同包内存库用例。
// ==========================================
package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"translator/internal/auth"
	"translator/internal/config"
	"translator/internal/store"
	"translator/internal/tenant"

	_ "modernc.org/sqlite"
)

// renewFixture 自动续费测试环境（一家企业租户 + 一名 tenant_admin + 一个上架付费包）。
type renewFixture struct {
	srv   *Server
	token string
	tid   int64
	pkgID int64
}

// newRenewFixture 搭建测试栈：内存 SQLite → store/tenant → 租户与管理员 → JWT → 付费包。
// 注册时间回拨 31 天，使订单金额等于挂牌价（脱离「首月半价」窗，断言更直白）。
func newRenewFixture(t *testing.T) *renewFixture {
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
	co, err := ten.Create("t_renew", "续费测试公司", "", "{}")
	if err != nil {
		t.Fatalf("创建租户失败: %v", err)
	}
	if _, err := raw.Exec(`UPDATE tenants SET created_at=? WHERE id=?`,
		time.Now().Add(-31*24*time.Hour).Format(time.RFC3339), co.ID); err != nil {
		t.Fatalf("回拨注册时间失败: %v", err)
	}
	if _, err := st.CreateUser(co.ID, "renew_admin", "hash-renew", "续费管理员", "tenant_admin", 1, 0); err != nil {
		t.Fatalf("创建租户管理员失败: %v", err)
	}
	u, err := st.GetUserByUsername(co.ID, "renew_admin")
	if err != nil {
		t.Fatalf("查询租户管理员失败: %v", err)
	}
	tk, serr := auth.Sign(u, time.Hour)
	if serr != nil {
		t.Fatalf("签发 JWT 失败: %v", serr)
	}
	pkg, err := st.CreatePackage(&store.Package{
		Code: "renew_month", Name: "续费包月", PType: store.PackagePaid,
		Sentences: 1000, PriceMoney: 199, DurationDays: 30, Enabled: 1,
	})
	if err != nil {
		t.Fatalf("创建套餐失败: %v", err)
	}
	return &renewFixture{srv: &Server{Store: st, Ten: ten}, token: tk, tid: co.ID, pkgID: pkg.ID}
}

// renewMux 自动续费相关 handler 的最小真实路由。
func (s *Server) renewMux() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("/api/package/subscribe", s.handlePackageSubscribe)
	m.HandleFunc("/api/package/auto-renew", s.handleAutoRenew)
	m.HandleFunc("/api/me/package", s.handleMyPackage)
	return m
}

// do 发一个带 Bearer 的 JSON 请求（body 为 nil 时发 GET 语义请求）。
func (f *renewFixture) do(t *testing.T, method, path string, body interface{}, tokenOverride ...string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == nil {
		r = httptest.NewRequest(method, path, nil)
	} else {
		b, _ := json.Marshal(body)
		r = httptest.NewRequest(method, path, bytes.NewReader(b))
		r.Header.Set("Content-Type", "application/json")
	}
	tok := f.token
	if len(tokenOverride) > 0 {
		tok = tokenOverride[0]
	}
	r.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	f.srv.renewMux().ServeHTTP(rec, r)
	return rec
}

// subscribe 走真实订阅流程（mock 模式即时到账并落下订阅身份）。
func (f *renewFixture) subscribe(t *testing.T) {
	t.Helper()
	rec := f.do(t, http.MethodPost, "/api/package/subscribe", map[string]string{"code": "renew_month"})
	if rec.Code != 200 {
		t.Fatalf("订阅请求失败: %d %s", rec.Code, rec.Body.String())
	}
}

// setAutoRenew 直接置位开关（绕过 handler，用于构造建单前置状态）。
func (f *renewFixture) setAutoRenew(t *testing.T, on bool) {
	t.Helper()
	if err := f.srv.Store.SetTenantAutoRenew(f.tid, on); err != nil {
		t.Fatalf("置位自动续费开关失败: %v", err)
	}
}

// perms 读取当前权限快照。
func (f *renewFixture) perms(t *testing.T) *tenant.Perms {
	t.Helper()
	p, err := f.srv.Store.GetTenantPerms(f.tid)
	if err != nil || p == nil {
		t.Fatalf("读取权限失败: %v", err)
	}
	return p
}

// pendingOrders 统计指定包的待支付订单数。
func (f *renewFixture) pendingOrders(t *testing.T) int {
	t.Helper()
	var n int
	if err := f.srv.Store.DB().QueryRow(
		"SELECT COUNT(1) FROM orders WHERE tenant_id=? AND package_id=? AND status='pending'", f.tid, f.pkgID).Scan(&n); err != nil {
		t.Fatalf("统计订单失败: %v", err)
	}
	return n
}

// 未订阅（无到期时间）时不得开启自动续费：否则扫描会对不存在的包建单。
func TestAutoRenewRequiresSubscription(t *testing.T) {
	f := newRenewFixture(t)
	rec := f.do(t, http.MethodPost, "/api/package/auto-renew", map[string]bool{"enabled": true})
	var resp struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应失败: %v (%s)", err, rec.Body.String())
	}
	if resp.Success {
		t.Fatalf("无订阅租户开启自动续费应被拒绝: %s", rec.Body.String())
	}
	if p := f.perms(t); p.AutoRenew {
		t.Fatal("被拒绝的请求不应落库开关")
	}
	// GET 回读：开关 false 且带出当前包编码（空）
	rec = f.do(t, http.MethodGet, "/api/package/auto-renew", nil)
	var got struct {
		Success   bool   `json:"success"`
		AutoRenew bool   `json:"auto_renew"`
		PkgCode   string `json:"package_code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || !got.Success || got.AutoRenew {
		t.Fatalf("GET 回读异常: %s", rec.Body.String())
	}
}

// 开关全链路：开启 → permissions 其余字段不被覆盖 → 回读一致 → 关闭。
func TestAutoRenewToggleKeepsOtherPerms(t *testing.T) {
	f := newRenewFixture(t)
	f.subscribe(t)
	before := f.perms(t)
	if before.PackageCode != "renew_month" || before.PackageExpires == "" {
		t.Fatalf("订阅后权限未落库: %+v", before)
	}
	rec := f.do(t, http.MethodPost, "/api/package/auto-renew", map[string]bool{"enabled": true})
	var resp struct {
		Success   bool `json:"success"`
		AutoRenew bool `json:"auto_renew"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || !resp.Success || !resp.AutoRenew {
		t.Fatalf("开启自动续费失败: %s", rec.Body.String())
	}
	after := f.perms(t)
	if !after.AutoRenew {
		t.Fatal("开关未落库")
	}
	if after.PackageCode != before.PackageCode || after.PackageExpires != before.PackageExpires {
		t.Fatalf("开关写入覆盖了订阅字段: before=%+v after=%+v", before, after)
	}
	// /api/me/package 透出开关态（订阅页开关初值）
	rec = f.do(t, http.MethodGet, "/api/me/package", nil)
	var mp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &mp); err != nil {
		t.Fatal(err)
	}
	if mp["auto_renew"] != true {
		t.Fatalf("/api/me/package 未透出 auto_renew: %v", mp["auto_renew"])
	}
	// 关闭开关
	rec = f.do(t, http.MethodPost, "/api/package/auto-renew", map[string]bool{"enabled": false})
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || !resp.Success || resp.AutoRenew {
		t.Fatalf("关闭自动续费失败: %s", rec.Body.String())
	}
	if p := f.perms(t); p.AutoRenew {
		t.Fatal("关闭后开关仍为 true")
	}
}

// 续费单生成：窗口内建单一次、重复调用去重、窗口外与开关关闭均不建单。
func TestAutoRenewCreatesRenewalOrder(t *testing.T) {
	f := newRenewFixture(t)
	f.subscribe(t)
	f.setAutoRenew(t, true)
	perms := f.perms(t)

	// ① 窗口外（剩 10 天）不建单
	if f.srv.maybeCreateRenewalOrder(f.tid, perms, 10) {
		t.Fatal("剩余 10 天不应提前建续费单")
	}
	if n := f.pendingOrders(t); n != 0 {
		t.Fatalf("窗口外不应有续费单，实际 %d", n)
	}
	// ② 窗口内（剩 2 天）建单一张（订阅单本身已 paid，不计入 pending）
	if !f.srv.maybeCreateRenewalOrder(f.tid, perms, 2) {
		t.Fatal("T-3 窗口内应生成续费单")
	}
	if n := f.pendingOrders(t); n != 1 {
		t.Fatalf("续费单应为 1 张，实际 %d", n)
	}
	// ③ 重复调用去重（每日扫描不得堆单）
	if f.srv.maybeCreateRenewalOrder(f.tid, perms, 2) {
		t.Fatal("已有 pending 续费单时应跳过")
	}
	if n := f.pendingOrders(t); n != 1 {
		t.Fatalf("去重失败，续费单堆到 %d 张", n)
	}
	// ④ 订单口径：金额=挂牌价、package_id 指向原包、渠道按支付模式（此处默认 mock）
	var got struct {
		Money   float64 `json:"amount_money"`
		Channel string  `json:"channel"`
	}
	if err := f.srv.Store.DB().QueryRow(
		"SELECT amount_money, channel FROM orders WHERE tenant_id=? AND package_id=? AND status='pending'",
		f.tid, f.pkgID).Scan(&got.Money, &got.Channel); err != nil {
		t.Fatalf("读取续费单失败: %v", err)
	}
	if got.Money != 199 {
		t.Fatalf("续费单金额应为挂牌价 199，实际 %.2f", got.Money)
	}
	if got.Channel != "mock" {
		t.Fatalf("默认支付模式下续费渠道应为 mock，实际 %q", got.Channel)
	}
	// ⑤ 已通知租户管理员（站内信标题命中）
	var notes int
	if err := f.srv.Store.DB().QueryRow(
		"SELECT COUNT(1) FROM notifications WHERE title=?", "续费订单已自动生成").Scan(&notes); err != nil {
		t.Fatal(err)
	}
	if notes == 0 {
		t.Fatal("续费单生成后未通知租户管理员")
	}
	// ⑥ 开关关闭后不再建单（用落库状态而非内存对象，验证真实读取路径）
	f.setAutoRenew(t, false)
	if f.srv.maybeCreateRenewalOrder(f.tid, f.perms(t), 1) {
		t.Fatal("关闭开关后不应建单")
	}
}

// 包下架时不建单，但必须通知管理员换包（静默失败会让客户到期才发现）。
func TestAutoRenewPackageUnavailable(t *testing.T) {
	f := newRenewFixture(t)
	f.subscribe(t)
	f.setAutoRenew(t, true)
	perms := f.perms(t)
	if _, err := f.srv.Store.DB().Exec("UPDATE packages SET enabled=0 WHERE id=?", f.pkgID); err != nil {
		t.Fatalf("下架套餐失败: %v", err)
	}
	if f.srv.maybeCreateRenewalOrder(f.tid, perms, 2) {
		t.Fatal("包下架时不应建单")
	}
	if n := f.pendingOrders(t); n != 0 {
		t.Fatalf("包下架后仍建了 %d 张单", n)
	}
	var notes int
	if err := f.srv.Store.DB().QueryRow(
		"SELECT COUNT(1) FROM notifications WHERE title=?", "自动续费未能生成订单").Scan(&notes); err != nil {
		t.Fatal(err)
	}
	if notes == 0 {
		t.Fatal("包下架应通知管理员换包")
	}
	// 开关前置校验同样拒绝为下架包开启
	rec := f.do(t, http.MethodPost, "/api/package/auto-renew", map[string]bool{"enabled": true})
	var resp struct {
		Success bool `json:"success"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Success {
		t.Fatal("订阅包已下架时不应允许开启自动续费")
	}
}

// 凭证边界：无效 Token 一律 401（未登录），且不动开关。
// ★ F-64①（批 I-7）改判：这里过去断言 403，而 403 的语义是「知道你是谁、但你不该做这件事」。
// 无效/过期 Token 属于「认证失败」＝401，前端 core.ts 的会话过期处理**只挂在 401**
// （handleUnauthorized 清 token 落回登录页）；回 403 会让浏览器停在原页反复撞闸，
// 用户看到「无权限」以为账号缺权限，实际只差重新登录——同 bug 同修法见 server.go writeAuthzError。
func TestAutoRenewRejectsBadToken(t *testing.T) {
	f := newRenewFixture(t)
	f.subscribe(t)
	f.setAutoRenew(t, true)
	rec := f.do(t, http.MethodPost, "/api/package/auto-renew", map[string]bool{"enabled": false}, "invalid-token")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("无效凭证应 401（未登录），实际 %d", rec.Code)
	}
	if p := f.perms(t); !p.AutoRenew {
		t.Fatal("被拒绝的请求不应改动开关状态")
	}
}
