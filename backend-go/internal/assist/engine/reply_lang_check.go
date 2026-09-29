// ============ reply_lang_check.go · 职责说明 ============
// 回复语言的**出站校验**：提示词里的【回复语言】段只是「请求」，本文件负责把它变成「保证」。
//
// ★ 082x 增补（2026-09-29 现网复问实测，同批改完换件后当场抓到的三条残留）：
//
// 换件后我用本机探针打现网助手连问四轮，其中一轮 lang=en 问价格，
// 模型回的是 86 个汉字的整段中文（source=llm）；**同一会话的下一轮英文提问又正常回了英文**。
// 也就是说 replyLangBlock 那段指令的命中率不是 0 也不是 1——它是一次概率性请求：
// 上面五段素材（persona / tone_rules / promise_rules / 系统现值 / 相关知识）全是中文，
// 思维链模型（现网 GLM-Z1）在长中文上下文里会被语域带跑，问得越"像查资料"越容易滑回中文。
//
// 三条可选修法：
//  1. 把【回复语言】写得更凶（再加两句"必须"）——还是请求，只是概率高一点，改天照样漏；
//  2. 检测语种不合格后**重问一次**——多付一整轮生成（现网 max_tokens=900，秒级），
//     而且第二次同样可能被带跑，且对话历史会留下"问了两次"的上下文污染；
//  3. 检测语种不合格后**把已生成的答案翻成访客语言**（复用 localize.go 那条已实现并测过的路）——
//     一次短翻译调用、不重生成、不污染历史、失败可 fail-soft 出原文。
//
// 选了 3。判据必须窄到"不会把合格答案拖去重翻"：见 replyLangMismatch 的分支口径与单测里的正负对照。
//
// 顺带收口另外两条同批现网取证（都属"模型输出没做末道卫生"）：
//   - 内部规则回声：zh 轮把提示词里的口径原样吐进气泡
//     「（不报具体价格数字，但用户问字单价时必须强调系统现值里没写的…）」，
//     同批还看到空括号「（）」。tone_rules 第 8 条早就写了「正文里不要加括号备注」，
//     和语言段一样——那是请求，不是保证，所以出站再剥一次（判据只认**内部段名**，
//     宁可漏剥也不误伤「（具体以注册页公示为准）」这类正常补充说明）。
//   - 残破字符与空行：日文轮出现 U+FFFD 替换符（思维链截断的常见残渣）、zh 轮尾部三个空行。
//
// 两者都在 sanitizeVisitorText 里收，挂在 postProcess 这一道总口上（历史回放与正文同一出口）。
// =============================================
package engine

import (
	"context"
	"strings"
	"unicode/utf8"

	"translator/internal/observability"
)

// replyLangMismatch 判断「这轮回复的语言和访客界面语言不符」。
//
// 判据按语种分档，**只写能一眼定性的那半**：
//   - zh / zh_hant / 空语种：恒判合格。前两者本来就是中文系；空语种说明没拿到界面语言，
//     此时作答语言由访客输入决定，而"输入是哪种语言"是模型的活，服务端没有可靠判据，
//     硬判只会把合格答案拖去重翻。
//   - ja：只有「通篇汉字、一个假名都没有」才判不合格。日文正文里出现汉字完全合法
//     （「翻訳」「レイアウト」之外的汉字词比比皆是），唯一不会冤枉人的形态是零假名——
//     现网那条就是纯中文回给日文访客。
//   - 其余语种（en/ru/fr/de/es/pt/ar/ko/th）：这些文字的正文里**不该出现汉字**。
//     出现即看**占比**：汉字数 ≥ 8 且占非空白字符 ≥ 15% 才算不合格。
//     为什么不加"汉字数超 25 就判不合格"这类绝对量档：英文回答里整段引用中文合同原文、
//     或贴一段中文引文都是**合格**回答（现网就有访客贴中文片段问英文），
//     只看绝对量会把这类回答拖去重翻一次——白花一次调用，还可能把引文翻成英文丢掉可比性。
//     占比才是"主体语言"的可靠证据。
func replyLangMismatch(uiLang, text string) bool {
	c := canonicalLang(uiLang)
	if c == "" || c == "zh" || c == "zh_hant" || strings.TrimSpace(text) == "" {
		return false
	}
	han, kana, total := countScripts(text)
	switch c {
	case "ja":
		return han >= 20 && kana == 0
	default:
		if han < 8 || total <= 0 {
			return false
		}
		return han*100/total >= 15
	}
}

