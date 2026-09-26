// ============================================================================
// pay_status_honesty_test.go — 收款路径「HTTP 状态码诚实」的行为级断言（★ F-64① 批 I-7）
//
// 与本包 payhonesty_gate_test.go 的分工（两层都要，缺一不可）：
//   - 闸门（静态 AST）：钉住「七个收款/账务文件里不许再出现 writeJSON(w, 200, {success:false})」，
//     它是**写法**锁——防止有人把失败重新塞回 200，但它不知道状态码到底对不对；
//   - 本文件（运行时 httptest）：真发请求真读响应，钉住**每条失败分支落在哪个状态码、
//     带哪个错误码、文案是不是中文**。它是**契约**锁——防止「写法迁了、状态码全写成 500」
//     这种把 400 类客户错误一律推给服务端的做法（那会让 SDK 侧按 code 分支全部失效）。
//
// 为什么必须两层：批 I-7 之前这批接口是 200 + {success:false}，前端只看 r.success 所以没暴露；
// 迁移中最容易出的错正是「状态码选错」而不是「没迁移」——只有一层静态锁看不见这个。
//
// 覆盖的分支（全部是真实 handler 出口，不构造合成响应）：
//
//	400 参数错 / 401 未登录 / 403 越权 / 404 查无此单 / 409 状态冲突 / 503 渠道未就绪。
//	500（建单/落库故障）刻意不在这里造：造它要破坏 DB，收益低于把一个正常写库打成故障的噪音。
//
// 方言：自钉 SQLite 内存库并显式钉死 config.C（AGENTS.md §一·4）。
// 运行：env DB_DRIVER=sqlite go test -count=1 ./internal/api/ -run TestPayStatusHonesty
// ============================================================================
package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"translator/internal/auth"
	"translator/internal/config"
	"translator/internal/iam"
	"translator/internal/store"
	"translator/internal/tenant"
)

// newPayHonestyProbe 装配「内存库 + 一个租户 + 该租户的 tenant_admin（带 JWT）」的最小服务器。
// 返回服务器、租户 ID、管理员 token、以及直接落库建单用的辅助闭包所需句柄。
func newPayHonestyProbe(t *testing.T) (*Server, int64, string) {
	t.Helper()
	pinSqliteDialect(t)
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	st, err := store.New(sqlDB)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	ts, err := tenant.NewStore(sqlDB)
	if err != nil {
		t.Fatalf("tenant.NewStore: %v", err)
	}
	tn, err := ts.Create("payhonesty", "状态码诚实探针租", "", `{"max_daily_chars":20000,"max_daily_tokens":20000}`)
	if err != nil {
		t.Fatalf("建探针租户: %v", err)
	}
	u, err := st.CreateUser(tn.ID, "payhonesty-admin", auth.PasswordHash("pw123456"), "探针管理员", iam.RoleTenantAdmin, 0, 0)
	if err != nil {
		t.Fatalf("建探针管理员: %v", err)
	}
	tok, err := auth.Sign(u, time.Hour)
	if err != nil {
		t.Fatalf("签发 JWT: %v", err)
	}
	return &Server{Store: st, Ten: ts, Cfg: config.C}, tn.ID, tok
}

// payHonestyResp 一次响应的三件套读数（状态码 / 错误码 / 文案）。
type payHonestyResp struct {
	status  int
	success interface{}
	code    string
	message string
	raw     string
}

// callPayHonesty 直调指定 handler 并把响应解析成三件套。
// 参数 fn=handler；method/path/body=请求；tok=JWT（空串＝匿名，用来验 401/403）。
func callPayHonesty(t *testing.T, fn func(http.ResponseWriter, *http.Request), method, path, body, tok string) payHonestyResp {
	t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	req.Header.Set("Content-Type", "application/json")
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	rec := httptest.NewRecorder()
	fn(rec, req)
	out := payHonestyResp{status: rec.Code, raw: rec.Body.String()}
	var m map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("响应不是合法 JSON（状态码 %d，body=%s）", rec.Code, truncPayHonesty(rec.Body.String()))
	}
	out.success = m["success"]
	if v, ok := m["code"].(string); ok {
		out.code = v
	}
	if v, ok := m["message"].(string); ok {
		out.message = v
	}
	return out
}

