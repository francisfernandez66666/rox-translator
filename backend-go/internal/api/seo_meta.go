// ============ 本文件职责中文说明 ============
// DEF-06（2026-10-24 云端 UAT）：服务端 SEO 注入。
//
// 现网缺陷：所有路由的 document.title 都是同一句品牌模板，canonical/og:url 恒指首页，
// /pricing、/compare 等内页无法被独立收录。本文件把 route_meta.json（12 语种单一事实源）
// 在 serveIndexHTML 出口注入 <title>/description/canonical/og/robots/hreflang：
//   - 语言：?lang= > X-App-Lang > Accept-Language > zh（12 码；不折叠成 zh/en）；
//   - canonical = 当前语言版本的稳定地址（path?lang=xx）；hreflang 12 条 + x-default 无参入口；
//   - /login、/register 与工作台/后台 noindex；/、/pricing、/compare 可收录；
//   - 只处理前端已知路由；/g/*、/blindtest/、未知 404、后端直出页一概不碰。
//
// ============================================
package api

import (
	"context"
	_ "embed"
	"encoding/json"
	"html"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"translator/internal/observability"
)

//go:embed seo_meta.json
var seoMetaJSON []byte

// seoRouteMeta 单条路由的 12 语种 title/description 与 robots。
type seoRouteMeta struct {
	Robots      string            `json:"robots"`
	Title       map[string]string `json:"title"`
	Description map[string]string `json:"description"`
}

// seoMetaDoc seo_meta.json 的内存结构（与前端 route_meta.json 同源）。
type seoMetaDoc struct {
	Version      int                     `json:"version"`
	Languages    []string                `json:"languages"`
	XDefaultLang string                  `json:"x_default_lang"`
	Routes       map[string]seoRouteMeta `json:"routes"`
	App          struct {
		Robots      string `json:"robots"`
		TitleSuffix string `json:"title_suffix"`
	} `json:"app"`
}

// seoMetaDocOnce/seoMetaDocData/seoMetaOK JSON 只解析一次，失败则注入停用（不 panic）。
var (
	seoMetaDocOnce sync.Once
	seoMetaDocData seoMetaDoc
	seoMetaOK      bool
)

// loadSEOMeta 解析嵌入的 seo_meta.json（sync.Once；解析失败只停用注入，不影响首页）。
func loadSEOMeta() {
	seoMetaDocOnce.Do(func() {
		if err := json.Unmarshal(seoMetaJSON, &seoMetaDocData); err != nil {
			observability.Error(context.Background(), "seo_meta.json 解析失败，服务端 SEO 注入停用", "err", err.Error())
			return
		}
		if len(seoMetaDocData.Languages) == 0 || len(seoMetaDocData.Routes) == 0 {
			observability.Error(context.Background(), "seo_meta.json 内容为空，服务端 SEO 注入停用")
			return
		}
		seoMetaOK = true
	})
}

// envSEORouteMeta／configSEORouteMeta 服务端 SEO 注入开关的三档读点。
// 与 site_flags.go 同一形态（AGENTS §一·3：环境变量 > 数据库配置 > 代码默认）：
//   - env 档是**部署级急救**（《部署指南》回滚表里那一行 `SEO_ROUTE_META=0`），换了二进制也不影响它；
//   - 库档 `seo_route_meta` 给运维一条"不发版、不重启就关掉注入"的出路
//     （收录策略临时收紧、或 route meta 出问题时先停注入保首页）；
//   - 默认开：这个特性的存在意义就是让内页各自可收录，缺配置不该退回"全站一句标题"。
//
// 脏值（打错一个字母、写了 "off " 之外的怪值）**一律不表态**，落到下一档——
// 误把一个非法值读成"关闭"＝整站内页失去 title/canonical，比读成"开启"坏得多。
const (
	envSEORouteMeta    = "SEO_ROUTE_META"
	configSEORouteMeta = "seo_route_meta"
)

// seoFlagTruthy／seoFlagFalsy 布尔开关的取值口径（三处共用一套，禁止再各写一份字面量集合）。
var (
	seoFlagTruthy = map[string]bool{"1": true, "true": true, "yes": true, "on": true}
	seoFlagFalsy  = map[string]bool{"0": true, "false": true, "no": true, "off": true}
)

// seoRouteMetaEnvDeclared 解析 env 档表态。返回 (是否表态, 表态后的开关值)。
func seoRouteMetaEnvDeclared() (declared, enabled bool) {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(envSEORouteMeta)))
	switch {
	case v == "":
		return false, true
	case seoFlagFalsy[v]:
		return true, false
	case seoFlagTruthy[v]:
		return true, true
	default:
		return false, true
	}
}

