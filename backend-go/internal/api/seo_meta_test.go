// ============ 本文件职责中文说明 ============
// DEF-06（2026-10-24）服务端 SEO 注入单测：12 语种解析、路由 meta 覆盖、
// title/canonical/robots/hreflang 注入、app 路由 noindex、与前端单一事实源同步。
// og:image 一族（2026-10-25 收口批）另有两条锁：
//
//	· TestInjectRouteMetaOGImage——出栈形态（平台域绝对 URL 且去重、白标域整族摘掉）；
//	· TestSEOOGImageAssetMatchesInjectedPath——跨语言同源（文件真在、IHDR 尺寸与注入值等值、
//	  TS 那份路径/尺寸/平台品牌名与 Go 常量逐字相同）。
//
// ============================================
package api

import (
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
)

const seoSampleHTML = `<!doctype html><html><head><title>old</title>` +
	`<meta name="description" content="old"><link rel="canonical" href="https://example.com/">` +
	`<meta property="og:url" content="https://example.com/"><meta property="og:title" content="old">` +
	`<meta property="og:description" content="old"></head><body><div id="root"></div></body></html>`

func TestSEOLangFromRequest(t *testing.T) {
	cases := []struct {
		query, app, accept, want string
	}{
		{"ja", "", "", "ja"},
		{"", "ko", "en-US,en;q=0.9", "ko"},
		{"", "", "zh-TW,zh;q=0.9", "zh_hant"},
		{"", "", "fr-CA,fr;q=0.9", "fr"},
		{"", "", "de-DE;q=0", "zh"},
		{"xxx", "", "th,en;q=0.9", "th"},
	}
	for _, c := range cases {
		r := httptest.NewRequest("GET", "/?lang="+c.query, nil)
		if c.app != "" {
			r.Header.Set("X-App-Lang", c.app)
		}
		if c.accept != "" {
			r.Header.Set("Accept-Language", c.accept)
		}
		if got := seoLangFromRequest(r); got != c.want {
			t.Errorf("query=%q app=%q accept=%q = %q, want %q", c.query, c.app, c.accept, got, c.want)
		}
	}
}

func TestSEOMetaForPath(t *testing.T) {
	if m, isApp, ok := seoMetaForPath("/pricing"); !ok || isApp || m.Robots != "index,follow" {
		t.Fatalf("pricing meta 错: %+v isApp=%v ok=%v", m, isApp, ok)
	}
	if m, isApp, ok := seoMetaForPath("/tickets"); !ok || !isApp || m.Robots != "noindex,nofollow" {
		t.Fatalf("app 路由应 noindex: %+v isApp=%v ok=%v", m, isApp, ok)
	}
	if _, _, ok := seoMetaForPath("/blindtest/"); ok {
		t.Fatal("静态目录页不应注入 route meta")
	}
}

func TestInjectRouteMeta(t *testing.T) {
	meta, isApp, ok := seoMetaForPath("/pricing")
	if !ok || isApp {
		t.Fatal("pricing meta 不存在")
	}
	out := injectRouteMeta(seoSampleHTML, meta, "en", "LangCross", "https", "example.com", "/pricing", false)
	for _, want := range []string{
		"<title>Pricing &amp; Plans · LangCross</title>",
		`rel="canonical" href="https://example.com/pricing?lang=en"`,
		`name="robots" content="index,follow"`,
		`hreflang="ja" href="https://example.com/pricing?lang=ja"`,
		`hreflang="x-default" href="https://example.com/pricing"`,
		`property="og:url" content="https://example.com/pricing?lang=en"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("注入结果缺 %q\n%s", want, out)
		}
	}
	// 旧 tags 必须被替换而不是重复
	if strings.Count(out, "<title>") != 1 {
		t.Fatalf("title 重复: %s", out)
	}
	if strings.Count(out, `rel="canonical"`) != 1 {
		t.Fatalf("canonical 重复: %s", out)
	}
	// 12 条 hreflang + x-default
	if n := strings.Count(out, `hreflang="`); n != 13 {
		t.Fatalf("hreflang 数量应为 13（12 语种+x-default），实得 %d", n)
	}
}

