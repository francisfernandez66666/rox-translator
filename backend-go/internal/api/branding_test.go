// ============ branding_test.go · 职责说明 ============
// 品牌域名解析回归测试（★ 2026-09-14 演示站品牌丢失事故复盘）：
// B11 收敛移除「lexicorn.cn」硬编码兜底后，子域→租户品牌解析完全由
// system_config.base_domain / env BRAND_DOMAIN_SUFFIX 驱动；本测试锁定
// 纯函数层的解析与降级语义，防止部署配置缺项时品牌静默丢失无从发现。
// =============================================
package api

import (
	"errors"
	"testing"
)

// TestPrimaryHostFor 主站主机名解析优先级：system_config(primary_host) > env 拼装 > 空。
func TestPrimaryHostFor(t *testing.T) {
	t.Setenv("BRAND_DOMAIN_SUFFIX", "lexicorn.cn")
	t.Setenv("BRAND_PRIMARY_PREFIX", "")
	// ① system_config 显式配置优先
	got := primaryHostFor(func(k string) (string, error) {
		if k == "primary_host" {
			return "langcross.lexicorn.cn", nil
		}
		return "", errors.New("missing")
	})
	if got != "langcross.lexicorn.cn" {
		t.Errorf("primary_host 配置应优先，got %q", got)
	}
	// ② 无配置时按 env 兜底拼装（前缀默认 www，杜绝代码内品牌明文）
	got = primaryHostFor(func(k string) (string, error) { return "", errors.New("missing") })
	if got != "www.lexicorn.cn" {
		t.Errorf("env 拼装主站应为 www.lexicorn.cn，got %q", got)
	}
	// ③ 基础域未配置 → 空（B11 降级语义：不泄漏任何运营方身份）
	t.Setenv("BRAND_DOMAIN_SUFFIX", "")
	if got = primaryHostFor(nil); got != "" {
		t.Errorf("未配置基础域时应返回空，got %q", got)
	}
}

// TestTenantPrefixFromHost 访问域名 → 租户子域前缀解析；未配置基础域时整层关闭（返回空）。
func TestTenantPrefixFromHost(t *testing.T) {
	s := &Server{} // Store=nil → brandingBaseDomain 走 env 兜底
	t.Setenv("BRAND_DOMAIN_SUFFIX", "lexicorn.cn")
	cases := map[string]string{
		"rox-test.lexicorn.cn":  "rox-test",
		"rox.lexicorn.cn":       "rox",
		"langcross.lexicorn.cn": "langcross", // 主站前缀由调用方与 primary_host 比对后跳过
		"lexicorn.cn":           "",          // apex 根域无租户前缀
		"evil.example.org":      "",          // 非品牌域一律拒绝
		"127.0.0.1:8787":        "",          // IP/本机访问无前缀
	}
	for host, want := range cases {
		if got := tenantPrefixFromHost(s, host); got != want {
			t.Errorf("tenantPrefixFromHost(%q)=%q, want %q", host, got, want)
		}
	}
	// B11 降级语义：未配置基础域（system_config 与 env 双空）时子域解析关闭
	t.Setenv("BRAND_DOMAIN_SUFFIX", "")
	if got := tenantPrefixFromHost(s, "rox-test.lexicorn.cn"); got != "" {
		t.Errorf("未配置 base_domain 时子域解析应关闭（返回空），got %q", got)
	}
}
