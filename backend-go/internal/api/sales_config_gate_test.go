// ============ 本文件职责中文说明 ============
// 二期「前端可配」API 面行为锁：未登录 401／角色不足 403、三段配置保存回读、
// 非法档位 400 且库内一字不动、配置与词表都按租户隔离、平台种子仅超管可补。
// 复用 adminGateServer 的全量路由表，不手挑清单。
// ========================================
package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"translator/internal/auth"
	"translator/internal/iam"
	"translator/internal/store"
)

// salesConfigToken 在给定服务上签一个指定租户与角色的登录态。
// 参数：t=测试句柄；srv=服务；tid=租户；role=角色；返回：Bearer token。
func salesConfigToken(t *testing.T, srv *Server, tid int64, role string) string {
	t.Helper()
	u, err := srv.Store.CreateUser(tid, "sales_cfg_"+role+"_"+time.Now().Format("150405.000000000"),
		auth.PasswordHash("pw123456"), "销售配置闸门", role, 0, 0)
	if err != nil {
		t.Fatalf("建测试身份失败: %v", err)
	}
	tok, err := auth.Sign(u, time.Hour)
	if err != nil {
		t.Fatalf("签 token 失败: %v", err)
	}
	return tok
}

// salesConfigCall 打一次配置面请求（GET 或 POST JSON）。
// 参数：srv=服务；method=方法；path=路径；tok=登录态；body=请求体；返回：状态码与解析后的响应。
func salesConfigCall(srv *Server, method, path, tok string, body any) (int, map[string]interface{}) {
	var reader *bytes.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	r := httptest.NewRequest(method, path, reader)
	if tok != "" {
		r.Header.Set("Authorization", "Bearer "+tok)
	}
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, r)
	var out map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

// C1：新配置路由未登录 401、普通会员 403（状态码语义不许并成一档）。
func TestSalesConfigRoutesRequireTenantAdmin(t *testing.T) {
	srv, memberTok := adminGateServer(t)
	paths := []string{
		"/api/admin/sales/config", "/api/admin/sales/config/save",
		"/api/admin/sales/lexicon/save", "/api/admin/sales/lexicon/delete",
		"/api/admin/sales/lexicon/seed",
	}
	for _, p := range paths {
		if code, _ := salesConfigCall(srv, http.MethodGet, p, "", nil); code != http.StatusUnauthorized {
			t.Fatalf("C1：%s 未登录必须 401，实得 %d", p, code)
		}
		if code, _ := salesConfigCall(srv, http.MethodGet, p, memberTok, nil); code != http.StatusForbidden {
			t.Fatalf("C1：%s 普通会员必须 403，实得 %d", p, code)
		}
	}
}