// TestInjectRouteMetaOGImage ★ 方案 §2.2「og:image，分享时不裸奔」在服务端注入这一面的锁。
// 三条判据各抓一种现网形态：
//
//	① 平台品牌域必须有**绝对 URL** 的 og:image + twitter:image（相对路径社交爬虫一律不解，等于没有卡图）；
//	② 基线 index.html 里那一份硬编码域名的卡图必须被**换掉**而不是并存两份
//	   （主站/演示站/租户域共用一份 dist，两份 og:image 时抓取方取第一条＝演示站分享出去指向主站）；
//	③ 白标（租户品牌域）必须**整族摘掉**——卡面写着「能言 LangCross」，
//	   客户转发自己域名的链接却给平台做广告，与上面「标题里的品牌名要换成租户名」是同一件事的两半。
func TestInjectRouteMetaOGImage(t *testing.T) {
	meta, isApp, ok := seoMetaForPath("/compare")
	if !ok || isApp {
		t.Fatal("compare meta 不存在")
	}
	// 基线里故意带着「别的域名」的卡图族：真实 dist 的 index.html 就写着主站的绝对 URL。
	base := `<!doctype html><html><head><title>old</title>` +
		`<meta property="og:image" content="https://other.example/og-image.png">` +
		`<meta property="og:image:width" content="1200"><meta property="og:image:height" content="630">` +
		`<meta name="twitter:card" content="summary_large_image">` +
		`<meta name="twitter:image" content="https://other.example/og-image.png">` +
		`</head><body><div id="root"></div></body></html>`

	// ①＋② 平台品牌：绝对 URL 指向当前请求的 scheme/host，且旧的那份不残留
	plat := injectRouteMeta(base, meta, "en", seoPlatformBrand, "https", "langcross.example.com", "/compare", false)
	for _, want := range []string{
		`<meta property="og:image" content="https://langcross.example.com/og-image.png">`,
		`<meta name="twitter:image" content="https://langcross.example.com/og-image.png">`,
		`<meta property="og:image:width" content="1200">`,
		`<meta property="og:image:height" content="630">`,
		`<meta name="twitter:card" content="summary_large_image">`,
	} {
		if !strings.Contains(plat, want) {
			t.Errorf("平台品牌注入缺 %q\n%s", want, plat)
		}
	}
	if strings.Contains(plat, "other.example") {
		t.Errorf("基线卡图没被替换，残留旧域名:\n%s", plat)
	}
	for _, tag := range []string{`property="og:image"`, `name="twitter:image"`, `property="og:image:width"`, `property="og:image:height"`, `name="twitter:card"`} {
		if n := strings.Count(plat, tag); n != 1 {
			t.Errorf("%s 应恰好 1 条，实得 %d（重复的 og:image 抓取方只认第一条）:\n%s", tag, n, plat)
		}
	}

	// ②' 基线**没有**卡图族时也要补上（后端直出/历史 dist 都可能缺，缺了就是"分享裸奔"）
	noImg := `<!doctype html><html><head><title>old</title></head><body></body></html>`
	inserted := injectRouteMeta(noImg, meta, "zh", seoPlatformBrand, "https", "langcross.example.com", "/compare", false)
	if !strings.Contains(inserted, `property="og:image" content="https://langcross.example.com/og-image.png"`) {
		t.Errorf("基线缺卡图时应插入，实得:\n%s", inserted)
	}

	// ③ 白标：整族摘掉，不许留任何一枚指向平台卡图的标签
	wl := injectRouteMeta(base, meta, "en", "Acme 译通", "https", "acme.example.com", "/compare", false)
	for _, banned := range []string{"og-image.png", `property="og:image"`, `name="twitter:image"`, `property="og:image:width"`, `property="og:image:height"`} {
		if strings.Contains(wl, banned) {
			t.Errorf("白标面不许出现平台分享卡 %q:\n%s", banned, wl)
		}
	}
	if !strings.Contains(wl, `<meta name="twitter:card" content="summary">`) {
		t.Errorf("白标面 twitter 卡档应回落小图（大卡图无图＝破图占位）:\n%s", wl)
	}
	if strings.Count(wl, `name="twitter:card"`) != 1 {
		t.Errorf("twitter:card 应恰好 1 条，实得 %d", strings.Count(wl, `name="twitter:card"`))
	}

	// 反证 R1：把「摘掉基线卡图」那一腿摘掉（只插不摘）必须被上面的判据点名。
	// 这里不复用产品函数，而是在测试里重现"只插不摘"的形态，验证去重/换域名两腿真有射程。
	dupOnly := strings.Replace(base, "</head>", `<meta property="og:image" content="https://langcross.example.com/og-image.png"></head>`, 1)
	if n := strings.Count(dupOnly, `property="og:image"`); n != 2 {
		t.Fatalf("反证夹具形状不对：变异体必须含两条 og:image，实得 %d", n)
	}
	if !strings.Contains(dupOnly, "other.example") {
		t.Fatal("反证夹具形状不对：变异体必须留着基线那条旧域名，否则抓不到「没替换」这一族")
	}
	if strings.Count(dupOnly, `property="og:image"`) == strings.Count(plat, `property="og:image"`) {
		t.Errorf("反证失效：产品输出与「只插不摘」形态的 og:image 条数相同，说明去重腿没跑")
	}
}

