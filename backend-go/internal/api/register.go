package api

// ============ 本文件职责中文说明 ============
// 本文件实现自助注册与试用额度发放、邀请码管理：
//   - handleRegister：自助注册（无邀请码→新建独立试用租户 + tenant_admin 账号；有绑定租户的邀请码→加入已有租户 + user 账号）
//   - 试用额度：新租户默认发放 trial_tokens（system_config 可配置，默认 50000 token）并写入每日字符上限权限
//   - 注册开关：system_config registration_enabled=0 时关闭注册
//   - 邀请码管理（super_admin）：列表 / 创建（handleInviteCodes / handleInviteCodeCreate）
// 安全要点：邀请码一次性使用（used=1 即失效）；受邀加入前校验该租户用户名唯一。

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"translator/internal/auth"
	"translator/internal/mail"
	"translator/internal/store"
	"translator/internal/tenant"
)

// ============ 自助注册 + 试用额度 ============

// handleRegister 自助注册接口：
//   - 不携带邀请码：创建独立租户（试用额度）→ 创建该租户 tenant_admin 账号
//   - 携带邀请码且邀请码绑定租户：加入已有租户（创建 user 账号）
//   - 试用额度默认发放（system_config trial_tokens，默认 50000），并在 permissions 记录 max_daily_chars
//
// 参数 w: HTTP 响应写入器；r: HTTP 请求（body 含 username/password/code/name/invite）。
// 返回: success=true 时携带新用户、tenant_id 与提示信息。
func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	// 平台未初始化（缺存储/租户存储）时拒绝注册
	if s.Store == nil || s.Ten == nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "平台未初始化"})
		return
	}
	// 注册开关：默认开放（system_config registration_enabled=0 时关闭）
	if v, _ := s.Store.GetConfig("registration_enabled"); v == "0" {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": "注册已关闭，请联系管理员开通"})
		return
	}
	// 注册频率护栏：同 IP 24h 内注册次数上限 + 最小间隔（防脚本批量薅试用额度）
	ip := clientIP(r)
	dailyLimit := 3
	if v, _ := s.Store.GetConfig("register_ip_daily_limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			dailyLimit = n
		}
	}
	minInterval := 60
	if v, _ := s.Store.GetConfig("register_ip_min_interval_sec"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			minInterval = n
		}
	}
	if ok, wait := s.regGuard.allow(ip, dailyLimit, minInterval); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(wait))
		writeJSON(w, 429, map[string]interface{}{"success": false,
			"message": fmt.Sprintf("注册过于频繁，请 %d 秒后再试", wait)})
		return
	}
	var req struct {
		Username  string `json:"username"`      // 注册用户名
		Password  string `json:"password"`      // 密码（至少 6 位）
		Code      string `json:"code"`          // 租户编码（无邀请码时必填）
		Name      string `json:"name"`          // 租户名称
		Invite    string `json:"invite"`        // 邀请码（可选）
		Email     string `json:"email"`         // 联系邮箱（找回密码验证码接收）
		EmailCode string `json:"email_code"`    // 邮箱验证码（email_verify_enabled=1 时必填）
		Captcha   string `json:"captcha_token"` // 人机验证 token（captcha_provider=turnstile 时必填）
		Industry  string `json:"industry"`      // 所属行业（新租户注册时必填，来自行业包 code）
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	// 字段去首尾空白
	req.Username = strings.TrimSpace(req.Username)
	req.Password = strings.TrimSpace(req.Password)
	req.Code = strings.TrimSpace(req.Code)
	req.Name = strings.TrimSpace(req.Name)
	// 基础校验：用户名密码必填
	if req.Username == "" || req.Password == "" {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "用户名和密码不能为空"})
		return
	}
	// 密码强度：至少 6 位
	if len(req.Password) < 6 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "密码至少 6 位"})
		return
	}

	// 试用额度（可配置）：默认 50000 token
	trialTokens := int64(50000)
	if v, _ := s.Store.GetConfig("trial_tokens"); v != "" {
		if tv, err := strconv.ParseInt(v, 10, 64); err == nil && tv > 0 {
			trialTokens = tv
		}
	}
	// 试用句数（可配置）：默认 500 句（每源句 × 目标语言数）
	trialSentences := int64(100)
	if v, _ := s.Store.GetConfig("trial_sentences"); v != "" {
		if sv, err := strconv.ParseInt(v, 10, 64); err == nil && sv > 0 {
			trialSentences = sv
		}
	}

	// 1. 处理邀请码（可选）：校验有效且未使用，标记为已使用
	inviteTenantID := int64(0)
	// 注册审核开关（registration_review=1）：新建试用租户暂不发放体验额度，
	// 由超管审核后经 /api/admin/tenants/grant-trial 手动发放（受邀加入不受影响）
	reviewMode := false
	if v, _ := s.Store.GetConfig("registration_review"); v == "1" {
		reviewMode = true
	}
	// 邮箱验证开关（email_verify_enabled=1）：自助注册必须先验证邮箱归属（受邀加入不受影响）
	verifyOn := false
	if v, _ := s.Store.GetConfig("email_verify_enabled"); v == "1" {
		verifyOn = true
	}
	if verifyOn && req.Invite == "" {
		if strings.TrimSpace(req.Email) == "" || strings.TrimSpace(req.EmailCode) == "" {
			writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请填写邮箱并输入验证码"})
			return
		}
		if !verifyEmailCode(req.Email, req.EmailCode) {
			writeJSON(w, 400, map[string]interface{}{"success": false, "message": "邮箱验证码错误或已过期"})
			return
		}
	}
	// 人机验证（captcha_provider=turnstile 时校验；自助注册与受邀加入均拦截）
	if err := s.verifyCaptcha(r, req.Captcha); err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	if req.Invite != "" {
		inv, err := s.Store.GetInviteCodeByCode(strings.TrimSpace(req.Invite))
		if err != nil || inv.Used == 1 {
			writeJSON(w, 400, map[string]interface{}{"success": false, "message": "邀请码无效或已使用"})
			return
		}
		inviteTenantID = inv.TenantID
		// 绑定租户的邀请码：先校验该租户下用户名是否已存在（防重复）
		if inviteTenantID > 0 {
			if _, err := s.Store.GetUserByUsername(inviteTenantID, req.Username); err == nil {
				writeJSON(w, 400, map[string]interface{}{"success": false, "message": "该租户下用户名已存在"})
				return
			}
		}
		// 标记邀请码已使用（一次性）
		_ = s.Store.MarkInviteCodeUsed(inv.ID, req.Username)
	}

	// 2. 创建租户（无绑定租户时新建独立试用租户）
	tenantInfo := map[string]interface{}{}
	if inviteTenantID == 0 {
		if req.Code == "" {
			writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请提供租户编码"})
			return
		}
		if req.Name == "" {
			req.Name = req.Code
		}
		// Req8：完全新租户注册需选择行业（来自超管创建的行业包），用于开通对应行业包
		if req.Industry == "" {
			writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请选择所属行业"})
			return
		}
		// 校验行业包存在（来自默认租户 tenant 1 的行业包）
		industryPkg, ipErr := s.Store.FindIndustryByCode(req.Industry)
		if ipErr != nil {
			writeJSON(w, 400, map[string]interface{}{"success": false, "message": "行业不存在，请重新选择"})
			return
		}
		// 权限：试用每日上限 2 万字符；审核模式下不预发试用句数（package_code 留空=未开通）
		perms := &tenant.Perms{MaxDailyChars: 20000}
		if !reviewMode {
			perms.SentenceBalance = trialSentences
			perms.PackageCode = "trial"
		}
		pb, _ := json.Marshal(perms)
		t, err := s.Ten.Create(req.Code, req.Name, "", string(pb))
		if err != nil {
			writeJSON(w, 400, map[string]interface{}{"success": false, "message": "租户创建失败: " + err.Error()})
			return
		}
		inviteTenantID = t.ID
		tenantInfo = map[string]interface{}{
			"id": t.ID, "code": t.Code, "name": t.Name, "status": t.Status,
			"expires_at": t.ExpiresAt, "permissions": t.Permissions,
		}
		// 初始化租户：默认 KB 包（组织包/部门包/语言文化包）+ 余额账户
		_ = s.Store.EnsureDefaultPackages(inviteTenantID)
		// 行业包单轨制：仅记录注册所选行业编码，内容从共享宿主（租户1）按行业载入，不再建空壳包
		_ = s.Ten.SetIndustry(inviteTenantID, industryPkg.Code)
		// 发放试用余额：确保有余额账户记录后充值 trial_tokens（审核模式下暂不充值）
		_ = s.Store.EnsureBalance(inviteTenantID)
		if trialTokens > 0 && !reviewMode {
			_ = s.Store.Charge(inviteTenantID, trialTokens)
		}
	} else {
		// 受邀加入已有租户：返回被加入租户信息
		if t, err := s.Ten.GetByID(inviteTenantID); err == nil {
			tenantInfo = map[string]interface{}{
				"id": t.ID, "code": t.Code, "name": t.Name, "status": t.Status,
				"expires_at": t.ExpiresAt, "permissions": t.Permissions,
			}
		}
	}

	// 3. 创建账号：独立租户 → tenant_admin；受邀加入 → user
	role := store.RoleTenantAdmin
	if inviteTenantID > 0 && s.wasInviteBind(req.Invite) {
		role = store.RoleUser
	}
	nu, err := s.Store.CreateUser(inviteTenantID, req.Username, auth.PasswordHash(req.Password), req.Username, role, 0, 0)
	if err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "用户创建失败: " + err.Error()})
		return
	}
	// 绑定联系邮箱（用于找回密码）
	if req.Email != "" {
		_ = s.Store.SetUserEmail(nu.ID, inviteTenantID, strings.TrimSpace(req.Email))
		nu.Email = strings.TrimSpace(req.Email)
	}
	// 清空密码哈希后返回
	nu.PasswordHash = ""
	// 注册审计
	s.Store.LogAudit(inviteTenantID, nu.ID, "register", "auth", "自助注册")
	// 登记注册成功（推进同 IP 频率窗口）
	s.regGuard.record(ip)
	// 成功提示：审核模式下提示等待发放
	msg := "注册成功，试用额度已发放"
	if reviewMode && req.Invite == "" {
		msg = "注册成功，账号已创建；管理员审核后将发放试用额度"
	}
	writeJSON(w, 200, map[string]interface{}{
		"success":   true,
		"message":   msg,
		"user":      nu,
		"tenant_id": inviteTenantID,
		"tenant":    tenantInfo,
	})
}

