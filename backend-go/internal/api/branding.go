// ============================================================================
// api/branding.go — ★ B11（2026-09-12）运营身份硬编码收敛：
// 品牌域名与运营通知邮箱全部来自部署配置（system_config / 环境变量），代码零明文默认。
//   - BRAND_DOMAIN_SUFFIX：品牌基础域（如 example.com），子域解析/主站兜底用；
//   - OPS_NOTIFY_EMAIL：企业注册等运营抄送邮箱（空=不抄送）；
//   - system_config base_domain / primary_host 优先级更高（后台可视化配置）。
//
// 未配置时的降级语义：品牌子域解析关闭（返回空前缀/主站=当前请求 Host），
// 运营抄送关闭——均不泄漏任何运营方身份。
// ============================================================================
package api

import (
	"os"
	"strings"
)

// brandSuffix 品牌基础域环境变量兜底（system_config 优先级更高的逻辑在各调用点）。
func brandSuffix() string { return strings.TrimSpace(os.Getenv("BRAND_DOMAIN_SUFFIX")) }

// opsNotifyEmail 运营抄送邮箱（env OPS_NOTIFY_EMAIL；空=不抄送）。
func opsNotifyEmail() string { return strings.TrimSpace(os.Getenv("OPS_NOTIFY_EMAIL")) }

// primaryHostFor 主站主机名：system_config(primary_host) → 主站前缀(env BRAND_PRIMARY_PREFIX，默认 www)按品牌后缀拼装 → 品牌后缀本身。
// 参数 get: system_config 读取函数（解耦 Store 判空）。
func primaryHostFor(get func(string) (string, error)) string {
	if get != nil {
		if v, err := get("primary_host"); err == nil && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	base := brandingBaseDomainNoStore()
	if base == "" {
		return ""
	}
	if p := primarySubdomainPrefix(); p != "" {
		return p + "." + base
	}
	return base
}

// primarySubdomainPrefix 主站子域前缀（env BRAND_PRIMARY_PREFIX 可配；默认 www——
// B11 收敛不允许代码内出现运营方品牌明文，生产以 system_config.primary_host 显式覆盖）。
func primarySubdomainPrefix() string {
	if v := strings.TrimSpace(os.Getenv("BRAND_PRIMARY_PREFIX")); v != "" {
		return v
	}
	return "www"
}

// brandingBaseDomainNoStore 仅按环境变量取品牌基础域（无 Store 场景）。
func brandingBaseDomainNoStore() string { return brandSuffix() }

// primaryHost Server 便捷方法：主站主机名（system_config primary_host → env 拼装；空=未配置）。
func (s *Server) primaryHost() string {
	var get func(string) (string, error)
	if s != nil && s.Store != nil {
		get = s.Store.GetConfig
	}
	return primaryHostFor(get)
}

// brandPrimaryHost 无请求上下文时取主站（仅 env 层，无 Store）。
func brandPrimaryHost() string { return primaryHostFor(nil) }
