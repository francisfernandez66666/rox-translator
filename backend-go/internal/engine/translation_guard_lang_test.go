// ============ translation_guard_lang_test.go · 职责说明 ============
// P0 骨架判据补强的单测（2026-11-20）：现网"品牌英文骨架"必须判不可用，
// 正常译文（哪怕含品牌名）不许误杀；语言无关（he/de/ja/ar 同口径）；
// 另配反劣化用例确保短正常译文不被骨架判据误伤。
// ============================================
package engine

import "testing"

// TestIsSkeletonForLang P0 骨架判据补强：带品牌英文的骨架必须被拦下，
// 正常译文（哪怕含品牌名）不许误杀。语言无关——he/de/ja/ar 同一口径。
func TestIsSkeletonForLang(t *testing.T) {
	cases := []struct {
		name string
		src  string
		out  string
		lang string
		want bool
	}{
		// A 正腿：现网样本——中文整段回显洗成"品牌英文+中文标点"骨架
		{"A_现网品牌骨架_he", "通过Facebook和Instagram、TikTok投放，线上转化=0；30天刷新",
			"Facebook / Instagram / TikTok ，，、；，。 = ", "he", true},
		// E 语言无关：同族骨架换目标语仍命中
		{"E_品牌骨架_de", "通过Facebook和Instagram、TikTok投放，线上转化=0；30天刷新",
			"Facebook / Instagram / TikTok ，，、；，。 = ", "de", true},
		{"E_品牌骨架_ja", "通过Facebook和Instagram、TikTok投放，线上转化=0；30天刷新",
			"Facebook / Instagram / TikTok ，，、；，。 = ", "ja", true},
		{"E_品牌骨架_ar", "通过Facebook和Instagram、TikTok投放，线上转化=0；30天刷新",
			"Facebook / Instagram / TikTok ，，、；，。 = ", "ar", true},
		// B 正腿：纯标点骨架
		{"B_纯标点骨架_he", "已实现=加权成本重放；浮动=市值-成本×数量；30s刷新",
			"=；=-×；30s", "he", true},
		// C 反腿：正常希伯来译文含品牌 Facebook，不许误杀
		{"C_正常希伯来含品牌", "通过Facebook和Instagram投放，线上转化=0；30天刷新",
			"דרך Facebook ו-Instagram, ההמרה המקוונת = 0; רענון תוך 30 יום", "he", false},
		// D 反腿：无中文源（纯数字/标识符）回吐原文，判据不适用
		{"D_无中文源_数字", "002584.SZ", "002584.SZ", "en", false},
		{"D_无中文源_标识符", "N_shape_dragon", "N_shape_dragon", "de", false},
		// 未知语言 fail-soft
		{"未知语言_不判", "通过Facebook投放，转化=0", "Facebook = 0", "xx", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsSkeletonForLang(c.src, c.out, c.lang); got != c.want {
				t.Errorf("IsSkeletonForLang(src=%q, out=%q, %s) = %v, want %v", c.src, c.out, c.lang, got, c.want)
			}
		})
	}
}

// TestIsSkeletonForLang_NotTooAggressive 反劣化：目标语正确匹配的正常译文不许误判骨架。
// （若 lang 与译文语种故意错配，那是 P2 脚本校验的活，不属骨架判据射程。）
func TestIsSkeletonForLang_NotTooAggressive(t *testing.T) {
	src := "价格是30元，共2项；详见手册。"
	cases := []struct {
		lang string
		out  string
	}{
		{"de", "Der Preis beträgt 30 Yuan, 2 Artikel; siehe Handbuch."},
		{"he", "המחיר 30 יואן, 2 פריטים; ראה מדריך."},
		{"en", "The price is 30 Yuan, 2 items; see the manual."},
		{"de", "30 Yuan, 2 Artikel"},
	}
	for _, c := range cases {
		if IsSkeletonForLang(src, c.out, c.lang) {
			t.Errorf("正常译文被误判成骨架：lang=%s out=%q", c.lang, c.out)
		}
	}
}
