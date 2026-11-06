// ============ product_ssr.go · 职责说明 ============
// 对外产品页（/p/{tenantCode}/{pageCode}）的**直出面**：解析两段短码 ＋ 注入 SEO 元数据与无 JS 首屏。
// =============================================
package api

// ============ 本文件职责中文说明 ============
// 一期裁定 6「对外页进 SEO 面」要求搜索引擎与无 JS 访客都能拿到产品事实，而前端是 SPA 壳
// （`#root` 空 div ＋ 懒加载 chunk），所以产品页的元数据只能由后端在 serveIndexHTML 这一跳注入，
// 形态与既有 `__BRANDING__`／route meta 完全同族，**复用 seo_meta.go 那一套替换器**，不另造第二把尺子。
// 三条硬口径：
//   1. **文案唯一出口＝`Store.ListPublicCorpus`**（SQL 层已挡掉 `visibility='gated'`），
//      本文件**禁止**直接读 `product_facts`／`product_assets`——绕过那一个出口就是 §5.6 V4 的第二个肇事面。
//   2. **短码解不到 ⇒ 真 404**（由 handleSPA 判定），不在 HTML 里回 200 空壳：
//      跨租户拼接与地址打错在访客与爬虫眼里都必须与"页面存在"不同形（G6 同一条判据）。
//   3. **首屏静态文案落 `<noscript>`**：带 JS 的浏览器由 React 渲染真实页面，无 JS／不执行 JS 的
//      爬虫读这一段。落点**不许**是 `#root` 内部——启动哨兵判"界面起没起来"只问
//      `#root.childElementCount`（AGENTS §一·5 批⑤），往里面塞东西会把失败态永久判成成功。

import (
	"encoding/json"
	"html"
	"net/http"
	"regexp"
	"strings"

	"translator/internal/store"
)

// productPagePathRe 对外页路径形状：两段不可枚举短码，允许可选结尾斜杠与任意查询串（查询串不参与解析）。
var productPagePathRe = regexp.MustCompile(`^/p/([A-Za-z0-9_-]{4,64})/([A-Za-z0-9_-]{4,64})/?$`)

// productPageCodesFromPath 取对外页的两段短码；形状不合（含 `/p/` 缺段、多段、非法字符）回 ok=false。
// 参数：path=请求路径（不含查询串）；返回租户段、页面段、是否命中形状。
func productPageCodesFromPath(path string) (string, string, bool) {
	m := productPagePathRe.FindStringSubmatch(path)
	if m == nil {
		return "", "", false
	}
	return m[1], m[2], true
}

// productPageFor 解析对外页该展示哪个产品：**两段短码按路径取**，然后走与 JSON 口
// **同一个** `productResolvedCodes`（含"按访问域名的租户一致性校验"）。
//
// ★ 不许在这里另起一条解析链：P10（A 域名＋B 短码不得出现 B 的内容／A 的品牌名）判的就是
//
//	那一个口；直出面绕过它，等于同一条不变量在两个面上各成立一次——正是本仓"两把尺子"的长法。
//
// 参数：r=请求；返回产品与是否命中。
func (s *Server) productPageFor(r *http.Request) (store.Product, bool) {
	tc, pc, ok := productPageCodesFromPath(strings.TrimSpace(r.URL.Path))
	if !ok {
		return store.Product{}, false
	}
	return s.productResolvedCodes(r, tc, pc)
}

// productPublicLinesForLang 取该语种可见的公开语料文本（保序、去空、去重）。
// 口径：事实项的 lang 恒空串（值本身不标语种）⇒ 一律可见；物料带语种 ⇒ 只在与界面语种一致时可见。
// 参数：items=ListPublicCorpus 出参，lang=本轮渲染语种；返回文本清单。
func productPublicLinesForLang(items []store.PublicCorpusItem, lang string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, it := range items {
		text := strings.TrimSpace(it.Text)
		if text == "" || seen[text] {
			continue
		}
		if it.Lang != "" && lang != "" && it.Lang != lang {
			continue
		}
		seen[text] = true
		out = append(out, text)
	}
	return out
}

