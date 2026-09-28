// ============ oz_front_test.go · 职责说明 ============
// 〇-Z「站点门面开关」的策略引擎侧单测（internal/ops）：
//
//	A 默认档＝展示主页（主站零感知：新因子落地不能把现网首页关掉）。
//	B 显式 false 能关掉——指针布尔的意义就在这里；若做成裸 bool，Merge 会把"没设置"和
//	  "设成 false"混为一谈，演示站将永远关不掉主页。
//	C 缺省继承：下层补丁不带 front 时，上层的结果必须原样透传（平台关→租户没表态→仍关）。
//	D 时间窗禁止夹带 front.landing_enabled（与 billing/payment 同款后门拦截：
//	  门面是对外的决定，不能随日历窗口自动翻转）。
//	E JSON 往返：前端只发 {"front":{"landing_enabled":false}} 这一小段时，
//	  ParseOps 必须解出指针 false（而不是丢成 nil），保存→读回链路才成立。
//
// =============================================
package ops

import "testing"

// TestFrontLandingDefault 默认档必须＝展示主页（A）。
func TestFrontLandingDefault(t *testing.T) {
	eff := DefaultEffective()
	if !eff.Front.LandingEnabled {
		t.Fatalf("默认档应为展示主页，实际 landing_enabled=%v（主站首页会被误关）", eff.Front.LandingEnabled)
	}
}

// TestFrontLandingMerge 显式 false 覆盖＋缺省继承（B/C）。
func TestFrontLandingMerge(t *testing.T) {
	// B：默认档 + 平台补丁显式 false ＝ 关
	off := Merge(DefaultEffective(), OperationsPolicy{Front: FrontPatch{LandingEnabled: boolPtr(false)}})
	if off.Front.LandingEnabled {
		t.Fatal("显式 landing_enabled=false 未生效：指针布尔被当成未设置")
	}
	// B 反向：显式 true 也必须是 true（防 Merge 写成"只处理 false"的单向分支）
	on := Merge(DefaultEffective(), OperationsPolicy{Front: FrontPatch{LandingEnabled: boolPtr(true)}})
	if !on.Front.LandingEnabled {
		t.Fatal("显式 landing_enabled=true 应为主页开放")
	}
	// C：平台关 → 租户补丁不含 front → 仍关（缺省继承，不能被下层"洗回"true）
	stillOff := Merge(off, OperationsPolicy{})
	if stillOff.Front.LandingEnabled {
		t.Fatal("下层空补丁把已关闭的门面洗回了开放（Merge 缺省继承失效）")
	}
	// C 反向：平台开 → 租户补丁不表态 → 仍开
	stillOn := Merge(DefaultEffective(), OperationsPolicy{})
	if !stillOn.Front.LandingEnabled {
		t.Fatal("空补丁不应改变默认开放档")
	}
	// Merge 不得改写 base（C8 克隆口径的延伸：门面是标量，浅拷贝天然安全，但仍钉一层防回归）
	base := DefaultEffective()
	_ = Merge(base, OperationsPolicy{Front: FrontPatch{LandingEnabled: boolPtr(false)}})
	if !base.Front.LandingEnabled {
		t.Fatal("Merge 污染了上层 base（应为不可变叠加）")
	}
}

// TestFrontLandingWindowBan 时间窗不得夹带门面开关（D）。
func TestFrontLandingWindowBan(t *testing.T) {
	w := PromoWindow{ID: "oz", Overrides: OperationsPolicy{Front: FrontPatch{LandingEnabled: boolPtr(false)}}}
	if err := ValidateWindowOverrides(w); err == nil {
		t.Fatal("front.landing_enabled 竟可通过时间窗夹带——门面开关必须显式操作")
	}
	// 反向对照：窗口不碰门面时必须仍通过（别把拦截写成"窗口一律拒绝"）
	if err := ValidateWindowOverrides(PromoWindow{ID: "oz2"}); err != nil {
		t.Fatalf("合规窗口被误伤: %v", err)
	}
	if err := ValidatePolicyWindows(OperationsPolicy{PromoWindows: []PromoWindow{w}}); err == nil {
		t.Fatal("整策略保存路径未拦截门面后门")
	}
}

// TestFrontLandingJSONRoundTrip 前端只发这一小段时的解析与回写（E）。
func TestFrontLandingJSONRoundTrip(t *testing.T) {
	p := ParseOps(`{"front":{"landing_enabled":false}}`)
	if p.Front.LandingEnabled == nil {
		t.Fatal(`ParseOps 未解析出 landing_enabled=false（omitempty 或字段名写错）`)
	}
	if *p.Front.LandingEnabled {
		t.Fatal("解析出的值应为 false")
	}
	// 未出现该键时必须保持 nil（＝不表态），否则 Merge 会把默认档覆盖掉
	q := ParseOps(`{"billing":{"markup_multiplier":1.6}}`)
	if q.Front.LandingEnabled != nil {
		t.Fatal("未配置门面时不应凭空产生表态")
	}
}
