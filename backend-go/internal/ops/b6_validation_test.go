// b6_validation_test.go · ★ B6 时间窗后门校验回归：billing.enforced/payment.mode 禁入
// overrides、id 必填且唯一、禁止嵌套子窗口。
package ops

import "testing"

func boolPtr(b bool) *bool { return &b }

func TestWindowOverridesForbiddenBackdoors(t *testing.T) {
	ok := PromoWindow{ID: "w1"}
	if err := ValidateWindowOverrides(ok); err != nil {
		t.Fatalf("空合规窗口应通过: %v", err)
	}
	cases := []struct {
		name string
		w    PromoWindow
	}{
		{"billing.enforced", PromoWindow{ID: "a", Overrides: OperationsPolicy{Billing: BillingPatch{Enforced: boolPtr(true)}}}},
		{"payment.mode", PromoWindow{ID: "b", Overrides: OperationsPolicy{Payment: PaymentPatch{Mode: "wechat"}}}},
		{"payment.auto_charge", PromoWindow{ID: "c", Overrides: OperationsPolicy{Payment: PaymentPatch{AutoCharge: boolPtr(true)}}}},
		{"空 id", PromoWindow{ID: "  "}},
		{"嵌套窗口", PromoWindow{ID: "d", Overrides: OperationsPolicy{PromoWindows: []PromoWindow{{ID: "e"}}}}},
	}
	for _, c := range cases {
		if err := ValidateWindowOverrides(c.w); err == nil {
			t.Fatalf("%s 应被拒绝", c.name)
		}
	}
}

func TestPolicyWindowsUniqueID(t *testing.T) {
	p := OperationsPolicy{PromoWindows: []PromoWindow{{ID: "x"}, {ID: "x"}}}
	if err := ValidatePolicyWindows(p); err == nil {
		t.Fatal("重复 id 应被拒绝")
	}
	p2 := OperationsPolicy{PromoWindows: []PromoWindow{{ID: "x"}, {ID: "y"}}}
	if err := ValidatePolicyWindows(p2); err != nil {
		t.Fatalf("合法窗口集应通过: %v", err)
	}
}
