// ============ auth_deptadmin_org_gate_test.go · 职责说明 ============
// 2026-09-25 发布前 UAT 修复批 F 的 F-31 + F-35 回归断言（auth.go 成员管理双闸 + 合并语义）。
//
// F-31（dept_admin 必绑部门，双闸）：
//
//	闸1 create：role=dept_admin 且 org_id<=0 → 400——旧实现放行后造出「未绑定部门的死角色」，
//	  当事人随后所有成员操作都被「部门管理员未绑定部门」守卫反锁；
//	闸2 update：按**合并后终态**判（finalRole 等级 2 且 finalOrg<=0 → 拒绝），
//	  只查请求字段会漏「给存量无部门账号升角成 dept_admin（org 缺席=沿用 0）」与
//	  「改角色的同时把部门摘成 0」两条降级路径。
//
// F-35（users/update 空字段静默清空）：
//
//	display_name/role/status 旧为非指针 string，缺席=空串直写整行覆盖——
//	只改角色会洗掉姓名、status 缺席被默认值翻回 active（停用账号被悄悄复活）。
//	现指针入参 + 缺席取目标现值合并；审计 after 记合并后真实写库值。
//
// 四向锁 + 合并锁：
//
//	A) create 无部门 dept_admin → 400（含「必须绑定部门」文案与结构化 code 字段）；
//	B) create 带部门 dept_admin → 成功且落库 org_id/role 等值；
//	C) update 终态两路降级各 400：①存量 user（org=0）直接升角 dept_admin 且不带 org；
//	   ②dept_admin 在位时把 org_id 摘 0；
//	D) update 正常改名（只传 display_name）→ role/status/org_id 全部原样保留；
//	E) F-35 核心链：先停用（status=disabled）→ 只改角色 → 断 status 仍 disabled、姓名未被洗掉
//	   （旧实现在此必红：status 默认回 active、display_name 被空串清空）。
//
// 方言：自钉 SQLite 内存库并显式钉死 config.C（AGENTS.md §一·4）；
// 命名共享缓存 DSN 保证连接池多连接同库。
// 运行：env DB_DRIVER=sqlite go test -count=1 ./internal/api/ -run TestUATBatchF
// =============================================
package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
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