// truncPayHonesty 失败信息里截断 body，避免整段响应糊满日志。
func truncPayHonesty(s string) string {
	if len(s) > 240 {
		return s[:240] + "…"
	}
	return s
}

// assertPayHonesty 断言三件套：HTTP 状态码 + 稳定错误码 + 中文文案（且 success 必须是 false）。
// 中文文案的判据是「至少含一个 CJK 字符」——本仓对外错误体一律中文（脱敏口径见 internal/errors），
// 出现纯英文 message 说明有人绕过了统一出口或把上游原文吐给了客户。
func assertPayHonesty(t *testing.T, name string, got payHonestyResp, wantStatus int, wantCode string) {
	t.Helper()
	if got.status != wantStatus {
		t.Errorf("%s：HTTP 状态码应为 %d，实得 %d（body=%s）", name, wantStatus, got.status, truncPayHonesty(got.raw))
	}
	if got.status == 200 {
		t.Errorf("%s：★ 又回到「200 承载失败」了（F-64① 的核心缺陷形态），body=%s", name, truncPayHonesty(got.raw))
	}
	if got.success != false {
		t.Errorf("%s：失败响应的 success 必须是 false，实得 %v（body=%s）", name, got.success, truncPayHonesty(got.raw))
	}
	if got.code != wantCode {
		t.Errorf("%s：错误码应为 %s（前端/SDK 按此分支），实得 %q", name, wantCode, got.code)
	}
	if !hasCJKPayHonesty(got.message) {
		t.Errorf("%s：错误文案必须是中文（脱敏统一出口口径），实得 %q", name, got.message)
	}
}

// hasCJKPayHonesty 是否含中日韩统一表意文字（rune 级判定，不看长度）。
func hasCJKPayHonesty(s string) bool {
	for _, r := range s {
		if r >= 0x4E00 && r <= 0x9FFF {
			return true
		}
	}
	return false
}

// TestPayStatusHonesty400CreateParams 建单入参类失败一律 400/VALIDATION_ERROR：
// 这三条是「客户一分钟就能自己改对」的错误，给 500 等于让他提工单。
func TestPayStatusHonesty400CreateParams(t *testing.T) {
	s, _, tok := newPayHonestyProbe(t)
	cases := []struct {
		name string
		body string
	}{
		{"points=0", `{"points":0,"channel":"mock"}`},
		{"points 负数", `{"points":-5,"channel":"mock"}`},
		{"body 非法 JSON", `{"points":`},
		{"points 超上限（溢出防护）", `{"points":1099511627777,"channel":"mock"}`},
	}
	for _, c := range cases {
		got := callPayHonesty(t, s.handlePayCreate, http.MethodPost, "/api/pay/create", c.body, tok)
		assertPayHonesty(t, "pay/create "+c.name, got, http.StatusBadRequest, "VALIDATION_ERROR")
	}
	// 渠道白名单外 → 同样 400（不是 404：渠道不是资源，没有「找不到」一说）
	got := callPayHonesty(t, s.handlePayCreate, http.MethodPost, "/api/pay/create", `{"points":10,"channel":"paypal"}`, tok)
	assertPayHonesty(t, "pay/create 未知渠道", got, http.StatusBadRequest, "VALIDATION_ERROR")
	// 状态码不许是 404 —— 顺手钉住「按语义选码」而不是「随便挑个 4xx」
	if got.status == http.StatusNotFound {
		t.Errorf("未知渠道被报成 404：渠道白名单校验是请求本身写错了，应为 400")
	}
}

