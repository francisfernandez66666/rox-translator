// ============ sso.go · 职责说明 ============
// 阶段六 SSO / OIDC 的 HTTP 接口：
//   - GET  /api/sso/login?provider=   生成 state 写入 HttpOnly cookie，重定向至 IdP 授权页
//   - GET  /api/sso/callback?provider=&code=&state=  校验 state，换取用户信息，
//        按邮箱匹配平台账号（飞书无邮箱则失败），命中则签发 JWT 并带 ?token= 重定向前端；
//        配置 auto_provision 时自动开通账号。
// 与既有登录体系一致：最终都签发同一套 JWT（前端 localStorage 托管），无独立会话态。
package api

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"net/url"
	"strings"
	"time"

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
	http.Redirect(w, r, front+"/?token="+url.QueryEscape(tok), http.StatusFound)
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
	u, err := s.Store.CreateUser(tenantID, username, passHash, info.Name, "member", 0, 0)
	if err != nil {
		return nil, err
	}
	if err := s.Store.SetUserEmail(u.ID, tenantID, info.Email); err != nil {
		return nil, err
	}
	return u, nil
}
