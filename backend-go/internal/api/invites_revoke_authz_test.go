// ============ invites_revoke_authz_test.go · 职责说明 ============
// G9（2026-10-19）：邀请码撤销接口 `handleInviteCodeRevoke` 的租户隔离与状态语义。
//
// 判据（四腿 + 反证性对照）：
//
//	① 企业管理员撤销**他租户**的码 ⇒ 404（不泄露存在性），且该码未被改动；
//	② 企业管理员撤销**本租户**的码 ⇒ 200 且落库 revoked=1（对照腿，证明 404 不是"全拒"）；
//	③ 平台超管可跨租户撤销 ⇒ 200；
//	④ 已使用的码 ⇒ 409（人已进来，不可撤销）。
//
// 运行：env DB_DRIVER=sqlite go test -count=1 ./internal/api/ -run TestInviteCodeRevokeAuthz
// =============================================
package api

import (
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"translator/internal/auth"
	"translator/internal/config"
	"translator/internal/store"
	"translator/internal/tenant"
)

// TestInviteCodeRevokeAuthz G9 主判据：跨租户拒绝、本租户放行、超管跨租户、已用码 409。
func TestInviteCodeRevokeAuthz(t *testing.T) {
	pinSqliteDialect(t)
	sqlDB, err := sql.Open("sqlite", ":memory:")
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
	a, err := ts.Create("g9a", "G9 甲租", "", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ts.Create("g9b", "G9 乙租", "", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := st.CreateUser(a.ID, "g9-admin", auth.PasswordHash("pw123456"), "甲租管理员", store.RoleTenantAdmin, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	root, err := st.CreateUser(0, "g9-root", auth.PasswordHash("pw123456"), "平台超管", store.RoleSuperAdmin, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !auth.IsSuperAdmin(root) {
		t.Fatalf("夹具没造对：平台超管需 IsSuperAdmin=true")
	}
	adminTok, _ := auth.Sign(admin, time.Hour)
	rootTok, _ := auth.Sign(root, time.Hour)

	ca, _ := st.CreateInviteCode("G9-A-CODE", a.ID)
	cb, _ := st.CreateInviteCode("G9-B-CODE", b.ID)
	usedCode, _ := st.CreateInviteCode("G9-USED-CODE", a.ID)
	if _, err := st.MarkInviteCodeUsed(usedCode.ID, "someone"); err != nil {
		t.Fatal(err)
	}

	s := &Server{Store: st, Ten: ts, Cfg: config.C}
	call := func(tok string, id int64) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]interface{}{"id": id})
		req := httptest.NewRequest("POST", "/api/admin/invite-codes/revoke", strings.NewReader(string(body)))
		req.Header.Set("Authorization", "Bearer "+tok)
		rec := httptest.NewRecorder()
		s.handleInviteCodeRevoke(rec, req)
		return rec
	}

	// ① 企业管理员撤销他租户的码 ⇒ 404 且无副作用
	rec := call(adminTok, cb.ID)
	if rec.Code != 404 {
		t.Fatalf("跨租户撤销应 404，实际 %d：%s", rec.Code, rec.Body.String())
	}
	if got, _ := st.GetInviteCode(cb.ID); got == nil || got.Revoked != 0 {
		t.Fatalf("跨租户 404 仍改动了乙租的码：%+v", got)
	}

	// ② 对照腿：撤销本租户的码 ⇒ 200 且落库
	rec = call(adminTok, ca.ID)
	if rec.Code != 200 {
		t.Fatalf("撤销本租户码应 200，实际 %d：%s", rec.Code, rec.Body.String())
	}
	if got, _ := st.GetInviteCode(ca.ID); got == nil || got.Revoked != 1 || got.Status != "revoked" {
		t.Fatalf("本租户码未落撤销态：%+v", got)
	}

	// ③ 平台超管跨租户撤销 ⇒ 200
	rec = call(rootTok, cb.ID)
	if rec.Code != 200 {
		t.Fatalf("超管跨租户撤销应 200，实际 %d：%s", rec.Code, rec.Body.String())
	}
	if got, _ := st.GetInviteCode(cb.ID); got == nil || got.Revoked != 1 {
		t.Fatalf("超管撤销未落库：%+v", got)
	}

	// ④ 已使用的码 ⇒ 409
	rec = call(adminTok, usedCode.ID)
	if rec.Code != 409 {
		t.Fatalf("撤销已用码应 409，实际 %d：%s", rec.Code, rec.Body.String())
	}

	// ⑤ 不存在 id ⇒ 404
	rec = call(adminTok, 999999)
	if rec.Code != 404 {
		t.Fatalf("不存在 id 应 404，实际 %d：%s", rec.Code, rec.Body.String())
	}
}
