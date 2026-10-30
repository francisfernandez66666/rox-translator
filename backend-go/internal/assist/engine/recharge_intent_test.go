// ============================================================================
// recharge_intent_test.go — 充值意图双向与抑制五档（★ 2026-10-30 组7 §8.3，G31 / R-1…R-4）
//
// 覆盖：
//   - 12 语种各一条「想充值/额度不够」⇒ 出充值入口；
//   - 12 语种各一条无关问题 ⇒ **零**充值入口（R-4 负向锁）；
//   - 抑制五档各一腿（刚充值/余额够 3 天/套餐在效/同会话 3 分钟/本轮在推工单）；
//   - 模型自己输出 【go:recharge】 也算有意图（R-1 第二条）；
//   - 入口只在 feature_links 有 recharge 行时出现（URL 只来自库）。
//
// ============================================================================
package engine

import (
	"context"
	"testing"
	"time"

	"translator/internal/assist/profile"
)

// withRechargeLink 建一条充值入口（与 seed.json 的 key 逐字一致）。
func withRechargeLink(t *testing.T, e *Engine) {
	t.Helper()
	if _, err := e.db.Create("feature_links", map[string]any{
		"key": "recharge", "name": "充值积分", "url": "/billing", "ftype": "route", "icon": "💳", "sort": 41, "enabled": 1,
	}); err != nil {
		t.Fatalf("建 recharge 入口失败: %v", err)
	}
}

func hasKey(acts []Action, key string) bool {
	for _, a := range acts {
		if a.Key == key {
			return true
		}
	}
	return false
}

// TestRechargeIntentTwelveLanguages G31 正向：12 语种各一条意图句都出卡。
func TestRechargeIntentTwelveLanguages(t *testing.T) {
	e := newTestEngine(t)
	withRechargeLink(t, e)
	cases := map[string]string{
		"zh":      "我想充值积分",
		"zh_hant": "我想充值",
		"en":      "I want to top up my credits",
		"ja":      "チャージしたいです",
		"ko":      "충전하고 싶어요",
		"fr":      "Je veux recharger mon compte",
		"de":      "Ich möchte mein Guthaben aufladen",
		"es":      "Quiero recargar mi saldo",
		"pt":      "Quero recarregar meu saldo",
		"ru":      "Я хочу пополнить баланс",
		"ar":      "أريد شحن رصيد",
		"th":      "ฉันต้องการเติมเงิน",
	}
	for lang, input := range cases {
		out := e.applyRechargeIntent(context.Background(), "s-"+lang, input, &Reply{Source: "llm"})
		if !hasKey(out.Actions, rechargeActionKey) {
			t.Fatalf("%s 意图句未出充值入口：%q（actions=%+v）", lang, input, out.Actions)
		}
	}
}

// TestRechargeIntentUnrelatedNoEntry G31 负向（R-4）：无关问题零充值入口。
func TestRechargeIntentUnrelatedNoEntry(t *testing.T) {
	e := newTestEngine(t)
	withRechargeLink(t, e)
	cases := map[string]string{
		"zh":      "今天天气怎么样",
		"zh_hant": "幫我翻譯這句話",
		"en":      "How are you",
		"ja":      "今日はいい天気ですね",
		"ko":      "오늘 날씨 어때",
		"fr":      "Quel temps fait-il",
		"de":      "Wie geht es dir",
		"es":      "Hola mundo",
		"pt":      "Bom dia",
		"ru":      "Я люблю чай",
		"ar":      "الطقس جميل",
		"th":      "สวัสดี",
	}
	for lang, input := range cases {
		out := e.applyRechargeIntent(context.Background(), "n-"+lang, input, &Reply{Source: "llm"})
		if hasKey(out.Actions, rechargeActionKey) {
			t.Fatalf("%s 无关句不该出充值入口：%q（actions=%+v）", lang, input, out.Actions)
		}
	}
}

