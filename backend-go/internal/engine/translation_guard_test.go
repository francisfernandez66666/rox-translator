// ============ translation_guard_test.go 职责说明 ============
// 译文可用性单一判定的回归断言（P0 整改 RC-2 + F-38 批 D）：
//   - IsTranslationUsable：空/失败占位/同文（含空白折叠）/指令回显残留/短格长度爆炸五类必判不可用；
//     源文与译文**都**含 '<' 的合法数学式必须判可用（防把 "A<B" 误杀）；
//     ★ 同文的例外档（判据 3′，2026-10-19 工单 107 收口）：源段无任何非 ASCII 字母时回吐判可用；
//     见 TestIsTranslationUsableIdentityEchoWithoutSourceLetters（含"有中文仍须判不可用"的反向对照）。
//   - collectKBPass：KB「源文=译文」脏行与「6 字格×2100 字邮件」爆炸行都不得算命中，
//     必须进模型补漏队列；
//   - missingSegments：脏行未写入 langTranslations ⇒ 最终计入未译清单（不再被硬闸永久跳过）。
//
// ========================================
package engine

import (
	"strings"
	"testing"
)

// TestIsTranslationUsableRejectsRefusalResidue ★ 2026-10-21（UAT 收口 M1）：
// 模型整段回绝/答非所问不得当译文交付——现网五件交付物全叫
// `I am unable to provide the translation you requested_en.*`，正文首行也是这句。
// 正腿逐形态拦（成对结构 / 换措辞 n-gram / 中文回绝），反腿钉住正常译文不误杀
// （尤其是「源文自己就谈翻译」和「源文自己就是一句回绝」这两档）。
func TestIsTranslationUsableRejectsRefusalResidue(t *testing.T) {
	rejected := []struct{ src, out string }{
		{"产品方案书", "I am unable to provide the translation you requested"},
		{"报价单", "I'm sorry, but I cannot help with that translation."},
		{"当前：全部允许·仓位留空/0 =使用全局单票金额", "抱歉，我无法提供您要求的翻译。"},
		{"蓝牙钥匙已激活，请勿拆卸。", "无法提供这段文字的翻译"},
		{"Q3 渠道复盘", "As an AI language model, I cannot translate this content."},
	}
	for _, c := range rejected {
		if IsTranslationUsable(c.src, c.out) {
			t.Errorf("拒译话术必须判不可用：src=%q out=%q", c.src, c.out)
		}
	}

	kept := []struct{ src, out string }{
		// 正常译文里出现 "cannot provide" —— 源文就是这句话，必须放行
		{"不能提供该服务", "We cannot provide this service."},
		// 源文自己在谈翻译（本仓产品文档的典型句式），不能当回绝拦掉
		{"翻译服务维护中，暂时无法提供", "The translation service is under maintenance and is temporarily unavailable."},
		// 源文自己是一句道歉（翻译它本来就该出 sorry）
		{"抱歉，本产品暂不可用。", "We are sorry, but this product is currently unavailable."},
		// 普通长句，既没有回绝措辞也没有翻译词根
		{"产品方案书 v5.1 包含定价、渠道与排期三部分。", "The product proposal v5.1 covers pricing, channels and scheduling."},
	}
	for _, c := range kept {
		if !IsTranslationUsable(c.src, c.out) {
			t.Errorf("正常译文被拒译判据误杀：src=%q out=%q", c.src, c.out)
		}
	}
}

// TestRefusalJudgeIsLoadBearing 反证：把拒译判据摘掉（等价于直接返回 false），
// 上面那批正腿必须转红——否则这条锁的射程其实落在别的判据上（§三「判据级自伤」）。
func TestRefusalJudgeIsLoadBearing(t *testing.T) {
	cases := []struct{ src, out string }{
		{"产品方案书", "I am unable to provide the translation you requested"},
		{"报价单", "I'm sorry, but I cannot help with that translation."},
	}
	for _, c := range cases {
		if !refusalResidue(c.src, c.out) {
			t.Fatalf("拒译判据本身没命中：src=%q out=%q", c.src, c.out)
		}
		// 同一条候选在「摘掉判据」的变异体下会落到后续判据，必须判可用，
		// 才证明正腿的红确实是这条判据撑起来的。
		if !usableWithoutRefusalJudge(c.src, c.out) {
			t.Fatalf("变异体仍判不可用 ⇒ 正腿不是这条判据撑起来的：src=%q out=%q", c.src, c.out)
		}
	}
}

