// ============ spa.go · 职责说明 ============
// SPA 兜底路由：非 API 路径统一回退 index.html，静态资源直出并设缓存头。
// =============================================
package api

// ============ 本文件职责中文说明 ============
// 本文件实现前端静态资源（SPA）托管服务：
//   - 拦截 /api 前缀的未匹配路由，统一返回 404 JSON（避免误回退到前端页面）
//   - 前端 dist 目录未构建时（index.html 缺失），首页返回安装引导页，其余路径返回 404
//   - 提供前端构建产物（HTML/JS/CSS/图片等）的静态文件服务
//   - SPA 路由回退：请求路径不存在时回退到 index.html（配合前端 history 路由）
//   - 目录 URL（`/blindtest/`）：先解析该目录的 index.html，缺失才回退 SPA 入口
//   - 缓存策略：带 hash 的 /assets/ 资源长缓存（immutable），其余资源每次重新校验
// 安全约束：对请求路径做 filepath.Clean 归一化并校验前缀，防止目录穿越（path traversal）。

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// handleSPA 前端静态资源服务入口；无 dist 构建产物时给出安装提示页。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（依据 r.URL.Path 分发静态资源或回退路由）。
func (s *Server) handleSPA(w http.ResponseWriter, r *http.Request) {
	// 拦截 /api 前缀的未匹配路由：API 由其他 Handler 处理，这里避免把接口请求回退到前端页面
	if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") {
		writeJSON(w, 404, map[string]string{"error": "接口不存在"})
		return
	}

	// 确定前端 dist 根目录：未配置时默认取进程工作目录下的 "dist"
	dist := s.Dist
	if dist == "" {
		dist = "dist"
	}
	// 校验 dist 下是否存在 index.html：缺失说明前端尚未构建，给出安装引导
	if _, err := os.Stat(filepath.Join(dist, "index.html")); err != nil {
		if r.URL.Path == "/" {
			// 首页：返回静态安装提示 HTML（说明如何构建前端）
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`<!DOCTYPE html><html><body style="font-family:sans-serif;max-width:640px;margin:60px auto">
<h1>能言服务已启动</h1>
<p>前端页面尚未构建。请先在前端目录执行 <code>npm run build</code>，
或在 <code>backend-go/cmd/server/main.go</code> 中通过 <code>-frontend</code> 指定前端 dist 目录。</p>
<p>API 正常：<a href="/api/health">/api/health</a></p>
</body></html>`))
		} else {
			// 非首页且未构建：返回 404
			writeJSON(w, 404, map[string]string{"error": "未找到前端资源"})
		}
		return
	}

	// 拼接实际文件路径：根路径映射到 index.html
	path := r.URL.Path
	if path == "/" {
		path = "/index.html"
	}
	// 归一化路径并校验前缀：防止 "../" 目录穿越读取 dist 之外的文件
	full := filepath.Join(dist, filepath.Clean(filepath.FromSlash(path)))
	if !strings.HasPrefix(full, filepath.Clean(dist)) {
		writeJSON(w, 404, map[string]string{"error": "非法路径"})
		return
	}
	// 目标文件不存在或是目录：SPA 路由回退到 index.html（每次重新校验，避免缓存旧 bundle）
	info, err := os.Stat(full)
	if err != nil || info.IsDir() {
		// ★ 目录 URL 先解析该目录自己的 index.html（2026-10-17 补：`/blindtest/` 这类可分享静态页的
		//   自然形态此前一律回退 SPA ⇒ 干净 URL 返回首页、只有带 `index.html` 才拿得到那张页，
		//   而 sitemap/分享链接不会写文件名）。命中即走 serveIndexHTML，使 `/x/` 与 `/x/index.html`
		//   两种写法**逐字节一致**（品牌注入、门面注入、no-cache 头都同一条链）。
		//   缺失（如 `/assets/`）才回退 SPA 入口，行为与改动前完全相同。
		if err == nil && info.IsDir() {
			if idx := filepath.Join(full, "index.html"); idx != "" {
				if idxInfo, idxErr := os.Stat(idx); idxErr == nil && !idxInfo.IsDir() {
					s.serveIndexHTML(w, r, idx)
					return
				}
			}
		}
		// ★ G6（2026-10-19）：只有**前端已知路由**才回退 SPA 入口；其余未知深链直出真 404 面。
		//   旧形态对任何磁盘上不存在的路径一律回 index.html（状态码 200），于是「地址不存在」与
		//   「要登录」在访客眼里同形（D7），且 SEO 拿不到真实状态码。已知路由白名单见 spaClientRoutes。
		if isKnownClientRoute(path) {
			// SPA 路由回退到 index.html（注入当前域名品牌，消除首屏「先通用后品牌」闪烁）
			s.serveIndexHTML(w, r, filepath.Join(dist, "index.html"))
			return
		}
		s.serveSPANotFound(w, r)
		return
	}
	// 入口 HTML（index.html）：注入当前访问域名的品牌定制，避免首屏闪烁
	if filepath.Base(full) == "index.html" {
		s.serveIndexHTML(w, r, full)
		return
	}
	// 缓存策略：带 hash 的 /assets/ 静态资源可长缓存；index.html 等入口每次重新校验
	if strings.HasPrefix(r.URL.Path, "/assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	http.ServeFile(w, r, full)
}

