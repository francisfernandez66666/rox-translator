// ============ email_login.go · 职责说明 ============
// 邮箱验证码登录主流程（★ 2026-10-21 IAM 重做）。
//   - 邮箱是唯一外部登录标识；验证码证明邮箱归属；
//   - 主站（非专属域名、无邀请上下文）遇到未注册邮箱时**自动创建空白个人账号**；
//   - 自动建号复用注册侧的防薅三档（IP / 设备 / 平台日名额）、Turnstile、一次性邮箱黑名单、
//     协议签署留痕与体验额度发放，避免"验证码登录"成为绕过注册限流的第二条腿；
//   - 旧账号（停用/注销）的邮箱可被新空白账号占用：旧账号数据保留，仅由 OneidMigrate 语义保证
//     邮箱占用只约束 active/deactivating。
//
// 口径：本文件只负责 HTTP 编排；建号事务在 iam.Store.CreateUserWithEmail 一处完成。
// =============================================
package api

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"translator/internal/auth"
	apierrors "translator/internal/errors"
	"translator/internal/observability"
	"translator/internal/store"
	"translator/internal/tenant"
)

// loginEmailReq 是邮箱验证码登录/密码登录共用的入参（沿用 /api/auth/login）。
type loginEmailReq struct {
	Username     string `json:"username"`
	Email        string `json:"email"`
	Password     string `json:"password"`
	Code         string `json:"code"`          // 验证码登录：邮箱验证码
	DeviceID     string `json:"device_id"`     // 防薅设备号（自动建号必需；缺失按匿名档处理，不返 400）
	CaptchaToken string `json:"captcha_token"` // Turnstile token（启用时必填）
	Agreed       bool   `json:"agreed"`        // 自动建号必须勾选协议；已有账号忽略
	AppLang      string `json:"app_lang"`
}

// isEmailLogin 判断本次请求是否走验证码登录（Code 非空且 Email 非空）。
func isEmailLogin(req *loginEmailReq) bool {
	return strings.TrimSpace(req.Code) != "" && strings.TrimSpace(req.Email) != ""
}

// handleEmailCodeLogin 邮箱验证码登录（未注册则自动建空白个人号）。
// 安全顺序：先验码（证明邮箱归属）→ 判账号状态 → 建号前过防薅/人机/黑名单/协议。
func (s *Server) handleEmailCodeLogin(w http.ResponseWriter, r *http.Request, req *loginEmailReq) {
	email := strings.ToLower(strings.TrimSpace(req.Email))
	code := strings.TrimSpace(req.Code)
	if s.Store == nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, "平台存储未初始化"))
		return
	}
	if !emailRe.MatchString(email) {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "邮箱格式不正确"))
		return
	}
	if !verifyEmailCodeFor(email, code, emailPurposeLogin) {
		// 验证码错与不存在同一条文案：登录面不提供"这个邮箱有没有注册"的回执。
		s.writeError(w, r, apierrors.New(apierrors.ErrUnauthorized, "验证码错误或已过期"))
		return
	}
	u, err := s.Store.GetUserByEmail(email)
	// ★ 只有「查无此邮箱」才允许建号；库读失败不是"这个邮箱没注册"，
	//   把它当未注册会绕过 OneidMigrate 的占用语义、凭空长出一个重复账号。
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, "账号查询失败"))
		return
	}
	if errors.Is(err, sql.ErrNoRows) {
		u = s.createPersonalByEmail(w, r, req, email)
		if u == nil {
			return // createPersonalByEmail 已写响应
		}
	}
	// 状态判定放在验码之后：已经证明邮箱归属，才允许告知"账号停用/已注销"。
	effective := auth.EffectiveUserStatus(u.Status, u.DeactivatedAt)
	if effective == store.UserDisabled || (u.Status == store.UserDeactivating && effective == store.UserDisabled) {
		if u.Status == store.UserDeactivating {
			s.Store.FinalizeDeactivation(u.ID, u.TenantID)
			s.writeError(w, r, apierrors.New(apierrors.ErrForbidden, "账号已注销，如需恢复请联系管理员"))
			return
		}
		s.writeError(w, r, apierrors.New(apierrors.ErrForbidden, "账号已停用"))
		return
	}
	if u.TenantID > 0 && s.Ten != nil {
		if t, terr := s.Ten.GetByID(u.TenantID); terr == nil && t != nil {
			switch t.Status {
			case tenant.StatusDisabled:
				s.writeError(w, r, apierrors.New(apierrors.ErrForbidden, "该企业账号已停用，请联系管理员"))
				return
			case tenant.StatusExpired:
				s.writeError(w, r, apierrors.New(apierrors.ErrForbidden, "该企业服务已到期，请联系管理员续费"))
				return
			}
		}
	}
	s.finishLogin(w, r, u)
}

