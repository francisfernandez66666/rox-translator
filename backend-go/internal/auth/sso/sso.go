// ============ sso.go · 职责说明 ============
// auth/sso 包：企业身份联合登录适配层（阶段六）。
//   - Provider 接口：AuthURL(state) 构造授权跳转地址；Exchange(code) 换取用户信息。
//   - 标准 OIDC：经 issuer 的 /.well-known/openid-configuration 发现端点（懒加载+缓存）。
//   - 飞书 / 钉钉：手动指定授权/令牌/用户信息端点（OAuth2 授权码流程）。
//   - Manager：按 name 索引各 IdP，供 api 层路由 /api/sso/login、/api/sso/callback 使用。
// 安全：state 用 crypto/rand 生成并校验防 CSRF；token 交换走 TLS；不落 IdP 密钥到日志。
// 离线环境无 IdP 无法端到端验证，但 AuthURL 构造与配置解析可单测；Exchange 仅依赖标准 HTTP。
package sso

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"translator/internal/config"
)

// UserInfo IdP 返回的标准用户标识。
type UserInfo struct {
	Sub   string // 主体标识（oid/sub/open_id）
	Email string // 邮箱（飞书可能为空）
	Name  string // 展示名
}

// Provider 单 IdP 适配接口。
type Provider interface {
	Name() string
	AuthURL(state string) string
	Exchange(ctx context.Context, code string) (*UserInfo, error)
}

// NewState 生成 CSRF state（32 字节 hex）。
func NewState() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// Manager 多 IdP 路由。
type Manager struct {
	mu        sync.RWMutex
	providers map[string]Provider
}

// NewManager 由配置构建 Manager（OIDC 发现延迟到首次使用时执行，避免启动期网络阻塞）。
func NewManager(cfgs []config.SSOProviderConfig) *Manager {
	m := &Manager{providers: map[string]Provider{}}
	for _, c := range cfgs {
		var p Provider
		switch strings.ToLower(c.Type) {
		case "oidc":
			p = &oidcProvider{cfg: c}
		case "feishu", "dingtalk":
			p = &oauth2Provider{cfg: c}
		default:
			continue
		}
		m.providers[strings.ToLower(c.Name)] = p
	}
	return m
}

// Enabled 是否有任一 IdP 启用。
func (m *Manager) Enabled() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.providers) > 0
}

// Get 按 name 取 IdP（不存在返回 false）。
func (m *Manager) Get(name string) (Provider, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, ok := m.providers[strings.ToLower(name)]
	return p, ok
}

// List 返回所有 IdP 的展示名（供前端渲染登录按钮）。
func (m *Manager) List() []config.SSOProviderConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]config.SSOProviderConfig, 0, len(m.providers))
	for _, p := range m.providers {
		if op, ok := p.(*oidcProvider); ok {
			out = append(out, op.cfg)
		} else if op, ok := p.(*oauth2Provider); ok {
			out = append(out, op.cfg)
		}
	}
	return out
}

// ---- 标准 OIDC ----

// oidcProvider 标准 OIDC 提供方适配器：持有配置与懒加载的发现文档（once 保证仅拉取一次）。
type oidcProvider struct {
	cfg  config.SSOProviderConfig
	once sync.Once
	disc *oidcDiscovery
	err  error
}

// oidcDiscovery 从 OIDC 发现文档（/.well-known/openid-configuration）提取的关键端点。
type oidcDiscovery struct {
	AuthURL     string `json:"authorization_endpoint"`
	TokenURL    string `json:"token_endpoint"`
	UserinfoURL string `json:"userinfo_endpoint"`
}

// Name 返回提供方展示名。
func (p *oidcProvider) Name() string { return p.cfg.Name }

// discover 懒加载并缓存 OIDC 发现文档（sync.Once 保证仅一次网络请求）。
// 返回发现文档与错误；issuer 为空或拉取失败时会记录到 p.err 并复用其结果。
func (p *oidcProvider) discover() (*oidcDiscovery, error) {
	p.once.Do(func() {
		if p.cfg.Issuer == "" {
			p.err = fmt.Errorf("oidc 提供方 %s 缺少 issuer", p.cfg.Name)
			return
		}
		wellKnown := strings.TrimRight(p.cfg.Issuer, "/") + "/.well-known/openid-configuration"
		req, _ := http.NewRequest(http.MethodGet, wellKnown, nil)
		cli := &http.Client{Timeout: 10 * time.Second}
		resp, err := cli.Do(req)
		if err != nil {
			p.err = err
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			p.err = fmt.Errorf("OIDC 发现文档拉取失败: %d", resp.StatusCode)
			return
		}
		var d oidcDiscovery
		if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
			p.err = err
			return
		}
		p.disc = &d
	})
	return p.disc, p.err
}

