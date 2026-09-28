// ============================================================================
// pprof_guard.go — ★ D-8（2026-09-29）pprof 诊断端点的「非回环必须有鉴权」启动期闸门
//
// 缺陷因果链：pprof 端点默认只绑 127.0.0.1:18787（这一档本身是安全的），但端口可由
//
//	env `PPROF_ADDR` 覆盖。运维排障时把它写成 `0.0.0.0:18787` 是最常见的一步误操作，
//	而 `/debug/pprof/*` 这条 mux **没有任何鉴权**——一旦对外可达，任何人都能拉到
//	heap/goroutine/profile：里面有进程内存片段、协程栈、环境变量线索（token 明文可被扫出），
//	同时 /debug/pprof/profile 还能被拿来打满 CPU（免费拒绝服务）。
//	旧写法是「默认回环 + 覆盖无防护」，等于安全属性完全依赖运维不改这个 env。
//
// 修法（对齐 REQUIRE_PROD_SECRETS 那套 fail-fast 范式）：
//
//	① 启动期判定：非回环监听且没配 `PPROF_TOKEN` → 直接拒绝启动（大声 FATAL，绝不"降级继续跑"）；
//	② 配了 token 时，mux 外面套一层常量时间比对（`subtle.ConstantTimeCompare`），
//	   拒绝无凭据请求；header 与 query 两种带法都收，是因为 curl 抓 profile 时
//	   用 query 更顺手，但**优先 header**（query 会进对端访问日志）。
//	回环监听维持「零配置即用」——本机排障是它的主要用途，给它加门槛只会逼人关掉它。
//
// ⚠️ 本文件只做判定与包装，不引第三方依赖；判定函数与中间件都是纯函数，
//
//	便于 pprof_guard_test.go 在不真正起端口的情况下把四态全跑一遍。
//
// ============================================================================
package main

import (
	"crypto/subtle"
	"fmt"
	"net/http"
	"strings"
)

// pprofGuard 判定给定的 pprof 监听地址是否允许启动。
// 返回 nil 表示放行（含 addr=="off" 的关闭态）；返回非 nil 即调用方必须拒绝启动。
// 参数：addr=PPROF_ADDR 的实际值（可以是 ""，代表走默认回环档）；token=PPROF_TOKEN 的值。
func pprofGuard(addr, token string) error {
	// 关闭态与默认回环档都不需要额外条件：off 不监听，空值由调用方回落 127.0.0.1
	if addr == "" || strings.EqualFold(addr, "off") {
		return nil
	}
	if isLoopbackListen(addr) {
		return nil // 回环：外网/反代打不到，维持零配置即用
	}
	// 非回环＝对外可达，没有 token 就是把诊断面裸奔送上网
	if strings.TrimSpace(token) == "" {
		return fmt.Errorf("PPROF_ADDR=%s 非回环（对外可达），但未配置 PPROF_TOKEN；"+
			"/debug/pprof 会泄漏进程内存与协程栈并可被打满 CPU，拒绝启动。"+
			"要么改回 127.0.0.1:端口（本机 curl 取样），要么设 PPROF_TOKEN 后重启", addr)
	}
	return nil
}

// pprofAuth 给 pprof mux 套一层 token 校验。
// token 为空 → 原样返回（回环零配置档，行为与本批改动前逐字一致）；
// token 非空 → 只放过 X-PPROF-Token 头或 ?token= 查询参数与之常量时间相等的请求。
func pprofAuth(token string, next http.Handler) http.Handler {
	if strings.TrimSpace(token) == "" {
		return next
	}
	want := []byte(token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("X-PPROF-Token")
		if got == "" {
			got = r.URL.Query().Get("token")
		}
		if subtle.ConstantTimeCompare([]byte(got), want) != 1 {
			// 401 但不回显期望值，也不写具体是哪一侧不符（探测者只需要知道"进不去"）
			w.Header().Set("WWW-Authenticate", "X-PPROF-Token")
			http.Error(w, "pprof 需要有效 token", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
