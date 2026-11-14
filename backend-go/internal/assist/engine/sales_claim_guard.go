// ============ sales_claim_guard.go · 职责说明 ============
// 三期 W4：Respond 出站咽喉上的**销售声称守卫**（用户裁定 2，§0.4 第 7 条、断言 A13）。
//
//   - 命中「触及受控键 / 无事实头 / 验签失败 / 事实集为空 / 声称锚点与译文对不上」时，
//     ★ 不是简单打回、也不是放行：把回复改成 12 语种各自的「我确认一下再给您回复」类回避句
//   - 转其他话题（安全入口），并把 Reply.PendingHuman 置真（主服务 writeBackAttribution 按
//     message_id 行内落位到 reply_attributions，W6 再消费）。
//   - engine 包只做薄适配（照 knowledge_guard.go 的形状）：ctx → Reply，判据吃
//     GuardLocalizedClaim/AvoidPhrase 两处导出纯函数，绝不在这里再抄第二把尺子。
//   - ★ 灰度（A15）：sales_facts_injection_enabled 与 sales_claim_guard_enabled 两把开关**缺省关**，
//     关/半套（只开一把）⇒ 逐字节等价放行；只有两把同时开才进入「注入/守卫全开」态。
//
// ★★ 本轮是否销售场景的边界（两枚反证测试钉住，别走样）：
//   - 销售页 URL ⇒ 销售轮：即便 ctx 无事实集（无头/验签失败/匿名未下发）也绝不 fail-open，
//     由本守卫走 fail-closed 回避（§0.4「无头 / 验签失败 / facts 为空 ⇒ 回避」）。
//   - 非销售页（翻译/客服老链路）⇒ 零改动放行（逐字节等价），绝不误伤：主服务 attachSalesFacts
//     对四个访客端点一视同仁地注入事实头，翻译/客服页也会带 SalesFactsSet，
//     所以「ctx 带事实集」绝不能单独当销售判据，否则灰度全开时老链路会被集体改写成回避话术。
//   - pageURL 为空（greeting/老客户端/历史轮）时，才退回「ctx 带事实集」这一补充信号。
//
// ================================================
package engine

import (
	"context"
	"regexp"
	"strconv"
	"strings"

	mainengine "translator/internal/engine"
	"translator/internal/observability"
)

// 灰度开关键（跨进程成对：主服务 buildSalesFactsSet 与此处 guardSalesClaims 各读自己库里的同两键）。
// ★ 缺省关、半套不生效：两把必须同时为真，A15「只开一把不得进入注入/守卫全开态」。
const (
	cfgSalesFactsInjection = "sales_facts_injection_enabled"
	cfgSalesClaimGuard     = "sales_claim_guard_enabled"
)

// configOn 把配置值归一成布尔；空/未配/未知一律 false（缺省关，安全方向）。
// ★ 与主服务 buildSalesFactsSet 用同一把 strconv.ParseBool 尺，两进程对 "1"/"true" 判定逐字一致。
// 参数：v=配置原值；返回是否开启。
func configOn(v string) bool {
	b, err := strconv.ParseBool(strings.TrimSpace(v))
	return err == nil && b
}

// salesClaimGuardArmed 两把开关是否同时开（「注入/守卫全开」）。
// 参数：e=引擎（持有 assist 库配置读口）；返回是否全开。
func salesClaimGuardArmed(e *Engine) bool {
	if e == nil || e.db == nil {
		return false
	}
	return configOn(e.db.GetConfig(cfgSalesFactsInjection, "0")) &&
		configOn(e.db.GetConfig(cfgSalesClaimGuard, "0"))
}

// salesPageSegs 销售页路径段（公开营销/产品/定价面）。★ 刻意不收工作台/翻译/工单等 app 段，
// 免得把翻译/客服老链路误判成销售轮。加段前先想清楚它是不是「访客问产品」那一面。
var salesPageSegs = map[string]bool{
	"product": true, "products": true, "pricing": true, "price": true,
	"plan": true, "plans": true, "shop": true, "store": true,
	"buy": true, "checkout": true, "quote": true, "sales": true,
	"package": true, "packages": true, "subscribe": true,
	"solution": true, "solutions": true, "compare": true, "demo": true,
	"industry": true, "industries": true,
}

