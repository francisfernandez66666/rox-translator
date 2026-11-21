// ============ 本文件职责中文说明 ============
// 「纯数量表达 ⇒ 纯数值译文可用」这条放行的锁（★ 2026-11-21 真值验收 tv_job3/tv_job4）。
//
// 缺陷形态（现网产物 tv_job4 L157/L159）：
//
//	源段（表格 cell）`45–80 万` —— 模型译成 `450,000–800,000`（正确）
//	→ cjkEchoWashedToSkeleton 判「源有文字、译文没有任何连续两个字母」＝"中文被洗成骨架"
//	→ IsTranslationUsable=false → 该段按原文回落
//	→ 客户表格里留下中文 `45–80 万`，而模型其实翻对了。
//
// 五条腿，正反各半（负向腿没有正向对照就是空转）：
//
//	A 正确纯数值译文必须判可用（靶心，真值形态逐字）；
//	B 丢了量级/数字必须仍判不可用（`45–80` / `450,000` 都是 10000 倍或半数的错）；
//	C 带目标语货币词的译文照旧可用（走 hasLetterRun 那条，不走豁免）；
//	D 实义修饰词（约/左右）不在豁免内：`约 20%` → `20%` 仍判不可用（丢义）；
//	E 原有骨架形态一个都不许被这条放行（中文回显洗成 `=；=-×；30s` 仍不可用）。
//
// ============================================
package engine

import "testing"

// TestQuantityDeliveryAcceptsNumericTranslation A 腿：真值产物里的正确译文必须判可用。
func TestQuantityDeliveryAcceptsNumericTranslation(t *testing.T) {
	cases := []struct{ name, src, tr string }{
		{"真值L157", "45–80 万", "450,000–800,000"},
		{"真值L159", "25–50 万", "250,000–500,000"},
		{"中文至", "45 至 80 万", "450,000–800,000"},
		{"亿档", "1.5–2 亿", "150,000,000–200,000,000"},
		{"单值量级", "20 万", "200,000"},
		{"带百分号", "20%", "20%"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if cjkEchoWashedToSkeleton(c.src, c.tr) {
				t.Errorf("纯数量源 + 纯数值译文不得判骨架：src=%q tr=%q", c.src, c.tr)
			}
			if !IsTranslationUsable(c.src, c.tr) {
				t.Errorf("纯数量源 + 纯数值译文必须判可用：src=%q tr=%q", c.src, c.tr)
			}
		})
	}
}

// TestQuantityDeliveryRejectsLossyNumeric B 腿：丢量级/丢数字必须仍判不可用。
func TestQuantityDeliveryRejectsLossyNumeric(t *testing.T) {
	cases := []struct{ name, src, tr string }{
		{"丢量级", "45–80 万", "45–80"},
		{"丢一半", "45–80 万", "450,000"},
		{"只翻一个", "45–80 万", "450,000–80"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !cjkEchoWashedToSkeleton(c.src, c.tr) {
				t.Errorf("丢了数字/量级仍必须判骨架：src=%q tr=%q", c.src, c.tr)
			}
			if IsTranslationUsable(c.src, c.tr) {
				t.Errorf("丢了数字/量级不得判可用：src=%q tr=%q", c.src, c.tr)
			}
		})
	}
}

// TestQuantityDeliveryKeepsRealTranslations C 腿：带目标语词的译文照旧可用（走字母那条）。
func TestQuantityDeliveryKeepsRealTranslations(t *testing.T) {
	cases := []struct{ name, src, tr string }{
		{"希伯来货币词(探针实测形态)", "45–80 万", "45,000–80,000 יואן"},
		{"英文货币词", "25–50 万 元", "250,000–500,000 yuan"},
		{"英文量级词", "1.5–2 亿", "150–200 million"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if cjkEchoWashedToSkeleton(c.src, c.tr) {
				t.Errorf("带目标语词的正常译文不得判骨架：src=%q tr=%q", c.src, c.tr)
			}
			if !IsTranslationUsable(c.src, c.tr) {
				t.Errorf("带目标语词的正常译文必须判可用：src=%q tr=%q", c.src, c.tr)
			}
		})
	}
}

// TestQuantityDeliveryExcludesModifiers D 腿：实义修饰词（约/左右）不在豁免内。
// `约 20%` 交付成 `20%` 是丢义——正确译文会带 about/כ- 之类的词，走字母那条。
func TestQuantityDeliveryExcludesModifiers(t *testing.T) {
	cases := []struct{ name, src, tr string }{
		{"约", "约 20%", "20%"},
		{"左右", "20 万左右", "200,000"},
		{"以上", "20 万以上", "200,000"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !cjkEchoWashedToSkeleton(c.src, c.tr) {
				t.Errorf("带实义修饰词的源段丢了修饰词必须仍判骨架：src=%q tr=%q", c.src, c.tr)
			}
		})
	}
	// 正向对照：把修饰词翻出来就可用（证明上一条不是把整类都打死）。
	for _, c := range []struct{ src, tr string }{
		{"约 20%", "כ-20%"},
		{"20 万左右", "about 200,000"},
	} {
		if cjkEchoWashedToSkeleton(c.src, c.tr) {
			t.Errorf("修饰词翻出来必须可用：src=%q tr=%q", c.src, c.tr)
		}
	}
}

// TestQuantityDeliveryDoesNotWeakenSkeletonGuard E 腿：原有骨架形态一个都不许被这条放行。
// 这条是「射程不许被改宽」的机械防线——一旦有人把豁免条件放宽成"译文没有字母就放行"，
// 这里立刻红。
func TestQuantityDeliveryDoesNotWeakenSkeletonGuard(t *testing.T) {
	src := "已实现=加权成本重放；浮动=市值-成本×数量；30s刷新"
	washed := PostProcessTranslation(src, "en")
	if washed == src {
		t.Fatalf("前提失效：后处理没删掉中文，样本不构成骨架形态")
	}
	if !cjkEchoWashedToSkeleton(src, washed) {
		t.Errorf("中文回显洗成骨架必须仍判骨架：%q", washed)
	}
	// 纯数值源但译文是符号骨架（不是数值）⇒ 不许放行。
	if !cjkEchoWashedToSkeleton("45–80 万", "=；=-×；30s") {
		t.Errorf("纯数量源配符号骨架译文必须判骨架")
	}
	// 有实义词的源配纯数值译文 ⇒ 不许放行（那是丢义，不是正确交付）。
	if !cjkEchoWashedToSkeleton("本季度营收", "200,000") {
		t.Errorf("有实义词的源配纯数值译文必须判骨架（丢义）")
	}
}
