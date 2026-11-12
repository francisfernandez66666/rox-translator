// ============ assist_sales_facts.go · 职责说明 ============
// 销售事实集与回复归因的**主服务侧**（三期 W2，§0.4 三段链路的第①③段）。
//
//	① attachSalesFacts：转发前按已校验会话组装事实集 → JSON+base64 → HMAC 签名头，
//	  挂在 assist_open_proxy.go 的 attachAssistProfile 之后（加头先例 assist_profile.go）。
//	③ writeBackAttribution：上游响应已全量读进 payload 且**已写给访客之后**，解析归因段
//	  写主库 reply_attributions / pack_stats。回写失败只 WARN，绝不影响访客答复。
//
// 三条硬口径：
//  1. **组合根唯一**：attribution.NewEngine / RecordReply / SyncPackStats 的生产装配点
//     只在本文件（A08 扫描面）。MarkPendingHuman 留待 W6 有 conversation_id 后在同点装配——
//     它按「会话/客户最近一条 + draft_id 非空」定位，W2 的行没有会话映射，调它结构性空转；
//     pending_human 在 W2 由 RecordReply 的 INSERT 按 message_id 精确落行（幂等刷新不动事件列）。
//  2. **先答客后回写**：w.Write(payload) 必须先于 writeBackAttribution（A04b/M0210 源码序锁）——
//     回写再慢再坏，访客拿到的字节一个都不变。
//  3. **W2 事实集＝已准入的静态空集**：准入读口 ListSalesFactsForInjection 与译文预生成是 W4；
//     本批把 header 协议/签名/限长一次定死，buildSalesFactsSet 的事实内容 W4 替换。
//     未登录/平台上下文/未配密钥 ⇒ 静默不发（匿名是合法回落，与画像同口径）。
//
// 安全：入站同名头由 stripSalesFactsHeaders 先删再自赋（P-7 同族）；头只走内网，
// 不进日志、不进错误信封；受控键在类型层就没有 Value 字段（internal/engine/sales_facts.go）。
// ================================================
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"translator/internal/attribution"
	mainengine "translator/internal/engine"
	"translator/internal/observability"
	"translator/internal/store"
)

// stripSalesFactsHeaders 删除入站请求上的四个事实集头（先删再自赋，P-7 同族）。
// 客户端自带伪造头时，这一步保证它到不了上游；assist 只认本进程按已校验会话重签的那份。
func stripSalesFactsHeaders(h http.Header) {
	if h == nil {
		return
	}
	h.Del(mainengine.SalesFactsHeader)
	h.Del(mainengine.SalesFactsTsHeader)
	h.Del(mainengine.SalesFactsSigHeader)
	h.Del(mainengine.SalesFactsVersionHeader)
}

// attachSalesFacts 若本次请求是登录用户、租户上下文有效且密钥已配置，则把事实集签名头写到出站请求。
// 任何一环取不到都静默不发：无头 ⇒ assist 侧空事实集 ⇒ W4 守卫 fail-closed 回避（不是放行）。
func (s *Server) attachSalesFacts(dst *http.Request, src *http.Request) {
	if dst == nil || src == nil {
		return
	}
	key := s.assistProfileKey()
	if len(key) == 0 {
		return
	}
	u := s.authUser(src)
	if u == nil || u.ID <= 0 {
		return
	}
	tid := s.effTenant(src, u)
	if tid <= 0 {
		return // 平台上下文（超管未选租户）没有产品事实语义
	}
	set := mainengine.LimitSalesFacts(s.buildSalesFactsSet(src, tid))
	payload, err := mainengine.EncodeSalesFacts(set)
	if err != nil {
		return
	}
	ts := time.Now().Unix()
	dst.Header.Set(mainengine.SalesFactsHeader, payload)
	dst.Header.Set(mainengine.SalesFactsTsHeader, strconv.FormatInt(ts, 10))
	dst.Header.Set(mainengine.SalesFactsSigHeader, mainengine.SignSalesFacts(key, payload, ts))
	dst.Header.Set(mainengine.SalesFactsVersionHeader, mainengine.SalesFactsVersion)
}

