// ============================================================================
// ★ H10 SCIM 2.0 端到端处理层测试：令牌鉴权、用户幂等建/改、PATCH 停用、
//
//	Group↔Org 同步与成员全量替换。
//
// ============================================================================
package api

import (
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"translator/internal/store"
)

func h10Server(t *testing.T) (*Server, *store.Store) {
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
	return &Server{Store: st}, st
}

func h10Cfg(t *testing.T, st *store.Store, enabled bool) *store.SCIMConfig {
	t.Helper()
	cfg := &store.SCIMConfig{TenantID: 7, Token: "tok-" + strings.Repeat("a", 40)[:30], Enabled: enabled}
	if err := st.UpsertSCIMConfig(cfg); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func h10Do(s *Server, method, path, token, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	s.handleSCIMUsers(w, r)
	return w
}

func TestH10SCIMUsersLifecycle(t *testing.T) {
	s, st := h10Server(t)
	cfg := h10Cfg(t, st, true)

	// 无令牌 → 401
	if w := h10Do(s, "GET", "/api/scim/v2/Users", "", ""); w.Code != 401 {
		t.Fatalf("无令牌应 401: %d", w.Code)
	}
	// 停用 → 403
	off := *cfg
	off.Enabled = false
	_ = st.UpsertSCIMConfig(&off)
	if w := h10Do(s, "GET", "/api/scim/v2/Users", cfg.Token, ""); w.Code != 403 {
		t.Fatalf("停用应 403: %d", w.Code)
	}
	_ = st.UpsertSCIMConfig(cfg)

	// 创建
	body := `{"userName":"zhang.san","displayName":"张三","externalId":"idp-001","emails":[{"value":"zs@corp.com"}],"active":true}`
	w := h10Do(s, "POST", "/api/scim/v2/Users", cfg.Token, body)
	if w.Code != 201 {
		t.Fatalf("创建应 201: %d %s", w.Code, w.Body.String())
	}
	var su scimUser
	_ = json.Unmarshal(w.Body.Bytes(), &su)
	uid := su.ID
	if uid == "" || su.UserName != "zhang.san" {
		t.Fatalf("响应错误: %s", w.Body.String())
	}
	// externalId 幂等：同 ext 再 POST → 更新不重建
	w2 := h10Do(s, "POST", "/api/scim/v2/Users", cfg.Token, strings.Replace(body, "张三", "张三丰", 1))
	if w2.Code != 200 {
		t.Fatalf("幂等更新应 200: %d", w2.Code)
	}
	var all []*store.User
	all, _ = st.ListUsers(7)
	if len(all) != 1 {
		t.Fatalf("用户数应 1: %d", len(all))
	}
	if all[0].DisplayName != "张三丰" {
		t.Fatalf("显示名未更新: %s", all[0].DisplayName)
	}
	// filter 列表
	wf := h10Do(s, "GET", `/api/scim/v2/Users?filter=userName%20eq%20%22nobody%22`, cfg.Token, "")
	var lr struct {
		TotalResults int `json:"totalResults"`
	}
	_ = json.Unmarshal(wf.Body.Bytes(), &lr)
	if lr.TotalResults != 0 {
		t.Fatalf("filter 未生效: %s", wf.Body.String())
	}
	// PATCH 停用
	idPath := "/api/scim/v2/Users/" + uid
	wp := h10Do(s, "PATCH", idPath, cfg.Token, `{"Operations":[{"op":"replace","path":"active","value":false}]}`)
	var after scimUser
	_ = json.Unmarshal(wp.Body.Bytes(), &after)
	if after.Active == nil || *after.Active {
		t.Fatalf("PATCH active=false 未生效: %s", wp.Body.String())
	}
	// DELETE → 软删（disabled）
	wd := h10Do(s, "PATCH", idPath, cfg.Token, `{"Operations":[{"op":"replace","path":"active","value":true}]}`)
	_ = wd
	wdel := httptest.NewRecorder()
	rdel := httptest.NewRequest("DELETE", idPath, nil)
	rdel.Header.Set("Authorization", "Bearer "+cfg.Token)
	s.handleSCIMUsers(wdel, rdel)
	if wdel.Code != 204 {
		t.Fatalf("DELETE 应 204: %d", wdel.Code)
	}
	got, _ := st.GetUser(mustI64(uid), 7)
	if got == nil || got.Status != "disabled" {
		t.Fatalf("软删未生效: %+v", got)
	}
}

func mustI64(s string) int64 {
	n := int64(0)
	for _, c := range s {
		n = n*10 + int64(c-'0')
	}
	return n
}

func TestH10SCIMGroupsSync(t *testing.T) {
	s, st := h10Server(t)
	cfg := h10Cfg(t, st, true)
	u1, err := st.CreateUser(7, "g.a", "x", "A", "user", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	u2, _ := st.CreateUser(7, "g.b", "x", "B", "user", 0, 0)

	// 建组
	w := h10Do2(s, "POST", "/api/scim/v2/Groups", cfg.Token, `{"displayName":"研发中心","members":[]}`)
	if w.Code != 201 {
		t.Fatalf("建组应 201: %d %s", w.Code, w.Body.String())
	}
	var g scimGroup
	_ = json.Unmarshal(w.Body.Bytes(), &g)
	gid := g.ID
	// 同名幂等
	w2 := h10Do2(s, "POST", "/api/scim/v2/Groups", cfg.Token, `{"displayName":"研发中心"}`)
	if w2.Code != 200 {
		t.Fatalf("同名组应 200 幂等: %d", w2.Code)
	}
	// 成员全量替换
	putB, _ := json.Marshal(map[string]interface{}{
		"displayName": "研发中心",
		"members":     []map[string]string{{"value": strconv.FormatInt(u1.ID, 10)}, {"value": strconv.FormatInt(u2.ID, 10)}},
	})
	wm := h10Do2(s, "PUT", "/api/scim/v2/Groups/"+gid, cfg.Token, string(putB))
	if wm.Code != 200 {
		t.Fatalf("PUT members 失败: %d %s", wm.Code, wm.Body.String())
	}
	if orgs := mustI64(gid); orgs > 0 {
		m := st.UserIDsByOrg(7, orgs)
		if len(m) != 2 {
			t.Fatalf("成员应 2: %v", m)
		}
	}
	// 移除一个成员后再同步
	putB2, _ := json.Marshal(map[string]interface{}{
		"members": []map[string]string{{"value": strconv.FormatInt(u1.ID, 10)}},
	})
	_ = h10Do2(s, "PUT", "/api/scim/v2/Groups/"+gid, cfg.Token, string(putB2))
	if len(st.UserIDsByOrg(7, mustI64(gid))) != 1 {
		t.Fatal("成员移除未生效")
	}
}

func h10Do2(s *Server, method, path, token, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	s.handleSCIMGroups(w, r)
	return w
}
