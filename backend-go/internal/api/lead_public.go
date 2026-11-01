// ============ lead_public.go · 职责说明 ============
// ★ DEF-P0-03（2026-11-01 云端 UAT 发布前复核，D-3 已拍板）：零 JS／拿不到人机验证 token 时的**留资降级通道**。
//
// 存在的理由：官网界面（含 `LeadForm`）全靠入口 JS 挂载，而现网国内访客跨境取那一个入口
// 出现过 32–75 秒／整片白屏（DEF-P0-01/02）。链路一塌，`index.html` 直出的那张静态表单就是
// **唯一的客户入口**；可它没有 token 可拿（Turnstile/极验都要 JS）⇒ 照 `/api/lead` 的口径必被拒。
// 本端点默认**不要 captcha**，代价用四道自保补：蜜罐 ＋ 更紧的 IP 配额 ＋ 一次性邮箱域名黑名单
// ＋ 两条"看起来不像真人"的升级信号（命中即当场要求 captcha）。
//
// ⚠️ 三条诚实边界（写在实现旁边才有人读，否则会被误读成"防护做全了"）：
//  1. **S1 只统计"在本端点上出现过的登录身份"**，不是全站登录监控。
//     静态表单既没有设备号也没有 uid ⇒ S1 对那一条腿**结构上不触发**（这是判据的能力边界，不是漏配）；
//     那条腿的防线就是蜜罐＋紧配额＋黑名单三道，别指望 S1 兜匿名。
//  2. **S2 读的是现成的 `login_fail` 账本，窗口 300s（5 分钟）**，不是 24h。
//     拿 24h 语义去读 300s 账本会出现"白天有人输错过、晚上静态表单就要验证码"的随机故障感；
//     反过来把窗口拉到 24h 会误伤共享出口（同一 NAT 后面多人）——**保持 300s，别调**。
//  3. **免 captcha ≠ 免防护，也 ≠ 免打标**：落库摘要、站内信与告警一律点名「免验证通道」。
//     两类线索可信度不同，混在一起看一次垃圾洪峰之后这个面就再没人信（同 AGENTS §一·15「读数要分得开」）。
//
// =============================================
package api

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	apierrors "translator/internal/errors"
	"translator/internal/observability"
	"translator/internal/store"
)

// 本通道的档位与账本名（都是"对外可读"的口径：日志、告警与运营看的就是这几个串）。
const (
	// 免验证通道的 IP 配额：比 /api/lead（10/日、5s 间隔）收紧一档——这一条腿没有人机验证兜底。
	leadGuardDailyPublic    = 3
	leadGuardIntervalPublic = 10
	// leadIdentWindowSec 升级判据 S1 的分组窗口（24 小时）。
	leadIdentWindowSec = 86400
	// S1 用新账本（按 (IP,设备号) 分组 / 组内身份），S2 复用现成的 login_fail——不另造第三套计数。
	leadIdentScope    = "lead_ident"
	leadIdentUIDScope = "lead_ident_uid"
	// leadGuardPublicPrefix 配额账本前缀：与 "lead:" 分开，两本账不许互相扣格子。
	leadGuardPublicPrefix = "lead-public:"
	// leadCaptchaAskText 升级后的对外文案（与 captcha.go 里 verifyCaptcha 那句同口径）。
	leadCaptchaAskText = "请完成人机验证后提交"
	// leadPublicSource 本通道落库摘要里的来源标签（也是"这条线索从降级面来的"的运营口径）。
	leadPublicSource = "static_fallback"
)

// leadEscalation 记录"这一次提交凭什么要人机验证"（档位名是对外排障契约，日志与告警都点名它）。
type leadEscalation struct {
	Reason string // ""＝不升级；"multi_identity"＝S1；"cred_fail_burst"＝S2
	Detail string // 人可读读数（几个身份／几次失败、窗口多长）
}

