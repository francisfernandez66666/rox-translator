// ============ spa_not_found_test.go · 职责说明 ============
// G6：未知深链直出真 404（internal/api/spa.go 的 handleSPA / isKnownClientRoute / serveSPANotFound）。
//
// 背景（D7）＝旧形态对任何"磁盘上不存在"的路径一律回 index.html 且状态码 200，
// 于是「地址不存在」与「要登录」在访客眼里同形、SEO 也拿不到真实状态码。
//
// 判据全部**走守卫入口** handleSPA（AGENTS §三「判据级断言代替不了守卫级」）：
//
//	N1 已知前端路由逐条仍回退 SPA 首页（200 + root shell），一条不许漏；
//	N2 未知深链必须 404 且带 `Cache-Control: no-store`（AGENTS §一·5 批④：不发 cache-control ≠ 不缓存）；
//	N3 射程自证＝白名单必须**覆盖前端真实路由全集**（从 App.tsx / main.tsx 现读派生），
//	   前端新增路由却忘了补白名单 ⇒ 本闸当场红（"忘了接线"正是这一族缺陷本体）；
//	N4 反证＝把派生集里任一条从白名单摘掉，覆盖判据必须红（否则射程判据是空转的）。
//
// 运行：env DB_DRIVER=sqlite go test -count=1 ./internal/api/ -run TestSPA
// =============================================
package api

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// readFrontendSrc 从包目录现读前端源码（射程判据的事实源）。
// 参数 t: 测试上下文；name: frontend-react/src 下的文件名。返回: 文件原文。
func readFrontendSrc(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join("..", "..", "..", "frontend-react", "src", name)
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("读取前端源码 %s 失败（射程判据的事实源不在）：%v", p, err)
	}
	return string(b)
}

// frontendKnownRoutes 从 App.tsx 的 <Route path> 与 main.tsx 的 PUBLIC_PATHS 现读派生
// 前端已知路由全集（含 `/admin` 前缀）。`*` 通配与站内锚点不入集。
// 参数 t: 测试上下文。返回: 派生出的已知路径列表。
func frontendKnownRoutes(t *testing.T) []string {
	t.Helper()
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		if p == "" || p == "*" || strings.Contains(p, ":") || seen[p] {
			return
		}
		seen[p] = true
		out = append(out, p)
	}

	app := readFrontendSrc(t, "App.tsx")
	for _, m := range regexp.MustCompile(`<Route\s+path="([^"]+)"`).FindAllStringSubmatch(app, -1) {
		add(m[1])
	}
	main := readFrontendSrc(t, "main.tsx")
	for _, m := range regexp.MustCompile(`'(/[A-Za-z0-9_/-]*)'`).FindAllStringSubmatch(
		between(main, "PUBLIC_PATHS = new Set([", "])"), -1) {
		add(m[1])
	}
	// /admin 是 prefix 语义（`path.startsWith('/admin')`），在 App.tsx 里非 <Route> 声明。
	add("/admin")
	return out
}

// between 取 s 中 first 与 last 之间的子串（测试辅助；找不到返回空串）。
func between(s, first, last string) string {
	i := strings.Index(s, first)
	if i < 0 {
		return ""
	}
	s = s[i+len(first):]
	j := strings.Index(s, last)
	if j < 0 {
		return ""
	}
	return s[:j]
}

// TestSPAKnownRoutesServeSPA N1：前端已知路由逐条仍回退 SPA 首页。
func TestSPAKnownRoutesServeSPA(t *testing.T) {
	s, _ := spaDirIndexFixture(t)
	routes := frontendKnownRoutes(t)
	// 射程自证：派生集本身必须够多，否则"全绿"可能只是集合为空。
	if len(routes) < 12 {
		t.Fatalf("射程自证失败：从前端只派生到 %d 条路由（<12），射程判据已失明：%v", len(routes), routes)
	}
	for _, p := range routes {
		rec := spaDirIndexGet(t, s, p)
		if rec.Code != 200 {
			t.Errorf("已知路由 %q 应回退 SPA 首页(200)，实际 %d", p, rec.Code)
			continue
		}
		if !strings.Contains(rec.Body.String(), "ROOT_SHELL_MARK") {
			t.Errorf("已知路由 %q 没有回退到 SPA 首页内容", p)
		}
	}
}

// TestSPAUnknownRoutesNotFound N2：未知深链 404 + no-store。
func TestSPAUnknownRoutesNotFound(t *testing.T) {
	s, _ := spaDirIndexFixture(t)
	for _, p := range []string{"/nope", "/nope/xyz", "/adminx", "/adm", "/login/extra", "/tickets/42/edit", "/user/1234"} {
		rec := spaDirIndexGet(t, s, p)
		if rec.Code != 404 {
			t.Errorf("未知深链 %q 应回 404，实际 %d", p, rec.Code)
			continue
		}
		if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "no-store") {
			t.Errorf("未知深链 %q 的 404 响应必须带 no-store（防 CDN 把 404 钉住），实际 %q", p, cc)
		}
		if strings.Contains(rec.Body.String(), "ROOT_SHELL_MARK") {
			t.Errorf("未知深链 %q 的 404 面串到了 SPA 首页内容", p)
		}
	}
	// /admin 前缀（含子路径）必须仍放行——它是已知路由，不属于 404 那一档
	for _, p := range []string{"/admin", "/admin/users"} {
		if rec := spaDirIndexGet(t, s, p); rec.Code != 200 || !strings.Contains(rec.Body.String(), "ROOT_SHELL_MARK") {
			t.Errorf("已知前缀路由 %q 被误判为未知（%d）", p, rec.Code)
		}
	}
}

// TestSPAClientRouteCoverage N3+N4：白名单覆盖前端真实路由全集，且覆盖判据有齿。
func TestSPAClientRouteCoverage(t *testing.T) {
	routes := frontendKnownRoutes(t)
	for _, p := range routes {
		if !isKnownClientRoute(p) {
			t.Errorf("前端路由 %q 未登记进 spaClientExactRoutes/spaClientRoutePrefixes ⇒ 其深链会被后端直出 404（漏接线）", p)
		}
	}
	// N4 反证：真把一条精确路由从白名单摘掉，覆盖判据必须红（证明 N3 的射程来源是那张表，不是恒真）。
	target := ""
	for _, p := range routes {
		if p != "/admin" {
			target = p
			break
		}
	}
	if target == "" {
		t.Fatal("无法选到用于反证的精确路由")
	}
	orig := spaClientExactRoutes[target]
	delete(spaClientExactRoutes, target)
	defer func() { spaClientExactRoutes[target] = orig }()
	caught := false
	for _, p := range routes {
		if !isKnownClientRoute(p) {
			caught = true
		}
	}
	if !caught {
		t.Fatalf("反证失效：从白名单摘掉 %q 后覆盖判据仍全绿", target)
	}
}
