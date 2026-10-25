// ============ trial_auto.go · 职责说明 ============
// 账号级免费体验自动发放（★ 2026-10-21 M3 用户令）。
//   - 注册成功：新账号自动获得一次体验额度；
//   - 首次登录：给“从未发过”的自助注册账号补发（覆盖注册时发放失败的账号）；
//   - 一个账号仅一次：由 store.user_trial_grants 的 user_id 主键保证，并发登录也只发一笔；
//   - 管理台创建的员工账号（created_by>0）不自动发放，避免企业批量导入时凭空产生市场费用；
//   - ★ 2026-10-24 多身份方案 R5：凭企业邀请码/邀请链接加入的**企业身份**也不发放，
//     但它走的是另一条机制（markTrialNotGranted 落一行 tokens=0 的占位账）——
//     因为这一族身份**会被登录路径补发**，只砍注册那一条腿救不了它。
//
// 失败一律 best-effort：注册/登录本身不因额度发放失败而失败，登录路径会在下一次补发。
// =============================================
package api

import (
	"context"
	"time"

	"translator/internal/observability"
	"translator/internal/store"
)

// autoGrantUserTrial 给一个自助注册账号发放一次体验额度。
func (s *Server) autoGrantUserTrial(ctx context.Context, u *store.User) {
	if s == nil || s.Store == nil || u == nil || u.TenantID <= 0 || u.CreatedBy != 0 {
		return
	}
	tokens, days := s.trialConfig()
	if tokens <= 0 {
		return
	}
	granted, err := s.Store.GrantUserTrialOnce(u.TenantID, u.ID, tokens, time.Now().Add(time.Duration(days)*24*time.Hour), "register")
	if err != nil {
		observability.Warn(ctx, "体验额度自动发放失败（下次登录重试）", "uid", u.ID, "tid", u.TenantID, "err", err.Error())
		return
	}
	if granted {
		observability.Info(ctx, "账号体验额度已自动发放",
			"uid", u.ID, "tid", u.TenantID, "points", s.Store.PointsFromTokens(tokens))
	}
}

// markTrialNotGranted 给「企业凭据门建出来的身份」留一行**不发放**的账（★ 2026-10-24 多身份方案 R5）。
//
// 为什么不是"直接不调 autoGrantUserTrial"就完事：发放有**三条腿**（注册、口令登录补发、邮箱验证码登录补发），
// 而补发腿的判据是「这一行从没在 user_trial_grants 里出现过」。只砍注册那一条，
// 这个员工第一次登录照样会被补出一份体验额度——发放动作 funded 的是**租户**，
// 于是每加一个员工身份就给公司钱包凭空加一份平台补贴（企业侧用量本该只走 quota_org.go 的预算墙）。
//
// tokens=0 是这条链上已有的"占位不发放"形态（与 UserTrialGrantMigrate 给存量行写的 'backfill' 占位同一把尺子）：
// GrantUserTrialOnce 只落台账、不建 quota_grants、不动到期提醒窗口、返回 false。
// 所以这一行既是"这个身份天生不带体验额度"的**持久事实**（换进程、重启、B2 的 PromoteHomeRow 摘家都仍然生效），
// 又不花一分钱。失败只出声不阻断注册：标记丢了最坏是多补发一份，注册本身不该为此失败。
func (s *Server) markTrialNotGranted(ctx context.Context, u *store.User) {
	if s == nil || s.Store == nil || u == nil || u.TenantID <= 0 {
		return
	}
	if _, err := s.Store.GrantUserTrialOnce(u.TenantID, u.ID, 0, time.Now(), "membership_no_trial"); err != nil {
		observability.Warn(ctx, "企业身份体验额度「不发放」标记失败（下次登录可能补发）",
			"uid", u.ID, "tid", u.TenantID, "err", err.Error())
	}
}
