// ============ tasks_cycle_test.go · 职责说明 ============
// ★ F-60（2026-09-26 〇-U 批 I-8）任务「周期两列」单口径的三条锁。
//
// 原缺陷：出厂周任务（id2「每周发起翻译」）两列互相矛盾——`task_type='daily'` 与
// `period='weekly'` 并存，展示层已改读 period（界面回「每周」），接口出参照旧带 daily，
// 于是「界面绿、接口红」：任何按 task_type 分支的消费方（SDK/报表）会把周任务算成日任务。
//
// 修法把真值收敛到 period，并让 task_type 成为它的恒等别名（store.normalizeTaskCycle +
// RepairTaskCycleColumns）。本文件按《缺陷核实与修复文档》8.6 的**前置要求**取证：
//
//	① 【订正前置·周任务领取回归】task_type 从 'daily' 改成 'weekly' 前后，
//	   「每天 1 次 / 每周 5 次」必须逐笔一致——绝不能把「每周 5 次」放大成「每天 5 次」
//	   （那是账务级事故），也不能把「每天 1 次」收紧成「每周 1 次」；
//	② 【存量订正方向】auto 行只动别名（period/cap_* 一列不碰）；手工行只动 period
//	   （领取幂等键仍由 task_type 决定 ⇒「终身一次」不会被放大成「每日一次」）；幂等复跑 0 行；
//	③ 【写侧归一】SaveUserTask 在表单回写脏组合（task_type='once' + period='daily'）时
//	   仍按手工真值归一，端到端二次领取必被拒。
//
// 方言：固定 SQLite 内存库，并显式钉死 config.C（AGENTS.md §一·4：run_uat 的 PG 模式会把
// 方言泄漏给同包内存库用例）。
// ========================================
package store

import (
	"testing"

	"translator/internal/config"
	"translator/internal/db"
)

// cycleEnv 独立内存库环境（每调一次起一套新库，供前后对照用）。
func cycleEnv(t *testing.T, tag string) (*Store, *User) {
	t.Helper()
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })
	st := newTestStoreWithTenants(t)
	u, err := st.CreateUser(1, "cycle_"+tag, "hash", "周期回归用户", RoleUser, 0, 0)
	if err != nil {
		t.Fatalf("CreateUser 失败: %v", err)
	}
	return st, u
}

