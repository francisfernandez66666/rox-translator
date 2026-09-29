// quote_guard_test.go — ★ 092x 红腿二（2026-09-29 现网复问：正文里出现「2000×400+7.5 计约 150 积分」）。
//
// 这条链的**风险不对称**：漏掉一句自算报价＝对外错报（客户按它充值、发现对不上账），
// 误判一句合格报价＝把客户要看的数字换成一句指路话（难看但不错）。
// 所以断言必须**两侧都钉**：现网那条要被抓掉，而服务端自己算好发出去的示例句、
// 公示系数句、字符数、英文 credits／日元金额句一个字都不许动。
package engine

import (
	"context"
	"strings"
	"testing"
)

// docForGuard 用现值夹具（fast 4/1k＋7.5、pro 8/1k＋7.5、1 积分=0.1 元）。
// 不新造一份数字：夹具与【系统现值】段同源，判据才有意义。
func docForGuard() *pricingMetaDoc { return pricingDocForTest() }

// engineWithDoc 只把缓存位填上，不打上游（出站核验只读缓存，见 cachedPricing 注释）。
func engineWithDoc(doc *pricingMetaDoc) *Engine {
	e := &Engine{}
	e.sysValDoc = doc
	return e
}

func TestQuoteArithmeticIsCaughtEvenWithoutUnits(t *testing.T) {
	// 现网原句的形态：算式两侧都是数字，句里没带任何计费单位词也必须判为报价
	if !quoteArithmeticPat.MatchString("按 2000×400+7.5 计") {
		t.Fatal("乘法算式没命中判据")
	}
	// 反向：尺寸与「每 1000 源字符」这类公示除法写法不许命中
	for _, ok := range []string{"2 x 3cm 的图", "每 1000 源字符 = 8 积分", "3,000 字符以内"} {
		if quoteArithmeticPat.MatchString(ok) {
			t.Errorf("正常文案被判成算式：%q", ok)
		}
	}
}

// TestGuardReplyQuoteReplacesOnlyModelComputedFigures 逐句判据的两侧。
func TestGuardReplyQuoteReplacesOnlyModelComputedFigures(t *testing.T) {
	doc := docForGuard()
	e := engineWithDoc(doc)
	ctx := context.Background()

	// pro 1000 字符＝15.5 积分（8+7.5）；现算示例 20000＝167.5
	mustReplace := []string{
		"专业模式按 2000×400+7.5 计，约 150 积分。",                       // 现网原句：算式＋复算不出的总额
		"按 2000 字符算，大约 160 积分。",                                // 无算式但数字对不上任何现值组合（pro 2000＝23.5、fast 2000＝15.5）
		"这单大概 999 积分。",                                         // 现编总额
		"Estimated cost is 807.5 credits for 2000 characters.", // 英文现编（fast 2000＝15.5）
	}
	for _, s := range mustReplace {
		rep := &Reply{Content: s, Source: "llm"}
		got := e.guardReplyQuote(ctx, "zh", rep).Content
		if got == s {
			t.Errorf("该拦的没拦：%q", s)
		}
		if strings.Contains(got, "×") || strings.Contains(got, "150 积分") || strings.Contains(got, "999") {
			t.Errorf("替换后仍留着原报价：%q", got)
		}
	}

	mustKeep := []string{
		"专业模式每 1000 源字符 = 8 积分，另每次建单固定 7.5 积分。", // ④ 系数与建单固定值本身
		"20000 源字符 = 167.5 积分（约 16.75 元）。",      // ③ 服务端现算示例
		"约 15.5 积分（1000 源字符，专业模式）。",             // ② 由同句字符数复算得出
		"支持 PDF、Word 与 epub 格式。",                // 根本不是报价
		"文件最大 40MB，单任务不超过 300000 源字符。",          // 上限说明，不是金额
	}
	for _, s := range mustKeep {
		rep := &Reply{Content: s, Source: "llm"}
		if got := e.guardReplyQuote(ctx, "zh", rep).Content; got != s {
			t.Errorf("合格句被改坏：\n 原：%q\n 改后：%q", s, got)
		}
	}

	// 非 llm 出处（话术直配／流程／兜底）一个字都不动：那些文案有另一条治理线（assist_kb_sync）
	text := "套餐说明里写着的旧价 299 元。"
	rep := &Reply{Content: text, Source: "script"}
	if got := e.guardReplyQuote(ctx, "zh", rep).Content; got != text {
		t.Fatalf("话术直配文案被数字核验改掉了：%q", got)
	}
}

