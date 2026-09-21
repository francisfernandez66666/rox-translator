// ============ task_rewards_test.go · 职责说明 ============
// 「任务系统」（需求 #33，2026-09-21 用户口径）数据层自动化断言。
// 逐条钉死用户原文数值口径，防止后续改动悄悄改掉发放规则：
//
//	① 每日登录 100 临时积分：一日一次、有效期 3 天、日叠加；
//	② 每周发起翻译 100 临时积分：每天 1 次 / 每周 5 次上限、有效期 7 天、周叠加；
//	③ 邀请好友注册 +500 临时积分（14 天、按好友去重、可叠加）；
//	④ 邀请好友且任意充值 +1000 永久积分（可叠加）；
//	⑤ 上传自有知识库并解析成功 +600 永久积分（一次性不叠加）；
//	⑥ 特殊：超管手动「重置已订阅全部用户的积分消耗量」——消耗完的也重置、有效期不变。
//
// 同时断言四条不变量：临时积分只进 quota_grants(kind='task')、永久积分只进
// balance_accounts.balance；扣减顺序「先到期优先」；发放中台关闸（ops_policy.task.enabled=false）零发放。
//
// 方言：本文件全部用例固定 SQLite 内存库，并显式钉死 config.C（AGENTS.md §4：
// run_uat 的 PG 模式下 env DB_DRIVER=postgres 会泄漏方言给同包内存库用例）。
// =============================================
package store

import (
	"strconv"
	"testing"
	"time"

	"translator/internal/config"
	"translator/internal/db"
)

// taskEnv 建立任务系统测试环境：租户 1（newTestStoreWithTenants 已建，is_personal=1）+ 一个用户。
// 返回的 Store 已随 New() 跑完 TaskRewardMigrate（出厂任务已种入）。
func taskEnv(t *testing.T) (*Store, *User) {
	t.Helper()
	// ★ 钉死方言（config.Default() 有写全局副作用，须先存旧值再 Cleanup 还原）
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })

	st := newTestStoreWithTenants(t)
	u, err := st.CreateUser(1, "task_user", "hash", "任务测试员", RoleUser, 0, 0)
	if err != nil {
		t.Fatalf("CreateUser 失败: %v", err)
	}
	return st, u
}

// grantTaskForTest 直接改任务定义（仅测试用：模拟超管调额/停用/改上限）。
func grantTaskForTest(t *testing.T, st *Store, taskKey, set string, args ...interface{}) {
	t.Helper()
	args = append(args, taskKey) // WHERE task_key=? 的实参固定在末尾
	if _, err := db.Exec(st.db, db.CurrentDialect(), "UPDATE user_tasks SET "+set+" WHERE task_key=?", args...); err != nil {
		t.Fatalf("调整任务 %s 失败: %v", taskKey, err)
	}
}

// sumTaskGrants 统计租户未过期任务临时积分台账合计与最晚到期日。
func sumTaskGrants(t *testing.T, st *Store, tid int64) (total int64, latest string) {
	t.Helper()
	err := db.QueryRow(st.db, db.CurrentDialect(),
		`SELECT COALESCE(SUM("left"),0), COALESCE(MAX(expires_at),'') FROM quota_grants
		 WHERE tenant_id=? AND kind=? AND "left">0 AND expires_at>?`,
		tid, TaskGrantKind, time.Now().UTC().Format(time.RFC3339)).Scan(&total, &latest)
	if err != nil {
		t.Fatalf("统计任务台账失败: %v", err)
	}
	return
}

// taskPermanentBalance 读租户永久余额。
func taskPermanentBalance(t *testing.T, st *Store, tid int64) int64 {
	t.Helper()
	if err := st.EnsureBalance(tid); err != nil {
		t.Fatalf("EnsureBalance 失败: %v", err)
	}
	b, err := st.GetBalance(tid)
	if err != nil {
		t.Fatalf("GetBalance 失败: %v", err)
	}
	return b.Balance
}

// findTask 按 task_key 取任务定义。
func findTask(t *testing.T, st *Store, key string) *UserTask {
	t.Helper()
	t2, err := scanTask(db.QueryRow(st.db, db.CurrentDialect(), "SELECT "+taskCols+" FROM user_tasks WHERE task_key=?", key))
	if err != nil {
		t.Fatalf("任务 %s 应存在: %v", key, err)
	}
	return t2
}