// usableWithoutRefusalJudge 判据 9 之外的八类判据（反证专用，逐条抄自 IsTranslationUsable，
// 唯一的差别是不调 refusalResidue）。它刻意不抽进产品代码：这个函数只服务反证，
// 抽出去会让「摘掉判据」这件事变成一次产品侧改动。
func usableWithoutRefusalJudge(src, out string) bool {
	o := strings.TrimSpace(out)
	if o == "" || o == translationFailureMark {
		return false
	}
	if normalizeForCompare(src) == normalizeForCompare(out) {
		return !sourceHasTranslatableLetters(src)
	}
	if hasInstructionEcho(src, out) || hasLengthExplosion(src, out) ||
		nonLinguisticMustStayVerbatim(src, out) || degenerateToFunctionWord(src, out) ||
		quotedVerbatimSourceExplained(src, out) {
		return false
	}
	return true
}

func TestIsTranslationUsableRejectsUnusableCandidates(t *testing.T) {
	cases := []struct {
		name string
		src  string
		out  string
	}{
		{"空译文", "产品方案书", ""},
		{"纯空白译文", "产品方案书", "  \t\n "},
		{"批量失败占位", "产品方案书", translationFailureMark},
		{"同文回显", "产品方案书", "产品方案书"},
		{"同文但空白被折叠", "产品 方案\n书", "产品 方案 书"},
		{"指令回显残留（源文无尖括号·译文凭空多出）",
			"The system matches the built-in automotive database",
			"<Only output the final translated text, enclosed entirely within  and nothing else>"},
		{"闭标签残留", "#", "</t>"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if IsTranslationUsable(c.src, c.out) {
				t.Errorf("应判不可用：src=%q out=%q", c.src, c.out)
			}
		})
	}
}

// TestIsTranslationUsableKeepsLegitAngles 合法含尖括号的译文必须判可用：
// 源文本身带 '<'（数学式/代码片段）时，译文同样带 '<' 是正确交付而非指令回显。
func TestIsTranslationUsableKeepsLegitAngles(t *testing.T) {
	cases := []struct{ src, out string }{
		{"当 A<B 时不触发告警", "No alert is triggered when A<B"},
		{"if (i<n) { return }", "当 i<n 时直接返回"},
	}
	for _, c := range cases {
		t.Run(c.src, func(t *testing.T) {
			if !IsTranslationUsable(c.src, c.out) {
				t.Errorf("合法尖括号译文被误杀：src=%q out=%q", c.src, c.out)
			}
		})
	}
}

// TestIsTranslationUsableMarkdownHeading 讲「只输出译文」这类正文行不得被当指令误杀：
// 源文与译文同为正文（都不含 '<'）时判可用——这是不用短语黑名单的实测理由。
func TestIsTranslationUsableMarkdownHeading(t *testing.T) {
	src := "只输出译文，不要解释"
	out := "Output only the translation, with no explanation"
	if !IsTranslationUsable(src, out) {
		t.Fatalf("正常正文被误判为指令残留：src=%q out=%q", src, out)
	}
}

// ---------- F-38（2026-09-25 批 D）第 5 判据「短格长度爆炸」 ----------

// f38MailGarbage 生产实测形态的合成样本：6 字表格单元格被上游吐回 2,100+ 字符的英文邮件
// （账本留证的脏串开头即 "Dear Valued Customer"，本守卫上线后交付物里不应再出现任何 "Dear "）。
var f38MailGarbage = "Dear Valued Customer, " + strings.Repeat("thank you very much for your recent purchase, your order is confirmed. ", 35)

