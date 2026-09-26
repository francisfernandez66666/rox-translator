// ============ tasks_cycle_test.go · 职责说明 ============
// ★ F-60（2026-09-26 〇-U 批 I-8）任务出参「周期两列」的 API 侧等值断言。
//
// 为什么必须有接口锁：本轮 UAT 抓到的正是「界面绿、接口红」——展示层（TaskCenterP）已改读
// period，而 GET /api/me/tasks 的 id2 行仍同时带 `"task_type":"daily"` 与 `"period":"weekly"`，
// 只靠 UI 断言永远抓不到。故本文件把不变量钉在**协议层**：
//
//	① 任一任务行 task_type ≡ period（逐行遍历，不点名 id，防「只测已知的那一行」）；
//	② 出厂周任务在两个接口（超管列表 / 用户列表）上都以 weekly/weekly 出现，
//	   并保留规则列 cap_per_day=1 / cap_per_week=5（订正只收口口径，不得动窗口）；
//	③ 写侧脏组合（表单回写 task_type=daily + period=weekly）经保存后**读回来即自洽**
//	   （F-55 那族「读写不同源」的同款锁）。
//
// 环境：内存 SQLite + 直调 handler（同 ops_gate_test 口径），方言按 AGENTS.md §一·4 钉死 SQLite。
// ========================================
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"translator/internal/store"
)

// cycleRowJSON 任务出参里本文件关心的字段（其余字段由既有测试覆盖）。
type cycleRowJSON struct {
	ID         int64  `json:"id"`
	TaskKey    string `json:"task_key"`
	TaskType   string `json:"task_type"`
	Period     string `json:"period"`
	Title      string `json:"title"`
	GrantMode  string `json:"grant_mode"`
	CapPerDay  int    `json:"cap_per_day"`
	CapPerWeek int    `json:"cap_per_week"`
}