// TestWeeklyGrantWindowIndependentOfLegacyAlias ① 周任务发放窗口与遗留别名取值无关（订正前置回归）。
// 同一套出厂数据，在 task_type='daily'（订正前）与 'weekly'（订正后）两种取值下
// 跑同一条 6 笔序列，逐笔结果必须一致：同日判重、周内 5 笔放行、第 6 笔 capped_week。
func TestWeeklyGrantWindowIndependentOfLegacyAlias(t *testing.T) {
	for _, legacy := range []string{"daily", "weekly"} {
		t.Run("task_type="+legacy, func(t *testing.T) {
			st, u := cycleEnv(t, legacy)
			// 只改别名一列，模拟订正前后的两种库内取值
			grantTaskForTest(t, st, TaskKeyTranslateWeek, "task_type=?", legacy)
			tk := findTask(t, st, TaskKeyTranslateWeek)
			// 前置守卫：真值三列必须逐字没动（改动若溢出到 period/cap_*，后面的对照就无意义）
			if tk.TaskType != legacy || tk.Period != "weekly" || tk.CapPerDay != 1 || tk.CapPerWeek != 5 {
				t.Fatalf("别名订正溢出到真值列: task_type=%s period=%s capDay=%d capWeek=%d",
					tk.TaskType, tk.Period, tk.CapPerDay, tk.CapPerWeek)
			}
			// 手工领取侧：auto 任务在两种别名下一律拒领（不会因订正开出第二条发放路径＝双发）
			if ok, _ := st.ClaimUserTask(u.ID, u.TenantID, tk.ID); ok {
				t.Fatalf("task_type=%s 时自动任务仍可人工领取（双发风险）", legacy)
			}
			// 第 1 笔 + 同日第 2 笔：「每天 1 次」既没被放宽，也没被收紧成「每周 1 次」
			if first := st.GrantTaskReward(u.ID, TaskKeyTranslateWeek, ""); !first.Granted {
				t.Fatalf("本周首笔应发放: %+v", first)
			}
			if dup := st.GrantTaskReward(u.ID, TaskKeyTranslateWeek, ""); dup.Granted || dup.Reason != "duplicate" {
				t.Fatalf("同日二次应判重（每日上限 1 次未随别名改变）: %+v", dup)
			}
			rotateTaskRewardDay(t, st, u.ID, tk.ID, 1) // 换到「周内次日」（周计数器保持累计）
			var granted int64 = 1
			var reasons []string
			for i := 2; i <= 6; i++ {
				res := st.GrantTaskReward(u.ID, TaskKeyTranslateWeek, "")
				reasons = append(reasons, res.Reason)
				switch {
				case res.Granted:
					granted++
					rotateTaskRewardDay(t, st, u.ID, tk.ID, i)
				case res.Reason == "capped_week": // 预期：第 6 笔触顶
				default:
					t.Fatalf("第 %d 次发放返回意外结果: %+v", i, res)
				}
			}
			// 等值锁：每周 5 次——既没被算成「每天 5 次」（granted=6），也没被算成「每周 1 次」（granted=1）
			if granted != 5 {
				t.Fatalf("task_type=%s 时周内应发 5 次，实际 %d 次（各笔 reason=%v）", legacy, granted, reasons)
			}
			stat := st.TaskRewardStatOf(u.ID, tk.ID)
			if stat.WeekCount != 5 {
				t.Fatalf("周进度应为 5，实际 %+v", stat)
			}
			if cnt := taskCounterOf(t, st, u.ID, tk.ID); cnt != 5 {
				t.Fatalf("周计数器应停在 5（不得击穿），实际 %d", cnt)
			}
		})
	}
}

// TestClaimDateKeyIgnoresNewCycleValues 领取幂等键判据锁：claimDateKey 只认 'once'，
// 新增的 'weekly'/'event' 取值必须落进「按日」分支（否则终身一次会被放大成每日一次）。
func TestClaimDateKeyIgnoresNewCycleValues(t *testing.T) {
	today := "2026-09-26"
	cases := map[string]string{"daily": today, "weekly": today, "event": today, "once": "once", "": today}
	for taskType, want := range cases {
		if got := (&UserTask{TaskType: taskType}).claimDateKey(today); got != want {
			t.Fatalf("task_type=%q 的领取键应为 %q，实际 %q", taskType, want, got)
		}
	}
}