// finishLogin 签发会话并返回统一的登录响应（验证码/密码两条腿共用）。
// 与旧 handleLogin 尾部保持同一契约，额外带上 profile_setup_required 供前端弹首登资料提醒。
func (s *Server) finishLogin(w http.ResponseWriter, r *http.Request, u *store.User) {
	tok, err := auth.Sign(u, 24*time.Hour)
	if err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, "签发失败"))
		return
	}
	s.Store.TouchLogin(u.ID)
	s.autoGrantUserTrial(r.Context(), u)
	auditTID := u.TenantID
	if auditTID <= 0 {
		auditTID = 1
	}
	s.grantTaskEventOnTenant(r, auditTID, u.ID, store.TaskKeyLoginDaily, "")
	s.Store.LogAudit(auditTID, u.ID, "login", "auth", "用户登录(邮箱)")
	writeJSON(w, 200, map[string]interface{}{
		"success": true,
		"token":   tok,
		"user": map[string]interface{}{
			"id": u.ID, "username": u.Username, "display_name": u.DisplayName,
			"role": u.Role, "tenant_id": u.TenantID, "email": u.Email,
			"must_change_pwd": u.MustChangePwd,
			// ★ 首登资料提醒：可跳过、不是 428 强制闸；与 must_change_pwd 严格分离。
			"profile_setup_required": u.ProfileSetupRequired,
		},
	})
}

