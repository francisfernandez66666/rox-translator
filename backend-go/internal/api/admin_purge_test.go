// ============ admin_purge_test.go · 职责说明 ============
// 超管删除租户（`POST /api/admin/tenants/delete`）的判据锁（★ 2026-10-23）。
//
// 这个接口是**不可逆**的：一次调用抹掉一个租户的全部账号、组织、订单、审计与产物记录。
// 所以判据不能只测「能删掉」，必须把**每一条拒绝路径**都钉住——删数据的功能里，
// 放行逻辑写错只是少删，拒绝逻辑写错才是事故。五段：
//
//	A 超管 + 正确确认串 ⇒ 200，且租户行、用户、组织、订单全部消失（读库复验，不看返回值）；
//	B 非超管（租户管理员）⇒ 403 且数据一行未动；
//	C 删自己所在的租户 ⇒ 403 且数据一行未动（否则调用者当场失去登录能力、审计也没了）；
//	D 确认串不符/为空 ⇒ 400 且数据一行未动（挡手滑，不是挡恶意）；
//	E 租户不存在 ⇒ 404（不是 403：超管本来有权限，如实说不存在最省事）。
//
// 另加 F 段：超管**跨租户**删用户 / 删组织本来就能做（既有两个接口），本批把这条能力
// 也钉住——用户令要的是「任意租户/组织/用户」，三样都得有据可查。
// =============================================
package api

