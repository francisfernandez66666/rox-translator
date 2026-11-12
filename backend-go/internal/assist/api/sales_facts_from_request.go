// ============ sales_facts_from_request.go · 职责说明 ============
// 销售事实集的 **assist 侧验签入口**（三期 W2，§0.4 三段链路第②段）。
//
// 形态照 profile.go 的 visitorProfileFromRequest：① 只信回环对端；② ±60s 时间窗；
// ③ HMAC 等值；④ 协议版本位。任一不过 ⇒ 返回 ok=false（ctx 里不放事实集），
// 由 W4 守卫按「无事实头」fail-closed 回避，**绝不返 4xx**（身份腿坏了不许把挂件整条打死）。
//
// 密钥与画像共用 ASSIST_PROFILE_KEY（profileKey()），头名独立（X-LangCross-Sales-Facts*）。
// 失败语义：缺头＝正常匿名（不出声）；带头但验不过＝去重 WARN（按档位，不刷屏）。
// ================================================
package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"translator/internal/assist/engine"
	mainengine "translator/internal/engine"
)

// salesFactsFromRequest 验签并解出事实集；任何失败都返回 ok=false（W4 据此 fail-closed）。
// 参数 r=入站请求；返回事实集、是否验签通过。
func (s *Server) salesFactsFromRequest(r *http.Request) (mainengine.SalesFactsSet, bool) {
	if r == nil {
		return mainengine.SalesFactsSet{}, false
	}
	payload := strings.TrimSpace(r.Header.Get(mainengine.SalesFactsHeader))
	ts := strings.TrimSpace(r.Header.Get(mainengine.SalesFactsTsHeader))
	sig := strings.TrimSpace(r.Header.Get(mainengine.SalesFactsSigHeader))
	if payload == "" && ts == "" && sig == "" {
		return mainengine.SalesFactsSet{}, false // 正常匿名：不出声
	}
	// 版本位不认 ⇒ 按无头处理（§7.2：assist 不认新版本时绝不放行）
	if v := strings.TrimSpace(r.Header.Get(mainengine.SalesFactsVersionHeader)); v != mainengine.SalesFactsVersion {
		s.warnProfileOnce(r.Context(), "sf_version")
		return mainengine.SalesFactsSet{}, false
	}
	if !profilePeerIsLoopback(r) {
		s.warnProfileOnce(r.Context(), "sf_peer")
		return mainengine.SalesFactsSet{}, false
	}
	key := s.profileKey()
	if len(key) == 0 {
		s.warnProfileOnce(r.Context(), "sf_key")
		return mainengine.SalesFactsSet{}, false
	}
	set, err := mainengine.VerifySalesFacts(key, payload, ts, sig, time.Now())
	if err != nil {
		reason := "sf_mismatch"
		switch err {
		case mainengine.ErrSalesFactsTimestamp:
			reason = "sf_expired"
		case mainengine.ErrSalesFactsPayload:
			reason = "sf_payload"
		case mainengine.ErrSalesFactsNoKey:
			reason = "sf_key"
		}
		s.warnProfileOnce(r.Context(), reason)
		return mainengine.SalesFactsSet{}, false
	}
	return set, true
}

// injectSalesFacts 把验签通过的事实集注入接待 ctx（唯一注入点，薄委托 assist/engine 适配层）。
// 参数 ctx=接待上下文；r=入站请求；返回派生 ctx（未通过则原样返回）。
func (s *Server) injectSalesFacts(ctx context.Context, r *http.Request) context.Context {
	if set, ok := s.salesFactsFromRequest(r); ok {
		return engine.InjectSalesFacts(ctx, set)
	}
	return ctx
}