// seoRouteMetaEnabled 服务端 SEO 注入总开关（方法面：库档要读 s.Store）。
// 第三层判据 `seoMetaOK` 与开关无关地兜底：嵌入的 JSON 解析失败＝本特性等同不存在，
// 首页照常直出（fail-open，同 §一·10「新增防护不拿可用性交押金」）。
func (s *Server) seoRouteMetaEnabled() bool {
	if declared, on := seoRouteMetaEnvDeclared(); declared {
		if !on {
			return false
		}
	} else if s.Store != nil {
		if v, err := s.Store.GetConfig(configSEORouteMeta); err == nil {
			val := strings.ToLower(strings.TrimSpace(v))
			if seoFlagFalsy[val] {
				return false
			}
			// 显式 "1" 与脏值一样落到最后的嵌入可用性判定（开＝回到默认档，不额外许诺）
		}
	}
	loadSEOMeta()
	return seoMetaOK
}

// seoLangFromRequest 12 语种解析：?lang= > X-App-Lang > Accept-Language > zh。
// ⚠️ 不能复用 internal/i18n.Detect（它把 zh 系折叠 zh、其余折叠 en，是服务端提示语两语种契约）。
func seoLangFromRequest(r *http.Request) string {
	if v := strings.TrimSpace(r.URL.Query().Get("lang")); v != "" {
		if l := seoNormalizeLang(v); l != "" {
			return l
		}
	}
	if v := strings.TrimSpace(r.Header.Get("X-App-Lang")); v != "" {
		if l := seoNormalizeLang(v); l != "" {
			return l
		}
	}
	for _, part := range strings.Split(r.Header.Get("Accept-Language"), ",") {
		segs := strings.SplitN(part, ";", 2)
		tag := strings.TrimSpace(segs[0])
		if tag == "" {
			continue
		}
		// q=0 表示明确拒绝；q=0.9 不能因为字符串里含 "q=0" 被误跳过（按数值判）。
		if len(segs) > 1 {
			if qs := strings.TrimSpace(segs[1]); strings.HasPrefix(strings.ToLower(qs), "q=") {
				if f, err := strconv.ParseFloat(strings.TrimSpace(qs[2:]), 64); err == nil && f == 0 {
					continue
				}
			}
		}
		if l := seoNormalizeLang(tag); l != "" {
			return l
		}
	}
	return seoDefaultLang()
}

// seoDefaultLang 兜底语种（JSON x_default_lang，缺省 zh）。
func seoDefaultLang() string {
	loadSEOMeta()
	if seoMetaDocData.XDefaultLang != "" {
		return seoMetaDocData.XDefaultLang
	}
	return "zh"
}

// seoNormalizeLang 把 BCP47/浏览器标签归一成 12 语种内部码；未知返回空串。
func seoNormalizeLang(tag string) string {
	t := strings.ToLower(strings.TrimSpace(tag))
	if t == "" || t == "*" {
		return ""
	}
	if strings.HasPrefix(t, "zh") {
		if strings.Contains(t, "hant") || strings.Contains(t, "-tw") || strings.Contains(t, "-hk") || strings.Contains(t, "-mo") {
			return "zh_hant"
		}
		return "zh"
	}
	for _, code := range []string{"en", "ru", "fr", "ar", "es", "pt", "de", "ja", "ko", "th"} {
		if t == code || strings.HasPrefix(t, code+"-") || strings.HasPrefix(t, code+"_") {
			return code
		}
	}
	return ""
}

// seoHreflangCode 内部语种码 → BCP47（hreflang 只认合法语言标签；zh_hant 下划线不合法）。
func seoHreflangCode(code string) string {
	switch code {
	case "zh":
		return "zh-Hans"
	case "zh_hant":
		return "zh-Hant"
	default:
		return code
	}
}

// seoPick 按语种取文案，缺该语种回落 en→zh（12 语种全量由 JSON 保证，回落只防漏键）。
func seoPick(m map[string]string, lang string) string {
	if m == nil {
		return ""
	}
	if v := strings.TrimSpace(m[lang]); v != "" {
		return v
	}
	if v := strings.TrimSpace(m["en"]); v != "" {
		return v
	}
	return strings.TrimSpace(m["zh"])
}

