// c8_merge_test.go · ★ C8 Merge 深拷贝回归：补丁不得改写 base 的 ModeRules。
package ops

import "testing"

func TestMergeDoesNotMutateBase(t *testing.T) {
	base := EffectivePolicy{ModeRules: map[string]ModeRule{
		"fast": {Enabled: true, Charge: true, Markup: 1.0},
	}}
	f := false
	patch := OperationsPolicy{}
	patch.Billing.ModeRules = map[string]ModeRulePatch{"fast": {Charge: &f}}
	out := Merge(base, patch)
	if out.ModeRules["fast"].Charge {
		t.Fatal("patch 未生效（out 侧 charge 应为 false）")
	}
	if !base.ModeRules["fast"].Charge {
		t.Fatal("base.ModeRules 被 Merge 浅拷贝污染（C8 回归）")
	}
}
