// ============ email_verify.go · 职责说明 ============
// api 包内部实现文件。
// =============================================
package api

// ============ 本文件职责中文说明 ============
// 注册邮箱验证（第二批·轻第三方）：自助注册前先验证邮箱归属，堵住脚本批量
// 注册薅体验额度的口子。
//   - handleEmailCode：POST /api/auth/email-code —— 发送 6 位验证码到邮箱
//     （同邮箱 60s 冷却；MAIL_ENABLED 未开启时走 NoopSender 打印日志，响应带 noop=true）
//   - verifyEmailCode：校验并一次性消费验证码（10 分钟有效，最多 5 次错误尝试）
//   - handleRegisterConfig：GET /api/auth/register-config —— 公开注册配置
//     （email_verify_enabled），前端据此显隐验证码输入框
// 验证码仅存内存（重启失效，短时效无需持久化）。
// =============================================

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	apierrors "translator/internal/errors"
	"translator/internal/mail"
)

// emailCode 单邮箱的验证码状态
type emailCode struct {
	Code      string    // 6 位数字码
	Purpose   string    // 用途：register/login/password/email_change（★ 2026-10-21 IAM 重做新增）
	ExpiresAt time.Time // 过期时间（10 分钟）
	Attempts  int       // 已错误尝试次数（≥5 作废防爆破）
	SentAt    time.Time // 发送时间（冷却判断）
}

// 邮箱验证码用途白名单。★ 用途必须隔离：登录码不能拿去改密、注册码不能拿去登录——
// 旧实现只有「一个邮箱一个码」，谁先发谁覆盖，用户在两个页面同时操作时会互相烧码。
// 新增用途必须同时进白名单，否则 handleEmailCode 直接 400（不给未知字符串拼进缓存键）。
const (
	emailPurposeRegister    = "register"
	emailPurposeLogin       = "login"
	emailPurposePassword    = "password"
	emailPurposeEmailChange = "email_change"
)

// emailCodeKey 把（邮箱,用途）折成存储键。register 沿用历史裸邮箱键，
// 保证老测试/老 Redis 行仍可读；其余用途用 "<purpose>|<邮箱>" 前缀隔离。
func emailCodeKey(email, purpose string) string {
	email = strings.ToLower(strings.TrimSpace(email))
	if purpose == "" || purpose == emailPurposeRegister {
		return email
	}
	return purpose + "|" + email
}

// validEmailPurpose 校验用途是否在白名单内（空串等价 register，兼容老客户端）。
func validEmailPurpose(p string) bool {
	switch p {
	case "", emailPurposeRegister, emailPurposeLogin, emailPurposePassword, emailPurposeEmailChange:
		return true
	}
	return false
}

// emailVerifyStore 邮箱验证码存储（★ 2026-09-09 技术债①P1：底层改用 emailCodeStore——
// Redis 键值 + 内存兜底跨实例共享；此结构保留历史字段与锁语义，读写委托 store）。
type emailVerifyStore struct {
	mu    sync.Mutex
	codes map[string]*emailCode // key: 小写邮箱（本地镜像；跨实例一致由 emailCodeStore 保证）
}

// 全局单例：与进程内 Redis 镜像层同生命周期（进程内存 + Redis 共享）
var emailCodes = &emailVerifyStore{codes: map[string]*emailCode{}}

// 邮箱格式宽松校验（本地@域名，不做 TLD 强校验）
var emailRe = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

// 常量：有效期 / 冷却 / 最大错误尝试
const (
	emailCodeTTL       = 10 * time.Minute
	emailCodeCooldown  = 60 * time.Second
	emailCodeMaxTries  = 5
	emailSendIPDailyNm = 20 // 单 IP 每日发码上限
)

// sendEmailCode 为指定邮箱生成并发送验证码；返回 (是否受理, 提示信息, 是否 Noop 模式)。
// 参数：s=服务（用于 mailer 与 IP 日上限）；ip=请求方 IP；email=目标邮箱；
// lang=收件界面语种（★ F-17 批E：12 码白名单，空=中文链路；验证码模版非中文回落英文稿）。
func (s *Server) sendEmailCode(ip, email, lang string) (bool, string, bool) {
	return s.sendEmailCodeFor(ip, email, lang, emailPurposeRegister)
}

