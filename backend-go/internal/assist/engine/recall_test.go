// ============ engine/recall_test.go · 职责说明 ============
// 第 2 级相似度召回单测：文本归一（全半角/繁简/标点/停用词）、三条相似通道的
// 正/负样本、以及保守红线（短输入不放行、泛指关键词不误伤、极短输入走兜底）。
// =============================================
package engine

import "testing"

// TestNormalizeText 归一：全半角、繁简、大小写、标点与停用词/语气词剔除
func TestNormalizeText(t *testing.T) {
	// 1) 全角字母数字 → 半角 + 小写
	if got := normalizeText("ＰＤＦ１２３"); got != "pdf123" {
		t.Fatalf("全半角: %s", got)
	}
	// 2) 繁体 → 简体
	if got := normalizeText("怎麼收費"); got != "怎么收费" {
		t.Fatalf("繁简: %s", got)
	}
	// 3) 标点/空白剔除 + 语气词剔除
	if got := normalizeText("支持哪些文件格式，吗？"); got != "支持哪些文件格式" {
		t.Fatalf("标点/语气: %q", got)
	}
	// 4) 多字停用词剔除
	if got := normalizeText("请问一下，你们多少钱"); got != "多少钱" {
		t.Fatalf("停用词: %q", got)
	}
}

// TestFuzzySimilarityChannels 三条通道各自的正样本
func TestFuzzySimilarityChannels(t *testing.T) {
	// 通道 1：bigram 包含（关键词几乎完整出现在输入中）
	if s := fuzzySimilarity("支持哪些的文件格式", "文件格式"); s < fuzzyContainMin {
		t.Fatalf("bigram 包含通道失效: %f", s)
	}
	// 通道 3：字符集全包含（词被插字打断）——「韩国语」⇄「韩语」
	if s := fuzzySimilarity("韩国语可以翻吗", "韩语"); s <= 0 {
		t.Fatalf("字符集通道失效: %f", s)
	}
	// 「大文件」⇄「一个文件最多能传多大」
	if s := fuzzySimilarity("一个文件最多能传多大", "大文件"); s <= 0 {
		t.Fatalf("字符集通道(3字)失效: %f", s)
	}
	// 繁体经归一后相似
	if s := fuzzySimilarity("請問怎麼收費", "怎么收费"); s <= 0 {
		t.Fatalf("归一联动失效: %f", s)
	}
}

// TestFuzzySimilarityConservative 保守红线（宁给兜底不给错答案）
func TestFuzzySimilarityConservative(t *testing.T) {
	bad := []struct{ in, kw string }{
		{"多少钱", "大文件"},              // 输入 <4 字：不参相似度
		{"xyzzy量子波动速翻布拉布拉", "文件格式"}, // 乱语不误伤
		{"公司要用多人协同怎么弄", "怎么用"},      // 泛指关键词（实义字 <2）不许靠字符集凑齐
		{"我想了解一下", "解决方案"},          // 无共现
		{"韩文支持吗", "阿拉伯语"},           // 单字「语」共现不构成命中
	}
	for _, c := range bad {
		if s := fuzzySimilarity(c.in, c.kw); s > 0 {
			t.Errorf("误伤: %q vs %q → %f", c.in, c.kw, s)
		}
	}
}

// TestFuzzyTierScoreBelowExact 分级打分不变式：fuzzy/vector 恒低于任何一次精确命中
// （seed 内 priority ≤10 口径）
func TestFuzzyTierScoreBelowExact(t *testing.T) {
	for prio := 0; prio <= 10; prio++ {
		if f := fuzzyTierScore(prio); f >= 10 || f < 1 {
			t.Fatalf("fuzzy 越界: prio=%d score=%d", prio, f)
		}
		if v := vectorTierScore(prio); v >= 10 || v < 1 {
			t.Fatalf("vector 越界: prio=%d score=%d", prio, v)
		}
		if fuzzyTierScore(prio) < vectorTierScore(prio) {
			t.Fatalf("层级倒挂: prio=%d fuzzy=%d < vector=%d", prio, fuzzyTierScore(prio), vectorTierScore(prio))
		}
	}
}
