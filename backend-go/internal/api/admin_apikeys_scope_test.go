// ============================================================================
// admin_apikeys_scope_test.go — 「列表与逐条操作同一把尺子」（★ 2026-10-16 现网缺陷回归锁）
//
// 现网实证（用户报「API Key 后台删不掉」）：超管在**平台上下文**（不带 X-Tenant-ID ⇒ effTenant=0）
// 打 /api/apikeys 拿到的是**全量** Key（ListAPIKeys 的 tid<=0 口径），而逐条操作硬编码
// `AND tenant_id=?` ⇒ `WHERE tenant_id=0` **恒 0 行影响且不报错**，接口照样回 `{"success":true}`，
// 前端刷新后那行还在（现场读数：14:31:37 与 14:31:53 两次 delete 均 200、列表字节数 1617 一字不变、
// 库里 id=56「临时」原封不动）。
//
// 本文件锁四层，缺任一层这族缺陷都能复发：
//
//	A. 超管平台视角的删／停／限额／轮换**真落到那把 Key 所在的那一行**（不是回个 success 就算）；
//	B. 轮换出来的新 Key **继承被轮换 Key 的租户**（否则库里会长出一把 tenant_id=0 的孤儿：
//	   聚合列表里看得见，而租户面与任务归属校验都查不到）；
//	C. 租户管理员**仍然动不了**别租户的 Key（404 且那一行一字未动）——反向腿：
//	   防"修好跨租户"顺手修成"取消租户隔离"（那是把一条静默失败换成越权，更坏）；
//	D. 不存在的 id ⇒ 404，不许再把「没改到任何东西」回成 success（这条正是 A 之前被掩盖的形态）。
//
// 运行：go test ./internal/api/ -run TestAPIKeyScope
// ============================================================================
package api

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"translator/internal/auth"
	"translator/internal/config"
	"translator/internal/store"
	"translator/internal/tenant"

	_ "modernc.org/sqlite"
)

// akScopeFixture 内存 SQLite ＋ 一名平台超管（tenant 0）与一名租户 1 管理员，
// 各自一把 Key：超管那把属 tenant 5、租户管理员那把属 tenant 1（复现"看得见别人的、动不了"的现场）。
type akScopeFixture struct {
	s          *Server
	st         *store.Store
	superTok   string // 平台超管 JWT
	tenantTok  string // 租户 1 管理员 JWT
	foreignKey *store.APIKey
	ownKey     *store.APIKey
}

func newAKScopeFixture(t *testing.T) *akScopeFixture {
	t.Helper()
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })

	dbh, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("开内存库失败: %v", err)
	}
	t.Cleanup(func() { _ = dbh.Close() })
	st, err := store.New(dbh)
	if err != nil {
		t.Fatalf("store.New 失败: %v", err)
	}
	sa, err := st.CreateUser(0, "sa_admin", "h", "平台超管", "admin", 0, 0)
	if err != nil {
		t.Fatalf("建超管失败: %v", err)
	}
	ta, err := st.CreateUser(1, "t1_admin", "h", "租户管理员", "tenant_admin", 0, 0)
	if err != nil {
		t.Fatalf("建租户管理员失败: %v", err)
	}
	if _, err := st.CreateAPIKey(5, 0, "他租户的 Key", "translate", 0); err != nil {
		t.Fatalf("建他租户 Key 失败: %v", err)
	}
	if _, err := st.CreateAPIKey(1, ta.ID, "本租户的 Key", "translate", 0); err != nil {
		t.Fatalf("建本租户 Key 失败: %v", err)
	}
	keys, err := st.ListAPIKeys(0)
	if err != nil || len(keys) != 2 {
		t.Fatalf("列 Key 失败: err=%v n=%d", err, len(keys))
	}
	f := &akScopeFixture{s: &Server{Store: st}, st: st}
	for _, k := range keys {
		if k.TenantID == 5 {
			f.foreignKey = k
		} else {
			f.ownKey = k
		}
	}
	if f.superTok, err = auth.Sign(sa, time.Hour); err != nil {
		t.Fatalf("签超管 JWT 失败: %v", err)
	}
	if f.tenantTok, err = auth.Sign(ta, time.Hour); err != nil {
		t.Fatalf("签租户管理员 JWT 失败: %v", err)
	}
	return f
}