// TestRepairTaskCycleColumns ② 存量行订正：两条方向各自只动「非真值」那一列，且幂等。
func TestRepairTaskCycleColumns(t *testing.T) {
	st, _ := cycleEnv(t, "repair")
	// 新库出厂即已收敛（种入写 task_type=period + 迁移末尾再跑一遍订正）
	if n, e := st.RepairTaskCycleColumns(); e != nil || n != 0 {
		t.Fatalf("已收敛的库重复订正应改 0 行，实际 rows=%d err=%v", n, e)
	}
	// 造两条历史脏行（绕过 SaveUserTask 直接写库，模拟 #33 半修状态）
	weekID := findTask(t, st, TaskKeyTranslateWeek).ID
	if _, e := db.Exec(st.db, db.CurrentDialect(), "UPDATE user_tasks SET task_type='daily' WHERE id=?", weekID); e != nil {
		t.Fatalf("构造脏 auto 行失败: %v", e)
	}
	manualOnce := &UserTask{TaskType: "once", Title: "终身一次（历史行）", RewardTokens: st.TokensFromPoints(30), Enabled: 1}
	manualID, e := st.SaveUserTask(manualOnce)
	if e != nil {
		t.Fatalf("建手工任务失败: %v", e)
	}
	// 手工行按「旧口径」退回 period='daily'（#33 补列时 default 灌进来的正是这个值）
	if _, e := db.Exec(st.db, db.CurrentDialect(), "UPDATE user_tasks SET period='daily' WHERE id=?", manualID); e != nil {
		t.Fatalf("构造脏手工行失败: %v", e)
	}
	changed, err := st.RepairTaskCycleColumns()
	if err != nil {
		t.Fatalf("RepairTaskCycleColumns 失败: %v", err)
	}
	if changed != 2 {
		t.Fatalf("应订正 2 行，实际 %d", changed)
	}
	// auto 行：只把别名拉齐，period 与两条上限逐字不变 ⇒ 发放窗口不可能被这次订正改动
	aw := findTask(t, st, TaskKeyTranslateWeek)
	if aw.TaskType != "weekly" || aw.Period != "weekly" || aw.CapPerDay != 1 || aw.CapPerWeek != 5 {
		t.Fatalf("auto 行订正不符（应只动 task_type）: task_type=%s period=%s capDay=%d capWeek=%d",
			aw.TaskType, aw.Period, aw.CapPerDay, aw.CapPerWeek)
	}
	// 手工行：只把 period 拉齐，task_type 仍是 'once' ⇒「终身一次」没被放大成「每日一次」
	am := st.GetUserTask(manualID)
	if am == nil {
		t.Fatal("手工任务应仍在库")
	}
	if am.TaskType != "once" || am.Period != "once" {
		t.Fatalf("手工 once 行订正不符（应只动 period）: task_type=%s period=%s", am.TaskType, am.Period)
	}
	// 幂等：再跑一遍必须 0 行（迁移随启动执行，不得反复改库）
	if n2, e2 := st.RepairTaskCycleColumns(); e2 != nil || n2 != 0 {
		t.Fatalf("重复订正应改 0 行，实际 rows=%d err=%v", n2, e2)
	}
	// 全局不变量：库内任意一行都不得再有两口径矛盾
	for _, row := range st.ListUserTasks() {
		if row.TaskType != row.Period {
			t.Fatalf("任务 id=%d 仍双口径矛盾: task_type=%s period=%s", row.ID, row.TaskType, row.Period)
		}
	}
}

// TestSaveUserTaskNeverWidensManualClaimWindow ③ 写侧归一：表单回写脏组合时按真值分流，
// 手工任务的领取窗口只可能保持或收紧，绝不被 period 反向放大。
func TestSaveUserTaskNeverWidensManualClaimWindow(t *testing.T) {
	st, u := cycleEnv(t, "write")
	// 超管表单对历史一次性手工任务的回写：task_type='once' + period='daily'（表单默认值）
	id, err := st.SaveUserTask(&UserTask{TaskType: "once", Period: "daily", Title: "终身一次（手工）",
		RewardTokens: st.TokensFromPoints(30), Enabled: 1})
	if err != nil {
		t.Fatalf("SaveUserTask 失败: %v", err)
	}
	tk := st.GetUserTask(id)
	if tk == nil || tk.TaskType != "once" || tk.Period != "once" {
		t.Fatalf("手工 once 行应归一为 once/once，实际 %+v", tk)
	}
	// 端到端：首笔可领、同日二次必拒（窗口=终身一次的取证）
	if ok, pts := st.ClaimUserTask(u.ID, u.TenantID, id); !ok || pts != st.TokensFromPoints(30) {
		t.Fatalf("手工一次性任务首笔应可领取，实际 ok=%v pts=%d", ok, pts)
	}
	if ok, _ := st.ClaimUserTask(u.ID, u.TenantID, id); ok {
		t.Fatal("手工一次性任务二次领取应被拒（被放大成每日一次即本条红灯）")
	}
	// auto 行反向：表单带旧别名 task_type='daily' + period='weekly' → 落库以 period 为准
	weekID := findTask(t, st, TaskKeyTranslateWeek).ID
	if _, e := st.SaveUserTask(&UserTask{ID: weekID, TaskType: "daily", Period: "weekly", GrantMode: "auto",
		Title: "每周发起翻译", RewardTokens: st.TokensFromPoints(100), Enabled: 1, ValidDays: 7, CapPerDay: 1, CapPerWeek: 5}); e != nil {
		t.Fatalf("更新周任务失败: %v", e)
	}
	aw := st.GetUserTask(weekID)
	if aw.TaskType != "weekly" || aw.Period != "weekly" {
		t.Fatalf("auto 行应以 period 为真值，实际 task_type=%s period=%s", aw.TaskType, aw.Period)
	}
	if aw.CapPerDay != 1 || aw.CapPerWeek != 5 || aw.ValidDays != 7 {
		t.Fatalf("归一不得动规则列: capDay=%d capWeek=%d validDays=%d", aw.CapPerDay, aw.CapPerWeek, aw.ValidDays)
	}
}

