// ============ 本文件职责中文说明 ============
// identity_test.go · 多身份账号方案（多身份账号方案_20261023.md）B2 批的 API 层机械锁。
//
// 覆盖方案 §十一 点名的三条 + B2 自身三条收口判据，共七组：
//
//	① 身份清单 identitiesFor 的三种返回形态（单身份／多身份／未迁移行）；
//	② GET /api/auth/identities 的鉴权与出栈形状（未登录 401、恒数组不恒 null）；
//	③ §十一·3 切换守卫：跨账户 ⇒ 403、目标行已停用 ⇒ 403、目标租户停用/到期 ⇒ 403，
//	   且**三种失败回同一句话**（防枚举：分开回等于送一条"哪些企业号存在"的探针）；
//	④ §十一·4 管理员移除成员：企业行停用、家行仍可定位且带口令、该邮箱在企业租户里被释放
//	   后可再建号；被移除的正是家行（纯企业用户）⇒ 个人行提升为家；
//	⑤ §十一·5 会员侧**没有**自助退出端点：拿真实路由注册表判（含正向对照，防止空转）
//	   ＋ 打真实 mux 必 404；
//	⑥ D5 口令写点收口：只写家行、成员行不残留可用哈希、token_version 只在家行递增（D7）、
//	   keepForceChange 两档的语义（代客重置／legacy 升级不许洗掉首登强制改密闸）、
//	   未迁移行退回旧的按行写法（B1 出厂判据「一人一行逐字节等价」）；
//	⑦ D4 成员行会话改密：原密码拿家行那一份验（credentialRowFor），改完家行口令生效、
//	   成员行会话不受影响、家行旧会话被撤销。
//
// 判据取向：全部走**内存 SQLite + 真实 handler**（与 pay_renew_test.go 同一夹具形态），
// 因为这一族缺陷从来不在纯函数里——它是"守卫的接线顺序""审计归属哪一家""切换后 token 带着谁的版本号"。
// ★ 单测自钉 sqlite 方言（AGENTS.md §一·4），避免 run_uat 的 PG 模式泄漏给同包内存库用例。
// ⚠️ 修改此处请同步更新本说明。
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

// identPassword 测试口令（走 auth.PasswordHash 的真实 bcrypt 路径，且过 auth.ValidatePassword 强度尺）。
const identPassword = "Ident-2026-passw0rd"

// identFixture 多身份测试栈：两个租户（个人版＋企业）＋真实 store/tenant＋签发 JWT 打 handler。
type identFixture struct {
	raw  *sql.DB
	srv  *Server
	st   *store.Store
	ten  *tenant.Store
	pTID int64 // 个人版租户（is_personal=true）
	cTID int64 // 企业租户
}

// newIdentFixture 搭测试栈。每个用例各自建一份内存库，避免账户号/邮箱在用例间互相占用。
func newIdentFixture(t *testing.T) *identFixture {
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
	p, err := ten.Create("t_ident_personal", "个人版测试", "", "{}")
	if err != nil {
		t.Fatalf("创建个人租户失败: %v", err)
	}
	if err := ten.SetPersonal(p.ID, true); err != nil {
		t.Fatalf("标记个人租户失败: %v", err)
	}
	c, err := ten.Create("t_ident_company", "企业测试甲", "", "{}")
	if err != nil {
		t.Fatalf("创建企业租户失败: %v", err)
	}
	return &identFixture{
		raw: raw, srv: &Server{Cfg: cfg, Store: st, Ten: ten},
		st: st, ten: ten, pTID: p.ID, cTID: c.ID,
	}
}

// identityMux 只挂本批相关的 handler（专用最小 mux，与生产路由同源的 handler 本体）。
func (s *Server) identityMux() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("/api/auth/identities", s.handleIdentities)
	m.HandleFunc("/api/auth/switch-identity", s.handleSwitchIdentity)
	m.HandleFunc("/api/auth/change-password", s.handleChangePassword)
	m.HandleFunc("/api/auth/login", s.handleLogin)
	m.HandleFunc("/api/admin/users/update", s.handleAdminUserUpdate)
	m.HandleFunc("/api/admin/users/delete", s.handleAdminUserDelete)
	m.HandleFunc("/api/admin/users/reset-password", s.handleAdminUserResetPassword)
	return m
}

