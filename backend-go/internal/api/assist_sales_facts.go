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
//     只在本文件（A08 扫描面）。pending_human 由 RecordReply 的 INSERT 按 message_id
//     精确落行（幂等刷新不动事件列）；登录档另有 MarkPendingHumanBySession 按
//     conversation_id 精确回填（三期 W6），不按「最近一条」猜行。
//  2. **先答客后回写**：w.Write(payload) 必须先于 writeBackAttribution（A04b/M0210 源码序锁）——
//     回写再慢再坏，访客拿到的字节一个都不变。
//  3. **事实集＝按作用域准入的真值**（四期 W1 起）：buildSalesFactsSet 用
//     resolveSalesFactsScope 解析出的 (TenantID, ProductID, Lang) 调
//     ListSalesFactsForInjection；productID 绝不传空串（三期那行会读出整租户事实＝串台）。
//     匿名产品页是合法作用域（product_page_anon），不是「未登录静默不发」；
//     平台上下文 / 无作用域 / 多产品 ambiguous / 未配密钥 ⇒ fail-closed 不发。
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

// attachSalesFacts 按**已解析好的作用域**把事实集签名头写到出站请求。
//
// ★ 四期 W1（§4.1.3-②）：签名从「登录用户 → tid」改成「作用域可注入」——
// 三期那句 `u == nil || u.ID <= 0` 早退正是「匿名公开页恒无事实」的成因（托管页 /p/... 的访客是匿名的），
// 现在唯一的早退条件是 `sc.injectable()`（租户 + 产品同时有效）。其余一律静默不发：
// 无头 ⇒ assist 侧空事实集 ⇒ W4 守卫 fail-closed 回避（不是放行）。
//
// ★ 开关读取刻意留在 buildSalesFactsSet 的第二道（作用域解析服务观测，注入判据服务行为，两处都要）。
// 参数：dst=出站请求；src=入站请求（上下文/语种）；sc=handleAssistOpenProxy 里**算一次**的作用域
// （禁止本函数再解析一遍：TTL 窗口内两次解析会给出两个答案，见 resolveSalesFactsScope 的注释）。
func (s *Server) attachSalesFacts(dst *http.Request, src *http.Request, sc salesFactsScope) {
	if dst == nil || src == nil || !sc.injectable() {
		return
	}
	key := s.assistProfileKey()
	if len(key) == 0 {
		return
	}
	set := mainengine.LimitSalesFacts(s.buildSalesFactsSet(src, sc))
	payload, err := mainengine.EncodeSalesFacts(set)
	if err != nil {
		return
	}
	ts := time.Now().Unix()
	dst.Header.Set(mainengine.SalesFactsHeader, payload)
	dst.Header.Set(mainengine.SalesFactsTsHeader, strconv.FormatInt(ts, 10))
	dst.Header.Set(mainengine.SalesFactsSigHeader, mainengine.SignSalesFacts(key, payload, ts))
	dst.Header.Set(mainengine.SalesFactsVersionHeader, mainengine.SalesFactsVersion)
	// ★ 观测（§4.1.3-② 的日志字段固定）：只有真下发过事实才出一行——
	// 公开页每个访客都会 greet，空集也打一行会把日志变成心跳噪音。
	// 字段只含不可读标识与计数：**绝不出事实值、产品名、租户名**（product_id 是主键，可进日志）。
	if n := len(set.Facts); n > 0 {
		s.salesFactsStats.injected.Add(int64(n))
		observability.Info(src.Context(), "销售事实集已下发",
			"reason", sc.Reason, "tenant_id", sc.TenantID, "product_id", sc.ProductID,
			"facts", n, "controlled", len(set.Controlled))
	}
}