// mustTaskRows 打一个任务列表接口并把 tasks 数组解出来（两接口出参结构一致，共用）。
func mustTaskRows(t *testing.T, body string) []cycleRowJSON {
	t.Helper()
	var resp struct {
		Success bool           `json:"success"`
		Tasks   []cycleRowJSON `json:"tasks"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("解析任务列表失败: %v (%s)", err, body)
	}
	if !resp.Success {
		t.Fatalf("任务列表应 success:true，实际 %s", body)
	}
	if len(resp.Tasks) == 0 {
		t.Fatalf("任务列表不应为空（出厂任务未种入？）: %s", body)
	}
	return resp.Tasks
}

// assertCycleEqual 遍历式等值锁：逐行核 task_type ≡ period，并负向锁「不得出现 daily 别名配 weekly 真值」。
func assertCycleEqual(t *testing.T, where string, rows []cycleRowJSON) {
	t.Helper()
	for _, row := range rows {
		if row.TaskType != row.Period {
			t.Fatalf("%s：任务 id=%d《%s》两列口径矛盾 task_type=%q period=%q（F-60 原形态复现）",
				where, row.ID, row.Title, row.TaskType, row.Period)
		}
	}
	// 负向锁：整个响应体里不得再出现「周任务的 daily 别名」这一对组合
	for _, row := range rows {
		if row.Period == "weekly" && row.TaskType == "daily" {
			t.Fatalf("%s：weekly 行仍带 task_type=daily，外部消费方会算成日任务", where)
		}
	}
}

// TestAdminTasksCycleColumnsConsistent ① + ② 超管列表：逐行自洽，周任务规则列未被订正改动。
func TestAdminTasksCycleColumnsConsistent(t *testing.T) {
	st := taskAPIEnv(t)
	s := &Server{Store: st}
	super, err := st.CreateUser(0, "cycle_super", "x", "超管", store.RoleSuperAdmin, 0, 0)
	if err != nil {
		t.Fatalf("创建超管失败: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/admin/tasks", nil)
	req.Header.Set("Authorization", bearerFor(t, super))
	s.handleAdminTasks(rec, req)
	if rec.Code != 200 {
		t.Fatalf("超管任务列表应 200，实得 %d: %s", rec.Code, rec.Body.String())
	}
	rows := mustTaskRows(t, rec.Body.String())
	assertCycleEqual(t, "GET /api/admin/tasks", rows)
	// 出厂周任务逐字段等值（真值 weekly + 别名对齐 + 规则列不变）
	week := findCycleRow(t, rows, store.TaskKeyTranslateWeek)
	if week.TaskType != "weekly" || week.Period != "weekly" || week.CapPerDay != 1 || week.CapPerWeek != 5 {
		t.Fatalf("周任务出参不符: %+v", week)
	}
}

// TestMyTasksCycleColumnsConsistent ① + ② 用户列表：同一不变量（本轮 UAT 实测的红点就在这条接口上）。
func TestMyTasksCycleColumnsConsistent(t *testing.T) {
	st := taskAPIEnv(t)
	s := &Server{Store: st}
	u, err := st.CreateUser(1, "cycle_member", "x", "会员", store.RoleUser, 0, 0)
	if err != nil {
		t.Fatalf("CreateUser 失败: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/me/tasks", nil)
	req.Header.Set("Authorization", bearerFor(t, u))
	s.handleMyTasks(rec, req)
	if rec.Code != 200 {
		t.Fatalf("用户任务列表应 200，实得 %d: %s", rec.Code, rec.Body.String())
	}
	rows := mustTaskRows(t, rec.Body.String())
	assertCycleEqual(t, "GET /api/me/tasks", rows)
	// 出厂五行逐行都要过等值判据（遍历式锁的覆盖面自检：少解一行等于没锁）
	if len(rows) < 5 {
		t.Fatalf("用户任务列表应含全部出厂任务，实际 %d 行", len(rows))
	}
	for _, wantKey := range []string{store.TaskKeyLoginDaily, store.TaskKeyTranslateWeek, store.TaskKeyKBUpload} {
		if findCycleRow(t, rows, wantKey).TaskType == "" {
			t.Fatalf("行 %s 未回显 task_type", wantKey)
		}
	}
}

// TestAdminTasksCycleLockIsLiveAndSelfHeals 反证 + 自愈链：
// 先把库内周任务改回历史脏组合（task_type='daily' + period='weekly'），
// 遍历判据必须**当场读出这个矛盾**（证明锁的射程真的落在库数据上，不是恒绿摆设）；
// 再随迁移入口 TaskRewardMigrate 跑一次，出参必须自愈为 weekly/weekly 且规则列分毫未动。
func TestAdminTasksCycleLockIsLiveAndSelfHeals(t *testing.T) {
	st := taskAPIEnv(t)
	s := &Server{Store: st}
	super, err := st.CreateUser(0, "cycle_super3", "x", "超管", store.RoleSuperAdmin, 0, 0)
	if err != nil {
		t.Fatalf("创建超管失败: %v", err)
	}
	bearer := bearerFor(t, super)
	list := func() []cycleRowJSON {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/admin/tasks", nil)
		req.Header.Set("Authorization", bearer)
		s.handleAdminTasks(rec, req)
		return mustTaskRows(t, rec.Body.String())
	}
	weekID := cycleTaskID(t, st, store.TaskKeyTranslateWeek)
	if _, e := st.DB().Exec("UPDATE user_tasks SET task_type='daily' WHERE id=" + itoaInt64(weekID)); e != nil {
		t.Fatalf("构造历史脏行失败: %v", e)
	}
	dirty := findCycleRow(t, list(), store.TaskKeyTranslateWeek)
	if dirty.TaskType == dirty.Period {
		t.Fatal("库内矛盾行未被出参照出⇒遍历判据射程没覆盖真实数据（假绿）")
	}
	// 自愈：迁移入口末尾的 RepairTaskCycleColumns（发版换二进制即自动执行，无需人工 SQL）
	st.TaskRewardMigrate()
	healed := findCycleRow(t, list(), store.TaskKeyTranslateWeek)
	if healed.TaskType != "weekly" || healed.Period != "weekly" || healed.CapPerDay != 1 || healed.CapPerWeek != 5 {
		t.Fatalf("自愈后出参不符: %+v", healed)
	}
	assertCycleEqual(t, "自愈后 /api/admin/tasks", list())
}

// TestTaskSaveHealsDivergentFormPair ③ 写侧脏组合：表单把旧别名一起回写，读回来必须已自洽。
func TestTaskSaveHealsDivergentFormPair(t *testing.T) {
	st := taskAPIEnv(t)
	s := &Server{Store: st}
	super, err := st.CreateUser(0, "cycle_super2", "x", "超管", store.RoleSuperAdmin, 0, 0)
	if err != nil {
		t.Fatalf("创建超管失败: %v", err)
	}
	bearer := bearerFor(t, super)
	weekID := cycleTaskID(t, st, store.TaskKeyTranslateWeek)
	// 模拟超管面板带着历史 task_type='daily' 保存周任务（真值仍是 weekly，规则列照抄库内值）
	saveBody := `{"id":` + itoaInt64(weekID) + `,"task_type":"daily","title":"每周发起翻译","reward_points":100,` +
		`"grant_mode":"auto","period":"weekly","valid_days":7,"stack_expiry":1,"cap_per_day":1,"cap_per_week":5,"enabled":1}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/admin/tasks/save", strings.NewReader(saveBody))
	req.Header.Set("Authorization", bearer)
	s.handleAdminTaskSave(rec, req)
	if rec.Code != 200 {
		t.Fatalf("保存应 200，实得 %d: %s", rec.Code, rec.Body.String())
	}
	if g := st.GetUserTask(weekID); g == nil || g.TaskType != "weekly" || g.Period != "weekly" {
		t.Fatalf("保存后库内仍双口径: %+v", g)
	}
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/api/admin/tasks", nil)
	req2.Header.Set("Authorization", bearer)
	s.handleAdminTasks(rec2, req2)
	assertCycleEqual(t, "保存后重取 /api/admin/tasks", mustTaskRows(t, rec2.Body.String()))
}

// findCycleRow 按 task_key 取一行（不依赖自增 id；手工任务 task_key 为空故不参与）。
func findCycleRow(t *testing.T, rows []cycleRowJSON, key string) cycleRowJSON {
	t.Helper()
	for _, r := range rows {
		if r.TaskKey == key {
			return r
		}
	}
	t.Fatalf("出参缺少 task_key=%s 的任务行: %+v", key, rows)
	return cycleRowJSON{}
}

// cycleTaskID 按 task_key 取库内任务 id。
func cycleTaskID(t *testing.T, st *store.Store, key string) int64 {
	t.Helper()
	for _, tk := range st.ListUserTasks() {
		if tk.TaskKey == key {
			return tk.ID
		}
	}
	t.Fatalf("库内缺少任务 %s", key)
	return 0
}