// call 发一个带 Bearer 的请求；body 为 nil 时不带请求体（GET 语义）。
func (f *identFixture) call(t *testing.T, method, path string, body interface{}, token string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == nil {
		r = httptest.NewRequest(method, path, nil)
	} else {
		b, _ := json.Marshal(body)
		r = httptest.NewRequest(method, path, bytes.NewReader(b))
		r.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	f.srv.identityMux().ServeHTTP(rec, r)
	return rec
}

// sign 为指定身份行签发会话 token（与生产同一条 auth.Sign）。
func (f *identFixture) sign(t *testing.T, u *store.User) string {
	t.Helper()
	tok, err := auth.Sign(u, 24*time.Hour)
	if err != nil {
		t.Fatalf("签发 JWT 失败: %v", err)
	}
	return tok
}

// newAccount 建一个账户的家行（个人版租户，带 bcrypt 口令）。
func (f *identFixture) newAccount(t *testing.T, username, email string) *store.User {
	t.Helper()
	u, err := f.st.CreateUserWithEmail(f.pTID, username, auth.PasswordHash(identPassword), username, store.RoleUser, 0, 0, email, "", 0)
	if err != nil {
		t.Fatalf("创建个人账户失败: %v", err)
	}
	return u
}

// join 为既有账户在指定租户建一条成员行（D4：成员行不含凭证、is_home=0）。
func (f *identFixture) join(t *testing.T, home *store.User, tid int64, role string) *store.User {
	t.Helper()
	m, err := f.st.CreateIdentityRow(home.AccountID, tid, home.Username+"-co", home.DisplayName, role, 0, 0, home.Email)
	if err != nil {
		t.Fatalf("创建成员身份行失败: %v", err)
	}
	return m
}

// mustGet 读一行并失败即终止（省略每个断言里的 err 分支噪音）。
func (f *identFixture) mustGet(t *testing.T, id, tid int64) *store.User {
	t.Helper()
	u, err := f.st.GetUser(id, tid)
	if err != nil || u == nil {
		t.Fatalf("读取身份行 id=%d tid=%d 失败: %v", id, tid, err)
	}
	return u
}

// rawExec 直接写库（只用于构造"未迁移行"这类迁移前形态）。
func (f *identFixture) rawExec(t *testing.T, query string, args ...interface{}) {
	t.Helper()
	if _, err := f.raw.Exec(query, args...); err != nil {
		t.Fatalf("构造测试前置数据失败: %v", err)
	}
}

// decode 复用包内已有助手（coupons_api_test.go: 解析失败即 t.Fatalf，形态与本文件判据一致）。

// identArr 取出栈里的 identities 数组（顺带判「恒数组、不是 null」这一条形状契约）。
func identArr(t *testing.T, body map[string]interface{}) []interface{} {
	t.Helper()
	raw, ok := body["identities"]
	if !ok {
		t.Fatalf("响应缺少 identities 字段: %v", body)
	}
	if raw == nil {
		t.Fatalf("identities 出栈成 null（前端每个消费方都要各判一次，应在服务端归一成 []）")
	}
	arr, ok := raw.([]interface{})
	if !ok {
		t.Fatalf("identities 不是数组: %T", raw)
	}
	return arr
}

// idItem 按租户号在 identities 数组里取一项；第二返回值表示是否存在。
func idItem(t *testing.T, body map[string]interface{}, tid int64) (map[string]interface{}, bool) {
	t.Helper()
	for _, it := range identArr(t, body) {
		m, ok := it.(map[string]interface{})
		if !ok {
			continue
		}
		if v, ok := m["tid"].(float64); ok && int64(v) == tid {
			return m, true
		}
	}
	return nil, false
}

// num 把 JSON 数字读成 int64。
func num(m map[string]interface{}, key string) int64 {
	if v, ok := m[key].(float64); ok {
		return int64(v)
	}
	return -1
}

// ---- ① identitiesFor 三种形态 ----

func TestIdentity01IdentitiesForShapes(t *testing.T) {
	f := newIdentFixture(t)
	home := f.newAccount(t, "alice", "alice@example.test")
	co := f.join(t, home, f.cTID, store.RoleUser)

	// 多身份：家行在前、两行都带租户名与个人版标记。
	ids := f.srv.identitiesFor(home)
	if len(ids) != 2 {
		t.Fatalf("多身份账户应回 2 条身份，实得 %d: %+v", len(ids), ids)
	}
	if !ids[0].IsHome || ids[0].TID != f.pTID {
		t.Fatalf("身份列表必须家行在前，实得首条 %+v", ids[0])
	}
	if ids[1].UID != co.ID || ids[1].IsHome {
		t.Fatalf("第二条应是刚建的成员行（uid=%d、is_home=false），实得 %+v", co.ID, ids[1])
	}
	if ids[0].TenantName != "个人版测试" || !ids[0].IsPersonal {
		t.Fatalf("个人版身份的租户名/个人标记不对: %+v", ids[0])
	}
	if ids[1].TenantName != "企业测试甲" || ids[1].IsPersonal {
		t.Fatalf("企业身份的租户名/个人标记不对: %+v", ids[1])
	}
	if ids[1].TenantStatus != tenant.StatusActive {
		t.Fatalf("成员行身份状态应 active（前端据此不置灰），实得 %q", ids[1].TenantStatus)
	}
	// 成员行的口令哈希绝不出 IAM 层，也不进出栈结构（结构里根本没有这个字段，这里核 store 侧）。
	if got := f.mustGet(t, co.ID, f.cTID); got.PasswordHash != "" {
		t.Fatalf("D4 要求成员行不含凭证，实得非空哈希")
	}

	// 单身份（只有家行）：回 1 条，前端据此不渲染切换 icon。
	sole := f.newAccount(t, "bob", "bob@example.test")
	if one := f.srv.identitiesFor(sole); len(one) != 1 || !one[0].IsHome || one[0].TID != f.pTID {
		t.Fatalf("单身份账户应回 1 条家身份，实得 %+v", one)
	}

	// 未迁移行（account_id 空）：回自己这一行，形状与改造前的单身份用户一致。
	legacy := f.newAccount(t, "carol", "carol@example.test")
	f.rawExec(t, `UPDATE users SET account_id='', is_home=0 WHERE id=?`, legacy.ID)
	legacyRow := f.mustGet(t, legacy.ID, f.pTID)
	if got := f.srv.identitiesFor(legacyRow); len(got) != 1 || got[0].UID != legacy.ID {
		t.Fatalf("未迁移行应回落本行一条身份，实得 %+v", got)
	}

	// nil 入参不出栈 panic。
	if got := f.srv.identitiesFor(nil); got != nil {
		t.Fatalf("nil 用户应回 nil，实得 %+v", got)
	}
}

// ---- ② 身份清单端点 ----

func TestIdentity02IdentitiesEndpoint(t *testing.T) {
	f := newIdentFixture(t)
	home := f.newAccount(t, "dave", "dave@example.test")
	member := f.join(t, home, f.cTID, store.RoleUser)

	// 未登录 401（不是 200 + 空数组：这一族接口是已登录态动作）。
	if rec := f.call(t, http.MethodGet, "/api/auth/identities", nil, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("未登录打身份清单应 401，实得 %d %s", rec.Code, rec.Body.String())
	}

	rec := f.call(t, http.MethodGet, "/api/auth/identities", nil, f.sign(t, home))
	if rec.Code != http.StatusOK {
		t.Fatalf("登录态取身份清单应 200，实得 %d %s", rec.Code, rec.Body.String())
	}
	body := decode(t, rec)
	if body["success"] != true {
		t.Fatalf("身份清单应回 success=true，实得 %v", body)
	}
	if len(identArr(t, body)) != 2 {
		t.Fatalf("应列出 2 条身份，实得 %d", len(identArr(t, body)))
	}
	// 按租户号定位那一条身份后核对出栈字段：前端切换器就按这两个字段渲染与定位，
	// 只数数组长度等于什么都没判（放两条任意 map 也能过）。
	coItem, ok := idItem(t, body, f.cTID)
	if !ok {
		t.Fatalf("身份清单里找不到企业身份那条（tid=%d）: %v", f.cTID, identArr(t, body))
	}
	if got := num(coItem, "uid"); got != member.ID {
		t.Fatalf("企业身份的 uid 应指向成员行 %d，实得 %d", member.ID, got)
	}
	if coItem["is_home"] != false {
		t.Fatalf("成员行身份的 is_home 应为 false，实得 %v", coItem["is_home"])
	}
	if coItem["is_personal"] != false {
		t.Fatalf("企业身份不该被标成个人版，实得 %v", coItem["is_personal"])
	}

	// 成员行会话也取得到同一份账户身份（切换后顶栏要能再切回来）。
	co := f.mustGet(t, member.ID, f.cTID)
	rec2 := f.call(t, http.MethodGet, "/api/auth/identities", nil, f.sign(t, co))
	if rec2.Code != http.StatusOK || len(identArr(t, decode(t, rec2))) != 2 {
		t.Fatalf("成员行会话应取到 2 条身份，实得 %d %s", rec2.Code, rec2.Body.String())
	}
}

// ---- ③ §十一·3 切换守卫 ----

func TestIdentity03SwitchGuards(t *testing.T) {
	f := newIdentFixture(t)
	home := f.newAccount(t, "erin", "erin@example.test")
	member := f.join(t, home, f.cTID, store.RoleUser)
	homeTok := f.sign(t, home)

	// 另一条独立账户在第三家企业里有行 ⇒ erin 切过去必须是越权（不是"目标不存在"那种可区分回执）。
	other, err := f.ten.Create("t_ident_company_b", "企业测试乙", "", "{}")
	if err != nil {
		t.Fatalf("创建第二家企业失败: %v", err)
	}
	bob := f.newAccount(t, "bob2", "bob2@example.test")
	f.join(t, bob, other.ID, store.RoleUser)

	const denied = "无权切换到该身份"
	// 三档失败各自的期望回执（全部同一句话、同一个状态码）。
	type c struct {
		name  string
		tid   int64
		seed  func()
		after func()
	}
	cases := []c{
		{name: "跨账户（目标行属于别人）", tid: other.ID},
		{name: "目标行已被管理员停用", tid: f.cTID,
			seed: func() {
				mustNoErr(t, f.st.UpdateUser(member.ID, f.cTID, member.DisplayName, member.Role, store.UserDisabled, 0), "停用成员行")
			},
			after: func() {
				mustNoErr(t, f.st.UpdateUser(member.ID, f.cTID, member.DisplayName, member.Role, store.UserActive, 0), "恢复成员行")
			}},
		{name: "目标租户被停用", tid: f.cTID,
			seed:  func() { mustNoErr(t, f.ten.SetStatus(f.cTID, tenant.StatusDisabled), "停用企业租户") },
			after: func() { mustNoErr(t, f.ten.SetStatus(f.cTID, tenant.StatusActive), "恢复企业租户") }},
		{name: "目标租户已到期", tid: f.cTID,
			seed:  func() { mustNoErr(t, f.ten.SetStatus(f.cTID, tenant.StatusExpired), "置企业租户到期") },
			after: func() { mustNoErr(t, f.ten.SetStatus(f.cTID, tenant.StatusActive), "恢复企业租户") }},
	}
	for _, tc := range cases {
		if tc.seed != nil {
			tc.seed()
		}
		rec := f.call(t, http.MethodPost, "/api/auth/switch-identity", map[string]interface{}{"tid": tc.tid}, homeTok)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s：应 403，实得 %d %s", tc.name, rec.Code, rec.Body.String())
		}
		if got := decode(t, rec)["message"]; got != denied {
			t.Fatalf("%s：403 文案必须与其余越权分支逐字相同（分开回＝送一条枚举探针），实得 %q", tc.name, got)
		}
		if tc.after != nil {
			tc.after()
		}
	}

	// 缺 tid 是载荷问题（400），不占 403 那一档。
	if rec := f.call(t, http.MethodPost, "/api/auth/switch-identity", map[string]interface{}{}, homeTok); rec.Code != http.StatusBadRequest {
		t.Fatalf("缺 tid 应 400，实得 %d %s", rec.Code, rec.Body.String())
	}

	// 正常切换：同账户另一身份 ⇒ 200 + 新 token（带目标 tid）+ user + identities，并在目标租户留审计。
	rec := f.call(t, http.MethodPost, "/api/auth/switch-identity", map[string]interface{}{"tid": f.cTID}, homeTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("切到本账户的企业身份应 200，实得 %d %s", rec.Code, rec.Body.String())
	}
	body := decode(t, rec)
	tokStr, _ := body["token"].(string)
	if tokStr == "" {
		t.Fatalf("切换成功必须下发新 token，实得 %v", body)
	}
	claims, err := auth.Verify(tokStr)
	if err != nil {
		t.Fatalf("新 token 验签失败: %v", err)
	}
	if claims.TenantID != f.cTID || claims.UserID != member.ID {
		t.Fatalf("新 token 必须落在目标身份 (uid=%d,tid=%d)，实得 uid=%d tid=%d", member.ID, f.cTID, claims.UserID, claims.TenantID)
	}
	userObj, _ := body["user"].(map[string]interface{})
	if userObj == nil || num(userObj, "tenant_id") != f.cTID {
		t.Fatalf("切换响应要带目标身份的 user，实得 %v", body["user"])
	}
	if len(identArr(t, body)) != 2 {
		t.Fatalf("切换响应要带全量身份列表（前端切完不用二次拉取），实得 %d", len(identArr(t, body)))
	}
	// 审计归属**目标**租户（切进哪家就在哪家留痕），detail 出 from→to 两个租户号。
	logs, err := f.st.ListAuditFilter(f.cTID, "identity_switch", "auth", member.ID, "", "", 10)
	if err != nil || len(logs) == 0 {
		t.Fatalf("切换身份应在目标租户留下 identity_switch 审计，实得 %d 条 err=%v", len(logs), err)
	}
	if !strings.Contains(logs[0].Detail, "身份切换") || !strings.Contains(logs[0].Detail, "→") {
		t.Fatalf("identity_switch 审计 detail 应写明 from→to，实得 %q", logs[0].Detail)
	}

	// 反向对照：未登录切身份 401（证明上面那批 403 来自守卫而不是"压根没鉴权"）。
	if rec := f.call(t, http.MethodPost, "/api/auth/switch-identity", map[string]interface{}{"tid": f.cTID}, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("未登录切身份应 401，实得 %d", rec.Code)
	}
}