// TestBuiltinTaskSeeds 出厂任务定义数值断言（#33 原文口径）+ 迁移幂等（重复执行不翻倍）。
func TestBuiltinTaskSeeds(t *testing.T) {
	st, _ := taskEnv(t)
	type want struct {
		key     string
		points  int64
		mode    string
		period  string
		days    int
		stack   int
		capDay  int
		capWeek int
	}
	wants := []want{
		{TaskKeyLoginDaily, 100, "auto", "daily", 3, 1, 1, 0},
		{TaskKeyTranslateWeek, 100, "auto", "weekly", 7, 1, 1, 5},
		{TaskKeyInviteReg, 500, "auto", "event", 14, 0, 0, 0},
		{TaskKeyInvitePaid, 1000, "auto", "event", 0, 0, 0, 0},
		{TaskKeyKBUpload, 600, "auto", "once", 0, 0, 0, 0},
	}
	for _, w := range wants {
		tt := findTask(t, st, w.key)
		if got := st.PointsFromTokens(tt.RewardTokens); got != w.points {
			t.Fatalf("%s 奖励应为 %d 积分，实际 %d", w.key, w.points, got)
		}
		if tt.RewardTokens != st.TokensFromPoints(w.points) {
			t.Fatalf("%s 落库 token 口径不符: %d ≠ %d", w.key, tt.RewardTokens, st.TokensFromPoints(w.points))
		}
		if tt.Enabled != 1 || tt.GrantMode != w.mode || tt.Period != w.period {
			t.Fatalf("%s 形态不符: enabled=%d mode=%s period=%s", w.key, tt.Enabled, tt.GrantMode, tt.Period)
		}
		if tt.ValidDays != w.days || tt.StackExpiry != w.stack || tt.CapPerDay != w.capDay || tt.CapPerWeek != w.capWeek {
			t.Fatalf("%s 规则不符: days=%d stack=%d capDay=%d capWeek=%d", w.key, tt.ValidDays, tt.StackExpiry, tt.CapPerDay, tt.CapPerWeek)
		}
	}
	// 迁移幂等：再跑一遍不得重复种入、不得改动数值
	before := len(st.ListUserTasks())
	st.TaskRewardMigrate()
	if after := len(st.ListUserTasks()); after != before {
		t.Fatalf("TaskRewardMigrate 重复执行导致任务翻倍: %d → %d", before, after)
	}
	if got := st.PointsFromTokens(findTask(t, st, TaskKeyLoginDaily).RewardTokens); got != 100 {
		t.Fatalf("重复迁移不应改额，实际每日登录 %d 积分", got)
	}
}

// TestLoginDailyTemporaryGrant 每日登录：100 临时积分入台账（3 天有效）、当日二次触发判重。
func TestLoginDailyTemporaryGrant(t *testing.T) {
	st, u := taskEnv(t)
	before := taskPermanentBalance(t, st, u.TenantID)

	res := st.GrantTaskReward(u.ID, TaskKeyLoginDaily, "")
	if !res.Granted || res.Points != 100 || res.ValidDays != 3 {
		t.Fatalf("首次登录奖励应发放 100 临时积分: %+v", res)
	}
	total, latest := sumTaskGrants(t, st, u.TenantID)
	if total != st.TokensFromPoints(100) {
		t.Fatalf("任务台账应新增 %d token，实际 %d", st.TokensFromPoints(100), total)
	}
	exp, err := time.Parse(time.RFC3339, latest)
	if err != nil {
		t.Fatalf("台账到期日解析失败 %q: %v", latest, err)
	}
	if h := time.Until(exp); h < 71*time.Hour || h > 73*time.Hour {
		t.Fatalf("有效期应为 3 天，实际剩余 %v", h)
	}
	// 永久余额分文不动：临时积分绝不进 balance_accounts
	if after := taskPermanentBalance(t, st, u.TenantID); after != before {
		t.Fatalf("临时积分不得进永久余额: %d → %d", before, after)
	}
	// 当日重复触发：判重且零发放（不产生第二条台账）
	if dup := st.GrantTaskReward(u.ID, TaskKeyLoginDaily, ""); dup.Granted || dup.Reason != "duplicate" {
		t.Fatalf("当日二次触发应判重: %+v", dup)
	}
	if n := taskGrantCount(t, st, u.TenantID); n != 1 {
		t.Fatalf("任务台账行数应为 1，实际 %d", n)
	}
	// 周期进度：今日 1 次 / 本周 1 次 / 累计 1 次
	st2 := st.TaskRewardStatOf(u.ID, findTask(t, st, TaskKeyLoginDaily).ID)
	if st2.TodayCount != 1 || st2.WeekCount != 1 || st2.TotalCount != 1 {
		t.Fatalf("任务进度统计不符: %+v", st2)
	}
}

// taskGrantCount 任务台账行数（含已消耗完的 left=0 行）。
func taskGrantCount(t *testing.T, st *Store, tid int64) int {
	t.Helper()
	var n int
	if err := db.QueryRow(st.db, db.CurrentDialect(),
		"SELECT COUNT(*) FROM quota_grants WHERE tenant_id=? AND kind=?", tid, TaskGrantKind).Scan(&n); err != nil {
		t.Fatalf("统计任务台账行数失败: %v", err)
	}
	return n
}

