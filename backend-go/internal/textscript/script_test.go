package textscript

import (
	"strings"
	"testing"
	"unicode"
)

// TestUnicodeVersionPinned 把 Unicode 版本钉在实测值上。
//
// 为什么这条锁存在：unicode.Version 随 Go 版本升（Go 1.21→1.26 已升过版），而升级会**静默**
// 改变 Of/Share/Profile 的全部判据（新增/调整 Script 属性、新增字符）。这条变红时的正确动作是
// 「回来复核字符判据并同步本文件的边界码位表」，不是把数字改成新值了事。
func TestUnicodeVersionPinned(t *testing.T) {
	if unicode.Version != "15.0.0" {
		t.Fatalf("Unicode 版本变了：%s（实测）。升 Go 会静默改变书写体系判据：请复核本包边界码位表与画像阈值，再改这条锁", unicode.Version)
	}
}

// TestOfBoundaryCodepoints 逐码位等值：40 个边界/易错码位各自的体系必须与期望一致。
func TestOfBoundaryCodepoints(t *testing.T) {
	cases := []struct {
		r    rune
		want Script
		why  string
	}{
		{'A', Latin, "拉丁大写"},
		{'z', Latin, "拉丁小写"},
		{'é', Latin, "带重音拉丁"},
		{'Ａ', Latin, "全角拉丁（与半角片假名同区，不能按区段切）"},
		{'一', Han, "汉字基本区首"},
		{'龥', Han, "汉字基本区末"},
		{'㐀', Han, "汉字扩展 A"},
		{'𠀀', Han, "汉字扩展 B（旧手抄区间必漏）"},
		{'あ', Hiragana, "平假名"},
		{'ゖ', Hiragana, "平假名末"},
		{'ア', Katakana, "片假名"},
		{'ヿ', Katakana, "片假名末"},
		{'ﾊ', Katakana, "半角片假名（与全角拉丁同区）"},
		{'가', Hangul, "谚文"},
		{'힣', Hangul, "谚文末"},
		{'А', Cyrillic, "西里尔"},
		{'я', Cyrillic, "西里尔末"},
		{'ا', Arabic, "阿拉伯"},
		{'ي', Arabic, "阿拉伯末"},
		{'א', Hebrew, "希伯来"},
		{'ת', Hebrew, "希伯来末"},
		{'α', Greek, "希腊"},
		{'ω', Greek, "希腊末"},
		{'ก', Thai, "泰文"},
		{'ฮ', Thai, "泰文末"},
		{'अ', Devanagari, "天城文"},
		{'ह', Devanagari, "天城文末"},
		// Common/Inherited 的显式归属（scriptExtend）——日文行是否被判成「含 Other」全靠这张表。
		{'ー', Katakana, "长音符（unicode.Katakana 对它是 false）"},
		{'・', Katakana, "中点（历史 isLoneKana 区间里就有它，行为保持不变）"},
		{'々', Han, "叠字符号"},
		{'〆', Han, "〆"},
		{'ゝ', Hiragana, "平假名叠字"},
		{'ゞ', Hiragana, "平假名叠字浊"},
		{'ヽ', Katakana, "片假名叠字"},
		{'ヾ', Katakana, "片假名叠字浊"},
		{'ﾞ', Katakana, "半角浊点"},
		{'ﾟ', Katakana, "半角半浊点"},
		// Other：标点/符号/数字不属于任何体系。
		{'1', Other, "数字"},
		{'!', Other, "标点"},
		{'（', Other, "全角括号"},
		{'→', Other, "箭头"},
		{'¥', Other, "货币符号"},
	}
	for _, c := range cases {
		if got := Of(c.r); got != c.want {
			t.Errorf("Of(%q U+%04X)=%s，期望 %s（%s）", c.r, c.r, got, c.want, c.why)
		}
	}
}

// TestShareDenominator 分母只数字母：数字/标点/空白不进分母，且占比之和为 1。
func TestShareDenominator(t *testing.T) {
	counts := Share("abc 123 中文，！")
	if counts[Latin] != 3 || counts[Han] != 2 {
		t.Fatalf("字母计数不对：latin=%d han=%d（期望 3/2）", counts[Latin], counts[Han])
	}
	if len(counts) != 2 {
		t.Fatalf("非字母字符混进了分母：%v", counts)
	}
	p := OfLines([]string{"abc 123 中文，！"})
	if p.Letters != 5 {
		t.Fatalf("Letters=%d，期望 5", p.Letters)
	}
	sum := 0.0
	for _, v := range p.Share {
		sum += v
	}
	if sum < 0.999 || sum > 1.001 {
		t.Fatalf("占比之和=%v，期望 1", sum)
	}
}

// TestOfLinesTiers 三档判定与边界值（边界值归上一档，不许留灰区）。
func TestOfLinesTiers(t *testing.T) {
	cases := []struct {
		name  string
		lines []string
		want  string
	}{
		{"纯英文", []string{"hello world", "manual entry"}, "single"},
		{"纯中文", []string{"产品说明书", "测试报告"}, "single"},
		{"正好95%", []string{strings.Repeat("a", 95) + strings.Repeat("中", 5)}, "single"},
		{"90%拉丁+10%汉字", []string{strings.Repeat("a", 90) + strings.Repeat("中", 10)}, "mixed"},
		{"80拉丁20汉字（第二体系到20%）", []string{strings.Repeat("a", 80) + strings.Repeat("中", 20)}, "multi"},
		{"40/30/30", []string{strings.Repeat("a", 40) + strings.Repeat("中", 30) + strings.Repeat("あ", 30)}, "multi"},
		{"空文档", []string{"", "123", "!!!"}, "single"},
	}
	for _, c := range cases {
		if got := OfLines(c.lines).Tier; got != c.want {
			t.Errorf("%s：Tier=%s，期望 %s（Share=%v）", c.name, got, c.want, OfLines(c.lines).Share)
		}
	}
}