// sendEmailCodeFor 是带用途的发码实现；sendEmailCode 保留为注册用途的薄包装。
func (s *Server) sendEmailCodeFor(ip, email, lang, purpose string) (bool, string, bool) {
	if purpose == "" {
		purpose = emailPurposeRegister
	}
	key := emailCodeKey(email, purpose)
	now := time.Now()
	// ★ 2026-09-09 技术债①：冷却判断/写入经 vcodeGet/vcodeSet（Redis 优先，本地 map 镜像）
	ekey := vcodeEmailKey(key)
	// 检查冷却期：60秒内不允许重发（本地 map 读取；Redis 命中时同步镜像到本地）
	var ec emailCode
	if b, ok := vcodeGet(ekey); ok && json.Unmarshal(b, &ec) == nil {
		if now.Sub(ec.SentAt) < emailCodeCooldown {
			wait := int((emailCodeCooldown - now.Sub(ec.SentAt)).Seconds()) + 1
			return false, fmt.Sprintf("发送过于频繁，请 %d 秒后再试", wait), false
		}
	} else {
		emailCodes.mu.Lock()
		if e, ok := emailCodes.codes[key]; ok && now.Sub(e.SentAt) < emailCodeCooldown {
			wait := int((emailCodeCooldown - now.Sub(e.SentAt)).Seconds()) + 1
			emailCodes.mu.Unlock()
			return false, fmt.Sprintf("发送过于频繁，请 %d 秒后再试", wait), false
		}
		emailCodes.mu.Unlock()
	}
	// 生成6位数字验证码
	code, err := genResetCode()
	if err != nil {
		return false, "生成验证码失败", false
	}
	// 存储验证码：设置10分钟有效期（本地 map + Redis 双写）
	ec = emailCode{Code: code, ExpiresAt: now.Add(emailCodeTTL), SentAt: now}
	emailCodes.mu.Lock()
	emailCodes.codes[key] = &ec
	emailCodes.mu.Unlock()
	if b, e := json.Marshal(ec); e == nil {
		vcodeSet(ekey, b, emailCodeTTL)
	}

	// 判断是否为 Noop 模式（测试环境）
	_, isNoop := s.mailer().(*mail.NoopSender)
	// 发送邮件：使用注册验证码模板。
	// ★ 收件人必须传真实 email 而非存储键 key——key 在 register 以外用途带
	//   "password|<邮箱>" 前缀，传 key 会把验证码寄给一个不存在地址（IAM 重做批发现）。
	err = s.sendTemplatedMail(email, "register_code", lang, map[string]string{"code": code})
	if err != nil {
		return false, "邮件发送失败，请稍后重试", isNoop
	}
	if isNoop {
		return true, "验证码已生成（测试模式，请查看服务端日志）", true
	}
	return true, "验证码已发送，请查收邮箱", false
}

// verifyEmailCode 校验并一次性消费验证码；错误尝试超限作废。
func verifyEmailCode(email, code string) bool {
	return verifyEmailCodeFor(email, code, emailPurposeRegister)
}

// verifyEmailCodeFor 按用途校验并一次性消费验证码；用途不同互不覆盖。
func verifyEmailCodeFor(email, code, purpose string) bool {
	return verifyEmailCodeImpl(email, code, purpose, true)
}

// verifyEmailCodeKeep 验证但**不作废**——供「先改密成功、后作废码」的链路使用（方案 P0-14）。
// ⚠️ 调用方必须在业务动作成功后调 consumeEmailCode，否则该码在 TTL 内可被重复使用。
// 用它的是 handleChangePassword：旧形态「验证即删」会把写库失败（err / 0 行）时的码一起烧掉，
// 用户明明没改成功却要重新发一次码——这正是 P0-14 要禁的那件事。
func verifyEmailCodeKeep(email, code, purpose string) bool {
	return verifyEmailCodeImpl(email, code, purpose, false)
}

// consumeEmailCode 作废指定用途的验证码（本地 + Redis 双删，与 verify 成功分支同一口径）。
func consumeEmailCode(email, purpose string) {
	if purpose == "" {
		purpose = emailPurposeRegister
	}
	key := emailCodeKey(email, purpose)
	emailCodes.mu.Lock()
	delete(emailCodes.codes, key)
	emailCodes.mu.Unlock()
	vcodeDel(vcodeEmailKey(key))
}

