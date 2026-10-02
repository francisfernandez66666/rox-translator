// ============ reply_instr_echo.go · 职责说明 ============
// ★ D-LLM-20261001-001 的**根治腿**（P2：引用重合度过滤器，2026-10-02 用户令「按p2做」落地）。
//
// 这一族缺陷此前一直靠「列词表打地鼠」收（reply_lang_check.go 的 selfNarrationMarkers／
// internalEchoMarkers）——词表天然追不上模型措辞，每轮都是等用户带截图来报、再补几条词条。
// 本文件把判据换成**比对提示词自身**：模型能漏出来的，正是 persona／tone_rules／promise_rules／
// 【回复语言】里那些「交代怎么答题」的散文；把这几段单独收成语料 I，出站时拿每条正文去和 I 比重合度，
// 够到阈值即判「这是被抄出来的指令句」→ 整句丢弃 + 记 WARN。**无需枚举新词**，这才是「彻底」两字的落点。
//
// 语料 I 的取舍（决定误伤半径，务必读）：
//   - **进 I**：persona + tone_rules + promise_rules + replyLangBlock（都是纯指令散文，
//     对外永远不出现，客户正常回答更不可能逐字复述它们）；
//   - **不进 I**：【相关知识】（RAG 素材，客户问的就是它，比对会误杀正确引用）
//     与【系统现值】（含价格系数与现算示例数字，合法报价**本就该逐字引用**它）。
//     ⚠️ 【报价纪律】那句虽属指令，却**长在【系统现值】段里**（system_values.go 的 renderQuoteExamples），
//     要单独抠它就得把整段数字一起拖进来当语料——那是把「合法引用单价」误杀成一族新缺陷。
//     故本腿**刻意不纳入** systemValuesBlock：纪律类数字核验另有 guardReplyQuote 那道专闸兜着，
//     而真正在漏的方法论旁白全部来自 persona／tone_rules（现网四轮取证逐条对上），不靠这句也能收。
//
// 判据两条，任一命中即判旁白（阈值取向见下面 instrEcho* 常量说明）：
//
//	① 连续重合：正文归一后有一段**连续 ≥14 字**都落在 I 的字符 4-gram 集合里（＝逐字抄了指令句）；
//	② 整句包含度：整句的 4-gram 有 **≥0.6** 能在 I 里找到（＝换了几个词但骨架照搬指令句）。
//
// 与纯词面腿的分工：sanitizeVisitorText 里的 stripRuleSelfReference／stripTrailingNarrationTail
// 收的是**改写式**旁白（换词、缩句，够不到重合阈值）；本文件的重合度腿收的是**逐字/近逐字复述**。
// 两条互补，缺一不可——只上重合度会漏掉「简短承认后引回业务」这类改写形，只上词表会永远慢模型一步。
//
// 挂载点：这是**需要每轮语料**的守卫，故不塞进纯函数 sanitizeVisitorText（那个还服务历史回放与补翻，
// 语料口径不同），而是按 AGENTS §一·13「新增守卫一律挂 Respond 这条咽喉」，只在 Source=="llm" 上跑
// （话术直配／流程／兜底都是人写的文案，不经模型、不可能复述提示词，跑它纯属浪费且有理论误伤）。
// =============================================
package engine

import (
	"context"
	"strings"
	"unicode"

	"translator/internal/observability"
)

// instrEchoMinSentRunes 一条正文句子归一后**至少这么长**才拿去比重合度。
// 短于它的句子（「能，但得看你要翻什么」）凑不满连续 14 字、包含度也没有统计意义，
// 判它只会被一两个公共 4-gram 顶成误杀——长度不足一律放行。
const instrEchoMinSentRunes = 12

// instrEchoRunGrams ①「连续重合」阈值：正文里要有这么**多个连续 4-gram** 全在 I 里才判抄。
// k 个连续命中的 4-gram ＝ k+3 个连续字，取 11 即对应「连续 ≥14 字逐字命中」这一档判据。
const instrEchoRunGrams = 11

// instrContainMin ②「整句包含度」阈值：整句去重 4-gram 里落在 I 的比例下限。
// 取 0.6：一条被换过词的复述指令句，骨架 gram 大多还在（远超 0.6）；
// 而一句正常客户话术里能凑出这么多「提示词专有 4-gram」的概率极低。
const instrContainMin = 0.6

// instrContainMinGrams 判包含度前，句子至少要有这么多**个不同 4-gram**，否则 0.6 的统计量太薄
// （5 个 gram 里 3 个命中＝0.6，可能只是撞了「积分」「源字符」这类也进 I 的高频业务词）。
const instrContainMinGrams = 6

// instructionCorpus 从指令段（persona／tone_rules／promise_rules／回复语言）拼出语料 I，
// 归一成字符 4-gram 集合。入参 answerLang 必须与本轮**真发给模型的那段**同源（Respond 传下来的作答语言），
// 否则 replyLangBlock 档位对不上，重合判据就和模型实际能抄到的东西错位。
func (e *Engine) instructionCorpus(answerLang string) map[string]struct{} {
	blocks := []string{
		e.db.GetConfig("persona", defaultPersona),
		e.db.GetConfig("tone_rules", defaultToneRules),
		e.db.GetConfig("promise_rules", defaultPromiseRules),
		replyLangBlock(answerLang),
	}
	grams := make(map[string]struct{})
	for _, b := range blocks {
		addCharGrams(grams, normalizeForEcho(b))
	}
	return grams
}

