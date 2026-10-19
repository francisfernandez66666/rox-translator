// ============ tenant_branding_authz_test.go · 职责说明 ============
// G16（2026-10-19）：租户品牌写入口 `handleTenantBrandingSet` 的授权必须用**来源锚点**
// `auth.IsSuperAdmin`（= 等级≥4 **且** tenant_id==0），而不是等级 `auth.RoleLevel(u.Role) >= 4`。
//
// 缺陷形态：`role='admin'`（等级 4，历史角色）且 `tenant_id>0` 的存量账号，入闸 `requireTenantAdmin`
// 只要求等级≥3，随后三处 `RoleLevel>=4` 判定全部跳过 ⇒ 可传任意 `id` 写他租户/平台品牌（越权）。
//
// 判据：喂 `role='admin', tenant_id>0` 夹具，写**他租户**品牌必须 403；写**本租户**必须 200（对照腿，
// 证明 403 不是"什么都拒"）；并断言他租户品牌值一字未动（403 不许产生副作用）。
//
// 运行：env DB_DRIVER=sqlite go test -count=1 ./internal/api/ -run TestTenantBrandingAuthz
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
	"translator/internal/iam"
	"translator/internal/store"
	"translator/internal/tenant"
)

// TestTenantBrandingAuthzRejectsCrossTenantByLevelOnly G16 主判据：等级4但非平台账号不许写他租品牌。
func TestTenantBrandingAuthzRejectsCrossTenantByLevelOnly(t *testing.T) {
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
	a, err := ts.Create("g16a", "G16 甲租", "", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ts.Create("g16b", "G16 乙租", "", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	// 关键夹具：role='admin'（等级 4）但 tenant_id=a.ID（>0）——这正是历史存量形态。
	u, err := st.CreateUser(a.ID, "g16-admin", auth.PasswordHash("pw123456"), "甲租历史管理员", iam.RoleAdmin, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if auth.RoleLevel(u.Role) < 4 || u.TenantID == 0 {
		t.Fatalf("夹具没造对：需要 RoleLevel>=4 且 TenantID>0，实际 role=%q tid=%d", u.Role, u.TenantID)
	}
	tok, err := auth.Sign(u, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{Store: st, Ten: ts, Cfg: config.C}

	call := func(tid int64, name string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]interface{}{"id": tid, "brand_name": name})
		req := httptest.NewRequest("POST", "/api/tenant/branding", strings.NewReader(string(body)))
		req.Header.Set("Authorization", "Bearer "+tok)
		rec := httptest.NewRecorder()
		s.handleTenantBrandingSet(rec, req)
		return rec
	}

	// ① 写他租户品牌 ⇒ 必须 403
	rec := call(b.ID, "越权改名")
	if rec.Code != 403 {
		t.Fatalf("等级4+他租账号写他租户品牌应 403，实际 %d：%s", rec.Code, rec.Body.String())
	}
	bt, _ := ts.GetByID(b.ID)
	if bt != nil && bt.BrandName == "越权改名" {
		t.Fatalf("403 仍产生了副作用：乙租品牌被改成 %q", bt.BrandName)
	}

	// ② 对照腿：写**本租户**品牌 ⇒ 200 且真落库（证明上一腿的 403 不是"全拒"）
	rec = call(a.ID, "甲租本名")
	if rec.Code != 200 {
		t.Fatalf("写本租户品牌应 200，实际 %d：%s", rec.Code, rec.Body.String())
	}
	at, _ := ts.GetByID(a.ID)
	if at == nil || at.BrandName != "甲租本名" {
		t.Fatalf("本租户品牌未落库：%+v", at)
	}
}
