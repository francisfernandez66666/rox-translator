// ============ han_residue.go · 职责说明 ============
// 「译文里留着没翻的中文词」的判残与补翻（★ 082x 第七条，2026-09-29 换件后现网复问抓到）。
//
// 现场是这样的：术语口径进翻译提示词、缓存指纹也修好了，英文首屏确实写出了 "credits"，
// 可整句是 `credits充值……What would you like to know?`——**术语对了，句子只翻了一半**；
// 同一轮的日文首屏更典型：`文書翻訳・会話翻訳・企業用語ベース・ポイント充実……「翻訳什么？」と只会让你更快帮你处理`
// 后半截直接是中文。这跟「模型答错语言」不是同一件事：那条是整个回答是中文，补翻能救；
// 这条是**补翻自己没翻完**，再补一句更凶的提示词也还是请求，不是保证。
//
// 所以这里做的是「判残 + 一次带残片的补翻」：
//   - 判残（hanResidueRuns）：按语种给不同的残留定义，日文单独一档（它正文本来就写汉字）；
//   - 补翻（repairHanResidue）：把残片点名交给模型重写整段，**只在同时满足
//     「残片变少」＋「行数没变」**时才采用新稿，否则保留上一稿并记 Warn。
//     宁可让客户看到"credits充值"这种半截译文（丑但数字是对的），
//     也不许拿一份可能被改坏总额的新稿替换旧稿——补翻是修饰，不是重写业务口径。
//
// ⚠️ 为什么补翻只打一次：greet 在访客打开挂件的关键路径上，多次往返就是把首响应拖成秒级。
//
//	一次不成就收手，让日志那行 Warn 成为这条软路径唯一的露面机会（同 localize.go 失败口径）。
//
// ⚠️ 为什么 canned 与对话正文共用这一条：两条路都走 translateOnce 这个底座，
//
//	分开放就迟早长出一边修好、另一边照漏的形态（本仓 074x/082x 两批都是这么收尾的）。
//
// =============================================
package engine

import (
	"context"
	"strings"

	"translator/internal/assist/llm"
)

// simplifiedOnlyRunes 「简体字独有、日文正字法里不长这样」的常用字（判残用，日文档专用）。
//
// 日文里 翻訳／ポイント 是正常写法，不能因为有汉字就判残留；能判的是
// 「这一段照抄了中文源文，而且里面有一个日文不会这么写的字形」。
// 这份表刻意做小（只收高频、字形差异确凿的），因为它**只决定要不要多打一次补翻**：
// 误报=白花一次调用，漏报=维持现状（跟今天没这条时一样），两种都不会把正文改坏。
// 逐字核对过日文正字法：值 U+503C 是简体写法，日文用 値 U+5024；「网/页/运/输/费/业/术/优/获/产/
// 让/说/设/记/远/层/简」同理都是日文不写的字形；「么」是纯简体字（日文「什麼」写作「何」）。
const simplifiedOnlyRunes = "译积关书询录价运网页输费业术优获产么让说设记远层简值对邮们搜"

// jaChineseWordForms 中文词形：每个字**单独看**日文都可能写（文、件、信、息都有日本汉字形态），
// 但**这一组合**日文不这么写（★ 082x 第八条：现网日文首屏残留「文件翻訳」，
// 上一档字形判据完全抓不到——文和件都不在简体独有表里，而日文该写「ファイル翻訳」）。
//
// 收录门槛比上面那张字表更严：只收「日文另有固定写法、且我们在做的是中→日翻译」的词，
// 且候选仍必须**能在中文源文里原样找到**才判残（见 jaLeakSubstrings）。
// 刻意不收「会話／情報」这类日文里确实存在的写法，收了就是把好译文送去重写。
var jaChineseWordForms = []string{
	"文件", "邮件", "搜索", "软件", "电脑", "手机号", "为什么", "什么时间",
	"多少钱", "怎么", "咱们", "信息", "网址", "账号", "网盘",
}

// hanResidueRuns 找译文里「没翻成的中文片段」，按语种分档；返回去重后的片段列表（空＝干净）。
//
//   - 中文系访客（zh／zh_hant／未提供 lang）：一律 nil——给他们的本来就是中文，判残没有意义，
//     而且中文界面里出现汉字是**正常渲染**，误判会让每次 greet 白打一次补翻。
//   - 日文：只认「能在中文源文里原样找到、且含简体独有字形」的汉字段（见上面那张表的注释）。
//   - 其余语种（拉丁／西里尔／阿拉伯／泰／谚文……）：任何汉字段都是残留。
//     这些语种的界面里一个汉字都不该出现，包括术语「积分」——它必须写成 credits/ポイント/кредитов
//     那张表里的说法（见 reply_lang.go 的 pointsTermByLang）。
func hanResidueRuns(uiLang, src, out string) []string {
	c := canonicalLang(uiLang)
	if out == "" || c == "" || c == "zh" || c == "zh_hant" {
		return nil
	}
	runs := hanRunsOf(out)
	if len(runs) == 0 {
		return nil
	}
	if c != "ja" {
		return runs
	}
	return jaLeakSubstrings(src, runs)
}