// countScripts 数三类关键书写形态：汉字（含扩展 A 区）、日文假名（平/片假名）、非空白总字符数。
// 刻意不数拉丁字母：判"该用英文却回了中文"只需要汉字这一侧的证据，
// 数拉丁反而会让"中英混排但主体是英文"的正常回答被误判。
func countScripts(s string) (han, kana, total int) {
	for _, r := range s {
		if r == ' ' || r == '\n' || r == '\t' || r == '\r' {
			continue
		}
		total++
		switch {
		case r >= 0x4E00 && r <= 0x9FFF, r >= 0x3400 && r <= 0x4DBF:
			han++
		case r >= 0x3040 && r <= 0x30FF:
			kana++
		}
	}
	return
}

// replyLocalizeMaxTokens 补翻一条对话正文的额度。取 1200 ＝ 现网对话额度（max_tokens 库里现值 900）
// 再放宽一档：翻译输出的信息量与原回答相当，而思维链模型那份额度是「链子 + 正文」共用的
// （074x 实测 400 额度下光链子就吃 276）。拿 localizeMaxTokens=400 去翻整条回答必然翻出半句，
// 而**半句译文比整段中文更糟**——那是对外错报，中文原文至少还是答对的内容。
// 截断在 translateOnce 里一律判失败（finish_reason=length 不进正文），这里只是把额度给够。
const replyLocalizeMaxTokens = 1200

// enforceReplyLang LLM 回复出站后的语言保证：语种不符就翻一次，翻不动就留着原文并露一次日志。
//
// 为什么 fail-soft 出中文而不是报错：访客问的东西已经答对了，只是语言不对；
// 把整条回答打成「助手暂时联系不上」比看到中文更糟（同 localize.go 文件头那条口径）。
// 但这条软路径必须留 WARN——现网第一次漏翻就是靠用户截图才发现的，不能有第二次。
func (e *Engine) enforceReplyLang(ctx context.Context, rep *Reply, uiLang string) *Reply {
	if rep == nil || !replyLangMismatch(uiLang, rep.Content) {
		return rep
	}
	han, _, total := countScripts(rep.Content)
	tr, err := e.LocalizeReply(ctx, rep.Content, uiLang)
	if err == nil && strings.TrimSpace(tr) != "" {
		// INFO 而不是 DEBUG：现网语言段的真实命中率只有靠这条计数才看得见——
		// 补翻占比降不下来，说明提示词那一路还得再修，不能让"界面看起来正常了"把问题埋掉。
		observability.Info(ctx, "assist.engine 回复语种与访客界面语言不符，已出站补翻",
			"lang", uiLang, "source", rep.Source, "han", han, "chars", total)
		rep.Content = tr
		rep.LangLocalized = true
		return rep
	}
	observability.Warn(ctx, "assist.engine 回复语种与访客界面语言不符，且补翻失败，按原文出",
		"lang", uiLang, "source", rep.Source, "han", han, "chars", total, "err", err)
	return rep
}

// internalEchoMarkers 内部段名清单：括号备注里命中任意一条，就判定为「提示词回声」而非给用户看的内容。
//
// 判据只认这些**不会出现在对外文案里的名字**（【系统现值】【承诺边界】【回复语言】【相关知识】
// 是提示词的段名，「入口标记」「限定词」「不许」是写给模型的规则用语）。
// ⚠️ 故意不收「必须」「不要」这类词：面向用户的操作指引里完全可能说「必须小于 40MB」，
// 收了就会把正常答案里的一段括号整段吃掉——误伤比留一句回声严重得多。
var internalEchoMarkers = []string{
	"系统现值", "承诺边界", "回复语言", "相关知识", "入口标记", "限定词", "不许", "语气规则",
}

