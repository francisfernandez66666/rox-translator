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
// ★ 092x 红腿一（2026-09-29 现网复问第四条）把判残＋补翻真正接到了**对话正文出站**：
//
//	此前"共用"只到 canned 那一路——整段回中文的回答会被 enforceReplyLang 拖去翻译，
//	顺路就过了这段补翻；而"日文句子嵌两个中文词"这种混排既不触发整段补翻、
//	又从不经过 translateOnce ⇒ 一路漏到客户屏幕。现在两条路各有一个入口
//	（repairHanResidue / repairReplyHanResidue），底座与三条硬判据只有一份
//	（repairHanResidueBase）。
//
// ★★ 093x（2026-09-30 换件后当天真机挂件复问又抓到一条，同一段日文回答的四个漏点）：
//
//	「例えば1,000文字の日本語を翻訳する場合、**快速モードで150ポイント**か、
//	**プロfessionalモードで400ポイント**か选択が必要です。…実際の扣费は…入力语言が…」
//	这一条把上一批的三条口径逐条打穿，本机把原文喂进判残复现出来的结论是：
//	  ① 只有「扣费」被抓到（费 在字形档里），「选択」「入力语言」漏——**选／语这两个简体字形
//	     根本不在表里**；「系数」漏——系／数 简日同形，字形档天生管不到，只能走词形档；
//	  ② 「プロfessionalモード」一个汉字都没有，本文件原来那两条腿（字形／词形）对它全部失明；
//	  ③ 补翻提示词写的是「其余措辞尽量照抄上一版」——于是即便新稿被采用，
//	     没进 leaks 清单的那几处坏词是**被要求照抄过来的**，等于只修被抓到的那一处。
//	这三条合起来才是"换件没修"的真实形态：不是判残函数没接上，是判残尺子窄到只剩一条腿。
//	本批据此：字形档补 12 个字（逐字过 cp932 复核）、词形档补 3 个词、
//	新立第三条腿 jaLatinIntrusions（假名直接嵌小写拉丁），提示词改成"点名的每一处都必须改掉"。
//	⚠️ 同批还有一条不属本文件的漏口：**补翻产物不再过末道卫生**（写歪的控制序列
//	「[ pricing ページで詳細を確認]」就是补翻把【go:pricing】换形后原样出栈的），
//	修法与判据见 engine.go 的 rehardenReplyRewrite。
//
// =============================================
package engine

import (
	"context"
	"regexp"
	"strings"

	"translator/internal/assist/llm"
	"translator/internal/observability"
)

// simplifiedOnlyRunes 「简体字独有、日文正字法里不长这样」的常用字（判残用，日文档专用）。
//
// 日文里 翻訳／ポイント 是正常写法，不能因为有汉字就判残留；能判的是
// 「这一段照抄了中文源文，而且里面有一个日文不会这么写的字形」。
// 这份表刻意做小（只收高频、字形差异确凿的），因为它**只决定要不要多打一次补翻**：
// 误报=白花一次调用，漏报=维持现状（跟今天没这条时一样），两种都不会把正文改坏。
// 逐字核对过日文正字法：值 U+503C 是简体写法，日文用 値 U+5024；「网/页/运/输/费/业/术/优/获/产/
// 让/说/设/记/远/层/简」同理都是日文不写的字形；「么」是纯简体字（日文「什麼」写作「何」）。
//
// ★ 093x（2026-09-30 真机挂件复问实证）补了十二个字形：这一批现网漏出的
// 「选択」「入力语言」这一族不是"没翻完"，是**根本没进这张表**。逐字对过码点
// （简体 → 日文正字）：语 U+8BED→語、选 U+9009→選、额 U+989D→額、图 U+56FE→図、
// 线 U+7EBF→線、现 U+73B0→現、专 U+4E13→専、释 U+91CA→釈、负 U+8D1F→負、
// 达 U+8FBE→達、过 U+8FC7→過、义 U+4E49→義。
// ✅ 这张表 42 个字形本机逐字用 `python3 -c "ch.encode('cp932')"` 复核过（cp932＝JIS X 0208＋
// NEC/IBM 厂商扩展，日文正字法用字大体落在里面）：**39 个直接编码失败**（日文标准字符集里根本没有这个字形），
// 只有 价／网／搜 三字能编进去——但它们是厂商扩展位带的生僻字，日文正字仍是
// 価（U+4FA1，价格→価格）／網（U+7DB2，网页→ウェブページ）／検索（搜索→検索），
// 三字按"日文不这么写"留在表里是对的。
// ⇒ 今后往这张表加字，先用这条命令过一遍：编不进去＝基本可以确定；编得进去必须再查日文正字，
//
//	编得进去**不等于**日文会这么写，也不等于不许收（上面那三个就是反例）。
//
// ⚠️ 刻意不收「号／制／属／全／文／件」这类简日同形的字（收进来就是把正常日文判成残留），
// 也不收「据」——日文有 据える 这个汉字，「数据」那一族只能走下面 jaChineseWordForms 的词形档。
const simplifiedOnlyRunes = "译积关书询录价运网页输费业术优获产么让说设记远层简值对邮们搜语选额图线现专释负达过义"