// seoMetaForPath 返回该路径的 meta；isApp=true 表示工作台/后台等 noindex 面。
func seoMetaForPath(path string) (seoRouteMeta, bool, bool) {
	loadSEOMeta()
	if m, ok := seoMetaDocData.Routes[path]; ok {
		return m, false, true
	}
	if !isKnownClientRoute(path) {
		return seoRouteMeta{}, false, false
	}
	return seoRouteMeta{
		Robots: seoMetaDocData.App.Robots,
		Title:  map[string]string{"en": seoMetaDocData.App.TitleSuffix, "zh": seoMetaDocData.App.TitleSuffix},
	}, true, true
}

// 注入用的标签匹配正则（替换首处；不存在则插到 </head> 前）。
var (
	seoTitleRe     = regexp.MustCompile(`(?is)<title[^>]*>.*?</title>`)
	seoDescRe      = regexp.MustCompile(`(?is)<meta[^>]*name=["']description["'][^>]*>`)
	seoOGTitleRe   = regexp.MustCompile(`(?is)<meta[^>]*property=["']og:title["'][^>]*>`)
	seoOGURLRe     = regexp.MustCompile(`(?is)<meta[^>]*property=["']og:url["'][^>]*>`)
	seoOGDescRe    = regexp.MustCompile(`(?is)<meta[^>]*property=["']og:description["'][^>]*>`)
	seoOGImageRe   = regexp.MustCompile(`(?is)<meta[^>]*property=["']og:image["'][^>]*>`)
	seoOGImageSzRe = regexp.MustCompile(`(?is)<meta[^>]*property=["']og:image:(?:width|height)["'][^>]*>`)
	seoTwImageRe   = regexp.MustCompile(`(?is)<meta[^>]*name=["']twitter:image["'][^>]*>`)
	seoTwCardRe    = regexp.MustCompile(`(?is)<meta[^>]*name=["']twitter:card["'][^>]*>`)
	seoCanonicalRe = regexp.MustCompile(`(?is)<link[^>]*rel=["']canonical["'][^>]*>`)
	seoRobotsRe    = regexp.MustCompile(`(?is)<meta[^>]*name=["']robots["'][^>]*>`)
	seoHreflangRe  = regexp.MustCompile(`(?is)<link[^>]*\bhreflang=["'][^"']*["'][^>]*>`)
)

// seoOGImagePath 分享卡图的站内路径（唯一事实源＝scripts/growth/build_og_image.py 的 OG_IMAGE_PATH，
// 落盘位置 frontend-react/public/og-image.png，随 dist 换源即上线）。
// og:image/twimage 按爬虫与社交抓取方的要求**必须是绝对 URL**，所以这里只存路径，
// 域名由注入时现取的 scheme/host 拼出来（同 canonical 口径，不写死站点域名）。
const seoOGImagePath = "/og-image.png"

// seoPlatformBrand 平台品牌名。白标判定用它：租户品牌域上的分享卡是平台广告位，必须摘掉。
const seoPlatformBrand = "能言 LangCross"

// 分享卡图尺寸（与 scripts/growth/build_og_image.py 的 W/H、SEO 工厂的 OG_IMAGE_W/H 同值三处；
// 改卡图尺寸必须三处一起改，社交端按这两个数预留占位）。
const (
	seoOGImageW = "1200"
	seoOGImageH = "630"
)

// seoInsertBeforeHead 把标签插到 </head> 之前（找不到 head 时前置，保证不丢）。
func seoInsertBeforeHead(htmlText, tag string) string {
	if i := strings.Index(strings.ToLower(htmlText), "</head>"); i >= 0 {
		return htmlText[:i] + tag + htmlText[i:]
	}
	return tag + htmlText
}

// seoReplaceOrInsert 替换首个匹配标签；没有匹配则插入。
func seoReplaceOrInsert(htmlText string, re *regexp.Regexp, tag string) string {
	if re.MatchString(htmlText) {
		return re.ReplaceAllStringFunc(htmlText, func(string) string { return tag })
	}
	return seoInsertBeforeHead(htmlText, tag)
}

// seoRemoveTag 摘掉全部匹配标签（白标面用：留着平台分享卡＝替平台在租户域上做广告）。
func seoRemoveTag(htmlText string, re *regexp.Regexp) string {
	return re.ReplaceAllString(htmlText, "")
}

// seoScheme 生成 canonical 用的 scheme（反代头优先，缺省 https）。
func seoScheme(r *http.Request) string {
	if p := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")); p == "http" || p == "https" {
		return p
	}
	if r.TLS != nil {
		return "https"
	}
	return "https"
}

