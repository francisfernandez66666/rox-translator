// ============ profile_setup.go · 职责说明 ============
// 首登资料初始化（★ 2026-10-21 IAM 重做）。
//   - 自动建号/老账号首次登录后提醒补全用户名、可选密码与职业角色；
//   - 用户可跳过：跳过只关本次弹窗，profile_setup_required 仍为 1，下次登录继续提醒；
//   - 只有完成才清标记；两条路都重签 token，让前端立即拿到最新资料状态；
//   - ★ 刻意不复用 must_change_pwd 的 428 强制闸：那是"不改密就什么都做不了"，
//     而首登资料按用户令必须可跳过（同 AGENTS §一·5 身份/门面口径）。
//
// =============================================
package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"translator/internal/auth"
	apierrors "translator/internal/errors"
)

// handleProfileSetup 完成或跳过首登资料初始化。
// body: {username?, password?, job_role?, skip?}；skip=true 时其余字段忽略。
func (s *Server) handleProfileSetup(w http.ResponseWriter, r *http.Request) {
	u := s.authUser(r)
	if u == nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrUnauthorized, "未登录"))
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		JobRole  string `json:"job_role"`
		Skip     bool   `json:"skip"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "请求格式错误"))
		return
	}
	if !req.Skip {
		uname := strings.TrimSpace(req.Username)
		if uname == "" {
			s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "请填写用户名或选择跳过"))
			return
		}
		if len([]rune(uname)) > 32 {
			s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "用户名最长 32 个字符"))
			return
		}
		if pwd := req.Password; pwd != "" {
			if !auth.ValidatePassword(pwd) {
				s.writeError(w, r, apierrors.New(apierrors.ErrValidation, auth.PasswordTooShortMsg))
				return
			}
			if _, err := s.Store.ResetPassword(u.ID, u.TenantID, auth.PasswordHash(pwd)); err != nil {
				s.writeError(w, r, apierrors.New(apierrors.ErrInternal, "密码保存失败"))
				return
			}
			// ResetPassword 递增 token_version：内存副本必须同步，否则重签出来的 token 立即失效。
			u.TokenVersion++
			u.PasswordHash = auth.PasswordHash(pwd)
		}
		if err := s.Store.SetUsername(u.ID, u.TenantID, uname); err != nil {
			s.writeError(w, r, apierrors.New(apierrors.ErrInternal, "用户名保存失败"))
			return
		}
		u.Username = uname
		u.DisplayName = uname
		if code := strings.ToLower(strings.TrimSpace(req.JobRole)); code != "" {
			if _, perr := s.Store.FindEnabledPersonaByCode(code); perr == nil {
				if serr := s.Store.SetJobRole(u.ID, u.TenantID, code); serr == nil {
					u.JobRole = code
				}
			}
		}
	}
	// ★ 「暂时跳过」只结束本轮，不消灭提醒：标记留在库里，下次登录仍会展示；
	//   只有真正填完（用户名必填）才清 0。与 must_change_pwd 的 428 强制闸仍是两条腿。
	left := u.ProfileSetupRequired
	if !req.Skip {
		if err := s.Store.SetProfileSetupRequired(u.ID, u.TenantID, 0); err != nil {
			s.writeError(w, r, apierrors.New(apierrors.ErrInternal, "资料状态保存失败"))
			return
		}
		left = 0
	}
	u.ProfileSetupRequired = left
	tok, serr := auth.Sign(u, 24*time.Hour)
	if serr != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, "签到失败"))
		return
	}
	s.Store.LogAudit(u.TenantID, u.ID, "profile_setup", "user", map[bool]string{true: "跳过", false: "完成"}[req.Skip])
	writeJSON(w, 200, map[string]interface{}{
		"success": true, "token": tok,
		"user": map[string]interface{}{
			"id": u.ID, "username": u.Username, "display_name": u.DisplayName,
			"role": u.Role, "tenant_id": u.TenantID, "email": u.Email,
			"must_change_pwd": u.MustChangePwd, "profile_setup_required": left,
		},
	})
}
