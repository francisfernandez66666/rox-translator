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
	"testing"
	"time"

	"translator/internal/auth"
	"translator/internal/iam"
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