// TestPayStatusHonesty401And403 未登录与越权必须分开：401 只清态回登录页，403 是「你是谁都知道你不行」。
// 两者混用会让 core 的 401 拦截器要么漏触发（把 403 当 401 清登录态＝用户被踢出），
// 要么不触发（把 401 当 403＝token 过期了还停在页面上反复撞闸）。
func TestPayStatusHonesty401And403(t *testing.T) {
	s, tid, _ := newPayHonestyProbe(t)
	// 低权限凭证：同租户下的普通成员（等级 1 < tenant_admin 的 3），用来反证 403 没被误翻
	mu, err := s.Store.CreateUser(tid, "payhonesty-member", auth.PasswordHash("pw123456"), "探针成员", iam.RoleUser, 0, 0)
	if err != nil {
		t.Fatalf("建探针成员: %v", err)
	}
	memberTok, err := auth.Sign(mu, time.Hour)
	if err != nil {
		t.Fatalf("签发成员 JWT: %v", err)
	}
	// ① 匿名 → /api/pay/create：401 未登录（★ server.go writeAuthzError 的分流）。
	//    旧写法一律 403，而 403 在 core.ts 里**不会**触发会话过期处理（只挂 401），
	//    于是 token 过期的客户在收银台点「确认支付」得到「无权限」、停在原页反复撞闸。
	//    这里钉死 401，防止有人把分流改回 403 还自觉「行为没变」。
	anon := callPayHonesty(t, s.handlePayCreate, http.MethodPost, "/api/pay/create", `{"points":10,"channel":"mock"}`, "")
	assertPayHonesty(t, "pay/create 匿名", anon, http.StatusUnauthorized, "UNAUTHORIZED")
	// 反证：登录了但角色不够（普通成员 token）必须还是 403，不许被 401 分流顺手带走
	got := callPayHonesty(t, s.handlePayCreate, http.MethodPost, "/api/pay/create", `{"points":10,"channel":"mock"}`, memberTok)
	assertPayHonesty(t, "pay/create 低权限", got, http.StatusForbidden, "FORBIDDEN")
	// ② /api/me/package 是本批刻意留在 401 的一条（unauthorized 而不是 forbid）——
	//    它是「我的套餐」，任何登录态问题都该把用户送回登录页，而不是报「无权限」。
	if got := callPayHonesty(t, s.handleMyPackage, http.MethodGet, "/api/me/package", "", ""); got.status != http.StatusUnauthorized {
		t.Errorf("/api/me/package 匿名应为 401（触发全局清态回登录页），实得 %d（body=%s）", got.status, truncPayHonesty(got.raw))
	} else {
		assertPayHonesty(t, "me/package 匿名", got, http.StatusUnauthorized, "UNAUTHORIZED")
	}
}

// TestPayStatusHonesty404OrderNotFound 查无此单必须 404，且**跨租户不得泄露存在性**：
// 本租户查不到的单，不能因为「另一个租户有」就回 409/带单号的文案（那是枚举订单的侧信道）。
func TestPayStatusHonesty404OrderNotFound(t *testing.T) {
	s, _, tok := newPayHonestyProbe(t)
	cases := []struct {
		name string
		fn   func(http.ResponseWriter, *http.Request)
		m    string
		p    string
		body string
	}{
		{"pay/status", s.handlePayStatus, http.MethodGet, "/api/pay/status?order_id=987654321", ""},
		{"pay/simulate", s.handlePaySimulate, http.MethodPost, "/api/pay/simulate", `{"order_id":987654321}`},
		{"pay/manual-confirm", s.handlePayManualConfirm, http.MethodPost, "/api/pay/manual-confirm", `{"order_id":987654321}`},
	}
	for _, c := range cases {
		got := callPayHonesty(t, c.fn, c.m, c.p, c.body, tok)
		// manual-confirm 对「查无此单」的选择是 404；simulate 亦是；status 亦是。
		assertPayHonesty(t, c.name+" 查无此单", got, http.StatusNotFound, "NOT_FOUND")
	}
	// 缺参 → 400（不是 404：连单号都没有，谈不上「找不到」）
	if got := callPayHonesty(t, s.handlePayStatus, http.MethodGet, "/api/pay/status", "", tok); got.status != http.StatusBadRequest {
		t.Errorf("pay/status 缺 order_id 应 400，实得 %d（body=%s）", got.status, truncPayHonesty(got.raw))
	}
}

