// ============ auth.go · 职责说明 ============
// api 包内部实现文件。
// =============================================
package api

// ============ 本文件职责中文说明 ============
// 本文件实现认证与用户管理相关 HTTP 接口：
//   - 登录（handleLogin）：JWT 签发 + 跨租户用户匹配 + 审计留痕
//   - 当前用户信息（handleMe）、修改密码（handleChangePassword）
//   - 用户管理（tenant_admin + super_admin）：列表/创建/更新/重置密码
// 安全要点：
//   - 登录跨租户匹配时优先平台超管账号；用户名在多租户重复时拒绝登录（防撞库）
//   - 仅超级管理员可创建/分配超管角色，租户管理员禁止操作超管账号（防越权）
//   - 所有写操作均写入审计日志（含变更前后值 diff）

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"translator/internal/config"

	"translator/internal/auth"
	apierrors "translator/internal/errors"
	"translator/internal/mail"
	"translator/internal/store"
)

// ============ 认证 ============

// 忘记密码验证码存储（★ 2026-09-09 技术债①P1：Redis 键值 + 内存兜底，跨实例共享；
// 历史为纯内存 map，重启/多实例失效——迁移到 vcodeStore 后任意实例生成的码其他实例可校验）。
// 键：用户 ID（vcode:reset:<uid>）；值：验证码、过期时间与已错误尝试次数。获取验证码时自动清理过期项。
var resetCodes = struct {
	sync.Mutex
	m map[int64]resetCode // 用户 ID → 验证码信息（兼容历史直接访问；读写经 resetCodeStore 兜底同步）
}{m: map[int64]resetCode{}}

// resetCodeMaxTries 单个重置码的最大错误尝试次数（≥5 作废防爆破；对齐 email_verify 口径）。
// 2026-08-26 P0-3 止血：此前无尝试上限，6 位码可在 10 分钟窗口内被脚本穷举 → 任意账号接管。
const resetCodeMaxTries = 5

// resetCode 验证码信息。
type resetCode struct {
	Code      string    // 6 位数字验证码
	ExpiresAt time.Time // 过期时间（10 分钟）
	Attempts  int       // 已错误尝试次数（≥resetCodeMaxTries 作废该码，2026-08-26 补充）
}

// handleLogin 登录接口：校验用户名密码并签发 JWT，返回 token + 用户信息。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（body 为 {username, password}）。
// 返回: success=true 时携带 token 与用户信息；失败一律走统一错误出口 s.writeError
//
//	（★ F-47 批 I-7 订正本注释：旧注释写「失败返回 200 + success=false」，实际自 #37 起
//	 已是 401/403/429＋{code,message,trace_id}——注释比代码落后一个整改批次）。
//	「不区分错误细节」这条安全口径不变：用户名不存在与密码错误回同一个「用户名或密码错误」，
//	防用户名枚举；但**传输层状态码必须诚实**（401＝凭证错、403＝账号停用、429＝撞冷却），
//	否则前端与 SDK 只能靠猜，且通用重试器无法对「稍后再试」做退避。
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	// 暴力破解防护：同一 IP 连续失败超过阈值后进入冷却期
	// ★ F-47（批 I-7）：冷却剩余秒数随错误一起出（429 + Retry-After + retry_after），
	// 旧实现只说「过于频繁」，客户端既不知道要等多久，也无法与「参数错」区分（同为 400）。
	if wait := s.loginCooldownSec(r); wait > 0 {
		s.writeError(w, r, apierrors.New(apierrors.ErrRateLimited, "登录尝试过于频繁，请稍后再试").WithRetryAfter(wait))
		return
	}
	// 平台存储未初始化时拒绝登录
	if s.Store == nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, "平台存储未初始化"))
		return
	}
	var req struct {
		Username string `json:"username"` // 登录用户名
		Password string `json:"password"` // 登录密码（明文，内部比对哈希）
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "请求格式错误"))
		return
	}
	// 先按当前请求上下文租户查用户；未命中则跨租户匹配
	tid := s.currentTenant(r)
	u, err := s.Store.GetUserByUsername(tid, req.Username)
	if err != nil {
		// 未在默认租户命中：跨租户全局匹配（兼容平台级账号与单租户唯一用户名）
		matches, gerr := s.Store.GetUserByUsernameGlobal(req.Username)
		if gerr != nil || len(matches) == 0 {
			s.writeError(w, r, apierrors.New(apierrors.ErrUnauthorized, "用户名或密码错误"))
			return
		}
		// 优先平台超管账号（tenant_id=0，平台级账号可在任何租户入口登录）
		u = nil
		for _, m := range matches {
			if m.TenantID == 0 {
				u = m
				break
			}
		}
		// 全平台唯一租户用户可直接登录
		if u == nil && len(matches) == 1 {
			u = matches[0]
		}
		// 用户名在多个租户重复且非超管：拒绝登录，要求通过租户专属入口登录
		if u == nil {
			s.writeError(w, r, apierrors.New(apierrors.ErrUnauthorized, "用户名在多个租户重复，请通过租户入口登录"))
			return
		}
	}
	// 账号状态校验：停用账号禁止登录；注销宽限期内（deactivating 当日）仍可登录，
	// 次日起惰性落停用态并拒绝（2026-08-26 自助注销需求）
	effective := auth.EffectiveUserStatus(u.Status, u.DeactivatedAt)
	if effective == store.UserDisabled || (u.Status == store.UserDeactivating && effective == store.UserDisabled) {
		if u.Status == store.UserDeactivating {
			s.Store.FinalizeDeactivation(u.ID, u.TenantID) // 宽限期届满：落停用态
			s.writeError(w, r, apierrors.New(apierrors.ErrForbidden, "账号已注销，如需恢复请联系管理员"))
			return
		}
		s.writeError(w, r, apierrors.New(apierrors.ErrForbidden, "账号已停用"))
		return
	}
	// 密码校验：比对存储的 bcrypt 哈希（兼容历史 SHA-256）
	if !auth.CheckPassword(u.PasswordHash, req.Password) {
		// 暴力破解防护：记录失败次数
		s.recordLoginFail(r)
		s.writeError(w, r, apierrors.New(apierrors.ErrUnauthorized, "用户名或密码错误"))
		return
	}
	// 历史哈希自动升级：登录成功后若仍为 SHA-256/legacy-salt 格式，改写为 bcrypt。
	// ★ B10（2026-09-12）：计数日志观察存量（固定盐 trans-salt 属公开信息，存量清零后移除此路径）。
	if auth.NeedMigrateHash(u.PasswordHash) {
		legacyCounter.Add(1)
		log.Printf("[auth] legacy 口令透明升级 uid=%d tid=%d（累计 %d 次）", u.ID, u.TenantID, legacyCounter.Load())
		if err := s.Store.ResetPassword(u.ID, u.TenantID, auth.PasswordHash(req.Password)); err == nil {
			u.PasswordHash = auth.PasswordHash(req.Password)
			// ★ B2 联动修正：ResetPassword 会递增库内 token_version，内存副本必须同步，
			//   否则下方 auth.Sign 携带旧版本号、签发即被 authUser 判为已撤销。
			u.TokenVersion++
		}
	}
	// 登录成功：清零失败计数
	s.clearLoginFails(r)
	// 签发有效期 24 小时的 JWT
	tok, err := auth.Sign(u, 24*time.Hour)
	if err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, "签发失败"))
		return
	}
	// 记录最近登录时间
	s.Store.TouchLogin(u.ID)
	// 登录审计归属实际登录租户（平台超管 tenant_id=0 归默认租户 1）
	auditTID := u.TenantID
	if auditTID <= 0 {
		auditTID = 1
	}
	// ★ #33 任务系统：每日登录任务（+100 临时积分，一日一次、有效期 3 天、日叠加）。
	// 此刻请求还没有 Authorization 头，生效租户显式传入；旁路调用，失败只留痕不影响登录。
	s.grantTaskEventOnTenant(r, auditTID, u.ID, store.TaskKeyLoginDaily, "")
	s.Store.LogAudit(auditTID, u.ID, "login", "auth", "用户登录")
	// 品牌域名跳转：若用户所属租户配置了专属品牌子域，且本次登录不在该子域上，
	// 返回 brand_host 供前端重定向过去（需求 1-B）。
	// ★ P0-3 修复（2026-09-14）：跨子域登录改发一次性 sso_code（60s、单次消费、
	//   经 /api/auth/sso/exchange 兑换 JWT）——旧实现把裸 JWT 放进重定向 URL
	//   （进浏览器历史/Referer），且 E3 后前端不再消费 ?token=，链路整体断裂。
	brandHost := ""
	brandSSOCode := ""
	if t, e := s.Ten.GetByID(u.TenantID); e == nil && t.Domain != "" {
		base := brandingBaseDomain(s)
		if bh := strings.TrimSpace(t.Domain) + "." + base; r.Host != bh {
			brandHost = bh
			if xcode := newSSOExchangeCode(); xcode != "" {
				payload, _ := json.Marshal(map[string]int64{"uid": u.ID, "tid": u.TenantID})
				vcodeSet("sso_xchg:"+xcode, payload, 60*time.Second)
				brandSSOCode = xcode
			}
		}
	}
	// 返回 JWT 与脱敏后的用户信息（不含密码哈希）
	// ★ P0-3：需要跨子域跳转时不返回裸 token，仅返回一次性 sso_code（E3 口径对齐）
	if brandHost != "" && brandSSOCode != "" {
		writeJSON(w, 200, map[string]interface{}{
			"success": true, "brand_host": brandHost, "sso_code": brandSSOCode,
			"user": map[string]interface{}{
				"id": u.ID, "username": u.Username, "display_name": u.DisplayName,
				"role": u.Role, "tenant_id": u.TenantID, "email": u.Email,
				"must_change_pwd": u.MustChangePwd,
			},
		})
		return
	}
	writeJSON(w, 200, map[string]interface{}{
		"success": true, "token": tok, "brand_host": brandHost,
		"user": map[string]interface{}{
			"id": u.ID, "username": u.Username, "display_name": u.DisplayName,
			"role": u.Role, "tenant_id": u.TenantID, "email": u.Email,
			// ★ 首登强制改密（2026-09-02 功能）：1=登录后必须先改密，前端弹窗引导
			"must_change_pwd": u.MustChangePwd,
		},
	})
}

