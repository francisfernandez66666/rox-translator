// ============ 本文件职责中文说明 ============
// 问题1 回归测试：前台顶栏「当前租户名」展示所需的后端数据契约。
// 修复点：handleTenantInviteEnabledGet 响应补 tenant_name / is_personal 字段，
// 前端据此在工作台顶栏叠加显示用户所属租户名（此前仅显示平台品牌，用户误以为
// 注册后落入平台根「翻译平台」）。
//
// 覆盖：
//
//	A) 企业租户 admin 读开关 → 返回租户名（如「汽车公司」）
//	B) 个人租户 admin 读开关 → 返回租户名（如「小明」）且 is_personal=true
//	C) 超管切换生效租户 → 返回被切换租户的租户名
//
// 使用内存 SQLite 构造 Store + tenant.Store，签发 JWT 走 handler 全链路。
// =============================================
package api

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"translator/internal/auth"
	"translator/internal/store"
	"translator/internal/tenant"

	_ "modernc.org/sqlite"
)

// newTenantNameTestServer 构造问题1回归测试的 Server：
// 内存 SQLite Store + tenant.Store，含 企业租户（carco）/个人租户（xiaoming），
// 各一名 tenant_admin。返回 Server、token 表、租户 ID 表。
func newTenantNameTestServer(t *testing.T) (*Server, map[string]string, map[string]int64) {
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
	if err := st.EnsureAdmin(0, "admin", "hash-admin", "平台超管", ""); err != nil {
		t.Fatalf("创建超管失败: %v", err)
	}
	// 企业租户（非个人）+ 个人租户
	car, err := ten.Create("t_car", "汽车公司", "", "{}")
	if err != nil {
		t.Fatalf("创建企业租户失败: %v", err)
	}
	if _, err := ten.Create("t_person", "小明", "", "{}"); err != nil {
		t.Fatalf("创建个人租户失败: %v", err)
	}
	// 标记个人租户 is_personal=1
	if err := ten.SetPersonal(car.ID, false); err != nil {
		t.Fatalf("标记企业租户非个人失败: %v", err)
	}
	person, err := ten.GetByCode("t_person")
	if err != nil {
		t.Fatalf("查询个人租户失败: %v", err)
	}
	if err := ten.SetPersonal(person.ID, true); err != nil {
		t.Fatalf("标记个人租户 is_personal 失败: %v", err)
	}

	s := &Server{Store: st, Ten: ten}

	// 各租户一名 tenant_admin + 平台超管
	mkUser := func(tid int64, username, display string) {
		if _, err := st.CreateUser(tid, username, "hash-"+username, display, "tenant_admin", 1, 0); err != nil {
			t.Fatalf("创建用户 %s 失败: %v", username, err)
		}
	}
	mkUser(car.ID, "car_admin", "汽车公司管理员")
	mkUser(person.ID, "person_admin", "小明")

	users, _ := st.ListAllUsers()
	tokens := map[string]string{}
	for _, u := range users {
		tk, err := auth.Sign(u, time.Hour)
		if err != nil {
			t.Fatalf("签发 %s 的 JWT 失败: %v", u.Username, err)
		}
		tokens[u.Username] = tk
	}
	return s, tokens, map[string]int64{"car": car.ID, "person": person.ID}
}

// getInviteEnabled 便捷：GET /api/tenant/invite-enabled，返回响应体 bytes。
func getInviteEnabled(t *testing.T, s *Server, token string, ctxTenant int64) []byte {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/tenant/invite-enabled", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	if ctxTenant > 0 {
		req = req.WithContext(tenant.WithTenant(req.Context(), ctxTenant))
	}
	rec := httptest.NewRecorder()
	s.muxForAdmin().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("invite-enabled 期望 200，实得 %d: %s", rec.Code, rec.Body.String())
	}
	return rec.Body.Bytes()
}

// TestTenantInviteEnabledReturnsTenantName 企业租户 admin → 返回其租户名。
func TestTenantInviteEnabledReturnsTenantName(t *testing.T) {
	s, tokens, _ := newTenantNameTestServer(t)
	body := getInviteEnabled(t, s, tokens["car_admin"], 0)
	if !containsStr(string(body), "汽车公司") {
		t.Fatalf("企业租户 admin 的 invite-enabled 应含租户名「汽车公司」，实得: %s", string(body))
	}
	if containsStr(string(body), `"is_personal":true`) {
		t.Fatalf("企业租户 is_personal 应为 false，实得: %s", string(body))
	}
}

// TestTenantInviteEnabledPersonalName 个人租户 admin → 返回租户名且 is_personal=true。
func TestTenantInviteEnabledPersonalName(t *testing.T) {
	s, tokens, _ := newTenantNameTestServer(t)
	body := getInviteEnabled(t, s, tokens["person_admin"], 0)
	if !containsStr(string(body), "小明") {
		t.Fatalf("个人租户 admin 的 invite-enabled 应含租户名「小明」，实得: %s", string(body))
	}
	if !containsStr(string(body), `"is_personal":true`) {
		t.Fatalf("个人租户 is_personal 应为 true，实得: %s", string(body))
	}
}

// TestTenantInviteEnabledSuperAdminSwitched 超管切到企业租户 → 返回该租户名。
func TestTenantInviteEnabledSuperAdminSwitched(t *testing.T) {
	s, tokens, ids := newTenantNameTestServer(t)
	// 超管切到「汽车公司」（X-Tenant-ID → tenant context）
	body := getInviteEnabled(t, s, tokens["admin"], ids["car"])
	if !containsStr(string(body), "汽车公司") {
		t.Fatalf("超管切到企业租户应返回「汽车公司」，实得: %s", string(body))
	}
}

// containsStr 简单子串判断（避免额外依赖）。
func containsStr(s, sub string) bool {
	return len(sub) > 0 && len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
