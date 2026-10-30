// ============================================================================
// assist_profile_sign_test.go — 主服务侧画像组装/签名（★ 2026-10-30 组7 §8.2，G27/G28/G29/G33）
//
// 锁住四件事：
//
//	A) 登录用户 → 三个画像头齐全，签名可被共享 profile.Verify 验过；字段来自客户面同一读点；
//	B) 匿名 → 一个画像头都不发；
//	C) 客户端伪造入站画像头 → 上游收到的仍是「无头像」/本进程重签的画像，伪造值一次都不许出现（P-7）；
//	D) 未配置密钥 → 即使登录也不发（回落匿名，不报错）。
//
// 方言：沿用 newAdminScopeTestServer 的内存 SQLite（不经过 config.Default()，不泄漏方言）。
// ============================================================================
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"translator/internal/assist/profile"
	"translator/internal/store"
)

// profileCapture 假 assist 上游：只记画像三头（其余转发逻辑由 assist_open_proxy_test.go 覆盖）。
type profileCapture struct {
	hits    int
	payload string
	ts      string
	sig     string
}

func (p *profileCapture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.hits++
	p.payload = r.Header.Get(profile.HeaderProfile)
	p.ts = r.Header.Get(profile.HeaderTs)
	p.sig = r.Header.Get(profile.HeaderSig)
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"reply":"ok"}`))
}

// newProfileFixture 建一个带登录用户的 Server + 假上游。
func newProfileFixture(t *testing.T, key string) (*Server, *profileCapture, *store.User) {
	t.Helper()
	t.Setenv(assistProfileKeyEnv, key)
	s, _ := newAdminScopeTestServer(t)
	u, err := s.Store.CreateUserWithEmail(1, "prof_u", "h", "张三", "user", 0, 0,
		"zhangsan@example.com", "", 0)
	if err != nil {
		t.Fatalf("创建用户失败: %v", err)
	}
	// 让画像里的余额有一个非零来源：写入一条台账（PointsFromTokens 的折算口径由 store 负责）。
	if err := s.Store.CreateQuotaGrant(1, "plan", 99000, time.Now().Add(24*time.Hour), "profile_test", 0); err != nil {
		t.Logf("写入测试台账跳过: %v", err)
	}
	up := &profileCapture{}
	srv := httptest.NewServer(up)
	t.Cleanup(srv.Close)
	t.Setenv("ASSIST_BASE_URL", srv.URL)
	return s, up, u
}

// doProxyWithHeaders 调转发 handler，可预置入站头（伪造画像头用）。
func doProxyWithHeaders(s *Server, hdr map[string]string) (int, string) {
	r := httptest.NewRequest(http.MethodGet, "/assist-api/api/assist/features", nil)
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	s.handleAssistOpenProxy(w, r)
	return w.Code, w.Body.String()
}

// TestAssistProfileSignedForLoggedIn A)：登录用户转发时带画像头且签名可验、字段与读点同源。
func TestAssistProfileSignedForLoggedIn(t *testing.T) {
	s, up, u := newProfileFixture(t, "unit-test-profile-key")
	code, body := doProxyWithHeaders(s, map[string]string{"Authorization": bearerFor(t, u)})
	if code != 200 {
		t.Fatalf("转发应 200，实际 %d: %s", code, body)
	}
	if up.payload == "" || up.ts == "" || up.sig == "" {
		t.Fatalf("登录请求必须带齐三个画像头，实际 payload=%q ts=%q sig=%q", up.payload, up.ts, up.sig)
	}
	v, err := profile.Verify([]byte("unit-test-profile-key"), up.payload, up.ts, up.sig, time.Now())
	if err != nil {
		t.Fatalf("签名应可验证，实际 %v", err)
	}
	if v.UID != u.ID {
		t.Fatalf("uid 不符：want %d got %d", u.ID, v.UID)
	}
	if v.DisplayName != "张三" {
		t.Fatalf("称呼丢失：%q", v.DisplayName)
	}
	if v.Email != "z***@example.com" {
		t.Fatalf("邮箱必须脱敏为 z***@example.com，实际 %q", v.Email)
	}
	// G29：余额与客户面同一函数（balancePayload → PointsFromTokens）等值。
	_, _, tokens, _ := s.balancePayload(1)
	if want := s.Store.PointsFromTokens(tokens); v.PointsBalance != want {
		t.Fatalf("画像余额必须与 balancePayload 同源：want %d got %d", want, v.PointsBalance)
	}
	// 原始 JSON 里不许出现明文邮箱（P-2）。
	raw, _ := json.Marshal(v)
	if strings.Contains(string(raw), "zhangsan@example.com") {
		t.Fatalf("序列化结果里出现明文邮箱：%s", raw)
	}
}

// TestAssistProfileAbsentForAnonymous B)：未登录不发画像头。
func TestAssistProfileAbsentForAnonymous(t *testing.T) {
	s, up, _ := newProfileFixture(t, "unit-test-profile-key")
	code, body := doProxyWithHeaders(s, nil)
	if code != 200 {
		t.Fatalf("匿名转发应 200，实际 %d: %s", code, body)
	}
	if up.payload != "" || up.ts != "" || up.sig != "" {
		t.Fatalf("匿名请求不得带画像头，实际 payload=%q ts=%q sig=%q", up.payload, up.ts, up.sig)
	}
}

// TestAssistProfileForgedInboundHeadersDropped C)：客户端伪造的画像头必须被删（P-7）。
func TestAssistProfileForgedInboundHeadersDropped(t *testing.T) {
	s, up, _ := newProfileFixture(t, "unit-test-profile-key")
	forged := map[string]string{
		profile.HeaderProfile: "Zm9yZ2Vk",
		profile.HeaderTs:      "1",
		profile.HeaderSig:     "deadbeef",
	}
	code, body := doProxyWithHeaders(s, forged)
	if code != 200 {
		t.Fatalf("匿名 + 伪造头应照常 200，实际 %d: %s", code, body)
	}
	if up.payload == "Zm9yZ2Vk" || up.ts == "1" || up.sig == "deadbeef" {
		t.Fatalf("伪造画像头必须被删除，上游收到 payload=%q ts=%q sig=%q", up.payload, up.ts, up.sig)
	}
}

// TestAssistProfileAbsentWithoutKey D)：未配置密钥 ⇒ 登录用户也不发头（匿名回落）。
func TestAssistProfileAbsentWithoutKey(t *testing.T) {
	s, up, u := newProfileFixture(t, "")
	code, body := doProxyWithHeaders(s, map[string]string{"Authorization": bearerFor(t, u)})
	if code != 200 {
		t.Fatalf("无密钥时转发应照常 200，实际 %d: %s", code, body)
	}
	if up.payload != "" || up.ts != "" || up.sig != "" {
		t.Fatalf("未配置密钥不得发画像头，实际 payload=%q ts=%q sig=%q", up.payload, up.ts, up.sig)
	}
}

// TestAssistProfileReflectsLiveBalance G29 的「不缓存 uid 字段」腿：
// 余额变了，下一次组装必须跟着变（同一函数、每轮现读）。
func TestAssistProfileReflectsLiveBalance(t *testing.T) {
	s, _, u := newProfileFixture(t, "k")
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	v1 := s.buildVisitorProfile(r, u)
	if v1 == nil {
		t.Fatal("首轮画像为空")
	}
	if err := s.Store.CreateQuotaGrant(1, "plan", 66000, time.Now().Add(24*time.Hour), "profile_test2", 0); err != nil {
		t.Fatalf("追加台账失败: %v", err)
	}
	v2 := s.buildVisitorProfile(r, u)
	if v2 == nil {
		t.Fatal("第二轮画像为空")
	}
	if v2.PointsBalance <= v1.PointsBalance {
		t.Fatalf("余额改动后画像未跟随（缓存了 uid 字段？）：before=%d after=%d", v1.PointsBalance, v2.PointsBalance)
	}
}

// TestAssistProfileSameSourceAsMyPackage G29：同刻取 /api/me/package 与画像，
// 余额/今日已用/本月已用三个数逐项等值（画像里另算一份 ⇒ 这条红）。
func TestAssistProfileSameSourceAsMyPackage(t *testing.T) {
	s, _, u := newProfileFixture(t, "k")
	// 造一条今天的用量（quantity 与客户面「已用」同一列/同一谓词）。
	if _, err := s.Store.DB().Exec(`INSERT INTO usage_ledger(tenant_id,user_id,task_type,provider,model,quantity,unit_price,cost,charge_kind,created_at)
		VALUES(1,?, 'translate','p','m', 5000, 0, 0, 'charge', ?)`,
		u.ID, time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatalf("写入用量失败: %v", err)
	}
	r := httptest.NewRequest(http.MethodGet, "/api/me/package", nil)
	r.Header.Set("Authorization", bearerFor(t, u))
	w := httptest.NewRecorder()
	s.handleMyPackage(w, r)
	if w.Code != 200 {
		t.Fatalf("/api/me/package 应 200，实际 %d: %s", w.Code, w.Body.String())
	}
	var doc map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatalf("解析 /api/me/package 失败: %v", err)
	}
	v := s.buildVisitorProfile(r, u)
	if v == nil {
		t.Fatal("画像为空")
	}
	eq := func(field string, got int64) {
		want, ok := doc[field].(float64)
		if !ok {
			t.Fatalf("/api/me/package 缺字段 %s：%v", field, doc)
		}
		if int64(want) != got {
			t.Fatalf("G29 等值腿红：%s package=%v profile=%d", field, doc[field], got)
		}
	}
	eq("points_balance", v.PointsBalance)
	eq("points_used_today", v.PointsUsedToday)
	eq("points_used_month", v.PointsUsedMonth)
}

// TestAssistProfileDoesNotCarrySecrets G28)：画像 JSON 里零 token/密码/手机/证件类字段。
func TestAssistProfileDoesNotCarrySecrets(t *testing.T) {
	s, _, u := newProfileFixture(t, "k")
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	v := s.buildVisitorProfile(r, u)
	if v == nil {
		t.Fatal("登录用户应能组装画像")
	}
	raw, _ := json.Marshal(v)
	low := strings.ToLower(string(raw))
	for _, forbidden := range []string{"password", "passwd", "token", "secret", "phone", "id_card", "apikey", "api_key"} {
		if strings.Contains(low, forbidden) {
			t.Fatalf("画像 JSON 出现敏感字段 %q：%s", forbidden, raw)
		}
	}
}