// buildSalesFactsSet 组装本轮事实集（四期 W1 起按**作用域**取数，不再按"整租户"）。
//
// ★ 第二道开关判据（刻意与 resolveSalesFactsScope 的第一道重复）：作用域解析服务观测
//
//	（health 的 scope_* 计数），本函数服务**行为**（开关没全开时连一次读库都不该发生）。
//	灰度开 / 半套（只开一把，A15）/ 缺省关 ⇒ 仍返回 W4 静态空集（逐字节等价放行）。
//
// ★ 读口必须带 sc.ProductID：传 "" 就是三期那行「该租户全部产品」，一个租户两个产品时
//
//	A 产品的 MOQ 会被拿去回答 B 产品的问题（F3 的变异体就是把它改回 `""`）。
//
// ★ 热路径零 LLM：本函数绝不调 SingleLangTranslate（译文只在写时预生成，A13c 源码扫描 =0）。
func (s *Server) buildSalesFactsSet(r *http.Request, sc salesFactsScope) mainengine.SalesFactsSet {
	empty := mainengine.SalesFactsSet{
		TenantID: sc.TenantID,
		Lang:     sc.Lang,
		Facts:    []mainengine.SalesFact{},
	}
	if s.Store == nil || !sc.injectable() {
		return empty
	}
	// ★ 两把开关读 store.GetConfig（缺省关，空/未配/非法一律 off）；只开一把不算全开（A15）。
	inj, _ := s.Store.GetConfig("sales_facts_injection_enabled")
	guard, _ := s.Store.GetConfig("sales_claim_guard_enabled")
	injOn, e1 := strconv.ParseBool(inj)
	guardOn, e2 := strconv.ParseBool(guard)
	if e1 != nil || e2 != nil || !injOn || !guardOn {
		return empty // 灰度关 / 半套 ⇒ 静态空集（逐字节等价）
	}
	facts, controlled, err := s.Store.ListSalesFactsForInjection(sc.TenantID, sc.ProductID, sc.Lang)
	if err != nil {
		observability.Warn(r.Context(), "销售事实准入读口失败，按空集下发（fail-closed）",
			"tenant_id", sc.TenantID, "product_id", sc.ProductID, "reason", sc.Reason, "err", err)
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
//
// ★ 四期 W1（§4.1.3-⑥ 施工口径 4/5）：
//
//	① 匿名**只放行 product_page_anon 这一支**，以 sc.TenantID（产品所属租户）为 tid 写入；
//	   其余匿名（无作用域 ⇒ denied）一律不写，绝不兜底进租户 1；
//	② 为什么不能传 effTenant：attribution.Engine.RecordReply 会用 salesflow.NewScope(ctx, tid)
//	   覆盖 row.TenantID，且 tid<=0 直接报错 ⇒ 传 0 报错、传 effTenant（匿名下是默认租户 1）串租户；
//	③ 匿名回写**只落归因行本身**：EnsureHandoffSession / MarkPendingHumanBySession 只在登录档走
//	   ——匿名访客没有身份，建出来的 handoff 行是孤儿（谁来接管？）；
//	④ ProductID 一律取 sc.ProductID：没有这一列，开闸之后答不了"哪个产品带来了这条询盘"。
//
// 非 JSON / 无归因段 / 作用域不可写 ⇒ 静默跳过（greeting/history/features 无归因段是常态）。
func (s *Server) writeBackAttribution(ctx context.Context, payload []byte, r *http.Request, sc salesFactsScope) {
	var resp assistAttributionPayload
	if err := json.Unmarshal(payload, &resp); err != nil {
		return // 上游异常体（HTML/纯文本）不 panic、不写库
	}
	if resp.Attribution.MessageID <= 0 || s.Store == nil || r == nil {
		return
	}
	u := s.authUser(r)
	loggedIn := u != nil && u.ID > 0
	var tid int64
	if loggedIn {
		tid = s.effTenant(r, u)
		if tid <= 0 {
			return
		}
	} else {
		// ★ 匿名只放行「显式产品页」这一档（且租户必须已由产品行给出）。
		if sc.Reason != salesScopeProductPageAnon || sc.TenantID <= 0 {
			return
		}
		tid = sc.TenantID
	}
	pending := 0
	if resp.Attribution.PendingHuman {
		pending = 1
	}
	// 组合根唯一装配点（A08）：归因引擎只在这里 new，不在 assist 进程、不在 handler 散点。
	eng := attribution.NewEngine(s.Store)
	row := store.ReplyAttributionRow{
		MessageID:    resp.Attribution.MessageID,
		ProductID:    sc.ProductID,
		Provider:     "assist",
		ModelName:    resp.Attribution.Model,
		PendingHuman: pending,
	}
	// ★ W6（v1.4 裁定）：会话映射先建好——归因行的 conversation_id 由此获得精确锚；
	//   assist 会话 id（TEXT）→ handoff_sessions 行 id（int64）。映射幂等，重复回写不重复建行。
	//   ★ W1：只对登录档建（匿名 handoff 行是孤儿，见本函数头 ③）。
	if loggedIn {
		if sid := strings.TrimSpace(resp.Attribution.SessionID); sid != "" {
			if convID, err := s.Store.EnsureHandoffSession(tid, sid, resp.Attribution.MessageID); err == nil && convID > 0 {
				row.ConversationID = convID
			}
		}
	}
	if err := eng.RecordReply(ctx, tid, row); err != nil {
		observability.Warn(ctx, "assist 回复归因落库失败（不影响访客答复）", "tenant_id", tid, "err", err)
		return
	}
	// ★ W6 待人工回填（A24/M0263）：守卫命中（pending_human=1）时按会话精确回填归因行与映射行，
	//   不按「最近一条」找（并发下会标错行）；重复回写只刷新映射不翻已清零的标记。
	//   ★ W1：同样只在登录档走（匿名没有卖家视野那一侧）。
	if loggedIn && resp.Attribution.PendingHuman && strings.TrimSpace(resp.Attribution.SessionID) != "" {
		if _, err := s.Store.MarkPendingHumanBySession(tid, resp.Attribution.SessionID); err != nil {
			observability.Warn(ctx, "待人工按会话回填失败（不影响访客答复）", "tenant_id", tid, "err", err)
		}
	}
	// SyncPackStats 同批接齐（修 B5）：W2 的行 draft_id 为空、聚合暂不物化，
	// 但接线必须在场——W6 素材行出现时不再补「忘了接」这一课。
	// ★ W1：它按租户物化打包效果快照，与"谁来接管"无关 ⇒ 匿名档同样要跑（否则漏斗缺一侧）。
	if err := eng.SyncPackStats(ctx, tid); err != nil {
		observability.Warn(ctx, "assist 素材效果快照物化失败（不影响访客答复）", "tenant_id", tid, "err", err)
	}
}