// C2：三段配置保存→回读逐字段等值；非法档位 400 且库内一字不动。
func TestSalesConfigSaveReadBackAndValidation(t *testing.T) {
	srv, _ := adminGateServer(t)
	tok := salesConfigToken(t, srv, 1, iam.RoleTenantAdmin)
	cfg := defaultSalesConfig()
	cfg.ModelStages[SalesStageReply] = "sales-reply-v2"
	cfg.Flow.Enabled = true
	cfg.Flow.MaxSteps = 42
	cfg.Strategy.MinSamples = 9
	code, body := salesConfigCall(srv, http.MethodPost, "/api/admin/sales/config/save", tok, cfg)
	if code != http.StatusOK || body["success"] != true {
		t.Fatalf("C2：合法配置应 200，实得 %d body=%v", code, body)
	}
	code, body = salesConfigCall(srv, http.MethodGet, "/api/admin/sales/config", tok, nil)
	if code != http.StatusOK || body["configured"] != true {
		t.Fatalf("C2：回读必须 200 且 configured=true，实得 %d body=%v", code, body)
	}
	raw, _ := json.Marshal(body["config"])
	var got SalesConfig
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("C2：配置反序列化失败: %v", err)
	}
	if got.ModelStages[SalesStageReply] != "sales-reply-v2" || got.Flow.MaxSteps != 42 || got.Strategy.MinSamples != 9 || !got.Flow.Enabled {
		t.Fatalf("C2：三段配置回读与保存不等值: %+v", got)
	}
	// 非法档位必须 400，且库内仍是上一份合法配置。
	for _, bad := range []SalesConfig{
		func() SalesConfig { c := defaultSalesConfig(); c.Flow.MaxSteps = 999; return c }(),
		func() SalesConfig { c := defaultSalesConfig(); c.Strategy.Scene = "not_a_scene"; return c }(),
		func() SalesConfig { c := defaultSalesConfig(); c.ModelStages["unknown_stage"] = "x"; return c }(),
		func() SalesConfig { c := defaultSalesConfig(); c.Strategy.Lang = "xx"; return c }(),
	} {
		if code, _ := salesConfigCall(srv, http.MethodPost, "/api/admin/sales/config/save", tok, bad); code != http.StatusBadRequest {
			t.Fatalf("C2：非法配置必须 400，实得 %d cfg=%+v", code, bad)
		}
	}
	code, body = salesConfigCall(srv, http.MethodGet, "/api/admin/sales/config", tok, nil)
	if code != http.StatusOK {
		t.Fatalf("C2：拒绝后回读仍应 200，实得 %d", code)
	}
	raw, _ = json.Marshal(body["config"])
	var after SalesConfig
	_ = json.Unmarshal(raw, &after)
	if after.Flow.MaxSteps != 42 || after.ModelStages[SalesStageReply] != "sales-reply-v2" {
		t.Fatalf("C2：非法保存不许改动库内既有配置，实得 %+v", after)
	}
}

// C3：配置与词表都按租户隔离；平台种子仅超管可补种且幂等。
func TestSalesConfigAndLexiconAreTenantScoped(t *testing.T) {
	srv, _ := adminGateServer(t)
	tok1 := salesConfigToken(t, srv, 1, iam.RoleTenantAdmin)
	tok2 := salesConfigToken(t, srv, 2, iam.RoleTenantAdmin)
	cfg := defaultSalesConfig()
	cfg.ModelStages[SalesStageMining] = "tenant1-only"
	if code, _ := salesConfigCall(srv, http.MethodPost, "/api/admin/sales/config/save", tok1, cfg); code != http.StatusOK {
		t.Fatalf("C3：租户 1 保存失败: %d", code)
	}
	code, body := salesConfigCall(srv, http.MethodGet, "/api/admin/sales/config", tok2, nil)
	if code != http.StatusOK || body["configured"] != false {
		t.Fatalf("C3：租户 2 不许读到租户 1 的配置，实得 %d body=%v", code, body)
	}
	lex := map[string]interface{}{"scene": "cross_border", "lang": "zh", "kind": "anchor_greeting", "words": []string{"租户专用招呼"}}
	if code, _ := salesConfigCall(srv, http.MethodPost, "/api/admin/sales/lexicon/save", tok1, lex); code != http.StatusOK {
		t.Fatalf("C3：租户 1 词表保存失败: %d", code)
	}
	if code, _ := salesConfigCall(srv, http.MethodPost, "/api/admin/sales/lexicon/delete", tok2, map[string]interface{}{"id": 1}); code != http.StatusNotFound {
		t.Fatalf("C3：跨租户删词表必须 404，实得 %d", code)
	}
	// 平台种子：租户管理员 403，超管 200 且二次补种不增行。
	if code, _ := salesConfigCall(srv, http.MethodPost, "/api/admin/sales/lexicon/seed", tok1, nil); code != http.StatusForbidden {
		t.Fatalf("C3：平台种子仅超管可补，实得 %d", code)
	}
	superTok := salesConfigToken(t, srv, 0, iam.RoleAdmin)
	if code, _ := salesConfigCall(srv, http.MethodPost, "/api/admin/sales/lexicon/seed", superTok, nil); code != http.StatusOK {
		t.Fatalf("C3：超管补种应 200，实得 %d", code)
	}
	cfgBody := func() map[string]interface{} {
		_, b := salesConfigCall(srv, http.MethodGet, "/api/admin/sales/config", tok1, nil)
		return b
	}
	before := cfgBody()["lexicon"].([]interface{})
	if code, _ := salesConfigCall(srv, http.MethodPost, "/api/admin/sales/lexicon/seed", superTok, nil); code != http.StatusOK {
		t.Fatalf("C3：二次补种应 200，实得 %d", code)
	}
	after := cfgBody()["lexicon"].([]interface{})
	if len(before) != len(after) {
		t.Fatalf("C3：重复补种不许增行，实得 %d → %d", len(before), len(after))
	}
}

