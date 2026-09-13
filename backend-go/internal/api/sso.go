// ============ sso.go · 职责说明 ============
// 阶段六 SSO / OIDC 的 HTTP 接口：
//   - GET  /api/sso/login?provider=   生成 state 写入 HttpOnly cookie，重定向至 IdP 授权页
//   - GET  /api/sso/callback?provider=&code=&state=  校验 state，换取用户信息，
//     按邮箱匹配平台账号（飞书无邮箱则失败），命中则签发 JWT 并带 ?token= 重定向前端；
//     配置 auto_provision 时自动开通账号。
//
// 与既有登录体系一致：最终都签发同一套 JWT（前端 localStorage 托管），无独立会话态。
package api

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"net/url"
	"strings"
	"time"

	"encoding/json"
	"translator/internal/auth"
	"translator/internal/auth/sso"
	apierrors "translator/internal/errors"

	"translator/internal/config"
	"translator/internal/store"
)

// ssoStateCookie state 校验用 Cookie 名（防 CSRF）。
const ssoStateCookie = "sso_state"

// routesSSO 注册 SSO 相关路由（未配置 IdP 时路由仍注册，但返回未启用）。
func (s *Server) routesSSO() {
	s.mux.HandleFunc("/api/sso/login", s.handleSSOLogin)
	s.mux.HandleFunc("/api/sso/callback", s.handleSSOCallback)
	s.mux.HandleFunc("/api/sso/providers", s.handleSSOProviders)
	// ★ B3：SSO 一次性 code → JWT 兑换端点（公开：code 本身即凭证，60s TTL、单次消费）
	s.mux.HandleFunc("/api/auth/sso/exchange", s.handleSSOExchange)
}

// handleSSOProviders 列出已启用的 IdP（供前端渲染登录按钮）。
func (s *Server) handleSSOProviders(w http.ResponseWriter, r *http.Request) {
	if s.SSO == nil || !s.SSO.Enabled() {
		writeJSON(w, 200, map[string]interface{}{"enabled": false, "providers": []interface{}{}})
		return
	}
	list := make([]map[string]string, 0)
	for _, p := range s.SSO.List() {
		list = append(list, map[string]string{"name": p.Name, "display_name": p.DisplayName})
	}
	writeJSON(w, 200, map[string]interface{}{"enabled": true, "providers": list})
}

// handleSSOLogin 生成 state 并跳转 IdP 授权页。
func (s *Server) handleSSOLogin(w http.ResponseWriter, r *http.Request) {
	if s.SSO == nil || !s.SSO.Enabled() {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "SSO 未启用"))
		return
	}
	name := r.URL.Query().Get("provider")
	p, ok := s.SSO.Get(name)
	if !ok {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "未知的 IdP: "+name))
		return
	}
	state := sso.NewState()
	authURL := p.AuthURL(state)
	if authURL == "" {
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, "IdP 发现失败（检查 issuer/端点配置与网络）"))
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     ssoStateCookie,
		Value:    state,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   httpsDetected(r), // ★ B3：HTTPS 下强制 Secure（反代场景按 X-Forwarded-Proto 判定）
		MaxAge:   600,
	})
	http.Redirect(w, r, authURL, http.StatusFound)
}

// handleSSOCallback IdP 回调解码：校验 state → 换用户信息 → 匹配/开通账号 → 签发 JWT。
func (s *Server) handleSSOCallback(w http.ResponseWriter, r *http.Request) {
	if s.SSO == nil || !s.SSO.Enabled() {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "SSO 未启用"))
		return
	}
	name := r.URL.Query().Get("provider")
	p, ok := s.SSO.Get(name)
	if !ok {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "未知的 IdP: "+name))
		return
	}
	// state 校验（防 CSRF）
	stateCookie, err := r.Cookie(ssoStateCookie)
	if err != nil || stateCookie.Value == "" {
		s.writeError(w, r, apierrors.New(apierrors.ErrUnauthorized, "缺少 state cookie，请重新发起登录"))
		return
	}
	if stateCookie.Value != r.URL.Query().Get("state") {
		s.writeError(w, r, apierrors.New(apierrors.ErrUnauthorized, "state 不匹配，疑似 CSRF"))
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "IdP 未返回授权码"))
		return
	}
	info, err := p.Exchange(r.Context(), code)
	if err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, "换取用户信息失败: "+err.Error()))
		return
	}
	if info.Email == "" {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "IdP 未返回邮箱，无法匹配平台账号"))
		return
	}
	u, err := s.Store.GetUserByEmail(info.Email)
	// ★ B3：命中已有账号也须复核生效状态（旧实现跳过禁用/注销检查，被停号可借 SSO 复活登录）
	if u != nil {
		if st := auth.EffectiveUserStatus(u.Status, u.DeactivatedAt); st != store.UserActive && st != store.UserDeactivating {
			s.writeError(w, r, apierrors.New(apierrors.ErrForbidden, "账号已停用或注销，无法通过 SSO 登录"))
			return
		}
	}
	if err != nil || u == nil {
		// 自动开通
		cfg := s.ssoProviderConfig(name)
		if cfg == nil || !cfg.AutoProvision {
			s.writeError(w, r, apierrors.New(apierrors.ErrForbidden, "该邮箱未开通账号，请联系管理员"))
			return
		}
		u, err = s.provisionSSOUser(info, cfg.DefaultTenantID)
		if err != nil {
			s.writeError(w, r, apierrors.New(apierrors.ErrInternal, "自动开通失败: "+err.Error()))
			return
		}
	}
	// 签发 JWT（与密码登录同套机制）
	tok, err := auth.Sign(u, 24*time.Hour)
	if err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, "签发失败"))
		return
	}
	s.Store.TouchLogin(u.ID)
	s.Store.LogAudit(u.TenantID, u.ID, "login_sso", "auth", "SSO 登录:"+name)
	// 清除 state cookie
	http.SetCookie(w, &http.Cookie{Name: ssoStateCookie, Path: "/", HttpOnly: true, MaxAge: -1})
	// 重定向前端（带 token），与品牌域名跳转机制一致
	front := strings.TrimRight(s.Cfg.SSOFrontendURL, "/")
	if front == "" {
		writeJSON(w, 200, map[string]interface{}{"success": true, "token": tok, "user": map[string]interface{}{
			"id": u.ID, "username": u.Username, "display_name": u.DisplayName, "role": u.Role, "tenant_id": u.TenantID,
		}})
		return
	}
	// ★ B3（2026-09-12）：不再把 JWT 拼进重定向 URL（地址栏/Referer/网关日志长期泄漏）。
	//   改为签发 60s 一次性 code，前端落地后 POST /api/auth/sso/exchange 换取 token。
	xcode := newSSOExchangeCode()
	if xcode == "" {
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, "生成兑换码失败"))
		return
	}
	payload, _ := json.Marshal(map[string]int64{"uid": u.ID, "tid": u.TenantID})
	vcodeSet("sso_xchg:"+xcode, payload, 60*time.Second)
	// code 为一次性短效凭据（60s、消费即删），经查询参数交予前端再兑换 JWT
	http.Redirect(w, r, front+"/?sso_code="+url.QueryEscape(xcode), http.StatusFound)
}

