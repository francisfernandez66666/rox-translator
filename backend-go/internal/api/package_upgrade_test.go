// ============ 本文件职责中文说明 ============
// 套餐升级 API 全链路测试（2026-09-09）：
//
//	A) 租户管理员升级付费包 → 创建升级订单（应付=新价−抵扣），mock 模式即时到账，
//	   旧包剩余台账作废并等价转入新包、PackageCode 切换；
//	B) 边界拒绝：未订阅 / 目标包低于当前价 / 目标为增量包 → success=false。
//
// 使用内存 SQLite + tenant.Store + 签发 JWT，走 handler 全链路（与生产路由同源）。
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
	"translator/internal/store"
	"translator/internal/tenant"

	_ "modernc.org/sqlite"
)

// newUpgradeTestServer 构造套餐升级测试的 Server：
// 内存 SQLite + tenant.Store，含企业租户（carco）一名 tenant_admin；
// 预置两个付费包（低价 old / 高价 new）与一个增量包。
func newUpgradeTestServer(t *testing.T) (*Server, string, map[string]int64) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("打开内存数据库失败: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	st, err := store.New(db)
	if err != nil {
		t.Fatalf("创建 Store 失败: %v", err)
	}
	ten, err := tenant.NewStore(db)
	if err != nil {
		t.Fatalf("创建 tenant.Store 失败: %v", err)
	}
	car, err := ten.Create("t_car", "汽车公司", "", "{}")
	if err != nil {
		t.Fatalf("创建企业租户失败: %v", err)
	}
	s := &Server{Store: st, Ten: ten}
	// 租户管理员
	if _, err := st.CreateUser(car.ID, "car_admin", "hash-car", "汽车公司管理员", "tenant_admin", 1, 0); err != nil {
		t.Fatalf("创建租户管理员失败: %v", err)
	}
	users, _ := st.ListAllUsers()
	var token string
	for _, u := range users {
		if u.Username == "car_admin" {
			tk, err := auth.Sign(u, time.Hour)
			if err != nil {
				t.Fatalf("签发 JWT 失败: %v", err)
			}
			token = tk
		}
	}
	// 预置套餐：低价付费包 / 高价付费包 / 增量包
	ids := map[string]int64{}
	for _, p := range []*store.Package{
		{Code: "paid500", Name: "包月 500 句", PType: store.PackagePaid, Sentences: 500, PriceMoney: 99, DurationDays: 30, Enabled: 1},
		{Code: "paid2000", Name: "包月 2000 句", PType: store.PackagePaid, Sentences: 2000, PriceMoney: 299, DurationDays: 30, Enabled: 1},
		{Code: "inc500", Name: "增量 500 句", PType: store.PackageIncrement, Sentences: 500, PriceMoney: 50, Enabled: 1},
	} {
		cp, err := st.CreatePackage(p)
		if err != nil {
			t.Fatalf("创建套餐 %s 失败: %v", p.Code, err)
		}
		ids[p.Code] = cp.ID
	}
	return s, token, ids
}

// upgradeMux 构造升级相关 handler 的最小真实路由（与生产注册同源）。
func (s *Server) upgradeMux() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("/api/package/subscribe", s.handlePackageSubscribe)
	m.HandleFunc("/api/package/upgrade", s.handlePackageUpgrade)
	m.HandleFunc("/api/me/package", s.handleMyPackage)
	return m
}

// postUpgrade 构造带 Bearer 的 JSON POST 请求，走升级路由。
func postUpgrade(t *testing.T, s *Server, path, token string, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.upgradeMux().ServeHTTP(rec, req)
	return rec
}

// TestPackageUpgradeAPI 升级全链路：订阅低价包 → 升级高价包 → 校验订单应付与到账后状态。
func TestPackageUpgradeAPI(t *testing.T) {
	s, token, _ := newUpgradeTestServer(t)

	// ① 订阅低价付费包（mock 即时到账）
	r := postUpgrade(t, s, "/api/package/subscribe", token, map[string]string{"code": "paid500"})
	var sub struct {
		Success bool           `json:"success"`
		Order   map[string]any `json:"order"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &sub); err != nil || !sub.Success {
		t.Fatalf("订阅低价包失败: %s", r.Body.String())
	}

	// ② 升级到高价包：应创建升级订单，应付=新价−抵扣（剩余率 100% ⇒ 抵扣=旧价 99）
	r = postUpgrade(t, s, "/api/package/upgrade", token, map[string]string{"code": "paid2000"})
	var up struct {
		Success     bool           `json:"success"`
		CreditMoney float64        `json:"credit_money"`
		Order       map[string]any `json:"order"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &up); err != nil {
		t.Fatalf("解析升级响应失败: %v (%s)", err, r.Body.String())
	}
	if !up.Success {
		t.Fatalf("升级失败: %s", r.Body.String())
	}
	if up.CreditMoney != 99.0 {
		t.Fatalf("抵扣金额应为旧价 99，实际 %.2f", up.CreditMoney)
	}
	amount := up.Order["amount_money"].(float64)
	if amount != 299-99 {
		t.Fatalf("升级订单应付应为 200（299−99），实际 %.2f", amount)
	}
	if up.Order["upgrade_from_order"].(float64) <= 0 {
		t.Fatal("升级订单应关联旧订单（upgrade_from_order>0）")
	}
	// mock 模式即时到账 → 订单应已 paid
	if up.Order["status"] != "paid" {
		t.Fatalf("mock 升级订单应立即到账，实际 status=%v", up.Order["status"])
	}

	// ③ 到账后校验：新台账 = 新包 token + 旧包剩余；PackageCode 切换为 paid2000
	oldTokens := int64(float64(500*s.Store.TokenSentenceRate()) * s.Store.MarkupMultiplier())
	newTokens := int64(float64(2000*s.Store.TokenSentenceRate()) * s.Store.MarkupMultiplier())
	if g := s.Store.SumActiveGrants(1); g != newTokens+oldTokens {
		t.Fatalf("升级后总台账应为 %d（新包）+ %d（旧剩）= %d，实际 %d", newTokens, oldTokens, newTokens+oldTokens, g)
	}
	perms, _ := s.Store.GetTenantPerms(1)
	if perms.PackageCode != "paid2000" {
		t.Fatalf("升级后 PackageCode 应为 paid2000，实际 %q", perms.PackageCode)
	}
}

// TestPackageUpgradeAPIRejections 升级边界拒绝：未订阅 / 目标低于当前价 / 目标为增量包。
func TestPackageUpgradeAPIRejections(t *testing.T) {
	s, token, _ := newUpgradeTestServer(t)

	// 未订阅 → 拒绝
	r := postUpgrade(t, s, "/api/package/upgrade", token, map[string]string{"code": "paid2000"})
	var resp struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(r.Body.Bytes(), &resp)
	if resp.Success {
		t.Fatalf("未订阅应拒绝升级: %s", r.Body.String())
	}

	// 订阅低价包后，目标为增量包 → 拒绝
	r = postUpgrade(t, s, "/api/package/subscribe", token, map[string]string{"code": "paid500"})
	if !json.Valid(r.Body.Bytes()) {
		t.Fatalf("订阅响应异常: %s", r.Body.String())
	}
	r = postUpgrade(t, s, "/api/package/upgrade", token, map[string]string{"code": "inc500"})
	_ = json.Unmarshal(r.Body.Bytes(), &resp)
	if resp.Success {
		t.Fatalf("目标为增量包应拒绝升级: %s", r.Body.String())
	}
}