// salesProductPageShape 托管产品页 /p/{tenantCode}/{pageCode} 的形状（四期 W2 新增）。
// ★ 这是**形状判据**，刻意不往 salesPageSegs 里塞 "p"：
//   - 单字母段 "p" 一旦进整词表，任何形如 /x/p/y（翻译工作台、工单等 app 路径）都会
//     命中整词表被判成销售轮 ⇒ 守卫把老链路改写成回避话术（误伤付费主链）。
//   - 旧整词表还含 "demo"/"shop"/"product"/"sales" 等词，租户短码恰好叫这些的
//     /p/demo/x 会「按短码抽奖」被判销售页、叫 acme 的反而漏判——形状按首段锚定，与短码取值无关。
// 形态 ^/p/[^/]+/[^/]+$：首段为 p，后两段非空且不含 '/'（恰好两段短码）。
var salesProductPageShape = regexp.MustCompile(`^/p/[^/]+/[^/]+$`)

// isProductPagePath 判纯路径（已去协议/查询串/锚点）是否 /p/{tc}/{pc} 托管产品页形状。
// 参数：path=归一化后的纯路径；返回是否命中形状。
func isProductPagePath(path string) bool {
	return salesProductPageShape.MatchString(path)
}

// isSalesPageURL 判页面 URL 是否属销售页（路径段命中，或显式 ?sales=1 标记）。
// 参数：pageURL=挂件送来的当前页（可为完整 href 或纯路径）；返回是否销售页。
func isSalesPageURL(pageURL string) bool {
	s := strings.ToLower(strings.TrimSpace(pageURL))
	if s == "" {
		return false
	}
	if i := strings.Index(s, "?"); i >= 0 {
		q := s[i+1:]
		if j := strings.Index(q, "#"); j >= 0 {
			q = q[:j]
		}
		for _, kv := range strings.Split(q, "&") {
			if kv == "sales=1" || kv == "sales=true" || kv == "sales=on" {
				return true
			}
		}
		s = s[:i]
	}
	if i := strings.Index(s, "#"); i >= 0 {
		s = s[:i]
	}
	// 去协议+域名，只留路径再逐段判。
	if i := strings.Index(s, "://"); i >= 0 {
		rest := s[i+3:]
		if j := strings.Index(rest, "/"); j >= 0 {
			s = rest[j:]
		} else {
			s = "/"
		}
	}
	// ★ 四期 W2：/p/{tenantCode}/{pageCode} 托管产品页按形状判销售页（放在整词表之前，
	//   与租户短码取值无关；形状判据绝不误伤 /x/p/y，见 salesProductPageShape 注释）。
	if isProductPagePath(s) {
		return true
	}
	for _, seg := range strings.Split(s, "/") {
		if salesPageSegs[strings.TrimSpace(seg)] {
			return true
		}
	}
	return false
}

// salesTurn 判「本轮是否销售场景」（边界全文见文件头 ★★）。
// 参数：ctx=接待上下文；pageURL=当前页 URL；返回是否销售轮。
func salesTurn(ctx context.Context, pageURL string) bool {
	if strings.TrimSpace(pageURL) != "" {
		return isSalesPageURL(pageURL)
	}
	_, ok := mainengine.SalesFactsFromContext(ctx)
	return ok
}

// touchesControlledKey 模型输出是否提及任一受控键（受控值一个字节都不在集里，只比主题 key）。
// 参数：text=模型输出正文；controlled=受控键清单；返回是否触及。
func touchesControlledKey(text string, controlled []mainengine.ControlledKey) bool {
	low := strings.ToLower(text)
	for _, c := range controlled {
		k := strings.ToLower(strings.TrimSpace(c.Key))
		if len(k) >= 2 && strings.Contains(low, k) {
			return true
		}
	}
	return false
}