func TestInjectAppRouteNoindex(t *testing.T) {
	meta, isApp, ok := seoMetaForPath("/tickets")
	if !ok || !isApp {
		t.Fatal("app meta 不存在")
	}
	out := injectRouteMeta(seoSampleHTML, meta, "zh", "能言 LangCross", "https", "example.com", "/tickets", true)
	if !strings.Contains(out, `name="robots" content="noindex,nofollow"`) {
		t.Fatalf("app 路由应 noindex: %s", out)
	}
	if strings.Contains(out, `hreflang="`) {
		t.Fatalf("app 路由不应输出 hreflang: %s", out)
	}
}

func TestSEOMetaJSONMatchesFrontendSource(t *testing.T) {
	// 后端 //go:embed 的是包内副本；源在 frontend-react/src/seo/route_meta.json。
	// 两份必须逐字节一致，否则「前端 12 语种」与「服务端 12 语种」会各长一套。
	src, err := os.ReadFile("../../../frontend-react/src/seo/route_meta.json")
	if err != nil {
		t.Fatalf("读前端 route_meta.json 失败: %v", err)
	}
	dst, err := os.ReadFile("seo_meta.json")
	if err != nil {
		t.Fatalf("读后端 seo_meta.json 失败: %v", err)
	}
	if string(src) != string(dst) {
		t.Fatal("前后端 SEO meta 不同步：跑 bash scripts/sync_seo_meta.sh")
	}
	loadSEOMeta()
	if !seoMetaOK {
		t.Fatal("seo_meta.json 解析失败")
	}
	for _, path := range []string{"/", "/pricing", "/compare", "/login", "/register"} {
		m, ok := seoMetaDocData.Routes[path]
		if !ok {
			t.Fatalf("缺少路由 %s", path)
		}
		for _, lang := range seoMetaDocData.Languages {
			if strings.TrimSpace(m.Title[lang]) == "" {
				t.Errorf("%s 缺 %s title", path, lang)
			}
			if strings.TrimSpace(m.Description[lang]) == "" {
				t.Errorf("%s 缺 %s description", path, lang)
			}
		}
	}
	// ★ 2026-10-24 发版复验补腿：五个公开路由的 title 在**同一语种内必须互不相同**。
	// 现网实测 /register 曾直接抄了 /login 的「Sign in · LangCross」——上面的"非空"判据
	// 对它完全无感（字面在、只是抄错对象），所以这条按"同语种去重"判。
	for _, lang := range seoMetaDocData.Languages {
		seen := map[string]string{}
		for _, path := range []string{"/", "/pricing", "/compare", "/login", "/register"} {
			title := strings.TrimSpace(seoMetaDocData.Routes[path].Title[lang])
			if prev, dup := seen[title]; dup {
				t.Errorf("%s 语种的 title 重复：%s 与 %s 完全相同（%q）——每路由必须各有一句", lang, prev, path, title)
			}
			seen[title] = path
		}
	}
}