// loginCooldownSec 返回当前请求 IP 的登录冷却剩余秒数（0＝不受限）。
// ★ F-47（批 I-7）：旧版叫 loginLocked 并返回 bool，注释写着「拒绝登录（429）」而实际经
//
//	统一出口发出的是 400，且不带任何时长——注释与代码分家、状态码与语义分家。
//	现按秒数返回，调用方据此同时得到三件一致的东西：HTTP 429、`Retry-After` 头、
//	出参 `retry_after` 字段（判据唯一来源＝loginLimiter.retryAfterSec，见 ratelimit.go）。
func (s *Server) loginCooldownSec(r *http.Request) int {
	if s.loginLimit == nil {
		return 0
	}
	return s.loginLimit.retryAfterSec(clientIP(r))
}

// recordLoginFail 记录当前请求 IP 一次登录失败。
// 参数 r: HTTP 请求。
func (s *Server) recordLoginFail(r *http.Request) {
	if s.loginLimit == nil {
		s.loginLimit = newLoginLimiter(s.Store)
	}
	s.loginLimit.fail(clientIP(r))
}

// clearLoginFails 登录成功后清零当前请求 IP 的失败记录。
// 参数 r: HTTP 请求。
func (s *Server) clearLoginFails(r *http.Request) {
	s.loginLimit.clear(clientIP(r))
}

// handleMe 当前用户信息接口：返回登录用户完整信息。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（需带 Authorization Bearer JWT）。
// 返回: success=true 时携带 user 对象；未登录返回 401。
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	u := s.authUser(r)
	if u == nil {
		writeJSON(w, 401, map[string]interface{}{"success": false, "message": "未登录"})
		return
	}
	// ★ 积分口径全面上线（2026-09-19）：不再下发积分↔token 汇率——所有余额/用量
	//   字段由后端直接折算成积分出参，前端零换算。
	writeJSON(w, 200, map[string]interface{}{"success": true, "user": u})
}

// handleMeContext 前台身份上下文：账号/所属租户/组织部门 + 可应用知识库包类型。
// 前台登录后调用一次，用于顶栏展示「账号 · 组织 · 部门 · 知识库包类型」；
// 平台级账号（tenant_id=0）不返回任何知识库包（平台直翻无知识库）。
func (s *Server) handleMeContext(w http.ResponseWriter, r *http.Request) {
	u := s.authUser(r)
	if u == nil {
		writeJSON(w, 401, map[string]interface{}{"success": false, "message": "未登录"})
		return
	}
	// 按登录归属逐级补齐展示字段：部门名（GetOrgByID）→ 租户名/个人账号/邀请开关（Ten.GetByID，查询失败留空不阻断）
	orgName := ""
	if u.OrgID > 0 {
		if o, e := s.Store.GetOrgByID(u.OrgID); e == nil {
			orgName = o.Name
		}
	}
	tenantName := ""
	isPersonal := false
	inviteEnabled := false
	if u.TenantID > 0 && s.Ten != nil {
		if t, e := s.Ten.GetByID(u.TenantID); e == nil {
			tenantName = t.Name
			isPersonal = t.IsPersonal
			inviteEnabled = t.InviteEnabled
		}
	}
	// 查询所属租户可应用 KB 包（前端导航门控用）后一次性组装返回
	packs, _ := s.Store.ListApplicablePacks(u.TenantID)
	// ★ 角色功能（2026-09-19）：追加当前用户的角色包（persona 按用户装配，不在租户级查询内）
	if u.JobRole != "" {
		if p, perr := s.Store.FindEnabledPersonaByCode(u.JobRole); perr == nil && p != nil {
			packs = append(packs, &store.PackBrief{ID: p.ID, PackType: store.PackPersona, Name: p.Name, Enabled: p.Enabled})
		}
	}
	writeJSON(w, 200, map[string]interface{}{
		"success":        true,
		"username":       u.Username,
		"display_name":   u.DisplayName,
		"email":          u.Email,
		"role":           u.Role,
		"job_role":       u.JobRole,
		"tenant_id":      u.TenantID,
		"tenant_name":    tenantName,
		"is_personal":    isPersonal,
		"invite_enabled": inviteEnabled,
		"org_id":         u.OrgID,
		"org_name":       orgName,
		"kb_packs":       packs,
	})
}

// legacyCounter ★ B10：历史格式口令透明升级累计计数（进程内，日志观察存量账号）。
var legacyCounter atomic.Int64

