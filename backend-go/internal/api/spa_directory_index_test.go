// ============ spa_directory_index_test.go · 职责说明 ============
// 目录 URL 解析锁（internal/api/spa.go 的 handleSPA）：
//
//	背景＝2026-10-17 现网实测 `/blindtest/`（可分享静态页的自然形态）落进 SPA 兜底、
//	回 11,974 B 首页，只有写全 `/blindtest/index.html` 才拿得到那张 7,287 B 的页；
//	而 sitemap 与分享链接都不会写文件名（作战手册里它就叫「可分享的实验」）。
//	根因＝命中目录时一律 `serveIndexHTML(dist/index.html)`，从不看该目录自己的 index.html。
//
// ★ 判据一律**走守卫入口** handleSPA（AGENTS §三「判据级断言代替不了守卫级」）：
//
//	只测一个"解析函数"会漏掉"忘了接线"这一族——本批缺陷本身正是"分支条件写错"而非"函数算错"。
//
// 相：A1 目录 URL 命中该目录 index.html（且与 A2 无斜杠、A3 显式文件名**逐字节等值**）／
//
//	A4 出口仍走 serveIndexHTML 注入链（带 __BRANDING__，与显式写法同一条脸）／
//	B 反向对照：目录**没有** index.html 且非已知路由（`/assets/`）⇒ 真 404（防"把所有目录都当页面"，
//	  口径已随 G6 从"回退 SPA 首页"翻转为"未知深链 404"）／
//	C 未知路径（`/nope/xyz`）真 404（G6）／
//	D 根 `/` 仍出 root shell（主站首页字节数判据的形态不许被目录解析改掉）／
//	E 静态文件直出不受影响（`/g/x.html` 不带品牌注入）／
//	F 目录穿越不能读到 dist 之外（`/blindtest/../../../../etc/passwd` 只许回退或 404）。
//
// 单测自钉 sqlite 方言（AGENTS §一·4）：brandingPayload ⇒ getPlatformBranding 要读 store。
// 运行：env DB_DRIVER=sqlite go test -count=1 ./internal/api/ -run TestSPADirectoryIndex
// =============================================
package api

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// spaDirIndexFixture 造一棵临时 dist（root shell ＋ 一个带 index.html 的目录 ＋ 一个不带 index.html 的目录 ＋ 一个静态文件），
// 并返回装配好的 Server（Store 用于品牌注入链，Dist 指向该树）。
// 参数 t: 测试上下文。返回: 服务实例与 dist 根。
func spaDirIndexFixture(t *testing.T) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"index.html":           `<!DOCTYPE html><html><head><title>root</title></head><body><div id="root"></div>ROOT_SHELL_MARK</body></html>`,
		"blindtest/index.html": `<!DOCTYPE html><html><head><title>blind</title></head><body>BLIND_MARK</body></html>`,
		"g/x.html":             `<!DOCTYPE html><html><head><title>g</title></head><body>G_MARK</body></html>`,
	}
	for rel, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("建目录失败 %s: %v", rel, err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatalf("写入 %s 失败: %v", rel, err)
		}
	}
	// assets 目录刻意不放 index.html：它代表"目录存在但不是页面"那一档（现实里 /assets/ 就是）。
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o755); err != nil {
		t.Fatalf("建 assets 目录失败: %v", err)
	}
	return &Server{Store: siteFlagsStore(t), Dist: dir}, dir
}

// spaDirIndexGet 直接驱动 handleSPA 并取回响应（绕开 mux，专注本路由的判据）。
// 参数 t: 测试上下文；s: 服务实例；target: 请求路径。返回: 响应记录器。
func spaDirIndexGet(t *testing.T, s *Server, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	s.handleSPA(rec, httptest.NewRequest("GET", target, nil))
	return rec
}