// TestSEOOGImageAssetMatchesInjectedPath ★ 跨语言同源锁（2026-10-25 og:image 收口批）。
//
// 卡图这条链最坏的静默形态是「meta 标签写得漂漂亮亮，指过去是 404」：
// 页面照常渲染、社交卡片只是没图，界面与日志零症状，只有转发出去的人才看见。
// 而路径与尺寸此刻住在三个地方（Python 生成器 / Go 注入 / TS 客户端同步），任一侧改名或改尺寸
// 都会让另外两侧继续指向一个不存在（或尺寸不符）的文件。所以这里一次问三件事：
//
//	① 磁盘上那个文件真在（public/og-image.png，随 vite build 进 dist）；
//	② 那文件的 IHDR 宽高与注入用的 og:image:width/height 逐字等值；
//	③ 前端 routeMeta.ts 里的路径常量与 Go 常量字面等值（同 route_meta.json 那份逐字节锁的写法）。
func TestSEOOGImageAssetMatchesInjectedPath(t *testing.T) {
	file := "../../../frontend-react/public" + seoOGImagePath
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("分享卡文件没落盘：%s（注入出去的 og:image 全指向 404）%v", file, err)
	}
	// PNG 头：8 字节签名 + IHDR 块（4 长度 + 4 类型 + 4 宽 + 4 高）
	if len(raw) < 24 || string(raw[12:16]) != "IHDR" {
		t.Fatalf("%s 不是 PNG（IHDR 缺失），社交抓取方会当它不存在", file)
	}
	w := int(raw[16])<<24 | int(raw[17])<<16 | int(raw[18])<<8 | int(raw[19])
	h := int(raw[20])<<24 | int(raw[21])<<16 | int(raw[22])<<8 | int(raw[23])
	if got := strconv.Itoa(w); got != seoOGImageW {
		t.Errorf("卡图实际宽 %d 与注入的 og:image:width=%s 不符（改尺寸必须三处一起改）", w, seoOGImageW)
	}
	if got := strconv.Itoa(h); got != seoOGImageH {
		t.Errorf("卡图实际高 %d 与注入的 og:image:height=%s 不符", h, seoOGImageH)
	}
	// 前端那份路径常量必须与 Go 同值：改名只可能发生在其中一侧
	ts, err := os.ReadFile("../../../frontend-react/src/seo/routeMeta.ts")
	if err != nil {
		t.Fatalf("读前端 routeMeta.ts 失败: %v", err)
	}
	if want := "const OG_IMAGE_PATH = '" + seoOGImagePath + "'"; !strings.Contains(string(ts), want) {
		t.Errorf("前端 routeMeta.ts 的卡图路径与 Go 常量不同源（期望 %q）——两侧会各指一个文件", want)
	}
	for _, want := range []string{"const OG_IMAGE_W = '" + seoOGImageW + "'", "const OG_IMAGE_H = '" + seoOGImageH + "'"} {
		if !strings.Contains(string(ts), want) {
			t.Errorf("前端 routeMeta.ts 缺 %q（尺寸两侧打架）", want)
		}
	}
	// 平台品牌名也是跨语言常量：TS 侧那份必须与 Go 的 seoPlatformBrand 逐字同值，
	// 否则「白标摘卡图」这一腿在两侧判的是不同的字符串。
	if want := "const PLATFORM_BRAND = '" + seoPlatformBrand + "'"; !strings.Contains(string(ts), want) {
		t.Errorf("前端 routeMeta.ts 缺 %q（白标判定两侧不同尺）", want)
	}
}

// TestSEORouteMetaEnabledTiers 注入开关的三档读点（env > system_config > 默认开）。
// 这一条测的是「谁能关掉注入、以什么顺序关掉」：
// 只测 env 档会漏掉「运营在库里配了 0 但代码只读 env」，只测库档会漏掉「env 急救压不过库」，
// 两者都是现网排查「标题怎么又变回一句」时要第一眼看的读数。
func TestSEORouteMetaEnabledTiers(t *testing.T) {
	t.Setenv("SEO_ROUTE_META", "")
	st := siteFlagsStore(t)
	s := &Server{Store: st}

	// ① 三档都没表态 ⇒ 默认开（缺配置不该退回"全站一句标题"）
	if !s.seoRouteMetaEnabled() {
		t.Fatal("默认档必须开注入（库里没这个键＝特性存在）")
	}
	// ② 库档配 0 ⇒ 关；库里写空串＝撤销态，回到默认开
	for _, off := range []string{"0", "false", "off", "NO", " 0 "} {
		if err := st.SetConfig(configSEORouteMeta, off); err != nil {
			t.Fatalf("写库档失败: %v", err)
		}
		if s.seoRouteMetaEnabled() {
			t.Errorf("库档 %q 应关闭注入，实得开", off)
		}
	}
	if err := st.SetConfig(configSEORouteMeta, ""); err != nil {
		t.Fatalf("清库档失败: %v", err)
	}
	// ③ 脏值不表态：打错一个字母不该把整站内页的 title/canonical 关掉
	for _, dirty := range []string{"maybe", "2", "-1", "of"} {
		if err := st.SetConfig(configSEORouteMeta, dirty); err != nil {
			t.Fatalf("写脏值失败: %v", err)
		}
		if !s.seoRouteMetaEnabled() {
			t.Errorf("库档脏值 %q 不许被读成关闭（非法值回落默认档）", dirty)
		}
	}
	if err := st.SetConfig(configSEORouteMeta, ""); err != nil {
		t.Fatalf("清库档失败: %v", err)
	}
	// ④ env 档优先于库档：运维急救那一行必须压得住库里的配置
	if err := st.SetConfig(configSEORouteMeta, "0"); err != nil {
		t.Fatalf("写库档失败: %v", err)
	}
	t.Setenv("SEO_ROUTE_META", "1")
	if !s.seoRouteMetaEnabled() {
		t.Error("env=1 必须压过库档 0（环境变量 > 数据库配置，AGENTS §一·3）")
	}
	t.Setenv("SEO_ROUTE_META", "0")
	if s.seoRouteMetaEnabled() {
		t.Error("env=0 必须关闭注入（《部署指南》回滚表那一行的读点）")
	}
	// ⑤ env 脏值不表态 ⇒ 落到库档那一档（这里库档是 0 ⇒ 关）
	t.Setenv("SEO_ROUTE_META", "banana")
	if s.seoRouteMetaEnabled() {
		t.Error("env 脏值不许冒充表态把库档挤掉")
	}
	// ⑥ 没有 Store（纯函数面/单测直调）⇒ 只由 env 与嵌入可用性决定，不许 panic
	t.Setenv("SEO_ROUTE_META", "")
	if !(&Server{}).seoRouteMetaEnabled() {
		t.Error("Store 为 nil 时按默认档放行注入（探针自己坏掉不该摘产品入口）")
	}
}

