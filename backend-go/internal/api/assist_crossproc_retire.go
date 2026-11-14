// ============ assist_crossproc_retire.go · 职责说明 ============
// 四期 W5（§4.5.2 步骤 3）：跨进程链**退役开关 + 适配**。
//
// 旧代码（attachAssistProfile / attachSalesFacts / strip*Headers / assistOpenMaxBody /
// assistClient / assistBaseURL）全部**原样保留**在各自文件里，一个字不改——
// 它们既是回滚档，也是 A1 逐字等值的对照面。本文件只做三件事：
//  1. 让本地链路成为默认（assistLinkIsLocal），显式 ASSIST_LINK=remote 或本地装配
//     未就绪时才回落旧代理 handleAssistOpenProxy；
//  2. 提供 /assist-api/* 的唯一分发入口 handleAssistOpen（组合根把 routes() 里
//     那一行改指向它，见 /tmp/w5_wiring.md）；
//  3. 事实集直接 WithSalesFacts(ctx, sc) —— 跨进程的
//     Encode→HMAC→Header.Set→回环验签→Decode 整段消失（localFactsContext）。
//
// 开关默认本地，是「先逻辑收敛后物理拆分」（§1.2 裁定 2）在代码里的唯一落点：
// 进程边界先消失，跨进程 header 签名 / 32KB 截断 / 两套 migrate 挂接点整段退役；
// 将来销售域独立成进程（W6）时再按域抽服务，是「代码搬家 + 客户端替换」，
// 不是「重新设计一次通信」。
// ================================================
package api

import (
	"context"
	"net/http"
	"os"

	mainengine "translator/internal/engine"
	"translator/internal/observability"
)

// assistLinkLocalEnv 退役开关：默认本地；显式 ASSIST_LINK=remote 才回落旧跨进程代理。
// 这一档是回滚用（旧二进制装回后不需要改代码，配一个 env 即回到旧链路）。
const assistLinkLocalEnv = "ASSIST_LINK"

// assistLinkIsLocal 本地链路是否作为默认。
// ★ 判据刻意写成「!= remote 即本地」：缺省、空值、任意拼错的值一律本地，
//
//	绝不让「读不到配置」变成「悄悄回到跨进程」——与 §8.1 第 3 条
//	「读不到 ≠ 关」同族，只是这里的缺省方向反过来（缺省＝已收敛）。
func assistLinkIsLocal() bool {
	return os.Getenv(assistLinkLocalEnv) != "remote"
}

// handleAssistOpen /assist-api/* 的唯一入口（组合根接线见 /tmp/w5_wiring.md）。
// 本地为默认；remote 或未装配时回落旧代理 handleAssistOpenProxy（旧代码零改动）。
func (s *Server) handleAssistOpen(w http.ResponseWriter, r *http.Request) {
	if assistLinkIsLocal() {
		if la := currentAssistLocal(); la != nil {
			s.serveAssistOpenLocal(w, r, la)
			return
		}
		observability.Warn(r.Context(), "assist 本地引擎未装配，回落跨进程代理",
			"path", r.URL.Path)
	}
	s.handleAssistOpenProxy(w, r)
}

// localFactsContext 事实集本地注入的**唯一适配点**（W5 §4.5.2 步骤 3）：
// 直接 WithSalesFacts(ctx, set)，不再签名成头。
//
// set 由调用方按旧链同一口径算好（LimitSalesFacts + buildSalesFactsSet），
// 并已经过 EncodeSalesFacts/DecodeSalesFacts 保形，故 ctx 里的 Set 与旧链
// assist 侧验签解码后逐字段同形（A1 的逐字等值靠这一层）。
// 守卫消费侧 mainengine.SalesFactsFromContext(ctx) 无需任何改动。
func (s *Server) localFactsContext(ctx context.Context, set mainengine.SalesFactsSet) context.Context {
	return mainengine.WithSalesFacts(ctx, set)
}

// retiredCrossProcReads 退役的跨进程读取点清单（A4 源码扫描锁的射程说明，
// 不是运行时配置）。旧文件保留，但本地 handler 不得再触碰这些点；
// 每一项都由新链路里的本地替代物顶上：
//
//	attachAssistProfile / attachSalesFacts → injectLocalAssistContext（ctx 直注）
//	stripAssistProfileHeaders / stripSalesFactsHeaders
//	    → 本地链路根本不存在这两族头，无需 strip（A4 只禁写头，不禁删入站头）
//	assistOpenMaxBody → serveAssistOpenLocal 仍按同一常量限长
//	assistClient / assistBaseURL → la.handler.ServeHTTP（零出网、零基址解析）
var retiredCrossProcReads = []string{
	"attachAssistProfile",
	"attachSalesFacts",
	"stripAssistProfileHeaders",
	"stripSalesFactsHeaders",
	"assistClient",
	"assistBaseURL",
	"assistOpenMaxBody",
}
