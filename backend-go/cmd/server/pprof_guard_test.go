// ============================================================================
// pprof_guard_test.go — ★ D-8 pprof 启动期闸门与 token 中间件的四态断言（2026-09-29）
// 覆盖面即「非回环必须有鉴权」这条判据的两向：
//
//	① 该拒的必须拒（0.0.0.0 / 空 host 的 ":端口" 这两种对外可达写法、且无 token）；
//	② 该放的必须放（off 关闭态、空值回落默认回环、127.x/::1 回环、非回环但带 token）——
//	   ② 里少一条就意味着把本机排障这条路也堵了，运维的下一步动作一定是直接关掉 pprof，
//	   那比漏洞更难发现（历史上「安全闸门顺手堵死开发路径 → 被整条绕过」不止一次）。
//
// 中间件同样双向：无凭据 401、带 header 200、带 query 200、错 token 401、空 token 直通。
// ⚠️ 地址一律用字面 IP：isLoopbackListen 对主机名会走 net.LookupHost，
//
//	离线 CI 里 "localhost" 的解析结果不可控，拿它做断言等于把闸门绑在 DNS 上。
//
// ============================================================================
package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestPPROFGuardRefusesExposedWithoutToken ①：对外可达且无 token ⇒ 必须给出拒绝理由。
func TestPPROFGuardRefusesExposedWithoutToken(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:18787", ":18787", "10.0.3.7:18787"} {
		err := pprofGuard(addr, "")
		if err == nil {
			t.Errorf("PPROF_ADDR=%q 无 token 却放行 ⇒ /debug/pprof 对外裸奔（内存/协程栈泄漏 + 可被打满 CPU）", addr)
			continue
		}
		// 拒绝理由必须「说实话」：点名是非回环导致的，而不是含糊一句配置错误
		if !strings.Contains(err.Error(), "非回环") {
			t.Errorf("PPROF_ADDR=%q 的拒绝理由没点明根因：%v", addr, err)
		}
	}
}

// TestPPROFGuardAllowsLegitShapes ②：四种合法形态一律不得拒（拒了就是把排障路径堵死）。
func TestPPROFGuardAllowsLegitShapes(t *testing.T) {
	cases := []struct {
		name  string
		addr  string
		token string
	}{
		{"关闭态 off", "off", ""},
		{"关闭态大写 OFF（与启动判定同为 EqualFold）", "OFF", ""},
		{"空值＝调用方回落默认回环", "", ""},
		{"回环 IPv4", "127.0.0.1:18787", ""},
		{"回环段内其它地址（127.0.0.53 仍是 loopback /8）", "127.0.0.53:19000", ""},
		{"回环 IPv6", "[::1]:18787", ""},
		{"非回环但带了 token", "0.0.0.0:18787", "s3cr3t"},
	}
	for _, c := range cases {
		if err := pprofGuard(c.addr, c.token); err != nil {
			t.Errorf("%s（addr=%q token=%q）不该被拒: %v", c.name, c.addr, c.token, err)
		}
	}
}

// TestPPROFGuardRejectsBlankToken 反证侧：非回环配一个「空到等于没配」的 token 不能算有防护。
// （运维真踩过的写法：PPROF_TOKEN=" " —— 若按 len!=0 判就放行了，中间件又会拿空格比对口径。）
func TestPPROFGuardRejectsBlankToken(t *testing.T) {
	if err := pprofGuard("0.0.0.0:18787", "   "); err == nil {
		t.Error("空白 token 视为已配置 ⇒ 非回环裸奔，判据必须按 TrimSpace 后是否为空")
	}
}

// TestPPROFAuthMiddleware 中间件四态：直通 / 无凭据 401 / header 200 / query 200 / 错值 401。
func TestPPROFAuthMiddleware(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("heap"))
	})

	// 空 token（回环零配置档）必须逐字等于本批改动前的行为：任何请求都直通
	if got := doPPROFRequest(pprofAuth("", inner), nil, ""); got != http.StatusOK {
		t.Errorf("空 token 时应当直通（本机排障不许加门槛），实际 %d", got)
	}

	h := pprofAuth("s3cr3t", inner)
	if got := doPPROFRequest(h, nil, ""); got != http.StatusUnauthorized {
		t.Errorf("无凭据请求应 401，实际 %d", got)
	}
	if got := doPPROFRequest(h, http.Header{"X-Pprof-Token": []string{"s3cr3t"}}, ""); got != http.StatusOK {
		t.Errorf("header 带对 token 应 200，实际 %d", got)
	}
	if got := doPPROFRequest(h, nil, "token=s3cr3t"); got != http.StatusOK {
		t.Errorf("query 带对 token 应 200（curl 抓 profile 的常用带法），实际 %d", got)
	}
	if got := doPPROFRequest(h, http.Header{"X-Pprof-Token": []string{"wrong"}}, ""); got != http.StatusUnauthorized {
		t.Errorf("错 token 必须 401（否则这层等于装饰），实际 %d", got)
	}
	// 401 里不许回显期望值：泄漏一个字节都比多写一条日志严重
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/debug/pprof/heap", nil)
	h.ServeHTTP(rec, req)
	if strings.Contains(rec.Body.String(), "s3cr3t") {
		t.Errorf("401 响应体回显了期望 token：%s", rec.Body.String())
	}
}

// doPPROFRequest 发一次内存请求（不真起端口），返回状态码。
func doPPROFRequest(h http.Handler, header http.Header, query string) int {
	rec := httptest.NewRecorder()
	target := "/debug/pprof/heap"
	if query != "" {
		target += "?" + query
	}
	req := httptest.NewRequest(http.MethodGet, target, nil)
	for k, vs := range header {
		for _, v := range vs {
			req.Header.Set(k, v)
		}
	}
	h.ServeHTTP(rec, req)
	return rec.Code
}