// AuthURL 基于 OIDC 发现文档生成授权跳转 URL（含 client_id/redirect_uri/scope/state）。
// 发现失败时返回空串（调用方应提示配置错误）。
func (p *oidcProvider) AuthURL(state string) string {
	d, err := p.discover()
	if err != nil || d == nil {
		// 发现失败仍尽量用 issuer 推断（兜底）
		return ""
	}
	q := url.Values{}
	q.Set("client_id", p.cfg.ClientID)
	q.Set("redirect_uri", p.cfg.RedirectURL)
	q.Set("response_type", "code")
	q.Set("scope", p.scopes())
	q.Set("state", state)
	return d.AuthURL + "?" + q.Encode()
}

// scopes 返回授权范围（scope）；未配置时使用默认 "openid email profile"。
func (p *oidcProvider) scopes() string {
	if p.cfg.Scopes != "" {
		return p.cfg.Scopes
	}
	return "openid email profile"
}

// Exchange 用授权码换取 OIDC token，并优先经 userinfo 端点获取用户信息。
// 返回统一 UserInfo（邮箱/名称/sub）；任一步失败均返回错误。
func (p *oidcProvider) Exchange(ctx context.Context, code string) (*UserInfo, error) {
	d, err := p.discover()
	if err != nil || d == nil {
		return nil, fmt.Errorf("oidc 发现失败: %w", err)
	}
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", p.cfg.RedirectURL)
	form.Set("client_id", p.cfg.ClientID)
	form.Set("client_secret", p.cfg.ClientSecret)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, d.TokenURL, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(url.QueryEscape(p.cfg.ClientID), url.QueryEscape(p.cfg.ClientSecret))
	cli := &http.Client{Timeout: 15 * time.Second}
	resp, err := cli.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("token 端点返回 %d: %s", resp.StatusCode, string(body))
	}
	var tok struct {
		AccessToken string `json:"access_token"`
		IDToken     string `json:"id_token"`
	}
	if err := json.Unmarshal(body, &tok); err != nil {
		return nil, err
	}
	// 用户信息：优先 userinfo 端点
	if d.UserinfoURL != "" && tok.AccessToken != "" {
		ureq, _ := http.NewRequestWithContext(ctx, http.MethodGet, d.UserinfoURL, nil)
		ureq.Header.Set("Authorization", "Bearer "+tok.AccessToken)
		uresp, err := cli.Do(ureq)
		if err == nil && uresp.StatusCode == 200 {
			defer uresp.Body.Close()
			var claims map[string]interface{}
			if json.NewDecoder(uresp.Body).Decode(&claims) == nil {
				return claimsToUser(claims), nil
			}
		}
	}
	return nil, fmt.Errorf("未能获取用户信息")
}

// claimsToUser 将 OIDC userinfo claims 映射为统一 UserInfo；邮箱为空时兼容 upn / preferred_username 兜底。
func claimsToUser(claims map[string]interface{}) *UserInfo {
	u := &UserInfo{}
	if v, ok := claims["sub"].(string); ok {
		u.Sub = v
	}
	if v, ok := claims["email"].(string); ok {
		u.Email = v
	}
	if v, ok := claims["name"].(string); ok {
		u.Name = v
	}
	if u.Email == "" {
		if v, ok := claims["upn"].(string); ok {
			u.Email = v
		} else if v, ok := claims["preferred_username"].(string); ok {
			u.Email = v
		}
	}
	return u
}

// ---- 飞书 / 钉钉 OAuth2 ----

// oauth2Provider 飞书/钉钉或通用 OAuth2 授权码提供方适配器（端点由配置显式指定）。
type oauth2Provider struct {
	cfg config.SSOProviderConfig
}

// Name 返回提供方展示名。
func (p *oauth2Provider) Name() string { return p.cfg.Name }

// AuthURL 生成 OAuth2 授权跳转 URL：按类型分流（飞书/钉钉专用端点，或配置的自定义端点）。
func (p *oauth2Provider) AuthURL(state string) string {
	q := url.Values{}
	q.Set("client_id", p.cfg.ClientID)
	q.Set("redirect_uri", p.cfg.RedirectURL)
	q.Set("response_type", "code")
	q.Set("state", state)
	switch strings.ToLower(p.cfg.Type) {
	case "feishu":
		q.Set("app_id", p.cfg.ClientID)
		return "https://open.feishu.cn/open-apis/authen/v1/authorize?" + q.Encode()
	case "dingtalk":
		q.Set("scope", "openid")
		return "https://login.dingtalk.com/oauth2/auth?" + q.Encode()
	}
	// 手动指定端点
	base := p.cfg.AuthURL
	if base == "" {
		return ""
	}
	if p.cfg.Scopes != "" {
		q.Set("scope", p.cfg.Scopes)
	}
	return base + "?" + q.Encode()
}

