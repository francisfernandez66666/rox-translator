// ============ branding_scope.go · 职责说明 ============
// F-79（2026-09-28 登记，同日用户拍板「把修法也加上，统一发布」）：
// 品牌解析里「显式 `?tenant_id=`」这条支路过去**不做任何身份判定**——`brandingPayload` 直接拿
// 参数里的 id 去 `Ten.GetByID`，于是任何人（包括未登录访客）都能在主站或 A 租户的品牌域上打
// `?tenant_id=B`，把 B 的品牌资产（品牌名 / Logo / 首页背景图 / 登录页形态）整份拉走。
// 而「客户品牌只在他自己的品牌域生效」正是我们对外卖的白标承诺，此前这条承诺**只由前端保证**
// （`frontend-react/src/branding.tsx` 只在超管后台切租户时才带这个参数），服务端没有兜底＝
// 客户可以合理质疑，第三方也能按 id 枚举已付费客户的品牌资产（背景图与名称本身就是商业信息）。
//
// 本文件只提供一条判据 `brandingExplicitIDAllowed`：「当前访问者能不能按 id 指名要看哪个租户的品牌」。
// 成立条件两条：
//
//	① 平台超管——跨租户配置与预览是这条参数唯一的设计用途（后台品牌设置页 `BrandP.tsx` 走的就是它）；
//	② 该租户自己的成员——他本来在自己品牌域上就看得到自家品牌，"指名"不构成新增暴露。
//
// ★ 为什么是「忽略参数」而不是「返回 403」：这是**未登录也可达的公开接口**，SPA 首屏注入走的
//
//	是同一个咽喉点，历史链接、收藏夹、别人转发的地址里都可能挂着 `?tenant_id=`。
//	403 会把本来完全正常的公开首屏打成错误页（F-64 那批"对外契约改动引发现网 500"的同形教训）；
//	忽略参数则只是让它回落到「按访问域名解析」这条本来就正确的路径——降级不等于放行。
//
// ★ 判据只长在咽喉点上：`/api/tenant/branding`（`handleTenantBrandingGet`）与 `spa.go` 的
//
//	`serveIndexHTML → injectBrandingScript` 两条面都调 `brandingPayload`，在这里收口即两条面同时生效；
//	分开改必然长出「接口堵了、首屏注入还漏」的第二形态——F-79 的线上复现恰好就是注入面。
//
// ★ 与 F-75 展示闸互不替代：F-75 管「这个租户有没有资格被展示」，本文件管「这个访问者有没有资格
//
//	点名看这个租户」。二者叠加后，同租户的普通成员用显式参数仍然拿不到已到期租户的视觉字段
//	（`brandingViewerExempt` 只豁免超管与该租户管理员），所以放宽第②条不会把付费闸捅开一个口子。
//
// ============================================================
package api

import (
	"net/http"

	"translator/internal/auth"
)

// brandingExplicitIDAllowed 判定 URL 上显式带的 `?tenant_id=` 能否生效（F-79）。
// want = 参数指名要看的租户 id；返回 false 时调用方必须**忽略该参数**、回落按访问域名解析。
// 三处防御性拒绝各自对应一条真实形态：nil 请求（单测/内部复用）、want≤0（脏参数与"平台根"语义）、
// 未登录或令牌失效（匿名跨域拉取——本缺陷的主案发现场）。
func (s *Server) brandingExplicitIDAllowed(r *http.Request, want int64) bool {
	if s == nil || r == nil || want <= 0 {
		return false
	}
	u := s.authUser(r)
	if u == nil {
		return false // 匿名访客：品牌只由访问域名决定，参数一律不作数
	}
	if auth.IsSuperAdmin(u) {
		return true // 超管跨租户配置/预览是这条参数的设计用途
	}
	return u.TenantID == want // 只准"点名自己家"，不许点名别家
}