// jaChineseWordForms 中文词形：每个字**单独看**日文都可能写（文、件、信、息都有日本汉字形态），
// 但**这一组合**日文不这么写（★ 082x 第八条：现网日文首屏残留「文件翻訳」，
// 上一档字形判据完全抓不到——文和件都不在简体独有表里，而日文该写「ファイル翻訳」）。
//
// 收录门槛比上面那张字表更严：只收「日文另有固定写法、且我们在做的是中→日翻译」的词，
// 且候选仍必须**能在中文源文里原样找到**才判残（见 jaLeakSubstrings）。
// 刻意不收「会話／情報」这类日文里确实存在的写法，收了就是把好译文送去重写。
//
// ★ 093x（2026-09-30 真机复问）补的词都满足同一门槛，且**字形档抓不到**（每个字日文都单独成立）：
// 系数→日文写「係数」、数据→「データ」、折扣→「割引」。
// 现网那条「系数」正是这一档漏出的：系/数 两个字形简日通用，只有词形能判。
// 「折扣」走词形而不是字形：折／扣 两个字形都能编进 cp932（日文里有这两个字），单看字形定不了性，
// 但它们只会以「折扣」这个中文词形漏进我们的日文译文——日文那侧的固定说法是「割引」。
// 刻意不补「专业／余额／网络」——它们各自的简体字形（专/额/网）已在上一档，同一处残片不需要两条腿各抓一遍。
var jaChineseWordForms = []string{
	"文件", "邮件", "搜索", "软件", "电脑", "手机号", "为什么", "什么时间",
	"多少钱", "怎么", "咱们", "信息", "网址", "账号", "网盘",
	"系数", "数据", "折扣",
}

// hanResidueRuns 找译文里「没翻成的中文片段」，按语种分档；返回去重后的片段列表（空＝干净）。
//
//   - 中文系访客（zh／zh_hant／未提供 lang）：一律 nil——给他们的本来就是中文，判残没有意义，
//     而且中文界面里出现汉字是**正常渲染**，误判会让每次 greet 白打一次补翻。
//   - 日文：只认「能在中文源文里原样找到、且含简体独有字形」的汉字段（见上面那张表的注释），
//     再叠第三条腿「假名直接嵌小写拉丁」的半截词（★ 093x，见 jaLatinIntrusions——
//     现网那条「プロfessionalモード」一个汉字都没有，汉字段判据对它完全失明）。
//   - 其余语种（拉丁／西里尔／阿拉伯／泰／谚文……）：任何汉字段都是残留。
//     这些语种的界面里一个汉字都不该出现，包括术语「积分」——它必须写成 credits/ポイント/кредитов
//     那张表里的说法（见 reply_lang.go 的 pointsTermByLang）。
func hanResidueRuns(uiLang, src, out string) []string {
	c := canonicalLang(uiLang)
	if out == "" || c == "" || c == "zh" || c == "zh_hant" {
		return nil
	}
	if c != "ja" {
		return hanRunsOf(out)
	}
	leaks := jaLeakSubstrings(src, hanRunsOf(out))
	return append(leaks, jaLatinIntrusions(out)...)
}

