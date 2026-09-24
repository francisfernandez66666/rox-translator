// ============ 本文件职责中文说明 ============
// 请求语种识别（★ 2026-09-24 〇-S #12 后端语言识别）：
// 从 HTTP 请求判定当前用户界面语言并注入 context，供 Msg() 决定提示语是否翻英。
// 优先级：X-App-Lang 头（前端 axios/SSE 统一附带）> Accept-Language 头（浏览器直出页）> 默认 zh。
// 归一口径：zh 系（含简体/繁体 zh-Hant/zh-TW/zh-HK 等）一律 → "zh"——
//
//	后端提示语只维护简中/英两套词典，繁体用户拿简体属本批明示的取舍；
//	其余任何语种（ja/ko/fr/…）→ "en"，与前端 lang→en 回退链同口径。
//
// 无头的请求（curl/UAT 脚本、服务端回调）→ "zh"，保证存量断言零改动。
// =============================================
package i18n

import (
	"context"
	"net/http"
	"strings"
)

// HeaderLang 是前端声明界面语言的请求头名（需在 CORS Allow-Headers 中放行）。
const HeaderLang = "X-App-Lang"

// ctxKeyLang 是语种在 context 中的私有键（仿 internal/errors/trace.go 口径，避免跨包碰撞）。
type ctxKeyLang struct{}

// WithLang 将语种写入 context（"zh" / "en"）。
func WithLang(ctx context.Context, lang string) context.Context {
	return context.WithValue(ctx, ctxKeyLang{}, lang)
}

// LangFrom 读取 context 中的语种；缺省 "zh"（未走 WithLang 的内部调用一律按中文处理）。
func LangFrom(ctx context.Context) string {
	if ctx == nil {
		return "zh"
	}
	if v, ok := ctx.Value(ctxKeyLang{}).(string); ok && v != "" {
		return v
	}
	return "zh"
}

// Detect 由两个请求头判定语种（X-App-Lang 优先）。无头 → zh；zh 系 → zh；其余 → en。
func Detect(xAppLang, acceptLanguage string) string {
	if s := normalize(xAppLang); s != "" {
		return s
	}
	// Accept-Language 形如 "ja,en;q=0.9,zh;q=0.8"：取第一个不带 q=0 的项即可，
	// 后端只有中英两套词典，权重细分没有意义，不做完整解析。
	for _, part := range strings.Split(acceptLanguage, ",") {
		tag := strings.TrimSpace(strings.SplitN(part, ";", 2)[0])
		if s := normalize(tag); s != "" {
			return s
		}
	}
	return "zh"
}

// FromRequest 是 Detect 的 http.Request 便捷入口。
func FromRequest(r *http.Request) string {
	return Detect(r.Header.Get(HeaderLang), r.Header.Get("Accept-Language"))
}

// normalize 把语言标签归一到 "zh"/"en"；无法识别（含空串）返回 ""，由调用方继续降级。
func normalize(tag string) string {
	t := strings.ToLower(strings.TrimSpace(tag))
	if t == "" {
		return ""
	}
	// zh / zh-CN / zh_Hans / zh-Hant / zh-TW …：统一按简体处理（繁体词典本批不做）
	if t == "zh" || strings.HasPrefix(t, "zh-") || strings.HasPrefix(t, "zh_") {
		return "zh"
	}
	if t == "en" || strings.HasPrefix(t, "en-") || strings.HasPrefix(t, "en_") {
		return "en"
	}
	// 通配 * 视为「跟随浏览器」但无信息量，按未知处理继续往后降级
	if t == "*" {
		return ""
	}
	// 其余明确语种（ja/ko/fr/de/es/ru/ar/th/vi/pt/id…）→ en
	return "en"
}