// mustNoErr 前置构造的小助手：失败即终止并点名是哪一步。
func mustNoErr(t *testing.T, err error, what string) {
	t.Helper()
	if err != nil {
		t.Fatalf("构造前置失败（%s）: %v", what, err)
	}
}

// ---- ④ §十一·4 管理员移除成员 ----

func TestIdentity04AdminRemovesMember(t *testing.T) {
	f := newIdentFixture(t)
	// 企业管理员（操作者）
	coAdmin, err := f.st.CreateUser(f.cTID, "co_admin", auth.PasswordHash(identPassword), "企业管理员", store.RoleTenantAdmin, 1, 0)
	if err != nil {
		t.Fatalf("创建企业管理员失败: %v", err)
	}
	coAdminTok := f.sign(t, coAdmin)

	// 情形 A：个人版是家、企业行是成员行 ⇒ 移除只撤企业身份，家行不受影响。
	home := f.newAccount(t, "frank", "frank@example.test")
	member := f.join(t, home, f.cTID, store.RoleUser)
	rec := f.call(t, http.MethodPost, "/api/admin/users/update",
		map[string]interface{}{"id": member.ID, "status": store.UserDisabled}, coAdminTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("管理员移除成员应 200，实得 %d %s", rec.Code, rec.Body.String())
	}
	if after := f.mustGet(t, member.ID, f.cTID); after.Status != store.UserDisabled {
		t.Fatalf("企业身份行应落停用态，实得 %q", after.Status)
	}
	if after := f.mustGet(t, home.ID, f.pTID); after.Status != store.UserActive || after.PasswordHash == "" || after.IsHome != 1 {
		t.Fatalf("家行必须仍是家、仍可用、仍带口令（登录永远家优先验口令），实得 status=%q is_home=%d 哈希空=%v",
			after.Status, after.IsHome, after.PasswordHash == "")
	}
	// 家优先解析落在家行（这个人还能登进来）。
	if byEmail, e := f.st.GetUserByEmail("frank@example.test"); e != nil || byEmail == nil || byEmail.ID != home.ID {
		t.Fatalf("移除企业身份后按邮箱取人应落回家行，实得 %+v err=%v", byEmail, e)
	}
	// 邮箱在企业租户里被释放 ⇒ 同邮箱可在该企业再建号（建新行，不复用被停用的旧行 —— R3）。
	if _, e := f.st.CreateUserWithEmail(f.cTID, "frank2", auth.PasswordHash(identPassword), "Frank 新号", store.RoleUser, 0, 0, "frank@example.test", "", 0); e != nil {
		t.Fatalf("成员停用后该邮箱应能在同企业重新持码加入，实得错误: %v", e)
	}
	// 两条审计：既有 user_update 一字不动 ＋ 新增 tenant_leave（方案 §九 动作名）。
	if logs, e := f.st.ListAuditFilter(f.cTID, "tenant_leave", "users", coAdmin.ID, "", "", 10); e != nil || len(logs) == 0 {
		t.Fatalf("移除成员应写一条 tenant_leave 审计，实得 %d 条 err=%v", len(logs), e)
	} else if !strings.Contains(logs[0].Detail, "frank") {
		t.Fatalf("tenant_leave 审计 detail 要点名被撤账号，实得 %q", logs[0].Detail)
	}
	if logs, e := f.st.ListAuditFilter(f.cTID, "user_update", "users", coAdmin.ID, "", "", 10); e != nil || len(logs) == 0 {
		t.Fatalf("原有 user_update 审计不得被新轨迹替换掉，实得 %d 条 err=%v", len(logs), e)
	}
	// 非移除类改动（只改展示名）不该产生 tenant_leave 轨迹。
	if rec := f.call(t, http.MethodPost, "/api/admin/users/update",
		map[string]interface{}{"id": member.ID, "display_name": "Frank 改名"}, coAdminTok); rec.Code != http.StatusOK {
		t.Fatalf("仅改名应 200，实得 %d %s", rec.Code, rec.Body.String())
	}
	if logs, e := f.st.ListAuditFilter(f.cTID, "tenant_leave", "users", coAdmin.ID, "", "", 100); e != nil {
		t.Fatalf("读审计失败: %v", e)
	} else if len(logs) != 1 {
		t.Fatalf("tenant_leave 只在真撤身份时写，改名不该再写一条，实得 %d 条", len(logs))
	}

	// 情形 B：纯企业用户（家行就在企业）⇒ 移除前先把个人行提升为家（D2 不变式）。
	pureHome, err := f.st.CreateUserWithEmail(f.cTID, "grace-co", auth.PasswordHash(identPassword), "Grace（企业为家）", store.RoleUser, 0, 0, "grace@example.test", "", 0)
	if err != nil {
		t.Fatalf("创建纯企业用户失败: %v", err)
	}
	if pureHome.IsHome != 1 {
		t.Fatalf("纯企业用户的首行应就是家行，实得 is_home=%d", pureHome.IsHome)
	}
	personal := f.join(t, pureHome, f.pTID, store.RoleUser) // 个人行此刻是成员行（is_home=0）
	rec = f.call(t, http.MethodPost, "/api/admin/users/update",
		map[string]interface{}{"id": pureHome.ID, "status": store.UserDisabled}, coAdminTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("移除纯企业用户的企业身份应 200，实得 %d %s", rec.Code, rec.Body.String())
	}
	if home2, e := f.st.GetHomeRow(pureHome.AccountID); e != nil || home2 == nil || home2.ID != personal.ID {
		t.Fatalf("被移除的正是家行时，个人行必须被提升为家（否则这个账户有可用行却没家，再也登不进来），实得 %+v err=%v", home2, e)
	}
	if got := f.mustGet(t, pureHome.ID, f.cTID); got.IsHome != 0 {
		t.Fatalf("被停用的原家行应摘掉家标记，实得 is_home=%d", got.IsHome)
	}
	if byEmail, e := f.st.GetUserByEmail("grace@example.test"); e != nil || byEmail == nil || byEmail.ID != personal.ID {
		t.Fatalf("搬家后按邮箱取人应落到个人行，实得 %+v err=%v", byEmail, e)
	}

	// 情形 C：删号（handleAdminUserDelete）同样先搬家。
	third, err := f.st.CreateUserWithEmail(f.cTID, "henry-co", auth.PasswordHash(identPassword), "Henry（企业为家）", store.RoleUser, 0, 0, "henry@example.test", "", 0)
	if err != nil {
		t.Fatalf("创建第三个纯企业用户失败: %v", err)
	}
	thirdPersonal := f.join(t, third, f.pTID, store.RoleUser)
	if rec := f.call(t, http.MethodPost, "/api/admin/users/delete", map[string]interface{}{"id": third.ID}, coAdminTok); rec.Code != http.StatusOK {
		t.Fatalf("删除企业身份行应 200，实得 %d %s", rec.Code, rec.Body.String())
	}
	if home3, e := f.st.GetHomeRow(third.AccountID); e != nil || home3 == nil || home3.ID != thirdPersonal.ID {
		t.Fatalf("删号路径也要先搬家，实得 %+v err=%v", home3, e)
	}
}