// newSSOExchangeCode 生成 SSO 一次性兑换码（crypto/rand 32hex）。
func newSSOExchangeCode() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}

// handleSSOExchange 一次性 code 兑换 JWT（★ B3）。参数 r: POST {"code":"..."}。
// code 单次消费（取出即删）；60s TTL 内有效；uid/tid 以服务端暂存为准，不信任客户端。
func (s *Server) handleSSOExchange(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "仅支持 POST"))
		return
	}
	var req struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err == nil && req.Code != "" {
		if val, ok := vcodeGet("sso_xchg:" + req.Code); ok {
			vcodeDel("sso_xchg:" + req.Code)
			var ids struct{ UID, TID int64 }
			// JSON 反序列化用大写字段名映射
			_ = json.Unmarshal(val, &ids)
			if ids.UID > 0 {
				if u, err := s.Store.GetUser(ids.UID, ids.TID); err == nil && u != nil {
					if st := auth.EffectiveUserStatus(u.Status, u.DeactivatedAt); st == store.UserActive || st == store.UserDeactivating {
						tok, err := auth.Sign(u, 24*time.Hour)
						if err == nil {
							writeJSON(w, 200, map[string]interface{}{"success": true, "token": tok, "user": map[string]interface{}{
								"id": u.ID, "username": u.Username, "display_name": u.DisplayName, "role": u.Role, "tenant_id": u.TenantID,
							}})
							return
						}
					}
				}
			}
		}
	}
	s.writeError(w, r, apierrors.New(apierrors.ErrUnauthorized, "兑换码无效或已过期，请重新发起 SSO 登录"))
}

// httpsDetected 判断当前请求是否走 HTTPS（直连 TLS 或反代 X-Forwarded-Proto=https）。
func httpsDetected(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// ssoProviderConfig 取回某 IdP 的原始配置（用于 auto_provision 参数）。
func (s *Server) ssoProviderConfig(name string) *config.SSOProviderConfig {
	for i := range s.Cfg.SSOProviders {
		if strings.EqualFold(s.Cfg.SSOProviders[i].Name, name) {
			return &s.Cfg.SSOProviders[i]
		}
	}
	return nil
}

// provisionSSOUser 自动开通 SSO 用户（邮箱为用户名，随机强密码，角色 member）。
func (s *Server) provisionSSOUser(info *sso.UserInfo, tenantID int64) (*store.User, error) {
	if tenantID <= 0 {
		tenantID = 1
	}
	username := info.Email
	if at := strings.Index(username, "@"); at > 0 {
		username = username[:at]
	}
	// 随机强密码（SSO 用户不会走密码登录，仅占位）
	rb := make([]byte, 24)
	if _, err := rand.Read(rb); err != nil {
		return nil, err
	}
	passHash := auth.PasswordHash(hex.EncodeToString(rb))
	// ★ B3：角色必须落在合法角色集（user/dept_admin/tenant_admin/super_admin）；
	//   旧值 "member" 不在 RoleLevel 定义内，权限解析行为未定义。SSO 自动开通按普通用户。
	u, err := s.Store.CreateUser(tenantID, username, passHash, info.Name, "user", 0, 0)
	if err != nil {
		return nil, err
	}
	if err := s.Store.SetUserEmail(u.ID, tenantID, info.Email); err != nil {
		return nil, err
	}
	return u, nil
}
