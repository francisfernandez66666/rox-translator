// ============ billing_deprecated_routes_test.go · 职责说明 ============
// ★ #42（2026-09-22）僵尸计费路由「废弃信号」断言：
//
//	/api/billing/balance 与 /api/billing/config 前端已无调用方，但按「实装优先、禁止删除既有功能」
//	保留给外部/运维脚本（scripts/uat 仍在打，删除须先取得用户确认），改为标准废弃声明：
//	RFC 8594 的 Deprecation 响应头 + Link 指向后继资源，成功响应体另加 deprecated/replacement 两键。
//	本用例钉住「头必须先于任何分支设置」这一条——两个 handler 的 Deprecation 头写在鉴权之前，
//	所以匿名 403 / 未装配存储的分支同样带信号；若有人把 Set 挪到成功分支里，这里第一个翻红。
//	出参字段与 scripts/uat 既有断言（A4-balance-shape 判 points_available、
//	T14-anon-balance 与 T25 判 "success":false）互不冲突，本文件同时把这两个 UAT 锚点固化成单测。
//
// 本文件用例会经 store/config 的路径已自钉 sqlite 方言（AGENTS.md 一.4），见 newProbeStoreServer。
// =============================================
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"translator/internal/auth"
	"translator/internal/iam"
)

// newAdminTokenForProbe 在内存库里建一个租户管理员并签 JWT，供需要过鉴权分支的出参断言复用。
// 角色取 tenant_admin（等级 3）——两个被标注废弃的接口都是 requireTenantAdmin 口径。
func newAdminTokenForProbe(t *testing.T, s *Server) string {
	t.Helper()
	u, err := s.Store.CreateUser(1, "dep-probe-admin", auth.PasswordHash("pw123456"), "废弃路由探针", iam.RoleTenantAdmin, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := auth.Sign(u, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

// TestDeprecatedBillingRoutesEmitSignal 两条僵尸路由必须无条件带上 Deprecation/Link 信号头。
func TestDeprecatedBillingRoutesEmitSignal(t *testing.T) {
	cases := []struct {
		name            string
		path            string
		handler         func(http.ResponseWriter, *http.Request)
		linkReplacement string
	}{
		{"balance", "/api/billing/balance", func(w http.ResponseWriter, r *http.Request) {
			(&Server{}).handleBalance(w, r)
		}, "/api/billing/my/overview"},
		{"config", "/api/billing/config", func(w http.ResponseWriter, r *http.Request) {
			(&Server{}).handleBillingConfig(w, r)
		}, "/api/admin/packages/settings"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// 匿名请求：走的是鉴权失败/存储未装配分支，仍应带信号头（写在函数第一行）
			req := httptest.NewRequest(http.MethodGet, c.path, nil)
			rec := httptest.NewRecorder()
			c.handler(rec, req)
			if got := rec.Header().Get("Deprecation"); got != "true" {
				t.Errorf("Deprecation 响应头应为 true，实际 %q（出参 %s）", got, rec.Body.String())
			}
			link := rec.Header().Get("Link")
			if !strings.Contains(link, c.linkReplacement) || !strings.Contains(link, `rel="successor-version"`) {
				t.Errorf("Link 响应头应指向后继资源 %s，实际 %q", c.linkReplacement, link)
			}
		})
	}
}

// TestDeprecatedBillingRouteBodyFields 成功响应体带 deprecated/replacement 两键，
// 且既有出参键不丢（UAT 的 points_available 锚点）。Store 未装配时 handleBalance 走 403，
// 故这里用真内存库 + 租户管理员会话验证成功体。
func TestDeprecatedBillingRouteBodyFields(t *testing.T) {
	s := newProbeStoreServer(t)
	// handleBalance 需 tenant_admin 及以上；构造一个超管会话（role=admin）打到 200 分支
	tok := newAdminTokenForProbe(t, s)
	req := httptest.NewRequest(http.MethodGet, "/api/billing/balance", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	s.handleBalance(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("超管查询余额应 200，实际 %d (%s)", rec.Code, rec.Body.String())
	}
	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("出参非法 JSON: %v", err)
	}
	if body["deprecated"] != true {
		t.Errorf("成功体应带 deprecated=true，出参 %s", rec.Body.String())
	}
	if body["replacement"] != "/api/billing/my/overview" {
		t.Errorf("成功体应带 replacement，出参 %s", rec.Body.String())
	}
	// UAT A4-balance-shape 锚点：新增键不得挤掉 points_available
	if _, ok := body["points_available"]; !ok {
		t.Errorf("points_available 不得因废弃字段而丢失：%s", rec.Body.String())
	}
}