// TestPayStatusHonesty409StateConflict 单在、但当前状态不允许该动作 → 409（与 404 严格分开）。
// 由来：旧写法把「查无此单」与「渠道非 mock 不可模拟」挤成同一句 200 壳，
// 用户看到「订单不存在」其实单子在，于是重复建单 → 重复付款风险。
func TestPayStatusHonesty409StateConflict(t *testing.T) {
	s, tid, tok := newPayHonestyProbe(t)
	// 造一张 manual 渠道的 pending 单（不是 mock，所以模拟支付必须被状态冲突拒绝）
	o, err := s.Store.CreateOrderChannel(tid, s.Store.TokensFromPoints(100), 0, 0, "manual", "")
	if err != nil || o == nil {
		t.Fatalf("造 manual 订单失败: %v", err)
	}
	got := callPayHonesty(t, s.handlePaySimulate, http.MethodPost, "/api/pay/simulate",
		`{"order_id":`+itoaPayHonesty(o.ID)+`}`, tok)
	assertPayHonesty(t, "pay/simulate 渠道非 mock", got, http.StatusConflict, "CONFLICT")
	// 反证：这条断言不能因为「单根本没建成」而假绿——单必须真的在库里
	if again := callPayHonesty(t, s.handlePayStatus, http.MethodGet, "/api/pay/status?order_id="+itoaPayHonesty(o.ID), "", tok); again.status != http.StatusOK {
		t.Errorf("探针订单 %d 查不到（%d），上面的 409 断言已失去前提", o.ID, again.status)
	}
}

// TestPayStatusHonesty503ChannelUnavailable 收款能力未就绪 → 503 + PAY_CHANNEL_UNAVAILABLE。
// 为什么不是 500：静态收款码没配，是运营侧差一个配置项，客户侧什么都不用改；
// 报 500 会让前端把它归入「服务端故障」，既不会提示「请联系管理员配置」，
// 也不会让监控按「依赖未就绪」这条单独告警。
// 为什么不是 400：客户没做错任何事。
func TestPayStatusHonesty503ChannelUnavailable(t *testing.T) {
	s, _, tok := newPayHonestyProbe(t)
	// 前置：确保 static_qr_image 未配置（内存库天然是空配置，这里显式清一遍防历史残留）
	if v, _ := s.Store.GetConfig("static_qr_image"); v != "" {
		t.Fatalf("探针库不该预置 static_qr_image（ got %q）", v)
	}
	got := callPayHonesty(t, s.handlePayCreate, http.MethodPost, "/api/pay/create", `{"points":100,"channel":"manual"}`, tok)
	assertPayHonesty(t, "pay/create 静态码未配置", got, http.StatusServiceUnavailable, "PAY_CHANNEL_UNAVAILABLE")
	if !strings.Contains(got.message, "管理员") {
		t.Errorf("503 文案要告诉客户下一步做什么（联系管理员配置），实得 %q", got.message)
	}
}

// TestPayStatusHonesty503NoLeakOnFailure 503 响应体不得把内部实现细节吐给客户：
// 只给中文文案与错误码，DB 错误原文/表名/SQL 一律不出现在 message。
// （publicErrMessage 的脱敏在 #37 已收口，本锁防止有人在这批迁移时顺手把 err.Error() 拼进 message。）
func TestPayStatusHonesty503NoLeakOnFailure(t *testing.T) {
	s, _, tok := newPayHonestyProbe(t)
	for _, c := range []struct {
		name string
		fn   func(http.ResponseWriter, *http.Request)
		body string
	}{
		{"建单静态码未配置", s.handlePayCreate, `{"points":100,"channel":"manual"}`},
	} {
		got := callPayHonesty(t, c.fn, http.MethodPost, "/api/pay/create", c.body, tok)
		low := strings.ToLower(got.raw)
		for _, bad := range []string{"sql", "no such table", "constraint", "panic", "goroutine", "dsn", "password"} {
			if strings.Contains(low, bad) {
				t.Errorf("%s：响应体出现内部细节关键词 %q（body=%s）", c.name, bad, truncPayHonesty(got.raw))
			}
		}
	}
}

// itoaPayHonesty 极简 int64→string（不额外 import strconv 的格式化栈）。
func itoaPayHonesty(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var b [20]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
