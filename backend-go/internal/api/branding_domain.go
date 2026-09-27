// ============ 本文件职责中文说明 ============
// 品牌域前缀的入库口径（★ F-76，2026-09-27 〇-X 用户批准「校验只允许小写字母」，
// 字符集按同日确认的「小写字母＋数字、必须字母开头」执行）。
//
// 修的是什么：历史写侧把 Domain 字段**原样入库**，读侧却是 `WHERE domain=?` 的裸等值匹配
// （tenant.GetByDomain），两边不认同一个字符串——租户在后台填 `ROX`、`rox.lexicorn.cn`
// 或 `rox/`，保存都返回成功，品牌却永远不生效（静默失效，客户只能靠截图找客服）。
// 同时没有任何占用校验：两个租户填同一前缀没人拦，谁都能把别人的前缀（甚至企业编码）
// 认领成自己的品牌域。
//
// 口径三件事：
//
//	① **能安全归一的先归一**：剥空格（含全角）、剥协议、剥路径、剥端口、剥基础域后缀、
//	   大写转小写——避免把"客户手滑"变成一次报错；
//	② **归一后仍不合法就明确拒绝**（不再静默存下永不生效的值）：必须匹配 ^[a-z][a-z0-9]*$，
//	   即字母开头、仅小写字母与数字，不允许连字符/点/下划线/大写/整域名；
//	③ **占用与保留校验**：前缀不得撞别的租户（domain 或 code 同值都不行，因为企业编码
//	   本身就是一个能打开的子域，见 resolveDedicatedTenant 的编码兜底），也不得撞保留前缀
//	   （主站、演示站与一批高危名）。
//
// 存量不清洗：库里已有的历史值（如演示站的 rox-test）读侧照旧生效，只是再保存时会被要求
// 改成合规值——避免一次改动把在跑的站点打停。
package api

import (
	"net/url"
	"regexp"
	"strings"

	apierrors "translator/internal/errors"
) // brandDomainPattern 品牌域前缀的字面规则：字母开头，后接小写字母或数字（用户 2026-09-27 定档）。
// 为什么连字符也禁：前缀要拼成 `<前缀>.<基础域>` 才能访问，允许连字符就会让 rox-test 这类
// 「与站点块同形」的名字被普通租户认领，而带点/斜杠的值更是直接把路由表写坏。
var brandDomainPattern = regexp.MustCompile(`^[a-z][a-z0-9]*$`)

// brandDomainReserved 保留前缀：这些名字永远不许被认领成品牌域。
// 除硬编码的高危名外，主站前缀与演示站前缀在 normalizeBrandDomainPrefix 里另判
// （它们随配置变化，写死会漂移）。
var brandDomainReserved = map[string]string{
	"www":       "站点通用入口",
	"api":       "接口域",
	"apiv1":     "接口域",
	"app":       "应用域",
	"admin":     "管理入口",
	"mail":      "邮件域",
	"smtp":      "邮件域",
	"static":    "静态资源域",
	"cdn":       "静态资源域",
	"assets":    "静态资源域",
	"docs":      "文档站",
	"help":      "帮助中心",
	"status":    "健康检查",
	"health":    "健康检查",
	"demo":      "演示用途",
	"test":      "测试用途",
	"staging":   "预发环境",
	"new":       "保留名",
	"dev":       "开发环境",
	"localhost": "本机域",
}