// TestIsolatedAndRare 「只以孤立短行出现」的定义：含该体系的每一行都只有 ≤2 个该体系字母。
func TestIsolatedAndRare(t *testing.T) {
	// 英文文档 600 个字母 + 一行孤立「の」：假名占比 1/601 < 0.5%，且只以孤立短行出现。
	lines := make([]string, 0, 51)
	for i := 0; i < 50; i++ {
		lines = append(lines, "manualmanual")
	}
	lines = append(lines, "の")
	p := OfLines(lines)
	if !p.Isolated[Katakana] && !p.Isolated[Hiragana] {
		t.Fatalf("孤立短行应被标为 Isolated：%v", p.Isolated)
	}
	if !p.IsRare(Katakana) && !p.IsRare(Hiragana) {
		t.Fatalf("占比 1/601 的假名应判 Rare：share=%v letters=%d", p.Share, p.Letters)
	}

	// 日文文档：假名占比高 ⇒ 永不 Rare。
	ja := []string{"これは日本語の文です", "の", "設定を変更します"}
	jp := OfLines(ja)
	if jp.IsRare(Hiragana) || jp.IsRare(Katakana) {
		t.Fatalf("日文文档里的假名不该判 Rare：share=%v", jp.Share)
	}

	// 英文行里出现多个假名字母 ⇒ 不算孤立短行（Isolated=false）。
	lines2 := append([]string{}, lines...)
	lines2[len(lines2)-1] = "ののの"
	p2 := OfLines(lines2)
	if p2.Isolated[Hiragana] || p2.Isolated[Katakana] {
		t.Fatalf("多字母假名行不该是 Isolated：%v", p2.Isolated)
	}
}

// TestIsLoneForeign 单字噪声判据的正例/反例。
func TestIsLoneForeign(t *testing.T) {
	lines := []string{}
	for i := 0; i < 50; i++ {
		lines = append(lines, "manualmanual")
	}
	lines = append(lines, "の")
	p := OfLines(lines)

	yes := []string{"の", "マ", "ア"}
	for _, s := range yes {
		if !IsLoneForeign(s, p) {
			t.Errorf("应判孤立异常：%q", s)
		}
	}
	no := []string{
		"manual", // 超过 2 个字母
		"ののの",    // 超过 2 个字母
		"¥",      // 非字母：归符号门
		"→",      // 同上
		"é",      // 拉丁是主体系，永不当异常
		"A",      // 同上
		"",       // 空串
		"1",      // 数字
	}
	for _, s := range no {
		if IsLoneForeign(s, p) {
			t.Errorf("不该判孤立异常：%q", s)
		}
	}
	// 日文文档里「の」不是异常；中文文档里「中」不是异常（各自的主体系）。
	if IsLoneForeign("の", OfLines([]string{"これは日本語の文です", "の"})) {
		t.Fatal("日文文档里的「の」不该判孤立异常")
	}
	if IsLoneForeign("中", OfLines([]string{"产品说明书", "测试报告", "中"})) {
		t.Fatal("中文文档里的「中」不该判孤立异常")
	}
	// 反过来：英文文档里出现一枚孤立汉字，就该判异常（与「の」同一族）。
	if !IsLoneForeign("中", p) {
		t.Fatal("英文文档里的孤立汉字应判异常")
	}
}

// TestHasOnlyLetters 只数字母：叠字符（々）不是「有汉字」的证据，但真正的汉字是。
func TestHasOnlyLetters(t *testing.T) {
	if !Has("中文", Han) {
		t.Fatal("中文应判含汉字")
	}
	if !Has("𠀀", Han) {
		t.Fatal("扩展 B 汉字应判含汉字（旧手抄区间会漏）")
	}
	// 々（U+3005）在 Unicode 里 Script=Han 且是字母，因此按"字母＋体系"口径它算汉字。
	// 这对残留意判据是更安全的形态：旧手抄区间会把「時々」拆成两段「時」，
	// 反而更容易与中文源文撞上；整段「時々」不会撞。
	if !Has("々", Han) {
		t.Fatal("々 的 Unicode Script 是 Han，按字母口径应判含汉字")
	}
	if Has("123", Han) {
		t.Fatal("数字不应判含汉字")
	}
}

// TestProfileHashStable 指纹稳定：同画像同串，占比变化后串变化。
func TestProfileHashStable(t *testing.T) {
	p1 := OfLines([]string{"hello world"})
	p2 := OfLines([]string{"hello world"})
	if p1.Hash() != p2.Hash() {
		t.Fatalf("同画像指纹不一致：%q vs %q", p1.Hash(), p2.Hash())
	}
	p3 := OfLines([]string{"hello 中"})
	if p1.Hash() == p3.Hash() {
		t.Fatal("占比变化后指纹必须变")
	}
	if !strings.Contains(p1.Hash(), "single") {
		t.Fatalf("指纹应包含判档：%q", p1.Hash())
	}
}