// handleChangePassword 修改密码接口：校验原密码后更新为 bcrypt 哈希。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（body 为 {old_password, new_password}）。
// 返回: success=true 表示修改成功；失败走统一错误出口给诚实状态码
//
//	（★ F-64② 批 I-10：旧写法是「HTTP 200 + success:false」，客户端只看状态码就判成改密成功）。
//	取码口径：原密码错=400（载荷不对）、写库失败=500 —— 两者都**不许** 401，
//	因为前端 request() 见 401 会清登录态，「旧密码记错」不该把人踢回登录页。
func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	u := s.authUser(r)
	if u == nil {
		writeJSON(w, 401, map[string]interface{}{"success": false, "message": "未登录"})
		return
	}
	var req struct {
		OldPassword string `json:"old_password"` // 原密码（用于校验身份）
		NewPassword string `json:"new_password"` // 新密码（存储前转为哈希）
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.NewPassword == "" {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	// 校验原密码是否正确
	if !auth.CheckPassword(u.PasswordHash, req.OldPassword) {
		// F-64②：原密码错误＝提交的载荷不对 → 400 VALIDATION_ERROR。
		// ★ 这里严禁取 ErrUnauthorized(401)：本接口是**已登录态**动作，而前端 core.ts 的 request()
		//   一见 401 就清 token 并踢回登录页（只对 /api/auth/login、/api/auth/register 豁免），
		//   一次「旧密码记错了」会把人现成的会话一起抹掉，改密改到一半反而要重新登录。
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "原密码错误"))
		return
	}
	// 仅允许修改自己租户下的账号（u.ID 绑定 u.TenantID，防跨租户篡改）
	if err := s.Store.ResetPassword(u.ID, u.TenantID, auth.PasswordHash(req.NewPassword)); err != nil {
		// F-64②：写库失败是本进程之外的服务端故障 → 500（同上：绝不 401，免得清掉正当会话）
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	// 记录修改密码审计
	// ★ F-63（2026-09-26 批 I-3）：detail 原为空串；只记「谁改了口令」这一事实与账号名，
	//   口令本身（新旧）一律不入审计——审计表明文可读面比业务表更宽，写进去就是泄露。
	s.Store.LogAudit(u.TenantID, u.ID, "change_password", "auth", "用户 "+u.Username+" 修改登录口令")
	// ★ 首登强制改密（2026-09-02 功能）：改密成功后自动清零标记
	if u.MustChangePwd > 0 {
		_ = s.Store.SetMustChangePwd(u.ID, u.TenantID, 0)
	}
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// ============ 忘记密码（验证码方式） ============

// handleForgotPassword 忘记密码接口：按用户名或邮箱定位用户，生成验证码并发送。
// 安全要点：无论用户是否存在统一返回 success=true（防用户名枚举）；
// 验证码通过邮件发送（SMTP 未配置时打印日志，前端提示测试模式）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（body 为 {username, email}）。
// 返回: success=true 表示已发送验证码（或进入测试模式）。
func (s *Server) handleForgotPassword(w http.ResponseWriter, r *http.Request) {
	// ★ IP 限流（2026-08-26 P0-3 止血）：复用注册护栏窗口逻辑，独立计数键。
	//   日上限 10 次 + 最小间隔 30s——防止「无限发码骚扰 + 配合爆破」的组合滥用。
	ip := clientIP(r)
	if ok, wait := s.regGuard.allow("pwd-forgot:"+ip, 10, 30); !ok {
		// ★ F-47（批 I-7）：内联 writeJSON(429) 改走统一出口——状态码不变（仍 429），
		// 但补齐 {code:"RATE_LIMITED", retry_after} 与 trace_id：同仓两类限流响应口径就此合流。
		s.writeError(w, r, apierrors.New(apierrors.ErrRateLimited, "请求过于频繁，请稍后再试").WithRetryAfter(wait))
		return
	}
	var req struct {
		Username string `json:"username"` // 用户名（二选一）
		Email    string `json:"email"`    // 联系邮箱（二选一）
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	// 定位用户：优先用户名（跨租户匹配），其次邮箱
	var u *store.User
	if req.Username != "" {
		if matches, err := s.Store.GetUserByUsernameGlobal(req.Username); err == nil && len(matches) == 1 {
			u = matches[0]
		} else if err == nil && len(matches) > 1 {
			// 多租户重名：需提供邮箱精确定位
			u = nil
		}
	}
	if u == nil && req.Email != "" {
		if found, err := s.Store.GetUserByEmail(req.Email); err == nil {
			u = found
		}
	}
	// 用户不存在或未绑定邮箱：统一返回成功（防枚举），但无法发送
	if u == nil || u.Email == "" {
		writeJSON(w, 200, map[string]interface{}{"success": true, "message": "如果账号存在且绑定了邮箱，验证码已发送"})
		return
	}
	// 校验账号状态：停用账号不发送（注销宽限期内仍可发送——找回密码链路保持可用）
	if iamEffectiveStatus(u) != store.UserActive {
		writeJSON(w, 200, map[string]interface{}{"success": true, "message": "如果账号存在且绑定了邮箱，验证码已发送"})
		return
	}
	// 生成 6 位数字验证码
	code, err := genResetCode()
	if err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, "生成验证码失败"))
		return
	}
	// 存储验证码（覆盖旧码，10 分钟有效；★ Redis 镜像 + 本地 map 双写，跨实例共享）
	rc := resetCode{Code: code, ExpiresAt: time.Now().Add(10 * time.Minute)}
	resetCodes.Lock()
	resetCodes.m[u.ID] = rc
	resetCodes.Unlock()
	if b, e := json.Marshal(rc); e == nil {
		vcodeSet(vcodeResetKey(u.ID), b, 10*time.Minute)
	}
	// 发送邮件（改为异步入队：SMTP 失败由队列重试/死信吸收，不阻塞用户）
	// ★ F-17（批E）语种跟随：取该账号注册时落库的 preferred_lang（查无/缺列回空=中文链路）
	resetLang, lerr := s.Store.GetPreferredLang(u.ID)
	if lerr != nil {
		resetLang = ""
	}
	if serr := s.sendTemplatedMail(u.Email, "reset_code", normalizeMailLang(resetLang), map[string]string{"code": code}); serr != nil {
		log.Printf("[mail] 密码重置验证码入队失败 to=%s err=%v", config.MaskEmail(u.Email), serr)
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, "邮件发送失败，请稍后重试或联系管理员"))
		return
	}
	s.regGuard.record("pwd-forgot:" + ip) // 业务成功才计数（与 email-code 同款 allow/record 模式）
	s.Store.LogAudit(u.TenantID, u.ID, "forgot_password", "auth", "请求重置密码验证码")
	writeJSON(w, 200, map[string]interface{}{"success": true, "message": "验证码已发送到绑定邮箱"})
}

