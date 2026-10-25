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
//	A) create 无部门 dept_admin → 400（含「必须绑定部门」文案与结构化 code 字段），users 与 invite_codes 都零行；
//	B) create 带部门 dept_admin → 成功发出链接，且 kind/role/org_id/tenant_id/预填名/有效期逐项等值；
//	   ★ R9（2026-10-24 B3-c）企业档不再建 users 行，本条的落库等值对象从 users 换成 invite_codes；
//	B2) create 企业档还带 password → 400「不再录入初始口令」（R9 复现锁：拒绝发生在写库之前，
//	   不许「口令被静默丢掉但链接照发」那种假成功形态）；
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
// ★ R9（2026-10-24 B3-c）企业档换轨：本租户（tid>0）建号已不录口令、不建 users 行，
//
//	而是走 issueInviteLink 发一枚专属链接——F-31 闸 1 的判据一字未动（它就在 issueInviteLink 里），
//	所以这两条锁只是把「落库等值」的对象从 users 行换成 invite_codes 行，并新增一条
//	「不许再录初始口令」的源头锁（R9 的复现锁：谁把口令写回这一档，它当场红）。
func TestUATBatchF_DeptAdminCreateGate(t *testing.T) {
	s, tid, tok, orgID := newF31Probe(t)

	// A) 无部门 dept_admin → 400 结构化错误（writeError：body 含 code 字段），不落库
	rec := callUserCreate(t, s, tok, map[string]interface{}{
		"username": "dead_da", "role": "dept_admin",
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
	// 被拒那一行也不该留下任何邀请凭据（闸 1 卡在写库之前，不是「先发了链接再报错」）
	if ls, e := s.Store.ListInviteLinks(tid); e != nil || len(ls) != 0 {
		t.Fatalf("被拒建号不得落邀请链接: n=%d err=%v", len(ls), e)
	}

	// B) 带部门 dept_admin → 成功发出链接，且 kind/role/org_id/tenant_id 等值落库
	// ★ R9：断言主体从「users 表多一行」翻成「users 表零行 + invite_codes 恰一行」——
	//   这正是「不建号」这一设计纪律（设计纪律 ①）的机械防线。
	ok := callUserCreate(t, s, tok, map[string]interface{}{
		"username": "live_da", "role": "dept_admin", "org_id": orgID,
	})
	if ok.Code != http.StatusOK || !strings.Contains(ok.Body.String(), `"success":true`) {
		t.Fatalf("带部门 dept_admin 建号应成功（发链接），实得 %d: %s", ok.Code, ok.Body.String())
	}
	var out struct {
		Success bool `json:"success"`
		Invite  struct {
			Code     string `json:"code"`
			URL      string `json:"url"`
			TenantID int64  `json:"tenant_id"`
		} `json:"invite"`
	}
	if e := json.Unmarshal(ok.Body.Bytes(), &out); e != nil {
		t.Fatalf("邀请链接响应解码失败: %v (%s)", e, ok.Body.String())
	}
	if !out.Success || out.Invite.Code == "" || !strings.HasPrefix(out.Invite.URL, "http") {
		t.Fatalf("响应必须带出邀请链接（code+url），实得: %s", ok.Body.String())
	}
	if ds, e := s.Store.GetUserByUsernameGlobal("live_da"); e != nil || len(ds) != 0 {
		t.Fatalf("R9 后建号口不得创建 users 行（受邀人注册时才落库）: n=%d err=%v", len(ds), e)
	}
	links, e := s.Store.ListInviteLinks(tid)
	if e != nil || len(links) != 1 {
		t.Fatalf("应恰有一枚链接落库: n=%d err=%v", len(links), e)
	}
	l := links[0]
	if l.Role != store.RoleDeptAdmin || l.OrgID != orgID || l.TenantID != tid || l.Kind != "link" {
		t.Fatalf("链接落库等值锁失败: role=%q org=%d tid=%d kind=%q", l.Role, l.OrgID, l.TenantID, l.Kind)
	}
	if l.BoundUsername != "live_da" || l.ExpiresAt == "" {
		t.Fatalf("链接必须带预填用户名与有效期: bound=%q expires=%q", l.BoundUsername, l.ExpiresAt)
	}

	// B2) ★ R9 源头锁：企业档仍带初始口令 ⇒ 400，且一行凭据都不许多出来
	//    （旧形态在这里会静默忽略入参、把口令丢掉——管理员以为设了密码，实际没设，
	//     那是「假成功」那一族；现在明确拒绝，让调用方改表单而不是让系统装没看见。）
	before := len(mustListLinks(t, s, tid))
	pw := callUserCreate(t, s, tok, map[string]interface{}{
		"username": "pw_row", "password": "pw123456", "role": "user",
	})
	if pw.Code != http.StatusBadRequest || !strings.Contains(pw.Body.String(), "不再录入初始口令") {
		t.Fatalf("企业档带口令应 400（R9），实得 %d: %s", pw.Code, pw.Body.String())
	}
	if !strings.Contains(pw.Body.String(), "code") {
		t.Fatalf("口令拒绝须走 writeError 结构化错误体: %s", pw.Body.String())
	}
	if len(mustListLinks(t, s, tid)) != before {
		t.Fatalf("口令拒绝不得留下链接凭据（拒绝就是拒绝，不是先发了再报错）")
	}
	if ds, _ := s.Store.GetUserByUsernameGlobal("pw_row"); len(ds) != 0 {
		t.Fatalf("口令拒绝不得建号")
	}
}

// mustListLinks 取本租户链接行（失败即 Fatal）：B2 段要做「前后计数等值」，
// 把 err 分支摊开在断言里会让读数淹没在长行里。
func mustListLinks(t *testing.T, s *Server, tid int64) []*store.InviteCode {
	t.Helper()
	ls, err := s.Store.ListInviteLinks(tid)
	if err != nil {
		t.Fatalf("列邀请链接失败: %v", err)
	}
	return ls
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
