// ============ quality_test.go · 职责说明 ============
// 「复核链路（quality）」策略因子单测（★ 2026-11-21 用户令）：
//
//	A 出厂默认＝两档全开（保持现行为：存量部署不能因为新因子落地而悄悄降一档质量）。
//	B 显式 false 能关——指针布尔的意义；做成裸 bool 时 Merge 会把"没设置"与"设成 false"
//	  混为一谈，运营永远关不掉。
//	C 两档**互相独立**：只关即时不能连工单一起关（这正是拆成两个字段的理由）。
//	D 缺省继承：下层空补丁不许把上层关掉的档位洗回开。
//	E 时间窗禁止夹带 quality.*（与 billing/payment/front/instant 同款后门拦截）。
//	F QualityOf 是运行期唯一出口：空串／非法 JSON／缺键 ⇒ 出厂默认（全开），
//	  不许出现"读不到就按关处理"（那会把一次库抖动打成全站降级）。
//
// =============================================
package ops

import "testing"

// TestQualityDefaultsAllOn 出厂档两档全开（A）。
func TestQualityDefaultsAllOn(t *testing.T) {
	q := DefaultEffective().Quality
	if !q.InstantReviewEnabled || !q.TicketReviewEnabled {
		t.Fatalf("出厂档应为两档全开，实际 instant=%v ticket=%v（存量部署会静默降一档质量）",
			q.InstantReviewEnabled, q.TicketReviewEnabled)
	}
	// 运行期唯一出口在"什么都没配"时也必须回全开
	if got := QualityOf(""); !got.InstantReviewEnabled || !got.TicketReviewEnabled {
		t.Fatalf("QualityOf(空串) 应为全开，实际 %+v", got)
	}
	if got := QualityOf(`{"version":3}`); !got.InstantReviewEnabled || !got.TicketReviewEnabled {
		t.Fatalf("QualityOf(无 quality 段) 应为全开，实际 %+v", got)
	}
	if got := QualityOf(`{ 这不是 json`); !got.InstantReviewEnabled || !got.TicketReviewEnabled {
		t.Fatalf("QualityOf(非法 JSON) 应为全开，实际 %+v", got)
	}
}

// TestQualityMergeExplicitFalse 显式 false 生效、true 也生效、两档互不牵连（B/C）。
func TestQualityMergeExplicitFalse(t *testing.T) {
	// B：只关即时
	onlyInstantOff := Merge(DefaultEffective(), OperationsPolicy{Quality: QualityPatch{
		InstantReviewEnabled: boolPtr(false),
	}})
	if onlyInstantOff.Quality.InstantReviewEnabled {
		t.Fatal("显式 instant_review_enabled=false 未生效：指针布尔被当成未设置")
	}
	// C：工单那档不许被连带关掉——拆两个字段的全部意义就在这里
	if !onlyInstantOff.Quality.TicketReviewEnabled {
		t.Fatal("只关即时档却把工单档一起关掉了（两档耦合＝运营无法只降即时成本）")
	}

	// B/C 反向：只关工单
	onlyTicketOff := Merge(DefaultEffective(), OperationsPolicy{Quality: QualityPatch{
		TicketReviewEnabled: boolPtr(false),
	}})
	if onlyTicketOff.Quality.TicketReviewEnabled {
		t.Fatal("显式 ticket_review_enabled=false 未生效")
	}
	if !onlyTicketOff.Quality.InstantReviewEnabled {
		t.Fatal("只关工单档却把即时档一起关掉了")
	}

	// B 反向对照：显式 true 也必须是 true（防 Merge 写成"只处理 false"的单向分支）
	on := Merge(DefaultEffective(), OperationsPolicy{Quality: QualityPatch{
		InstantReviewEnabled: boolPtr(true), TicketReviewEnabled: boolPtr(true),
	}})
	if !on.Quality.InstantReviewEnabled || !on.Quality.TicketReviewEnabled {
		t.Fatal("显式 true 应保持开启")
	}

	// D：平台关 → 下层空补丁 → 仍关（缺省继承，不能被"没表态"洗回开）
	stillOff := Merge(onlyInstantOff, OperationsPolicy{})
	if stillOff.Quality.InstantReviewEnabled {
		t.Fatal("下层空补丁把已关闭的复核链路洗回了开启（Merge 缺省继承失效）")
	}

	// Merge 不得改写 base
	base := DefaultEffective()
	_ = Merge(base, OperationsPolicy{Quality: QualityPatch{InstantReviewEnabled: boolPtr(false)}})
	if !base.Quality.InstantReviewEnabled {
		t.Fatal("Merge 污染了上层 base（应为不可变叠加）")
	}
}

// TestQualityWindowBan 时间窗不得夹带复核链路开关（E）。
func TestQualityWindowBan(t *testing.T) {
	for _, p := range []QualityPatch{
		{InstantReviewEnabled: boolPtr(false)},
		{TicketReviewEnabled: boolPtr(false)},
		{InstantReviewEnabled: boolPtr(false), TicketReviewEnabled: boolPtr(false)},
	} {
		w := PromoWindow{ID: "q", Overrides: OperationsPolicy{Quality: p}}
		if err := ValidateWindowOverrides(w); err == nil {
			t.Fatalf("quality 因子（%+v）竟可通过时间窗夹带——复核链路必须显式操作", p)
		}
	}
	// 反向对照：窗口不碰复核链路时必须仍通过（别把拦截写成"窗口一律拒绝"）
	if err := ValidateWindowOverrides(PromoWindow{ID: "q2"}); err != nil {
		t.Fatalf("合规窗口被误伤: %v", err)
	}
	// 整策略保存路径同样要拦
	w := PromoWindow{ID: "q3", Overrides: OperationsPolicy{Quality: QualityPatch{TicketReviewEnabled: boolPtr(false)}}}
	if err := ValidatePolicyWindows(OperationsPolicy{PromoWindows: []PromoWindow{w}}); err == nil {
		t.Fatal("整策略保存路径未拦截复核链路后门")
	}
}

// TestQualityJSONRoundTrip 前端只发这一小段时的解析与回写（F 的邻面）。
func TestQualityJSONRoundTrip(t *testing.T) {
	p := ParseOps(`{"quality":{"instant_review_enabled":false}}`)
	if p.Quality.InstantReviewEnabled == nil {
		t.Fatal(`ParseOps 未解析出 instant_review_enabled=false（omitempty 或字段名写错）`)
	}
	if *p.Quality.InstantReviewEnabled {
		t.Fatal("解析出的值应为 false")
	}
	// 未出现的键必须保持 nil（＝不表态），否则 Merge 会把另一档的默认值覆盖掉
	if p.Quality.TicketReviewEnabled != nil {
		t.Fatal("未配置工单档时不应凭空产生表态")
	}
	// 只发工单档时同理
	q := ParseOps(`{"quality":{"ticket_review_enabled":false}}`)
	if q.Quality.TicketReviewEnabled == nil || *q.Quality.TicketReviewEnabled {
		t.Fatal(`ParseOps 未解析出 ticket_review_enabled=false`)
	}
	if q.Quality.InstantReviewEnabled != nil {
		t.Fatal("未配置即时档时不应凭空产生表态")
	}
	// 走一遍 QualityOf 的完整链路：显式关 + 另一档缺省 ⇒ 一关一开
	eff := QualityOf(`{"quality":{"instant_review_enabled":false}}`)
	if eff.InstantReviewEnabled || !eff.TicketReviewEnabled {
		t.Fatalf("QualityOf 组合结果错误: %+v", eff)
	}
}
