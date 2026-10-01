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
//
// ★ 093x 增补（2026-09-30 真机挂件复问，日文轮正文尾部两条残渣）：
//
//	「…ご案内できます。[ pricing ページで詳細を確認]  （※日本語で回答するため、必要に応じて
//	「ポイント」を用い、入力语言が日本語であることを考慮して翻訳を実施。）」
//
// 这一族把本文件原有的三道闸**逐个绕过去**的形态各不相同，各自的修法与误伤对照都在下面：
//   - 旁白是**日文写的**：internalEchoMarkers／selfNarrationMarkers 收的全是中文指令用词，
//     而观测腿的语种反差档对日文轮整档豁免 ⇒ 既没剥也没记（本次靠用户截图才发现）。
//     故删除清单只补**现网逐字实证**的两条日文多字形态，观测腿另开第三条泛化筛子
//     （点名语种＋交代作答动作，只记日志不改正文，见 mentionsAnsweringInLanguage）；
//   - 那圈方括号是**模型自己写的 markdown 链接只剩一半**（`(url)` 根本没出）：
//     新增 unwrapBrokenLinkBrackets，只拆括号留文字，带目标的真链接与未闭合一律不碰。
//
// 本文件的卫生函数从这一批起**不只挂在 postProcess 上**：两条补翻腿的产物也过同一道
// （engine.go 的 rehardenReplyRewrite），因为"补翻会把括号形态再换一次"这条早就写下的事实，
// 此前在出站那一刻没有任何一条腿接住它。
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
	// ↓ ★ 093x（2026-09-30 真机挂件复问，日文轮正文尾部整段旁白漏出）：
	//
	//	「（※日本語で回答するため、必要に応じて「ポイント」を用い、入力语言が日本語であることを
	//	  考慮して翻訳を実施。）」
	//
	// 这一族此前**两条腿全瞎**：internalEchoMarkers／selfNarrationMarkers 收的都是中文指令用词，
	// 而这段是**日文写的旁白**；unstrippedAsides 的观测腿又按"语种反差"筛，日文轮整档豁免 ⇒
	// 既没剥也没记，客户屏幕上直接看到助手在叙述自己的作答策略。
	// 入删除清单的只有这两条**现网逐字实证**的日文多字形态，不收单字词：
	// 「で回答するため」（＝"因为要用日语回答"，对客户永远不构成一句人话的补充说明）、
	// 「翻訳を実施」（＝"实施了翻译"，正文明显是在交代自己的动作而不是交代产品）。
	// ⚠️ 刻意不收「翻訳」单词：「（翻訳は人間がチェックします）」是正常对外说明，收了就是吃掉内容；
	// 也不收「するため」：「（ご確認するため）」这类目的状语在正常客服话术里高频。
	"で回答するため", "翻訳を実施",
}

// visitorDropMarkers 出站正文「整段括号删掉」的总清单＝提示词段名＋模型旁白形态。
// 合成一处而不是在 sanitizeVisitorText 里连扫两遍：同一串字过两遍没有额外收益，
// 而清单分家反而会长出「以后加一条要记得加两处」这种漏。
// 两张源表各自保留，是为了让「这段为什么被删」能按形态查回上面两段判据说明。
var visitorDropMarkers = append(append([]string{}, internalEchoMarkers...), selfNarrationMarkers...)

// ============ 开头「方法论引子」剥离（★ D-LLM-20261001-001，2026-10-01 用户带截图报）============
//
// 现场：中文轮问「你和deepl」，回复第一句是「先肯定对比合理性，再分角度补充新细节：」，
// 冒号后面才是真正的正文。这一句是模型把 tone_rules 第 9 条「先肯定他的考虑，再讲我们的差异」
// 这条**回答策略**当成正文复述出来了——思维链残渣的又一种形态。
//
// 为什么上面两张表都拦不住它：dropParentheticals 只处理**括号里**的内容，
// 而这一句是**独立成句、不带括号、还带个冒号当引子**的，整条清洗链对它完全无感，
// 连观测腿 unstrippedAsides（同样按括号段遍历）都一次都没报出来。
//
// 判据取向与全文件一致：**宁可窄，只砍模型多出来的策略引子，绝不砍正常正文**。
// 三条同时成立才剥（缺一即留），把误伤半径压到最小：
//
//	① 位置：只认**第一行**（去掉前导空白后到第一个换行为止），剥完必须还剩非空正文；
//	② 形态：该行以冒号结尾（：或:），且长度 ≤ instructionEchoLeadMaxRunes——
//	   正常回答的第一句通常更长、且不以「策略描述＋冒号」收口；
//	③ 用词：该行同时含「先」与至少一个**策略名词**（肯定／认可／角度／细节…），
//	   这些词是提示词自己的方法论用词，面向客户的正常话术不会这么开头。
//
// ⚠️ 为什么不收裸「先…再…」结构（不加策略名词腿）：客户真会看到的操作引导里
// 「先注册，再上传，最后下载：」这种分步引子是**合法正文**，只按结构判会把它吃掉。
// 加上策略名词这条 lexical 腿，才能把「交代自己怎么回答」和「交代用户怎么做」分开。
// ⚠️ 与 selfNarrationMarkers 同样的追不上模型措辞的风险：新形态先靠方案 A 的提示词定界挡，
// 挡不住再往 instructionEchoLeadWords 补词（补词即改正文，须配误伤对照单测）。
const instructionEchoLeadMaxRunes = 48