// TestIsTranslationUsableRejectsLengthExplosion 修复文档的字节级基准断言：6 字源 × 2100 字邮件 = false。
func TestIsTranslationUsableRejectsLengthExplosion(t *testing.T) {
	if len([]rune(f38MailGarbage)) <= 2100 {
		t.Fatalf("样本体积失真（应 ≥2100 rune 才能复现生产形态）: %d", len([]rune(f38MailGarbage)))
	}
	if IsTranslationUsable("产品方案书", f38MailGarbage) {
		t.Fatal("6 字短格 × 2100 字邮件必须判不可用（F-38 正是被前四判据全放行才进了交付物）")
	}
	// collectKBPass 同口径：爆炸候选不算命中、进补漏队列、未写入即计入未译清单。
	texts := []string{"产品方案书", "支持多格式文件翻译"}
	kbVal := []string{f38MailGarbage, "Multi-format file translation is supported"}
	accepted, needModelIdx := collectKBPass(texts, []bool{true, true}, kbVal, nil, IsTranslationUsable)
	if len(accepted) != 1 || accepted[0] != 1 {
		t.Fatalf("爆炸候选不得算命中：accepted=%v", accepted)
	}
	if len(needModelIdx) != 1 || needModelIdx[0] != 0 {
		t.Fatalf("爆炸候选必须进模型补漏队列：needModelIdx=%v", needModelIdx)
	}
	tr := map[string]string{texts[1]: kbVal[1]}
	missing := missingSegments(texts, tr)
	if len(missing) != 1 || missing[0] != texts[0] {
		t.Fatalf("爆炸段必须浮出水面计入未译（而不是被硬闸永久跳过），got %q", missing)
	}
}

// TestLengthExplosionBoundaries 判据边界等值锁：下限 80、8×src 缩放、>12 rune 源文不生效。
// 钉的是「只对短格生效避免误杀缩写展开/长语系」这条设计约束——把上限改成一刀切会在这里红灯。
func TestLengthExplosionBoundaries(t *testing.T) {
	cases := []struct {
		name string
		src  string
		out  string
		want bool // IsTranslationUsable 期望
	}{
		{"5字源_80rune贴线可用", "产品方案书", strings.Repeat("a", 80), true},
		{"5字源_81rune越界不可用", "产品方案书", strings.Repeat("a", 81), false},
		{"12字源按8倍缩放96线", "一二三四五六七八九十甲乙", strings.Repeat("b", 96), true},
		{"12字源_100rune不可用", "一二三四五六七八九十甲乙", strings.Repeat("b", 100), false},
		{"13字源超短格范围不生效", "一二三四五六七八九十甲乙丙", strings.Repeat("c", 5000), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsTranslationUsable(c.src, c.out); got != c.want {
				t.Fatalf("src=%d字 out=%d字: got %v want %v", len([]rune(c.src)), len([]rune(c.out)), got, c.want)
			}
		})
	}
	// 空源文不参与长度判据（由第 1 判据的空串口径负责），反向锁 hasLengthExplosion 本身。
	if hasLengthExplosion("", strings.Repeat("d", 500)) {
		t.Fatal("空源文不应触发长度爆炸判据（职责归第 1 判据）")
	}
	// 缩写展开反向锁（修复文档「避免误杀」的实测样例）：AWS→亚马逊云科技 必须可用
	if !IsTranslationUsable("AWS", "亚马逊云科技") {
		t.Fatal("合法缩写展开被长度判据误杀")
	}
}

// TestLengthExplosionThresholdsEnvOverridable 阈值 env 可配（修复文档「阈值 8×/300rune env 可配」）。
// 非法值（0/负数/非数字）必须回退默认——配置手滑不能把闸门拆成放行。
func TestLengthExplosionThresholdsEnvOverridable(t *testing.T) {
	t.Setenv("LC_GUARD_SHORT_RATIO", "1")
	t.Setenv("LC_GUARD_SHORT_FLOOR_RUNES", "10")
	if !hasLengthExplosion("一二三四五六七八九十甲乙", strings.Repeat("x", 13)) {
		t.Fatal("ratio=1/floor=10 未生效（12 字源上限应为 12）")
	}
	t.Setenv("LC_GUARD_SHORT_MAX_SRC_RUNES", "3")
	if hasLengthExplosion("一二三四五", strings.Repeat("y", 9000)) {
		t.Fatal("max_src=3 后 5 字源不应再进短格判据")
	}
	t.Setenv("LC_GUARD_SHORT_RATIO", "abc")
	t.Setenv("LC_GUARD_SHORT_FLOOR_RUNES", "-5")
	if got := shortGuardLimit(5); got != 80 {
		t.Fatalf("非法 env 应回退默认（5 字源→max(8×5,80)=80），got %d", got)
	}
	// absCap 兜底：ratio 被调巨大时短格仍封顶 300
	t.Setenv("LC_GUARD_SHORT_RATIO", "100000")
	t.Setenv("LC_GUARD_SHORT_FLOOR_RUNES", "1")
	if got := shortGuardLimit(12); got != 300 {
		t.Fatalf("absCap 兜底失效（应封顶 300），got %d", got)
	}
}