// handleResetPassword 重置密码接口：校验验证码后更新密码。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（body 为 {username, code, new_password}）。
// 返回: success=true 表示重置成功；
// ★ 观察2②（2026-09-25 UAT 修复批G）：验证码错误/用户不存在/已过期/错次超限/一次性码失配
//
//	四类失败族由「HTTP 200 + success:false」统一收口为 HTTP 400 + 统一错误码
//	VALIDATION_ERROR（s.writeError，与 F-21③ 同向）；中文文案逐字保留原文
//	（i18n catalog 按中文字面量匹配，换文案会翻红），前端 request() 对 4xx 会归一
//	解析 body.message，登录页失败提示链路不变。
func (s *Server) handleResetPassword(w http.ResponseWriter, r *http.Request) {
	// ★ IP 限流（2026-08-26 P0-3 止血）：验证码校验接口的爆破主战场，
	//   与 forgot 限流独立计数；日 20 次 + 最小间隔 10s（正常用户重试绰绰有余）。
	ip := clientIP(r)
	if ok, wait := s.regGuard.allow("pwd-reset:"+ip, 20, 10); !ok {
		// ★ F-47（批 I-7）：与 forgot 同口径走统一出口（429 + RATE_LIMITED + retry_after）
		s.writeError(w, r, apierrors.New(apierrors.ErrRateLimited, "请求过于频繁，请稍后再试").WithRetryAfter(wait))
		return
	}
	var req struct {
		Username    string `json:"username"`     // 用户名（用于定位验证码归属）
		Code        string `json:"code"`         // 6 位验证码
		NewPassword string `json:"new_password"` // 新密码
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Username == "" || req.Code == "" || req.NewPassword == "" {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	// 定位用户
	var u *store.User
	if matches, err := s.Store.GetUserByUsernameGlobal(req.Username); err == nil && len(matches) == 1 {
		u = matches[0]
	}
	// ★ 观察2②（批G）失败族①「用户不存在」：与「验证码错误」回同一文案（防枚举语义不变），
	//   仅把出口从内联 writeJSON(200, success:false) 收口为 writeError → HTTP 400 + VALIDATION_ERROR。
	if u == nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "验证码错误或已过期"))
		return
	}
	// 校验验证码（一次性 + 防爆破，2026-08-26 P0-3 止血）：
	//   ① 单次加锁内完成「读码→比对→错计/销毁」，消除旧实现两次 Lock 分离的竞态窗口；
	//   ② 错误尝试 ≥resetCodeMaxTries(5) 即作废该码——6 位数字码若无次数限制，
	//      攻击者可在 10 分钟有效期内脚本穷举（10^6 空间）→ 任意账号接管。
	//   ★ 2026-09-09 技术债①：读取优先 Redis（跨实例共享），未命中回退本地 map；
	//     写入/删除双端同步，保证多实例下任意实例生成的码可被其他实例校验。
	rkey := vcodeResetKey(u.ID)
	// ★ P1-12 修复（2026-09-14）：per-key 互斥锁串行化「读→判→比→错计/消费」全流程。
	//   上方注释宣称的「单次加锁」实为分段锁 + Redis Get/Set 弱一致（vcode.go 自认），
	//   并发请求可全部读到 attempts=0，一次并发脉冲即近似穷举 6 位码空间。
	unlockRC := vcodeLockOf("reset:" + rkey)
	defer unlockRC()
	// 读：Redis 优先（反序列化失败/未命中回退本地 map）
	var rc resetCode
	found := false
	if b, ok := vcodeGet(rkey); ok && json.Unmarshal(b, &rc) == nil {
		found = true
	} else {
		resetCodes.Lock()
		rc, ok = resetCodes.m[u.ID]
		resetCodes.Unlock()
		found = ok
	}
	// ★ 观察2②（批G）失败族②「验证码缺失或已过期」：改走 writeError → 400 + VALIDATION_ERROR（文案逐字不变）。
	if !found || time.Now().After(rc.ExpiresAt) {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "验证码错误或已过期"))
		return
	}
	if rc.Attempts >= resetCodeMaxTries {
		// 超限作废：直接删除该码，即使后续答对也不放行
		resetCodes.Lock()
		delete(resetCodes.m, u.ID)
		resetCodes.Unlock()
		vcodeDel(rkey)
		// ★ 观察2②（批G）失败族③「错误尝试超限」：改走 writeError → 400 + VALIDATION_ERROR（文案逐字不变）。
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "验证码错误次数过多，请重新获取"))
		return
	}
	if rc.Code != req.Code {
		rc.Attempts++
		resetCodes.Lock()
		resetCodes.m[u.ID] = rc // 回写累计错误次数
		resetCodes.Unlock()
		if b, e := json.Marshal(rc); e == nil {
			vcodeSet(rkey, b, 10*time.Minute)
		}
		// ★ 观察2②（批G）失败族④「一次性码失配」：改走 writeError → 400 + VALIDATION_ERROR（文案逐字不变）。
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "验证码错误或已过期"))
		return
	}
	// 验证通过：作废该验证码并更新密码（本地 + Redis 双删）
	resetCodes.Lock()
	delete(resetCodes.m, u.ID)
	resetCodes.Unlock()
	vcodeDel(rkey)
	// 更新密码
	if err := s.Store.ResetPassword(u.ID, u.TenantID, auth.PasswordHash(req.NewPassword)); err != nil {
		// F-64②：验证码已核对通过、写库却失败，这是服务端故障 → 500 INTERNAL_ERROR。
		// ★ 匿名链路（忘记密码）同样严禁 401：request() 的 401 拦截器只豁免
		//   /api/auth/login 与 /api/auth/register，重置密码页吃一个 401 会把用户刚填的
		//   验证码/新密码连同会话一起抹掉，回到「什么都没做」的登录页。
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	s.regGuard.record("pwd-reset:" + ip) // 重置成功才计数（限流窗口按成功动作推进）
	s.Store.LogAudit(u.TenantID, u.ID, "reset_password", "auth", "通过验证码重置密码")
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// genResetCode 生成 6 位数字验证码（密码学安全随机）。
func genResetCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

// iamEffectiveStatus 计算用户生效状态（注销宽限期次日起等效停用）。
func iamEffectiveStatus(u *store.User) string {
	if u == nil {
		return store.UserDisabled
	}
	return auth.EffectiveUserStatus(u.Status, u.DeactivatedAt)
}

// handleDeactivateAccount 自助注销接口（POST /api/me/deactivate，2026-08-26 需求）：
//   - 仅普通用户（role=user）可自助注销；管理员账号请联系上级停用
//   - 生效语义：当日仍可正常使用，次日起无法登录；后台数据保留不删除
//   - 联动：立即停用该用户 id 名下签发的全部 API Key（收回开放 API 调用权限）
//   - 撤回：管理员在「成员管理」把状态改回启用即可恢复
func (s *Server) handleDeactivateAccount(w http.ResponseWriter, r *http.Request) {
	u := s.authUser(r)
	if u == nil {
		writeJSON(w, 401, map[string]interface{}{"success": false, "message": "未登录"})
		return
	}
	if u.Role != store.RoleUser {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": "仅普通用户支持自助注销；管理员账号请联系上级处理"})
		return
	}
	if err := s.Store.DeactivateSelf(u.ID, u.TenantID); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": publicErrMessage(r.Context(), err)})
		return
	}
	revoked := s.Store.DisableAPIKeysByUser(u.TenantID, u.ID) // ★ 连带停用名下全部 API Key
	s.Store.LogAudit(u.TenantID, u.ID, "account_deactivate", "users",
		fmt.Sprintf("自助注销生效（宽限至今日），停用 API Key %d 把", revoked))
	writeJSON(w, 200, map[string]interface{}{
		"success":       true,
		"message":       "注销申请已生效：今日仍可使用，明日 00:00 起无法登录；名下 API Key 已全部停用。数据保留，如需撤回请联系管理员重新启用。",
		"keys_disabled": revoked,
	})
}

// mailer 创建邮件发送器（按环境变量 MAIL_ENABLED / SMTP_* 惰性构建）。
func (s *Server) mailer() mail.Sender {
	return mail.NewSender(&mail.Config{
		Enabled: os.Getenv("MAIL_ENABLED") == "1",
		Host:    os.Getenv("SMTP_HOST"),
		Port:    os.Getenv("SMTP_PORT"),
		User:    os.Getenv("SMTP_USER"),
		Pass:    os.Getenv("SMTP_PASS"),
		From:    os.Getenv("SMTP_FROM"),
	})
}