// TestTaskRewardExpiryStacking 叠加口径：日/周任务以「当前未过期台账最晚到期日」为基准续期；
// 邀请注册（stack_expiry=0）固定 now+14 天，不受已有台账影响。
func TestTaskRewardExpiryStacking(t *testing.T) {
	st, u := taskEnv(t)
	first := st.GrantTaskReward(u.ID, TaskKeyLoginDaily, "")
	if !first.Granted {
		t.Fatalf("首次发放失败: %+v", first)
	}
	base, _ := time.Parse(time.RFC3339, first.ExpiresAt)
	// 模拟次日触发：把流水的日粒度去重键与当日计数器挪走（同一租户台账保留 → 触发叠加）
	rotateTaskRewardDay(t, st, u.ID, findTask(t, st, TaskKeyLoginDaily).ID, 1)
	second := st.GrantTaskReward(u.ID, TaskKeyLoginDaily, "")
	if !second.Granted {
		t.Fatalf("次日发放失败: %+v", second)
	}
	secondExp, _ := time.Parse(time.RFC3339, second.ExpiresAt)
	if delta := secondExp.Sub(base); delta < 71*time.Hour || delta > 73*time.Hour {
		t.Fatalf("日叠加应在旧到期日基础上再续 3 天，实际 +%v", delta)
	}
	if n := taskGrantCount(t, st, u.TenantID); n != 2 {
		t.Fatalf("叠加应新增台账行（独立过期）而非改写，实际 %d 行", n)
	}
	// 不叠加任务：先放一条 1 天后到期的同 kind 台账，邀请注册仍应落在 now+14 天
	if err := st.CreateQuotaGrant(u.TenantID, TaskGrantKind, 1000, time.Now().UTC().Add(24*time.Hour), "ut:seed", 0); err != nil {
		t.Fatalf("种入台账失败: %v", err)
	}
	inv := st.GrantTaskReward(u.ID, TaskKeyInviteReg, "invitee:9001")
	if !inv.Granted || inv.ValidDays != 14 {
		t.Fatalf("邀请注册奖励应发放 500 临时积分/14 天: %+v", inv)
	}
	invExp, _ := time.Parse(time.RFC3339, inv.ExpiresAt)
	if h := time.Until(invExp); h < 335*time.Hour || h > 337*time.Hour {
		t.Fatalf("不叠加任务有效期应固定 14 天，实际剩余 %v", h)
	}
}

// rotateTaskRewardDay 把某任务最近一条发放流水挪到虚构的「历史某天」（dedup 键带 seq 保证互不撞），
// 并清掉当日计数器，用于在同一进程内模拟次日再次触发（同周内则继续吃周上限）。
func rotateTaskRewardDay(t *testing.T, st *Store, uid, taskID int64, seq int) {
	t.Helper()
	d := db.CurrentDialect()
	fake := "sim-day-" + strconv.Itoa(seq)
	if _, err := db.Exec(st.db, d, `UPDATE user_task_rewards SET dedup_key=?, period_day=?
		WHERE id=(SELECT MAX(id) FROM user_task_rewards WHERE user_id=? AND task_id=?)`,
		fake, fake, uid, taskID); err != nil {
		t.Fatalf("挪动发放流水失败: %v", err)
	}
	today, _ := taskPeriodKeys(time.Now())
	if _, err := db.Exec(st.db, d, "DELETE FROM user_task_period_cnt WHERE user_id=? AND task_id=? AND period_key=?",
		uid, taskID, "D:"+today); err != nil {
		t.Fatalf("清理当日计数器失败: %v", err)
	}
}

// TestTranslateWeeklyCap 每周发起翻译：每日 1 次 + 每周 5 次上限（第 6 次触顶拒发、不落台账）。
func TestTranslateWeeklyCap(t *testing.T) {
	st, u := taskEnv(t)
	tk := findTask(t, st, TaskKeyTranslateWeek)
	var granted int64
	for i := 0; i < 6; i++ {
		res := st.GrantTaskReward(u.ID, TaskKeyTranslateWeek, "")
		switch {
		case res.Granted:
			granted++
			rotateTaskRewardDay(t, st, u.ID, tk.ID, i+1) // 模拟周内下一天（周计数器保持累计）
		case res.Reason == "capped_week":
			// 预期：第 6 次触顶
		default:
			t.Fatalf("第 %d 次发放返回意外结果: %+v", i+1, res)
		}
	}
	if granted != 5 {
		t.Fatalf("每周上限 5 次，实际发放 %d 次", granted)
	}
	if total := st.TaskRewardStatOf(u.ID, tk.ID).TotalCount; total != 5 {
		t.Fatalf("发放流水应 5 条（触顶笔不落账），实际 %d", total)
	}
	if cnt := taskCounterOf(t, st, u.ID, tk.ID); cnt != 5 {
		t.Fatalf("周计数器应停在 5（不得击穿），实际 %d", cnt)
	}
}