// TestGuardReplyQuoteWithoutPricingFallsBackToArithmeticOnly 取不到现值时**只判算式**。
// 没有系数就没有"复算得出"的基准，此时把知识条目里的合法面值抹掉属于误伤。
func TestGuardReplyQuoteWithoutPricingFallsBackToArithmeticOnly(t *testing.T) {
	e := engineWithDoc(nil)
	ctx := context.Background()

	// 注意：guardReplyQuote 是**原地改 rep**，比较基线必须先快照，别拿改后的 rep.Content 当原文比
	origin := "按 2000×400 计约 800 积分。"
	rep := &Reply{Content: origin, Source: "llm"}
	if got := e.guardReplyQuote(ctx, "zh", rep).Content; got == origin {
		t.Fatal("没有现值时算式仍必须拦（算式本身就不该发给客户）")
	}
	keepOrigin := "专业版 299 元，含 3000 积分。"
	kept := &Reply{Content: keepOrigin, Source: "llm"}
	if got := e.guardReplyQuote(ctx, "zh", kept).Content; got != keepOrigin {
		t.Fatalf("无现值时不许拿数字核验当刀：%q → %q", keepOrigin, got)
	}
}

// TestQuoteSafeLineCarriesRateOnlyWhenModeIsNamed 替换句里的数字只在认得出模式名时才出现。
// 带错档的系数比不带数字更糟——那是本守卫要消灭的"对外错报"的新实例。
func TestQuoteSafeLineCarriesRateOnlyWhenModeIsNamed(t *testing.T) {
	doc := docForGuard()
	if line := quoteSafeLine("zh", doc, "快速模式大概 999 积分"); !strings.Contains(line, "每 1000 源字符 = 4 积分") {
		t.Fatalf("认得出「快速」却没带上该档现值系数：%q", line)
	}
	if line := quoteSafeLine("ja", doc, "専門モードは 999 ポイント"); strings.Contains(line, "999") {
		t.Fatalf("认不出模式名（日文写的是「専門」不是「プロ」）却漏出数字：%q", line)
	}
	for _, lang := range []string{"en", "ru", "fr", "th", "ko"} {
		line := quoteSafeLine(lang, doc, "It costs 999 credits")
		if strings.Contains(line, "999") {
			t.Fatalf("%s 档替换句里留着错数字：%q", lang, line)
		}
		if !strings.Contains(line, "LangCross") && !strings.Contains(line, "pricing") && !strings.Contains(line, "cost") {
			t.Fatalf("%s 档替换句不含任何可执行信息：%q", lang, line)
		}
	}
	// 日文替换句必须是纯日文（红腿一同族：守卫自己不能产出混排句）
	ja := quoteSafeLine("ja", doc, "専門モードで 999 ポイント")
	if strings.Contains(ja, "文件") || strings.Contains(ja, "费") || strings.Contains(ja, "积分") {
		t.Fatalf("日文替换句里混着中文词形：%q", ja)
	}
	// 现值缺失时也必须出一句完整的话，不能空串（空串＝气泡里那句报价被整句删没）
	if line := quoteSafeLine("zh", nil, "按 2000×400 计"); strings.TrimSpace(line) == "" {
		t.Fatal("无现值时替换句为空，客户会看到报价整句消失")
	}
}

// TestGuardReplyQuoteKeepsLayoutAndOtherSentences 只换那一句，标点与换行原样保留。
func TestGuardReplyQuoteKeepsLayoutAndOtherSentences(t *testing.T) {
	e := engineWithDoc(docForGuard())
	content := "支持 PDF 原版式。\n专业模式按 2000×400+7.5 计约 150 积分。\n把文件发我就能算准。"
	got := e.guardReplyQuote(context.Background(), "zh", &Reply{Content: content, Source: "llm"}).Content
	if strings.Count(got, "\n") != 2 {
		t.Fatalf("换行被守卫吃掉了（列表会挤成一坨）：%q", got)
	}
	if !strings.Contains(got, "支持 PDF 原版式。") || !strings.Contains(got, "把文件发我就能算准。") {
		t.Fatalf("非报价句被一起改掉了：%q", got)
	}
	if !strings.Contains(got, "算准总额。\n把文件发我就能算准。") {
		t.Fatalf("替换句没接回原句的换行（列表被挤成一坨）：%q", got)
	}
}