// injectRouteMeta 在 </head> 前注入/替换路由 meta（先品牌注入，后用品牌名拼标题）。
func injectRouteMeta(htmlText string, meta seoRouteMeta, lang, brand, scheme, host, path string, isApp bool) string {
	title := seoPick(meta.Title, lang)
	if isApp {
		title = strings.TrimSpace(brand)
		if title == "" {
			title = seoMetaDocData.App.TitleSuffix
		}
	}
	if title == "" {
		title = brand
	}
	desc := seoPick(meta.Description, lang)
	// 白标：租户品牌域上，route meta 里的平台名必须替换为该租户品牌（否则品牌域标题仍是 LangCross）。
	whiteLabel := brand != "" && brand != seoPlatformBrand
	if whiteLabel {
		title = strings.ReplaceAll(title, seoPlatformBrand, brand)
		title = strings.ReplaceAll(title, "LangCross", brand)
		desc = strings.ReplaceAll(desc, seoPlatformBrand, brand)
		desc = strings.ReplaceAll(desc, "LangCross", brand)
	}
	escTitle := html.EscapeString(title)
	escDesc := html.EscapeString(desc)
	canonical := scheme + "://" + host + path + "?lang=" + lang
	escCanonical := html.EscapeString(canonical)
	// 分享卡图：图本身是**平台品牌资产**（卡面写着「能言 LangCross」），所以只有平台品牌域发它；
	// 租户品牌域上必须**整颗摘掉**（含 og:image:width/height 两枚配套尺寸），
	// 否则客户转发自己域名的链接，社交卡上出现的是别人的品牌——这与上面「白标替换品牌名」是同一件事的两半。
	ogImageURL := html.EscapeString(scheme + "://" + host + seoOGImagePath)

	out := seoReplaceOrInsert(htmlText, seoTitleRe, "<title>"+escTitle+"</title>")
	// 分享卡图族：先**整族摘掉基线 HTML 里那一份**，再按品牌形态重插——
	// 判据问的是「最终出栈形态」，不是「有没有这一行」；逐条替换会在基线缺某一条时长出残缺组。
	out = seoRemoveTag(out, seoOGImageRe)
	out = seoRemoveTag(out, seoOGImageSzRe)
	out = seoRemoveTag(out, seoTwImageRe)
	if whiteLabel {
		// 平台卡图不在租户域上发，twitter 卡档同时回落小图（大卡图却无图＝社交端渲染成破图占位）。
		out = seoReplaceOrInsert(out, seoTwCardRe, `<meta name="twitter:card" content="summary">`)
	} else {
		out = seoInsertBeforeHead(out,
			`<meta property="og:image" content="`+ogImageURL+`">`+
				`<meta property="og:image:width" content="`+seoOGImageW+`">`+
				`<meta property="og:image:height" content="`+seoOGImageH+`">`+
				`<meta name="twitter:image" content="`+ogImageURL+`">`)
		out = seoReplaceOrInsert(out, seoTwCardRe, `<meta name="twitter:card" content="summary_large_image">`)
	}
	if desc != "" {
		out = seoReplaceOrInsert(out, seoDescRe, `<meta name="description" content="`+escDesc+`">`)
	}
	out = seoReplaceOrInsert(out, seoCanonicalRe, `<link rel="canonical" href="`+escCanonical+`">`)
	out = seoReplaceOrInsert(out, seoOGURLRe, `<meta property="og:url" content="`+escCanonical+`">`)
	out = seoReplaceOrInsert(out, seoOGTitleRe, `<meta property="og:title" content="`+escTitle+`">`)
	if desc != "" {
		out = seoReplaceOrInsert(out, seoOGDescRe, `<meta property="og:description" content="`+escDesc+`">`)
	}
	robots := strings.TrimSpace(meta.Robots)
	if robots == "" {
		robots = "index,follow"
	}
	out = seoReplaceOrInsert(out, seoRobotsRe, `<meta name="robots" content="`+html.EscapeString(robots)+`">`)
	if !isApp && !strings.Contains(robots, "noindex") {
		out = seoHreflangRe.ReplaceAllString(out, "")
		var b strings.Builder
		for _, code := range seoMetaDocData.Languages {
			b.WriteString(`<link rel="alternate" hreflang="` + html.EscapeString(seoHreflangCode(code)) + `" href="` +
				html.EscapeString(scheme+"://"+host+path+"?lang="+code) + `">`)
		}
		b.WriteString(`<link rel="alternate" hreflang="x-default" href="` +
			html.EscapeString(scheme+"://"+host+path) + `">`)
		out = seoInsertBeforeHead(out, b.String())
	}
	return out
}

// seoBrandForRequest 从品牌注入载荷取展示名；取不到回落平台名。
func seoBrandForRequest(payload map[string]interface{}) string {
	if payload != nil {
		if v, ok := payload["brand_name"].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
		if v, ok := payload["name"].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return "能言 LangCross"
}
