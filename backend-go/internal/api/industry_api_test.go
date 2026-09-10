// ============ 本文件职责中文说明 ============
// 行业字典管理 API（2026-09-10 超管可创建/维护行业）handler 全链路测试：
//   A) 超管创建行业 → 列表可见 → 编辑名 → 停用/启用 → 删除（成功）
//   B) 非超管（普通企业用户）访问写接口 → 403 拦截（仅超管可管理行业）
//   C) 被租户引用的行业删除 → 400 拒绝（引用保护，提示改用停用）
// 复用 admin_superadmin_scope_test.go 的内存 SQLite + 真实 JWT 基建，
// 走 handler 全链路（鉴权 requireDeptAdmin → 超管判定 → store 落库）。
// ========================================
package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"translator/internal/auth"
	"translator/internal/store"

	_ "modernc.org/sqlite"
)

// newIndustryTestServer 构造行业 API 测试 Server：
// 内存 SQLite Store + 平台超管（tid=0, role=admin）+ 一个企业租户普通用户。
// 测试库 tenants 表缺 industry 列（生产由 tenant.Store 启动补列），此处手动补，
// 供 IndustryReferenced 引用保护用例使用。
func newIndustryTestServer(t *testing.T) (*Server, map[string]string) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("打开内存数据库失败: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	// tenants 表由生产 tenant.Store 启动建表，测试需在建 Store 前手工建（含 industry 列）
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS tenants (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		"code" TEXT UNIQUE NOT NULL,
		"name" TEXT NOT NULL DEFAULT '',
		"status" TEXT NOT NULL DEFAULT 'active',
		"expires_at" TEXT NOT NULL DEFAULT '',
		"permissions" TEXT NOT NULL DEFAULT '{}',
		"is_personal" INTEGER NOT NULL DEFAULT 0,
		"industry" TEXT NOT NULL DEFAULT '',
		"created_at" TEXT,
		"updated_at" TEXT
	)`); err != nil {
		t.Fatalf("建 tenants 表失败: %v", err)
	}
	st, err := store.New(db)
	if err != nil {
		t.Fatalf("创建 Store 失败: %v", err)
	}
	// 一个以 auto 为行业的测试租户（引用保护用例）
	if _, err := db.Exec(`INSERT INTO tenants (id, code, name, status, industry) VALUES (77,'tauto','汽车租户','active','auto')`); err != nil {
		t.Fatalf("插入测试租户失败: %v", err)
	}
	// 平台超管
	if err := st.EnsureAdmin(0, "admin", "hash-admin", "平台超管", ""); err != nil {
		t.Fatalf("创建超管失败: %v", err)
	}
	// 一个企业租户普通用户（无超管权限）
	if _, err := st.CreateUser(1, "u_ent", "hash-ent", "企业用户", "user", 1, 0); err != nil {
		t.Fatalf("创建企业用户失败: %v", err)
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
	return s, tokens
}

// muxForIndustry 构造行业相关 handler 的最小真实路由（与 server.go 生产注册同源）。
func (s *Server) muxForIndustry() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("/api/admin/industries", s.handleIndustries)
	m.HandleFunc("/api/admin/industries/create", s.handleIndustryCreate)
	m.HandleFunc("/api/admin/industries/update", s.handleIndustryUpdate)
	m.HandleFunc("/api/admin/industries/status", s.handleIndustryStatus)
	m.HandleFunc("/api/admin/industries/delete", s.handleIndustryDelete)
	return m
}

// industryPost 带 Bearer 的 JSON POST，统一走行业路由。
func industryPost(t *testing.T, s *Server, path, token string, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.muxForIndustry().ServeHTTP(rec, req)
	return rec
}

// industryGet 带 Bearer 的 GET 列表。
func industryGet(t *testing.T, s *Server, path, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	s.muxForIndustry().ServeHTTP(rec, req)
	return rec
}

// TestIndustryAPIChain 超管行业全链路：创建→列表→改名→停用→启用→删除。
func TestIndustryAPIChain(t *testing.T) {
	s, tokens := newIndustryTestServer(t)

	// 创建行业（code 小写规范化）
	resp := industryPost(t, s, "/api/admin/industries/create", tokens["admin"],
		map[string]interface{}{"code": "MEDIA", "name": "传媒广告"})
	var created struct {
		Success  bool   `json:"success"`
		Message  string `json:"message"`
		Industry *store.KBPackage `json:"industry"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &created); err != nil || !created.Success {
		t.Fatalf("创建行业应成功: %v (%s)", err, resp.Body.String())
	}
	// code 规范化：MEDIA → media
	if created.Industry == nil || created.Industry.Code != "media" {
		t.Fatalf("行业 code 应规范化为小写 media: %+v", created.Industry)
	}
	id := created.Industry.ID

	// 列表包含新行业
	lg := industryGet(t, s, "/api/admin/industries", tokens["admin"])
	var listed struct {
		Success    bool               `json:"success"`
		Industries []*store.KBPackage `json:"industries"`
	}
	if err := json.Unmarshal(lg.Body.Bytes(), &listed); err != nil || !listed.Success {
		t.Fatalf("列表应成功: %v (%s)", err, lg.Body.String())
	}
	found := false
	for _, x := range listed.Industries {
		if x.Code == "media" {
			found = true
		}
	}
	if !found {
		t.Fatalf("列表未包含新行业 media")
	}

	// 编辑名称
	up := industryPost(t, s, "/api/admin/industries/update", tokens["admin"],
		map[string]interface{}{"id": id, "name": "传媒广告·改名"})
	var upOut struct{ Success bool }
	if err := json.Unmarshal(up.Body.Bytes(), &upOut); err != nil || !upOut.Success {
		t.Fatalf("编辑行业应成功: %v (%s)", err, up.Body.String())
	}
	got, _ := s.Store.GetKBPackage(id, store.SharedHostTenant)
	if got == nil || got.Name != "传媒广告·改名" {
		t.Fatalf("行业名未更新: %+v", got)
	}

	// 停用 / 启用
	st0 := industryPost(t, s, "/api/admin/industries/status", tokens["admin"],
		map[string]interface{}{"id": id, "enabled": 0})
	var st0Out struct{ Success bool }
	if err := json.Unmarshal(st0.Body.Bytes(), &st0Out); err != nil || !st0Out.Success {
		t.Fatalf("停用行业应成功: %v (%s)", err, st0.Body.String())
	}
	if got, _ = s.Store.GetKBPackage(id, store.SharedHostTenant); got.Enabled != 0 {
		t.Fatalf("行业停用失败: enabled=%d", got.Enabled)
	}
	st1 := industryPost(t, s, "/api/admin/industries/status", tokens["admin"],
		map[string]interface{}{"id": id, "enabled": 1})
	var st1Out struct{ Success bool }
	if err := json.Unmarshal(st1.Body.Bytes(), &st1Out); err != nil || !st1Out.Success {
		t.Fatalf("启用行业应成功: %v (%s)", err, st1.Body.String())
	}
	if got, _ = s.Store.GetKBPackage(id, store.SharedHostTenant); got.Enabled != 1 {
		t.Fatalf("行业启用失败: enabled=%d", got.Enabled)
	}

	// 删除
	del := industryPost(t, s, "/api/admin/industries/delete", tokens["admin"],
		map[string]interface{}{"id": id})
	var delOut struct{ Success bool }
	if err := json.Unmarshal(del.Body.Bytes(), &delOut); err != nil || !delOut.Success {
		t.Fatalf("删除行业应成功: %v (%s)", err, del.Body.String())
	}
	if got, _ = s.Store.GetKBPackage(id, store.SharedHostTenant); got != nil {
		t.Fatalf("行业删除后仍存在: %+v", got)
	}
}