// spaClientExactRoutes 前端客户端路由的**精确**白名单（G6）。
// ★ 维护口径：与 frontend-react/src/App.tsx 的 <Routes> / Root 分诊段同源——新增顶层路由必须同步补这里，
// 否则该路由的深链会被 handleSPA 判成「非路由」而由后端直出 404（构建不出错、只在访问时现形）。
// 只放**前端自己消费**的路径；后端注册的 API/静态前缀（/api、/docs、/brand、/office、/extensions、/sdk…）
// 根本不进 handleSPA，无需登记。
var spaClientExactRoutes = map[string]bool{
	"/":             true,
	"/login":        true,
	"/register":     true,
	"/pricing":      true,
	"/compare":      true,
	"/tickets":      true,
	"/editor":       true,
	"/billing":      true,
	"/invites":      true,
	"/packages":     true,
	"/my":           true,
	"/chat-history": true,
}

// spaClientRoutePrefixes 带子树的前端路由（/admin 内的面板由本地 tab 态切换，但仍保守放行整棵子树）。
var spaClientRoutePrefixes = []string{"/admin"}

// isKnownClientRoute 判断路径是否为前端已知路由（G6：仅已知路由才回退 SPA 入口）。
// 参数 path: URL 路径（已由 handleSPA 归一为该形态）。返回: 命中白名单即 true。
func isKnownClientRoute(path string) bool {
	if spaClientExactRoutes[path] {
		return true
	}
	for _, p := range spaClientRoutePrefixes {
		if path == p || strings.HasPrefix(path, p+"/") {
			return true
		}
	}
	return false
}

// serveSPANotFound 对未知深链直出真 404 面（G6）。
//
// 三条硬口径：① 状态码必须真 404（旧形态回 200 整页 index.html，SEO 与访客都无法分辨「不存在」）；
// ② 必须显式 `Cache-Control: no-store`——AGENTS §一·5 批④ 实测「源站不发 cache-control ≠ 不缓存」，
// Cloudflare 会给 404 自补 max-age 并 HIT；③ 面文案只说「不存在」，不泄露任何内部信息。
func (s *Server) serveSPANotFound(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNotFound)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write([]byte(`<!DOCTYPE html><html lang="zh"><head><meta charset="utf-8">` +
		`<meta name="viewport" content="width=device-width,initial-scale=1">` +
		`<title>404 · 页面不存在</title></head>` +
		`<body style="margin:0;background:#000;color:#E7E9EA;font-family:system-ui,-apple-system,sans-serif">` +
		`<main style="max-width:560px;margin:18vh auto;padding:0 24px;text-align:center">` +
		`<h1 style="font-size:56px;margin:0 0 8px">404</h1>` +
		`<p style="font-size:18px;color:#B9BFC6;margin:0 0 24px">这个地址不存在 · This page does not exist</p>` +
		`<a href="/" style="display:inline-block;padding:10px 20px;border:1.2px solid #565E6B;border-radius:999px;color:#FFFFFF;text-decoration:none">返回首页 · Back to home</a>` +
		`</main></body></html>`))
}