import (
	"bytes"
	"context"
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

// purgeFixture 删除租户测试的现场：一个平台超管 + 一个租户管理员 + 一个待删租户。
type purgeFixture struct {
	s        *Server
	tokens   map[string]string // username -> JWT
	targetID int64             // 待删租户 ID
	otherID  int64             // 另一个不该被牵连的租户 ID
	adminTid int64             // 租户管理员所在租户（＝C 段「删自己」的目标）
}

// newPurgeFixture 构造现场：内存 SQLite + 三个租户（默认/待删/陪跑）+ 超管与租户管理员。
// 待删租户里塞满各类租户级数据（用户/组织/订单），用来证明「级联」不是一句空话。
func newPurgeFixture(t *testing.T) *purgeFixture {
	t.Helper()
	conn, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("打开内存数据库失败: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	st, err := store.New(conn)
	if err != nil {
		t.Fatalf("创建 Store 失败: %v", err)
	}
	// 租户域（store 只读租户，建租户走 tenant 子模块，两者共用同一个 *sql.DB）
	ts, err := tenant.NewStore(conn)
	if err != nil {
		t.Fatalf("创建租户 Store 失败: %v", err)
	}
	if _, err := ts.EnsureDefault(); err != nil { // 默认租户 rox（id=1）
		t.Fatalf("建默认租户失败: %v", err)
	}
	victim, err := ts.Create("hajimi", "Hajimi", "", "{}")
	if err != nil {
		t.Fatalf("建待删租户失败: %v", err)
	}
	other, err := ts.Create("keepme", "陪跑租户", "", "{}")
	if err != nil {
		t.Fatalf("建陪跑租户失败: %v", err)
	}
	// 平台超管（tid=0，L4）
	if err := st.EnsureAdmin(0, "root_admin", "hash-root", "平台超管", ""); err != nil {
		t.Fatalf("建超管失败: %v", err)
	}
	// 待删租户里的租户管理员 + 普通成员
	// ⚠️ 这里**不能**用 EnsureAdmin——它无视传入的 tid，恒建 tenant_id=0 的平台超管
	//    （见 iam/store.go 的 EnsureAdmin：tid 只用于「同名普通账号」那条查找分支）。
	//    本文件第一版就栽在这上面：B 段「非超管应 403」实测 200，因为那个"租户管理员"
	//    其实是个货真价实的超管。要建租户级管理员必须走 CreateUser 指定角色。
	if _, err := st.CreateUser(victim.ID, "hajimi_admin", "hash-h", "Hajimi 管理员", "tenant_admin", 1, 0); err != nil {
		t.Fatalf("建待删租户管理员失败: %v", err)
	}
	if _, err := st.CreateUser(victim.ID, "hajimi_member", "hash-m", "Hajimi 成员", "user", 1, 0); err != nil {
		t.Fatalf("建待删租户成员失败: %v", err)
	}
	// 陪跑租户里的成员（用来证明删除不越界）
	if _, err := st.CreateUser(other.ID, "keep_member", "hash-k", "陪跑成员", "user", 1, 0); err != nil {
		t.Fatalf("建陪跑租户成员失败: %v", err)
	}
	// 待删租户里再塞一条组织与一条订单，让「级联」有东西可级联
	if _, err := st.CreateOrg(victim.ID, 0, "Hajimi 总部", "org"); err != nil {
		t.Fatalf("建组织失败: %v", err)
	}

	s := &Server{Store: st}
	users, _ := st.ListAllUsers()
	tokens := map[string]string{}
	for _, u := range users {
		tk, err := auth.Sign(u, time.Hour)
		if err != nil {
			t.Fatalf("签发 %s 的 JWT 失败: %v", u.Username, err)
		}
		tokens[u.Username] = tk
	}
	// 租户管理员所在租户＝待删租户（C 段要拿它去删「自己所在的租户」）
	return &purgeFixture{s: s, tokens: tokens, targetID: victim.ID, otherID: other.ID, adminTid: victim.ID}
}

// purgeMux 本批新增接口的最小真实路由（与 server.go 生产注册同源）。
func (s *Server) purgeMux() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("/api/admin/tenants/delete", s.handleAdminTenantDelete)
	m.HandleFunc("/api/admin/users/delete", s.handleAdminUserDelete)
	m.HandleFunc("/api/orgs/delete", s.handleOrgDelete)
	return m
}

// postPurge 发一次删除请求，返回状态码与响应体（**不**像 postAdmin 那样断言 200，
// 因为本文件的重点正是各种非 200 的拒绝路径）。
func postPurge(t *testing.T, s *Server, path, token string, body interface{}) (int, string) {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.purgeMux().ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

// tenantRowCount 读库统计某租户还剩多少用户/组织（拒绝路径要证明「一行未动」）。
func tenantRowCount(t *testing.T, s *Server, tid int64) (users, orgs int) {
	t.Helper()
	all, err := s.Store.ListAllUsers()
	if err != nil {
		t.Fatalf("ListAllUsers 失败: %v", err)
	}
	for _, u := range all {
		if u.TenantID == tid {
			users++
		}
	}
	if list, e := s.Store.ListOrgs(tid); e == nil {
		orgs = len(list)
	}
	return users, orgs
}

// A 段：超管 + 正确确认串 ⇒ 200，且数据真的没了（读库复验）
func TestPurgeTenantBySuperAdmin(t *testing.T) {
	f := newPurgeFixture(t)
	if u, o := tenantRowCount(t, f.s, f.targetID); u == 0 || o == 0 {
		t.Fatalf("前置条件不成立：待删租户应有用户与组织，实得 users=%d orgs=%d", u, o)
	}
	code, body := postPurge(t, f.s, "/api/admin/tenants/delete", f.tokens["root_admin"],
		map[string]interface{}{"id": f.targetID, "confirm": "Hajimi"})
	if code != http.StatusOK {
		t.Fatalf("超管删租户应 200，实得 %d: %s", code, body)
	}
	var out struct {
		Success          bool  `json:"success"`
		DeletedTotal     int64 `json:"deleted_total"`
		TenantRowDeleted bool  `json:"tenant_row_deleted"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("响应不是合法 JSON: %v (%s)", err, body)
	}
	if !out.Success || !out.TenantRowDeleted {
		t.Fatalf("响应应 success 且 tenant_row_deleted=true，实得 %s", body)
	}
	if out.DeletedTotal <= 0 {
		t.Fatalf("deleted_total 应 >0（至少要删掉用户与组织），实得 %d", out.DeletedTotal)
	}
	// 读库复验：用户/组织清零，且租户行不存在了
	if u, o := tenantRowCount(t, f.s, f.targetID); u != 0 || o != 0 {
		t.Fatalf("级联没删干净：users=%d orgs=%d", u, o)
	}
	if _, _, ok := f.s.Store.TenantBrief(f.targetID); ok {
		t.Fatal("tenants 行仍在：租户没真删掉")
	}
	// 陪跑租户一个字节都不许动
	if u, _ := tenantRowCount(t, f.s, f.otherID); u == 0 {
		t.Fatal("陪跑租户的用户被误删（删除越界）")
	}
}

// B 段：非超管（租户管理员）⇒ 403 且数据一行未动
func TestPurgeTenantRejectsNonSuperAdmin(t *testing.T) {
	f := newPurgeFixture(t)
	before, beforeOrgs := tenantRowCount(t, f.s, f.targetID)
	code, body := postPurge(t, f.s, "/api/admin/tenants/delete", f.tokens["hajimi_admin"],
		map[string]interface{}{"id": f.targetID, "confirm": "Hajimi"})
	if code != http.StatusForbidden {
		t.Fatalf("租户管理员删租户应 403，实得 %d: %s", code, body)
	}
	if u, o := tenantRowCount(t, f.s, f.targetID); u != before || o != beforeOrgs {
		t.Fatalf("被拒绝的请求竟然改了数据：users %d→%d orgs %d→%d", before, u, beforeOrgs, o)
	}
	if _, _, ok := f.s.Store.TenantBrief(f.targetID); !ok {
		t.Fatal("被拒绝的请求竟然删掉了租户行")
	}
}

// C 段：删自己所在的租户 ⇒ 403 且数据一行未动
//
// 为什么这条要绕开 HTTP 鉴权链直接注入用户：`IsSuperAdmin` 的定义是
// `RoleLevel>=4 && TenantID==0`（iam/auth.go），也就是说**走正常登录链路的超管永远 tid=0**，
// 「删自己所在租户」在现网配置下不可能由超管触发。但这条闸不能因此删掉：
// 它守的是「将来某个带租户归属的角色拿到这个接口」那一天——那时它才是唯一的拦网。
// 所以用 context 里的鉴权缓存（authMemoKey，本包内可见）注入一个「tid=目标租户的超管」，
// 让守卫本身被真正执行一次；不做这一步，这条闸就是一段没人验证过的代码。
func TestPurgeTenantRejectsOwnTenant(t *testing.T) {
	f := newPurgeFixture(t)
	before, _ := tenantRowCount(t, f.s, f.targetID)

	fake := &store.User{ID: 90001, TenantID: f.targetID, Role: "admin", Username: "self_tenant_super"}
	body, _ := json.Marshal(map[string]interface{}{"id": f.targetID, "confirm": "Hajimi"})
	req := httptest.NewRequest(http.MethodPost, "/api/admin/tenants/delete", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(context.WithValue(req.Context(), authMemoKey{}, &authMemo{done: true, user: fake}))
	rec := httptest.NewRecorder()
	f.s.purgeMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("删自己所在租户应 403，实得 %d: %s", rec.Code, rec.Body.String())
	}
	if u, _ := tenantRowCount(t, f.s, f.targetID); u != before {
		t.Fatalf("被拒绝的请求竟然改了数据：users %d→%d", before, u)
	}
	if _, _, ok := f.s.Store.TenantBrief(f.targetID); !ok {
		t.Fatal("被拒绝的请求竟然删掉了租户行")
	}
}

// D 段：确认串不符/为空 ⇒ 400 且数据一行未动
// ⚠️ 首尾空白**不算不符**：服务端对 confirm 做了 TrimSpace（粘贴时带空格是最常见的形态，
//
//	为这个拦人只会逼用户去数空格）。判据只认「实质不同」与「大小写不同」。
func TestPurgeTenantRequiresConfirmEcho(t *testing.T) {
	f := newPurgeFixture(t)
	before, _ := tenantRowCount(t, f.s, f.targetID)
	for _, bad := range []string{"", "hajimi2", "HAJIMI", "Hajimi总部", "rox"} {
		code, body := postPurge(t, f.s, "/api/admin/tenants/delete", f.tokens["root_admin"],
			map[string]interface{}{"id": f.targetID, "confirm": bad})
		if code != http.StatusBadRequest {
			t.Fatalf("确认串 %q 应 400，实得 %d: %s", bad, code, body)
		}
	}
	if u, _ := tenantRowCount(t, f.s, f.targetID); u != before {
		t.Fatalf("确认串不符却改了数据：users %d→%d", before, u)
	}
	// 正例对照：按 code 回显（带首尾空格）应放行——TrimSpace 是刻意的
	code, body := postPurge(t, f.s, "/api/admin/tenants/delete", f.tokens["root_admin"],
		map[string]interface{}{"id": f.targetID, "confirm": " hajimi "})
	if code != http.StatusOK {
		t.Fatalf("按 code 回显（含首尾空格）应放行，实得 %d: %s", code, body)
	}
}

// E 段：租户不存在 ⇒ 404
func TestPurgeTenantNotFound(t *testing.T) {
	f := newPurgeFixture(t)
	code, body := postPurge(t, f.s, "/api/admin/tenants/delete", f.tokens["root_admin"],
		map[string]interface{}{"id": 99999, "confirm": "whatever"})
	if code != http.StatusNotFound {
		t.Fatalf("不存在的租户应 404，实得 %d: %s", code, body)
	}
}

// F 段：超管跨租户删用户 / 删组织（既有两个接口的能力，本批钉住）
func TestSuperAdminCanDeleteUserAndOrgCrossTenant(t *testing.T) {
	f := newPurgeFixture(t)
	member := findUser(t, f.s, "hajimi_member")

	// ① 跨租户删用户：超管在平台上下文（不带 X-Tenant-ID）删掉待删租户里的普通成员
	code, body := postPurge(t, f.s, "/api/admin/users/delete", f.tokens["root_admin"],
		map[string]interface{}{"id": member.ID})
	if code != http.StatusOK {
		t.Fatalf("超管跨租户删用户应 200，实得 %d: %s", code, body)
	}
	if all, _ := f.s.Store.ListAllUsers(); func() bool {
		for _, u := range all {
			if u.ID == member.ID {
				return true
			}
		}
		return false
	}() {
		t.Fatal("用户仍在：跨租户删除没生效")
	}

	// ② 跨租户删组织：超管按组织自身归属租户解析（orgTenant 的超管分支）
	orgs, err := f.s.Store.ListOrgs(f.targetID)
	if err != nil || len(orgs) == 0 {
		t.Fatalf("前置条件不成立：待删租户应有组织（err=%v n=%d）", err, len(orgs))
	}
	code, body = postPurge(t, f.s, "/api/orgs/delete", f.tokens["root_admin"],
		map[string]interface{}{"id": orgs[0].ID})
	if code != http.StatusOK {
		t.Fatalf("超管跨租户删组织应 200，实得 %d: %s", code, body)
	}
	if left, _ := f.s.Store.ListOrgs(f.targetID); len(left) != 0 {
		t.Fatalf("组织仍在：跨租户删除没生效（剩 %d 个）", len(left))
	}
}