// Exchange 按提供方类型分发授权码换 token：飞书/钉钉走各自实现，其余走通用 OAuth2。
func (p *oauth2Provider) Exchange(ctx context.Context, code string) (*UserInfo, error) {
	switch strings.ToLower(p.cfg.Type) {
	case "feishu":
		return p.exchangeFeishu(ctx, code)
	case "dingtalk":
		return p.exchangeDingTalk(ctx, code)
	}
	return p.exchangeGeneric(ctx, code)
}

// exchangeFeishu 飞书 OIDC 授权码换 token + 用户信息（Basic 认证用 app_id/app_secret）。
func (p *oauth2Provider) exchangeFeishu(ctx context.Context, code string) (*UserInfo, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://open.feishu.cn/open-apis/authen/v1/oidc/access_token",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// 飞书用 app_id/app_secret 作为 Basic 认证
	req.SetBasicAuth(p.cfg.ClientID, p.cfg.ClientSecret)
	cli := &http.Client{Timeout: 15 * time.Second}
	resp, err := cli.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("飞书 token 返回 %d: %s", resp.StatusCode, string(body))
	}
	var r struct {
		Data struct {
			AccessToken string `json:"access_token"`
			OpenID     string `json:"open_id"`
			Name       string `json:"name"`
			Email      string `json:"email"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, err
	}
	return &UserInfo{Sub: r.Data.OpenID, Email: r.Data.Email, Name: r.Data.Name}, nil
}

// exchangeDingTalk 钉钉 OAuth2 授权码换 token，再经 contact/users/me 拉取用户信息。
func (p *oauth2Provider) exchangeDingTalk(ctx context.Context, code string) (*UserInfo, error) {
	payload := map[string]interface{}{
		"clientId":     p.cfg.ClientID,
		"clientSecret": p.cfg.ClientSecret,
		"code":         code,
		"grantType":    "authorization_code",
	}
	b, _ := json.Marshal(payload)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.dingtalk.com/v1.0/oauth2/accessToken", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	cli := &http.Client{Timeout: 15 * time.Second}
	resp, err := cli.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("钉钉 token 返回 %d: %s", resp.StatusCode, string(body))
	}
	var t struct {
		AccessToken string `json:"accessToken"`
	}
	if err := json.Unmarshal(body, &t); err != nil {
		return nil, err
	}
	// 用户信息：contact/users/me
	ureq, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.dingtalk.com/v1.0/contact/users/me", nil)
	ureq.Header.Set("Content-Type", "application/json")
	ureq.Header.Set("x-acs-dingtalk-access-token", t.AccessToken)
	uresp, err := cli.Do(ureq)
	if err != nil {
		return nil, err
	}
	defer uresp.Body.Close()
	ub, _ := io.ReadAll(uresp.Body)
	if uresp.StatusCode != 200 {
		return nil, fmt.Errorf("钉钉 userinfo 返回 %d: %s", uresp.StatusCode, string(ub))
	}
	var u struct {
		OpenID   string `json:"openId"`
		Email    string `json:"email"`
		NickName string `json:"nickName"`
	}
	if err := json.Unmarshal(ub, &u); err != nil {
		return nil, err
	}
	return &UserInfo{Sub: u.OpenID, Email: u.Email, Name: u.NickName}, nil
}

// exchangeGeneric 通用 OAuth2 授权码换 token（配置 token_url），再用 access_token 拉取 userinfo。
func (p *oauth2Provider) exchangeGeneric(ctx context.Context, code string) (*UserInfo, error) {
	if p.cfg.TokenURL == "" {
		return nil, fmt.Errorf("提供方 %s 未配置 token_url", p.cfg.Name)
	}
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", p.cfg.RedirectURL)
	form.Set("client_id", p.cfg.ClientID)
	form.Set("client_secret", p.cfg.ClientSecret)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, p.cfg.TokenURL, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	cli := &http.Client{Timeout: 15 * time.Second}
	resp, err := cli.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("token 返回 %d: %s", resp.StatusCode, string(body))
	}
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	json.Unmarshal(body, &tok)
	if p.cfg.UserinfoURL == "" || tok.AccessToken == "" {
		return claimsToUser(decodeClaims(body)), nil
	}
	ureq, _ := http.NewRequestWithContext(ctx, http.MethodGet, p.cfg.UserinfoURL, nil)
	ureq.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	uresp, err := cli.Do(ureq)
	if err != nil {
		return nil, err
	}
	defer uresp.Body.Close()
	ub, _ := io.ReadAll(uresp.Body)
	if uresp.StatusCode != 200 {
		return nil, fmt.Errorf("userinfo 返回 %d: %s", uresp.StatusCode, string(ub))
	}
	return claimsToUser(decodeClaims(ub)), nil
}

// decodeClaims 将 token/userinfo 响应体尽力解析为 claims map（解析失败返回 nil，调用方按兜底处理）。
func decodeClaims(b []byte) map[string]interface{} {
	var m map[string]interface{}
	_ = json.Unmarshal(b, &m)
	return m
}