// taskCounterOf 读用户在该任务上的周期计数器合计（日键+周键累计）。
func taskCounterOf(t *testing.T, st *Store, uid, taskID int64) int64 {
	t.Helper()
	var c int64
	if err := db.QueryRow(st.db, db.CurrentDialect(),
		"SELECT COALESCE(SUM(cnt),0) FROM user_task_period_cnt WHERE user_id=? AND task_id=?", uid, taskID).Scan(&c); err != nil {
		t.Fatalf("读周期计数器失败: %v", err)
	}
	return c
}

// TestInviteTasksPermanentAndPerInvitee 邀请类任务：
// 付费奖励 1000 永久积分（入 balance_accounts、不同好友可叠加、同一好友判重）。
func TestInviteTasksPermanentAndPerInvitee(t *testing.T) {
	st, u := taskEnv(t)
	inviter := u
	// 建两名受邀用户并挂邀请关系
	inviterTaskID := findTask(t, st, TaskKeyInvitePaid).ID
	before := taskPermanentBalance(t, st, inviter.TenantID)
	makeInvitee := func(name string) int64 {
		e, err := st.CreateUser(1, name, "hash", "受邀人", RoleUser, 0, 0)
		if err != nil {
			t.Fatalf("CreateUser 失败: %v", err)
		}
		if _, err := db.Exec(st.db, db.CurrentDialect(), "UPDATE users SET referred_by=? WHERE id=?", inviter.ID, e.ID); err != nil {
			t.Fatalf("挂邀请关系失败: %v", err)
		}
		return e.ID
	}
	e1, e2 := makeInvitee("invitee_1"), makeInvitee("invitee_2")

	r1 := st.GrantTaskRewardToInviter(e1, TaskKeyInvitePaid)
	if !r1.Granted || r1.Points != 1000 || r1.ValidDays != 0 || r1.ExpiresAt != "" {
		t.Fatalf("受邀充值应发 1000 永久积分: %+v", r1)
	}
	after := taskPermanentBalance(t, st, inviter.TenantID)
	if want := before + st.TokensFromPoints(1000); after != want {
		t.Fatalf("永久余额应 +%d 实际 %d", want-before, after)
	}
	if n := taskGrantCount(t, st, inviter.TenantID); n != 0 {
		t.Fatalf("永久积分不得产生台账行，实际 %d 行", n)
	}
	// 同一好友重复触发：判重不重复发
	if dup := st.GrantTaskRewardToInviter(e1, TaskKeyInvitePaid); dup.Granted || dup.Reason != "duplicate" {
		t.Fatalf("同一好友应判重: %+v", dup)
	}
	if again := taskPermanentBalance(t, st, inviter.TenantID); again != after {
		t.Fatalf("重复触发不得二次入账: %d → %d", after, again)
	}
	// 不同好友：可叠加
	if r2 := st.GrantTaskRewardToInviter(e2, TaskKeyInvitePaid); !r2.Granted {
		t.Fatalf("不同好友应继续发放: %+v", r2)
	}
	if want := after + st.TokensFromPoints(1000); taskPermanentBalance(t, st, inviter.TenantID) != want {
		t.Fatalf("不同好友充值应叠加至 %d", want)
	}
	if cnt := st.TaskRewardStatOf(inviter.ID, inviterTaskID).TotalCount; cnt != 2 {
		t.Fatalf("邀请付费流水应 2 条，实际 %d", cnt)
	}
	// 非邀请来源用户：静默跳过（bad_task，不发钱不报错）
	stranger, _ := st.CreateUser(1, "stranger", "hash", "无邀请来源", RoleUser, 0, 0)
	if res := st.GrantTaskRewardToInviter(stranger.ID, TaskKeyInvitePaid); res.Granted {
		t.Fatalf("非邀请来源不应获得奖励: %+v", res)
	}
}