// ---- ⑤ §十一·5 会员侧无自助退出端点 ----

// TestIdentity05NoSelfLeaveRoute 判「退出企业只能由管理员做」（R8 拍板）在路由层成立。
//
// 判据走**真实路由注册表**（routeMux 记录了每一个注册串），不靠 grep 源码字符串：
// 正向对照先钉住"这一批确实注册了两个身份端点"，否则"没有任何退出端点"这句负向判据
// 会因为整张表为空而结构性恒真（AGENTS §一·5 那条「负向锁必须配正向对照」）。
func TestIdentity05NoSelfLeaveRoute(t *testing.T) {
	srv := &Server{mux: newRouteMux()}
	srv.routesAuth()
	patterns := map[string]bool{}
	for _, meta := range srv.mux.allRoutes() {
		patterns[meta.Pattern] = true
	}
	for _, must := range []string{"/api/auth/identities", "/api/auth/switch-identity"} {
		if !patterns[must] {
			t.Fatalf("身份端点必须注册在真实路由表里：%s 缺席 ⇒ 本闸门的负向判据失去对照（空表会让「没有退出端点」恒真）", must)
		}
	}
	// 会员侧自助退出/离开企业的各种可能命名一律不许存在（B4 前端也不该造这条腿）。
	forbidden := []string{"leave", "quit", "exit", "remove-self", "self-remove"}
	for p := range patterns {
		if !strings.HasPrefix(p, "/api/auth/") {
			continue
		}
		for _, word := range forbidden {
			if strings.Contains(strings.ToLower(p), word) {
				t.Fatalf("会员侧出现了自助退出端点 %q（含 %q）——方案 R8 拍板：退出只能由租户管理员在管理台移除成员完成", p, word)
			}
		}
	}

	// 反证锁的第二半：拿真实 mux 打一个"看起来像自助退出"的路径，必须 404。
	f := newIdentFixture(t)
	f.srv.mux = newRouteMux()
	f.srv.routesAuth()
	// 一个登录态凭证就够：两条路径共用它（同一邮箱在循环里各注册一次会被唯一性判重复，
	// 那是夹具自伤不是判据）。
	ivanTok := f.sign(t, f.newAccount(t, "ivan", "ivan@example.test"))
	for _, path := range []string{"/api/auth/leave-tenant", "/api/auth/identities/leave"} {
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader("{}"))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+ivanTok)
		rec := httptest.NewRecorder()
		f.srv.mux.ServeHTTP(rec, r)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("自助退出路径 %s 必须 404（没有这条端点），实得 %d %s", path, rec.Code, rec.Body.String())
		}
	}
}

