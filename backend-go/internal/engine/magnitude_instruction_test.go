// ============ 本文件职责中文说明 ============
// 「中文量级词换算」指令的等值锁（★ 2026-11-22 真值验收 §7.8）。
//
// 现网形态（希伯来语 tv7，三处同一根因）：源文 `80–120 万/年`→`80,000–120,000`、
// `150–250 万`→`150–250 אלפי`（150–250 千）、`年费 300 万+`→`300,000+`——全部差 10 倍。
// 数字一致性闸门只核"数字串有没有出现"，量级错译在字符层面完全合法，所以它一路过闸；
// 收口只能落在**指令**上（量级词是中文构词法，不点名时模型按"数词+单位"处理）。
//
// 锁三件：
//  1. 逐分支覆盖（全部目标语 × 中/英两套界面语言）——「只给 en 加没给 zh 加」这类半截修复最隐蔽；
//  2. 条款必须点名 `万 = 10,000` 与两个真值示例（只写"注意量级"等于没写）；
//  3. 定向重译腿的「数字保持」提示词必须带同一口径（它才是真正去救这三行的那一枪）。
//
// ============================================
package engine

import (
	"strings"
	"testing"
)

// TestMagnitudeClauseAppendedToEveryBranch 逐 (target, uiLang) 组合覆盖量级条款。
func TestMagnitudeClauseAppendedToEveryBranch(t *testing.T) {
	targets := []string{"zh", "zh_hant", "ja", "ko", "kk", "he", "de", "ar", ""}
	for _, ui := range []string{"en", "zh"} {
		for _, tg := range targets {
			full := translateInstruction("zh", tg, ui)
			core := translateInstructionCore("zh", tg, ui)
			if !strings.HasPrefix(full, core) || full == core {
				t.Fatalf("target=%q ui=%q 未在核心指令尾部追加条款：full=%q", tg, ui, full)
			}
			note := magnitudeInstructionNote("zh", ui)
			if note == "" {
				t.Fatalf("target=%q ui=%q 中文源文下条款不得为空", tg, ui)
			}
			if !strings.Contains(full, note) {
				t.Fatalf("target=%q ui=%q 缺量级条款：full=%q", tg, ui, full)
			}
		}
	}
}

// TestMagnitudeClauseNamesMultipliers 条款必须点名乘数与真值示例（措辞可改，口径不许丢）。
func TestMagnitudeClauseNamesMultipliers(t *testing.T) {
	en := magnitudeInstructionNote("zh", "en")
	for _, kw := range []string{"万 = 10,000", "亿 = 100,000,000", "800,000–1,200,000", "3,000,000+"} {
		if !strings.Contains(en, kw) {
			t.Fatalf("英文条款缺 %q：%q", kw, en)
		}
	}
	// 反向：不许只写"注意量级"这类空口径（那是把判据写成口号）。
	if !strings.Contains(en, "factor of ten") {
		t.Fatalf("英文条款未点名「差十倍」这一现网形态：%q", en)
	}
	zh := magnitudeInstructionNote("zh", "zh")
	for _, kw := range []string{"万＝10,000", "亿＝100,000,000", "800,000–1,200,000", "十倍"} {
		if !strings.Contains(zh, kw) {
			t.Fatalf("中文条款缺 %q：%q", kw, zh)
		}
	}
	// 空 uiLang 走中文档（与 currencyInstructionNote 同一分派口径）。
	if magnitudeInstructionNote("zh", "") != zh {
		t.Fatal("空 uiLang 应与中文界面同档（口径分派只认 zh/zh_hant）")
	}
}

// TestMagnitudeClauseOnlyForChineseSource 只在中文源文下追加（口径不跨语向乱洒）。
//
// 判据取向是**成对**的：中文源文必须有、非中文源文必须没有——只测前者的话，
// 把条件写成 `return note` 无条件追加也照样绿（噪声进了英/法/阿拉伯源文的提示词）。
func TestMagnitudeClauseOnlyForChineseSource(t *testing.T) {
	for _, src := range []string{"zh", "zh_hant"} {
		if magnitudeInstructionNote(src, "en") == "" {
			t.Fatalf("源文 %s 必须追加量级条款", src)
		}
	}
	for _, src := range []string{"en", "fr", "ar", "he", "ja", "ko", ""} {
		if got := magnitudeInstructionNote(src, "en"); got != "" {
			t.Fatalf("源文 %q 不该追加量级条款，got %q", src, got)
		}
	}
	// 接线腿：非中文源文的完整指令里一个字节都不许出现该条款
	full := translateInstruction("en", "he", "zh")
	if strings.Contains(full, "数字量级口径") {
		t.Fatalf("英→希伯来 的指令里混进了中文量级条款：%q", full)
	}
}

// TestSegmentHintNumberKeepNamesMagnitude 定向重译腿的「数字保持」提示词必须带同一口径。
//
// 这条腿是真正去救 L158/L160/L181 的那一枪：只给语境、不点名量级时模型照旧把 `万` 写成 thousand
// （现网两轮实测均未救回）。所以判据不是"提示词非空"，而是"里面写着 万 = 10,000"。
func TestSegmentHintNumberKeepNamesMagnitude(t *testing.T) {
	hint := segmentHintNote("数字保持")
	if hint == "" {
		t.Fatal("「数字保持」档必须有提示词")
	}
	for _, kw := range []string{"Hard requirement", "万 = 10,000", "800,000–1,200,000", "factor of ten"} {
		if !strings.Contains(hint, kw) {
			t.Fatalf("「数字保持」提示词缺 %q：%q", kw, hint)
		}
	}
	// 反向对照：脚本一致那一档不许被顺手塞进数字口径（两档各治一病，混档等于两把尺子）。
	if strings.Contains(segmentHintNote("目标语言脚本一致"), "10,000") {
		t.Fatal("「目标语言脚本一致」档被塞进了数字口径 ⇒ 两档混档")
	}
	// 未知档恒空（不凭空长出第三个触发条件）。
	if segmentHintNote("别的") != "" || segmentHintNote("") != "" {
		t.Fatal("未知/空 hint 必须返回空串")
	}
}