// productMetaTitle 产品页标题＝「产品名 · 品牌」；品牌取不到就只出产品名（不出空分隔符）。
// 参数：p=产品，brand=该域品牌名。
func productMetaTitle(p store.Product, brand string) string {
	name := strings.TrimSpace(p.Name)
	brand = strings.TrimSpace(brand)
	if brand == "" {
		return name
	}
	return name + " · " + brand
}

// productMetaDescription 摘要＝tagline ＋最多三条公开事实，总长夹到 200 字（超出截断加省略号）。
// 参数：p=产品，lines=该语种可见公开文本。
func productMetaDescription(p store.Product, lines []string) string {
	parts := []string{}
	if t := strings.TrimSpace(p.Tagline); t != "" {
		parts = append(parts, t)
	}
	for i, l := range lines {
		if i >= 3 {
			break
		}
		parts = append(parts, l)
	}
	desc := strings.Join(parts, "；")
	r := []rune(desc)
	if len(r) > 200 {
		desc = string(r[:200]) + "…"
	}
	return desc
}

// productJSONLD 拼 schema.org/Product 结构化数据；★ 只装产品名＋描述＋公开条目，
// 受控值物理上进不来（数据源就是 ListPublicCorpus），不靠"记得过滤"。
// 参数：p=产品，url=canonical，lines=公开文本。
func productJSONLD(p store.Product, url string, lines []string) string {
	desc := lines
	if len(desc) > 12 {
		desc = desc[:12]
	}
	doc := map[string]interface{}{
		"@context":    "https://schema.org",
		"@type":       "Product",
		"name":        strings.TrimSpace(p.Name),
		"description": strings.TrimSpace(p.Tagline),
		"url":         url,
		"category":    strings.TrimSpace(p.IndustryCode),
		"additionalProperty": func() []map[string]string {
			out := []map[string]string{}
			for _, l := range desc {
				out = append(out, map[string]string{"@type": "PropertyValue", "name": l, "value": l})
			}
			return out
		}(),
	}
	b, err := json.Marshal(doc)
	if err != nil {
		return ""
	}
	return `<script type="application/ld+json">` + string(b) + `</script>`
}

