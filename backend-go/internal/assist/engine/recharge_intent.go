// ============ recharge_intent.go · 职责说明 ============
// 充值意图识别与「只在该出手时才出手」的抑制（★ 2026-10-30 三事合一批 组7 §8.3，R-1…R-5）。
//
// 两条判据取「或」（R-1）：
//
//	① 访客这句话命中 rechargeIntentCues（12 语种线索词，**单独成表，不进 culture 敏感词表**——
//	   那是避雷，不是意图；词表混用会把「要不要花钱」当敏感词拦掉，也会把避雷词当购买信号）；
//	② 模型自己输出了 【go:recharge】 控制序列（模型判定有购买意图；它出现在 rep.Actions 里）。
//
// 抑制五档（R-3）收在**一个函数** `rechargeSuppressed`，四个回复分支共享同一处判据：
//  1. 24h 内刚充值过（画像的 recharged_recently，主服务按 orders 现读）；
//  2. 余额 ≥ 3 天用量（按本月日均 / 今日用量取大者估一天；新用户日均 0 ⇒ 不抑制）；
//  3. 有在效套餐且未到期（含"不限期"套餐）；
//  4. 同一会话 3 分钟内已推过（进程内记录；重启清零，最坏后果是多推一次）；
//  5. 本轮已经在推工单（回复里已有 tickets 按钮——"交给工单"是比充值更正确的下一步）。
//
// R-4 的负向口径：**无意图轮次零充值入口**。所以本函数对每一轮都做一次「是否保留 recharge 按钮」
// 的显式决定——不是只在命中时追加，还要在未命中/被抑制时**摘掉**模型自作主张加进来的那一枚。
// R-5：只给一个下一步（按钮），不写折扣、不写「限时」，正文仍由模型按画像与系统现值组织。
//
// 出口只有一个：feature_links 里 key=recharge 的那一行（URL 只来自库，模型不许自编 URL）。
// =============================================
package engine

import (
	"context"
	"strings"
	"time"

	"translator/internal/assist/profile"
	"translator/internal/observability"
)

// rechargeActionKey 充值入口在 feature_links 里的唯一 key（与 seed.json 逐字一致）。
// 模型只许输出 【go:recharge】；URL/文案一律由图里的那一行决定。
const rechargeActionKey = "recharge"

// rechargePushGap 同一会话两次充值引导的最小间隔（R-3 第 4 档）。
const rechargePushGap = 3 * time.Minute

// rechargeIntentCues 购买/充值意图线索（小写；拉丁词按词边界匹配，CJK/阿拉伯/泰文按子串）。
//
// ★ 刻意不收的短词：「plan」（"plan a trip" 会误判）、「cost」（"cost of living" 闲聊）、
// 「balance」（"balance sheet" 是财务正文）。词表宁可漏一点，也不要把无关轮次变成催缴。
// ★ 这张表**不进 culture 敏感词表**：两张表射程不同（一个是意图信号、一个是输出避雷）。
var rechargeIntentCues = []string{
	// 中文（含繁体常用写法）
	"充值", "充钱", "续费", "付费", "购买", "买积分", "升级套餐", "加量", "额度不够", "余额不够",
	"不够用", "多少钱", "怎么付", "付款", "发票", "套餐", "太贵",
	// English
	"top up", "top-up", "recharge", "payment", "pay for", "upgrade", "pricing", "price", "invoice",
	"subscription", "subscribe", "purchase", "credits", "quota", "not enough",
	// 日本語
	"チャージ", "入金", "支払", "購入", "アップグレード", "料金", "価格", "請求", "残高", "足りない",
	// 한국어
	"충전", "결제", "구매", "업그레이드", "가격", "요금", "청구", "잔액", "부족",
	// Français
	"recharger", "paiement", "payer", "acheter", "achat", "prix", "tarif", "facture", "abonnement", "solde",
	// Deutsch
	"aufladen", "bezahlen", "zahlung", "kaufen", "preis", "rechnung", "abonnement", "guthaben",
	// Español
	"recargar", "pagar", "pago", "comprar", "precio", "factura", "suscripción", "saldo",
	// Português
	"recarregar", "pagamento", "comprar", "preço", "fatura", "assinatura", "saldo",
	// Русский
	"пополнить", "оплатить", "оплата", "купить", "обновить", "цена", "счёт", "подписка", "баланс",
	// العربية
	"شحن", "دفع", "شراء", "ترقية", "سعر", "فاتورة", "اشتراك", "رصيد",
	// ไทย
	"เติมเงิน", "ชำระ", "ซื้อ", "อัปเกรด", "ราคา", "ใบแจ้งหนี้", "สมัคร", "ยอดคงเหลือ",
}