// akPost 构造一次 POST：tok=登录态，tenantCtx=注入的租户上下文（0＝平台上下文，非 0＝该租户视角）。
func akPost(tok string, tenantCtx int64, path, body string) *http.Request {
	r := httptest.NewRequest("POST", path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+tok)
	return r.WithContext(tenant.WithTenant(r.Context(), tenantCtx))
}

// TestAPIKeyScopeSuperAdminPlatformCanDelete 超管平台视角删除他租户的 Key：必须真删掉那一行。
func TestAPIKeyScopeSuperAdminPlatformCanDelete(t *testing.T) {
	f := newAKScopeFixture(t)
	w := httptest.NewRecorder()
	f.s.handleAPIKeyDelete(w, akPost(f.superTok, 0, "/api/apikeys/delete",
		`{"id":`+strconv.FormatInt(f.foreignKey.ID, 10)+`}`))
	if w.Code != 200 {
		t.Fatalf("超管平台视角删除应回 200，实得 %d body=%s", w.Code, w.Body.String())
	}
	if _, err := f.st.GetAPIKey(f.foreignKey.ID, 5); err == nil {
		t.Fatal("接口回了成功，但 tenant 5 那把 Key 还在库里 ⇒ 又回到「0 行影响＋假装删过了」的形态")
	}
	// 反向腿：本租户那把不许被顺手删掉（按 id 精确，不是"删一片"）。
	if _, err := f.st.GetAPIKey(f.ownKey.ID, 1); err != nil {
		t.Fatalf("另一租户那把 Key 被误删: %v", err)
	}
}

// TestAPIKeyScopeSuperAdminPlatformCanStatusAndLimit 超管平台视角的启停与限额同样要落库。
func TestAPIKeyScopeSuperAdminPlatformCanStatusAndLimit(t *testing.T) {
	f := newAKScopeFixture(t)
	id := strconv.FormatInt(f.foreignKey.ID, 10)
	w := httptest.NewRecorder()
	f.s.handleAPIKeyStatus(w, akPost(f.superTok, 0, "/api/apikeys/status", `{"id":`+id+`,"status":"disabled"}`))
	if w.Code != 200 {
		t.Fatalf("超管停用应回 200，实得 %d body=%s", w.Code, w.Body.String())
	}
	got, err := f.st.GetAPIKey(f.foreignKey.ID, 5)
	if err != nil {
		t.Fatalf("回读失败: %v", err)
	}
	if got.Status != "disabled" {
		t.Fatalf("回了成功但库里 status=%q（期望 disabled）⇒ 启停没落库", got.Status)
	}
	w = httptest.NewRecorder()
	f.s.handleAPIKeyLimit(w, akPost(f.superTok, 0, "/api/apikeys/limit", `{"id":`+id+`,"daily_call_limit":7}`))
	if w.Code != 200 {
		t.Fatalf("超管限应回 200，实得 %d body=%s", w.Code, w.Body.String())
	}
	got, err = f.st.GetAPIKey(f.foreignKey.ID, 5)
	if err != nil {
		t.Fatalf("回读失败: %v", err)
	}
	if got.DailyCallLimit != 7 {
		t.Fatalf("回了成功但库里 daily_call_limit=%d（期望 7）⇒ 限额没落库", got.DailyCallLimit)
	}
}

