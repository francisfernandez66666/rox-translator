// ============ health_probes.go · 职责说明 ============
// api 包内部实现文件：进程存活探针（/livez）与依赖就绪探针（/readyz）。
// 只读探活、不写业务数据、不接鉴权（两条路由登记在
// route_auth_gate_test.go 的 publicRouteAllowlist，公开且只输出状态词）。
// =============================================
package api

// ============ 本文件职责中文说明 ============
// ★ #42（2026-09-22）P2 技术债：探针拆分。
//
// 为什么要拆：本仓此前只有 /api/health（handleHealth，server.go 内），它把「进程活着」和
// 「各模块是否装配」混在一个 200 里，且**既不探 DB 也不探 Redis**（只判 s.Store != nil）。
// 于是两种监控需求都没被满足：
//   - 存活（liveness）：K8s/systemd 据此决定「要不要重启这个进程」。它必须在 Redis/DB 故障时
//     **依然返回 200**——依赖故障重启进程救不回来，反而把本可靠降级继续跑的实例（计量缓冲、
//     工单续跑、连接池自愈都在内存里）反复杀掉，把一次依赖抖动放大成全站重启风暴。
//   - 就绪（readiness）：据此决定「要不要往这个实例上放流量」。这里才该真探依赖：
//     库连不上就 503，让上游把它摘出轮询，等依赖恢复自动回流量。
// 两者合一必然顾此失彼，故拆成 /livez（零依赖检查）+ /readyz（探依赖并点名失败方）。
//
// /api/health 保持原样不动：它已承担云端诊断与监控告警口径（store_ready/distributed 等字段
// 被运维看板读取），改它的语义等于改监控契约；本文件只做加法。
//
// 依赖判定口径（决策记录，勿凭猜改动）：
//  1. 平台存储（s.Store + DB Ping）不可达 ⇒ 未就绪。这是唯一硬依赖：用户/计费/工单全在这一库里。
//  2. 分布式能力（Redis）按 redis.Availability() 的三态判定，且**实时补一次带超时的 PING**：
//     - redis：跨实例聚合可用 ⇒ 就绪。
//     - in-process / unknown：未配 REDIS_ADDR，锁/限流走进程内实现 ⇒ **视为就绪**。
//       理由：启动闸门（cmd/server/main.go 的 #40 段）在 REQUIRE_REDIS=1（多副本口径）时
//       已经拒绝过这种状态的进程启动；能正常起服务就说明运维**显式允许**单副本降级跑，
//       readyz 再判它不就绪等于把主站（现役单副本）整体摘空，与运维契约相反。
//     - unreachable：配了地址却探活失败 ⇒ 未就绪。配了 Redis 就是想要跨实例语义，
//       此状态下扣费/通知会各副本各算一份（资金风险），摘流量比继续接客更安全。
//  3. 失败原因只输出**粗粒度状态词**，绝不带 err.Error()：探针是匿名可达的，
//     DB 报错原文会含主机/端口/文件路径等内网拓扑（与 /api/health 只暴露状态词的既有口径一致）。
// =============================================

import (
	"context"
	"net/http"
	"time"

	"translator/internal/infra/redis"
)

// probeVersion 探针回显的服务版本，与 handleHealth 的 version 字段同一口径（改一处即可）。
const probeVersion = "2.0.0-go"

// probeStoreTimeout DB 探活超时（与 /status 的 db_ok 判定同为 2s）。
// 必须设上限：探针被慢库挂住时，编排器只会看到「探针超时」而拿不到任何原因。
const probeStoreTimeout = 2 * time.Second

// probeRedisTimeout Redis 探活超时。比 DB 更短：PING 本应亚毫秒，1s 还不回就是真出问题了。
// ★ 刻意不走包级 redis.Ping()（它内部用 context.Background，黑洞连接会把探针 goroutine 挂死）。
const probeRedisTimeout = 1 * time.Second

// handleLivez 存活探针（/livez）：只回答「进程还在不在」，**零依赖检查**。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（不使用其任何字段）。
// 返回: 恒 200 + {status:ok, version}。Redis/DB/引擎故障一律不影响本端点。
func (s *Server) handleLivez(w http.ResponseWriter, r *http.Request) {
	// 这里刻意什么都不判：进程能被调度到就跑到了这一行，能返回 200 即存活。
	// 不要「顺手」加 DB/Redis 检查——那等于把 liveness 变回 readiness，重启风暴回来了。
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":  "ok",
		"version": probeVersion,
	})
}

// handleReadyz 就绪探针（/readyz）：真探依赖，不就绪返回 503 并在 body 点名失败的依赖。
// 参数 w: HTTP 响应写入器；r: HTTP 请求。
// 返回: 200 {status:ready, store, distributed}；503 {status:not_ready, failed_dependencies:[...], ...}。
func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	storeState, storeOK := s.probeStore()
	distState, distOK := probeDistributed()

	// 失败依赖按「名字」收集：上游监控/值班只看这一个数组就知道该修什么，无需解析状态词组合。
	var failed []string
	if !storeOK {
		failed = append(failed, "store")
	}
	if !distOK {
		failed = append(failed, "redis")
	}

	resp := map[string]interface{}{
		"store":       storeState,
		"distributed": distState,
		"version":     probeVersion,
	}
	if len(failed) > 0 {
		resp["status"] = "not_ready"
		resp["failed_dependencies"] = failed
		writeJSON(w, http.StatusServiceUnavailable, resp)
		return
	}
	resp["status"] = "ready"
	writeJSON(w, http.StatusOK, resp)
}

// probeStore 平台存储可达性：返回粗粒度状态词与是否就绪。
// 状态词：ok / not_initialized（Store 未装配，等价于启动失败）/ unreachable（Ping 失败或超时）。
// 不返回 error 文本，理由见本文件头第 3 条判定口径（匿名端点禁止泄漏内网拓扑）。
func (s *Server) probeStore() (state string, ok bool) {
	if s.Store == nil {
		return "not_initialized", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), probeStoreTimeout)
	defer cancel()
	if err := s.Store.DB().PingContext(ctx); err != nil {
		return "unreachable", false
	}
	return "ok", true
}

// probeDistributed 分布式能力（Redis）就绪性：返回状态词与是否就绪，口径详见文件头第 2 条。
// 未配置 Redis（含单测里未走启动闸门的 unknown）判为就绪；配了但 PING 失败判为不就绪。
func probeDistributed() (state string, ok bool) {
	if !redis.Enabled() {
		// 无客户端实例：没有可探的对端，沿用启动闸门的结论词（单测下为 unknown）。
		return redis.Availability(), true
	}
	c := redis.Get()
	if c == nil {
		return "in-process", true
	}
	ctx, cancel := context.WithTimeout(context.Background(), probeRedisTimeout)
	defer cancel()
	if err := c.Ping(ctx); err != nil {
		return "unreachable", false
	}
	return "redis", true
}