// guardReplyInstructionEcho 出站咽喉上的引用重合度过滤（调用点见 engine.go Respond，排在 reharden 之后）。
// 逐句与 I 比重合度，命中即判「抄出来的指令句」→ 整句丢弃 + WARN（原句进日志攒证据）。
// 只管 Source=="llm"：话术／流程／兜底不经过模型，不可能复述提示词。
//
// 两条 fail-soft（与全链一致，误删客户正文的代价高于漏掉一句旁白）：
//   - **剥空即回退**：整条回复全被判旁白属异常形态（多半是语料被误灌），宁可原样发出也不发空气泡；
//   - **控制序列所在句豁免**：正文里若还挂着【go:key】（理论上 postProcess 已摘，真出现＝标记与旁白同行），
//     整句跳过，别把功能入口连坐删掉（同 guardReplyQuote 那条口径）。
func (e *Engine) guardReplyInstructionEcho(ctx context.Context, answerLang string, rep *Reply) *Reply {
	if rep == nil || rep.Content == "" || rep.Source != "llm" {
		return rep
	}
	grams := e.instructionCorpus(answerLang)
	if len(grams) == 0 {
		return rep // 语料为空（配置异常）＝无判据可依据，不动正文
	}
	parts := splitNarrationSentences(rep.Content)
	kept := make([]string, 0, len(parts))
	var dropped []string
	for _, s := range parts {
		if strings.TrimSpace(s) == "" || strings.Contains(s, goMarkerHead) {
			kept = append(kept, s)
			continue
		}
		norm := normalizeForEcho(s)
		if isEchoedInstruction(norm, grams) {
			dropped = append(dropped, strings.TrimSpace(s))
			continue
		}
		kept = append(kept, s)
	}
	if len(dropped) == 0 {
		return rep
	}
	joined := strings.TrimSpace(strings.Join(kept, ""))
	observability.Warn(ctx, "assist.engine 正文出现与提示词指令段高重合的句子，已整句丢弃（引用重合度过滤）",
		"lang", canonicalLang(answerLang), "source", rep.Source, "count", len(dropped),
		"sentences", strings.Join(dropped, " | "))
	if joined == "" {
		return rep // 全被判抄＝异常，宁可不发空泡，原样留着（连同上面这条 WARN 一起交人看）
	}
	rep.Content = joined
	return rep
}

// isEchoedInstruction 归一后的正文句子是否与语料 I 高重合（两条腿任一命中即真）。
func isEchoedInstruction(norm []rune, grams map[string]struct{}) bool {
	if len(norm) < instrEchoMinSentRunes {
		return false // 太短不判（见常量说明）
	}
	// ① 连续重合：数最长的、每 4-gram 都在 I 里命中的连续 run。
	if longestGramRun(norm, grams) >= instrEchoRunGrams {
		return true
	}
	// ② 整句包含度：去重 gram 里命中 I 的比例。
	total, hit := 0, 0
	seen := make(map[string]struct{})
	for g := 0; g+4 <= len(norm); g++ {
		key := string(norm[g : g+4])
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		total++
		if _, ok := grams[key]; ok {
			hit++
		}
	}
	if total >= instrContainMinGrams && float64(hit)/float64(total) >= instrContainMin {
		return true
	}
	return false
}

// longestGramRun norm 里「每个 4-gram 都命中 I」的最长连续 run（按 gram 个数计）。
// 命中即接着数、落空即归零重数；返回的是连续命中的 gram 个数（k 个 gram ＝ k+3 连续字）。
func longestGramRun(norm []rune, grams map[string]struct{}) int {
	best, cur := 0, 0
	for g := 0; g+4 <= len(norm); g++ {
		if _, ok := grams[string(norm[g:g+4])]; ok {
			cur++
			if cur > best {
				best = cur
			}
		} else {
			cur = 0
		}
	}
	return best
}

// addCharGrams 把归一串的全部 4-gram 收进集合（语料侧用，重复无所谓）。
func addCharGrams(dst map[string]struct{}, rs []rune) {
	for g := 0; g+4 <= len(rs); g++ {
		dst[string(rs[g:g+4])] = struct{}{}
	}
}

// normalizeForEcho 归一：转小写、只留字母／数字（unicode.IsLetter／IsDigit 覆盖 CJK／假名／谚文），
// 丢掉空白、标点、全角符号与 emoji。这样正文与语料的标点差异（「，」／空格／括号）不会打断重合，
// 判据问的是「这段字的**序列**是否出自提示词」，而不是「连标点都一模一样」。
func normalizeForEcho(s string) []rune {
	var out []rune
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			out = append(out, r)
		}
	}
	return out
}