// verifyEmailCodeImpl 是三个对外入口共用的实现：
//   - verifyEmailCode / verifyEmailCodeFor：consume=true，验证通过即作废（注册/登录语义，一次性）；
//   - verifyEmailCodeKeep：consume=false，留给调用方在业务成功后自行 consumeEmailCode（改密语义，P0-14）。
func verifyEmailCodeImpl(email, code, purpose string, consume bool) bool {
	if purpose == "" {
		purpose = emailPurposeRegister
	}
	key := emailCodeKey(email, purpose)
	// ★ P1-12 修复（2026-09-14）：per-key 互斥锁串行化「读→判→比→错计/消费」，
	//   消除分段锁 + Redis 弱一致下的并发穷举窗口（与 handleResetPassword 同口径）。
	unlock := vcodeLockOf("email:" + key)
	defer unlock()
	// ★ 2026-09-09 技术债①：读优先 Redis（跨实例），未命中回退本地 map；错计双写、消费双删
	ekey := vcodeEmailKey(key)
	var ec emailCode
	if b, ok := vcodeGet(ekey); ok && json.Unmarshal(b, &ec) == nil {
		// Redis 命中即用；逻辑同下（统一走下方判定）
	} else {
		emailCodes.mu.Lock()
		e, ok := emailCodes.codes[key]
		emailCodes.mu.Unlock()
		if ok {
			ec = *e
		} else {
			return false
		}
	}
	if ec.Attempts >= emailCodeMaxTries || time.Now().After(ec.ExpiresAt) {
		return false
	}
	if ec.Code != strings.TrimSpace(code) {
		ec.Attempts++
		emailCodes.mu.Lock()
		emailCodes.codes[key] = &ec
		emailCodes.mu.Unlock()
		if b, e := json.Marshal(ec); e == nil {
			vcodeSet(ekey, b, emailCodeTTL)
		}
		return false
	}
	// 验证通过即消费：本地 + Redis 双删（consume=false 时留给调用方在业务成功后自己删）
	if consume {
		emailCodes.mu.Lock()
		delete(emailCodes.codes, key)
		emailCodes.mu.Unlock()
		vcodeDel(ekey)
	}
	return true
}

// handleEmailCode 发送注册验证码接口（公开；受注册频率护栏同款 IP 日上限约束）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（body 含 email）。
// 返回: success=true 时已发送；noop=true 表示 MAIL_ENABLED 未开启（码打印在服务端日志）。
func (s *Server) handleEmailCode(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email        string `json:"email"`
		Purpose      string `json:"purpose"`       // 用途：register/login/password/email_change（空=register，兼容老客户端）
		CaptchaToken string `json:"captcha_token"` // 人机验证 token（启用 turnstile 时必填）
		AppLang      string `json:"app_lang"`      // ★ F-17（批E）：发送界面语种（可空；回落 X-App-Lang 头）
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "请求格式错误"))
		return
	}
	email := strings.TrimSpace(req.Email)
	if !emailRe.MatchString(email) {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "邮箱格式不正确"))
		return
	}
	if !validEmailPurpose(req.Purpose) {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "验证码用途无效"))
		return
	}
	if req.Purpose == "" {
		req.Purpose = emailPurposeRegister
	}
	// 改密/换绑邮箱的验证码只发给**已登录的本人**：不允许匿名给任意邮箱发"改密码"。
	if req.Purpose == emailPurposePassword || req.Purpose == emailPurposeEmailChange {
		u := s.authUser(r)
		if u == nil {
			s.writeError(w, r, apierrors.New(apierrors.ErrUnauthorized, "请先登录"))
			return
		}
		if req.Purpose == emailPurposePassword {
			req.Email = u.Email
		}
	}
	// ★ 密码用途的收件人取自本人账号，必须**在鉴权分支之后**重新取一次；
	//   沿用函数开头那个 `email` 会把"改密验证码"发到请求体里任意填的地址（自伤式放行）。
	email = strings.TrimSpace(req.Email)
	if !emailRe.MatchString(email) {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "邮箱格式不正确"))
		return
	}
	// ★ S3 防薅：一次性邮箱不发码。登录用途例外——已有账号可能历史绑定了一次性域名，
	// 不能因为域名规则把人锁在门外；自动建号分支在创建前会再走一次黑名单。
	if req.Purpose != emailPurposeLogin {
		if msg := s.disposableEmailRejected(email); msg != "" {
			s.writeError(w, r, apierrors.New(apierrors.ErrValidation, msg))
			return
		}
	}
	// 人机验证：防脚本刷短信/邮件接口。
	// ★ 2026-10-21 IAM 重做：改密验证码不走人机验证——这一档只允许**当前已登录会话**给本人
	//   绑定邮箱发码，不是匿名开放的发码面；沿用改密口「刻意不做人机验证」的既有口径。
	if req.Purpose != emailPurposePassword {
		if err := s.verifyCaptcha(r, req.CaptchaToken); err != nil {
			// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
			s.writeAuthzError(w, r, err)
			return
		}
	}
	// 单 IP 每日发码上限（复用注册护栏的窗口逻辑，独立计数键）
	ip := clientIP(r)
	if ok, wait := s.regGuard.allow("email-code:"+ip, emailSendIPDailyNm, 0); !ok {
		// ★ F-47（批 I-7）：统一出口口径（429 + RATE_LIMITED + Retry-After + retry_after）
		s.writeError(w, r, apierrors.New(apierrors.ErrRateLimited, "发送过于频繁，请稍后再试").WithRetryAfter(wait))
		return
	}
	ok, msg, noop := s.sendEmailCodeFor(ip, email, requestMailLang(r, req.AppLang), req.Purpose)
	if ok {
		s.regGuard.record("email-code:" + ip)
	}
	status := 200
	if !ok && strings.Contains(msg, "频繁") {
		status = 429
	}
	writeJSON(w, status, map[string]interface{}{"success": ok, "message": msg, "noop": noop && ok})
}