// handleLeadCreatePublic POST /api/lead/public：匿名留资降级通道。
// 校验顺序：方法 → 解码（表单/JSON 分流）→ 蜜罐（假成功）→ 必填与格式 → 一次性邮箱黑名单
// → IP 配额 → 升级信号（命中才要人机验证，省一次对外校验）→ 落库（打标）→ 通知/告警 → 出路。
func (s *Server) handleLeadCreatePublic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, r, apierrors.New(apierrors.ErrMethodNotAllowed, "仅支持 POST"))
		return
	}
	req, formMode, err := s.leadDecode(r)
	if err != nil {
		// ★ 解码失败与必填缺失**分开回**（§E-2 同一条纪律）：这一档说的是"body 看不懂"，不是"你没填公司名"。
		s.leadFail(w, r, formMode, apierrors.ErrValidation, "请求格式错误")
		return
	}
	// 蜜罐命中：按成功返回但不落库不打扰超管（不给 bot 反馈信号；与 lead.go 同一判据同一形态）
	if strings.TrimSpace(req.Site) != "" {
		s.leadOK(w, r, formMode)
		return
	}
	company := truncateRunes(strings.TrimSpace(req.Company), leadMaxCompany)
	email := strings.ToLower(strings.TrimSpace(req.Email))
	langs := truncateRunes(strings.TrimSpace(req.Langs), leadMaxLangs)
	message := truncateRunes(strings.TrimSpace(req.Message), leadMaxMessage)
	if company == "" {
		s.leadFail(w, r, formMode, apierrors.ErrValidation, "请填写公司/团队名称")
		return
	}
	if !validLeadEmail(email) {
		s.leadFail(w, r, formMode, apierrors.ErrValidation, "请填写有效的联系邮箱")
		return
	}
	if leadDisposableDomain(email, s.leadDisposableDomains()) {
		s.leadFail(w, r, formMode, apierrors.ErrValidation, "请填写常用的联系邮箱（临时邮箱收不到回复）")
		return
	}
	src := leadSourceOrDefault(req.Source) // 与 /api/lead 同一份白名单（本端点多认 static_fallback）
	// 设备号格式不合＝当没上报（不返 400：新增防护不拿可用性交押金，AGENTS §一·10）
	device := ""
	if id := strings.TrimSpace(req.DeviceID); trialDeviceIDRe.MatchString(id) {
		device = id
	}
	ip := clientIP(r)

	if ok, wait := s.regGuard.allow(leadGuardPublicPrefix+ip, leadGuardDailyPublic, leadGuardIntervalPublic); !ok {
		s.leadFail(w, r, formMode, apierrors.ErrRateLimited, "提交过于频繁，请 "+strconv.Itoa(wait)+" 秒后再试")
		return
	}

	// 升级信号（★ D-3 拍板：命中任一条即当场要求人机验证）
	if esc := s.leadEscalationFor(r.Context(), ip, device); esc.Reason != "" {
		if verr := s.verifyCaptcha(r, req.Captcha); verr != nil {
			observability.Warn(r.Context(), "留资免验证通道命中升级信号，已要求人机验证",
				"reason", esc.Reason, "detail", esc.Detail, "ip", ip)
			msg := strings.TrimSpace(verr.Error())
			if msg == "" {
				msg = leadCaptchaAskText
			}
			s.leadFail(w, r, formMode, apierrors.ErrValidation, msg)
			return
		}
	}

	content := truncateRunes("【销售线索·免验证通道】"+leadSummaryLine(company, email, langs, src, message), leadContentRuneCap)
	f := &store.Feedback{TenantID: 0, UserID: 0, TargetType: "lead", Content: content, TargetLangs: langs}
	if err := s.Store.CreateFeedback(f); err != nil {
		s.leadFail(w, r, formMode, apierrors.ErrInternal, "提交失败，请稍后再试")
		return
	}
	s.regGuard.record(leadGuardPublicPrefix + ip) // 坐实点＝线索真落库那一刻（成功才计数，与 forgot/reset 同语义）
	s.recordLeadIdentities(r, ip, device)

	s.notifySuperAdmins("收到新的销售线索（免验证通道）",
		company+"（"+email+"）", store.NotifyRefFeedback, f.ID)
	s.Store.CreateAlert(0, "warning", "lead",
		"新销售线索·免验证通道："+company+"｜"+email+"｜意向语言："+orNone(langs))
	s.leadOK(w, r, formMode)
}

