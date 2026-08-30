// ============ captcha.go · 职责说明 ============
// api 包内部实现文件。
// =============================================
package api

// ============ 本文件职责中文说明 ============
// 人机验证接缝（第三批前置）：配置驱动的 CAPTCHA 校验适配器。
//   - captcha_provider=none（默认）：不校验，行为与历史版本一致
//   - captcha_provider=turnstile：校验 Cloudflare Turnstile token
//     （需配置 captcha_secret_key；站点 key 经 /api/auth/register-config 下发给前端渲染组件）
// 接入点：自助注册 handleRegister 与发码 handleEmailCode——防脚本批量薅试用额度的最后一道闸。
// 未来接入极验/腾讯天御：新增一个 verify 分支即可，业务代码零改动。
// =============================================

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// turnstileVerifyURL Cloudflare Turnstile 服务端校验端点（captcha_provider=turnstile 时启用）。
const turnstileVerifyURL = "https://challenges.cloudflare.com/turnstile/v0/siteverify"

// captchaProvider 读取当前人机验证提供方（小写归一化；空/none 均视为关闭）。
func (s *Server) captchaProvider() string {
	v, _ := s.Store.GetConfig("captcha_provider")
	return strings.ToLower(strings.TrimSpace(v))
}

// captchaEnabled 判断是否启用人机验证（仅 turnstile 已实现）。
func (s *Server) captchaEnabled() bool { return s.captchaProvider() == "turnstile" }

// verifyCaptcha 校验前端提交的人机验证 token；未启用时恒通过。
// 参数 r: HTTP 请求（取客户端 IP 供 siteverify 参考）；token: 前端组件产出的一次性令牌。
func (s *Server) verifyCaptcha(r *http.Request, token string) error {
	// 未启用人机验证时直接放行
	if !s.captchaEnabled() {
		return nil
	}
	// token 为空时拒绝
	if strings.TrimSpace(token) == "" {
		return &apiErr{"请完成人机验证"}
	}
	// 读取 Cloudflare Turnstile 服务端密钥
	secret, _ := s.Store.GetConfig("captcha_secret_key")
	if secret == "" {
		return &apiErr{"人机验证未配置密钥，请联系管理员"}
	}
	// 构造表单参数：secret + response（token）+ remoteip（可选）
	form := url.Values{
		"secret":   {secret},
		"response": {strings.TrimSpace(token)},
	}
	if ip := clientIP(r); ip != "" {
		form.Set("remoteip", ip)
	}
	// 调用 Cloudflare Turnstile 服务端校验接口
	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.PostForm(turnstileVerifyURL, form)
	if err != nil {
		return &apiErr{"人机验证服务不可用，请稍后再试"}
	}
	defer resp.Body.Close()
	// 解析响应并判断校验结果
	var vr struct {
		Success bool `json:"success"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&vr); err != nil || !vr.Success {
		return &apiErr{"人机验证失败，请重试"}
	}
	return nil
}

// captchaSiteKey 读取站点 key（公开下发用；provider 非 turnstile 时返回空）。
func (s *Server) captchaSiteKey() string {
	if !s.captchaEnabled() {
		return ""
	}
	v, _ := s.Store.GetConfig("captcha_site_key")
	return strings.TrimSpace(v)
}
