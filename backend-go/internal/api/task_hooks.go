// ============ task_hooks.go · 职责说明 ============
// api 包「任务系统事件钩子」（需求 #33，2026-09-21）。
// 把产品口径的四类事件接进发放中台，全部为**旁路尽力而为**：
//   - 登录成功 → login_daily（+100 临时积分 / 有效期 3 天 / 一日一次 / 日叠加）
//   - 发起翻译（即时文本与文件翻译，成功计量后）→ translate_week（+100 临时积分 / 7 天 / 周 ≤5 次、日 ≤1 次）
//   - 好友经邀请码注册成功 → invite_register（+500 临时积分 / 14 天 / 可叠加）
//   - 知识库上传并解析成功 → kb_upload（+600 永久积分 / 一次性）
//
// （invite_paid「好友充值 +1000 永久积分」在存储层 ReferralPaidReward 内挂钩：
//
//	支付确认入口多且分散，挂在唯一的付费奖励收敛点上才能保证全路径覆盖。）
//
// 共同约束：
//   - 运营策略 task.enabled=false（租户级）或平台关闸时零副作用跳过；
//   - 发放失败/重复/触顶只记结构化日志，绝不打断用户主流程（奖励可事后人工补）；
//   - 对外零 token：日志与出参一律积分口径。
//
// =============================================
package api

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"

	"translator/internal/observability"
	"translator/internal/store"
)

// grantTaskEvent 单个事件的任务奖励发放（旁路：只记日志不返回错误）。
// 生效租户从请求登录态推导（登录等尚无登录态的场景请用 grantTaskEventOnTenant）。
// 参数：r=触发请求（用于取生效租户与 trace_id，可为 nil）；uid=获奖用户；
// taskKey=任务标识（store.TaskKey*）；dedup=事件去重后缀（period=event 任务必填，如 "invitee:123"）。
func (s *Server) grantTaskEvent(r *http.Request, uid int64, taskKey, dedup string) store.TaskRewardResult {
	tid := int64(0)
	if r != nil {
		if u := s.authUser(r); u != nil {
			tid = s.effTenant(r, u)
		}
	}
	return s.grantTaskEventOnTenant(r, tid, uid, taskKey, dedup)
}

// grantTaskEventOnTenant 与 grantTaskEvent 同义，但显式指定用于策略门禁的生效租户
// （登录成功时机尚无 Authorization 头，authUser 取不到人，必须把租户 ID 传进来，否则租户级关闸会被绕过）。
func (s *Server) grantTaskEventOnTenant(r *http.Request, tid, uid int64, taskKey, dedup string) store.TaskRewardResult {
	var empty store.TaskRewardResult
	if s.Store == nil || uid <= 0 {
		return empty
	}
	ctx := context.Background()
	if r != nil {
		ctx = r.Context()
	}
	// 租户级策略门禁（与任务中心开关同口径）
	if tid > 0 && !s.effectivePolicyCached(tid).Task.Enabled {
		return empty
	}
	res := s.Store.GrantTaskReward(uid, taskKey, dedup)
	switch {
	case res.Granted:
		observability.Log(ctx, slog.LevelInfo, "task_reward_granted",
			"user_id", uid, "task", res.TaskKey, "points", res.Points, "valid_days", res.ValidDays, "expires_at", res.ExpiresAt)
	case res.Reason == "duplicate":
		// 已发放过（每日一次/同一好友一次）属常态，不告警只留痕
		observability.Log(ctx, slog.LevelDebug, "task_reward_duplicate", "user_id", uid, "task", taskKey)
	case res.Reason == "capped_day" || res.Reason == "capped_week":
		observability.Log(ctx, slog.LevelInfo, "task_reward_capped", "user_id", uid, "task", taskKey, "reason", res.Reason)
	case res.Reason != "disabled":
		observability.Log(ctx, slog.LevelWarn, "task_reward_skipped", "user_id", uid, "task", taskKey, "reason", res.Reason)
	}
	return res
}

// grantTaskEventForInvitee 以「受邀人」为事件主体给其邀请人发奖（invite_register / invite_paid 共用）。
// 参数：r=触发请求（可空）；inviteeUID=被邀请注册/充值成功的用户。
func (s *Server) grantTaskEventForInvitee(r *http.Request, inviteeUID int64, taskKey string) store.TaskRewardResult {
	var empty store.TaskRewardResult
	if s.Store == nil || inviteeUID <= 0 {
		return empty
	}
	inviter := s.Store.InviterOf(inviteeUID)
	if inviter <= 0 {
		return empty // 非邀请来源：无任务奖励
	}
	return s.grantTaskEvent(r, inviter, taskKey, "invitee:"+strconv.FormatInt(inviteeUID, 10))
}

// grantTranslateTask 翻译事件任务奖励（#33「每周发起翻译」：+100 临时积分，日 ≤1 次、周 ≤5 次）。
// 四个翻译入口（流式/非流式 × 文本/文件）的成功分支统一调用；
// 「每天最多一次」由存储层按日去重键保证，因此连点多次翻译只发一笔。
func (s *Server) grantTranslateTask(r *http.Request, tid int64) {
	if s.Store == nil || r == nil {
		return
	}
	u := s.authUser(r)
	if u == nil {
		return // 匿名（理论上已被翻译闸门拒绝）
	}
	s.grantTaskEventOnTenant(r, tid, u.ID, store.TaskKeyTranslateWeek, "")
}
