// ============ site_flags.go · 职责说明 ============
// 〇-Z「站点门面开关」的后端半边：把「官网首页对未登录访客是否开放」这一决策
// 以 window.__SITE_FLAGS__ 的形式随入口 HTML 直出，供前端 App.tsx 在访客分流处判定。
//
// 为什么做在后端而不是前端写死域名：
//
//	演示站（rox-test）与主站跑同一份 dist，前端若按 hostname 判定，等于把「哪个站是体验机」
//	这件事写进构建产物 —— 换域名/加体验机都要改代码重构建，且本地闸门完全看不见这条分支。
//	做成部署级开关后：二进制与 dist 两站共用，差异只落在演示单元的一个环境变量上。
//
// 配置优先序（AGENTS §一·3：环境变量 > 数据库配置 > 代码默认）：
//
//		env LANDING_DISABLED > system_config.ops_policy 的 front.landing_enabled > 默认展示
//
//	  · env 取 "1"/"true"/"yes"/"on"（忽略大小写与空白）＝强制关闭主页；
//	    取 "0"/"false"/"no"/"off" ＝强制保留主页（用来覆盖库里误关掉的策略，运维急救方向）；
//	    其余值（含未设置）视为未表态，落到库配置。
//	  · 库配置只读**平台层** ops_policy，不做租户级覆盖：门面是「这个部署要不要对外营业」
//	    的平台口径，不是租户白标配置（详见 internal/ops/policy.go 的 FrontPatch 射程注释）。
//
// ★ 注入纪律（本文件最重要的一条）：**只在关闭主页时注入**脚本。
//
//	主页开放时首页 HTML 必须与「本特性不存在」逐字节相同 —— 现网主站首页 2,591 B 是
//	《部署指南》§十 与 deploy/smoke_brand_homepage.sh 钉死的判据，若此处无脑注入一个
//	恒为 true 的标记，那个字节数判据会被无声顶翻，排查的人会先去怀疑「品牌注入」那半边链。
//	判据锁在 site_flags_test.go（「开＝与未注入完全相等」等值锁 + 负向：不出现 "landing":true）。
//
// =============================================
package api

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
)

// siteFlagsScriptTagID 注入脚本的 id，测试与冒烟脚本按它定位（与 __branding__ 同族命名）。
const siteFlagsScriptTagID = "__site_flags__"

// envLandingDisabled 部署级门面开关环境变量名。
const envLandingDisabled = "LANDING_DISABLED"

// siteFlagsEnvLanding 解析 LANDING_DISABLED 环境变量对主页开关的表态。
// 返回值语义：(是否表态, 表态后的「主页是否开放」)。未表态时交由库配置与默认档决定。
// 注意这是**反向**语义的环境变量（DISABLED＝关闭主页），所以 "1" 对应 landing=false；
// 取名时故意与运维直觉一致（"把主页关掉" 就写 LANDING_DISABLED=1），不再多加一层否定。
func siteFlagsEnvLanding() (declared bool, landingEnabled bool) {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(envLandingDisabled)))
	switch v {
	case "":
		return false, true
	case "1", "true", "yes", "on":
		return true, false
	case "0", "false", "no", "off":
		return true, true
	default:
		// 非法值不表态：与 brandGraceDays 的「非法值回落」同口径，避免手滑打错一个字母就把主页关没。
		return false, true
	}
}

// siteLandingEnabled 当前部署是否对未登录访客开放官网主页。
// 优先序见文件头。只读平台层策略，一次 GetConfig，无租户入参 —— 禁止在此顺手接 tid。
func (s *Server) siteLandingEnabled() bool {
	if declared, v := siteFlagsEnvLanding(); declared {
		return v
	}
	if s.Store == nil {
		return true
	}
	plat := s.opsPlatformPolicy()
	if plat.Front.LandingEnabled != nil {
		return *plat.Front.LandingEnabled
	}
	return true
}

// siteFlagsPayload 组装随首页直出的站点门面标记；主页开放时返回 nil（＝不注入）。
// 之所以把「返不返回」的判断放这里而不是调用点：调用点（serveIndexHTML）只有一条注入链，
// 让「开＝零痕迹」这件事在本文件内自洽，测试也只需打这一个函数。
func (s *Server) siteFlagsPayload(r *http.Request) map[string]interface{} {
	_ = r // 门面是部署级口径，与访问域名/租户无关，保留入参只为与 brandingPayload 同形、便于日后扩展
	if s.siteLandingEnabled() {
		return nil
	}
	return map[string]interface{}{"landing": false}
}

// injectSiteFlagsScript 把站点门面标记注入 HTML 的 </head> 之前；payload 为空时原样返回。
// 与 injectBrandingScript 同款写法（无标志位就不落任何字节），差别仅在「为空」是常态。
func injectSiteFlagsScript(payload map[string]interface{}, html string) string {
	if len(payload) == 0 {
		return html
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return html
	}
	script := `<script id="` + siteFlagsScriptTagID + `">window.__SITE_FLAGS__=` + string(body) + `;</script>`
	if i := strings.Index(strings.ToLower(html), "</head>"); i >= 0 {
		return html[:i] + script + html[i:]
	}
	return script + html
}