// handleGrantTrial 超管向待审核租户发放试用额度（幂等）：
//   - 幂等校验：租户已有 package_code（已开通/已订阅）时拒绝重复发放
//   - 发放内容：trial_sentences 句数 + trial_tokens 余额，并置 package_code="trial"
//
// 参数 w: HTTP 响应写入器；r: HTTP 请求（body 含 tenant_id，需 super_admin）。
// 返回: success=true 时携带发放后的句数余额。
func (s *Server) handleGrantTrial(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireAdminUser(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		TenantID int64 `json:"tenant_id"` // 待发放的租户 ID
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.TenantID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "无效的租户 ID"})
		return
	}
	t, err := s.Ten.GetByID(req.TenantID)
	if err != nil {
		writeJSON(w, 404, map[string]interface{}{"success": false, "message": "租户不存在"})
		return
	}
	// 幂等：已有任何包身份（含 trial）即拒绝重复发放
	perms := tenant.ParsePerms(t.Permissions)
	if perms.PackageCode != "" {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "该租户已开通试用或已订阅商业包，无需重复发放"})
		return
	}
	// 读取发放配置（与注册路径同一套配置键）
	trialSentences := int64(100)
	if v, _ := s.Store.GetConfig("trial_sentences"); v != "" {
		if sv, perr := strconv.ParseInt(v, 10, 64); perr == nil && sv > 0 {
			trialSentences = sv
		}
	}
	trialTokens := int64(50000)
	if v, _ := s.Store.GetConfig("trial_tokens"); v != "" {
		if tv, perr := strconv.ParseInt(v, 10, 64); perr == nil && tv > 0 {
			trialTokens = tv
		}
	}
	// 发放句数并置试用包身份
	perms.SentenceBalance = trialSentences
	perms.PackageCode = "trial"
	perms.SubscribedAt = time.Now().Format(time.RFC3339)
	pb, _ := json.Marshal(perms)
	if err := s.Ten.Update(t.ID, t.Name, t.ExpiresAt, string(pb)); err != nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	// 充值 token 余额
	_ = s.Store.EnsureBalance(t.ID)
	if trialTokens > 0 {
		_ = s.Store.Charge(t.ID, trialTokens)
	}
	// 审计 + 通知租户管理员
	s.Store.LogAuditDiff(s.effTenant(r, u), u.ID, "grant_trial", "tenant", strconv.FormatInt(t.ID, 10),
		`{"package_code":""}`, `{"package_code":"trial","sentences":`+strconv.FormatInt(trialSentences, 10)+`}`)
	s.notifyTenantAdmins(t.ID, "试用额度已发放", "您的企业工作台已开通，试用句数 "+strconv.FormatInt(trialSentences, 10)+" 句已到账。")
	writeJSON(w, 200, map[string]interface{}{"success": true, "sentence_balance": perms.SentenceBalance})
}