// TestNormalizeTaskCycleTable 纯函数判定表（两列恒等 + 手工行只认 once/daily 两个值）。
func TestNormalizeTaskCycleTable(t *testing.T) {
	type c struct {
		mode, taskType, period, wantType, wantPeriod string
	}
	cases := []c{
		{"auto", "daily", "weekly", "weekly", "weekly"}, // 本轮 UAT 的 id2 形态：别名随真值
		{"auto", "once", "event", "event", "event"},     // 邀请类：两列同为 event
		{"auto", "once", "", "once", "once"},            // period 缺省：先由旧列反推，不误降 daily
		{"auto", "", "daily", "daily", "daily"},         // 两列都缺 → daily
		{"manual", "once", "daily", "once", "once"},     // ★ 危险方向：period 不得把终身一次放大成每日
		{"manual", "daily", "weekly", "daily", "daily"}, // 手工无周窗口：period 归一到实际行为 daily
		{"manual", "weekly", "", "daily", "daily"},      // 旧列非法值 → 落 daily（不新增第三种手工窗口）
	}
	for i, cs := range cases {
		tk := &UserTask{GrantMode: cs.mode, TaskType: cs.taskType, Period: cs.period}
		normalizeTaskCycle(tk)
		if tk.TaskType != cs.wantType || tk.Period != cs.wantPeriod {
			t.Fatalf("判定表第 %d 条不符: mode=%s 入(%s,%s) 出(%s,%s) 期望(%s,%s)",
				i+1, cs.mode, cs.taskType, cs.period, tk.TaskType, tk.Period, cs.wantType, cs.wantPeriod)
		}
		if tk.TaskType != tk.Period {
			t.Fatalf("判定表第 %d 条破坏两列恒等不变量: %s ≠ %s", i+1, tk.TaskType, tk.Period)
		}
	}
}

// TestTaskViewsCarryCanonicalCycle 用户/超管视图读侧：period 与 task_type 取自同一行且必等
// （出参层等值锁的 store 半，接口半见 api 包 tasks_cycle_test.go）。
func TestTaskViewsCarryCanonicalCycle(t *testing.T) {
	st, u := cycleEnv(t, "views")
	if _, e := st.SaveUserTask(&UserTask{TaskType: "daily", Title: "手工每日", RewardTokens: st.TokensFromPoints(10), Enabled: 1}); e != nil {
		t.Fatalf("建任务失败: %v", e)
	}
	views := st.ListUserTaskViews(u.ID)
	if len(views) == 0 {
		t.Fatal("用户视图不应为空")
	}
	for _, v := range views {
		if v.TaskType != v.Period {
			t.Fatalf("视图行 id=%d 两列矛盾: task_type=%s period=%s", v.ID, v.TaskType, v.Period)
		}
	}
}