// buildSalesFactsSet 组装本轮事实集（保留 W2 签名，A04/A08 扫本文件其它行零改动）。
// ★ W4：灰度两把开关（sales_facts_injection_enabled / sales_claim_guard_enabled）同时开才进入
//
//	「注入/守卫全开」——接准入读口 ListSalesFactsForInjection(tid, productID, answerLang) +
//	按 answerLang 取已落库译文 + 受控键清单；缺译文 / source_hash 失配的事实不进集（fail-closed）。
//	关 / 半套（只开一把，A15）/ 缺省关 ⇒ 仍返回 W2 静态空集（逐字节等价放行）。
//
// ★ 热路径零 LLM：本函数绝不调 SingleLangTranslate（译文只在写时预生成，A13c 源码扫描 =0）。
func (s *Server) buildSalesFactsSet(r *http.Request, tid int64) mainengine.SalesFactsSet {
	empty := mainengine.SalesFactsSet{
		TenantID: tid,
		Lang:     seoLangFromRequest(r),
		Facts:    []mainengine.SalesFact{},
	}
	if s.Store == nil {
		return empty
	}
	// ★ 两把开关读 store.GetConfig（缺省关，空/未配/非法一律 off）；只开一把不算全开（A15）。
	inj, _ := s.Store.GetConfig("sales_facts_injection_enabled")
	guard, _ := s.Store.GetConfig("sales_claim_guard_enabled")
	injOn, e1 := strconv.ParseBool(inj)
	guardOn, e2 := strconv.ParseBool(guard)
	if e1 != nil || e2 != nil || !injOn || !guardOn {
		return empty // 灰度关 / 半套 ⇒ W2 静态空集（逐字节等价）
	}
	lang := seoLangFromRequest(r)
	facts, controlled, err := s.Store.ListSalesFactsForInjection(tid, "", lang)
	if err != nil {
		observability.Warn(r.Context(), "销售事实准入读口失败，按空集下发（fail-closed）",
			"tenant_id", tid, "err", err)
		return empty
	}
	out := empty
	for _, f := range facts {
		out.Facts = append(out.Facts, mainengine.SalesFact{
			Kind: f.Kind, Key: f.Key, Value: f.Value, Lang: f.Lang,
		})
	}
	for _, c := range controlled {
		// ★ 受控键只放 Kind/Key（engine.ControlledKey 类型层已无 Value 字段），value 绝不进集。
		out.Controlled = append(out.Controlled, mainengine.ControlledKey{
			Kind: c.Kind, Key: c.Key,
		})
	}
	return out
}

// assistAttributionPayload 上游 /chat 响应里的归因段（assist/api/server.go handleChat 回吐）。
// 只认这五个字段；body 其余部分（reply/actions/...）原样透传，一个字节都不改。
type assistAttributionPayload struct {
	Attribution struct {
		MessageID    int64  `json:"message_id"`
		Source       string `json:"source"`
		Model        string `json:"model"`
		PendingHuman bool   `json:"pending_human"`
		SessionID    string `json:"session_id"`
	} `json:"attribution"`
}

// writeBackAttribution 把上游归因段写进主库（fire-and-forget：任何失败只 WARN，不返回错误）。
// 调用点必须位于 w.Write(payload) 之后（A04b/M0210）：访客响应已落字，回写成败与访客无关。
// 非 JSON / 无归因段 / 未登录 / 平台上下文 ⇒ 静默跳过（greeting/history/features 无归因段是常态）。
func (s *Server) writeBackAttribution(ctx context.Context, payload []byte, r *http.Request) {
	var resp assistAttributionPayload
	if err := json.Unmarshal(payload, &resp); err != nil {
		return // 上游异常体（HTML/纯文本）不 panic、不写库
	}
	if resp.Attribution.MessageID <= 0 || s.Store == nil || r == nil {
		return
	}
	u := s.authUser(r)
	if u == nil || u.ID <= 0 {
		return // 匿名访客的会话→租户绑定是 W4/W6；W2 不写（绝不兜底进租户 1）
	}
	tid := s.effTenant(r, u)
	if tid <= 0 {
		return
	}
	pending := 0
	if resp.Attribution.PendingHuman {
		pending = 1
	}
	// 组合根唯一装配点（A08）：归因引擎只在这里 new，不在 assist 进程、不在 handler 散点。
	eng := attribution.NewEngine(s.Store)
	row := store.ReplyAttributionRow{
		MessageID:    resp.Attribution.MessageID,
		Provider:     "assist",
		ModelName:    resp.Attribution.Model,
		PendingHuman: pending,
	}
	// ★ W6（v1.4 裁定）：会话映射先建好——归因行的 conversation_id 由此获得精确锚；
	//   assist 会话 id（TEXT）→ handoff_sessions 行 id（int64）。映射幂等，重复回写不重复建行。
	if sid := strings.TrimSpace(resp.Attribution.SessionID); sid != "" {
		if convID, err := s.Store.EnsureHandoffSession(tid, sid, resp.Attribution.MessageID); err == nil && convID > 0 {
			row.ConversationID = convID
		}
	}
	if err := eng.RecordReply(ctx, tid, row); err != nil {
		observability.Warn(ctx, "assist 回复归因落库失败（不影响访客答复）", "tenant_id", tid, "err", err)
		return
	}
	// ★ W6 待人工回填（A24/M0263）：守卫命中（pending_human=1）时按会话精确回填归因行与映射行，
	//   不按「最近一条」找（并发下会标错行）；重复回写只刷新映射不翻已清零的标记。
	if resp.Attribution.PendingHuman && strings.TrimSpace(resp.Attribution.SessionID) != "" {
		if _, err := s.Store.MarkPendingHumanBySession(tid, resp.Attribution.SessionID); err != nil {
			observability.Warn(ctx, "待人工按会话回填失败（不影响访客答复）", "tenant_id", tid, "err", err)
		}
	}
	// SyncPackStats 同批接齐（修 B5）：W2 的行 draft_id 为空、聚合暂不物化，
	// 但接线必须在场——W6 素材行出现时不再补「忘了接」这一课。
	if err := eng.SyncPackStats(ctx, tid); err != nil {
		observability.Warn(ctx, "assist 素材效果快照物化失败（不影响访客答复）", "tenant_id", tid, "err", err)
	}
}
