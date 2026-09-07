// ============ 本文件职责中文说明 ============
// 2026-09 权限收口与奖励总开关的 HTTP 层测试：
//   - TestOpsPolicySavePermission：运营策略仅超管可写（租户管理员 403）
//   - TestTaskCenterPolicyGate：task.enabled 总开关关闭后任务列表置空、领取被拒
//   - TestEffPayMode：支付模式收敛为运营策略 payment.mode（缺省 mock）
// =============================================
package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"translator/internal/auth"
	"translator/internal/store"
)

// newTestAPIStore 创建基于内存 SQLite 的 api 层测试用 Store（完整 schema）。
func newTestAPIStore(t *testing.T) *store.Store {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("打开内存数据库失败: %v", err)
	}
	st, err := store.New(db)
	if err != nil {
		t.Fatalf("创建测试 Store 失败: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return st
}

// bearerFor 为指定用户签发 JWT 并返回 Authorization 请求头值。
func bearerFor(t *testing.T, u *store.User) string {
	t.Helper()
	tok, err := auth.Sign(u, time.Hour)
	if err != nil {
		t.Fatalf("签发 JWT 失败: %v", err)
	}
	return "Bearer " + tok
}

// TestOpsPolicySavePermission 运营策略保存权限收口（2026-09）：
// 仅超级管理员（平台级）可写；租户管理员调用返回 403，且系统配置不被篡改。
func TestOpsPolicySavePermission(t *testing.T) {
	st := newTestAPIStore(t)
	s := &Server{Store: st}

	// 准备超管（平台上下文 tenant_id=0）与租户管理员
	super, err := st.CreateUser(0, "ops_super", "x", "超管", store.RoleSuperAdmin, 0, 0)
	if err != nil {
		t.Fatalf("创建超管失败: %v", err)
	}
	tadmin, err := st.CreateUser(1, "ops_tadmin", "x", "租管", store.RoleTenantAdmin, 0, 0)
	if err != nil {
		t.Fatalf("创建租户管理员失败: %v", err)
	}

	// ① 超管保存成功（scope 恒 platform，租户级被忽略）
	body := `{"scope":"platform","policy":{"invite":{"enabled":false},"task":{"enabled":false}}}`
	req := httptest.NewRequest(http.MethodPost, "/api/admin/ops/policy/save", strings.NewReader(body))
	req.Header.Set("Authorization", bearerFor(t, super))
	rec := httptest.NewRecorder()
	s.handleOpsPolicySave(rec, req)
	if rec.Code != 200 {
		t.Fatalf("超管保存应 200，实得 %d: %s", rec.Code, rec.Body.String())
	}
	var ok struct {
		Success bool   `json:"success"`
		Scope   string `json:"scope"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &ok); err != nil || !ok.Success || ok.Scope != "platform" {
		t.Fatalf("超管保存响应异常: %s", rec.Body.String())
	}
	// 策略已落库
	raw, _ := st.GetConfig("ops_policy")
	if !strings.Contains(raw, `"invite":{"enabled":false}`) && !strings.Contains(raw, `"enabled":false`) {
		t.Fatalf("ops_policy 应已写入: %s", raw)
	}

	// ② 租户管理员保存被拒（403），且不回写
	before, _ := st.GetConfig("ops_policy")
	req2 := httptest.NewRequest(http.MethodPost, "/api/admin/ops/policy/save", strings.NewReader(`{"scope":"tenant","policy":{"invite":{"enabled":true}}}`))
	req2.Header.Set("Authorization", bearerFor(t, tadmin))
	rec2 := httptest.NewRecorder()
	s.handleOpsPolicySave(rec2, req2)
	if rec2.Code != 403 {
		t.Fatalf("租户管理员保存应 403，实得 %d: %s", rec2.Code, rec2.Body.String())
	}
	after, _ := st.GetConfig("ops_policy")
	if before != after {
		t.Fatalf("被拒请求不应改动配置: %q → %q", before, after)
	}
}

// TestTaskCenterPolicyGate 任务中心奖励总开关（task.enabled）：
// 关闭后列表置空且领取被拒（发放中台关闸）；开启后恢复正常。
func TestTaskCenterPolicyGate(t *testing.T) {
	st := newTestAPIStore(t)
	s := &Server{Store: st}
	u, err := st.CreateUser(1, "task_user", "x", "普通用户", store.RoleUser, 0, 0)
	if err != nil {
		t.Fatalf("创建用户失败: %v", err)
	}
	bearer := bearerFor(t, u)

	// 关闭任务中心奖励
	if err := st.SetConfig("ops_policy", `{"task":{"enabled":false}}`); err != nil {
		t.Fatalf("写 ops_policy 失败: %v", err)
	}

	// ① 任务列表置空 + disabled 标记
	req := httptest.NewRequest(http.MethodGet, "/api/me/tasks", nil)
	req.Header.Set("Authorization", bearer)
	rec := httptest.NewRecorder()
	s.handleMyTasks(rec, req)
	if rec.Code != 200 {
		t.Fatalf("任务列表应 200，实得 %d", rec.Code)
	}
	var list struct {
		Success  bool          `json:"success"`
		Disabled bool          `json:"disabled"`
		Tasks    []interface{} `json:"tasks"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("响应解析失败: %v", err)
	}
	if !list.Success || !list.Disabled || len(list.Tasks) != 0 {
		t.Fatalf("关闭后应返回空列表 + disabled=true: %s", rec.Body.String())
	}

	// ② 领取被拒
	req2 := httptest.NewRequest(http.MethodPost, "/api/me/tasks/claim", strings.NewReader(`{"id":1}`))
	req2.Header.Set("Authorization", bearer)
	rec2 := httptest.NewRecorder()
	s.handleClaimTask(rec2, req2)
	var claim struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(rec2.Body.Bytes(), &claim); err != nil {
		t.Fatalf("领取响应解析失败: %v", err)
	}
	if claim.Success || claim.Message != "任务奖励暂未开放" {
		t.Fatalf("关闭后领取应被拒: %s", rec2.Body.String())
	}

	// ③ 重新开启后恢复（不再 disabled）
	if err := st.SetConfig("ops_policy", `{"task":{"enabled":true}}`); err != nil {
		t.Fatalf("写 ops_policy 失败: %v", err)
	}
	req3 := httptest.NewRequest(http.MethodGet, "/api/me/tasks", nil)
	req3.Header.Set("Authorization", bearer)
	rec3 := httptest.NewRecorder()
	s.handleMyTasks(rec3, req3)
	var list2 struct {
		Success  bool          `json:"success"`
		Disabled bool          `json:"disabled"`
		Tasks    []interface{} `json:"tasks"`
	}
	if err := json.Unmarshal(rec3.Body.Bytes(), &list2); err != nil {
		t.Fatalf("响应解析失败: %v", err)
	}
	if !list2.Success || list2.Disabled {
		t.Fatalf("开启后不应 disabled: %s", rec3.Body.String())
	}
}

// TestEffPayMode 支付模式收敛：读取点统一走运营策略 payment.mode，缺省回落 mock。
func TestEffPayMode(t *testing.T) {
	st := newTestAPIStore(t)
	s := &Server{Store: st}

	// 缺省（无任何配置）→ mock
	if m := s.effPayMode(1); m != "mock" {
		t.Fatalf("缺省支付模式应为 mock，实得 %q", m)
	}

	// 策略配置 wechat → 全部读取点生效（租户/平台上下文一致）
	if err := st.SetConfig("ops_policy", `{"payment":{"mode":"wechat"}}`); err != nil {
		t.Fatalf("写 ops_policy 失败: %v", err)
	}
	if m := s.effPayMode(1); m != "wechat" {
		t.Fatalf("策略 payment.mode=wechat 应生效，实得 %q", m)
	}
	if m := s.effPayMode(0); m != "wechat" {
		t.Fatalf("平台上下文也应读取同一模式，实得 %q", m)
	}
}