// selfNarrationMarkers 模型**旁白**的形态清单（★ 082x 第八条，2026-09-29 现网复问抓到）。
//
// 现场：lang=en 问「能不能保留 PDF 原版式并自动重算公式」，回答主体是合格英文，
// 但句子里嵌着两段中文旁白——「（先接住，用户可能期待否定或肯定，这里肯定但自带前提）」
// 与「（同样引用边界）」。这就是模型把给自己看的说话策略直接写进了正文（思维链残渣的另一种形态：
// 不是被截断，而是**主动把旁白当内容**）。英文轮里它同时是一条语种缺陷（50 个汉字）。
//
// 为什么不并进 internalEchoMarkers 而是另开一张表：那一条清的是**提示词段名**（【系统现值】这类，
// 对外永远不出现，误伤面为零）；这一条清的是**说话口吻**——"用户/客户/对方"是第三人称提到访客，
// 而挂件对访客永远说「你」。两种判据的误伤半径完全不同，混在一张表里以后想收窄其中一类都拆不开。
//
// 现场二（★ 082x 第十条，2026-09-29 换件后当天又被用户带截图抓到一条）：中文轮问比价，
// 气泡尾部漏出「（注：根据规则，此处需在最后单独输出标记，且 key 必须来自指定列表。
// 用户输入"deep"属于延续比较场景，故推荐对比页面入口。）」——
// 这一整段是模型在**叙述自己对提示词的执行情况**（"输出标记""指定列表"，并把访客那句叫"用户输入"），
// 而上一版清单里一个词都没命中 ⇒ containsAny 判假、原样送出。
// 这一批据此补了八条实测形态（见清单末尾那行）。
//
// 漏剥的后果只是气泡里多一句难看的中文旁白，误剥的后果是把客户要看的补充说明吃掉——
// 所以这张表宁可窄，不收单字词；高风险词一律不收：「期待」（「（期待您的文档）」是正常客服话术）、
// 「口径」「边界」（对外真会说「计费口径」「能力边界」）、「素材」（上传的原文对客户而言就叫素材）、
// 「此处需」（操作指引里真会说「（此处需填写你的域名）」→ 只收完整形态「此处需在最后」）。
//
// ★ 为什么这一批**没有**加「成对判据」那条看起来更能泛化的腿（规则×输出、标记×列表 两词同现即剥）：
// 逐条想过、逐条被对外文案否掉了——「（输出为 PDF，按套餐规则计费）」「（需要登录后台看标签列表）」
// 这两类客户真会看到的补充说明都能凑齐那些组合，误剥的代价（吃掉客户要看的说明）大于漏剥。
// 所以清单追不上模型措辞时，走下面 `chineseAsideRuns` 那条**只记日志不改正文**的观测腿攒证据，
// 下一批再把确认过的形态写成词条。
var selfNarrationMarkers = []string{
	"接住", "提示词", "系统提示", "人设", "复述", "反问", "自检",
	"用户可能", "用户问", "用户想", "客户问", "客户想", "对方问",
	"引用边界", "自带前提", "期待否定", "期待肯定", "这里肯定", "这里否定",
	// ↓ 082x 第十条：现网「（注：根据规则…此处需在最后单独输出标记，且 key 必须来自指定列表…用户输入…）」实测漏出
	"根据规则", "此处需在最后", "单独输出", "输出标记", "指定列表", "用户输入", "本轮输入", "思考过程",
}

// visitorDropMarkers 出站正文「整段括号删掉」的总清单＝提示词段名＋模型旁白形态。
// 合成一处而不是在 sanitizeVisitorText 里连扫两遍：同一串字过两遍没有额外收益，
// 而清单分家反而会长出「以后加一条要记得加两处」这种漏。
// 两张源表各自保留，是为了让「这段为什么被删」能按形态查回上面两段判据说明。
var visitorDropMarkers = append(append([]string{}, internalEchoMarkers...), selfNarrationMarkers...)

// sanitizeVisitorText 出站正文的末道卫生：剥内部规则回声、剥模型旁白、剥空括号、去 U+FFFD、收多余空行。
// 在 postProcess 里、摘完【go:…】控制序列之后调用。
func sanitizeVisitorText(text string) string {
	s := strings.ReplaceAll(text, "\uFFFD", "")
	s = dropParentheticals(s, visitorDropMarkers)
	s = strings.NewReplacer("（）", "", "()", "").Replace(s)
	// 连续 3 个及以上换行压成 2 个（模型爱在结尾甩一串空行，气泡里就是一段空白）
	for strings.Contains(s, "\n\n\n") {
		s = strings.ReplaceAll(s, "\n\n\n", "\n\n")
	}
	return strings.TrimSpace(s)
}

