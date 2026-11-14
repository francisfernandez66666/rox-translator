// ============ 本文件职责中文说明 ============
// 二期 §8.1 B1／B2 的 API 面判据：/api/sales/capability 两把闸必须读出
// off／half／on **三种互不相同的状态词**（只开一把不许呈现为「已生效」）；
// 未登录必须 401（不是 403、不是 200 空壳）；登录态逐档正向对照。
// 复用同包 adminGateServer 的全量路由表，故意不手挑清单。
// ========================================
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestSalesCapabilityThreeStatesAreDistinct 两把闸三态 + 未登录 401。
func TestSalesCapabilityThreeStatesAreDistinct(t *testing.T) {
	// 业务正向用租户管理员；adminGateServer 返回的普通会员留作权限反向对照。
	srv, lowTok := adminGateServer(t)
	tok := salesGateAdminToken(t, srv)
	set := func(k, v string) {
		t.Helper()
		if err := srv.Store.SetConfig(k, v); err != nil {
			t.Fatalf("写配置 %s 失败: %v", k, err)
		}
	}
	probe := func(token string) (int, map[string]interface{}) {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "/api/sales/capability", nil)
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		srv.mux.ServeHTTP(w, r)
		var body map[string]interface{}
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		return w.Code, body
	}

	if code, _ := probe(""); code != http.StatusUnauthorized {
		t.Fatalf("未登录必须 401，实得 %d", code)
	}
	// ★ 反向对照（三期 W3 权限收紧）：普通会员等级 1 读 capability 必须 403。
	//   夹具从普通会员换成管理员之后，这条是唯一还钉着「不得因夹具升级放宽权限闸」的腿。
	if code, _ := probe(lowTok); code != http.StatusForbidden {
		t.Fatalf("普通会员应 403，实得 %d", code)
	}

	cases := []struct {
		name    string
		engine  string
		billing string
		want    string
	}{
		{"两把都关", "0", "0", "off"},
		{"只开引擎", "1", "0", "half"},
		{"只开计费", "0", "1", "half"},
		{"两把都开", "1", "1", "on"},
	}
	seen := map[string]bool{}
	for _, c := range cases {
		set(salesEngineSwitchKey, c.engine)
		set(billingEnforcedKey, c.billing)
		code, body := probe(tok)
		if code != http.StatusOK {
			t.Fatalf("%s：登录态应 200，实得 %d body=%v", c.name, code, body)
		}
		status, _ := body["status"].(string)
		if status != c.want {
			t.Fatalf("B2：%s 应读 %s，实得 %s ⇒ 半套配置不许呈现为已生效", c.name, c.want, status)
		}
		if body["engine_enabled"] != (c.engine == "1") || body["billing_enforced"] != (c.billing == "1") {
			t.Fatalf("%s：两把闸的逐档读数与库内不一致: %v", c.name, body)
		}
		seen[status] = true
	}
	if len(seen) != 3 {
		t.Fatalf("三态必须互不相同（off/half/on），实得 %v", seen)
	}
}