// evasionActions 「转其他话题」的安全入口：chat/pricing/register 中剔除当前话题相关 key。
// 参数：set=事实集（取受控键）；rep=原回复（取其原有按钮）；返回安全入口列表。
func (e *Engine) evasionActions(set mainengine.SalesFactsSet, rep *Reply) []Action {
	topic := map[string]bool{}
	for _, c := range set.Controlled {
		topic[strings.ToLower(strings.TrimSpace(c.Key))] = true
	}
	for _, a := range rep.Actions {
		topic[strings.ToLower(strings.TrimSpace(a.Key))] = true
	}
	out := []Action{}
	for _, a := range e.FeatureLinksByKey([]string{"chat", "pricing", "register"}) {
		if topic[strings.ToLower(strings.TrimSpace(a.Key))] {
			continue
		}
		out = append(out, a)
	}
	if len(out) == 0 {
		// 新装库 feature_links 缺行时仍给转场入口，绝不让「转其他话题」腿变成空按钮（M0233）。
		out = []Action{
			{Key: "chat", Name: "Chat", URL: "/chat", FType: "route"},
			{Key: "pricing", Name: "Pricing", URL: "/pricing", FType: "route"},
			{Key: "register", Name: "Register", URL: "/register", FType: "route"},
		}
	}
	return out
}

// guardSalesClaims 出站咽喉：销售轮命中任一档 ⇒ 回避 + 转其他话题 + PendingHuman。
// ★ 回避句语种按本轮作答语言（effectiveReplyLang 产物，由 uiLang 打底）选，缺省回落中文（AvoidPhrase）。
// 参数：ctx=接待上下文（可携带已验签 SalesFactsSet）；answerLang=本轮作答语言；
// pageURL=当前页 URL（销售场景判据）；rep=出站回复；返回处理后的回复。
func (e *Engine) guardSalesClaims(ctx context.Context, answerLang, pageURL string, rep *Reply) *Reply {
	if rep == nil || rep.Content == "" {
		return rep
	}
	// ⓪ 灰度：关 / 半套（只开一把）/ 缺省关 ⇒ 逐字节等价放行（A15）。
	if !salesClaimGuardArmed(e) {
		return rep
	}
	// ① 非销售场景（翻译/客服老链路）⇒ 零改动放行，绝不误伤。
	if !salesTurn(ctx, pageURL) {
		return rep
	}
	// ② 判据（fail-closed：任一命中都改写，绝不 fail-open 放行）。
	set, ok := mainengine.SalesFactsFromContext(ctx)
	reason := ""
	switch {
	case !ok:
		reason = "无事实头或验签失败"
	case len(set.Facts) == 0:
		reason = "事实集为空"
	case touchesControlledKey(rep.Content, set.Controlled):
		reason = "模型输出触及受控键"
	default:
		if pass, r := GuardLocalizedClaim(rep.Content, set); !pass {
			reason = r
		}
	}
	if reason == "" {
		return rep // 准入事实可对上：一字不动放行
	}
	// ③ 回避 + 转其他话题（不丢空、不编造、不放行；日志不含任何事实值）。
	phrase, fallback := AvoidPhrase(answerLang)
	if fallback {
		observability.Warn(ctx, "assist.engine 回避句未命中本轮语种，回落中文并留痕",
			"lang", canonicalLang(answerLang), "key", avoidPhraseKey)
	}
	observability.Warn(ctx, "assist.engine 销售声称守卫命中，改回避并置待人工接管",
		"lang", canonicalLang(answerLang), "reason", reason, "source", rep.Source)
	rep.Content = phrase
	rep.Actions = e.evasionActions(set, rep)
	rep.Source = "fallback"
	rep.PendingHuman = true // ★ 守卫命中 ⇒ 待人工接管（主服务按 message_id 行内落 reply_attributions）
	return rep
}