// normalizeBrandDomainPrefix 把客户填写的品牌域归一成「子域前缀」并给出可展示的中文错误。
// 参数 raw=后台提交的原值，baseDomain=品牌基础域（如 lexicorn.cn，可为空表示未启用子域解析），
// primaryHost=主站完整主机名（如 langcross.lexicorn.cn）。
// 返回 (前缀, 错误信息)：错误信息为空表示通过；两者都空串表示「清空绑定」（允许，用于解绑）。
//
// 归一顺序是有意的：先剥结构（协议/路径/端口/基础域后缀），再做字符集校验，
// 最后才查占用——否则用户填 `https://rox.lexicorn.cn/login` 会因为带大写与斜杠被拒，
// 而这本来是可以安全理解的输入。
func normalizeBrandDomainPrefix(raw, baseDomain, primaryHost string) (string, string) {
	v := strings.TrimSpace(strings.ReplaceAll(raw, "　", "")) // 全角空格一并剥掉
	if v == "" {
		return "", "" // 清空绑定：允许（后台"取消品牌域"就是这一路）
	}
	// 带协议或有路径的整 URL：取 host 段
	if strings.Contains(v, "://") {
		if u, err := url.Parse(v); err == nil && u.Host != "" {
			v = u.Host
		} else {
			return "", "品牌域格式无法识别，请填写子域前缀（例如 rox）"
		}
	}
	v = strings.TrimSuffix(v, "/")
	if i := strings.Index(v, "/"); i >= 0 {
		v = v[:i]
	}
	if i := strings.Index(v, ":"); i >= 0 {
		v = v[:i] // 剥端口
	}
	v = strings.ToLower(v)

	base := strings.ToLower(strings.TrimSpace(baseDomain))
	if base != "" {
		switch {
		case v == base:
			return "", "品牌域不能是主站域名本身，请填写子域前缀（例如 rox）"
		case strings.HasSuffix(v, "."+base):
			v = strings.TrimSuffix(v, "."+base) // 填了完整子域 → 取前缀
		default:
			// 填了一个不含基础域的整域名：多半是想绑自定义域名（本功能只发子域前缀）
			if strings.Contains(v, ".") {
				return "", "暂不支持自定义完整域名，请填写 " + base + " 下的子域前缀（例如 rox）"
			}
		}
	}
	if !brandDomainPattern.MatchString(v) {
		return "", "品牌域只能用小写字母开头、由小写字母与数字组成（不许大写、连字符、点或斜杠），例如 rox"
	}
	if strings.EqualFold(v, strings.TrimSpace(primaryHost)) {
		return "", "该名称与主站地址冲突"
	}
	if reason, hit := brandDomainReserved[v]; hit {
		return "", "「" + v + "」是平台保留名称（" + reason + "），不能绑定为品牌域"
	}
	if primaryHost != "" && strings.EqualFold(primaryHostPrefix(primaryHost, base), v) {
		return "", "该名称与主站子域冲突"
	}
	return v, ""
}

// primaryHostPrefix 从主站完整主机名取子域前缀（langcross.lexicorn.cn + lexicorn.cn → langcross）。
func primaryHostPrefix(primaryHost, baseDomain string) string {
	p := strings.ToLower(strings.TrimSpace(primaryHost))
	base := strings.ToLower(strings.TrimSpace(baseDomain))
	if base == "" {
		return ""
	}
	if strings.HasSuffix(p, "."+base) {
		return strings.TrimSuffix(p, "."+base)
	}
	return ""
}

// normalizeDomainOnSave 保存链路上的品牌域裁决：归一 → 字符集/保留名校验 → 占用校验。
// 参数 raw=后台提交的 Domain 原值，tid=目标租户 ID（0＝平台主站，不做占用校验）。
// 返回 (可入库的前缀, 错误)：错误为 nil 即通过。两类错误状态码不同，故直接回结构化错误——
// 写坏格式是 400（你填错了），撞名是 409（你填的没错，但名额已被占），
// 前端与 SDK 只有按 code 才能区分「改一下再提交」与「换个名字」。
//
// ★ 存量原值豁免：提交的值与库里现值逐字相同（去掉首尾空格后）即原样放行、不做清洗。
// 后台品牌设置页是「整表回提」的（改一张 Logo 也会把 domain 原值带回来），
// 若不放行，历史填过 rox-test 这类不合新规则值的租户会连"换个 Logo"都保存不了——
// 那是把一次性迁移的代价摊到客户每次操作上。豁免只放行"没动过"，一改就走新规则。
func (s *Server) normalizeDomainOnSave(raw string, tid int64) (string, *apierrors.APIError) {
	trimmed := strings.TrimSpace(strings.ReplaceAll(raw, "　", ""))
	if tid > 0 && s.Ten != nil {
		if cur, err := s.Ten.GetByID(tid); err == nil && cur != nil &&
			strings.TrimSpace(cur.Domain) != "" && trimmed == strings.TrimSpace(cur.Domain) {
			return cur.Domain, nil
		}
	}
	prefix, msg := normalizeBrandDomainPrefix(raw, brandingBaseDomain(s), s.primaryHost())
	if msg != "" {
		return "", apierrors.New(apierrors.ErrValidation, msg)
	}
	// 占用校验：品牌域（domain 列）与企业编码（code 列）都不能撞——后者是一个天然可访问的子域
	if prefix != "" && tid > 0 && s.Ten != nil {
		if other, e := s.Ten.GetByDomain(prefix); e == nil && other != nil && other.ID != tid {
			return "", apierrors.New(apierrors.ErrConflict, "该品牌域已被企业「"+other.Name+"」使用，请换一个名称")
		}
		if other, e := s.Ten.GetByCode(prefix); e == nil && other != nil && other.ID != tid {
			return "", apierrors.New(apierrors.ErrConflict, "该名称与某企业的编码相同（编码本身即为可访问子域），请换一个名称")
		}
	}
	return prefix, nil
}