// leadEscalationFor 算两条升级信号（★ D-3：同 IP＋同设备合并判断 ＋ 同 IP 密码输错突发）。
// Reason=="" 表示这一次不需要人机验证。
func (s *Server) leadEscalationFor(ctx context.Context, ip, device string) leadEscalation {
	if s.Store == nil || ip == "" {
		return leadEscalation{} // 取不到账本＝不升级（fail-open：一次库抖动不该把唯一客户入口打成"请填验证码"）
	}
	// S1：同 IP ＋ 同设备 分组内出现 ≥ lead_ident_min 个不同登录身份（24h 窗口）
	if device != "" {
		min := s.Store.ConfigInt("lead_ident_min", 2)
		group := ip + ":" + device
		n, err := s.Store.CountDistinctRateKeys(leadIdentUIDScope, group+":", time.Now().Unix()-leadIdentWindowSec)
		if err == nil && n >= min {
			return leadEscalation{Reason: "multi_identity",
				Detail: strconv.Itoa(n) + " 个身份 / 近 24h 同 IP+同设备"}
		}
	}
	// S2：同 IP 近 5 分钟登录失败 ≥ lead_cred_fail_min 次（读现成 login_fail 账本）
	minFails := s.Store.ConfigInt("lead_cred_fail_min", 2)
	st, err := s.Store.RateLoad(loginIPScope, ip)
	if err == nil && st.Count >= int64(minFails) && time.Now().Unix()-st.WindowStart <= int64(loginWindowSec) {
		return leadEscalation{Reason: "cred_fail_burst",
			Detail: strconv.FormatInt(st.Count, 10) + " 次登录失败 / 近 " + strconv.Itoa(loginWindowSec/60) + " 分钟"}
	}
	return leadEscalation{}
}

// recordLeadIdentities 写 S1 的两本账（分组存在性 ＋ 组内身份）。
// uid 取当前请求的登录用户；匿名（静态表单那一腿）⇒ 身份本不写，S1 自然不触发。
func (s *Server) recordLeadIdentities(r *http.Request, ip, device string) {
	if s.Store == nil || ip == "" || device == "" {
		return
	}
	group := ip + ":" + device
	if _, err := s.Store.RateRecord(leadIdentScope, group, leadIdentWindowSec); err != nil {
		return
	}
	if u := s.authUser(r); u != nil && u.ID > 0 {
		if _, err := s.Store.RateRecord(leadIdentUIDScope, group+":"+strconv.FormatInt(u.ID, 10), leadIdentWindowSec); err != nil {
			observability.Warn(r.Context(), "留资升级账本写入失败（只影响下一次是否要求验证码，不影响本次提交）",
				"scope", leadIdentUIDScope, "err", err.Error())
		}
	}
}

// leadDisposableDomains 一次性邮箱域名黑名单：system_config 的现值优先，取不到/为空回内置档。
// 名单只在这一处读（前端不许抄第二份——那是又一把尺子）。
func (s *Server) leadDisposableDomains() []string {
	raw := ""
	if s.Store != nil {
		raw, _ = s.Store.GetConfig("lead_disposable_domains")
	}
	return leadDisposableList(raw)
}

// leadDisposableList 把配置串拆成域名表；空串/全空 ⇒ 回内置档（默认就挡最烂的那几家）。
func leadDisposableList(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r' || r == ' ' || r == '\t'
	})
	out := make([]string, 0, len(fields)+len(leadDisposableDefault))
	for _, f := range fields {
		if d := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(f, "@"))); d != "" {
			out = append(out, d)
		}
	}
	if len(out) == 0 {
		return leadDisposableDefault
	}
	return out
}

// leadDisposableDefault 内置档：只收"公开广告自己就是临时邮箱"的那几家（宁窄勿宽——
// 误拦一个正常企业邮箱的代价是**丢掉一条真线索**，比放过一个 bot 严重）。
var leadDisposableDefault = []string{
	"mailinator.com", "guerrillamail.com", "sharklasures.com", "10minutemail.com",
	"temp-mail.org", "tempmailo.com", "throwawaymail.com", "yopmail.com",
	"getnada.com", "maildrop.cc", "trashmail.com", "dispostable.com",
}

// leadDisposableDomain 邮箱域名是否命中黑名单（裸域相等或作为子域后缀；`@` 前的本地部分不参与）。
func leadDisposableDomain(email string, list []string) bool {
	at := strings.LastIndex(email, "@")
	if at < 0 {
		return false
	}
	dom := strings.ToLower(strings.TrimSpace(email[at+1:]))
	if dom == "" {
		return false
	}
	for _, d := range list {
		if d == "" {
			continue
		}
		if dom == d || strings.HasSuffix(dom, "."+d) {
			return true
		}
	}
	return false
}
