// ============ register.go · 职责说明 ============
// api 包内部实现文件。
// =============================================
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
	// 专属域名自助注册：从访问 Host 解析目标租户（仅品牌子域、非主站、非默认平台租户）。
	// 命中后注册强制归入该企业且仅为普通成员（禁止建企业/升管理员、免邀请码）。
	dedicatedTid := resolveDedicatedTenant(s, r)
	var req struct {
		Username   string `json:"username"`      // 注册用户名
		Password   string `json:"password"`      // 密码（至少 6 位）
		Code       string `json:"code"`          // 租户编码（无邀请码时必填）
		Name       string `json:"name"`          // 租户名称
		Invite     string `json:"invite"`        // 邀请码（可选）
		Email      string `json:"email"`         // 联系邮箱（找回密码验证码接收）
		EmailCode  string `json:"email_code"`    // 邮箱验证码（email_verify_enabled=1 时必填）
		Captcha    string `json:"captcha_token"` // 人机验证 token（captcha_provider=turnstile 时必填）
		Industry   string `json:"industry"`      // 所属行业（新租户注册时必填，来自行业包 code）
		RoleChoice string `json:"role_choice"`   // 角色选择（兼容旧客户端）：admin=我是管理员(建企业) / user=我是普通用户(邀请码加入)
		Type       string `json:"type"`          // 注册类型：personal=个人用户 / enterprise=企业用户（默认）
		Ref        string `json:"ref"`           // 个人邀请码（可选，邀请裂变：?ref=<个人码> 链接携带）
		// ★ 协议签署（2026-08-27 需求）：注册即视为同意《用户协议》与《隐私协议》，
		//   前端注册表单须勾选后方可提交；勾选时 agreed=true 并随注册写入签署时间。
		Agreed bool `json:"agreed"`
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
	// 协议同意校验：注册必须勾选同意《用户协议》与《隐私协议》
	if !req.Agreed {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请阅读并同意《用户协议》与《隐私协议》"})
		return
	}

	// 试用额度（可配置）：默认 50000 token
	defaultKey := "" // 新租户默认 API Key（明文，仅注册响应返回一次）
	trialTokens := int64(300000)
	trialDays := 14
	if v, _ := s.Store.GetConfig("free_trial_tokens"); v != "" {
		if x, e := strconv.ParseInt(v, 10, 64); e == nil && x > 0 {
			trialTokens = x
		}
	}
	if v, _ := s.Store.GetConfig("free_trial_days"); v != "" {
		if x, e := strconv.Atoi(v); e == nil && x > 0 {
			trialDays = x
		}
	}
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
	// ★ 邮箱必填：所有自助注册路径（含受邀加入）都必须提供邮箱——验证码收件与找回密码依赖
	if strings.TrimSpace(req.Email) == "" {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "邮箱为必填项"})
		return
	}
	// 邮箱全局唯一（他人已绑定则拒绝）
	if other, oerr := s.Store.GetUserByEmail(strings.TrimSpace(req.Email)); oerr == nil && other != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "该邮箱已被其他账号绑定"})
		return
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
	// ★ 注册类型（个人用户 / 企业用户）：
	//   personal = 个人用户：自动建个人租户，好友邀请码经 ref 走邀请裂变（个人用户默认可获奖励）；
	//   enterprise（默认，兼容旧客户端）= 合并原「管理员/普通用户」为单一企业注册，注册人成为企业管理员。
	//   专属域名场景（dedicatedTid>0）忽略类型选择：强制为普通用户、自动归入该企业。
	creatingPersonal := false
	if dedicatedTid == 0 {
		switch strings.ToLower(strings.TrimSpace(req.Type)) {
		case "personal":
			// 个人用户：公开注册不凭企业邀请码加入；好友邀请码经 ref 处理
			req.Invite = ""
			creatingPersonal = true
			// 自动生成个人租户编码/名称（前端不填企业信息）
			req.Code = fmt.Sprintf("p_%d", time.Now().UnixNano())
			if req.Name == "" {
				req.Name = req.Username
			}
			if req.Name == "" {
				req.Name = "个人用户"
			}
		default: // enterprise
			// 企业用户按角色选择区分（需求 7）：
			//  admin  = 我是管理员（新建企业，注册人成为企业管理员，忽略邀请码）
			//  member = 我是普通成员（须凭有效企业邀请码加入；无效/非企业码 → 降级为个人用户，需求 2）
			roleChoice := strings.ToLower(strings.TrimSpace(req.RoleChoice))
			if roleChoice == "member" {
				if code := strings.TrimSpace(req.Invite); code != "" {
					if inv, e := s.Store.GetInviteCodeByCode(code); e == nil && inv.Used == 0 && inv.TenantID > 0 {
						// 有效企业邀请码：保留 invite，后续走受邀加入（普通成员）
					} else {
						// 无效/已用/非企业邀请码：降级为个人用户（强制不得注册该企业用户）
						req.Invite = ""
						creatingPersonal = true
						req.Code = fmt.Sprintf("p_%d", time.Now().UnixNano())
						if req.Name == "" {
							req.Name = req.Username
						}
						if req.Name == "" {
							req.Name = "个人用户"
						}
					}
				} else {
					// 成员模式但未提供邀请码：降级为个人用户
					creatingPersonal = true
					req.Code = fmt.Sprintf("p_%d", time.Now().UnixNano())
					if req.Name == "" {
						req.Name = req.Username
					}
					if req.Name == "" {
						req.Name = "个人用户"
					}
				}
			}
			// admin（或默认）新建企业：忽略邀请码
			if !creatingPersonal {
				req.Invite = ""
			}
		}
	}
	// 专属域名：自动归入对应租户、强制普通用户、忽略邀请码与类型选择
	if dedicatedTid > 0 {
		req.Invite = ""
		inviteTenantID = dedicatedTid
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
		// 标记邀请码已使用（一次性；★ 整改 A4：抢占失败=被并发请求抢先，拒绝本次注册）
		if claimed, merr := s.Store.MarkInviteCodeUsed(inv.ID, req.Username); merr != nil || !claimed {
			writeJSON(w, 400, map[string]interface{}{"success": false, "message": "邀请码无效或已使用"})
			return
		}
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
		// Req8（2026-08-26 UAT 产品决策修订）：行业不再拒绝注册——
		//   缺选 → 通用行业兜底；错选(不存在) → 通用行业兜底；通用包也缺失(异常部署)
		//   → 跳过行业载入不阻塞注册。行业仅决定共享行业包的载入范围。
		industryCode := strings.TrimSpace(req.Industry)
		if industryCode == "" {
			industryCode = store.GeneralIndustryCode
		}
		industryPkg, ipErr := s.Store.FindIndustryByCode(industryCode)
		if ipErr != nil {
			industryPkg, ipErr = s.Store.FindIndustryByCode(store.GeneralIndustryCode)
		}
		if ipErr != nil {
			// 极端兜底：连通用包都缺失（部署异常）——置 nil，下方跳过 SetIndustry
			industryPkg = nil
			s.Store.LogAudit(0, 0, "register_industry_missing", "kb_packages",
				"通用行业包缺失，注册未载入行业(租户编码:"+req.Code+")")
		}
		// 权限：试用每日上限 2 万字符 + 2 万 token（D4 token 口径优先）；审核模式下不预发
		perms := &tenant.Perms{MaxDailyChars: 20000, MaxDailyTokens: 20000}
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
		// 行业包单轨制：仅记录注册所选行业编码（含通用兜底），内容从共享宿主（租户1）
		// 按行业载入，不再建空壳包；industryPkg=nil（异常部署）时跳过
		if industryPkg != nil {
			_ = s.Ten.SetIndustry(inviteTenantID, industryPkg.Code)
		}
		_ = s.Store.EnsureBalance(inviteTenantID)
		if trialTokens > 0 && !reviewMode {
			if gerr := s.Store.CreateQuotaGrant(inviteTenantID, "trial", trialTokens, time.Now().Add(time.Duration(trialDays)*24*time.Hour), "register", 0); gerr != nil {
				_ = s.Store.Charge(inviteTenantID, trialTokens) // 台账失败兜底旧通道
			}
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
	// 个人用户租户标记（企业用户默认 is_personal=0，无需额外处理）。
	// 个人用户默认可参与邀请好友奖励；企业用户默认不参与（见下方 ref 奖励门禁）。
	if creatingPersonal && inviteTenantID > 0 {
		_ = s.Ten.SetPersonal(inviteTenantID, true)
	}

	// 3. 创建账号：独立租户 → tenant_admin；受邀加入 → user；专属域名 → 强制普通用户
	role := store.RoleTenantAdmin
	if inviteTenantID > 0 && s.wasInviteBind(req.Invite) {
		role = store.RoleUser
	}
	if dedicatedTid > 0 {
		role = store.RoleUser
	}
	// ★ 邀请码绑定组织：受邀用户归入邀请码指定的组织层级（四期）
	inviteOrgID := int64(0)
	if req.Invite != "" {
		if inv, err := s.Store.GetInviteCodeByCode(strings.TrimSpace(req.Invite)); err == nil {
			inviteOrgID = inv.OrgID
		}
	}
	nu, err := s.Store.CreateUser(inviteTenantID, req.Username, auth.PasswordHash(req.Password), req.Username, role, 0, inviteOrgID)
	if err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "用户创建失败: " + err.Error()})
		return
	}
	// 记录协议签署时间（注册勾选即视为同意《用户协议》与《隐私协议》，给后台超管「协议签署」tab 留痕）
	if req.Agreed {
		now := time.Now().Format(time.RFC3339)
		_ = s.Store.SetUserAgreed(nu.ID, inviteTenantID, now)
		nu.AgreedAt = now
	}
	// 绑定联系邮箱（用于找回密码）
	if req.Email != "" {
		_ = s.Store.SetUserEmail(nu.ID, inviteTenantID, strings.TrimSpace(req.Email))
		nu.Email = strings.TrimSpace(req.Email)
	}
	// ★ 注册成功自动发送《产品手册》PDF 邮件（个人/企业用户均发送；用 info 专用邮箱，附件为手册 PDF）
	if req.Email != "" {
		go func(to, uname string) {
			_ = s.sendManualEmail(to, uname)
		}(strings.TrimSpace(req.Email), req.Username)
	}
	// ★ 企业注册提醒（需求 2026-08-27）：企业用户注册成功后，向注册人发送欢迎邮件
	//   并抄送运营邮箱（抄送地址由邮件模板 enterprise_reg 控制，默认 575160894@qq.com）建联。
	//   仅「新建企业（非个人、非受邀加入、非专属域名）」触发；个人用户不抄送。
	if !creatingPersonal && dedicatedTid == 0 && req.Invite == "" && req.Email != "" {
		go func(to, name, username string) {
			_ = s.sendTemplatedMail(to, "enterprise_reg", map[string]string{
				"name":     name,
				"username": username,
				"email":    to,
			})
		}(strings.TrimSpace(req.Email), req.Name, req.Username)
	}
	// ★ 新租户默认 API Key（2026-08-26 冒烟修复）：移到建号之后签发并强绑定创建人——
	//   原「先发 Key 后建号」产生 user_id=0 的孤儿 Key，被 authenticateAPIKey
	//   「Key 必须归属用户」闸门拦截，新注册租户的 Key 要重启服务（backfill）后才生效。
	//   专属域名注册视为「加入已有企业」，不签发独立 API Key（与邀请码加入保持一致）。
	if inviteTenantID > 0 && req.Invite == "" && dedicatedTid == 0 {
		defaultKey = s.issueDefaultAPIKeyFor(inviteTenantID, nu.ID, "默认 Key")
	}
	// ★ 邀请裂变首绑（白皮书 §5）：携带个人邀请码注册→写入 referred_by（首绑闸门），
	//   绑定成功即给邀请人叠加体验奖励：+invite_reward_tokens、时长 +invite_extend_days（与既有体验到期取大后叠加，按对去重）
	//   ★ 总开关门禁（2026-08-26 U3）：referral_enabled=0 时跳过绑定与奖励发放
	if strings.TrimSpace(req.Ref) != "" && s.Store.ReferralEnabled() {
		if inviterUID, inviterTID, ok := s.Store.BindReferral(nu.ID, nu.TenantID, strings.TrimSpace(req.Ref)); ok {
			// ★ 需求 2026-08-27：企业用户默认不参与邀请好友奖励；仅个人用户邀请人可获奖励。
			inviterPersonal := false
			if it, ie := s.Ten.GetByID(inviterTID); ie == nil {
				inviterPersonal = it.IsPersonal
			}
			if !inviterPersonal {
				s.Store.LogAudit(inviteTenantID, nu.ID, "referral_bind", "user",
					fmt.Sprintf("受邀绑定邀请人 uid=%d（企业用户，不参与邀请奖励）", inviterUID))
			} else {
				refTokens := int64(300000)
				if v, _ := s.Store.GetConfig("invite_reward_tokens"); v != "" {
					if x, e := strconv.ParseInt(v, 10, 64); e == nil && x > 0 {
						refTokens = x
					}
				}
				refDays := 14
				if v, _ := s.Store.GetConfig("invite_extend_days"); v != "" {
					if x, e := strconv.Atoi(v); e == nil && x > 0 {
						refDays = x
					}
				}
				// ★ 整改 A5：单邀请人日发放上限（referral_max_daily_rewards，默认 50 笔）——
				//   把「换 IP 自邀刷奖励」的损失上限钉死为可配置常数；触顶升 critical 告警拒发。
				//   配套 U3 总开关（referral_enabled=0 全量停发）构成两级防薅。
				maxDaily := int64(50)
				if v, _ := s.Store.GetConfig("referral_max_daily_rewards"); v != "" {
					if x, e := strconv.ParseInt(v, 10, 64); e == nil && x > 0 {
						maxDaily = x
					}
				}
				if n := s.Store.CountInviterRewardsToday(inviterUID); n >= maxDaily {
					s.Store.CreateAlert(inviterTID, "critical", "referral_cap",
						fmt.Sprintf("邀请人 uid=%d 今日奖励已达上限 %d 笔，本笔(+%d token/%d天)已拒发，请人工核实",
							inviterUID, maxDaily, refTokens, refDays))
				} else {
					_ = s.Store.GrantTrialStack(inviterUID, inviterTID, nu.ID, refTokens, refDays)
					s.Store.LogAudit(inviteTenantID, nu.ID, "referral_bind", "user", fmt.Sprintf("受邀绑定邀请人 uid=%d", inviterUID))
				}
			}
		}
	}
	// 清空密码哈希后返回
	nu.PasswordHash = ""
	// 注册审计
	auditRole := "user"
	if inviteTenantID == 0 || !s.wasInviteBind(req.Invite) {
		auditRole = "admin"
	}
	if dedicatedTid > 0 {
		auditRole = "user"
	}
	s.Store.LogAudit(inviteTenantID, nu.ID, "register", "auth", "自助注册 role="+auditRole)
	// 登记注册成功（推进同 IP 频率窗口）
	s.regGuard.record(ip)
	// 成功提示：审核模式下提示等待发放
	msg := "注册成功，试用额度已发放"
	if reviewMode && req.Invite == "" {
		msg = "注册成功，账号已创建；管理员审核后将发放试用额度"
	}
	regResp := map[string]interface{}{
		"success":   true,
		"message":   msg,
		"user":      nu,
		"tenant_id": inviteTenantID,
		"tenant":    tenantInfo,
	}
	// ★ 新租户默认 API Key：明文仅此一次随注册响应返回（受邀加入为空串）
	if defaultKey != "" {
		regResp["api_key"] = defaultKey
	}
	writeJSON(w, 200, regResp)
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
	trialTokens := int64(300000)
	if v, _ := s.Store.GetConfig("free_trial_tokens"); v != "" {
		if tv, perr := strconv.ParseInt(v, 10, 64); perr == nil && tv > 0 {
			trialTokens = tv
		}
	}
	trialDays := 14
	if v, _ := s.Store.GetConfig("free_trial_days"); v != "" {
		if d2, perr := strconv.Atoi(v); perr == nil && d2 > 0 {
			trialDays = d2
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
		if gerr := s.Store.CreateQuotaGrant(t.ID, "trial", trialTokens, time.Now().Add(time.Duration(trialDays)*24*time.Hour), "register", 0); gerr != nil {
			_ = s.Store.Charge(t.ID, trialTokens)
		}
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
				_ = s.sendTemplatedMail(to, "tenant_notify", map[string]string{
					"title": title,
					"body":  body,
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
	if _, err := s.requireTenantAdmin(r); err != nil {
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
	u, err := s.requireTenantAdmin(r)
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