// instructionEchoLeadWords 见上③：策略引子的词汇指纹（全部来自提示词自身的方法论用词）。
var instructionEchoLeadWords = []string{
	"肯定", "认可", "认同", "理解", "接住", "合理性",
	"角度", "细节", "差异", "盘算", "复述", "然后讲", "再讲",
}

// stripInstructionEchoLead 剥掉开头那句「先…再…：」式的方法论引子（见上面整段说明）。
// 不命中就原样返回（含"整段只有引子、剥完会空"的病态形态——那种宁可不剥，留给观测）。
func stripInstructionEchoLead(text string) string {
	// 前导空白不算进"第一行"，但要能定位真正的第一行
	body := strings.TrimLeft(text, " \t\r\n")
	if body == "" {
		return text
	}
	nl := strings.IndexByte(body, '\n')
	firstLine := body
	if nl >= 0 {
		firstLine = body[:nl]
	} else {
		// 没有换行＝整段就一行，剥完必空，属病态形态，不动
		return text
	}
	rest := strings.TrimLeft(body[nl+1:], " \t\r\n")
	if rest == "" {
		return text // 引子后面没正文，不剥（避免把整条回复清空）
	}
	line := strings.TrimRight(firstLine, " \t")
	if !strings.HasSuffix(line, "：") && !strings.HasSuffix(line, ":") {
		return text
	}
	runeCount := utf8.RuneCountInString(line)
	if runeCount == 0 || runeCount > instructionEchoLeadMaxRunes {
		return text
	}
	if !strings.Contains(line, "先") || !containsAny(line, instructionEchoLeadWords) {
		return text
	}
	return rest
}

// sanitizeVisitorText 出站正文的末道卫生：剥开头方法论引子、剥内部规则回声、剥模型旁白、剥空括号、拆裸方括号链接文字、
// 去 U+FFFD、收多余空行。在 postProcess 里、摘完【go:…】控制序列之后调用；
// ★ 093x 起补翻产物也走这一道（engine.go 的 rehardenReplyRewrite），卫生只有一份。
func sanitizeVisitorText(text string) string {
	s := strings.ReplaceAll(text, "\uFFFD", "")
	// ★ D-LLM-20261001-001：先剥开头的「先…再…：」方法论引子（不带括号，dropParentheticals 管不着它）。
	// 放在最前面：引子剥掉后剩下的正文再走括号回声与旁白那几道，顺序不影响各自判据。
	s = stripInstructionEchoLead(s)
	s = dropParentheticals(s, visitorDropMarkers)
	s = strings.NewReplacer("（）", "", "()", "").Replace(s)
	s = unwrapBrokenLinkBrackets(s)
	// 连续 3 个及以上换行压成 2 个（模型爱在结尾甩一串空行，气泡里就是一段空白）
	for strings.Contains(s, "\n\n\n") {
		s = strings.ReplaceAll(s, "\n\n\n", "\n\n")
	}
	return strings.TrimSpace(s)
}