// TestSPADirectoryIndex 目录 URL 解析守卫：命中该目录 index.html，缺失才回退 SPA。
func TestSPADirectoryIndex(t *testing.T) {
	s, _ := spaDirIndexFixture(t)

	slash := spaDirIndexGet(t, s, "/blindtest/")
	noSlash := spaDirIndexGet(t, s, "/blindtest")
	explicit := spaDirIndexGet(t, s, "/blindtest/index.html")

	// A1：干净目录 URL 必须拿到那张页——这是本批修的那一条
	if !strings.Contains(slash.Body.String(), "BLIND_MARK") {
		t.Fatalf("`/blindtest/` 没解析到该目录的 index.html（回退成了 SPA）：\n%s", slash.Body.String())
	}
	if strings.Contains(slash.Body.String(), "ROOT_SHELL_MARK") {
		t.Fatalf("`/blindtest/` 仍回 U 首页 root shell：\n%s", slash.Body.String())
	}
	// A2/A3：三种写法逐字节等值（口径只有一条脸，不许"带文件名才注入品牌"）
	if slash.Body.String() != explicit.Body.String() {
		t.Fatalf("`/blindtest/` 与 `/blindtest/index.html` 出口不等值：\n%s\n----\n%s", slash.Body.String(), explicit.Body.String())
	}
	if noSlash.Body.String() != explicit.Body.String() {
		t.Fatalf("`/blindtest`（无斜杠）与显式写法出口不等值：\n%s", noSlash.Body.String())
	}
	// A4：出口真走了 serveIndexHTML 注入链（证明接的是同一条脸，不是另开一条裸 ServeFile 旁路）
	if !strings.Contains(slash.Body.String(), "__BRANDING__") {
		t.Fatalf("目录 index.html 出口缺品牌注入 ⇒ 接的不是 serveIndexHTML：\n%s", slash.Body.String())
	}
	if ct := slash.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("目录 index.html 出口 Content-Type 不是 HTML：%q", ct)
	}

	// B 反向对照：目录存在但没有 index.html 且**不是前端已知路由** ⇒ 现在是真 404 面（G6）。
	// 旧形态会回退 SPA 首页（状态码 200）；这里点明口径已翻转为"未知深链 404"。
	assets := spaDirIndexGet(t, s, "/assets/")
	if assets.Code != 404 {
		t.Fatalf("`/assets/`（无 index.html、非已知路由）应回 404，实际 %d：\n%s", assets.Code, assets.Body.String())
	}
	if strings.Contains(assets.Body.String(), "ROOT_SHELL_MARK") || strings.Contains(assets.Body.String(), "BLIND_MARK") {
		t.Fatal("`/assets/` 404 面串到了别的页面内容（目录解析越界）")
	}

	// C 未知路径现在回真 404（G6：只有已知前端路由才回退 SPA）
	if rec := spaDirIndexGet(t, s, "/nope/xyz"); rec.Code != 404 {
		t.Fatalf("未知深链 `/nope/xyz` 应回 404，实际 %d：\n%s", rec.Code, rec.Body.String())
	}
	// D 根路径形态不许变（主站首页字节数判据依赖它）
	root := spaDirIndexGet(t, s, "/")
	if !strings.Contains(root.Body.String(), "ROOT_SHELL_MARK") || strings.Contains(root.Body.String(), "BLIND_MARK") {
		t.Fatalf("根路径出口不是 root shell：\n%s", root.Body.String())
	}
	// E 静态文件直出不受影响（这类是"文件"不是"目录"，本批分支根本不该碰它们）
	gs := spaDirIndexGet(t, s, "/g/x.html")
	if !strings.Contains(gs.Body.String(), "G_MARK") {
		t.Fatalf("静态 HTML 文件直出被目录分支吃掉：\n%s", gs.Body.String())
	}
	if strings.Contains(gs.Body.String(), "__BRANDING__") {
		t.Fatal("普通静态 HTML 被注入了品牌（只有 index.html 才走注入链）")
	}
	// F 目录穿越：Clean + 前缀校验仍在，绝不许回系统文件内容
	tr := spaDirIndexGet(t, s, "/blindtest/../../../../etc/passwd")
	if strings.Contains(tr.Body.String(), "root:") {
		t.Fatalf("目录穿越读到了系统文件：\n%s", tr.Body.String())
	}
}
