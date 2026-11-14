// ============ assist_route_compat_gate_test.go · 职责说明 ============
// 四期 §4.5.3 **A3**：/assist-api 四端点路由/方法/错误码不变。
// 线上挂件是缓存包，路由不能删（§4.5.2 步骤 4）。
//
// 反证（M0300）：删/改一个白名单项 ⇒ 本地白名单与旧代理白名单不再逐项相等 ⇒ 红。
// ================================================
package api

import (
	"os"
	"strings"
	"testing"
)

// a3PathsEqual 两张白名单逐键逐值相等（不是「都非空」那种空转判据）。
func a3PathsEqual(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		bv, ok := b[k]
		if !ok || bv != v {
			return false
		}
	}
	return true
}

func a3Read(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("A3 读 %s 失败: %v", name, err)
	}
	return string(b)
}

// TestA3AssistRouteCompat 四端点白名单 / 前缀 / 方法错误码三处都不许变。
func TestA3AssistRouteCompat(t *testing.T) {
	// ① 本地白名单与旧代理白名单**运行时逐项相等**（不是各写一份清单）。
	if !a3PathsEqual(assistOpenPaths, localAssistOpenPaths) {
		t.Fatalf("A3 本地白名单与 /assist-api 旧白名单不等值：old=%v local=%v（M0300 删一项必红）",
			assistOpenPaths, localAssistOpenPaths)
	}
	if len(localAssistOpenPaths) != 4 {
		t.Fatalf("A3 白名单 %d 项（期望恰 4）：多一项是旁路，少一项是丢端点", len(localAssistOpenPaths))
	}
	for _, p := range []string{
		"/api/assist/greeting", "/api/assist/chat",
		"/api/assist/history", "/api/assist/features",
	} {
		if !assistOpenPaths[p] || !localAssistOpenPaths[p] {
			t.Fatalf("A3 端点 %s 不在位（路由不能删）", p)
		}
	}

	// ② /assist-api 前缀与路由登记仍在（兼容层不许被摘）。
	proxySrc := a3Read(t, "assist_open_proxy.go")
	if !strings.Contains(proxySrc, `const assistOpenPrefix = "/assist-api"`) {
		t.Fatal("A3 /assist-api 前缀常量被改：线上缓存包会全量落 SPA 兜底")
	}
	serverSrc := a3Read(t, "server.go")
	if !strings.Contains(serverSrc, `s.mux.HandleFunc(assistOpenPrefix+"/", s.handleAssistOpenProxy)`) &&
		!strings.Contains(serverSrc, `s.mux.HandleFunc(assistOpenPrefix+"/", s.handleAssistOpen)`) {
		t.Fatal("A3 /assist-api/* 的路由登记不在位（或指向了未知 handler）：兼容层被删")
	}

	// ③ 本地转调仍做白名单/方法/限长/剥前缀四道守卫（错误码不变的形态保证）。
	localSrc := a3Read(t, "assist_local.go")
	for _, anchor := range []string{
		"!localAssistOpenPaths[up]",
		`r.Method != http.MethodGet && r.Method != http.MethodPost`,
		`w.Header().Set("Allow", "GET, POST")`,
		`strings.TrimPrefix(r.URL.Path, assistOpenPrefix)`,
		"http.MaxBytesReader(w, r.Body, assistOpenMaxBody)",
	} {
		if !strings.Contains(localSrc, anchor) {
			t.Fatalf("A3 本地转调缺守卫 %q：四端点的路由/方法/错误码形态不再等价", anchor)
		}
	}

	// ★ 反证（射程自证）：把本地白名单删一项喂同一个比对函数，必须判不等；
	//   若 a3PathsEqual 恒返回 true，上面 ① 就是空转锁。
	bad := map[string]bool{}
	for k, v := range localAssistOpenPaths {
		bad[k] = v
	}
	delete(bad, "/api/assist/features")
	if a3PathsEqual(assistOpenPaths, bad) {
		t.Fatal("A3 反证失明：删掉 features 后两张白名单仍判相等 ⇒ 比对函数是空转的")
	}
}