// dropParentheticals 删除**内容命中任一标记**的括号段（全角/半角都管），其余正文一字不动。
// 未闭合的括号不动（那多半是 max_tokens 截断，截断有自己的 WARN 与治理口径，
// 这里悄悄补全反而把现场抹掉了）。
//
// 线性扫描而不是递归：每一轮至少吃掉一整个括号段，构造不出死循环；
// 递归版要在"合格括号"分支上再切一次串，边界（字节位 vs rune 位）极易写错——
// 全角括号一个字符占 3 字节，按字节算长度就会切坏 UTF-8。
//
// ⚠️ 位置一律用**字节下标**（strings.IndexFunc 返回的就是字节位），括号宽度用
// utf8.DecodeRuneInString 取该 rune 的字节数。曾经这里手写过一个 runeWidth(s string) int
// 用 len(string(s[i])) 算首字符宽度，而 s[i] 是**字节**：全角括号的三个字节会被算成 2/1/1，
// 切出来的子串是非法 UTF-8，好译文被切成乱码——这正是本文件要修的第三条症状。
func dropParentheticals(s string, markers []string) string {
	var sb strings.Builder
	rest := s
	for {
		open := strings.IndexFunc(rest, isParenOpen)
		if open < 0 {
			sb.WriteString(rest)
			break
		}
		innerStart := open + firstRuneBytes(rest[open:])
		rel := strings.IndexFunc(rest[innerStart:], isParenClose)
		if rel < 0 {
			sb.WriteString(rest) // 未闭合：原样留着（截断有自己的 WARN 与治理口径）
			break
		}
		end := innerStart + rel + firstRuneBytes(rest[innerStart+rel:])
		inner := rest[innerStart : innerStart+rel]
		if containsAny(inner, markers) {
			// 命中内部段名：整段连括号删掉，顺手收掉括号前悬着的连接符（「……，（系统现值…）」→「……」）
			sb.WriteString(strings.TrimRight(rest[:open], " ，,、；;"))
			rest = rest[end:]
			continue
		}
		sb.WriteString(rest[:end])
		rest = rest[end:]
	}
	return sb.String()
}

// isParenOpen / isParenClose 括号判定（全角与半角都算：中文提示词里两种模型都会用）。
func isParenOpen(r rune) bool { return r == '（' || r == '(' }

// isParenClose 见 isParenOpen。
func isParenClose(r rune) bool { return r == '）' || r == ')' }

// firstRuneBytes 返回 s 首个字符占用的字节数（s 为空回 0）。
// 按字节切 UTF-8 串时必须切在字符边界上，这里就是那个边界尺子。
func firstRuneBytes(s string) int {
	if s == "" {
		return 0
	}
	_, n := utf8.DecodeRuneInString(s)
	return n
}

// containsAny 括号内文本是否命中任一内部标记。
func containsAny(s string, markers []string) bool {
	for _, mk := range markers {
		if mk != "" && strings.Contains(s, mk) {
			return true
		}
	}
	return false
}

// asideHanMin 观测腿认定「一句中文旁白」的最小汉字量（与 detectInputLang 那条汉字档同源取 4：
// 三个以下多半是引用的术语或文件名（「（PDF）」这种），报出来只会淹掉日志）。
const asideHanMin = 4

// asideMetaNotePrefixes 观测腿认的「元说明起手」：模型写给自己看的备注几乎都以这几个字起头。
// 只用于挑日志候选，不参与删正文，所以宁可多报（「（注：输出为 PDF）」这类会被报出来但留着）。
var asideMetaNotePrefixes = []string{"注：", "注:", "备注：", "备注:", "说明：", "说明:"}

// asideMechanicsNouns 观测腿的第二条腿用的名词（中文轮里"旁白"没有语种反差可看，只能看用词）。
// ⚠️ 这些词**单个都不许进删除清单**（「（按套餐规则计费）」是正常说明），
// 这里只是"起手是元说明 + 句中有这类名词"两个弱信号叠起来当日志筛子。
var asideMechanicsNouns = []string{"标记", "提示词", "规则", "系统", "列表", "模型", "输出", "要求"}