// TestSEORouteMetaServedHTMLFollowsSwitch 接线锁：开关真的作用在 serveIndexHTML 出口上。
// 只测判定函数会漏掉「函数改对了但 spa.go 还在调旧的包级函数」——现网表现是
// 「库里配了 0，首页注入照旧」，而单测全绿。
func TestSEORouteMetaServedHTMLFollowsSwitch(t *testing.T) {
	t.Setenv("SEO_ROUTE_META", "")
	st := siteFlagsStore(t)
	if err := st.SetConfig(configSEORouteMeta, "0"); err != nil {
		t.Fatalf("写库档失败: %v", err)
	}
	dir := t.TempDir()
	if err := writeFileForSiteFlags(dir+"/index.html", seoSampleHTML); err != nil {
		t.Fatalf("写入临时 index.html 失败: %v", err)
	}
	s := &Server{Store: st, Dist: dir}
	r := httptest.NewRequest("GET", "/pricing", nil)
	off := serveIndexCapture(t, s, r, dir+"/index.html")
	if strings.Contains(off, `rel="alternate" hreflang=`) {
		t.Fatal("库档关闭后仍注入了 hreflang ⇒ spa.go 没走开关（或开关读的不是库档）")
	}
	if err := st.SetConfig(configSEORouteMeta, "1"); err != nil {
		t.Fatalf("写库档失败: %v", err)
	}
	// 库配置缓存（默认 5s TTL）会吃掉"改完即生效"，测试按同一 kill-switch 口径钉成现查
	// （同 AGENTS §一·11 那条「夹具的写完即读到必须钉 kill-switch，不许 sleep」）。
	t.Setenv("SYSTEM_CONFIG_CACHE_TTL_SEC", "0")
	on := serveIndexCapture(t, s, httptest.NewRequest("GET", "/pricing", nil), dir+"/index.html")
	if !strings.Contains(on, `rel="canonical" href="https://example.com/pricing?lang=`) {
		t.Fatalf("库档打开后应恢复注入，实得：%s", on)
	}
}

// TestSEOMetaRouteTitleDistinctnessDetectsCopy 反证：/register 的 title 被抄回 /login 必须判红。
func TestSEOMetaRouteTitleDistinctnessDetectsCopy(t *testing.T) {
	loadSEOMeta()
	if !seoMetaOK {
		t.Fatal("seo_meta.json 解析失败")
	}
	r := seoMetaDocData.Routes["/register"]
	l := seoMetaDocData.Routes["/login"]
	if len(r.Title) == 0 || len(l.Title) == 0 {
		t.Fatal("路由 title 映射为空，射程自证失败")
	}
	for _, lang := range seoMetaDocData.Languages {
		if strings.TrimSpace(r.Title[lang]) == strings.TrimSpace(l.Title[lang]) {
			t.Fatalf("反证失败：%s 语种 /register 与 /login 的 title 相同（%q）——抄回去的形态已复活", lang, r.Title[lang])
		}
	}
}