// TestKBUploadOncePermanent 上传自有知识库：+600 永久积分、终身一次。
func TestKBUploadOncePermanent(t *testing.T) {
	st, u := taskEnv(t)
	before := taskPermanentBalance(t, st, u.TenantID)
	res := st.GrantTaskReward(u.ID, TaskKeyKBUpload, "")
	if !res.Granted || res.Points != 600 || res.ValidDays != 0 {
		t.Fatalf("知识库上传应发 600 永久积分: %+v", res)
	}
	if want := before + st.TokensFromPoints(600); taskPermanentBalance(t, st, u.TenantID) != want {
		t.Fatalf("永久余额应 %d，实际 %d", want, taskPermanentBalance(t, st, u.TenantID))
	}
	// 一次性：即便带不同 dedup 后缀也只发一次（period=once 去重键固定）
	if dup := st.GrantTaskReward(u.ID, TaskKeyKBUpload, "pack:2"); dup.Granted || dup.Reason != "duplicate" {
		t.Fatalf("一次性任务应终身判重: %+v", dup)
	}
	// 用户视角：once 任务以累计次数判定已领取
	var found bool
	for _, v := range st.ListUserTaskViews(u.ID) {
		if v.TaskKey != TaskKeyKBUpload {
			continue
		}
		found = true
		if !v.Claimed {
			t.Fatalf("已发放的一次性任务应显示已领取: %+v", v.Reward)
		}
	}
	if !found {
		t.Fatal("用户任务视图缺少 kb_upload")
	}
}

// TestTaskRewardGates 三类关闸：任务停用 / 发放中台（ops_policy.task.enabled=false）/ 手工任务不走事件发放。
func TestTaskRewardGates(t *testing.T) {
	st, u := taskEnv(t)
	// ① 单任务停用
	grantTaskForTest(t, st, TaskKeyLoginDaily, "enabled=0")
	if res := st.GrantTaskReward(u.ID, TaskKeyLoginDaily, ""); res.Granted || res.Reason != "disabled" {
		t.Fatalf("停用任务应拒发: %+v", res)
	}
	grantTaskForTest(t, st, TaskKeyLoginDaily, "enabled=1")
	// ② 发放中台关闸（全站任务奖励总开关）
	if err := st.SetConfig("ops_policy", `{"task":{"enabled":false}}`); err != nil {
		t.Fatalf("写入运营策略失败: %v", err)
	}
	if res := st.GrantTaskReward(u.ID, TaskKeyLoginDaily, ""); res.Granted || res.Reason != "disabled" {
		t.Fatalf("中台关闸后应零发放: %+v", res)
	}
	if n := taskGrantCount(t, st, u.TenantID); n != 0 {
		t.Fatalf("关闸不得留下台账行，实际 %d", n)
	}
	if err := st.SetConfig("ops_policy", `{"task":{"enabled":true}}`); err != nil {
		t.Fatalf("恢复运营策略失败: %v", err)
	}
	if res := st.GrantTaskReward(u.ID, TaskKeyLoginDaily, ""); !res.Granted {
		t.Fatalf("恢复开关后应可发放: %+v", res)
	}
	// ③ 手工（无 task_key）任务不走事件发放；④ 未知 task_key 安全回落
	manual := &UserTask{TaskType: "daily", Title: "手工任务", RewardTokens: st.TokensFromPoints(50), Enabled: 1}
	if _, err := st.SaveUserTask(manual); err != nil {
		t.Fatalf("SaveUserTask 失败: %v", err)
	}
	if res := st.GrantTaskReward(u.ID, "no_such_task", ""); res.Reason != "bad_task" {
		t.Fatalf("未知任务标识应回落 bad_task: %+v", res)
	}
	// period=event 缺去重后缀 → bad_task（防止无 dedup 的重复发放）
	if res := st.GrantTaskReward(u.ID, TaskKeyInviteReg, ""); res.Reason != "bad_task" {
		t.Fatalf("event 任务缺 dedup 应拒绝: %+v", res)
	}
}

// TestManualClaimRejectsAutoTasks 人工领取通道对自动发放任务关闭（防双发），老的手工任务仍可领取。
func TestManualClaimRejectsAutoTasks(t *testing.T) {
	st, u := taskEnv(t)
	auto := findTask(t, st, TaskKeyLoginDaily)
	if ok, _ := st.ClaimUserTask(u.ID, u.TenantID, auto.ID); ok {
		t.Fatal("自动发放任务不得再走人工领取（双发风险）")
	}
	manual := &UserTask{TaskType: "daily", Title: "每日签到（手工）", RewardTokens: st.TokensFromPoints(20), Enabled: 1}
	id, err := st.SaveUserTask(manual)
	if err != nil {
		t.Fatalf("SaveUserTask 失败: %v", err)
	}
	if ok, tokens := st.ClaimUserTask(u.ID, u.TenantID, id); !ok || tokens != st.TokensFromPoints(20) {
		t.Fatalf("手工任务应可领取 20 积分，实际 ok=%v tokens=%d", ok, tokens)
	}
	if ok, _ := st.ClaimUserTask(u.ID, u.TenantID, id); ok {
		t.Fatal("手工每日任务当日二次领取应被拒")
	}
}