// unwrapBrokenLinkBrackets 把「[ 文字 ]」这种**没有目标地址**的半截 markdown 链接拆成纯文字
// （★ 093x，2026-09-30 真机挂件复问实证：日文正文尾部挂着「[ pricing ページで詳細を確認]」——
// 模型写了链接语法的方括号那一半，`(url)` 那一半根本没出，气泡里就留一圈方括号）。
//
// 为什么是"拆括号留文字"而不是"整段删掉"：括号里那句「pricing ページで詳細を確認」是**客户能读的话**，
// 删掉等于把一条入口提示吃掉；留着括号则是把模型的语法残渣摆给客户。拆成纯文字两个损失都没有。
//
// 三条不算残渣、原样送出的形态（都是误伤对照，单测里逐条钉住）：
//   - 方括号后面紧跟圆括号目标（真 markdown 链接 `[text](url)`）——那是完整语法，不是半截；
//   - 未闭合的 `[`——按本文件一贯口径当成截断处理，留着现场比悄悄补全好；
//   - 空括号或内容里还有左括号／超 80 字——不像链接文字，宁可不碰。
func unwrapBrokenLinkBrackets(s string) string {
	if !strings.ContainsRune(s, '[') {
		return s
	}
	var sb strings.Builder
	rest := s
	for {
		i := strings.IndexByte(rest, '[')
		if i < 0 {
			sb.WriteString(rest)
			break
		}
		rel := strings.IndexByte(rest[i+1:], ']')
		if rel < 0 {
			sb.WriteString(rest) // 未闭合：原样留着
			break
		}
		inner := rest[i+1 : i+1+rel]
		after := rest[i+1+rel+1:] // 右方括号**之后**的内容
		keep := inner == "" || strings.ContainsRune(inner, '[') || len(inner) > 80 ||
			strings.HasPrefix(strings.TrimLeft(after, " \t"), "(") // 带目标的 markdown 链接不动
		if keep {
			sb.WriteString(rest[:i+1+rel+1])
		} else {
			sb.WriteString(rest[:i])
			sb.WriteString(strings.TrimSpace(inner))
		}
		rest = after
	}
	return sb.String()
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

// asideLangNames／asideAnswerActWords ★ 093x 观测腿的第三条：**交代"本轮用什么语言作答/翻译"**的括号段。
//
// 为什么单独开这一条：日文轮那句「（※日本語で回答するため…翻訳を実施。）」把前两条腿全绕过了
// ——删除清单收的是中文指令用词，语种反差腿对日文轮整档豁免（日文正文本来就有汉字）。
// 而"旁白"这一族有个跨语种共性：**它在描述助手自己的作答动作，而不是客户的事**，
// 所以几乎每次都会点名一种语言＋带上"回答/翻译"这个动作。两个弱信号叠起来当筛子。
//
// ⚠️ 这一条**只用于记日志，不参与删正文**（同上面两条腿的口径）：
// 「（日本語・英語・中国語の12言語に対応）」「（翻訳は人間がチェックします）」都是客户会看到的正文，
// 单看"点名语言"或单看"提到翻译"都能凑齐误删条件，叠起来也只是**候选**而不是定性。
// 现网实证过的形态才进上面的删除清单（见 selfNarrationMarkers 末尾那两条日文形态）。
var asideLangNames = []string{
	"中文", "简体中文", "繁体中文", "英文", "英语", "日本语", "双语",
	"日本語", "英語", "中国語", "韓国語", "한국어", "汉语",
	"Chinese", "English", "Japanese", "Korean", "German", "French", "Spanish", "Russian", "Thai",
	"Русский", "Deutsch", "Français", "Español", "Português", "العربية", "ภาษาไทย",
}

// asideAnswerActWords 见 asideLangNames（"回答／翻译"这个动作本身，各语种写法）。
// 刻意不收「対応」「チェック」：那是产品能力用词，正常对外说明里高频出现。
var asideAnswerActWords = []string{
	"回答", "作答", "回复", "翻译", "译文", "応答", "返信",
	"answering", "we answer", "responding", "reply in", "translat",
}

// mentionsAnsweringInLanguage 括号段是否在交代"用什么语言作答/翻译"（观测腿第三条，见上面两条表）。
func mentionsAnsweringInLanguage(inner string) bool {
	return containsAny(inner, asideLangNames) && containsAny(inner, asideAnswerActWords)
}

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
//
// ★ 093x 再加**第三条腿，对所有语种生效**（含日文）：点名某种语言＋交代"作答/翻译"这个动作
// （mentionsAnsweringInLanguage）。前两条腿都按语种反差或中文用词办事，
// 而现网那条日文轮旁白「（※日本語で回答するため…）」两者都不沾 ⇒ 整档豁免，
// 连一条 WARN 都没留下，缺陷只能靠用户截图报——第三条腿就是为了让下一族新形态**先出现在日志里**。
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
			((kanjiShape && hanCountIn(inner) >= asideHanMin) || isMetaNoteAside(inner) ||
				mentionsAnsweringInLanguage(inner)) {
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