// TestRechargeSuppressionFiveBranches 抑制五档各一腿。
func TestRechargeSuppressionFiveBranches(t *testing.T) {
	e := newTestEngine(t)
	withRechargeLink(t, e)
	cue := "我想充值"
	now := time.Now()

	// ① 24h 内刚充值过。
	ctx1 := WithVisitorProfile(context.Background(), &profile.Visitor{UID: 1, RechargedRecently: true})
	if out := e.applyRechargeIntent(ctx1, "a", cue, &Reply{Source: "llm"}); hasKey(out.Actions, rechargeActionKey) {
		t.Fatal("刚充值过仍推了充值入口")
	}
	// ② 余额 ≥ 3 天用量（今日已用 1000 ⇒ 余额 3000 正好够 3 天）。
	ctx2 := WithVisitorProfile(context.Background(), &profile.Visitor{UID: 2, PointsBalance: 3000, PointsUsedToday: 1000})
	if out := e.applyRechargeIntent(ctx2, "b", cue, &Reply{Source: "llm"}); hasKey(out.Actions, rechargeActionKey) {
		t.Fatal("余额够 3 天仍推了充值入口")
	}
	// ③ 在效套餐未到期。
	ctx3 := WithVisitorProfile(context.Background(), &profile.Visitor{
		UID: 3, PackageCode: "pro", PackageExpires: now.Add(72 * time.Hour).Format(time.RFC3339),
	})
	if out := e.applyRechargeIntent(ctx3, "c", cue, &Reply{Source: "llm"}); hasKey(out.Actions, rechargeActionKey) {
		t.Fatal("套餐在效仍推了充值入口")
	}
	// ④ 同一会话 3 分钟内已推过：第一次允许，第二次抑制。
	if out := e.applyRechargeIntent(context.Background(), "same", cue, &Reply{Source: "llm"}); !hasKey(out.Actions, rechargeActionKey) {
		t.Fatal("第一次应允许出充值入口（否则第 4 档判据无从成立）")
	}
	if out := e.applyRechargeIntent(context.Background(), "same", cue, &Reply{Source: "llm"}); hasKey(out.Actions, rechargeActionKey) {
		t.Fatal("3 分钟内重复推送未被抑制")
	}
	// ⑤ 本轮已经在推工单。
	rep5 := &Reply{Source: "llm", Actions: []Action{{Key: "tickets", Name: "文件翻译"}}}
	if out := e.applyRechargeIntent(context.Background(), "e", cue, rep5); hasKey(out.Actions, rechargeActionKey) {
		t.Fatal("本轮已推工单仍加了充值入口")
	}
}

// TestRechargeModelIntentCounts R-1 第二条：模型自己输出 recharge 也算有意图；
// 而无意图时模型自作主张加的那一枚必须被摘掉（R-4）。
func TestRechargeModelIntentCounts(t *testing.T) {
	e := newTestEngine(t)
	withRechargeLink(t, e)
	// 无关句 + 模型给了 recharge ⇒ 视为模型判定有意图，保留。
	rep := &Reply{Source: "llm", Actions: []Action{{Key: rechargeActionKey, Name: "充值积分", URL: "/billing"}}}
	out := e.applyRechargeIntent(context.Background(), "m1", "今天天气怎么样", rep)
	if !hasKey(out.Actions, rechargeActionKey) {
		t.Fatal("模型判定的购买意图被误删")
	}

	// 已被抑制时，模型给的那一枚也要摘（同一函数决定去留）。
	ctx := WithVisitorProfile(context.Background(), &profile.Visitor{UID: 9, RechargedRecently: true})
	rep2 := &Reply{Source: "llm", Actions: []Action{{Key: rechargeActionKey}, {Key: "pricing"}}}
	out2 := e.applyRechargeIntent(ctx, "m2", "我想充值", rep2)
	if hasKey(out2.Actions, rechargeActionKey) {
		t.Fatal("被抑制时模型自加的充值入口未被摘掉")
	}
	if !hasKey(out2.Actions, "pricing") {
		t.Fatal("摘入口不许误删其它按钮")
	}
}

// TestRechargeNoFeatureLinkNoEntry URL/文案只来自 feature_links：图里没有就不出入口。
func TestRechargeNoFeatureLinkNoEntry(t *testing.T) {
	e := newTestEngine(t) // 未建 recharge 行
	out := e.applyRechargeIntent(context.Background(), "x", "我想充值", &Reply{Source: "llm"})
	if hasKey(out.Actions, rechargeActionKey) {
		t.Fatal("feature_links 无 recharge 行时不得凭空造入口")
	}
}