// ============ A11（W3 扩充）：销售域 handler 鉴权同源源码腿 ============
// 判据：新建管理面 sales_role_api.go 的每个 handle* 函数体只允许经
//   salesRoleGate / salesConfigAdmin 取鉴权，出现裸 authUser 即红；scanned==0 Fatal。
// ★ 扫描面说明：sales_api.go 四端点由主 agent W3 同批收紧（见报告「4 行调用」清单），
//   收紧后本锁扫描面可一并覆盖；本批先把「新管理面不许开第二套裸 authUser」钉死（M0227 靶）。
// ================================================================

// salesAuthSameSourceViolations 扫描一份源码里 handle* 函数体的鉴权来源（正反对照共用）。
// 返回违规清单与扫描到的 handler 数。
func salesAuthSameSourceViolations(src string) ([]string, int) {
	re := regexp.MustCompile(`(?s)func \(s \*Server\) (handle\w+)\([^)]*\) \{(.*?)\n\}`)
	var bad []string
	scanned := 0
	for _, m := range re.FindAllStringSubmatch(src, -1) {
		scanned++
		name, body := m[1], m[2]
		if strings.Contains(body, "s.authUser(") {
			bad = append(bad, name+": 出现裸 authUser")
		}
		if !strings.Contains(body, "s.salesConfigAdmin(") && !strings.Contains(body, "s.salesRoleGate(") {
			bad = append(bad, name+": 未经 salesRoleGate/salesConfigAdmin")
		}
	}
	return bad, scanned
}

// TestSalesConfigAuthSameSource A11：管理面 handler 鉴权同源 + M0227 反证 + 合法样本放行。
func TestSalesConfigAuthSameSource(t *testing.T) {
	raw, err := os.ReadFile("sales_role_api.go")
	if err != nil {
		t.Fatalf("A11 读 sales_role_api.go 失败: %v", err)
	}
	bad, scanned := salesAuthSameSourceViolations(string(raw))
	if scanned == 0 {
		t.Fatal("A11 射程自证失败：scanned==0，一个 handler 都没扫到")
	}
	if len(bad) != 0 {
		t.Fatalf("A11：销售域 handler 鉴权不同源: %v", bad)
	}
	// M0227 反证：在 handler 里写一次裸 authUser，扫描器必须命中（不许恒绿）。
	rogue := `func (s *Server) handleRogue(w http.ResponseWriter, r *http.Request) {
	u := s.authUser(r)
	_ = u
}`
	rogueBad, rogueScanned := salesAuthSameSourceViolations(rogue)
	if rogueScanned != 1 || len(rogueBad) == 0 {
		t.Fatalf("M0227 反证：裸 authUser 必须判红，实得 scanned=%d bad=%v", rogueScanned, rogueBad)
	}
	// 合法样本放行：只把鉴权改成统一助手的同形副本，违规应为空（防「一律判红」）。
	good := `func (s *Server) handleGood(w http.ResponseWriter, r *http.Request) {
	u, tid, ok := s.salesConfigAdmin(w, r)
	_, _, _ = u, tid, ok
}`
	goodBad, goodScanned := salesAuthSameSourceViolations(good)
	if goodScanned != 1 || len(goodBad) != 0 {
		t.Fatalf("A11 正向对照：合法统一助手样本应放行，实得 scanned=%d bad=%v", goodScanned, goodBad)
	}
}