// TestIndustryAPICodeValidation 非法 code 被拒绝（大写以外的符号/重复 code）。
func TestIndustryAPICodeValidation(t *testing.T) {
	s, tokens := newIndustryTestServer(t)

	// 含非法字符（连字符）
	r1 := industryPost(t, s, "/api/admin/industries/create", tokens["admin"],
		map[string]interface{}{"code": "auto-car", "name": "非法"})
	var o1 struct{ Success bool }
	_ = json.Unmarshal(r1.Body.Bytes(), &o1)
	if r1.Code != http.StatusBadRequest {
		t.Fatalf("含空格 code 应 400 拒绝，实得 %d: %s", r1.Code, r1.Body.String())
	}

	// 重复 code（先建 auto，再建同名）
	industryPost(t, s, "/api/admin/industries/create", tokens["admin"],
		map[string]interface{}{"code": "auto", "name": "汽车"})
	r2 := industryPost(t, s, "/api/admin/industries/create", tokens["admin"],
		map[string]interface{}{"code": "auto", "name": "汽车重复"})
	var o2 struct{ Success bool }
	_ = json.Unmarshal(r2.Body.Bytes(), &o2)
	if r2.Code != http.StatusBadRequest {
		t.Fatalf("重复 code 应 400 拒绝，实得 %d: %s", r2.Code, r2.Body.String())
	}
}

// TestIndustryAPINonSuperRejected 非超管（企业普通用户）管理行业被 403 拦截。
func TestIndustryAPINonSuperRejected(t *testing.T) {
	s, tokens := newIndustryTestServer(t)

	r := industryPost(t, s, "/api/admin/industries/create", tokens["u_ent"],
		map[string]interface{}{"code": "media", "name": "传媒广告"})
	if r.Code != http.StatusForbidden {
		t.Fatalf("非超管创建行业应 403，实得 %d: %s", r.Code, r.Body.String())
	}
}

// TestIndustryAPIDeleteReferencedRejected 被租户引用的行业删除被 400 拒绝（引用保护）。
func TestIndustryAPIDeleteReferencedRejected(t *testing.T) {
	s, tokens := newIndustryTestServer(t)

	// 创建 auto 行业包（与测试租户 tauto 的 industry=auto 呼应，触发引用保护）
	cr := industryPost(t, s, "/api/admin/industries/create", tokens["admin"],
		map[string]interface{}{"code": "auto", "name": "汽车"})
	var created struct {
		Success  bool              `json:"success"`
		Industry *store.KBPackage  `json:"industry"`
	}
	if err := json.Unmarshal(cr.Body.Bytes(), &created); err != nil || !created.Success || created.Industry == nil {
		t.Fatalf("创建 auto 行业应成功: %v (%s)", err, cr.Body.String())
	}

	// 删除应被 400 拒绝（被企业租户引用）
	del := industryPost(t, s, "/api/admin/industries/delete", tokens["admin"],
		map[string]interface{}{"id": created.Industry.ID})
	if del.Code != http.StatusBadRequest {
		t.Fatalf("被引用行业删除应 400 拒绝，实得 %d: %s", del.Code, del.Body.String())
	}
}