// TestCollectKBPassRejectsDirtyRow KB 脏行（源文=译文）场景：不算命中、进补漏队列，
// 且因为键未写入 langTranslations，收尾统计会把该段计入未译（而不是永久锁死硬闸）。
func TestCollectKBPassRejectsDirtyRow(t *testing.T) {
	texts := []string{"产品方案书 v5.1", "支持多格式文件翻译"}
	kbHitIdx := []bool{true, true} // 两段都「命中」了
	kbVal := []string{"产品方案书 v5.1", "Multi-format file translation is supported"}

	accepted, needModelIdx := collectKBPass(texts, kbHitIdx, kbVal, nil, IsTranslationUsable)
	if len(accepted) != 1 || accepted[0] != 1 {
		t.Fatalf("脏行不得算命中：accepted=%v，want [1]", accepted)
	}
	if len(needModelIdx) != 1 || needModelIdx[0] != 0 {
		t.Fatalf("脏行必须进模型补漏队列：needModelIdx=%v，want [0]", needModelIdx)
	}

	// 模拟补漏仍未译出（模型稳定回显）后的收尾统计：该段必须浮出水面计入 untranslated。
	tr := map[string]string{}
	for _, i := range accepted {
		tr[texts[i]] = kbVal[i]
	}
	missing := missingSegments(texts, tr)
	if len(missing) != 1 || missing[0] != texts[0] {
		t.Fatalf("脏行段必须计入未译清单，got %q", missing)
	}
}

// TestCollectKBPassBlockedSegmentNotRetried 敏感拦截段既不算命中也不进补漏（上游零暴露口径不变）。
func TestCollectKBPassBlockedSegmentNotRetried(t *testing.T) {
	texts := []string{"违规内容段", "正常段落"}
	accepted, needModelIdx := collectKBPass(texts, []bool{false, false}, []string{"", ""},
		map[string]bool{"违规内容段": true}, IsTranslationUsable)
	if len(accepted) != 0 {
		t.Fatalf("无命中时 accepted 应为空，got %v", accepted)
	}
	if len(needModelIdx) != 1 || needModelIdx[0] != 1 {
		t.Fatalf("仅未拦截段进补漏队列，got %v", needModelIdx)
	}
}

// ---------- ★ 2026-10-17 第二批：非语言段噪声 / 退化功能词 / 自述式解释 ----------

// t4NoisePairs 工单 T20261017234421W7H（交易看板截图 · pro · restore · en）**实测交付物**里的
// 全部噪声对（源段 → 交付物里那一段的字面值）。24 对一条不落：这批串当时**全部通过了旧判据**
// 写进了成品 md（64 段里 24 段），是本批新增三条判据的直接依据。改判据时先跑本表。
var t4NoisePairs = [][2]string{
	// 判据 ①：不需要翻译的段（数字/货币/日期时间）被译成 "and"
	{"0", "and"},
	{"4", "and"},
	{"7", "and"},
	{"1", "and"},
	{"0.00", "and"},
	{"10.03", "and"},
	{"100", "and"},
	{"435", "and"},
	{"9.19", "and"},
	{"1003", "and"},
	{"4716", "and"},
	{"¥1003.00", "and"},
	{"￥435.00", "and"},
	{"¥919.00", "and"},
	{"09-17 09:40:33", "and"},
	{"09-15 09:57:20", "and"},
	{"<", "and seems to be an meaningless filler in the translation, which does not constitute a valid linguistic element. Please revise the translation accordingly."},
	// 判据 ②：源段是实义词，译文整体退化成孤立功能词（含模型自己加的包装）
	{"manua", "` and `"},
	{"の", "and"},
	// 判据 ③：译文把源段逐字引号引用再另起一句解释
	{"manual", "manual  \nThe original term \"manual\" remains unchanged in the translation."},
	{"配件", "\"配件\" is a term used in the manual."}, // CJK 源段同样：引号逐字引用 + 一整句解释（且源段压根没被翻）
	// 既有判据（第 4 类指令回显）覆盖的两条：本批一并入表，防止将来被重构掉
	{"dragon_return", "<dragon_return>"},
	{"代码", "<code></code>"},
}

