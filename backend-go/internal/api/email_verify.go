// ============ email_verify.go · 职责说明 ============
// api 包内部实现文件。
// =============================================
package api

// ============ 本文件职责中文说明 ============
// 注册邮箱验证（第二批·轻第三方）：自助注册前先验证邮箱归属，堵住脚本批量
// 注册薅试用额度的口子（与 registration_review 审核开关叠加使用）。
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

	"translator/internal/mail"
)

// emailCode 单邮箱的验证码状态
type emailCode struct {
	Code      string    // 6 位数字码
	ExpiresAt time.Time // 过期时间（10 分钟）
	Attempts  int       // 已错误尝试次数（≥5 作废防爆破）
	SentAt    time.Time // 发送时间（冷却判断）
}

// emailVerifyStore 邮箱验证码存储（并发安全）
type emailVerifyStore struct {
	mu    sync.Mutex
	codes map[string]*emailCode // key: 小写邮箱
}

// 全局单例：与 resetCodes 同生命周期（进程内存）
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
// 参数：s=服务（用于 mailer 与 IP 日上限）；ip=请求方 IP；email=目标邮箱。
func (s *Server) sendEmailCode(ip, email string) (bool, string, bool) {
	key := strings.ToLower(strings.TrimSpace(email))
	now := time.Now()
	emailCodes.mu.Lock()
	if ec, ok := emailCodes.codes[key]; ok {
		// 冷却期内拒绝重发
		if now.Sub(ec.SentAt) < emailCodeCooldown {
			wait := int((emailCodeCooldown - now.Sub(ec.SentAt)).Seconds()) + 1
			emailCodes.mu.Unlock()
			return false, fmt.Sprintf("发送过于频繁，请 %d 秒后再试", wait), false
		}
	}
	code, err := genResetCode()
	if err != nil {
		emailCodes.mu.Unlock()
		return false, "生成验证码失败", false
	}
	emailCodes.codes[key] = &emailCode{Code: code, ExpiresAt: now.Add(emailCodeTTL), SentAt: now}
	emailCodes.mu.Unlock()

	sender := s.mailer()
	_, isNoop := sender.(*mail.NoopSender)
	err = s.sendTemplatedMail(key, "register_code", map[string]string{"code": code})
	if err != nil {
		return false, "邮件发送失败，请稍后重试", isNoop
	}
	return true, "验证码已发送，请查收邮箱", isNoop
}

// verifyEmailCode 校验并一次性消费验证码；错误尝试超限作废。
func verifyEmailCode(email, code string) bool {
	key := strings.ToLower(strings.TrimSpace(email))
	emailCodes.mu.Lock()
	defer emailCodes.mu.Unlock()
	ec, ok := emailCodes.codes[key]
	if !ok || ec.Attempts >= emailCodeMaxTries || time.Now().After(ec.ExpiresAt) {
		return false
	}
	if ec.Code != strings.TrimSpace(code) {
		ec.Attempts++
		return false
	}
	delete(emailCodes.codes, key) // 验证通过即消费
	return true
}

// handleEmailCode 发送注册验证码接口（公开；受注册频率护栏同款 IP 日上限约束）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（body 含 email）。
// 返回: success=true 时已发送；noop=true 表示 MAIL_ENABLED 未开启（码打印在服务端日志）。
func (s *Server) handleEmailCode(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email        string `json:"email"`
		CaptchaToken string `json:"captcha_token"` // 人机验证 token（启用 turnstile 时必填）
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	email := strings.TrimSpace(req.Email)
	if !emailRe.MatchString(email) {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "邮箱格式不正确"})
		return
	}
	// 人机验证：防脚本刷短信/邮件接口
	if err := s.verifyCaptcha(r, req.CaptchaToken); err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	// 单 IP 每日发码上限（复用注册护栏的窗口逻辑，独立计数键）
	ip := clientIP(r)
	if ok, wait := s.regGuard.allow("email-code:"+ip, emailSendIPDailyNm, 0); !ok {
		w.Header().Set("Retry-After", itoaInt(wait))
		writeJSON(w, 429, map[string]interface{}{"success": false,
			"message": fmt.Sprintf("发送过于频繁，请稍后再试")})
		return
	}
	ok, msg, noop := s.sendEmailCode(ip, email)
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
// 返回: success=true 时携带 email_verify_enabled / registration_review。
func (s *Server) handleRegisterConfig(w http.ResponseWriter, r *http.Request) {
	ev := false
	if v, _ := s.Store.GetConfig("email_verify_enabled"); v == "1" {
		ev = true
	}
	rv := false
	if v, _ := s.Store.GetConfig("registration_review"); v == "1" {
		rv = true
	}
	writeJSON(w, 200, map[string]interface{}{
		"success":              true,
		"email_verify_enabled": ev,
		"registration_review":  rv,
		"captcha_enabled":      s.captchaEnabled(),
		"captcha_site_key":     s.captchaSiteKey(),
	})
}

// itoaInt int 转字符串（本文件内使用的简短别名）。
func itoaInt(n int) string { return fmt.Sprintf("%d", n) }

// handleMeEmailCode 登录用户向「新邮箱」发送变更验证码（修改邮箱专用，需登录）。
// 与注册发码共用存储/冷却/有效期；不做人机验证（已登录态），但受 60s 冷却与日上限约束。
func (s *Server) handleMeEmailCode(w http.ResponseWriter, r *http.Request) {
	u := s.authUser(r)
	if u == nil {
		writeJSON(w, 401, map[string]interface{}{"success": false, "message": "未登录"})
		return
	}
	var req struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	email := strings.TrimSpace(req.Email)
	if !emailRe.MatchString(email) {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "邮箱格式不正确"})
		return
	}
	if other, err := s.Store.GetUserByEmail(email); err == nil && other != nil && other.ID != u.ID {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": "该邮箱已被其他账号绑定"})
		return
	}
	ok, msg, noop := s.sendEmailCode(clientIP(r), email)
	status := 200
	if !ok && strings.Contains(msg, "频繁") {
		status = 429
	}
	writeJSON(w, status, map[string]interface{}{"success": ok, "message": msg, "noop": noop && ok})
}