// ---- ⑥ D5 口令写点收口 ----

func TestIdentity06SetAccountPasswordHomeOnly(t *testing.T) {
	f := newIdentFixture(t)
	home := f.newAccount(t, "judy", "judy@example.test")
	member := f.join(t, home, f.cTID, store.RoleUser)

	// 以成员行为入参写口令：落库的必须是**家行**，成员行不得残留第二份可用哈希（R1 备选 A 的病根）。
	n, err := f.srv.setAccountPassword(member, auth.PasswordHash("Rotated-2026-pass"), true)
	if err != nil || n != 1 {
		t.Fatalf("代客重置应真实写到家行（n=1），实得 n=%d err=%v", n, err)
	}
	if got := f.mustGet(t, home.ID, f.pTID); !auth.CheckPassword(got.PasswordHash, "Rotated-2026-pass") {
		t.Fatalf("家行口令未更新")
	}
	if got := f.mustGet(t, member.ID, f.cTID); got.PasswordHash != "" {
		t.Fatalf("成员行绝不能持有可用哈希（D4），实得非空")
	}

	// D7：token_version 只在家行递增 ⇒ 企业身份会话不受影响（换设备重登才需要），家行旧会话被撤销。
	if h := f.mustGet(t, home.ID, f.pTID); h.TokenVersion != 1 {
		t.Fatalf("家行 token_version 应 +1（撤销家行会话），实得 %d", h.TokenVersion)
	}
	if m := f.mustGet(t, member.ID, f.cTID); m.TokenVersion != 0 {
		t.Fatalf("D7 拍板成员行不受影响，实得 token_version=%d", m.TokenVersion)
	}

	// keepForceChange=true：代客重置/legacy 升级**不是**用户亲手设口令，两把首登闸必须原样留着。
	mustNoErr(t, f.st.SetMustChangePwd(home.ID, f.pTID, 1), "置强制改密闸")
	mustNoErr(t, f.st.SetProfileSetupRequired(home.ID, f.pTID, 1), "置资料补全提醒")
	if n, err = f.srv.setAccountPassword(home, auth.PasswordHash("Kept-2026-pass"), true); err != nil || n != 1 {
		t.Fatalf("带闸重置应成功，实得 n=%d err=%v", n, err)
	}
	if h := f.mustGet(t, home.ID, f.pTID); h.MustChangePwd != 1 || h.ProfileSetupRequired != 1 {
		t.Fatalf("keepForceChange=true 却洗掉了首登闸（批量导入的初始口令全靠这两把闸兜住）：must_change_pwd=%d profile_setup_required=%d",
			h.MustChangePwd, h.ProfileSetupRequired)
	}

	// keepForceChange=false：用户亲手改密，两把闸当场关（SetPasswordForAccount 自己就清）。
	if n, err = f.srv.setAccountPassword(home, auth.PasswordHash("Owned-2026-pass"), false); err != nil || n != 1 {
		t.Fatalf("自助改密应成功，实得 n=%d err=%v", n, err)
	}
	if h := f.mustGet(t, home.ID, f.pTID); h.MustChangePwd != 0 || h.ProfileSetupRequired != 0 {
		t.Fatalf("keepForceChange=false 应关掉两把首登闸，实得 %d / %d", h.MustChangePwd, h.ProfileSetupRequired)
	}

	// 未迁移行（account_id 空）退回旧的按 (id,tid) 写法：B1 出厂判据「一人一行逐字节等价」。
	legacy := f.newAccount(t, "kate", "kate@example.test")
	f.rawExec(t, `UPDATE users SET account_id='', is_home=0 WHERE id=?`, legacy.ID)
	legacyRow := f.mustGet(t, legacy.ID, f.pTID)
	n, err = f.srv.setAccountPassword(legacyRow, auth.PasswordHash("Legacy-2026-pass"), true)
	if err != nil || n != 1 {
		t.Fatalf("未迁移行应走旧的单行写法并真实写 1 行，实得 n=%d err=%v", n, err)
	}
	if got := f.mustGet(t, legacy.ID, f.pTID); !auth.CheckPassword(got.PasswordHash, "Legacy-2026-pass") {
		t.Fatalf("未迁移行的口令未写入本行（回落腿失效）")
	}

	// 账户没有家行（迁移没跑完／数据被并坏）⇒ 0 行必须如实回传，调用方判失败，禁止当成功。
	orphan := f.newAccount(t, "leo", "leo@example.test")
	f.rawExec(t, `UPDATE users SET is_home=0 WHERE id=?`, orphan.ID)
	if n, err = f.srv.setAccountPassword(orphan, auth.PasswordHash("NoHome-2026-pass"), false); n != 0 {
		t.Fatalf("账户无家行时必须如实回 0 行（0 行不是错误，但绝不能被写成成功），实得 n=%d err=%v", n, err)
	}
}

