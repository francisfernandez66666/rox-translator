// ============ file_brand_presence_test.go · 职责说明 ============
// G4（2026-10-19）「文件交付里品牌名不许被翻掉」的判据锁。
//
// D5 实测：同一份含品牌行的原件两次交付首行不同（`Can LangCross speak?` vs
// `Can you speak LangCross?`）——品牌被当普通词翻进疑问句，且非确定性对客是双倍伤害。
// 挂件出栈有 guardReplyBrand，工单/文件链此前没有同一道闸。本文件钉住纯函数
// countBrandPresenceDrops 的三条口径：
//
//	① 源段含品牌、译文含规定译法 ⇒ 不判（正锁，误报会把正常交付打成缺陷）；
//	② 源段含品牌、译文丢了品牌 ⇒ 判缺（本闸的唯一目的）；
//	③ 名单取不到（brandTerms 空）⇒ fail-soft 一律不判（§一·13③：拿不到清单＝不知道品牌名，
//	   不是「什么都算丢」）。
//
// 反证：把 ② 的期望翻过来（要求缺失时判 0）必然红——本用例即反证的载体。
// 本文件只跑纯函数，不连库、不调模型。
// =============================================
package engine

import "testing"

func TestCountBrandPresenceDrops(t *testing.T) {
	terms := map[string]map[string]string{
		"ru": {"极石": "ROX"},
		"en": {"极石": "ROX"},
	}
	langs := map[string]map[string]string{
		"ru": {
			"预约 极石 试驾":    "Запишитесь на тест-драйв ROX", // 品牌在 ⇒ 不判
			"极石汽车 2026 款": "Автомобиль 2026 года",         // 品牌被翻掉 ⇒ 判
		},
		"en": {
			"极石 服务": "ROX service", // 品牌在 ⇒ 不判
		},
	}
	drops, hit := countBrandPresenceDrops(langs, terms)
	if drops != 1 {
		t.Fatalf("应恰判 1 段品牌缺失，实得 %d（命中语言 %v）", drops, hit)
	}
	if !hit["ru"] || hit["en"] {
		t.Fatalf("缺失语言集合应为 {ru}，实得 %v", hit)
	}
}

// TestCountBrandPresenceFailSoft 名单取不到 ⇒ 不判（阻止把「无清单」误升级成「缺品牌」）。
func TestCountBrandPresenceFailSoft(t *testing.T) {
	langs := map[string]map[string]string{
		"ru": {"极石 试驾": "тест-драйв"}, // 译文确实没有品牌
	}
	if drops, hit := countBrandPresenceDrops(langs, nil); drops != 0 || len(hit) != 0 {
		t.Fatalf("名单为空必须 fail-soft 判 0，实得 drops=%d hit=%v", drops, hit)
	}
	if drops, hit := countBrandPresenceDrops(langs, map[string]map[string]string{}); drops != 0 || len(hit) != 0 {
		t.Fatalf("名单为空 map 必须 fail-soft 判 0，实得 drops=%d hit=%v", drops, hit)
	}
}

// TestCountBrandPresenceNoBrandInSource 源段本不含品牌 ⇒ 不判（避免把普通段落误报成缺品牌）。
func TestCountBrandPresenceNoBrandInSource(t *testing.T) {
	terms := map[string]map[string]string{"ru": {"极石": "ROX"}}
	langs := map[string]map[string]string{"ru": {"普通段落无品牌": "обычный абзац"}}
	if drops, _ := countBrandPresenceDrops(langs, terms); drops != 0 {
		t.Fatalf("源段无品牌不得判缺，实得 %d", drops)
	}
}