// ============ A10（按本批登记改在 sales_config_gate_test.go 扩充）：四端点 × 四档全表 ============
// 判据：四端点 × 四档角色 × 401/403/200 全表；期望值一律从 store.PermForEndpoint() +
// store.DefaultSalesRoleAllowed() 现推，cases<16 Fatal；未登录独立 401；approve 单独钉死 ≥3。
// ★ 独立性：直接调 s.salesRoleGate(w, r)（A09 收紧后的唯一鉴权入口），
//   不依赖 sales_api.go 四端点是否已由主 agent 补 4 行调用——那部分由 A11 源码腿 + 报告清单收口。
// ============================================================================

// TestSalesRoleGateFourEndpointsFourRoles A10 全表：16 组 + 未登录 401。
func TestSalesRoleGateFourEndpointsFourRoles(t *testing.T) {
	srv, _ := adminGateServer(t)
	paths := []string{
		"/api/sales/capability",
		"/api/sales/drafts",
		"/api/sales/draft",
		"/api/sales/draft/approve",
	}
	roles := []string{iam.RoleUser, iam.RoleDeptAdmin, iam.RoleTenantAdmin, iam.RoleSuperAdmin}

	// M0226 靶：转正审批端点必须 ≥3，不许降到 2/1（独立于映射表，防改映射把期望一起带走）。
	approvePerm := store.PermForEndpoint("/api/sales/draft/approve")
	if !store.DefaultSalesRoleAllowed(3, approvePerm) || store.DefaultSalesRoleAllowed(2, approvePerm) {
		t.Fatalf("A10：approve 端点必须 ≥3，不许降到 1/2（M0226 靶），实得 perm=%s", approvePerm)
	}

	cases := 0
	for _, p := range paths {
		perm := store.PermForEndpoint(p)
		if perm == "" {
			t.Fatalf("A10：端点 %s 未在 PermForEndpoint 登记，无法现推期望", p)
		}
		for _, role := range roles {
			cases++
			tok := salesConfigToken(t, srv, 1, role)
			r := httptest.NewRequest(http.MethodGet, p, nil)
			r.Header.Set("Authorization", "Bearer "+tok)
			w := httptest.NewRecorder()
			_, _, ok := srv.salesRoleGate(w, r)
			want := store.DefaultSalesRoleAllowed(auth.RoleLevel(role), perm)
			if want {
				if !ok || w.Code != http.StatusOK {
					t.Fatalf("A10：%s @ %s 应放行(200)，实得 ok=%v code=%d", p, role, ok, w.Code)
				}
			} else if ok || w.Code != http.StatusForbidden {
				t.Fatalf("A10：%s @ %s 应 403，实得 ok=%v code=%d", p, role, ok, w.Code)
			}
		}
	}
	if cases < 16 {
		t.Fatalf("A10 射程自证失败：cases<16，实得 %d", cases)
	}
	r := httptest.NewRequest(http.MethodGet, "/api/sales/drafts", nil)
	w := httptest.NewRecorder()
	_, _, ok := srv.salesRoleGate(w, r)
	if ok || w.Code != http.StatusUnauthorized {
		t.Fatalf("A10：未登录必须 401，实得 ok=%v code=%d", ok, w.Code)
	}
}

// TestSalesRoleGateUnknownEndpointFailClosed 未登记端点 fail-closed，不许猜默认权限放行。
func TestSalesRoleGateUnknownEndpointFailClosed(t *testing.T) {
	srv, _ := adminGateServer(t)
	tok := salesConfigToken(t, srv, 1, iam.RoleSuperAdmin)
	r := httptest.NewRequest(http.MethodGet, "/api/sales/not-registered", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	_, _, ok := srv.salesRoleGate(w, r)
	if ok || w.Code != http.StatusForbidden {
		t.Fatalf("A10：未登记端点必须 fail-closed 403，实得 ok=%v code=%d", ok, w.Code)
	}
}