// TestIsTranslationUsableRejectsT4TicketNoise 真件回归：本批报障工单的那些串必须一律判不可用。
// 改坏了会怎样：成品 md / 原位回填图上会重新出现整排 "and" 与英文方法论（用户看到的就是这个）。
func TestIsTranslationUsableRejectsT4TicketNoise(t *testing.T) {
	for _, p := range t4NoisePairs {
		src, out := p[0], p[1]
		t.Run(src, func(t *testing.T) {
			if IsTranslationUsable(src, out) {
				t.Errorf("噪声串被判可用（会写进交付物）：src=%q out=%q", src, out)
			}
		})
	}
}

// t4LegitPairs 同一份真件里**正常译出**的段落（源段 → 交付物字面值）+ 几类必须放行的短译。
// 本表是「防误杀」的反向对照：新增判据若把这批打掉，成品会出现成片未译的中文/脱格，
// 比留在原地的 "and" 更糟（方向＝只砍模型多出来的，不砍模型没写够的）。
var t4LegitPairs = [][2]string{
	{"全局", "Global"},
	{"买入", "Buy"},
	{"时间", "Time"},
	{"动量", "Momentum"},
	{"代码", "code"}, // 与「代码→<code></code>」同源不同产物：正常译名必须放行
	{"元/次", "Yuan/time"},
	{"已同步", "Synchronized"},
	{"0胜／ 0负", "0 Wins / 0 Losses"},
	{"-11312.00", "-11312.00"},         // 数字列：模型原样回显（判据 ① 要求原样，但同文由第 3 类判据挡下，见下行说明）
	{"龙头战法 Dragon", "Dragon Strategy"}, // 中英混排
	{"关闭的战法信号不会进实盘链路（模拟盘不受影响）", "Closed strategy signals will not enter the live trading link (simulated trading is unaffected)"},
	{"和", "and"},              // ★ 源段本身就是功能词 ⇒ 判据 ② 必须放行（"和"→"and" 是正译）
	{"的", "of"},               // ★ 同上
	{"and", "y"},              // ★ 英语虚词译成目标语虚词是正译，不得被判据 ② 误杀
	{"the", "le"},             // ★ 同上（法语）
	{"manual", "user manual"}, // ★ 正常译名（带空格）不得被判据 ③ 误杀
	{"描述这一段是怎么标价的", "Codes that describe how this section is priced"},
}

// TestIsTranslationUsableKeepsT4LegitTranslations 真件里的正常译段落与几类短译必须放行。
func TestIsTranslationUsableKeepsT4LegitTranslations(t *testing.T) {
	for _, p := range t4LegitPairs {
		src, out := p[0], p[1]
		// 数字列「-11312.00 → -11312.00」是同文：第 3 类判据刻意判不可用（回显不算译文），
		// 文件链由预填原文交付（identitySegments），交付值一字不差——故此处不算「误杀」。
		if normalizeForCompare(src) == normalizeForCompare(out) {
			continue
		}
		t.Run(src, func(t *testing.T) {
			if !IsTranslationUsable(src, out) {
				t.Errorf("正常译文被误杀：src=%q out=%q", src, out)
			}
		})
	}
}