// createPersonalByEmail 为未注册邮箱自动创建空白个人账号（个人租户 + tenant_admin）。
// 返回 nil 表示已向客户端写出错误响应。
func (s *Server) createPersonalByEmail(w http.ResponseWriter, r *http.Request, req *loginEmailReq, email string) *store.User {
	// ① 只允许主站：专属品牌域不开放个人注册（用户在租户域应走邀请/企业登录）。
	if resolveDedicatedTenant(s, r) > 0 {
		s.writeError(w, r, apierrors.New(apierrors.ErrForbidden, "该域名不开放个人账号注册，请通过企业邀请加入"))
		return nil
	}
	// ② 注册开关
	if v, _ := s.Store.GetConfig("registration_enabled"); v == "0" {
		s.writeError(w, r, apierrors.New(apierrors.ErrForbidden, "当前暂未开放新账号注册"))
		return nil
	}
	// ③ 协议：自动建号不能静默置 true（用户令 + 审计要求）
	if !req.Agreed {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "请先阅读并同意用户协议与隐私政策"))
		return nil
	}
	// ④ 一次性邮箱黑名单只拦"新建号"，已有账号（上面 err==nil 分支）不拦。
	if msg := s.disposableEmailRejected(email); msg != "" {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, msg))
		return nil
	}
	// ⑤ 人机验证
	if err := s.verifyCaptcha(r, req.CaptchaToken); err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, err.Error()))
		return nil
	}
	// ⑥ 防薅三档与注册同源：IP 日名额/最小间隔 → 设备档 → 平台日名额。
	//   漏掉 IP 这一档时，换设备号即可无限自动建号，另外两档各自只能挡住一半。
	ip := clientIP(r)
	ipDaily := configIntTier(s.Store, "REGISTER_IP_DAILY", "register_ip_daily_limit", regIPDailyDefault, false)
	ipMin := configIntTier(s.Store, "REGISTER_IP_MIN_INTERVAL", "register_ip_min_interval_sec", regIPMinIntervalDef, true)
	if ok, wait := s.regGuard.allow(ip, ipDaily, ipMin); !ok {
		s.writeError(w, r, apierrors.New(apierrors.ErrRateLimited, fmt.Sprintf("注册过于频繁，请 %d 秒后再试", wait)).WithRetryAfter(wait))
		return nil
	}
	device := ""
	if id := strings.TrimSpace(req.DeviceID); trialDeviceIDRe.MatchString(id) {
		device = id
	}
	devLimit := configIntTier(s.Store, "REGISTER_DEVICE_DAILY", "register_device_daily_limit", regDeviceDailyDefault, false)
	globalLimit := configIntTier(s.Store, "REGISTER_GLOBAL_DAILY", "register_global_daily_limit", regGlobalDailyDefault, false)
	ticket, wait, reason := s.regGuard.reserveFreeAccount(device, devLimit, globalLimit)
	if ticket == nil {
		if reason == "global" {
			s.registerBudgetAlert(wait)
			s.writeError(w, r, apierrors.New(apierrors.ErrRateLimited, "今日免费注册名额已用完，请明天再试").WithRetryAfter(wait))
			return nil
		}
		s.writeError(w, r, apierrors.New(apierrors.ErrRateLimited, "该设备今天注册的账号已达上限，请明天再试或登录已有账号").WithRetryAfter(wait).
			WithDetails(map[string]interface{}{"reason": reason}))
		return nil
	}
	defer ticket.release() // 建号成功前任何失败都退格

	// ⑦ 个人租户 + 账号事务（邮箱、协议时间、首登资料标记同一笔落库）
	uname := localPartOfEmail(email)
	if uname == "" {
		uname = "user"
	}
	t, terr := s.Ten.Create("p_"+time.Now().Format("20060102150405.000000000"), uname, "", "{}")
	if terr != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, "创建个人租户失败"))
		return nil
	}
	nu, cerr := s.Store.CreateUserWithEmail(t.ID, uname, "", uname, store.RoleTenantAdmin, 0, 0,
		email, time.Now().Format(time.RFC3339), 1)
	if cerr != nil {
		_ = s.Ten.Delete(t.ID) // 补偿：不留孤儿租户
		s.writeError(w, r, apierrors.New(apierrors.ErrConflict, cerr.Error()))
		return nil
	}
	ticket.markSpent() // 账号已落库：格子坐实（额度发放失败由登录路径补发）
	if err := s.Ten.SetPersonal(t.ID, true); err != nil {
		observability.Warn(r.Context(), "个人租户标记失败", "tid", t.ID, "err", err.Error())
	}
	if err := s.Store.EnsureDefaultPackages(t.ID); err != nil {
		observability.Warn(r.Context(), "个人租户默认知识库包初始化失败", "tid", t.ID, "err", err.Error())
	}
	_ = s.Store.EnsureBalance(t.ID)
	_ = s.Ten.SetIndustry(t.ID, store.GeneralIndustryCode)
	s.autoGrantUserTrial(r.Context(), nu)
	s.regGuard.record(ip)
	s.Store.LogAudit(t.ID, nu.ID, "register", "auth", "邮箱验证码自动建号")
	return nu
}

// localPartOfEmail 取邮箱 @ 前的部分做默认用户名（用户名已不唯一，仅作展示与排障线索）。
func localPartOfEmail(email string) string {
	at := strings.IndexByte(email, '@')
	if at <= 0 {
		return ""
	}
	raw := email[:at]
	var b strings.Builder
	for _, c := range raw {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '.' || c == '-' {
			b.WriteRune(c)
		}
	}
	out := strings.Trim(b.String(), ".-_")
	if len(out) > 32 {
		out = out[:32]
	}
	return out
}