// ---- ⑦ D4 成员行会话改密 + 管理台代客重置 ----

func TestIdentity07MemberSessionChangePassword(t *testing.T) {
	f := newIdentFixture(t)
	home := f.newAccount(t, "mary", "mary@example.test")
	member := f.join(t, home, f.cTID, store.RoleUser)
	homeTok := f.sign(t, home)
	memberTok := f.sign(t, member)

	// 企业身份里改密：原密码拿家行那一份验（成员行本身没有凭证），失败形态是"请先获取邮箱验证码"。
	const newPassword = "Member-2026-pass"
	rec := f.call(t, http.MethodPost, "/api/auth/change-password",
		map[string]string{"old_password": identPassword, "new_password": newPassword}, memberTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("成员行会话凭原口令改密应 200，实得 %d %s", rec.Code, rec.Body.String())
	}
	if got := f.mustGet(t, home.ID, f.pTID); !auth.CheckPassword(got.PasswordHash, newPassword) {
		t.Fatalf("改密必须写到家行")
	}
	if got := f.mustGet(t, member.ID, f.cTID); got.PasswordHash != "" {
		t.Fatalf("改密不得给成员行留一份哈希")
	}
	// D7 落地形态：成员行会话仍可用，家行会话被撤销。
	if rec := f.call(t, http.MethodGet, "/api/auth/identities", nil, memberTok); rec.Code != http.StatusOK {
		t.Fatalf("改密只撤家行会话（D7），成员行会话应仍可用，实得 %d %s", rec.Code, rec.Body.String())
	}
	if rec := f.call(t, http.MethodGet, "/api/auth/identities", nil, homeTok); rec.Code != http.StatusUnauthorized {
		t.Fatalf("家行旧 token 应随改密失效，实得 %d", rec.Code)
	}

	// 原密码错误仍是 400（不许 401——前端见 401 会清会话把人踢回登录页）。
	rec = f.call(t, http.MethodPost, "/api/auth/change-password",
		map[string]string{"old_password": "Wrong-Old-Pass", "new_password": newPassword}, f.sign(t, f.mustGet(t, home.ID, f.pTID)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("原密码错误应 400，实得 %d %s", rec.Code, rec.Body.String())
	}

	// 管理台代客重置：写的仍是家行，且首登强制改密闸不被洗掉。
	coAdmin, err := f.st.CreateUser(f.cTID, "co_admin7", auth.PasswordHash(identPassword), "企业管理员", store.RoleTenantAdmin, 1, 0)
	if err != nil {
		t.Fatalf("创建企业管理员失败: %v", err)
	}
	mustNoErr(t, f.st.SetMustChangePwd(home.ID, f.pTID, 1), "置强制改密闸")
	rec = f.call(t, http.MethodPost, "/api/admin/users/reset-password",
		map[string]interface{}{"id": member.ID, "password": "Admin-Set-2026"}, f.sign(t, coAdmin))
	if rec.Code != http.StatusOK {
		t.Fatalf("管理台代客重置应 200，实得 %d %s", rec.Code, rec.Body.String())
	}
	if got := f.mustGet(t, home.ID, f.pTID); !auth.CheckPassword(got.PasswordHash, "Admin-Set-2026") {
		t.Fatalf("代客重置必须写到家行（登录永远家优先验口令）")
	}
	if got := f.mustGet(t, member.ID, f.cTID); got.PasswordHash != "" {
		t.Fatalf("代客重置不得给成员行留哈希")
	}
	if got := f.mustGet(t, home.ID, f.pTID); got.MustChangePwd != 1 {
		t.Fatalf("代客重置不是用户亲手设口令，强制改密闸必须原样留着，实得 %d", got.MustChangePwd)
	}
}

// ---- ⑧ legacy 哈希透明升级不洗闸（登录口那条腿） ----

func TestIdentity08LegacyUpgradeKeepsGate(t *testing.T) {
	f := newIdentFixture(t)
	// 造一个仍是历史哈希、且带着首登强制改密闸的存量账号（批量导入的形态）。
	u, err := f.st.CreateUserWithEmail(f.pTID, "nina", "legacy-hash-placeholder", "Nina", store.RoleUser, 0, 0, "nina@example.test", "", 0)
	if err != nil {
		t.Fatalf("创建存量账号失败: %v", err)
	}
	legacyHash := auth.PasswordHashLegacy(identPassword)
	if !auth.NeedMigrateHash(legacyHash) {
		t.Fatalf("夹具自身失效：造出来的哈希不属于待迁移形态")
	}
	f.rawExec(t, `UPDATE users SET password_hash=?, must_change_pwd=1 WHERE id=?`, legacyHash, u.ID)

	rec := f.call(t, http.MethodPost, "/api/auth/login",
		map[string]string{"email": "nina@example.test", "password": identPassword}, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("存量口令登录应 200，实得 %d %s", rec.Code, rec.Body.String())
	}
	body := decode(t, rec)
	got := f.mustGet(t, u.ID, f.pTID)
	if auth.NeedMigrateHash(got.PasswordHash) {
		t.Fatalf("登录后应把历史哈希透明升级为 bcrypt")
	}
	if !auth.CheckPassword(got.PasswordHash, identPassword) {
		t.Fatalf("透明升级必须仍以同一句口令可验")
	}
	if got.MustChangePwd != 1 {
		t.Fatalf("透明升级不是用户亲手换口令：一次登录就把「首登强制改密」闸洗掉了，实得 %d", got.MustChangePwd)
	}
	// 响应里的闸值与库里一致（前端 428 那一腿靠它出弹窗）。
	if userObj, ok := body["user"].(map[string]interface{}); !ok || num(userObj, "must_change_pwd") != 1 {
		t.Fatalf("登录响应要如实带出强制改密闸，实得 %v", body["user"])
	}
	// B2 登录响应带 identities：单身份账户回一条（前端据此不渲染切换 icon）。
	if len(identArr(t, body)) != 1 {
		t.Fatalf("单身份账户登录应带 1 条身份，实得 %d", len(identArr(t, body)))
	}
}