// TestResetTaskGrantConsumption ★#33 特殊任务：重置「已订阅用户」的任务积分消耗量——
// 消耗完（left=0）的也重置、有效期不变、未订阅租户不受影响、plan 台账不动。
func TestResetTaskGrantConsumption(t *testing.T) {
	st, _ := taskEnv(t)
	d := db.CurrentDialect()
	// 租户 1 = 已订阅（有未过期 plan 台账）；租户 8 = 未订阅
	if _, err := db.Exec(st.db, d, "INSERT INTO tenants (id, code, name, status) VALUES (8,'t8','未订阅租户','active')"); err != nil {
		t.Fatalf("建租户 8 失败: %v", err)
	}
	exp := time.Now().UTC().Add(20 * 24 * time.Hour)
	if err := st.CreateQuotaGrant(1, "plan", 500000, exp, "order", 0); err != nil {
		t.Fatalf("种入订阅台账失败: %v", err)
	}
	// 订阅租户两条任务台账：一条部分消耗、一条消耗完（含已过期一条，均不应被重置）
	if err := st.CreateQuotaGrant(1, TaskGrantKind, 30000, time.Now().UTC().Add(3*24*time.Hour), "ut:a", 0); err != nil {
		t.Fatalf("种入任务台账失败: %v", err)
	}
	if err := st.CreateQuotaGrant(1, TaskGrantKind, 9000, time.Now().UTC().Add(5*24*time.Hour), "ut:b", 0); err != nil {
		t.Fatalf("种入任务台账失败: %v", err)
	}
	if err := st.CreateQuotaGrant(1, TaskGrantKind, 7000, time.Now().UTC().Add(-24*time.Hour), "ut:expired", 0); err != nil {
		t.Fatalf("种入过期任务台账失败: %v", err)
	}
	if _, err := db.Exec(st.db, d, `UPDATE quota_grants SET "left"=10000 WHERE tenant_id=1 AND kind=? AND source='ut:a'`, TaskGrantKind); err != nil {
		t.Fatalf("制造部分消耗失败: %v", err)
	}
	if _, err := db.Exec(st.db, d, `UPDATE quota_grants SET "left"=0 WHERE tenant_id=1 AND kind=? AND source='ut:b'`, TaskGrantKind); err != nil {
		t.Fatalf("制造消耗完失败: %v", err)
	}
	// 已过期台账：同样消耗完，但不得被重置（过期即作废，与全站台账口径一致）
	if _, err := db.Exec(st.db, d, `UPDATE quota_grants SET "left"=0 WHERE tenant_id=1 AND source='ut:expired'`); err != nil {
		t.Fatalf("制造过期消耗完失败: %v", err)
	}
	// 未订阅租户同样有一条消耗完的任务台账
	if err := st.CreateQuotaGrant(8, TaskGrantKind, 6000, time.Now().UTC().Add(4*24*time.Hour), "ut:c", 0); err != nil {
		t.Fatalf("种入租户8台账失败: %v", err)
	}
	if _, err := db.Exec(st.db, d, `UPDATE quota_grants SET "left"=0 WHERE tenant_id=8 AND source='ut:c'`); err != nil {
		t.Fatalf("制造租户8消耗完失败: %v", err)
	}
	planExpiryBefore := taskGrantExpiry(t, st, 1, "order")

	tenants, rows, err := st.ResetTaskGrantConsumption(true)
	if err != nil {
		t.Fatalf("重置失败: %v", err)
	}
	if tenants != 1 || rows != 2 {
		t.Fatalf("应只重置订阅租户的 2 条任务台账，实际 tenants=%d rows=%d", tenants, rows)
	}
	if left := taskGrantLeft(t, st, 1, "ut:a"); left != 30000 {
		t.Fatalf("部分消耗台账应拉回 total，实际 left=%d", left)
	}
	if left := taskGrantLeft(t, st, 1, "ut:b"); left != 9000 {
		t.Fatalf("消耗完台账也应重置，实际 left=%d", left)
	}
	if left := taskGrantLeft(t, st, 1, "ut:expired"); left != 0 {
		t.Fatalf("已过期台账不得重置（有效期口径），实际 left=%d", left)
	}
	if left := taskGrantLeft(t, st, 8, "ut:c"); left != 0 {
		t.Fatalf("未订阅租户不得被重置，实际 left=%d", left)
	}
	if left := taskGrantLeft(t, st, 1, "order"); left != 500000 {
		t.Fatalf("plan 订阅台账不在本操作影响范围内（只重置 kind=task）")
	}
	// 有效期不变（用户原文口径）
	if got := taskGrantExpiry(t, st, 1, "ut:a"); got == "" {
		t.Fatal("重置后台账丢失")
	}
	if before, after := planExpiryBefore, taskGrantExpiry(t, st, 1, "order"); before != after {
		t.Fatalf("plan 到期时间被改写: %s → %s", before, after)
	}
	if taskGrantExpiry(t, st, 1, "ut:b") == "" {
		t.Fatal("有效期应保持不变（不得清空）")
	}
	// subscribedOnly=false：连未订阅租户一起重置（超管兜底口径）
	if tenants2, rows2, err := st.ResetTaskGrantConsumption(false); err != nil || tenants2 != 1 || rows2 != 1 {
		t.Fatalf("全量重置应只影响租户8的 1 条（订阅租户已满额），实际 tenants=%d rows=%d err=%v", tenants2, rows2, err)
	}
	if left := taskGrantLeft(t, st, 8, "ut:c"); left != 6000 {
		t.Fatalf("全量重置后租户8应恢复 6000，实际 %d", left)
	}
	// 幂等：再次重置无行可改
	if _, rows3, err := st.ResetTaskGrantConsumption(false); err != nil || rows3 != 0 {
		t.Fatalf("重复重置应 0 行，实际 rows=%d err=%v", rows3, err)
	}
}

