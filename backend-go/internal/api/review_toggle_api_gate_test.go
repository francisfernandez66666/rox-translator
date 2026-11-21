// ============ review_toggle_api_gate_test.go · 职责说明 ============
// 「复核链路运营开关」的 HTTP 面机械锁（★ 2026-11-21 用户令：超管在后台可配）。
//
// 三条判据，缺一条都会让这个功能在现网"看不见"或"改不动"：
//
//	A 回显：GET /api/admin/ops/policy 的 platform / effective 两段都必须带 quality，
//	  且没配过时是 true/true（面板据此回填；字段缺席＝面板开关恒显示关＝运营以为线上关了）。
//	B 落库：POST save 带 quality 必须真写进 system_config.ops_policy（否则保存成功、开关没生效，
//	  而屏幕上是一次"已保存"）。
//	C 后门：时间窗里夹带 quality.* 必须 400 且**库里一字不动**（与 billing/payment/front/instant 同款）。
//	  ⚠️ 这条不只要 ops 包单测——保存口是唯一写点，绕过它就等于绕过整条闸门。
//
// 只测 HTTP 面与库侧事实，不跑真模型（引擎侧生效判据见 engine/review_scope_test.go
// 与 orchestrator/review_toggle_workflow_test.go）。
// ============================================
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"translator/internal/store"
)

// rtGetPolicy 以超管身份读一次运营策略，返回解析后的四层视图。
func rtGetPolicy(t *testing.T, s *Server, super *store.User) map[string]interface{} {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/admin/ops/policy", nil)
	req.Header.Set("Authorization", bearerFor(t, super))
	rec := httptest.NewRecorder()
	s.handleOpsPolicy(rec, req)
	if rec.Code != 200 {
		t.Fatalf("读取运营策略应 200，实得 %d: %s", rec.Code, rec.Body.String())
	}
	var out map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("响应解析失败: %v (%s)", err, rec.Body.String())
	}
	return out
}

// rtQualityOf 从四层视图里取某一段的 quality 对象。
func rtQualityOf(t *testing.T, view map[string]interface{}, section string) map[string]interface{} {
	t.Helper()
	sec, ok := view[section].(map[string]interface{})
	if !ok {
		t.Fatalf("响应缺少 %s 段: %v", section, view)
	}
	q, ok := sec["quality"].(map[string]interface{})
	if !ok {
		t.Fatalf("%s 段缺少 quality 对象（面板无法回填复核链路开关）: %v", section, sec)
	}
	return q
}

