// ============ 本文件职责中文说明 ============
// policy_test.go · 运营策略引擎因子配置单测
// 覆盖《改造方案_计费流程引擎因子配置.md》§9.1：
//
//	默认值 / 解析容错 / 两层合并 / 显式关闭布尔 / 时间窗命中与优先级 / 租户白名单 / 模式回落。
//
// =============================================
package ops

import (
	"testing"
	"time"
)

// TestDefaultEffective 默认策略与存量散键现值一致。
func TestDefaultEffective(t *testing.T) {
	d := DefaultEffective()
	if !d.Enforced || d.MarkupMultiplier != 1.5 {
		t.Fatalf("默认强制计费/系数错误: enforced=%v markup=%v", d.Enforced, d.MarkupMultiplier)
	}
	if d.Package.TrialTokens != 300000 || d.Package.TrialDays != 14 {
		t.Fatalf("默认体验额度错误: %+v", d.Package)
	}
	if d.Invite.RewardTokens != 300000 || d.Invite.RewardDays != 14 || d.Invite.MaxDailyRewards != 50 {
		t.Fatalf("默认邀请因子错误: %+v", d.Invite)
	}
	if r, ok := d.Mode("fast"); !ok || !r.Charge {
		t.Fatalf("默认 fast 应收费: %+v ok=%v", r, ok)
	}
}

// TestParseOpsTolerant 解析容错：空/非法回零值，显式 false 可解析。
func TestParseOpsTolerant(t *testing.T) {
	if p := ParseOps(""); p.Billing.Enforced != nil || p.PromoWindows != nil {
		t.Fatal("空串应解析为零值策略")
	}
	if p := ParseOps("not-json{"); p.Billing.ModeRules != nil {
		t.Fatal("非法 JSON 应解析为零值策略")
	}
	p := ParseOps(`{"billing":{"mode_rules":{"fast":{"charge":false,"limit_chars":500}}}}`)
	r := p.Billing.ModeRules["fast"]
	if r.Charge == nil || *r.Charge {
		t.Fatal("显式 charge=false 应解析为 false")
	}
	if r.LimitChars == nil || *r.LimitChars != 500 {
		t.Fatalf("limit_chars 解析错误: %+v", r.LimitChars)
	}
}

// TestMergeTenantOverride 两层合并：租户覆盖生效、未覆盖回落；enabled=false 可显式关闭。
func TestMergeTenantOverride(t *testing.T) {
	base := DefaultEffective()
	// 租户覆盖：fast 免费 + 上限 300；未覆盖的 pro 保持收费
	tenant := ParseOps(`{"billing":{"mode_rules":{"fast":{"charge":false,"limit_chars":300}}}}`)
	eff := Merge(base, tenant)
	fast, _ := eff.Mode("fast")
	if fast.Charge || fast.LimitChars != 300 {
		t.Fatalf("租户覆盖未生效: %+v", fast)
	}
	pro, _ := eff.Mode("pro")
	if !pro.Charge {
		t.Fatal("未覆盖的 pro 应回落收费")
	}
	// 显式关闭模式
	eff2 := Merge(base, ParseOps(`{"billing":{"mode_rules":{"fast":{"enabled":false}}}}`))
	f2, _ := eff2.Mode("fast")
	if f2.Enabled {
		t.Fatal("enabled=false 应显式关闭")
	}
}

// TestModeFallback 空模式回落 pro 语义。
func TestModeFallback(t *testing.T) {
	eff := Merge(DefaultEffective(), ParseOps(`{"billing":{"mode_rules":{"pro":{"charge":true}}}}`))
	if _, ok := eff.Mode(""); !ok {
		t.Fatal("空模式应回落 pro")
	}
	r, _ := eff.Mode("")
	if !r.Charge {
		t.Fatal("空模式回落 pro 应收费")
	}
}

// TestActiveWindowsPriority 时间窗命中与优先级排序。
func TestActiveWindowsPriority(t *testing.T) {
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	ws := []PromoWindow{
		{ID: "low", Start: "2026-09-01", End: "2026-09-30", Priority: 5,
			Overrides: ParseOps(`{"invite":{"reward_tokens":400000}}`)},
		{ID: "high", Start: "2026-09-01", End: "2026-09-30", Priority: 10,
			Overrides: ParseOps(`{"invite":{"reward_tokens":600000}}`)},
		{ID: "past", Start: "2026-08-01", End: "2026-08-31"}, // 已过期
	}
	hit := ActiveWindows(ws, now, "Asia/Shanghai")
	if len(hit) != 2 {
		t.Fatalf("应命中 2 个窗口, 实得 %d", len(hit))
	}
	if hit[0].ID != "high" {
		t.Fatalf("priority 高者应排前, 实得 %s", hit[0].ID)
	}
	// 窗口覆盖叠加：ActiveWindows 返回 priority 降序，倒序应用使高优先级最后生效（赢）
	eff := DefaultEffective()
	for i := len(hit) - 1; i >= 0; i-- {
		eff = Merge(eff, hit[i].Overrides)
	}
	if eff.Invite.RewardTokens != 600000 {
		t.Fatalf("窗口叠加应取高优先级 600000, 实得 %d", eff.Invite.RewardTokens)
	}
}

// TestWindowTimesValid 起止校验。
func TestWindowTimesValid(t *testing.T) {
	if !WindowTimesValid(PromoWindow{Start: "2026-09-01", End: "2026-09-30"}, "Asia/Shanghai") {
		t.Fatal("合法窗口应通过")
	}
	if WindowTimesValid(PromoWindow{Start: "2026-09-30", End: "2026-09-01"}, "Asia/Shanghai") {
		t.Fatal("start≥end 应拒绝")
	}
	if WindowTimesValid(PromoWindow{Start: "garbage", End: "2026-09-30"}, "Asia/Shanghai") {
		t.Fatal("非法时间应拒绝")
	}
}