// taskGrantLeft 按 source 读台账剩余量（多条取合计）。
func taskGrantLeft(t *testing.T, st *Store, tid int64, source string) int64 {
	t.Helper()
	var v int64
	err := db.QueryRow(st.db, db.CurrentDialect(),
		`SELECT COALESCE(SUM("left"),-1) FROM quota_grants WHERE tenant_id=? AND source=?`, tid, source).Scan(&v)
	if err != nil {
		t.Fatalf("读台账剩余失败: %v", err)
	}
	return v
}

// taskGrantExpiry 按 source 读台账到期时间（空=未找到）。
func taskGrantExpiry(t *testing.T, st *Store, tid int64, source string) string {
	t.Helper()
	var v string
	err := db.QueryRow(st.db, db.CurrentDialect(),
		"SELECT COALESCE(MAX(expires_at),'') FROM quota_grants WHERE tenant_id=? AND source=?", tid, source).Scan(&v)
	if err != nil {
		t.Fatalf("读台账到期时间失败: %v", err)
	}
	return v
}

// TestTaskGrantsDeductedFirst 扣减顺序不变量：任务临时积分（先到期）先于永久余额核销。
func TestTaskGrantsDeductedFirst(t *testing.T) {
	st, u := taskEnv(t)
	if err := st.EnsureBalance(u.TenantID); err != nil {
		t.Fatalf("EnsureBalance 失败: %v", err)
	}
	if _, err := db.Exec(st.db, db.CurrentDialect(), "UPDATE balance_accounts SET balance=? WHERE tenant_id=?",
		st.TokensFromPoints(500), u.TenantID); err != nil {
		t.Fatalf("预置永久余额失败: %v", err)
	}
	if res := st.GrantTaskReward(u.ID, TaskKeyLoginDaily, ""); !res.Granted {
		t.Fatalf("登录奖励发放失败: %+v", res)
	}
	if err := st.DeductWithGrants(u.TenantID, st.TokensFromPoints(100)); err != nil {
		t.Fatalf("扣减失败: %v", err)
	}
	if total, _ := sumTaskGrants(t, st, u.TenantID); total != 0 {
		t.Fatalf("应优先核销任务临时积分，台账剩余 %d", total)
	}
	if want := st.TokensFromPoints(500); taskPermanentBalance(t, st, u.TenantID) != want {
		t.Fatalf("永久余额不应被动用，实际 %d ≠ %d", taskPermanentBalance(t, st, u.TenantID), want)
	}
}

// TestTaskViewsExposeRewardProgress 用户视角任务视图：自动任务带进度与开关状态、事件任务不显示已领。
func TestTaskViewsExposeRewardProgress(t *testing.T) {
	st, u := taskEnv(t)
	if res := st.GrantTaskReward(u.ID, TaskKeyTranslateWeek, ""); !res.Granted {
		t.Fatalf("发起翻译奖励发放失败: %+v", res)
	}
	var login, invite *UserTaskView
	for _, v := range st.ListUserTaskViews(u.ID) {
		switch v.TaskKey {
		case TaskKeyLoginDaily:
			login = v
		case TaskKeyTranslateWeek:
			invite = v
		}
	}
	if login == nil || invite == nil {
		t.Fatal("任务视图缺少出厂任务")
	}
	if login.Reward == nil || invite.Reward == nil {
		t.Fatal("自动发放任务必须带进度（Reward 为 nil 则前端无法展示）")
	}
	if !invite.Claimed || invite.Reward.TodayCount != 1 || invite.Reward.WeekCount != 1 {
		t.Fatalf("发起翻译本周应显示已达成: %+v", invite)
	}
	if login.Claimed {
		t.Fatal("未发放的每日登录任务不应显示已达成")
	}
	if st.TaskRewardStatOf(u.ID, invite.ID).LastExpiry == "" {
		t.Fatal("临时积分任务应回带最近到期时间")
	}
}