// TestAPIKeyScopeRotateKeepsKeyTenant 超管平台视角轮换他租户的 Key：新 Key 必须仍属那个租户。
func TestAPIKeyScopeRotateKeepsKeyTenant(t *testing.T) {
	f := newAKScopeFixture(t)
	w := httptest.NewRecorder()
	f.s.handleAPIKeyRotate(w, akPost(f.superTok, 0, "/api/apikeys/rotate",
		`{"id":`+strconv.FormatInt(f.foreignKey.ID, 10)+`}`))
	if w.Code != 200 {
		t.Fatalf("超管轮换应回 200，实得 %d body=%s", w.Code, w.Body.String())
	}
	if _, err := f.st.GetAPIKey(f.foreignKey.ID, 5); err == nil {
		t.Fatal("旧 Key 未被吊销（轮换语义要求旧 Key 立即失效）")
	}
	keys, err := f.st.ListAPIKeys(5)
	if err != nil || len(keys) != 1 {
		t.Fatalf("tenant 5 名下应有且只有 1 把新 Key: err=%v n=%d", err, len(keys))
	}
	for _, k := range keys {
		if k.Name != "他租户的 Key" || k.TenantID != 5 {
			t.Fatalf("新 Key 未继承原租户/名称: name=%q tid=%d", k.Name, k.TenantID)
		}
	}
	orphans, err := f.st.ListAPIKeys(0)
	if err != nil {
		t.Fatalf("列全量失败: %v", err)
	}
	for _, k := range orphans {
		if k.TenantID == 0 {
			t.Fatal("轮换把新 Key 落成了 tenant_id=0 的孤儿（平台上下文直接用了 tid=0）")
		}
	}
}

// TestAPIKeyScopeTenantAdminStillIsolated 租户管理员动别租户的 Key：404 且那一行一字未动。
func TestAPIKeyScopeTenantAdminStillIsolated(t *testing.T) {
	f := newAKScopeFixture(t)
	id := strconv.FormatInt(f.foreignKey.ID, 10)
	cases := []struct {
		name string
		call func(w http.ResponseWriter, r *http.Request)
		body string
	}{
		{"delete", f.s.handleAPIKeyDelete, `{"id":` + id + `}`},
		{"status", f.s.handleAPIKeyStatus, `{"id":` + id + `,"status":"disabled"}`},
		{"limit", f.s.handleAPIKeyLimit, `{"id":` + id + `,"daily_call_limit":9}`},
		{"rotate", f.s.handleAPIKeyRotate, `{"id":` + id + `}`},
		{"reveal", f.s.handleAPIKeyReveal, `{"id":` + id + `}`},
	}
	for _, c := range cases {
		w := httptest.NewRecorder()
		c.call(w, akPost(f.tenantTok, 0, "/api/apikeys/"+c.name, c.body))
		if w.Code != 404 {
			t.Fatalf("租户管理员越权 %s 应回 404，实得 %d body=%s", c.name, w.Code, w.Body.String())
		}
	}
	got, err := f.st.GetAPIKey(f.foreignKey.ID, 5)
	if err != nil {
		t.Fatalf("他租户那把 Key 被越权改/删了: %v", err)
	}
	if got.Status != "active" || got.DailyCallLimit != 0 {
		t.Fatalf("他租户那把 Key 被越权改了: status=%q limit=%d", got.Status, got.DailyCallLimit)
	}
}

// TestAPIKeyScopeMissingIDIsNotFound 不存在的 id：删/停/限额一律 404，不许再回 success。
func TestAPIKeyScopeMissingIDIsNotFound(t *testing.T) {
	f := newAKScopeFixture(t)
	for _, c := range []struct {
		name string
		call func(w http.ResponseWriter, r *http.Request)
		body string
	}{
		{"delete", f.s.handleAPIKeyDelete, `{"id":999999}`},
		{"status", f.s.handleAPIKeyStatus, `{"id":999999,"status":"disabled"}`},
		{"limit", f.s.handleAPIKeyLimit, `{"id":999999,"daily_call_limit":1}`},
	} {
		w := httptest.NewRecorder()
		c.call(w, akPost(f.superTok, 0, "/api/apikeys/"+c.name, c.body))
		if w.Code != 404 {
			t.Fatalf("%s 不存在的 id 应回 404，实得 %d body=%s（「没改到任何东西」不许回成 success）",
				c.name, w.Code, w.Body.String())
		}
	}
}
