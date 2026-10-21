// ============ trial_auto.go · 职责说明 ============
// 账号级免费体验自动发放（★ 2026-10-21 M3 用户令）。
//   - 注册成功：新账号自动获得一次体验额度；
//   - 首次登录：给“从未发过”的自助注册账号补发（覆盖注册时发放失败的账号）；
//   - 一个账号仅一次：由 store.user_trial_grants 的 user_id 主键保证，并发登录也只发一笔；
//   - 管理台创建的员工账号（created_by>0）不自动发放，避免企业批量导入时凭空产生市场费用。
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