// TestSegmentNeedsTranslationBoundaries 判据 ① 的边界：什么算「需要翻译」按**字符类别**判，
// 这是「不需要翻译的段不送上游」与「模型噪声必须拒」两个消费者的共用尺子。
func TestSegmentNeedsTranslationBoundaries(t *testing.T) {
	need := []string{"全局", "dragon_return", "MANUAL", "ok", "сумма", "合計", "の", "fac_1"}
	for _, s := range need {
		if !segmentNeedsTranslation(s) {
			t.Errorf("应判为需要翻译：%q", s)
		}
	}
	// 不需要翻译：纯数字/货币/日期/符号/单字母/字母数字短标识（"a1"/"D" 这类表格里的标识）
	noNeed := []string{"0", "0.00", "4716", "¥1003.00", "￥435.00", "09-17 09:40:33",
		"-11312.00", "<", "---", "•", "D", "A", "a1", "+", "  ", "", "!!", "…"}
	for _, s := range noNeed {
		if segmentNeedsTranslation(s) {
			t.Errorf("不该判为需要翻译：%q", s)
		}
		if !NeedsIdentityDelivery(s) {
			t.Errorf("不需要翻译的段必须走原文交付（预填）：%q", s)
		}
	}
}

// TestIdentitySegmentsPrefillsVerbatimAndSkipsModel 预填语义：只对「不需要翻译」的段预填**原文**，
// 每种目标语言各一份，返回值供模型派发跳过。
// 改坏了会怎样：数字/日期段被重新送去上游 ⇒ 换回 "and"（本批报障）并计入未译出告警；
// 或者反过来把中文段落也预填成原文 ⇒ 整份文档不翻译却显示"已完成"。
func TestIdentitySegmentsPrefillsVerbatimAndSkipsModel(t *testing.T) {
	texts := []string{"全局", "4716", "已同步", "¥1003.00", "09-17 09:40:33", "manual"}
	langs := []string{"en", "ja"}
	got := map[string]map[string]string{}
	for _, lc := range langs {
		got[lc] = map[string]string{}
	}
	addTrans := func(lc, orig, tr string) { got[lc][orig] = tr }

	identity := identitySegments(texts, langs, addTrans)

	for _, s := range []string{"4716", "¥1003.00", "09-17 09:40:33"} {
		if !identity[s] {
			t.Errorf("不需要翻译的段必须进预填集合：%q", s)
		}
		for _, lc := range langs {
			if got[lc][s] != s {
				t.Errorf("预填值必须是原文本身：lang=%s src=%q got=%q", lc, s, got[lc][s])
			}
		}
	}
	for _, s := range []string{"全局", "已同步", "manual"} {
		if identity[s] {
			t.Errorf("需要翻译的段不得预填（否则等于跳过翻译）：%q", s)
		}
		if _, ok := got["en"][s]; ok {
			t.Errorf("需要翻译的段不得写入译文表：%q", s)
		}
	}

	// ★ 同源锁：预填集合与判据 ① 必须共用 segmentNeedsTranslation（改一处必须两处同时变）。
	// 反证：把 NeedsIdentityDelivery 改成恒真/恒假，本循环与上面两条断言必然一起红。
	for _, s := range texts {
		if identity[s] != NeedsIdentityDelivery(s) {
			t.Errorf("预填集合与 NeedsIdentityDelivery 不同源：src=%q identity=%v needsIdentity=%v",
				s, identity[s], NeedsIdentityDelivery(s))
		}
		// 且：预填集合里的段，模型给任何「不同」的串都必须判不可用（判据 ① 的射程）。
		if identity[s] && IsTranslationUsable(s, "and") {
			t.Errorf("预填段（无需翻译）应拒绝模型噪声：src=%q", s)
		}
	}

	// mergeSegSets 供模型派发只查一次集合：两集合的并集，且不改写原集合。
	blocked := map[string]bool{"manual": true}
	merged := mergeSegSets(blocked, identity)
	if !merged["manual"] || !merged["4716"] || merged["全局"] {
		t.Fatalf("并集内容不对：%v", merged)
	}
	if len(blocked) != 1 {
		t.Fatalf("mergeSegSets 不得改写入参：%v", blocked)
	}
}