// notifyTenantAdmins 向指定租户的全部租户管理员发送站内通知；email_notify_enabled=1 时
// 同时向其绑定邮箱发送邮件触达（找回密码同链路，SMTP 未配置时 Noop 打印日志）。
// 参数 tid: 租户 ID；title/body: 通知标题与正文。
func (s *Server) notifyTenantAdmins(tid int64, title, body string) {
	users, err := s.Store.ListUsers(tid)
	if err != nil {
		return
	}
	emailOn, _ := s.Store.GetConfig("email_notify_enabled")
	for _, usr := range users {
		if usr.Role != store.RoleTenantAdmin {
			continue
		}
		_ = s.Store.CreateNotification(usr.ID, title, body, "tenant", tid)
		if emailOn == "1" && usr.Email != "" {
			go func(to string) {
				_ = s.mailer().Send(&mail.Message{
					To:      to,
					Subject: "【翻译助手】" + title,
					Body:    body,
				})
			}(usr.Email)
		}
	}
}

// hasOpenAlert 判断租户是否存在指定类型的未处理告警（邮件触达去重依据）。
func (s *Server) hasOpenAlert(tid int64, kind string) bool {
	alerts, err := s.Store.ListAlerts(tid, "open", 50)
	if err != nil {
		return false
	}
	for _, a := range alerts {
		if a.Kind == kind {
			return true
		}
	}
	return false
}