// TestOpsPolicyQualityFace A/B 腿：默认全开 + 保存后真落库并能读回。
func TestOpsPolicyQualityFace(t *testing.T) {
	st := newTestAPIStore(t)
	s := &Server{Store: st}
	super, err := st.CreateUser(0, "q_super", "x", "超管", store.RoleSuperAdmin, 0, 0)
	if err != nil {
		t.Fatalf("创建超管失败: %v", err)
	}

	// A：没配过 ⇒ base/effective 两段都必须是 true/true（字段缺席会让面板显示"已关闭"）。
	//   ⚠️ platform 段是**补丁视图**（指针 + omitempty）：没配过时它就该是空对象——
	//   那正是"库里什么都没存"的诚实形态，所以这一档不参与"默认全开"的断言。
	view := rtGetPolicy(t, s, super)
	for _, section := range []string{"base", "effective"} {
		q := rtQualityOf(t, view, section)
		if q["instant_review_enabled"] != true || q["ticket_review_enabled"] != true {
			t.Fatalf("%s.quality 默认应为全开，实得 %v", section, q)
		}
	}
	if q := rtQualityOf(t, view, "platform"); len(q) != 0 {
		t.Fatalf("未配置时 platform.quality 应为空补丁（omitempty 的指针语义），实得 %v", q)
	}

	// B：只关工单档 ⇒ 落库、且回显分档（即时仍开）
	body := `{"scope":"platform","policy":{"quality":{"ticket_review_enabled":false}}}`
	req := httptest.NewRequest(http.MethodPost, "/api/admin/ops/policy/save", strings.NewReader(body))
	req.Header.Set("Authorization", bearerFor(t, super))
	rec := httptest.NewRecorder()
	s.handleOpsPolicySave(rec, req)
	if rec.Code != 200 {
		t.Fatalf("超管保存复核链路开关应 200，实得 %d: %s", rec.Code, rec.Body.String())
	}
	raw, _ := st.GetConfig("ops_policy")
	if !strings.Contains(raw, "ticket_review_enabled") {
		t.Fatalf("ops_policy 未写入 ticket_review_enabled（保存成功但开关没落地）: %s", raw)
	}
	view2 := rtGetPolicy(t, s, super)
	plat := rtQualityOf(t, view2, "platform")
	if plat["ticket_review_enabled"] != false {
		t.Fatalf("platform.quality.ticket_review_enabled 应为 false，实得 %v", plat)
	}
	// platform 是补丁视图：没动即时档 ⇒ 该键缺席（nil），不许被写成 true/false
	if v, exists := plat["instant_review_enabled"]; exists {
		t.Fatalf("只改工单档时 platform 不应凭空出现即时档表态，实得 %v", v)
	}
	eff := rtQualityOf(t, view2, "effective")
	if eff["ticket_review_enabled"] != false || eff["instant_review_enabled"] != true {
		t.Fatalf("effective.quality 应为「工单关、即时开」，实得 %v", eff)
	}
}

// TestOpsPolicyQualityWindowBackdoor C 腿：时间窗夹带必须 400 且库里一字不动。
func TestOpsPolicyQualityWindowBackdoor(t *testing.T) {
	st := newTestAPIStore(t)
	s := &Server{Store: st}
	super, err := st.CreateUser(0, "q_super2", "x", "超管", store.RoleSuperAdmin, 0, 0)
	if err != nil {
		t.Fatalf("创建超管失败: %v", err)
	}
	if err := st.SetConfig("ops_policy", `{"version":1}`); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	before, _ := st.GetConfig("ops_policy")

	body := `{"scope":"platform","policy":{"promo_windows":[{"id":"q1","start":"2026-01-01T00:00:00Z","end":"2026-12-31T00:00:00Z","overrides":{"quality":{"ticket_review_enabled":false}}}]}}`
	req := httptest.NewRequest(http.MethodPost, "/api/admin/ops/policy/save", strings.NewReader(body))
	req.Header.Set("Authorization", bearerFor(t, super))
	rec := httptest.NewRecorder()
	s.handleOpsPolicySave(rec, req)
	if rec.Code != 400 {
		t.Fatalf("时间窗夹带 quality.* 应 400，实得 %d: %s", rec.Code, rec.Body.String())
	}
	after, _ := st.GetConfig("ops_policy")
	if before != after {
		t.Fatalf("被拒请求不得改动配置: %q → %q", before, after)
	}

	// 反向对照：同一形状但窗口不带 quality 时必须仍能保存（别把拦截写成"窗口一律拒绝"）
	okBody := `{"scope":"platform","policy":{"promo_windows":[{"id":"q2","start":"2026-01-01T00:00:00Z","end":"2026-12-31T00:00:00Z","overrides":{"billing":{"markup_multiplier":1.6}}}]}}`
	req2 := httptest.NewRequest(http.MethodPost, "/api/admin/ops/policy/save", strings.NewReader(okBody))
	req2.Header.Set("Authorization", bearerFor(t, super))
	rec2 := httptest.NewRecorder()
	s.handleOpsPolicySave(rec2, req2)
	if rec2.Code != 200 {
		t.Fatalf("合规窗口应能保存，实得 %d: %s", rec2.Code, rec2.Body.String())
	}
}