// productHreflangLangs 决定对外页铺哪几语的 hreflang：产品声明过就按声明（与站点支持语种取交集，
// 保持提交顺序）；没声明就回站点全表。参数：p=产品行；返回语种码切片。
func productHreflangLangs(p store.Product) []string {
	// ★ 先确保嵌入的码表已经解出来：`seoMetaDocData` 是 sync.Once 懒加载的，
	//   而 /p/ 这一条腿完全可能比首页更早被走到（爬虫直接抓对外页、或单测直接打那一个路径）。
	//   没确保就读 ⇒ Languages 是空表 ⇒ 铺出**零条 hreflang 且一条日志都没有**，
	//   正是本仓「有自动降级的链路，产物能打开不算验收」那一族。
	loadSEOMeta()
	want := p.TargetLangList()
	if len(want) == 0 {
		return seoMetaDocData.Languages
	}
	supported := map[string]bool{}
	for _, c := range seoMetaDocData.Languages {
		supported[c] = true
	}
	out := []string{}
	for _, c := range want {
		if supported[c] {
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		// 声明的全部是站点不支持的码（脏值／历史数据）：宁可铺全表，也不铺出一串 404 的 hreflang。
		return seoMetaDocData.Languages
	}
	return out
}

// injectProductMeta 对外页直出时注入 title／description／canonical／hreflang／JSON-LD 与 `<noscript>` 首屏。
//
// 与 route meta 的分工：`route_meta.json` 是**静态路由**的 12 语种唯一事实源；动态产品页**不复制第二份元数据**，
// 只从 `products` 现算（§5.8 第一条），两者共用同一批替换器与 hreflang 码表。
// 参数：htmlText=已完成品牌／门面注入的入口 HTML，r=请求，brand=该域品牌名。
func (s *Server) injectProductMeta(htmlText string, r *http.Request, brand string) string {
	p, ok := s.productPageFor(r)
	if !ok {
		return htmlText
	}
	lang := seoLangFromRequest(r)
	items, err := s.Store.ListPublicCorpus(p.TenantID, p.ID)
	if err != nil {
		items = []store.PublicCorpusItem{}
	}
	lines := productPublicLinesForLang(items, lang)
	title := productMetaTitle(p, brand)
	desc := productMetaDescription(p, lines)
	path := r.URL.Path
	scheme := seoScheme(r)
	canonical := scheme + "://" + r.Host + path

	escTitle := html.EscapeString(title)
	out := seoReplaceOrInsert(htmlText, seoTitleRe, "<title>"+escTitle+"</title>")
	if desc != "" {
		out = seoReplaceOrInsert(out, seoDescRe, `<meta name="description" content="`+html.EscapeString(desc)+`">`)
		out = seoReplaceOrInsert(out, seoOGDescRe, `<meta property="og:description" content="`+html.EscapeString(desc)+`">`)
	}
	out = seoReplaceOrInsert(out, seoOGTitleRe, `<meta property="og:title" content="`+escTitle+`">`)
	out = seoReplaceOrInsert(out, seoCanonicalRe, `<link rel="canonical" href="`+html.EscapeString(canonical)+`">`)
	out = seoReplaceOrInsert(out, seoOGURLRe, `<meta property="og:url" content="`+html.EscapeString(canonical)+`">`)
	// robots：未 live 的页面是"有链接但没发布"，对爬虫必须明确 noindex（sitemap 那一侧同判据已排 live）。
	robots := "noindex,follow"
	if p.Status == store.ProductStatusLive {
		robots = "index,follow"
	}
	out = seoReplaceOrInsert(out, seoRobotsRe, `<meta name="robots" content="`+robots+`">`)
	// hreflang 码表与静态路由同源（`seoMetaDocData.Languages`），只在真发布且开了索引时铺。
	if p.Status == store.ProductStatusLive && p.SeoIndexable == 1 {
		out = seoHreflangRe.ReplaceAllString(out, "")
		var b strings.Builder
		// ★ O 组（2026-11-06）：产品声明了目标语言就只铺那几语（声明是卖家的话，不许系统替他多说）；
		//   **未声明时保持全表铺**——把空值读成"只支持中文"或"跟随界面语种"都是拿缺省替客户决定对外说法。
		for _, code := range productHreflangLangs(p) {
			b.WriteString(`<link rel="alternate" hreflang="` + html.EscapeString(seoHreflangCode(code)) + `" href="` +
				html.EscapeString(scheme+"://"+r.Host+path+"?lang="+code) + `">`)
		}
		b.WriteString(`<link rel="alternate" hreflang="x-default" href="` + html.EscapeString(canonical) + `">`)
		out = seoInsertBeforeHead(out, b.String())
	}
	if ld := productJSONLD(p, canonical, lines); ld != "" {
		out = seoInsertBeforeHead(out, ld)
	}
	return insertProductNoscriptFirstScreen(out, title, lines)
}

// productNoscriptAnchor 无 JS 首屏的落点锚：`#root` 那一个 div。
// 只认带 id="root" 的开标签，命中它的**闭合形态**（`></div>` 或自闭）才插到它**后面**——
// 插进里面就是上面口径 3 禁止的形态。
var productNoscriptAnchor = regexp.MustCompile(`(?is)<div[^>]*\bid="root"[^>]*(/>|>[\s]*</div>)`)

// insertProductNoscriptFirstScreen 在 `#root` **之后**插一段 `<noscript>` 首屏（标题＋公开条目）。
// 找不到锚（构建形态变了）就原样返回并让 SEO 判据自己红——不许猜一个位置插坏整页。
// 参数：htmlText=入口 HTML，title=页面标题，lines=公开文本。
func insertProductNoscriptFirstScreen(htmlText, title string, lines []string) string {
	loc := productNoscriptAnchor.FindStringIndex(htmlText)
	if loc == nil {
		return htmlText
	}
	var b strings.Builder
	b.WriteString(`<noscript id="__product_first__"><h1>`)
	b.WriteString(html.EscapeString(title))
	b.WriteString(`</h1>`)
	for i, l := range lines {
		if i >= 20 {
			break
		}
		b.WriteString(`<p>`)
		b.WriteString(html.EscapeString(l))
		b.WriteString(`</p>`)
	}
	b.WriteString(`</noscript>`)
	end := loc[1]
	return htmlText[:end] + b.String() + htmlText[end:]
}