// wasInviteBind 判断是否通过绑定租户的邀请码加入（用于角色判定）。
// 参数 invite: 邀请码字符串；返回: true 表示该邀请码绑定了具体租户（受邀加入），false 表示未绑定（新建租户）。
func (s *Server) wasInviteBind(invite string) bool {
	if invite == "" {
		return false
	}
	inv, err := s.Store.GetInviteCodeByCode(strings.TrimSpace(invite))
	if err != nil {
		return false
	}
	// 邀请码 TenantID>0 即视为绑定租户
	return inv.TenantID > 0
}

// handleInviteCodes 邀请码列表接口（super_admin）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（需 admin 权限）。
// 返回: success=true 时携带 codes 数组。
func (s *Server) handleInviteCodes(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireAdminUser(r); err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	codes, err := s.Store.ListInviteCodes()
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "codes": codes})
}

// handleInviteCodeCreate 创建邀请码接口（super_admin）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（body 含 code/tenant_id）。
// 返回: success=true 时携带新建邀请码对象。
func (s *Server) handleInviteCodeCreate(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireAdminUser(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		Code     string `json:"code"`      // 邀请码字符串（必填）
		TenantID int64  `json:"tenant_id"` // 0=新建租户；>0=绑定已有租户（受邀加入）
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Code) == "" {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "邀请码不能为空"})
		return
	}
	c, err := s.Store.CreateInviteCode(strings.TrimSpace(req.Code), req.TenantID)
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": "创建失败: " + err.Error()})
		return
	}
	// 创建审计
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "invite_create", "invite", req.Code)
	writeJSON(w, 200, map[string]interface{}{"success": true, "code": c})
}