// rechargeIntentHit 判断一句话是否命中充值意图线索。
func rechargeIntentHit(input string) bool {
	s := strings.ToLower(strings.TrimSpace(input))
	if s == "" {
		return false
	}
	for _, cue := range rechargeIntentCues {
		if cue == "" {
			continue
		}
		if isLatinCue(cue) {
			if containsLatinWord(s, cue) {
				return true
			}
			continue
		}
		// CJK / 阿拉伯 / 泰文：没有"词"的概念，按子串。
		if strings.Contains(s, cue) {
			return true
		}
	}
	return false
}

// isLatinCue 纯 ASCII 字母/空格/连字符的线索词走词边界（AGENTS §18 那条拉丁短词的教训）。
func isLatinCue(s string) bool {
	for _, r := range s {
		if r > 127 {
			return false
		}
	}
	return true
}

// containsLatinWord 带词边界的子串查找：`pay` 不该命中 `repaymentx` 之外的巧合，
// 但 `pay for`/`top up` 这种多词短语照常按整体匹配。判据只看两侧是不是拉丁字母/数字。
func containsLatinWord(s, w string) bool {
	if w == "" {
		return false
	}
	from := 0
	for {
		i := strings.Index(s[from:], w)
		if i < 0 {
			return false
		}
		i += from
		beforeOK := i == 0 || !isWordByte(s[i-1])
		after := i + len(w)
		afterOK := after >= len(s) || !isWordByte(s[after])
		if beforeOK && afterOK {
			return true
		}
		from = i + 1
		if from >= len(s) {
			return false
		}
	}
}

// isWordByte 判拉丁词边界用的字符类：字母/数字/下划线算词内，其余算分隔（与 ⑲ 守卫同一口径）。
func isWordByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= '0' && b <= '9'
}

// applyRechargeIntent 是本轮「要不要给充值入口」的**唯一判据**。
// 放在 Respond 咽喉上（补翻/守卫之后、按钮本地化之前），四道回复分支自动同享。
func (e *Engine) applyRechargeIntent(ctx context.Context, sessionID, input string, rep *Reply) *Reply {
	if rep == nil {
		return rep
	}
	cue := rechargeIntentHit(input)
	modelEmitted := hasActionKey(rep.Actions, rechargeActionKey)
	if !cue && !modelEmitted {
		// R-4：无意图轮次零充值入口（把模型自作主张加的那一枚也摘掉）。
		rep.Actions = dropActionKey(rep.Actions, rechargeActionKey)
		return rep
	}
	now := time.Now()
	if e.rechargeSuppressed(ctx, sessionID, rep, now) {
		rep.Actions = dropActionKey(rep.Actions, rechargeActionKey)
		return rep
	}
	if !modelEmitted {
		if acts := e.FeatureLinksByKey([]string{rechargeActionKey}); len(acts) > 0 {
			rep.Actions = append(rep.Actions, acts...)
		} else {
			// 图里没有这一行（运营未同步 seed）⇒ 不出入口，但要看得到。
			observability.Warn(ctx, "assist.engine 充值意图命中但 feature_links 缺 recharge 行，本轮不出入口",
				"session", sessionID)
			return rep
		}
	}
	e.markRechargePushed(sessionID, now)
	return rep
}

