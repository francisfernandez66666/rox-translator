// ============ ui_lang.go · 职责说明 ============
// 挂件请求里那个「访客界面语言」代码的收口处（★ 082x，2026-09-29，
// 用户指令「不能根据用户的前台语言和使用语言来回复，一律用中文」）。
//
// 为什么要在 API 层归一而不是直接透传：这个字段来自浏览器本地（localStorage 的 app_lang），
// 挂件是老缓存包也可能送 'zh-Hant'、'EN'、带空格的串，甚至送一个 200 字的串。
// 它下游有两个用途，都得吃规范值：
//  1. engine.replyLangBlock 按它决定「默认用哪种语言作答」；
//  2. engine.visitorWantsChinese 按它决定「中文话术/流程能不能直出」。
//
// 判错的代价不对称：把 'EN' 当成未知 → 英文访客继续吃中文话术（就是这次要修的 bug）；
// 把乱码当成某种语言 → 只是少一次话术加速。所以这里**只放行形状合法的短码**，其余归空。
//
// 归空（""）不等于「未知即拒绝服务」：下游把空值按中文档处理
// （082x 之前的挂件根本不带这个字段，判成非中文会让全站话术在升级瞬间集体失效），
// 同时 replyLangBlock 会明确告诉模型「没拿到界面语言，按访客输入的语言作答」——
// 所以老客户端的行为不劣于修复前。
// =============================================
package api

import (
	"regexp"
	"strings"
)

// uiLangRe 界面语言码形状：2-3 位字母，可选「-/_ + 2-4 位字母或数字」的地区/书写后缀。
// 与前端 src/i18n/index.ts 的 Lang 联合类型（zh/en/…/zh_hant）对齐，
// 同时容下 BCP-47 写法（zh-Hant、pt-BR），归一时统一成下划线小写。
var uiLangRe = regexp.MustCompile(`^[a-z]{2,3}([_-][a-z0-9]{2,4})?$`)

// normalizeUILang 归一访客界面语言码；不合法/超长/形状不对的一律归 ""（按中文档处理，见文件头）。
func normalizeUILang(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" {
		return ""
	}
	if !uiLangRe.MatchString(s) {
		return ""
	}
	return strings.ReplaceAll(s, "-", "_")
}