// TestInvitePaidTaskRewardRevokedOnRefund ★#33 反补丁：受邀人订单全部退款时，
// 「邀请好友且任意充值 +1000 永久积分」必须与裂变付费奖励同源回收——
// 否则「付费→退款」可白嫖一笔任务积分（与 A3 回收口径保持一致）。
func TestInvitePaidTaskRewardRevokedOnRefund(t *testing.T) {
	st, u := taskEnv(t)
	makeInvitee := func(name string) int64 {
		e, err := st.CreateUser(1, name, "hash", "受邀人", RoleUser, 0, 0)
		if err != nil {
			t.Fatalf("CreateUser 失败: %v", err)
		}
		if _, err := db.Exec(st.db, db.CurrentDialect(), "UPDATE users SET referred_by=? WHERE id=?", u.ID, e.ID); err != nil {
			t.Fatalf("挂邀请关系失败: %v", err)
		}
		return e.ID
	}
	e1, e2 := makeInvitee("refund_invitee_1"), makeInvitee("refund_invitee_2")
	taskID := findTask(t, st, TaskKeyInvitePaid).ID
	base := taskPermanentBalance(t, st, u.TenantID)

	if r := st.GrantTaskRewardToInviter(e1, TaskKeyInvitePaid); !r.Granted {
		t.Fatalf("受邀充值奖励应发放: %+v", r)
	}
	if r := st.GrantTaskRewardToInviter(e2, TaskKeyInvitePaid); !r.Granted {
		t.Fatalf("第二名受邀充值奖励应发放: %+v", r)
	}
	gifted := st.TokensFromPoints(1000)
	if got := taskPermanentBalance(t, st, u.TenantID); got != base+2*gifted {
		t.Fatalf("两名好友充值后余额应为 %d，实际 %d", base+2*gifted, got)
	}

	// 回收其中一名受邀人的奖励：余额退回一笔奖励额度、该条流水撤销、另一名不受牵连
	if revoked := st.RevokeTaskRewardForInvitee(e1, TaskKeyInvitePaid); revoked != gifted {
		t.Fatalf("应回收 %d token，实际 %d", gifted, revoked)
	}
	if got := taskPermanentBalance(t, st, u.TenantID); got != base+gifted {
		t.Fatalf("回收后余额应为 %d，实际 %d", base+gifted, got)
	}
	if cnt := st.TaskRewardStatOf(u.ID, taskID).TotalCount; cnt != 1 {
		t.Fatalf("回收后应只剩 1 条邀请付费流水，实际 %d", cnt)
	}
	// 幂等：同一受邀人二次回收不再扣款
	if again := st.RevokeTaskRewardForInvitee(e1, TaskKeyInvitePaid); again != 0 {
		t.Fatalf("重复回收应返回 0，实际 %d", again)
	}
	// 流水撤销后同一好友重新付费可再次达标（不被 dedup 永久挡住）
	if r := st.GrantTaskRewardToInviter(e1, TaskKeyInvitePaid); !r.Granted {
		t.Fatalf("退款后重新付费应可再次发放: %+v", r)
	}
	// 余额不足时守卫式扣回（扣到 0，不产生负余额）并留缺口告警
	if _, err := db.Exec(st.db, db.CurrentDialect(), "UPDATE balance_accounts SET balance=0 WHERE tenant_id=?", u.TenantID); err != nil {
		t.Fatalf("置零余额失败: %v", err)
	}
	// CreateAlert 对同租户同类未处理告警幂等去重：先结掉上一笔，缺口告警才会落库
	if _, err := db.Exec(st.db, db.CurrentDialect(), "UPDATE alerts SET status='resolved' WHERE kind='task_reward_revoke'"); err != nil {
		t.Fatalf("重置告警状态失败: %v", err)
	}
	if got := st.RevokeTaskRewardForInvitee(e1, TaskKeyInvitePaid); got != 0 {
		t.Fatalf("余额为 0 时应扣回 0，实际 %d", got)
	}
	if bal := taskPermanentBalance(t, st, u.TenantID); bal != 0 {
		t.Fatalf("回收不得把余额扣成负数，实际 %d", bal)
	}
	var infoCnt, warnCnt int
	_ = db.QueryRow(st.db, db.CurrentDialect(), "SELECT COUNT(*) FROM alerts WHERE kind='task_reward_revoke' AND level='info'").Scan(&infoCnt)
	_ = db.QueryRow(st.db, db.CurrentDialect(), "SELECT COUNT(*) FROM alerts WHERE kind='task_reward_revoke' AND level='warning'").Scan(&warnCnt)
	if infoCnt == 0 || warnCnt == 0 {
		t.Fatalf("正常回收与缺口回收都要留痕，实际 info=%d warning=%d", infoCnt, warnCnt)
	}
}