// replyHanResidueRuns **对话正文**的判残（★ 092x 红腿一）。与上面 hanResidueRuns 的差集只有一处，
// 但这一处是形态决定的：对话正文没有"中文源文"这个对照面——那条日文回答里的「文件」「費」
// 不是从哪份源文抄来的，是模型自己把中文词形写进了日文句子，
// 所以 canned 那条的「必须在源文里原样找到」在这里既无从下手、也不该下手。
//
// 剩下的判据都只看字形/词形本身：含简体独有字形（费／译／积…），
// 或含 jaChineseWordForms 里"日文另有写法"的中文词形（文件／邮件／信息／系数…），
// 再加★ 093x 那条「假名嵌拉丁」腿（プロfessional）——三条腿都只读物本身，不需要源文。
// 非中文、非日文的作答语种仍按"任何汉字段都是残留"（同 canned 那一路）。
//
// 误伤半径：日文正文里「文件」这类词形出现，客户看到的就是半中半日；
// 唯一真正的例外是**引用访客自己的中文材料**（「您那份《文件清单》里…」）。
// 这种引用会被判残、白花一次补翻——三条硬判据（未截断／行数一致／残片严格变少）
// 兜住"改坏"，代价只有一次上游调用。宁可这样，也不留「文件翻訳」在客户屏幕上。
func replyHanResidueRuns(answerLang, text string) []string {
	c := canonicalLang(answerLang)
	if text == "" || c == "" || c == "zh" || c == "zh_hant" {
		return nil
	}
	runs := hanRunsOf(text)
	if c != "ja" {
		return runs
	}
	out := make([]string, 0, len(runs))
	seen := make(map[string]bool, len(runs))
	for _, r := range runs {
		if !containsSimplifiedOnlyRune(r) && !containsJaChineseWordForm(r) {
			continue
		}
		if !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	return append(out, jaLatinIntrusions(text)...)
}

// jaLatinIntrusionPat 「假名词写了一半就换拉丁」的混排形态（★ 093x，2026-09-30 真机挂件复问实证：
// 现网日文正文里的「**プロfessionalモード**」——模型想写「専門モード」或「プロフェッショナルモード」，
// 词头留在片假名、词身直接抄了英文 professional）。
//
// 三条口径收得很窄，每一条都对着"正常日文里确实会出现的英文"让过：
//   - 只认**片假名**打头，平假名一律不算：「このappは」这种口语混写不在射程
//     （平假名后面挂英文是风格问题，片假名后面挂小写英文是**词被劈开了**，两种形态不能混治）；
//   - 只认**紧跟假名、中间没有空格**的小写拉丁，且 ≥2 个字母：
//     「pricing ページ」「PDFのファイル」「OKです」「Word/Excel/PPT/PDF」「1,000文字」全部不命中
//     （拉丁在假名之前、或大写、或隔空格，都是正常排版）；
//   - 长音符 ー 也算假名侧（「コンピューera」是同一种病）。
//
// 命中只送去补翻、绝不就地替换：服务端猜不出「プロfessional」该落成「専門」还是「プロフェッショナル」，
// 而补翻那三条硬判据保证"猜错就不采用"——这条腿的作用是**让它进入射程**，不是替模型写作文。
var jaLatinIntrusionPat = regexp.MustCompile(`[ァ-ヴー]+[a-z]{2,}`)

// jaLatinIntrusions 取出日文正文里「假名嵌拉丁」的半截词（按出现顺序去重，空＝干净）。
func jaLatinIntrusions(text string) []string {
	if text == "" {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, m := range jaLatinIntrusionPat.FindAllString(text, -1) {
		if !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}
	return out
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
//
// 这一条是 canned（欢迎词／chips）那一路的入口：它有**中文原文**可比，
// 所以判残用的是 hanResidueRuns（"必须在源文里原样找到"那一档才成立）。
// 对话正文走 repairReplyHanResidue，判据换成 replyHanResidueRuns（没有源文，见那里）。
func (e *Engine) repairHanResidue(ctx context.Context, client *llm.Client, uiLang, src, draft string,
	leaks []string, maxTokens int) (string, bool) {
	return e.repairHanResidueBase(ctx, client, uiLang,
		"\n【中文原文】\n"+src, draft, leaks, maxTokens,
		func(out string) []string { return hanResidueRuns(uiLang, src, out) })
}

// repairReplyHanResidue 对话正文出站的汉字残留补翻（★ 092x 红腿一，2026-09-29 现网复问第四条）。
//
// 现场：日文轮的回答主体是合格日文，句子里却嵌着「文件翻訳」与「ポイント充費」这类
// 中文词形／简体字形——既不是"整段回了中文"（enforceReplyLang 按占比判，抓不到几条字），
// 也不是 canned 文案（那一路的补翻早就接上了）。同一族缺陷第四次靠用户截图发现，
// 这一条把它接到**对话正文的唯一出站咽喉**上（调用点见 engine.go Respond）。
//
// 与 canned 那一路的两处差别，都是"对话正文"这个形态逼出来的：
//  1. 判残不再要求「能在中文源文里原样找到」——对话正文没有源文，模型是直接用对方语言写的；
//  2. 额度用 replyLocalizeMaxTokens（一条回答可能二十行，400 会翻出半句，见 reply_lang_check.go）。
//
// 补翻只许改善、不许换坏：三条硬判据一条不落（未截断／行数一致／残片严格变少），
// 不成立就原样留着并记 WARN——让客户看到「文件翻訳」这种半中半日的句子（丑但内容是真的），
// 也比拿一份可能被改坏额度数字的新稿覆盖旧稿安全。
func (e *Engine) repairReplyHanResidue(ctx context.Context, answerLang string, rep *Reply) *Reply {
	if rep == nil || rep.Content == "" {
		return rep
	}
	// **整段回错语言的正文不在这一条的射程**（判据用 replyLangMismatch，与整段补翻同一条尺子）：
	// 那条由上层的 enforceReplyLang 负责，它翻成功就不会走到这里、翻失败说明上游本来就不通，
	// 这里再补一次只是把访客的等待时间翻倍（现网踩过：截断那次多打的一枪就是这种重叠）。
	// 本条只管"主体是对的、句子里嵌着几个中文词"那种混排——那才是整段占比判据抓不到的形态。
	if replyLangMismatch(answerLang, rep.Content) {
		return rep
	}
	leaks := replyHanResidueRuns(answerLang, rep.Content)
	if len(leaks) == 0 {
		return rep
	}
	client := e.ensureLLM(ctx)
	fixed, ok := e.repairHanResidueBase(ctx, client, answerLang, "", rep.Content, leaks,
		replyLocalizeMaxTokens, func(out string) []string { return replyHanResidueRuns(answerLang, out) })
	if !ok {
		observability.Warn(ctx, "assist.engine 对话正文汉字残留补翻未采用，保留原稿",
			"lang", canonicalLang(answerLang), "source", rep.Source,
			"count", len(leaks), "leaks", strings.Join(leaks, ","))
		return rep
	}
	observability.Info(ctx, "assist.engine 对话正文汉字残留已出站补翻",
		"lang", canonicalLang(answerLang), "source", rep.Source,
		"before", len(leaks), "after", len(replyHanResidueRuns(answerLang, fixed)), "leaks", strings.Join(leaks, ","))
	rep.Content = fixed
	return rep
}

// repairHanResidueBase 两条补翻路共用的底座（canned 与对话正文只差"有没有中文源文"和"残片怎么数"）。
//
// 为什么要抽这一层而不是复制一份提示词：同一族缺陷在两处各修一次，
// 迟早长成「canned 修好了、对话正文照漏」的形态（本仓 074x/082x 两批都是这么收尾的，
// 见 han_residue.go 文件头）。提示词、三条硬判据、温度与截断口径都必须只有一份。
func (e *Engine) repairHanResidueBase(ctx context.Context, client *llm.Client, uiLang, srcBlock, draft string,
	leaks []string, maxTokens int, afterLeaks func(string) []string) (string, bool) {
	label := langLabel(uiLang)
	if label == "" || client == nil || !client.Enabled() || len(leaks) == 0 {
		return "", false
	}
	prompt := "上一版" + label + "译文里有这些片段没写成" + label + "（照抄了中文词形／简体字形，" +
		"或一个词被劈成假名＋英文）：" + strings.Join(leaks, "、") +
		"。请把下面这段译文**整段重写一遍**：上面点名的**每一处**都必须换成 " + label +
		" 里的自然写法，一处都没改掉就等于没修；除此之外其余措辞照抄上一版，" +
		"不许增删句子、不许加任何括号备注，行数与上一版完全一致，一行对应一行，" +
		"不要加解释、不要加引号、不要输出思考过程，也不许改任何数字。\n" +
		translateContract(uiLang) +
		srcBlock + "\n【上一版" + label + "译文】\n" + draft + "\n---"
	out, _, usage, err := client.Chat(ctx, localizeTemperature, maxTokens,
		[]llm.Message{{Role: "user", Content: prompt}})
	if err != nil || usage.Truncated {
		return "", false
	}
	out = cleanTranslated(out)
	if out == "" || lineCountOf(out) != lineCountOf(draft) {
		return "", false
	}
	if len(afterLeaks(out)) >= len(leaks) {
		return "", false // 没改善就收手，别拿一份同样带残片的新稿去覆盖
	}
	return out, true
}