// jaLeakSubstrings 日文档的判残：每个汉字段里，取「**能在中文源文里原样找到**、
// 且含简体独有字形」的**最长**子串（2~6 字，一段最多报一个）。
//
// 为什么不能整段比对：现网那条是 `翻訳什么？`——"翻訳" 是日文字形、"什么" 是照抄的中文，
// 两段连成一个汉字段，整段拿去查源文必然查不到（源文写的是「翻译什么」），
// 于是最典型的残留反而判不出来。取子串才对齐得上。
// 为什么要「含简体独有字形」：日文里 翻訳／会話／企業 这类汉字词是**正常正文**，
// 而它们中的某些两字组合恰好也能在中文源文里对上（例如「企業」），
// 只按「源文里有」判就会把好端端的日文译文判成残留，每次 greet 白打一次补翻、
// 还把没问题的译文送去重写——那是把修好的东西再弄坏一次。
func jaLeakSubstrings(src string, runs []string) []string {
	seen := make(map[string]bool, len(runs))
	out := make([]string, 0, len(runs))
	for _, run := range runs {
		rs := []rune(run)
		best := ""
		for i := 0; i < len(rs); i++ {
			for j := i + 2; j <= len(rs) && j <= i+6; j++ {
				cand := string(rs[i:j])
				if !strings.Contains(src, cand) || !(containsSimplifiedOnlyRune(cand) || containsJaChineseWordForm(cand)) {
					continue
				}
				if len(cand) > len(best) {
					best = cand
				}
			}
		}
		if best != "" && !seen[best] {
			seen[best] = true
			out = append(out, best)
		}
	}
	return out
}

// hanRunsOf 取出串里的连续汉字段（U+4E00–U+9FFF，与探针 HAN 判据同口径），按出现顺序去重。
func hanRunsOf(s string) []string {
	var out []string
	seen := map[string]bool{}
	cur := strings.Builder{}
	flush := func() {
		if r := cur.String(); r != "" && !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
		cur.Reset()
	}
	for _, r := range s {
		if r >= 0x4e00 && r <= 0x9fff {
			cur.WriteRune(r)
			continue
		}
		flush()
	}
	flush()
	return out
}

// containsSimplifiedOnlyRune 段里是否含简体字独有的字形（日文档判残用）。
func containsSimplifiedOnlyRune(s string) bool {
	return strings.ContainsAny(s, simplifiedOnlyRunes)
}

// containsJaChineseWordForm 段里是否含「日文不这么写」的中文词形（日文档判残第二档，
// 管的是字形相同、词形不同的那一批：文件／邮件／信息——见 jaChineseWordForms 的收录门槛）。
func containsJaChineseWordForm(s string) bool {
	for _, w := range jaChineseWordForms {
		if strings.Contains(s, w) {
			return true
		}
	}
	return false
}

// lineCountOf 非空行数。补翻前后必须一致——chips 那条路靠行数拆回（LocalizeChips），
// 欢迎语也按行渲染；一份"翻得更干净但少了两行"的新稿是把内容删没了，不算改善。
func lineCountOf(s string) int {
	n := 0
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			n++
		}
	}
	return n
}

// repairHanResidue 把残片点名，让模型整段重写一次；返回「新稿 + 是否采用」。
// 采用的三条硬判据：调用成功且没被 max_tokens 截断、行数与上一稿一致、残片数量**严格变少**。
// 任一不成立就回 false，调用方保留上一稿并记 Warn——这里绝不做的是"拿一份没验过的新稿换掉旧稿"。
func (e *Engine) repairHanResidue(ctx context.Context, client *llm.Client, uiLang, src, draft string,
	leaks []string, maxTokens int) (string, bool) {
	label := langLabel(uiLang)
	if label == "" || client == nil || !client.Enabled() || len(leaks) == 0 {
		return "", false
	}
	prompt := "上一版" + label + "译文里有这些中文词没翻成 " + label + "：" + strings.Join(leaks, "、") +
		"。请把下面这段译文**整段重写一遍**：那些词换成 " + label + " 里的自然说法，" +
		"其余措辞尽量照抄上一版；行数与上一版完全一致，一行对应一行，" +
		"不要加解释、不要加引号、不要输出思考过程，也不许改任何数字。\n" +
		translateContract(uiLang) +
		"\n【中文原文】\n" + src + "\n【上一版" + label + "译文】\n" + draft + "\n---"
	out, _, usage, err := client.Chat(ctx, localizeTemperature, maxTokens,
		[]llm.Message{{Role: "user", Content: prompt}})
	if err != nil || usage.Truncated {
		return "", false
	}
	out = cleanTranslated(out)
	if out == "" || lineCountOf(out) != lineCountOf(draft) {
		return "", false
	}
	if len(hanResidueRuns(uiLang, src, out)) >= len(leaks) {
		return "", false // 没改善就收手，别拿一份同样带残片的新稿去覆盖
	}
	return out, true
}
