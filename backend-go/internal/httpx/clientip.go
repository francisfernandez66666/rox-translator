// ============ clientip.go · 职责说明 ============
// 全仓「这个请求是谁发来的」**唯一**实现（★ 2026-10-30 三事合一批 组2a，K6／K7 的事实源）。
//
// 为什么必须收成一处（现仓此前有两份互不相干的取法，都在生产上跑）：
//
//	① `internal/api/ratelimit.go` —— `TRUST_PROXY_XFF=1` 时取 X-Forwarded-For 最左，
//	   否则只认 RemoteAddr。生产确实开了这个 env（`deploy/systemd/prod.conf`），
//	   但它**只看一个开关、不看这一枪从哪儿来**：哪天有人把端口直接暴露出去（或本地跑 UAT 时忘了这个 env 的语义），
//	   任何人都能靠一个 `X-Forwarded-For: 1.2.3.4` 把自己伪装成新访客。
//	② `internal/assist/api/server.go` —— **无条件**信任 XFF／X-Real-IP／Proxy-Client-IP／WL-Client-IP 四个头。
//	   挂件那侧的匿名会话归属与限流就建在这句话上，伪造成本为零。
//
//	本批要给「即时翻译免费档」按 IP 记一格（`free_ip`），**判据一旦被伪造，档位就是纸糊的**，
//	所以先收实现、再上档位（顺序反了就是"上线当天发现防不住"）。
//
// 信任链（只有一条，且**按来路判，不按值判**——AGENTS §一·3 那条「谁优先的判据只能是构造期来源锚点」同一取向）：
//
//  1. 直连对端是**回环**（本机反代形态，现网＝Caddy 在同一台机器上 `reverse_proxy 127.0.0.1:PORT`）
//     ⇒ 才看代理链给的头：先 `CF-Connecting-IP`（Cloudflare 覆写过、客户端伪造不了），
//     再 `X-Forwarded-For` 的最左一跳（现网由 Caddy 用 `header_up X-Forwarded-For {http.request.header.CF-Connecting-IP}`
//     重写成同一个值，见 `deploy/caddy/translator.conf`）。
//  2. 直连对端**不是**回环 ⇒ 那个头没有任何东西为它背书，**一律用直连对端**，
//     带不带 XFF、带什么值都不改变结论（K6 判的就是这一档）。
//  3. 两个头都取到但**解析不出一个 IP**（`unknown`、空段、带端口的整串）⇒ 继续往下回落，
//     不把脏值当身份用——归属类判据最坏的形态不是"取不到"，而是"取到一个谁都不同的假值"，
//     那会把同一个访客的每一枪都算成新人（限流与免费档同时失效，且日志里看不出区别）。
//
// ⚠️ 残余风险照实说：本函数判的是「来路是不是本机回环」，**不判「这台到底是不是真反代」**。
//
//	如果哪天源站端口被直接暴露给公网（绕过 Caddy），攻击者的直连对端就不是回环，落到第 2 档 ⇒ 安全；
//	反过来，如果有人在同一台机器上又起了一个"会把客户端头原样透传"的代理，第 1 档就会转嫁那个头的可信性。
//	那属于部署拓扑变更，不在这个函数能判的范围里，改拓扑时要重新核这一条（同 §一·16 那条「现网状态要现读」）。
//
// 调用方口径：`internal/api` 与 `internal/assist/api` 两侧的同名 `clientIP` 一律**薄委托**到这里，
// 由 `internal/httpx/clientip_gate_test.go` 的 K7 按「谁读 X-Forwarded-For」的全集机械守住（名单外出现即红）。
// =============================================
package httpx

import (
	"net"
	"net/http"
	"strings"
)

// ClientIP 取这个请求的客户端地址（已剥端口；取不到时回落 RemoteAddr 原样，绝不回空串）。
// 参数 r: 原始请求——本函数只读它的 Header 与 RemoteAddr，不碰 context，所以任何调用点都能直接用。
func ClientIP(r *http.Request) string {
	if r == nil {
		return ""
	}
	peer := peerHost(r.RemoteAddr)
	// 第 2 档的短路先写在这里：**不是回环就不看任何头**，
	// 免得"看了头但值不合格"与"根本没资格看头"两种情况在后续分支里混成一种。
	if !isLoopbackHost(peer) {
		return peer
	}
	// 第 1 档：Cloudflare 覆写过的单值头优先（客户端伪造不到它）。
	if ip := normalizeIP(r.Header.Get("CF-Connecting-IP")); ip != "" {
		return ip
	}
	// 再取 XFF 的最左一跳（现网它已被 Caddy 重写成同一个 CF 值；
	// 非 CF 形态的多跳链里，最左仍是"最早那个客户端地址"这一惯例口径）。
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		first := strings.SplitN(xff, ",", 2)[0]
		if ip := normalizeIP(first); ip != "" {
			return ip
		}
	}
	return peer
}

// peerHost 从 RemoteAddr（"host:port" 或 "host"）里取主机部分。
// 取不出（畸形串）时**原样返回**：老实现就是这么回落的，这里不改对外形态——
// 把它折成空串会让"归属"落到空 key 上，所有畸形请求反而共享一格。
func peerHost(remoteAddr string) string {
	if remoteAddr == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(remoteAddr); err == nil {
		return host
	}
	// 没有端口形态（含 IPv6 的 "[::1]" 已带括号，SplitHostPort 认得；走到这里是真畸形串）
	return remoteAddr
}

// normalizeIP 把一段可能带空格、带端口、带前导 "::ffff:" 的地址折成一个纯 IP 文本；
// 解析不出来就回空串（**回空串＝这一档不采纳**，由调用侧继续回落，绝不返回原值）。
func normalizeIP(v string) string {
	v = strings.TrimSpace(v)
	if v == "" || strings.EqualFold(v, "unknown") {
		return ""
	}
	// 带端口的形态（"[::1]:1234"／"1.2.3.4:5678"）先剥掉端口再判
	if h, _, err := net.SplitHostPort(v); err == nil {
		v = h
	}
	ip := net.ParseIP(v)
	if ip == nil {
		return ""
	}
	return ip.String()
}

// isLoopbackHost 判一个主机文本是否回环（127.0.0.0/8 与 ::1）。
// 参数 host: 已由 peerHost 剥过端口的地址。
// ⚠️ 只认**字面回环**，不认私网段（10/8、192.168/16 那类）：现网的反代与后端在同一台机器上，
// 把私网也算进信任档等于把"谁能进这台内网"当成新的信任边界——那不是一个可判的事实，是一张网络拓扑图。
func isLoopbackHost(host string) bool {
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}