// rechargeSuppressed 抑制五档（R-3），全部收在这一个函数里。
// 返回 true 表示「即使有意图也不推」。
func (e *Engine) rechargeSuppressed(ctx context.Context, sessionID string, rep *Reply, now time.Time) bool {
	v := VisitorProfileFromCtx(ctx)
	// ① 24h 内刚充值过。
	if v != nil && v.RechargedRecently {
		return true
	}
	// ② 余额 ≥ 3 天用量。日均取「本月已用 / 本月已过天数」与「今日已用」的较大者；
	//    两者都为 0（新用户/没跑过量）⇒ 无法估日用，不抑制——否则所有新用户都看不到入口。
	if v != nil {
		if daily := estimatedDailyPoints(v, now); daily > 0 && v.PointsBalance >= 3*daily {
			return true
		}
		// ③ 有在效套餐且未到期（"不限期"也算在效）。
		if rechargePackageActive(v, now) {
			return true
		}
	}
	// ④ 同一会话 3 分钟内已推过。
	if e.rechargePushedRecently(sessionID, now) {
		return true
	}
	// ⑤ 本轮已经在推工单。
	if hasActionKey(rep.Actions, "tickets") {
		return true
	}
	return false
}

// estimatedDailyPoints 估算「一天的用量」（积分）。只用于抑制判定，不对外出栈。
func estimatedDailyPoints(v *profile.Visitor, now time.Time) int64 {
	if v == nil {
		return 0
	}
	day := int64(now.UTC().Day())
	if day < 1 {
		day = 1
	}
	avg := v.PointsUsedMonth / day
	if v.PointsUsedToday > avg {
		return v.PointsUsedToday
	}
	return avg
}

// rechargePackageActive 套餐是否仍在有效期内。
// PackageExpires 空＝不限期（在效）；解析失败＝判不出来，按「不抑制」处理（宁可多给一个入口，
// 也不要把一个已到期的用户永久挡在充值入口外）。
func rechargePackageActive(v *profile.Visitor, now time.Time) bool {
	if v == nil {
		return false
	}
	code := strings.TrimSpace(v.PackageCode)
	if code == "" || code == "trial" {
		return false
	}
	exp := strings.TrimSpace(v.PackageExpires)
	if exp == "" {
		return true
	}
	t, err := time.Parse(time.RFC3339, exp)
	if err != nil {
		return false
	}
	return t.After(now)
}

// hasActionKey 回复里是否已有某个入口。
func hasActionKey(acts []Action, key string) bool {
	for _, a := range acts {
		if a.Key == key {
			return true
		}
	}
	return false
}

// dropActionKey 摘掉某个入口（保留其余按钮与顺序）。
func dropActionKey(acts []Action, key string) []Action {
	if len(acts) == 0 {
		return acts
	}
	out := acts[:0]
	for _, a := range acts {
		if a.Key != key {
			out = append(out, a)
		}
	}
	return out
}

// rechargePushedRecently 读「这个会话最近是否已推过」（顺带清理过期项，防 map 无限增长）。
func (e *Engine) rechargePushedRecently(sessionID string, now time.Time) bool {
	if sessionID == "" || e == nil {
		return false
	}
	e.rechargeMu.Lock()
	defer e.rechargeMu.Unlock()
	for k, t := range e.rechargePushed {
		if now.Sub(t) > 10*rechargePushGap {
			delete(e.rechargePushed, k)
		}
	}
	t, ok := e.rechargePushed[sessionID]
	return ok && now.Sub(t) < rechargePushGap
}

// markRechargePushed 记录本会话的推送时刻（只影响第 4 档抑制）。
func (e *Engine) markRechargePushed(sessionID string, now time.Time) {
	if sessionID == "" || e == nil {
		return
	}
	e.rechargeMu.Lock()
	defer e.rechargeMu.Unlock()
	if e.rechargePushed == nil {
		e.rechargePushed = map[string]time.Time{}
	}
	e.rechargePushed[sessionID] = now
}
