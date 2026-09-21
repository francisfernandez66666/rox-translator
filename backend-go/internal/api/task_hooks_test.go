// ============ task_hooks_test.go · 职责说明 ============
// api 包「任务系统」（需求 #33，2026-09-21）HTTP 层自动化断言：
//   - TestTaskLoginHookGrants：登录钩子发 100 临时积分、当日二次判重、平台关闸零发放；
//   - TestTaskTranslateHookViaAuth：翻译入口经登录态发奖（日 ≤1 次）；
//   - TestTaskMyTasksAutoViewShape：/api/me/tasks 出参带 task_key/grant_mode/有效期与周期进度；
//   - TestTaskAdminSaveRoundTrip：超管保存回写事件发放字段（缺省回落原值，不被清零）；
//   - TestTaskAdminResetConsumption：特殊任务「重置消耗量」——仅超管可调、有效期不变、幂等。
//
// 环境：内存 SQLite + 直调 handler（同 ops_gate_test 口径），方言按 AGENTS.md §4 钉死 SQLite。
// =============================================
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"translator/internal/config"
	"translator/internal/store"
)

// taskAPIEnv 钉死 SQLite 方言并返回完整 schema 的测试 Store。
func taskAPIEnv(t *testing.T) *store.Store {
	t.Helper()
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })
	return newTestAPIStore(t)
}