// serveIndexHTML 读取并回退前端入口 HTML，并在 </head> 前注入当前访问域名的品牌定制
//
// ★ F-46（2026-09-26 批 I-9）缓存口径——**修到一半推翻了自己原来的改法，这里记成决策**：
//
//	修复文档原本要「对无品牌定制的域名走可缓存路径（public, max-age=60），只给品牌站留 no-cache」。
//	实现完按线上实测头一核对，这条**收益不存在、风险却真实**：
//	  · 主站响应头实测 `cf-cache-status: DYNAMIC` + `Cache-Control: no-cache, must-revalidate`
//	    （源站自己写的是 no-cache，`must-revalidate` 是 Cloudflare 那层加的）
//	    ⇒ HTML 在 CDN 侧**从来没被缓存过**，改这个头换不来"少回源"；
//	  · 反而一旦写 `public, max-age=60`，若哪天给 HTML 开了边缘缓存，而缓存键没带上 Host/Vary，
//	    就会出现「品牌站的 HTML 被主站访客命中」这种跨域名串品牌（真实且难查的事故形态）。
//	首屏 2 MB 的病根是**注入体积**（dataURI 整串进 HTML），已由 brand_assets.go 落静态件根治，
//	与 HTML 缓不缓存无关。故此处**保持无条件 no-cache**（产品语义：后台改品牌立刻见效），
//	并把"给无品牌站长缓存"这个念头连同它的判据一起钉在这里，免得下一轮又有人"照文档施工"。
func (s *Server) serveIndexHTML(w http.ResponseWriter, r *http.Request, path string) {
	raw, err := os.ReadFile(path)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "读取前端入口失败"})
		return
	}
	html := injectBrandingScript(s.brandingPayload(r), string(raw))
	// ★ 〇-Z 站点门面标记：只在「主页对访客关闭」时落这一字节段，开放时上面那行已是终点
	//（保持主站首页字节数判据不动，理由见 site_flags.go 文件头）。
	html = injectSiteFlagsScript(s.siteFlagsPayload(r), html)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	// ★ 2026-10-18 慢链路批（#1「国内加载慢」）：关键资源提前声明（RFC 8288 Link）。
	//   入口 script 与首页样式表都在文档**末尾**才出现，浏览器要等整篇 HTML 解析到那里
	//   才发起请求；这一条把它变成"收 HTML 的同时就起请求"，CDN 侧（Cloudflare Early Hints）
	//   还能据此在正文之前先把这两个 URL 推给客户端。名字**现读现算**（构建一变哈希就变），
	//   硬编码＝指向 404 的反向优化（见 spa_preload_links_test.go 的 R1）。
	if links := criticalAssetLinks(html); links != "" {
		w.Header().Set("Link", links)
	}
	w.Write([]byte(html))
}

// preloadScriptRe / preloadStyleReA / preloadStyleReB 从入口 HTML 里现读关键资源 URL。
// 样式表两条正则是因为属性顺序不固定（Vite 出的是 `rel` 在前，手写页可能 `href` 在前）。
var (
	preloadScriptRe = regexp.MustCompile(`(?is)<script[^>]*\bsrc="([^"]+)"`)
	preloadStyleReA = regexp.MustCompile(`(?is)<link[^>]*\brel="stylesheet"[^>]*\bhref="([^"]+)"`)
	preloadStyleReB = regexp.MustCompile(`(?is)<link[^>]*\bhref="([^"]+)"[^>]*\brel="stylesheet"`)
)

// criticalAssetLinks 现读入口 HTML，拼出首屏关键资源的 Link 头值（没有关键资源就回空串）。
//
// 只认 `/assets/` 前缀：那是构建产物（带内容哈希）所在，也正是 Caddy/后端的长缓存面；
// 其它形态（外链 CDN、dataURI）不猜、不预加载——预加载一个不存在的 URL 的代价比不预加载大。
func criticalAssetLinks(html string) string {
	var links []string
	if m := preloadScriptRe.FindStringSubmatch(html); m != nil {
		if u := m[1]; strings.HasPrefix(u, "/assets/") {
			links = append(links, "<"+u+">; rel=preload; as=script; crossorigin")
		}
	}
	href := ""
	if m := preloadStyleReA.FindStringSubmatch(html); m != nil {
		href = m[1]
	} else if m := preloadStyleReB.FindStringSubmatch(html); m != nil {
		href = m[1]
	}
	if strings.HasPrefix(href, "/assets/") {
		links = append(links, "<"+href+">; rel=preload; as=style")
	}
	return strings.Join(links, ", ")
}

// injectBrandingScript 将品牌定制 JSON 注入 HTML 的 </head> 之前（缺省则前置）。
func injectBrandingScript(payload map[string]interface{}, html string) string {
	b, err := json.Marshal(payload)
	if err != nil {
		return html
	}
	script := `<script id="__branding__">window.__BRANDING__=` + string(b) + `;</script>`
	if i := strings.Index(strings.ToLower(html), "</head>"); i >= 0 {
		return html[:i] + script + html[i:]
	}
	return script + html
}