// newF31Probe 装配 F-31/F-35 探针：内存库 + 租户 + tenant_admin 操作者 JWT + 一个部门组织。
// 返回: Server、租户 ID、操作者 JWT、部门组织 ID。
func newF31Probe(t *testing.T) (*Server, int64, string, int64) {
	t.Helper()
	pinSqliteDialect(t)
	sqlDB, err := sql.Open("sqlite", fmt.Sprintf("file:f31gate_%s?mode=memory&cache=shared", t.Name()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	st, err := store.New(sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	ts, err := tenant.NewStore(sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	tn, err := ts.Create("f31t", "F31探针租", "", "{}")
	if err != nil {
		t.Fatal(err)
	}
	op, err := st.CreateUser(tn.ID, "f31-admin", auth.PasswordHash("pw123456"), "F31探针管理员", iam.RoleTenantAdmin, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := auth.Sign(op, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	org, err := st.CreateOrg(tn.ID, 0, "销售部", "dept")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.UploadDir = t.TempDir()
	return &Server{Store: st, Ten: ts, Cfg: cfg}, tn.ID, tok, org.ID
}

// callUserCreate 直调管理员建号 handler（JSON body），返回响应记录。
func callUserCreate(t *testing.T, s *Server, tok string, body map[string]interface{}) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/admin/users/create", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	s.handleAdminUserCreate(rec, req)
	return rec
}

// callUserUpdate 直调管理员改号 handler（JSON body），返回响应记录。
func callUserUpdate(t *testing.T, s *Server, tok string, body map[string]interface{}) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/admin/users/update", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	s.handleAdminUserUpdate(rec, req)
	return rec
}

// TestUATBatchF_DeptAdminCreateGate 锁 A/B：create 侧「dept_admin 必绑部门」源头闸。
func TestUATBatchF_DeptAdminCreateGate(t *testing.T) {
	s, tid, tok, orgID := newF31Probe(t)

	// A) 无部门 dept_admin → 400 结构化错误（writeError：body 含 code 字段），不落库
	rec := callUserCreate(t, s, tok, map[string]interface{}{
		"username": "dead_da", "password": "pw123456", "role": "dept_admin",
	})
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "必须绑定部门") {
		t.Fatalf("无部门 dept_admin 建号应 400「必须绑定部门」，实得 %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "code") {
		t.Fatalf("新错误分支必须走 writeError 结构化错误体（含 code 字段，AGENTS §一·8）: %s", rec.Body.String())
	}
	if ds, _ := s.Store.GetUserByUsernameGlobal("dead_da"); len(ds) != 0 {
		t.Fatalf("被拒建号不得落库")
	}

	// B) 带部门 dept_admin → 成功，且 role/org_id 等值落库
	ok := callUserCreate(t, s, tok, map[string]interface{}{
		"username": "live_da", "password": "pw123456", "role": "dept_admin", "org_id": orgID,
	})
	if ok.Code != http.StatusOK || !strings.Contains(ok.Body.String(), `"success":true`) {
		t.Fatalf("带部门 dept_admin 建号应成功，实得 %d: %s", ok.Code, ok.Body.String())
	}
	us, e := s.Store.GetUserByUsernameGlobal("live_da")
	if e != nil || len(us) != 1 {
		t.Fatalf("live_da 应恰有一行落库: %v", e)
	}
	if us[0].Role != store.RoleDeptAdmin || us[0].OrgID != orgID || us[0].TenantID != tid {
		t.Fatalf("落库等值锁失败: role=%q org=%d tid=%d", us[0].Role, us[0].OrgID, us[0].TenantID)
	}
}

// TestUATBatchF_DeptAdminUpdateFinalStateGate 锁 C：update 侧终态判据两条降级路径。
func TestUATBatchF_DeptAdminUpdateFinalStateGate(t *testing.T) {
	s, tid, tok, orgID := newF31Probe(t)

	// 造两个存量账号：无部门普通用户 u_plain、有部门 dept_admin u_da
	plain, err := s.Store.CreateUser(tid, "u_plain", auth.PasswordHash("pw123456"), "普通甲", store.RoleUser, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	da, err := s.Store.CreateUser(tid, "u_da", auth.PasswordHash("pw123456"), "部门乙", store.RoleDeptAdmin, 0, orgID)
	if err != nil {
		t.Fatal(err)
	}

	// C①）给 org=0 的存量 user 直接升角 dept_admin 且不带 org（org 缺席=沿用 0）→ 400
	r1 := callUserUpdate(t, s, tok, map[string]interface{}{"id": plain.ID, "role": "dept_admin"})
	if r1.Code != http.StatusBadRequest || !strings.Contains(r1.Body.String(), "必须绑定部门") {
		t.Fatalf("升角不带部门应 400，实得 %d: %s", r1.Code, r1.Body.String())
	}
	if g, e := s.Store.GetUser(plain.ID, tid); e != nil || g.Role != store.RoleUser {
		t.Fatalf("被拒更新不得改库: role=%q err=%v", g.Role, e)
	}

	// C②）dept_admin 在位时把 org_id 摘 0（角色缺席=沿用 dept_admin）→ 400
	r2 := callUserUpdate(t, s, tok, map[string]interface{}{"id": da.ID, "org_id": 0})
	if r2.Code != http.StatusBadRequest || !strings.Contains(r2.Body.String(), "必须绑定部门") {
		t.Fatalf("摘部门留 dept_admin 角色应 400，实得 %d: %s", r2.Code, r2.Body.String())
	}
	if g, e := s.Store.GetUser(da.ID, tid); e != nil || g.OrgID != orgID {
		t.Fatalf("被拒摘部门不得改库: org=%d err=%v", g.OrgID, e)
	}

	// 对照：升角 dept_admin 且同时给部门 → 成功（终态合法）
	r3 := callUserUpdate(t, s, tok, map[string]interface{}{"id": plain.ID, "role": "dept_admin", "org_id": orgID})
	if r3.Code != http.StatusOK || !strings.Contains(r3.Body.String(), `"success":true`) {
		t.Fatalf("带部门升角应成功，实得 %d: %s", r3.Code, r3.Body.String())
	}
	if g, e := s.Store.GetUser(plain.ID, tid); e != nil || g.Role != store.RoleDeptAdmin || g.OrgID != orgID {
		t.Fatalf("合法升角应等值落库: role=%q org=%d err=%v", g.Role, g.OrgID, e)
	}
}

// TestUATBatchF_UserUpdateMergeSemantics 锁 D/E：F-35 缺席字段=不修改。
// D) 只传 display_name → role/status/org_id 原样保留；
// E) 先停用再只改角色 → status 不得被默认值翻回 active、姓名不得被空串洗掉
//
//	（旧实现：非指针空串直写整行覆盖 + status 空默认 active，两坑在此同炸）。
func TestUATBatchF_UserUpdateMergeSemantics(t *testing.T) {
	s, tid, tok, orgID := newF31Probe(t)
	u, err := s.Store.CreateUser(tid, "u_merge", auth.PasswordHash("pw123456"), "合并语义保留我", store.RoleDeptAdmin, 0, orgID)
	if err != nil {
		t.Fatal(err)
	}

	// D) 只改名：其余缺席
	r1 := callUserUpdate(t, s, tok, map[string]interface{}{"id": u.ID, "display_name": "改过的新名"})
	if r1.Code != http.StatusOK || !strings.Contains(r1.Body.String(), `"success":true`) {
		t.Fatalf("只改名应成功，实得 %d: %s", r1.Code, r1.Body.String())
	}
	g1, e1 := s.Store.GetUser(u.ID, tid)
	if e1 != nil {
		t.Fatal(e1)
	}
	if g1.DisplayName != "改过的新名" || g1.Role != store.RoleDeptAdmin || g1.Status != store.UserActive || g1.OrgID != orgID {
		t.Fatalf("缺席字段必须原样保留: display=%q role=%q status=%q org=%d", g1.DisplayName, g1.Role, g1.Status, g1.OrgID)
	}

	// E) 先停用（显式只传 status）
	r2 := callUserUpdate(t, s, tok, map[string]interface{}{"id": u.ID, "status": "disabled"})
	if r2.Code != http.StatusOK {
		t.Fatalf("停用请求应 200，实得 %d: %s", r2.Code, r2.Body.String())
	}
	// 再只降角色为普通用户（不带部门——降角终态合法）：姓名与停用态都不得被动
	r3 := callUserUpdate(t, s, tok, map[string]interface{}{"id": u.ID, "role": "user"})
	if r3.Code != http.StatusOK || !strings.Contains(r3.Body.String(), `"success":true`) {
		t.Fatalf("只降角色应成功，实得 %d: %s", r3.Code, r3.Body.String())
	}
	g3, e3 := s.Store.GetUser(u.ID, tid)
	if e3 != nil {
		t.Fatal(e3)
	}
	if g3.DisplayName != "改过的新名" {
		t.Fatalf("姓名被空串洗掉（F-35 复发）: %q", g3.DisplayName)
	}
	if g3.Status != "disabled" {
		t.Fatalf("停用态被默认值翻回 active（F-35 复发）: %q", g3.Status)
	}
	if g3.Role != store.RoleUser {
		t.Fatalf("显式传入的角色应生效: %q", g3.Role)
	}
}