// unstrippedAsides 清单没命中、但形态像旁白的括号段（**只用于记日志，不改正文**）。
//
// 为什么需要这条腿：`selfNarrationMarkers` 是按实测形态列的词表，它天然追不上模型的措辞
// ——本批修的就是"第八条的词表漏掉第十条的形态"。词表之外再写一条能泛化的**硬**判据，
// 上面已经算过一遍：能同时避开「（输出为 PDF，按套餐规则计费）」这种正常说明的成对判据不存在。
// 所以这里只**报告**，把候选形态原样打进日志，下一批照读数补词条（补进词表才是改正文的动作，
// 那一步人工看过再做）。日志候选允许过量，误报只花人一眼，误删花的是客户的内容。
//
// 两条筛选口径按语种分开：
//   - 非中文系且非日文：正文里出现一整段汉字备注本身就是反差，≥4 个汉字即报
//     （3 个以下多半是引用的术语或文件名，「（PDF）」这种，报出来只会淹掉日志）；
//   - 中文系／日文：没有语种反差可用，改看形态——起手是元说明（注：／备注：／说明：）
//     且句中含内部名词，才报。
func unstrippedAsides(answerLang, text string) []string {
	if text == "" {
		return nil
	}
	kanjiShape := true
	switch c := canonicalLang(answerLang); c {
	case "zh", "zh_hant", "ja":
		kanjiShape = false
	case "":
		kanjiShape = false // 没定出语种＝按中文界面处理，别把中文正常文案全报一遍
	}
	var out []string
	rest := text
	for {
		open := strings.IndexFunc(rest, isParenOpen)
		if open < 0 {
			break
		}
		innerStart := open + firstRuneBytes(rest[open:])
		rel := strings.IndexFunc(rest[innerStart:], isParenClose)
		if rel < 0 {
			break // 未闭合：那是截断，有自己的 WARN 与治理口径
		}
		inner := strings.TrimSpace(rest[innerStart : innerStart+rel])
		if !containsAny(inner, visitorDropMarkers) &&
			((kanjiShape && hanCountIn(inner) >= asideHanMin) || isMetaNoteAside(inner)) {
			out = append(out, inner)
		}
		rest = rest[innerStart+rel:]
	}
	return out
}

// isMetaNoteAside 起手元说明＋句中有内部名词（中文轮的旁白形态，见 unstrippedAsides 第二条口径）。
func isMetaNoteAside(inner string) bool {
	hit := false
	for _, p := range asideMetaNotePrefixes {
		if strings.HasPrefix(inner, p) {
			hit = true
			break
		}
	}
	if !hit {
		return false
	}
	return containsAny(inner, asideMechanicsNouns)
}

// hanCountIn 数串里的汉字个数（U+4E00–U+9FFF 与扩展 A 区，与 countScripts 同档）。
func hanCountIn(s string) int {
	n := 0
	for _, r := range s {
		if (r >= 0x4E00 && r <= 0x9FFF) || (r >= 0x3400 && r <= 0x4DBF) {
			n++
		}
	}
	return n
}

// observeVisitorAsides 出站前的旁白观测（见 unstrippedAsides：只记 WARN，正文一个字都不改）。
// 挂在 Respond 这个唯一出站咽喉上，四道回复与兜底都过这里，所以不会长成"只有 LLM 那一路有观测"。
//
// 为什么值得为一条日志占一个咽喉调用：这一族缺陷（提示词里的规矩只是请求）已经连续四轮靠用户截图发现，
// 每轮都是一次换件。WARN 里带原文＝下一批补词条有依据，不用等第五次截图。
func (e *Engine) observeVisitorAsides(ctx context.Context, answerLang string, rep *Reply) {
	if rep == nil {
		return
	}
	asides := unstrippedAsides(answerLang, rep.Content)
	if len(asides) == 0 {
		return
	}
	observability.Warn(ctx, "assist.engine 出站正文带出疑似旁白的括号备注，词表未命中（不改正文，只攒词条证据）",
		"lang", canonicalLang(answerLang), "source", rep.Source, "count", len(asides),
		"asides", strings.Join(asides, " | "))
}
