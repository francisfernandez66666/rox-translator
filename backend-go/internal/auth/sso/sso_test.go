// Package sso 的企业身份联合登录适配层单元测试。
// 覆盖 CSRF state 唯一性，以及飞书/钉钉/通用 OAuth2 提供方的授权地址构造。
package sso

import (
	"strings"
	"testing"

	"translator/internal/config"
)

// TestNewStateUnique 校验 NewState 生成的 state 长度固定且互不重复（防 CSRF 重放）。
func TestNewStateUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		st := NewState()
		if len(st) != 64 {
			t.Fatalf("state 长度不符: %d", len(st))
		}
		if seen[st] {
			t.Fatal("state 重复")
		}
		seen[st] = true
	}
}

// TestFeishuAuthURL 校验飞书提供方可构造正确的授权跳转地址并携带必要参数。
func TestFeishuAuthURL(t *testing.T) {
	p := &oauth2Provider{cfg: config.SSOProviderConfig{Name: "feishu", Type: "feishu", ClientID: "cid", RedirectURL: "https://app.example.com/cb"}}
	u := p.AuthURL("xyz")
	if !strings.Contains(u, "open.feishu.cn/open-apis/authen/v1/authorize") {
		t.Fatalf("飞书授权地址错误: %s", u)
	}
	if !strings.Contains(u, "state=xyz") || !strings.Contains(u, "redirect_uri=") {
		t.Fatalf("缺少必要参数: %s", u)
	}
}

// TestDingTalkAuthURL 校验钉钉提供方可构造正确的授权跳转地址并携带 state。
func TestDingTalkAuthURL(t *testing.T) {
	p := &oauth2Provider{cfg: config.SSOProviderConfig{Name: "dingtalk", Type: "dingtalk", ClientID: "cid", RedirectURL: "https://app.example.com/cb"}}
	u := p.AuthURL("abc")
	if !strings.Contains(u, "login.dingtalk.com/oauth2/auth") {
		t.Fatalf("钉钉授权地址错误: %s", u)
	}
	if !strings.Contains(u, "state=abc") {
		t.Fatalf("缺少 state: %s", u)
	}
}

// TestGenericAuthURL 校验手动指定端点的通用 OAuth2 提供方可构造正确授权地址。
func TestGenericAuthURL(t *testing.T) {
	p := &oauth2Provider{cfg: config.SSOProviderConfig{Name: "myidp", Type: "oidc", ClientID: "cid",
		AuthURL: "https://idp.example.com/auth", RedirectURL: "https://app.example.com/cb"}}
	u := p.AuthURL("s1")
	if !strings.Contains(u, "idp.example.com/auth") || !strings.Contains(u, "client_id=") {
		t.Fatalf("通用授权地址错误: %s", u)
	}
}