// TestTaskLoginHookGrants 登录事件钩子：100 临时积分入台账、当日二次判重、平台关闸后零发放。
func TestTaskLoginHookGrants(t *testing.T) {
	st := taskAPIEnv(t)
	s := &Server{Store: st}
	u, err := st.CreateUser(1, "hook_login", "x", "登录用户", store.RoleUser, 0, 0)
	if err != nil {
		t.Fatalf("CreateUser 失败: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/login", nil)

	res := s.grantTaskEventOnTenant(req, u.TenantID, u.ID, store.TaskKeyLoginDaily, "")
	if !res.Granted || res.Points != 100 || res.ValidDays != 3 {
		t.Fatalf("登录应发放 100 临时积分（3 天）: %+v", res)
	}
	if got := st.SumActiveGrants(u.TenantID); got != st.TokensFromPoints(100) {
		t.Fatalf("未过期台账合计应 %d，实际 %d", st.TokensFromPoints(100), got)
	}
	if again := s.grantTaskEventOnTenant(req, u.TenantID, u.ID, store.TaskKeyLoginDaily, ""); again.Granted {
		t.Fatalf("当日二次登录应判重: %+v", again)
	}
	if n := st.SumActiveGrants(u.TenantID); n != st.TokensFromPoints(100) {
		t.Fatalf("判重不得二次入账，实际台账合计 %d", n)
	}
	// 平台关闸：旁路零副作用（不产生台账，也不报错）
	if err := st.SetConfig("ops_policy", `{"task":{"enabled":false}}`); err != nil {
		t.Fatalf("写 ops_policy 失败: %v", err)
	}
	s.invalidatePolicyCache()
	if gated := s.grantTaskEventOnTenant(req, u.TenantID, u.ID, store.TaskKeyTranslateWeek, ""); gated.Granted {
		t.Fatalf("关闸后不得发放: %+v", gated)
	}
	if n := st.SumActiveGrants(u.TenantID); n != st.TokensFromPoints(100) {
		t.Fatalf("关闸后台账合计应仍为 %d，实际 %d", st.TokensFromPoints(100), n)
	}
}

// myTaskViewJSON /api/me/tasks 单条出参（仅取本文件关心的字段）。
type myTaskViewJSON struct {
	ID           int64  `json:"id"`
	TaskKey      string `json:"task_key"`
	GrantMode    string `json:"grant_mode"`
	Period       string `json:"period"`
	ValidDays    int    `json:"valid_days"`
	RewardKind   string `json:"reward_kind"`
	RewardPoints int64  `json:"reward_points"`
	Claimed      bool   `json:"claimed"`
	Reward       *struct {
		TodayCount int64 `json:"today_count"`
		WeekCount  int64 `json:"week_count"`
		TotalCount int64 `json:"total_count"`
	} `json:"reward"`
}

// TestTaskTranslateHookAndMyView 翻译事件钩子（经登录态）+ 用户任务列表出参形状。
func TestTaskTranslateHookAndMyView(t *testing.T) {
	st := taskAPIEnv(t)
	s := &Server{Store: st}
	u, err := st.CreateUser(1, "hook_translate", "x", "翻译用户", store.RoleUser, 0, 0)
	if err != nil {
		t.Fatalf("CreateUser 失败: %v", err)
	}
	bearer := bearerFor(t, u)
	req := httptest.NewRequest(http.MethodPost, "/api/translate/stream", nil)
	req.Header.Set("Authorization", bearer)

	s.grantTranslateTask(req, u.TenantID)
	if n := st.SumActiveGrants(u.TenantID); n != st.TokensFromPoints(100) {
		t.Fatalf("首次翻译应发 100 临时积分，实际台账 %d", n)
	}
	s.grantTranslateTask(req, u.TenantID) // 同日二次：只一笔
	if n := st.SumActiveGrants(u.TenantID); n != st.TokensFromPoints(100) {
		t.Fatalf("同日二次翻译应判重，实际台账 %d", n)
	}

	// 用户视角出参：自动任务带 task_key/grant_mode/有效期/进度，且对外只有积分口径
	rec := httptest.NewRecorder()
	listReq := httptest.NewRequest(http.MethodGet, "/api/me/tasks", nil)
	listReq.Header.Set("Authorization", bearer)
	s.handleMyTasks(rec, listReq)
	if rec.Code != 200 {
		t.Fatalf("任务列表应 200，实得 %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Success bool             `json:"success"`
		Tasks   []myTaskViewJSON `json:"tasks"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析失败: %v (%s)", err, rec.Body.String())
	}
	var translate, login *myTaskViewJSON
	for i := range body.Tasks {
		switch body.Tasks[i].TaskKey {
		case store.TaskKeyTranslateWeek:
			translate = &body.Tasks[i]
		case store.TaskKeyLoginDaily:
			login = &body.Tasks[i]
		}
	}
	if translate == nil || login == nil {
		t.Fatalf("出参缺少出厂任务: %s", rec.Body.String())
	}
	if translate.GrantMode != "auto" || translate.Period != "weekly" || translate.ValidDays != 7 || translate.RewardKind != "temporary" {
		t.Fatalf("发起翻译任务元数据不符: %+v", translate)
	}
	if translate.Reward == nil || translate.Reward.TodayCount != 1 || translate.Reward.WeekCount != 1 || translate.Reward.TotalCount != 1 {
		t.Fatalf("发起翻译周期进度不符: %+v", translate.Reward)
	}
	if !translate.Claimed {
		t.Fatal("本周已发放的翻译任务应标记已达成")
	}
	if login.RewardPoints != 100 || translate.RewardPoints != 100 {
		t.Fatalf("出参积分口径不符: login=%d translate=%d", login.RewardPoints, translate.RewardPoints)
	}
	if strings.Contains(rec.Body.String(), "reward_tokens") {
		t.Fatal("出参不得含 reward_tokens（对外零 token 口径）")
	}
	if login.Claimed {
		t.Fatal("未发放的每日登录任务不应标记已达成")
	}
}

// TestTaskAdminSaveRoundTrip 超管保存事件任务：新字段落库并回显；缺省字段回落原值不被清零。
func TestTaskAdminSaveRoundTrip(t *testing.T) {
	st := taskAPIEnv(t)
	s := &Server{Store: st}
	super, err := st.CreateUser(0, "task_super", "x", "超管", store.RoleSuperAdmin, 0, 0)
	if err != nil {
		t.Fatalf("创建超管失败: %v", err)
	}
	bearer := bearerFor(t, super)

	// 新建一条事件任务
	saveBody := `{"task_type":"daily","title":"自定义事件任务","description":"desc","reward_points":50,` +
		`"task_key":"custom_event","grant_mode":"auto","period":"event","valid_days":5,"stack_expiry":0,"cap_per_day":2,"cap_per_week":0,"enabled":1}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/admin/tasks/save", strings.NewReader(saveBody))
	req.Header.Set("Authorization", bearer)
	s.handleAdminTaskSave(rec, req)
	if rec.Code != 200 {
		t.Fatalf("保存应 200，实得 %d: %s", rec.Code, rec.Body.String())
	}
	var saved struct {
		Success bool   `json:"success"`
		ID      int64  `json:"id"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &saved); err != nil || !saved.Success {
		t.Fatalf("保存响应异常: %s", rec.Body.String())
	}
	got := st.GetUserTask(saved.ID)
	if got == nil {
		t.Fatal("保存后读不回任务")
	}
	if got.TaskKey != "custom_event" || got.GrantMode != "auto" || got.Period != "event" || got.ValidDays != 5 ||
		got.StackExpiry != 0 || got.CapPerDay != 2 {
		t.Fatalf("事件发放字段未落库: %+v", got)
	}
	if st.PointsFromTokens(got.RewardTokens) != 50 {
		t.Fatalf("奖励应为 50 积分口径，实际 %d token", got.RewardTokens)
	}
	// 只带 id/title/reward_points 更新：事件字段必须回落原值（否则超管改个标题就把任务规则清了）
	updateBody := `{"id":` + itoaInt64(saved.ID) + `,"task_type":"daily","title":"改名","reward_points":80}`
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/api/admin/tasks/save", strings.NewReader(updateBody))
	req2.Header.Set("Authorization", bearer)
	s.handleAdminTaskSave(rec2, req2)
	if rec2.Code != 200 {
		t.Fatalf("二次保存应 200，实得 %d: %s", rec2.Code, rec2.Body.String())
	}
	after := st.GetUserTask(saved.ID)
	if after == nil {
		t.Fatal("二次保存后读不回任务")
	}
	if after.Title != "改名" || st.PointsFromTokens(after.RewardTokens) != 80 {
		t.Fatalf("标题/额度未更新: %+v", after)
	}
	if after.TaskKey != "custom_event" || after.GrantMode != "auto" || after.Period != "event" ||
		after.ValidDays != 5 || after.StackExpiry != 0 || after.CapPerDay != 2 {
		t.Fatalf("缺省字段应回落原值，实际被清零: %+v", after)
	}
	if after.Enabled != 1 {
		t.Fatalf("enabled 缺省应回落原值 1，实际 %d", after.Enabled)
	}
	// 事件任务已发放后仍不可人工领取（防双发，见 store 层断言）
	if ok, _ := st.ClaimUserTask(super.ID, super.TenantID, saved.ID); ok {
		t.Fatal("auto 任务不得走人工领取")
	}
}

// TestTaskAdminResetConsumption ★#33 特殊任务：超管手动重置消耗量（仅超管、有效期不变、幂等）。
func TestTaskAdminResetConsumption(t *testing.T) {
	st := taskAPIEnv(t)
	s := &Server{Store: st}
	super, err := st.CreateUser(0, "reset_super", "x", "超管", store.RoleSuperAdmin, 0, 0)
	if err != nil {
		t.Fatalf("创建超管失败: %v", err)
	}
	tadmin, err := st.CreateUser(1, "reset_tadmin", "x", "租管", store.RoleTenantAdmin, 0, 0)
	if err != nil {
		t.Fatalf("创建租管失败: %v", err)
	}
	user, err := st.CreateUser(1, "reset_user", "x", "用户", store.RoleUser, 0, 0)
	if err != nil {
		t.Fatalf("CreateUser 失败: %v", err)
	}
	// 普通用户同样无权限
	recU := httptest.NewRecorder()
	reqU := httptest.NewRequest(http.MethodPost, "/api/admin/tasks/reset-consumption", strings.NewReader(`{}`))
	reqU.Header.Set("Authorization", bearerFor(t, user))
	s.handleAdminTaskResetConsumption(recU, reqU)
	if recU.Code != 403 {
		t.Fatalf("普通用户调用重置应 403，实得 %d: %s", recU.Code, recU.Body.String())
	}
	// 租户 1：有未过期订阅 + 一条消耗完的任务台账
	exp := time.Now().UTC().Add(20 * 24 * time.Hour)
	if err := st.CreateQuotaGrant(1, "plan", 500000, exp, "order", 0); err != nil {
		t.Fatalf("种入订阅台账失败: %v", err)
	}
	taskExp := time.Now().UTC().Add(3 * 24 * time.Hour)
	if err := st.CreateQuotaGrant(1, store.TaskGrantKind, st.TokensFromPoints(100), taskExp, "task:login_daily", 0); err != nil {
		t.Fatalf("种入任务台账失败: %v", err)
	}
	if err := st.DeductWithGrants(1, st.TokensFromPoints(100)); err != nil {
		t.Fatalf("消耗任务台账失败: %v", err)
	}
	if got := st.SumActiveGrants(1); got != 500000 {
		t.Fatalf("预置后应只剩订阅台账 500000，实际 %d", got)
	}

	// ① 权限：未登录 403、租管 403、超管放行
	for name, hdr := range map[string]string{"未登录": "", "租管": bearerFor(t, tadmin)} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/admin/tasks/reset-consumption", strings.NewReader(`{}`))
		if hdr != "" {
			req.Header.Set("Authorization", hdr)
		}
		s.handleAdminTaskResetConsumption(rec, req)
		if rec.Code != 403 {
			t.Fatalf("%s 调用重置应 403，实得 %d: %s", name, rec.Code, rec.Body.String())
		}
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/admin/tasks/reset-consumption", strings.NewReader(`{"subscribed_only":true}`))
	req.Header.Set("Authorization", bearerFor(t, super))
	s.handleAdminTaskResetConsumption(rec, req)
	if rec.Code != 200 {
		t.Fatalf("超管调用应 200，实得 %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Success   bool   `json:"success"`
		Message   string `json:"message"`
		Tenants   int64  `json:"tenants"`
		ResetRows int64  `json:"reset_rows"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || !out.Success {
		t.Fatalf("重置响应异常: %s", rec.Body.String())
	}
	if out.Tenants != 1 || out.ResetRows != 1 {
		t.Fatalf("应重置 1 个订阅租户的 1 条任务台账，实际 %+v", out)
	}
	if !strings.Contains(out.Message, "有效期保持不变") {
		t.Fatalf("提示文案应说明有效期不变，实际 %q", out.Message)
	}
	if got := st.SumActiveGrants(1); got != 500000+st.TokensFromPoints(100) {
		t.Fatalf("重置后任务临时积分应回到 %d，实际合计 %d", st.TokensFromPoints(100), got)
	}
	// 有效期不改写
	var expiry string
	if err := st.DB().QueryRow(`SELECT expires_at FROM quota_grants WHERE tenant_id=1 AND kind='task'`).Scan(&expiry); err != nil {
		t.Fatalf("读任务台账到期日失败: %v", err)
	}
	if expiry != taskExp.Format(time.RFC3339) {
		t.Fatalf("有效期不得被改写: %s ≠ %s", expiry, taskExp.Format(time.RFC3339))
	}
	// ② 幂等：再调一次无行可改
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/api/admin/tasks/reset-consumption", strings.NewReader(`{}`))
	req2.Header.Set("Authorization", bearerFor(t, super))
	s.handleAdminTaskResetConsumption(rec2, req2)
	var out2 struct {
		Tenants   int64 `json:"tenants"`
		ResetRows int64 `json:"reset_rows"`
	}
	if err := json.Unmarshal(rec2.Body.Bytes(), &out2); err != nil {
		t.Fatalf("二次解析失败: %s", rec2.Body.String())
	}
	if out2.ResetRows != 0 || out2.Tenants != 0 {
		t.Fatalf("重复重置应 0 行 0 租户，实际 %+v", out2)
	}
}