// infoMailer 产品手册/欢迎邮件专用发送器，发件地址由 INFO_SMTP_FROM 配置，凭据取自 INFO_SMTP_* 环境变量。
// 默认 host/port 走阿里云邮箱；未配置 USER 时退化 Noop（不报错，仅日志缺失）。
func (s *Server) infoMailer() mail.Sender {
	host := os.Getenv("INFO_SMTP_HOST")
	if host == "" {
		host = "smtp.mxhichina.com"
	}
	port := os.Getenv("INFO_SMTP_PORT")
	if port == "" {
		port = "465"
	}
	user := os.Getenv("INFO_SMTP_USER")
	pass := os.Getenv("INFO_SMTP_PASS")
	from := os.Getenv("INFO_SMTP_FROM")
	if from == "" {
		from = user
	}
	enabled := user != "" && os.Getenv("INFO_SMTP_ENABLED") != "0"
	return mail.NewSender(&mail.Config{Enabled: enabled, Host: host, Port: port, User: user, Pass: pass, From: from})
}

// ============ 用户管理（tenant_admin + super_admin） ============

// handleAdminUsers 用户列表接口：列出当前生效租户下的用户。
// 权限：部门管理员及以上；部门管理员仅可见本部门及其子部门下用户，租户管理员及以上可见全部。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（需 dept_admin 及以上权限）。
// 返回: success=true 时携带 users 数组；查询失败按 500 走统一错误出口
//
//	（★ F-64② 批 I-10：旧写法回「HTTP 200 + success:false」，管理台把它当成功、
//	 渲染成「该部门/该租户下没有成员」的空表，故障被完全藏住）。
func (s *Server) handleAdminUsers(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireDeptAdmin(r)
	if err != nil {
		// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
		s.writeAuthzError(w, r, err)
		return
	}
	tid := s.effTenant(r, u)
	// 部门管理员：仅本部门及其子部门下用户（部门树归集）；租户管理员及以上：租户全部用户
	if auth.RoleLevel(u.Role) == 2 && u.OrgID > 0 {
		orgIDs, err := s.Store.OrgDescendantIDs(tid, u.OrgID)
		if err != nil {
			// F-64②：部门子树查询失败是存储层故障 → 500。旧写法回 200，成员页会把它当
			// 「查询成功但列表为空」渲染成空表格，管理员误以为部门下没人。
			s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
			return
		}
		users, err := s.Store.ListUsersByOrg(tid, orgIDs)
		if err != nil {
			// F-64②：同上，部门成员列表读失败＝服务端出错 → 500（不是权限问题，
			// 权限判定已在上方 requireDeptAdmin 完成，这里给 403/401 都会指错方向）
			s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
			return
		}
		writeJSON(w, 200, map[string]interface{}{"success": true, "users": users})
		return
	}
	// 超管平台根上下文（tid=0）：跨租户列出全部账号（与 handleOrgUsers 平台根视图一致）
	if auth.IsSuperAdmin(u) && tid <= 0 {
		users, err := s.Store.ListAllUsers()
		if err != nil {
			// F-64②：平台级全量账号列举失败＝存储层出错 → 500（读接口没有「业务失败」这一说，
			// 除了权限已经在上游拦掉；把 DB 故障伪装成 200 会让超管看到一张空表而以为平台没人）
			s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
			return
		}
		writeJSON(w, 200, map[string]interface{}{"success": true, "users": users})
		return
	}
	// 租户隔离：仅列出生效租户（超管可切换）下的用户
	users, err := s.Store.ListUsers(tid)
	if err != nil {
		// F-64②：本租户用户列举失败 → 500，同上；租户维度不是「未授权」，
		// 取 401 会触发前端清登录态（core.ts 的 401 豁免名单里没有本路径）
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "users": users})
}

// handleAdminUserCreate 创建用户接口（超管可指定归属租户；租户管理员限本租户；部门管理员限本部门子树）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（body 含 username/password/display_name/role/tenant_id）。
// 返回: success=true 时携带新用户（密码哈希已置空）。
func (s *Server) handleAdminUserCreate(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireDeptAdmin(r)
	if err != nil {
		// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
		s.writeAuthzError(w, r, err)
		return
	}
	var req struct {
		Username    string `json:"username"`     // 用户名（租户内唯一）
		Password    string `json:"password"`     // 初始密码（存储前转哈希）
		DisplayName string `json:"display_name"` // 显示名称
		Role        string `json:"role"`         // 角色：user/tenant_admin/super_admin/approver/admin
		TenantID    int64  `json:"tenant_id"`    // 归属租户（仅超管可指定，默认生效租户）
		OrgID       int64  `json:"org_id"`       // 所属组织 ID（0=根组织/未分配）
		Email       string `json:"email"`        // 联系邮箱（找回密码验证码接收）
	}
	decErr := json.NewDecoder(r.Body).Decode(&req)
	if decErr != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": fmt.Sprintf("请求格式错误: %v", decErr)})
		return
	}
	if req.Username == "" || req.Password == "" {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "用户名和密码不能为空"})
		return
	}
	// 角色默认普通用户
	if req.Role == "" {
		req.Role = store.RoleUser
	}
	// 角色白名单校验（四级：user/dept_admin/tenant_admin/super_admin + 兼容旧值）
	switch req.Role {
	case store.RoleUser, store.RoleDeptAdmin, store.RoleTenantAdmin, store.RoleSuperAdmin, store.RoleApprover, store.RoleAdmin:
	default:
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "角色无效"})
		return
	}
	// 权限校验：仅超级管理员可创建超级管理员/管理员等高权限角色；租户管理员可创建部门管理员/普通用户
	if auth.RoleLevel(req.Role) >= 4 && !auth.IsSuperAdmin(u) {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": "权限不足：仅超级管理员可分配该角色"})
		return
	}
	if auth.RoleLevel(req.Role) >= 3 && !auth.IsTenantAdmin(u) {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": "权限不足：仅租户管理员及以上可分配该角色"})
		return
	}
	// ★ F-31（闸 1/2，2026-09-25 批F）：dept_admin 必须绑定部门——
	//   org_id=0 的部门管理员会在建号/成员操作等入口被「部门管理员未绑定部门」守卫反锁（死角色），
	//   必须在创建源头卡死，而不是造出一个无法履职的账号再让当事人报障。
	if req.Role == store.RoleDeptAdmin && req.OrgID <= 0 {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "部门管理员必须绑定部门：请先在组织列表中选择所属部门"))
		return
	}
	// 归属租户判定：超管创建超管时平台级(0)；否则租户管理员限本租户、超管用所选/指定租户
	tid := s.effTenant(r, u)
	if req.Role == store.RoleSuperAdmin || req.Role == store.RoleAdmin {
		tid = 0
	} else if auth.IsSuperAdmin(u) {
		// 超管开通账号的租户解析优先级：所属组织归属租户 > 显式 tenant_id > 生效租户
		// 平台树展示所有租户的组织，账号必须跟随所选组织归属其租户（闭环，防跨租户错挂）
		if req.OrgID > 0 {
			if org, e := s.Store.GetOrgByID(req.OrgID); e == nil && org.TenantID > 0 {
				tid = org.TenantID
			}
		} else if req.TenantID > 0 {
			tid = req.TenantID
		}
	}
	// 组织归属校验：非平台级用户组织必须属于归属租户
	if tid > 0 {
		if err := s.validateOrg(tid, req.OrgID); err != nil {
			writeJSON(w, 400, map[string]interface{}{"success": false, "message": publicErrMessage(r.Context(), err)})
			return
		}
	}
	// 部门管理员范围：目标组织必须在其本部门及子部门树内（否则拒绝创建）
	if auth.RoleLevel(u.Role) == 2 {
		if u.OrgID <= 0 {
			writeJSON(w, 403, map[string]interface{}{"success": false, "message": "部门管理员未绑定部门，无法开通账号"})
			return
		}
		if req.OrgID > 0 {
			inTree, e := s.Store.IsOrgInSubtree(tid, u.OrgID, req.OrgID)
			if e != nil || !inTree {
				writeJSON(w, 403, map[string]interface{}{"success": false, "message": "无权在非本部门下开通账号"})
				return
			}
		}
	}
	// ★ 邮箱唯一预检（oneid 账户体系）：管理员建号此前绕过全局判重——补齐。
	//   目标邮箱正被其他账号持有则拒绝创建（避免建出无邮箱残号）。
	// ★ F-64② 收尾定夺（2026-09-26 批 I-10）：原回 400，与下面「用户名已存在」的 409 是同一次
	//   提交里的两条撞库守卫，却取了两个码；也与自助注册/换绑邮箱的口径不一致（统一 409）。
	//   邮箱被他人持有＝与库里既有记录冲突，管理员可修的动作是「换一个邮箱」而非「改请求格式」，
	//   故取 409 CONFLICT。严禁 401：管理员此刻是登录态，401 会被 core.ts 清 token 踢回登录页。
	if req.Email != "" {
		if other, oerr := s.Store.GetUserByEmail(strings.ToLower(strings.TrimSpace(req.Email))); oerr == nil && other != nil {
			s.writeError(w, r, apierrors.New(apierrors.ErrConflict, "该邮箱已被其他账号绑定"))
			return
		}
	}
	nu, err := s.Store.CreateUser(tid, req.Username, auth.PasswordHash(req.Password), req.DisplayName, req.Role, u.ID, req.OrgID)
	if err != nil {
		// ★ 脱敏（2026-09-12）：驱动错误不透吐（PG 泄漏约束名/SQLSTATE，双方言文案漂移）
		if store.IsUniqueViolation(err) {
			// F-64②：用户名撞了租户内唯一约束＝状态冲突 → 409 CONFLICT。
			// 不取 400：这不是「请求写错了」，而是「与库里既有记录撞车」，前端据此才能
			// 把提示停在「换个用户名」这条分支上；也不取 401——管理员此刻是登录态，
			// request() 的 401 拦截（豁免名单只有 login/register）会把他踢出管理台。
			s.writeError(w, r, apierrors.New(apierrors.ErrConflict, "创建失败：用户名已存在"))
			return
		}
		log.Printf("[admin] 建号失败 username=%s: %v", req.Username, err)
		// F-64②：非撞库的建号失败是真·存储故障 → 500（旧 200 壳让弹窗显示「创建成功」
		// 后刷新列表却找不到新账号，管理员只能反复重试建号）
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, "创建失败: "+store.DebriefDBError(err)))
		return
	}
	// 绑定联系邮箱（用于找回密码；SetUserEmail 内含占用即拒绝的最终守卫）
	// 绑定失败不阻断建号（邮箱可由用户后续自助补绑），成功才同步内存副本供返回展示
	if req.Email != "" {
		if eerr := s.Store.SetUserEmail(nu.ID, tid, req.Email); eerr == nil {
			nu.Email = req.Email
		}
	}
	// 返回前清空密码哈希，避免泄露
	nu.PasswordHash = ""
	s.Store.LogAudit(u.TenantID, u.ID, "user_create", "users", req.Username)
	writeJSON(w, 200, map[string]interface{}{"success": true, "user": nu})
}