// TestT4NewJudgesAreLoadBearing 三条新判据各自必须**独任**——即存在一对样本，
// 只有它能拦下、其余判据与既有四类都不拦：
//   - 若某条判据其实拦不下任何东西（写成了恒假/被上游判据抢先），本用例直接指出是哪条；
//   - 若见证样本被别的判据顺带拦下，则「摘掉这条判据」不会让任何断言变红 —— 判据级断言
//     代替不了守卫级（本仓老坑：反证 A11/A12 都曾被"别处的回退"掩护成假绿）。
func TestT4NewJudgesAreLoadBearing(t *testing.T) {
	type judge struct {
		name string
		fn   func(src, out string) bool
	}
	r1 := judge{"新①-非语言段必须原样", nonLinguisticMustStayVerbatim}
	r2 := judge{"新②-退化功能词", degenerateToFunctionWord}
	r3 := judge{"新③-引号自述解释", quotedVerbatimSourceExplained}
	all := []judge{
		{"既有-同文", func(s, o string) bool { return normalizeForCompare(s) == normalizeForCompare(o) }},
		{"既有-指令回显", hasInstructionEcho},
		{"既有-长度爆炸", hasLengthExplosion},
		r1, r2, r3,
	}
	witnesses := []judge{r1, r2, r3}
	samples := [][2]string{
		{"4716", "and"}, // 见 t4NoisePairs：整排 "and" 里的代表
		{"manua", "` and `"},
		{"manual", "manual  \nThe original term \"manual\" remains unchanged in the translation."},
		{"の", "and"},
	}
	for _, w := range witnesses {
		found := false
		for _, s := range samples {
			if !w.fn(s[0], s[1]) {
				continue
			}
			for _, other := range all {
				if other.name == w.name {
					continue
				}
				if other.fn(s[0], s[1]) {
					t.Errorf("%s 在样本 src=%q 上被「%s」抢先拦下 ⇒ 摘掉它不会让任何断言变红，见证不独任",
						w.name, s[0], other.name)
				}
			}
			if IsTranslationUsable(s[0], s[1]) {
				t.Errorf("%s 独任命中后 IsTranslationUsable 仍判可用：src=%q out=%q", w.name, s[0], s[1])
			}
			found = true
		}
		if !found {
			t.Errorf("%s 在见证样本集里一次都没命中 ⇒ 该判据形同不存在", w.name)
		}
	}
}

// TestIsTranslationUsableIdentityEchoWithoutSourceLetters 判据 3′（★ 2026-10-19 工单 107 收口，
// 用户令「无中文源回吐即视为原文交付」）。
//
// 缺陷形态：工单 107 的 12 段"未译出"里有 9 段是**无中文的 OCR 碎片**（股票代码 002584.SZ、
// 标识符 N_shape/dragon_return/fac_1/manua、单假名 の…）。它们被 segmentNeedsTranslation 的
// 「≥2 连排 ASCII 字母」判成"要翻"而送上游，模型原样回吐 ⇒ 旧判据 3 判不可用 ⇒ 每一轮都失败，
// 最终在工单里记一条"未译出"（成品字节其实与回落原文一致，纯属假告警）。
//
// 两侧都要钉：**有**非 ASCII 字母回吐仍须判不可用——那正是中文泄露形态，
// 把这半边放松＝把本函数存在的理由拆掉（本批真中文段 `金额`、`当前：全部允许…` 就在这一档）。
func TestIsTranslationUsableIdentityEchoWithoutSourceLetters(t *testing.T) {
	t.Run("无中文源回吐⇒原文交付", func(t *testing.T) {
		for _, s := range []string{
			"002584.SZ", "600279.SH", "600000.SH", // 股票代码
			"N_shape", "dragon_return", "fac_1", "manua", "dragon", // 标识符/拉丁词
			"#", "1", "•", "¥1003.00", "09-17 09:40:33", // 无上下文短串/数字/时间（注释里点名的同族）
			"  N_shape  ", // 空白折叠后仍同文
		} {
			if !IsTranslationUsable(s, strings.TrimSpace(s)) {
				t.Errorf("无非 ASCII 字母的源段回吐应判可用（原文交付），got false: src=%q", s)
			}
		}
	})
	t.Run("有中文源回吐⇒仍判不可用", func(t *testing.T) {
		for _, s := range []string{
			"金额", "当前：全部允许·仓位留空/0 =使用全局单票金额",
			"已实现=加权成本重放；浮动=市值-成本×数量；30s刷新", "の", "产品方案书",
		} {
			if IsTranslationUsable(s, s) {
				t.Errorf("含非 ASCII 字母的源段回吐必须判不可用（中文泄露形态），got true: src=%q", s)
			}
		}
	})
}