// handleRegisterConfig 公开注册配置接口（前端注册面板显隐用）。
// 返回: success=true 时携带 email_verify_enabled（★ 任务2.2：registration_review 审核机制已下线，不再返回）、
// industries（★ 2026-09-10 行业字典动态化：公开返回启用中的行业列表，注册下拉动态拉取）。
func (s *Server) handleRegisterConfig(w http.ResponseWriter, r *http.Request) {
	ev := false
	if v, _ := s.Store.GetConfig("email_verify_enabled"); v == "1" {
		ev = true
	}
	// 行业字典：仅返回启用中的行业（停用行业不参与新注册选择）
	type industryItem struct {
		Code string `json:"code"`
		Name string `json:"name"`
	}
	industries := []industryItem{}
	if inds, err := s.Store.ListIndustries(); err == nil {
		for _, p := range inds {
			if p.Enabled == 0 {
				continue
			}
			industries = append(industries, industryItem{Code: p.Code, Name: p.Name})
		}
	}
	// ★ 角色功能（2026-09-19）：职业角色字典同口径公开下发（仅启用中；注册表单角色下拉动态拉取）
	personas := []industryItem{}
	if ps, err := s.Store.ListPersonas(); err == nil {
		for _, p := range ps {
			if p.Enabled == 0 {
				continue
			}
			personas = append(personas, industryItem{Code: p.Code, Name: p.Name})
		}
	}
	writeJSON(w, 200, map[string]interface{}{
		"success":              true,
		"email_verify_enabled": ev,
		"captcha_enabled":      s.captchaEnabled(),
		"captcha_site_key":     s.captchaSiteKey(),
		// ★ 2026-10-27 D-02 批③：把**提供方名字**也下发——前端装载器要按它选脚本源与回调形状
		//   （turnstile 拿的是不透明串、geetest 拿的是四件套 JSON）。只回 enabled＋sitekey
		//   的话，加一个提供方就要改前端判断，迟早长出「配置说开了、界面挂错了」那一族。
		"captcha_provider": s.captchaProvider(),
		"industries":       industries,
		"personas":         personas,
	})
}

// handleMeEmailCode 登录用户向「新邮箱」发送变更验证码（修改邮箱专用，需登录）。
// 与注册发码共用存储/冷却/有效期；不做人机验证（已登录态），但受 60s 冷却与日上限约束。
func (s *Server) handleMeEmailCode(w http.ResponseWriter, r *http.Request) {
	u := s.authUser(r)
	if u == nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrUnauthorized, "未登录"))
		return
	}
	var req struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "请求格式错误"))
		return
	}
	email := strings.TrimSpace(req.Email)
	if !emailRe.MatchString(email) {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "邮箱格式不正确"))
		return
	}
	// ★ S3 防薅：奖励双唯一之外，一次性邮箱直接拒绑
	if msg := s.disposableEmailRejected(email); msg != "" {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, msg))
		return
	}
	if other, err := s.Store.GetUserByEmail(email); err == nil && other != nil && other.ID != u.ID {
		// F-64②：目标邮箱已被别的账号持有＝与既有记录的状态冲突 → 409 CONFLICT
		//（与 handleUpdateEmail 同一条判据同码，两步链路「先查占用再发码」口径必须一致）。
		// ★ 严禁 401：本接口需登录，用户此刻会话是好的，取 401 会让前端 core.ts 的
		//   handleUnauthorized 清 token 并踢回登录页——他刚点的「发送验证码」按钮连带作废，
		//   而且 /api/me/email-code 不在 401 豁免名单里（只有 login/register 豁免）。
		s.writeError(w, r, apierrors.New(apierrors.ErrConflict, "该邮箱已被其他账号绑定"))
		return
	}
	// ★ F-17（批E）换绑验证码语种：X-App-Lang 头优先（authHeaders 自动附带），头缺失回落账号存量 preferred_lang
	lang := requestMailLang(r, "")
	if lang == "" {
		if pl, perr := s.Store.GetPreferredLang(u.ID); perr == nil {
			lang = normalizeMailLang(pl)
		}
	}
	ok, msg, noop := s.sendEmailCode(clientIP(r), email, lang)
	status := 200
	if !ok && strings.Contains(msg, "频繁") {
		status = 429
	}
	writeJSON(w, status, map[string]interface{}{"success": ok, "message": msg, "noop": noop && ok})
}