// handleAdminUserUpdate 更新用户接口（名称/角色/状态/组织归属），带变更前后值审计。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（body 含 id/display_name/role/status/org_id）。
// 返回: success=true 表示更新成功。
// ★ F-35（2026-09-25 批F）：display_name/role/status 改指针入参——「字段缺席」与「显式置空」
//
//	分离，缺席一律取目标现值合并后落库（旧实现把缺席当空串直写，只改角色会静默洗掉姓名、
//	状态被默认值翻回 active）；★ F-31（闸 2/2）：合并后的终态若为 dept_admin 且无部门，拒绝。
func (s *Server) handleAdminUserUpdate(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireDeptAdmin(r)
	if err != nil {
		// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
		s.writeAuthzError(w, r, err)
		return
	}
	var req struct {
		ID          int64   `json:"id"`           // 目标用户 ID
		DisplayName *string `json:"display_name"` // 显示名称（nil=不修改）
		Role        *string `json:"role"`         // 目标角色（nil/空串=不修改）
		Status      *string `json:"status"`       // 状态：active/disabled（nil/空串=不修改）
		OrgID       *int64  `json:"org_id"`       // 所属组织 ID（nil=不修改，0=根组织）
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	// ★ 目标用户真实租户解析（问题2修复）：超管平台上下文（tid<=0）按 ID 定位用户实际归属租户，
	//   否则 UpdateUser 以 tenant_id=0 执行 UPDATE 匹配不到目标行，角色/组织修改静默失效。
	tid := s.effTenant(r, u)
	if auth.IsSuperAdmin(u) && tid <= 0 {
		if all, le := s.Store.ListAllUsers(); le == nil {
			for _, uu := range all {
				if uu.ID == req.ID {
					tid = uu.TenantID
					break
				}
			}
		}
	}
	// ★ F-35：目标现值前置加载——合并语义与全部越权判据都以真实行为准，
	//   目标不存在时旧实现会带着空字段继续 UPDATE（0 行受影响仍回 success），现明确 404。
	target, terr := s.Store.GetUser(req.ID, tid)
	if terr != nil || target == nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrNotFound, "目标用户不存在"))
		return
	}
	// 请求侧角色字符串（nil 与空串都按「不修改」处理——空角色本就不是合法值）
	reqRole := ""
	if req.Role != nil {
		reqRole = *req.Role
	}
	// 权限校验：目标用户角色检查（防越权操作超管）
	// 租户管理员不能操作超管账号
	if auth.IsSuperAdmin(target) && !auth.IsSuperAdmin(u) {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": "权限不足：不能操作超级管理员"})
		return
	}
	// 非超管不能把用户提升为超管级角色
	if reqRole != "" && auth.RoleLevel(reqRole) >= 4 && !auth.IsSuperAdmin(u) {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": "权限不足：仅超级管理员可分配该角色"})
		return
	}
	// ★ 收口（2026-09-16 安全整改）：等级≥4 角色只允许平台级账号（tenant_id=0）持有——
	//   users/create 侧本有「super/admin 强制落 tid=0」的不变量，但 update 侧此前缺失，
	//   实测可造出 role='admin' AND tenant_id>0 的违规行，穿透旧 IsSuperAdmin 完成
	//   跨租户退款/下载。与 create 口径对齐：非平台归属目标直接拒绝高角色分配。
	if reqRole != "" && auth.RoleLevel(reqRole) >= 4 && tid > 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "超管级角色仅可分配给平台级账号（tenant_id=0），请先调整归属"})
		return
	}
	// 非租户管理员不能分配租户管理员级角色
	if reqRole != "" && auth.RoleLevel(reqRole) >= 3 && !auth.IsTenantAdmin(u) {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": "权限不足：仅租户管理员及以上可分配该角色"})
		return
	}
	// 部门管理员范围：目标用户必须在本部门及子部门树内
	if auth.RoleLevel(u.Role) == 2 {
		if u.OrgID <= 0 {
			writeJSON(w, 403, map[string]interface{}{"success": false, "message": "部门管理员未绑定部门，无法操作"})
			return
		}
		// ★ P0-2 修复（2026-09-14）：目标 org_id=0（未分配，注册/SCIM/建号默认值）也必须拒绝——
		//   旧实现 `target.OrgID > 0` 才做子树校验，org_id=0 时整段校验被跳过，
		//   部门管理员可重置/接管同租户租户管理员账号（未分配部门）。与 Delete 口径对齐。
		if target.OrgID <= 0 {
			writeJSON(w, 403, map[string]interface{}{"success": false, "message": "无权操作未分配部门的账号"})
			return
		}
		inTree, e2 := s.Store.IsOrgInSubtree(tid, u.OrgID, target.OrgID)
		if e2 != nil || !inTree {
			writeJSON(w, 403, map[string]interface{}{"success": false, "message": "无权操作非本部门下账号"})
			return
		}
	}
	// 组织归属校验：非超管不能把用户移出本租户组织（租户隔离，仅校验组织存在性）
	if req.OrgID != nil {
		if err := s.validateOrg(tid, *req.OrgID); err != nil {
			writeJSON(w, 400, map[string]interface{}{"success": false, "message": publicErrMessage(r.Context(), err)})
			return
		}
		// 部门管理员：目标组织也须在本部门子树内
		if auth.RoleLevel(u.Role) == 2 {
			inTree, e2 := s.Store.IsOrgInSubtree(tid, u.OrgID, *req.OrgID)
			if e2 != nil || !inTree {
				writeJSON(w, 403, map[string]interface{}{"success": false, "message": "无权把用户移出本部门范围"})
				return
			}
		}
	}
	// ★ F-35 合并：缺席字段取目标现值，store 委托签名不变（整行覆盖语义由 handler 补齐「不传=不动」）。
	finalDisplay := target.DisplayName
	if req.DisplayName != nil {
		finalDisplay = *req.DisplayName
	}
	finalRole := target.Role
	if reqRole != "" {
		finalRole = reqRole
	}
	finalStatus := target.Status
	if req.Status != nil && *req.Status != "" {
		finalStatus = *req.Status
	}
	orgID := target.OrgID
	if req.OrgID != nil {
		orgID = *req.OrgID
	}
	// ★ F-31（闸 2/2）：终态判据——本次变更合并后的最终形态若是「dept_admin 且无部门」一律拒绝。
	//   只查请求字段会漏两条降级路径：①把 dept_admin 改成 user 之外的角色时顺手摘部门；
	//   ②给存量无部门账号直接把角色升成 dept_admin（org_id 缺席=沿用 0）。
	if auth.RoleLevel(finalRole) == 2 && orgID <= 0 {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "部门管理员必须绑定部门：请同时指定所属部门，或将角色改为普通用户"))
		return
	}
	// 结构化变更轨迹：记录 before/after diff（★ F-35：after 记合并后真实写库值，不记请求原样）
	before := map[string]string{"role": target.Role, "status": target.Status, "display_name": target.DisplayName}
	beforeJSON, _ := json.Marshal(before)
	if err := s.Store.UpdateUser(req.ID, tid, finalDisplay, finalRole, finalStatus, orgID); err != nil {
		// F-64②：越权（403）、目标不存在（404）、终态不合法（400）都在上方逐条拦掉了，
		// 走到这里仍是失败＝写库故障 → 500。
		// 旧 200 壳最坑的一点：改完角色刷新列表还是老值，界面却提示「已提交」，
		// 管理员以为权限已经调整完毕（本函数唯一的兜底失败分支；
		// 也不许取 401——同前端 core.ts 的清登录态拦截，见 handleChangePassword 的注释）。
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	afterJSON, _ := json.Marshal(map[string]string{"role": finalRole, "status": finalStatus, "display_name": finalDisplay})
	// 写入审计 diff（审计留痕：记录角色/状态变更前后值）
	s.Store.LogAuditDiff(tid, u.ID, "user_update", "users", before["display_name"], string(beforeJSON), string(afterJSON))
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// validateOrg 校验组织归属：组织必须属于指定租户（防越权挂到其他租户组织）。
// 参数 tid: 租户 ID；orgID: 组织 ID（0=根组织，直接合法）。
// 返回: nil 表示合法。
func (s *Server) validateOrg(tid, orgID int64) error {
	if orgID <= 0 {
		return nil // 0=根组织/未分配，始终合法
	}
	org, err := s.Store.GetOrgByID(orgID)
	if err != nil {
		return fmt.Errorf("组织不存在")
	}
	if org.TenantID != tid {
		return fmt.Errorf("组织不属于当前租户")
	}
	return nil
}

// handleAdminUserResetPassword 重置密码接口（部门管理员限本部门子树内）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（body 含 id/password）。
// 返回: success=true 表示重置成功；非超管不能重置超管密码。
func (s *Server) handleAdminUserResetPassword(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireDeptAdmin(r)
	if err != nil {
		// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
		s.writeAuthzError(w, r, err)
		return
	}
	var req struct {
		ID       int64  `json:"id"`       // 目标用户 ID
		Password string `json:"password"` // 新密码（存储前转哈希）
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 || req.Password == "" {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	tid := s.effTenant(r, u)
	// ★ 平台上下文（tid=0）下按用户实际归属租户定位（问题2修复：否则 ResetPassword 以 tenant_id=0 匹配不到）
	if auth.IsSuperAdmin(u) && tid <= 0 {
		if all, le := s.Store.ListAllUsers(); le == nil {
			for _, uu := range all {
				if uu.ID == req.ID {
					tid = uu.TenantID
					break
				}
			}
		}
	}
	// 权限校验：非超管不能重置超管密码（防越权）
	if target, err := s.Store.GetUser(req.ID, tid); err == nil && auth.IsSuperAdmin(target) && !auth.IsSuperAdmin(u) {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": "权限不足：不能操作超级管理员"})
		return
	}
	// 部门管理员范围：目标用户须在本部门子树内
	if auth.RoleLevel(u.Role) == 2 {
		if u.OrgID <= 0 {
			writeJSON(w, 403, map[string]interface{}{"success": false, "message": "部门管理员未绑定部门，无法操作"})
			return
		}
		// ★ P0-2 修复（2026-09-14）：org_id=0（未分配）目标必须拒绝——旧实现跳过校验，
		//   部门管理员可重置租户管理员密码完成接管。与 Update/Delete 口径对齐。
		target, e := s.Store.GetUser(req.ID, tid)
		if e != nil {
			writeJSON(w, 404, map[string]interface{}{"success": false, "message": "目标用户不存在"})
			return
		}
		if target.OrgID <= 0 {
			writeJSON(w, 403, map[string]interface{}{"success": false, "message": "无权操作未分配部门的账号"})
			return
		}
		inTree, e2 := s.Store.IsOrgInSubtree(tid, u.OrgID, target.OrgID)
		if e2 != nil || !inTree {
			writeJSON(w, 403, map[string]interface{}{"success": false, "message": "无权操作非本部门下账号"})
			return
		}
	}
	// 租户隔离：仅重置生效租户下的用户
	if err := s.Store.ResetPassword(req.ID, tid, auth.PasswordHash(req.Password)); err != nil {
		// F-64②：管理员重置他人与否的判据都过了，写库仍失败＝存储故障 → 500。
		// 严禁 401：管理员此刻在后台会话里，前端 request() 吃 401 会直接把他踢回登录页
		// （/api/admin/users/reset-password 不在 core.ts 的 401 豁免名单中）。
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	// ★ F-63（批 I-3）：detail 补「被重置的账号」，并留「首登强制改密」标记（旧实现空串：
	//   管理员重置了谁的口令无从回查）。口令内容仍一律不入审计。
	//   审计归属租户同时改为实际生效的 tid（旧写 u.TenantID：超管跨租户重置时轨迹记在平台租户 0，
	//   与 F-55 同族——「谁的数据被改了」记错对象）。
	targetName := ""
	if t, e := s.Store.GetUser(req.ID, tid); e == nil && t != nil {
		targetName = t.Username
	}
	auditLabel := targetName
	if auditLabel == "" {
		auditLabel = fmt.Sprintf("uid=%d", req.ID)
	}
	s.Store.LogAudit(tid, u.ID, "user_reset_pwd", "users", "管理员重置账号 "+auditLabel+" 的登录口令")
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// handleAdminUserDelete 删除用户账号（部门管理员及以上）。
// 权限：非超管不能删超管；部门管理员仅能删本部门子树内账号；不能删除自己。
func (s *Server) handleAdminUserDelete(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireDeptAdmin(r)
	if err != nil {
		// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
		s.writeAuthzError(w, r, err)
		return
	}
	var req struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	if req.ID == u.ID {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "不能删除自己的账号"})
		return
	}
	tid := s.effTenant(r, u)
	target, err := s.Store.GetUser(req.ID, tid)
	if err != nil {
		// 平台上下文（tid=0）下按用户实际归属租户定位
		if tid <= 0 {
			// 历史遗留空操作：按空用户名做全局查询无意义，结果被显式丢弃，仅占位保留
			if matches, e := s.Store.GetUserByUsernameGlobal(""); e == nil {
				_ = matches
			}
			all, le := s.Store.ListAllUsers()
			if le == nil {
				for _, uu := range all {
					if uu.ID == req.ID {
						tid = uu.TenantID
						target = uu
						break
					}
				}
			}
		}
		if target == nil {
			// F-64②：按 ID 定位不到目标账号（含跨租户不可见）＝记录不存在 → 404 NOT_FOUND。
			// 取 404 而不是 403：本接口是「对这个用户动手」，目标不在生效租户下时
			// 与「根本不存在」回同一口径，既诚实又不泄露其它租户是否存在该 ID；
			// 更不能用 401（管理员是登录态，前端会清会话踢回登录页）。
			s.writeError(w, r, apierrors.New(apierrors.ErrNotFound, "用户不存在"))
			return
		}
	}
	// 删除闸口：非超管不得删超管；部门管理员限同租户且目标账号在本部门子树内
	if auth.IsSuperAdmin(target) && !auth.IsSuperAdmin(u) {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": "权限不足：不能操作超级管理员"})
		return
	}
	if auth.RoleLevel(u.Role) == 2 {
		if u.OrgID <= 0 || target.OrgID <= 0 || target.TenantID != tid {
			writeJSON(w, 403, map[string]interface{}{"success": false, "message": "无权删除非本部门账号"})
			return
		}
		inTree, e := s.Store.IsOrgInSubtree(tid, u.OrgID, target.OrgID)
		if e != nil || !inTree {
			writeJSON(w, 403, map[string]interface{}{"success": false, "message": "无权删除非本部门账号"})
			return
		}
	}
	if err := s.Store.DeleteUser(req.ID, tid); err != nil {
		// F-64②：目标已在上方按 ID 定位过（查不到就是 404），走到这里仍失败＝写库故障
		// 或并发删除（0 行受影响时 iam 回业务错误「用户不存在」）→ 500 兜底。
		// 不判 404 也不判 409：本层没有可靠的错误类型可分辨（只有裸中文 error 串，
		// 靠字符串猜会把真故障误判成业务态）；更不许 401——管理员在后台会话里，
		// 前端 request() 吃 401 会直接清 token 把他踢回登录页。
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	s.Store.LogAudit(u.TenantID, u.ID, "user_delete", "users", target.Username)
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// handleUpdateEmail 登录用户自助绑定/修改邮箱（强提醒维护策略的数据入口）。
// 校验：格式合法 + 全局唯一（他人已绑定则拒绝）；成功后立即可接收验证码。
// ★ F-64②（批 I-10）：验证码核验失败=400、邮箱被占用=409、写库失败=500 走统一出口；
//
//	这一组里**没有一个**该用 401——用户本来就在登录态里改设置，401 会被前端
//	core.ts 的 handleUnauthorized 当成「会话失效」清 token 踢回登录页。
func (s *Server) handleUpdateEmail(w http.ResponseWriter, r *http.Request) {
	u := s.authUser(r)
	if u == nil {
		writeJSON(w, 401, map[string]interface{}{"success": false, "message": "未登录"})
		return
	}
	var req struct {
		Email   string `json:"email"`    // 新邮箱
		NewCode string `json:"new_code"` // 发往新邮箱的验证码（证明对新邮箱支配权）
		OldCode string `json:"old_code"` // 发往原绑定邮箱的验证码（防劫持：证明对旧邮箱支配权）
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	if emailRe.MatchString(email) == false {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "邮箱格式不正确"})
		return
	}
	// ★ S3 防薅：一次性邮箱拒绑
	if msg := s.disposableEmailRejected(email); msg != "" {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": msg})
		return
	}
	// ★ 新邮箱撞库预检（2026-08-26 需求）：目标邮箱出现在邀请奖励流水中即拒绝换绑——
	//   从账户层入口杜绝「换到带奖励历史的邮箱 → 后续受邀零奖励」的死胡同。
	if s.Store.EmailInRewardLedger(email) {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "该邮箱已被用于邀请注册奖励，无法换绑"})
		return
	}
	if strings.TrimSpace(req.NewCode) == "" {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请输入新邮箱验证码"})
		return
	}
	// ★ 已绑定过邮箱的账号：还需校验「原邮箱」验证码，双重确认防止账号邮箱被单点劫持
	oldEmail := strings.ToLower(strings.TrimSpace(u.Email))
	if oldEmail != "" && strings.TrimSpace(req.OldCode) == "" {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请输入原邮箱验证码"})
		return
	}
	if oldEmail != "" && !verifyEmailCode(oldEmail, req.OldCode) {
		// F-64②：原邮箱验证码不匹配＝这次提交的凭证不对 → 400 VALIDATION_ERROR。
		// ★ 严禁 401：这是**已登录用户**在换绑邮箱，前端 request() 一见 401 就清 token
		//   并踢回登录页（豁免名单只有 /api/auth/login 与 /api/auth/register），
		//   一次「验证码看错」会把整条设置流程连同刚填的表单一起抹掉。
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "原邮箱验证码错误或已过期"))
		return
	}
	if !verifyEmailCode(email, req.NewCode) {
		// F-64②：同上——新邮箱验证码错是载荷层面的核验失败 → 400，绝不 401（清会话）
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "新邮箱验证码错误或已过期"))
		return
	}
	if other, err := s.Store.GetUserByEmail(email); err == nil && other != nil && other.ID != u.ID {
		// F-64②：邮箱已被他人占用＝与既有记录的状态冲突 → 409 CONFLICT，
		// 前端据此才能走「换一个邮箱」而不是「重新登录」；401 同样禁止（理由见上两处）
		s.writeError(w, r, apierrors.New(apierrors.ErrConflict, "该邮箱已被其他账号绑定"))
		return
	}
	if err := s.Store.SetUserEmail(u.ID, u.TenantID, email); err != nil {
		// F-64②：三重核验都过了仍写不进去＝存储层故障 → 500
		//（SetUserEmail 内部的占用守卫也会回错，但那条已由上方 409 分支拦掉，
		//  这里再判一次字符串没意义，且会把真故障误报成业务冲突）
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	s.Store.LogAudit(u.TenantID, u.ID, "update_email", "users", email)
	writeJSON(w, 200, map[string]interface{}{"success": true})
}
