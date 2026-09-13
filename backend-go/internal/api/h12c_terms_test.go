// ============================================================================
// ★ H12 术语检索开放接口测试：鉴权 401/403、参数 400、正常命中形状。
// ============================================================================
package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	_ "modernc.org/sqlite"

	"translator/internal/auth"
	"translator/internal/store"
)

func h12cSetup(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.New(db)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	_, e := st.CreateUser(1, "owner", auth.PasswordHash("pw123456"), "归属人", "tenant_admin", 0, 0)
	if e != nil {
		t.Fatal(e)
	}
	if _, e := db.Exec(`INSERT INTO kb_packages (id, tenant_id, code, name, pack_type, role, enabled, org_id, share_cross_dept) VALUES (10,1,'p','企业术语包','tenant','source',1,0,1)`); e != nil {
		t.Fatal(e)
	}
	if _, e := db.Exec(`INSERT INTO kb_entries (tenant_id, package_id, layer, source_lang, source_text, target_lang, target_text) VALUES (1,10,1,'zh','服务器','en','server')`); e != nil {
		t.Fatal(e)
	}
	return &Server{Store: st}, st
}

func h12cGet(t *testing.T, s *Server, key, query string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/openapi/v1/terms?"+query, nil)
	if key != "" {
		r.Header.Set("Authorization", "Bearer "+key)
	}
	w := httptest.NewRecorder()
	s.handleOpenAPITerms(w, r)
	return w
}

func TestH12cTermsEndpoint(t *testing.T) {
	s, st := h12cSetup(t)
	u, _ := st.GetUserByUsername(1, "owner")
	key, e := st.CreateAPIKey(1, u.ID, "k", "translate", 0)
	if e != nil {
		t.Fatal(e)
	}
	badKey, _ := st.CreateAPIKey(1, u.ID, "k2", "billing", 0)

	if w := h12cGet(t, s, "", "q=服务器"); w.Code != 401 {
		t.Fatalf("无 Key 应 401: %d", w.Code)
	}
	if w := h12cGet(t, s, key, ""); w.Code != 400 {
		t.Fatalf("缺 q 应 400: %d", w.Code)
	}
	if w := h12cGet(t, s, key, "q=服务器&lang=xx"); w.Code != 400 {
		t.Fatalf("非法 lang 应 400: %d", w.Code)
	}
	if w := h12cGet(t, s, badKey, "q=服务器"); w.Code != 403 {
		t.Fatalf("billing Key 应 403: %d", w.Code)
	}
	w := h12cGet(t, s, key, "q=服务器")
	if w.Code != 200 {
		t.Fatalf("应 200: %d %s", w.Code, w.Body.String())
	}
	var resp struct {
		Success bool `json:"success"`
		Count   int  `json:"count"`
		Terms   []struct {
			Source   string `json:"source"`
			Target   string `json:"target"`
			Exact    bool   `json:"exact"`
			PackName string `json:"package"`
		} `json:"terms"`
	}
	if e := json.Unmarshal(w.Body.Bytes(), &resp); e != nil || !resp.Success || resp.Count != 1 {
		t.Fatalf("响应形状错误: %v %s", e, w.Body.String())
	}
	if resp.Terms[0].Source != "服务器" || resp.Terms[0].Target != "server" || !resp.Terms[0].Exact {
		t.Fatalf("命中内容错误: %+v", resp.Terms[0])
	}
}
