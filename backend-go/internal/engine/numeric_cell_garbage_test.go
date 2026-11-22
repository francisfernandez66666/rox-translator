// ============ numeric_cell_garbage_test.go · 职责说明（2026-11-22 tv13 终轮）============
// 数字格静默垃圾锁：源格数字占主导、译文只剩 1–3 个字母且无数字时必须拒，
// 正确纯数字 / 带单位词 / 长句含数字的译文不得被这条误伤。
// 同时锁住诊断原因名与采纳布尔同源（两把尺子纪律）。
// ================================================
package engine

import (
	"context"
	"testing"
)

func TestNumericCellShortLetterGarbage(t *testing.T) {
	cases := []struct {
		name, src, out string
		want           bool
	}{
		{"tv13-L150-单字母", "65–120 万", "ו", true},
		{"tv13-L150-单字母带换行", "65–120 万<br>", "ו <br>", true},
		{"三字母残词", "300 万+", "של", true},
		{"四字母短词不射程", "300 万+", "שלום", false},
		{"正确纯数字", "65–120 万", "650,000–1,200,000", false},
		{"正确带单位词", "45–80 万", "0.45–0.8 מיליון", false},
		{"百分比", "约 20%", "כ-20%", false},
		{"长句含数字不射程", "2026 年 3 月发布新版并覆盖 40 个国家", "גרסה חדשה", false},
		{"空串不抢归因", "65–120 万", "", false},
	}
	for _, c := range cases {
		if got := isNumericCellShortLetterGarbage(c.src, c.out); got != c.want {
			t.Errorf("%s：isNumericCellShortLetterGarbage(%q,%q)=%v，want %v", c.name, c.src, c.out, got, c.want)
		}
	}
}

func TestRejectReasonNamesNumericCellGarbage(t *testing.T) {
	e := &Engine{}
	ctx := context.Background()
	src, bad, good := "65–120 万", "ו", "650,000–1,200,000"
	// 这一形态先被 IsTranslationUsable（degenerateToFunctionWord）拦下；新判据是
	// 交付面第二道保险，不抢它的归因名。两把尺子只需同时判"不可用"。
	if got := e.fileTranslationRejectReason(ctx, src, bad, "he"); got == "" {
		t.Error("静默垃圾格不该被采纳，原因名不能为空")
	}
	if got := e.fileTranslationRejectReason(ctx, src, good, "he"); got != "" {
		t.Errorf("正确纯数字译文被判据误拒：%q", got)
	}
	if e.fileTranslationUsable(ctx, src, bad, "he") {
		t.Error("静默垃圾格不该被采纳")
	}
}

func TestDeterministicNumericDelivery(t *testing.T) {
	cases := []struct{ src, want string }{
		{"65–120 万<br>", "650,000–1,200,000<br>"},
		{"45–80 万", "450,000–800,000"},
		{"25–50 万", "250,000–500,000"},
		{"150–250 万", "1,500,000–2,500,000"},
		{"300 万+", "3,000,000+"},
		{"80–120 万/年", "800,000–1,200,000"},
		{"约 20%", ""},
		{"2026 年 3 月发布", ""},
	}
	for _, c := range cases {
		if got := deterministicNumericDelivery(c.src); got != c.want {
			t.Errorf("src=%q：确定性交付=%q，want %q", c.src, got, c.want)
		}
	}
}

func TestDeliveryTranslationsNoShortLetterGarbage(t *testing.T) {
	cases := []struct{ src, in, want string }{
		{"65–120 万", "ו", "650,000–1,200,000"},             // 模型给残字也按确定性换算交付
		{"65–120 万", "65–120 מיליון", "650,000–1,200,000"}, // 模型量级漂移被确定性值覆盖
		{"45–80 万", "0.45–0.8 מיליון", "450,000–800,000"},  // 带单位词形态统一为绝对值
	}
	for _, c := range cases {
		got := deliveryTranslations("he", map[string]string{c.src: c.in})[c.src]
		if got != c.want {
			t.Errorf("src=%q in=%q：交付=%q，want %q", c.src, c.in, got, c.want)
		}
	}
}