// TestAcceptSetCoversEverySentenceThePromptItselfShows 关键对照：
// 【系统现值】段自己发给模型看的那三档示例，出站核验必须**逐字放行**。
// 不同源就会长出"提示词给示例、守卫判错报"的自我矛盾——那比原来的缺陷更难排查。
func TestAcceptSetCoversEverySentenceThePromptItselfShows(t *testing.T) {
	doc := docForGuard()
	block := renderQuoteExamples(doc)
	if block == "" {
		t.Fatal("夹具渲染不出示例")
	}
	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.Contains(line, "积分") {
			continue
		}
		for _, part := range strings.Split(line, "；") {
			part = strings.TrimSpace(part)
			if part == "" || !isPricingSentence(part) {
				continue
			}
			if !allQuoteNumbersVerifiable(part+"。", doc) {
				t.Errorf("系统自己给的示例句被判成错报：%q", part)
			}
		}
	}
	// 报价纪律那句也含数字（1000/20000），同样不许被判错
	if !allQuoteNumbersVerifiable("每 1000 源字符 = 8 积分，另每次建单固定 7.5 积分。", doc) {
		t.Error("公示系数句被判成错报（那是第④类，必须放行）")
	}
}

// TestNumbersWithUnitsWindow ★ 窗口判据自身的三条腿（2026-09-29 反证补的）。
//
// 为什么单独立一条：反证跑出来「窗口上限从 32 退回到 14」时整包**全绿**——
// 也就是说这一档当时只由示例句间接覆盖，谁都看不出它是可动的。这类"改了也没人红"的
// 判据档位就是长期烂在代码里的东西，所以给它一套点名到长度的正负对照：
//
//	① 数字与单位隔着 20 个字符仍须命中（钉住"窗口不小于 20"）；
//	② 隔到 40 个字符就不许命中（钉住"窗口有上限"，否则后半篇的单位会认错主人）；
//	③ 中间插进另一个数字 ⇒ 单位归后面那个数字，前一个不许认领
//	   （这就是公示句「每 1000 源字符 = 8 积分」里 1000 曾被当成一笔复算不出的报价的根因）。
func TestNumbersWithUnitsWindow(t *testing.T) {
	near := "8" + strings.Repeat("说", 20) + "积分"
	if got := numbersWithUnits(near, quotePointsUnits); len(got) != 1 || got[0] != 8 {
		t.Fatalf("窗口过小判据失守：20 字符外的单位应命中，实际 %v", got)
	}
	far := "8" + strings.Repeat("说", 40) + "积分"
	if got := numbersWithUnits(far, quotePointsUnits); len(got) != 0 {
		t.Fatalf("窗口没有上限判据失守：40 字符外的单位不该算到这个数字头上，实际 %v", got)
	}
	// 正向对照：把长度缩回 20 同一份串必须命中（否则②属于"函数从不返回东西"的恒真空转）
	if got := numbersWithUnits("8"+strings.Repeat("说", 20)+"积分", quotePointsUnits); len(got) != 1 {
		t.Fatal("②的对照失守：同函数在短窗口下取不到数字")
	}
	pub := "每 1000 源字符 = 8 积分"
	if got := numbersWithUnits(pub, quotePointsUnits); len(got) != 1 || got[0] != 8 {
		t.Fatalf("数字之间插了另一个数字仍被认到单位：期望 [8] 实际 %v（公示基数会被判成错报）", got)
	}
	// 正向对照：字符数那一档必须归字符单位管，两处判据不能互相吞
	if got := numbersWithUnits(pub, quoteCharUnits); len(got) != 1 || got[0] != 1000 {
		t.Fatalf("1000 没被认成计费基数：期望 [1000] 实际 %v", got)
	}
}
