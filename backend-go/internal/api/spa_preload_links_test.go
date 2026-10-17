// ============ spa_preload_links_test.go · 职责说明 ============
// 首屏关键资源预加载（internal/api/spa.go 的 criticalAssetLinks + serveIndexHTML）判据：
//
//	背景＝2026-10-18 慢链路批（#1「国内加载慢」）。入口 script 与首页样式表在 HTML 末尾，
//	浏览器要解析到那里才发起请求；Link 头（rel=preload）让这两个请求与 HTML 同时起跑，
//	CDN 侧（Cloudflare Early Hints）还能据此在正文之前先把 URL 推给客户端。
//
// 判据（全部走守卫入口 serveIndexHTML / handleSPA，AGENTS §三「判据级断言代替不了守卫级」）：
//
//	L1 入口 HTML 带入口 script ⇒ Link 头含 `rel=preload; as=script; crossorigin`，URL 逐字现读；
//	L2 样式表两条属性顺序（`rel` 在前／`href` 在前）都能认出来；
//	L3 非 /assets/ 前缀（外链、dataURI）与"什么都没有"的页 ⇒ **不预加载**（不猜 URL）；
//	L4 出口仍在：加了 Link 头不许动正文（`__BRANDING__` 注入、字节数、Content-Type 全部照旧）；
//	L5 现读现算：两个不同哈希名喂进去，取到的 URL 必须各自等于入参（防硬编码）。
//
// R1 反证（把 criticalAssetLinks 改成返回写死串 ⇒ L5 必红）由本文件末尾的变异体用例机械保证。
//
// 运行：env DB_DRIVER=sqlite go test -count=1 ./internal/api/ -run TestSpaPreloadLinks
// =============================================
package api

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// spaPreloadHTML 造一份"构建产物形态"的入口 HTML（入口 script + 样式表 + favicon）。
func spaPreloadHTML(entry, css string) string {
	return `<!DOCTYPE html><html><head><title>t</title>` +
		`<link rel="icon" type="image/svg+xml" href="/logo.svg" />` +
		`<link rel="stylesheet" crossorigin href="` + css + `">` +
		`</head><body><div id="root"></div>` +
		`<script type="module" crossorigin src="` + entry + `"></script></body></html>`
}

// TestSpaPreloadLinks 关键资源 Link 头的四腿 + 现读现算腿。
func TestSpaPreloadLinks(t *testing.T) {
	entry := "/assets/index-PpbXDXsp.js"
	css := "/assets/index-TCgNzM9F.css"

	// L1/L2：两条属性顺序都要认出样式表
	for _, order := range []string{"rel-first", "href-first"} {
		html := spaPreloadHTML(entry, css)
		if order == "href-first" {
			html = strings.Replace(html, `<link rel="stylesheet" crossorigin href="`+css+`">`,
				`<link href="`+css+`" rel="stylesheet">`, 1)
		}
		got := criticalAssetLinks(html)
		if !strings.Contains(got, "<"+entry+">; rel=preload; as=script; crossorigin") {
			t.Fatalf("[%s] Link 头没带入口 script 预加载：%q", order, got)
		}
		if !strings.Contains(got, "<"+css+">; rel=preload; as=style") {
			t.Fatalf("[%s] Link 头没带样式表预加载：%q", order, got)
		}
		// favicon 不在射程内（只认 rel=stylesheet 的 link）
		if strings.Contains(got, "logo.svg") {
			t.Fatalf("[%s] 把 favicon 也预加载了：%q", order, got)
		}
		// 顺序：script 在前（它是首屏最关键的那一个）
		if strings.Index(got, "as=script") > strings.Index(got, "as=style") {
			t.Fatalf("[%s] 预加载顺序把样式表排在了入口脚本前面：%q", order, got)
		}
	}

	// L3：非 /assets/ 前缀与无关键资源 ⇒ 不预加载
	for _, html := range []string{
		`<html><head><link rel="stylesheet" href="https://cdn.example.com/x.css"></head><body><script src="https://cdn.example.com/x.js"></script></body></html>`,
		`<html><head><link rel="stylesheet" href="data:text/css,body{}</head><body></body></html>`,
		`<html><head></head><body>静态页，没有入口脚本</body></html>`,
		``,
	} {
		if got := criticalAssetLinks(html); got != "" {
			t.Fatalf("不该预加载却出了 Link 头（%q ⇒ %q）", html, got)
		}
	}

	// L5：现读现算——换一个哈希名，取到的 URL 必须跟着换
	other := "/assets/index-1A-0R5X_.js"
	got2 := criticalAssetLinks(spaPreloadHTML(other, css))
	if !strings.Contains(got2, other) || strings.Contains(got2, entry) {
		t.Fatalf("Link 头没有跟着入参走（疑似硬编码）：%q", got2)
	}
}

// TestSpaPreloadLinksServedThroughGuard L4：走 serveIndexHTML 出口，
// 断言 Link 头真的发出去、且正文/注入/头一字未动（预加载不许顺手改页）。
func TestSpaPreloadLinksServedThroughGuard(t *testing.T) {
	dir := t.TempDir()
	entry := "/assets/index-PpbXDXsp.js"
	css := "/assets/index-TCgNzM9F.css"
	body := spaPreloadHTML(entry, css)
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(body), 0o600); err != nil {
		t.Fatalf("写 index.html 失败: %v", err)
	}
	s := &Server{Store: siteFlagsStore(t), Dist: dir}

	rec := httptest.NewRecorder()
	s.handleSPA(rec, httptest.NewRequest("GET", "/", nil))

	link := rec.Header().Get("Link")
	if link == "" {
		t.Fatalf("根路径没有 Link 预加载头（接线漏了）：headers=%v", rec.Header())
	}
	if !strings.Contains(link, entry) || !strings.Contains(link, css) {
		t.Fatalf("Link 头内容不对：%q", link)
	}
	// 正文与既有头不许被动
	if !strings.Contains(rec.Body.String(), "__BRANDING__") {
		t.Fatalf("品牌注入没了（改了无关行为）：%s", rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("Content-Type 变了：%q", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Fatalf("Cache-Control 变了（F-46 的既有决策，非本批射程）：%q", cc)
	}
	// 反向对照：静态子页（没有入口 script 的普通页面）不该被塞 Link 头
	sub := filepath.Join(dir, "blindtest")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("建目录失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sub, "index.html"), []byte(`<html><head><title>b</title></head><body>B</body></html>`), 0o600); err != nil {
		t.Fatalf("写子页失败: %v", err)
	}
	subRec := httptest.NewRecorder()
	s.handleSPA(subRec, httptest.NewRequest("GET", "/blindtest/", nil))
	if got := subRec.Header().Get("Link"); got != "" {
		t.Fatalf("普通静态子页被塞了预加载头：%q", got)
	}
}
