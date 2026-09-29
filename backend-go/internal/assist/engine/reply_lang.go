// ============ reply_lang.go · 职责说明 ============
// 挂件「用什么语言回答」这一件事的单一事实源。
//
// ★ 082x（2026-09-29，用户指令「不能根据用户的前台语言和使用语言来回复，一律用中文」）：
//
// 触发点是现网截图那条：英文站访客用英文问「what kind of feature do you have」，
// 挂件回了一整段中文。根因不是一句「忘了翻译」，而是**整条链路上没有任何一处知道访客是谁**：
//  1. 挂件请求体只有 {session, tok, message, page}（frontend-react/src/api/assist.ts assistChat），
//     界面语言从来没送进来；
//  2. 系统提示词四段（persona / tone_rules / promise_rules / 相关知识）全中文，
//     末尾还写着「直接回复用户：」——模型照素材的语言作答，是它当时的最优解；
//  3. 更硬的一层：Respond 的第 2、3 道（话术直配、流程）和第 4 道的兜底
//     （fallbackReply 把中文知识原文拼接）**根本不经过模型**，直接返回中文写好的文案。
//     只补提示词的话，这三条路照样把中文甩给英文访客。
//
// 所以本文件同时管两件事，缺一不可：
//   - replyLangBlock：给 LLM 那一路的语言口径（界面语言打底、访客输入语言优先、品牌名分语言）。
//   - visitorWantsChinese：给规则那几路的闸门——非中文界面语言时不许中文话术/流程/知识原文直出，
//     让位给模型；模型不可用时才回落中文素材（并在日志里露一次，别让它变成暗病）。
//
// 品牌名口径为什么也住在这里：configs.persona 里写死了「你是『能言』…」，
// 而运营改人设是整段替换（同 promise.go 文件头那条理由），事实性口径不能寄生的士。
// 品牌写法分两档（见 brandKanjiLocales：简繁中文＋日文用汉字名「能言」，其余一律 LangCross），
// 既管模型自称，也管挂件标题——前端 i18n 的 chat.assistTitle 此前把品牌名音译成 Nengyan，
// 已由 082x 一并订正，并由 frontend-react/src/i18n/brandName.test.ts 保证不再复活。
//
// ★ 同批还收了一个同级漏口：计费单位「积分」的跨语种写法（见 pointsTermByLang）。
// 现网补翻把它译成 "integral"，与官网各界面的 credits／ポイント／кредитов 全对不上——
// 品牌名和**钱的名字**是同一类东西：客户拿它跟账单核对，一处一个叫法就是对外错报。
// =============================================
package engine

import "strings"

// langLabels 界面语言代码 → 给模型看的语言名（英文名 + 该语言自称）。
// 键与前端 src/i18n/index.ts 的 Lang 联合类型一一对应（12 语种口径，★ 2026-09-20 全站十语种升级）。
// 刻意用「英文名（自称）」双写：只给 code 时小模型会把 "th" 认成别的语种，
// 只给自称又会在中文提示词里出现一堆模型未必稳读的字符。
var langLabels = map[string]string{
	"zh":      "Simplified Chinese（简体中文）",
	"zh_hant": "Traditional Chinese（繁體中文）",
	"en":      "English（English）",
	"ru":      "Russian（Русский）",
	"fr":      "French（Français）",
	"ar":      "Arabic（العربية）",
	"es":      "Spanish（Español）",
	"pt":      "Portuguese（Português）",
	"de":      "German（Deutsch）",
	"ja":      "Japanese（日本語）",
	"ko":      "Korean（한국어）",
	"th":      "Thai（ไทย）",
}

// langLabel 界面语言代码转可读名；未知/空语言回 ""（调用方据此走「按访客输入判断」分支）。
func langLabel(code string) string {
	return langLabels[canonicalLang(code)]
}

// canonicalLang 把界面语言码归一成「小写＋下划线」形态。
// 为什么要专门归一一次而不是直接用送进来的值：缓存键用它拼（i18n:welcome:<lang>），
// 'EN' 与 'en'、'zh-Hant' 与 'zh_hant' 必须是同一个键——否则同一个语种在库里留两份译文，
// 运营手工改过的那份（!manual）可能被另一条键绕过，变成「改了没生效」。
// API 层的 normalizeUILang 已经做过同样的归一，这里不依赖它：engine 包不该反向认识 api 包。
func canonicalLang(code string) string {
	c := strings.ToLower(strings.TrimSpace(code))
	return strings.ReplaceAll(c, "-", "_")
}

// visitorWantsChinese 本轮是否可以把中文话术/流程/知识原文直接回给访客。
//
// 判据只有一条：**界面语言是中文系**（zh / zh_hant）或未提供界面语言。
// 为什么不用「访客这句话是不是中文」判：那是模型的活（它能看出中文界面里有人用英文提问，
// 并按下面 replyLangBlock 的第 2 条改口），而服务端要判的是「这段中文 canned 文案能不能原样送出去」——
// 后者只要界面语言不是中文系就不能送，英文界面里的中文提问，答案是给**其他看得到这段对话的人**的，
// 反过来（中文界面 + 英文提问）交给模型处理更合适。
//
// 空 code 一律算中文：老缓存包、脚本客户端、以及 082x 之前的挂件都不带 lang，
// 把它们判成「非中文」会让全站话术直配在升级瞬间集体失效（那是行为回退，不是修复）。
func visitorWantsChinese(uiLang string) bool {
	c := canonicalLang(uiLang)
	return c == "" || c == "zh" || c == "zh_hant"
}

// brandKanjiLocales 用汉字写品牌名的语种（zh / zh_hant / ja）。
//
// 判据来自前端既有约定，不是我另起炉灶：src/i18n/locales/ 里 ja.ts 的 "app.title" 本就是「能言」，
// 而 ko/th/ru/… 全部是 LangCross（★ 082x 逐个核过）。挂件标题、登录页、页面 title 都按这份表渲染，
// 所以模型自称的品牌名必须跟着同一张表走——否则日文访客会看到「标题写能言、正文写 LangCross」，
// 那是第二个品牌 bug，跟用户报的「Nengyan」同级。
//
// ★ 改动这张表必须同步改前端 locales（闸门＝frontend-react/src/i18n/brandName.test.ts，
// 它直接读那 12 个 locale 文件与这里的口径交叉锁）。
var brandKanjiLocales = map[string]bool{"zh": true, "zh_hant": true, "ja": true}

// brandNameFor 品牌名在该语种下的写法。空语种按 LangCross 给（它是国际通用写法，
// 而「能言」只有在对方读得懂汉字时才成立）。
func brandNameFor(uiLang string) string {
	if brandKanjiLocales[canonicalLang(uiLang)] {
		return "能言"
	}
	return "LangCross"
}

// brandNamingLine 品牌名口径那句（进【回复语言】段，也被 localize.go 的翻译提示词复用）。
// 单一事实源：拼音禁令与语种分档只在这里写一遍，两处各写一份迟早会分叉（同官网报价那条教训）。
func brandNamingLine(uiLang string) string {
	if brandNameFor(uiLang) == "能言" {
		return "- 品牌名：本轮用中文写法「能言」（该语种界面与挂件标题也是这个写法）；" +
			"任何情况下都不许写成 Nengyan、NengYan 之类拼音。\n"
	}
	return "- 品牌名：本轮一律写 LangCross；中文原名是「能言」，" +
		"但除简繁中文与日文外不许用汉字写法，更不许写成 Nengyan、NengYan 之类拼音，也不许两个名字混着叫。\n"
}

// pointsTermByLang 计费单位「积分」在各语种**官网界面里既有的写法**。
//
// ★ 082x 增补（2026-09-29 换件后现网复问读数）：英文访客问价格，补翻把答案里的「积分」写成了
// "integral"（7.5 integral fee / 400 integral per 1,000 characters）。那是数学课本译法，
// 官网任何语种都没有这个词——客户在界面上一边看到「credits」一边听到助手说「integral」，
// 对不上账的一句报价就成了对外错报（同「官网报价三口径打架」F-12 那族的形态，只是这次发生在语言之间）。
//
// 事实源不是我挑的词，而是前端词典本身：`frontend-react/src/i18n/locales/<lang>.ts` 的
// `app.pkgLineFmt` 里 `{points}` 后面那个词（en credits / ja ポイント / ru кредитов / ar نقطة / …），
// 交叉锁＝`frontend-react/src/i18n/pointsTerm.test.ts`（它直接读这份 Go 表逐语种核对）。
// ⚠️ 改动任何一侧都会红，这是设计：术语只在一处定义，两处必须同源。
var pointsTermByLang = map[string]string{
	"en":      "credits",
	"zh_hant": "積分",
	"ru":      "кредитов",
	"fr":      "points",
	"ar":      "نقطة",
	"es":      "créditos",
	"pt":      "pontos",
	"de":      "Punkte",
	"ja":      "ポイント",
	"ko":      "포인트",
	"th":      "คะแนน",
}

// pointsTermLine 计费单位口径那句（进【回复语言】段，也被 localize.go 的翻译提示词复用）。
// 中文系（zh）不追加——素材本来就是中文；语种未知时也返回空串，让调用方按「没有界面语言」分支走。
func pointsTermLine(uiLang string) string {
	c := canonicalLang(uiLang)
	if c == "" || c == "zh" {
		return ""
	}
	term := pointsTermByLang[c]
	if term == "" {
		// 表里没有的语种宁可不提这条，也不要现场编一个词：编出来的词一定跟界面对不上。
		return ""
	}
	return "- 计费单位：中文素材里的「积分」在本轮语言一律写作「" + term +
		"」（官网该语种界面用的就是这个词），不许换第二种说法、不许直译成 integral 之类对不上账的译名，" +
		"也不许把中文「积分」两字原样留在对方的正文里。\n"
}

// pointsTranslationLine 同一条术语口径的**翻译路**形态（prose 而不是 bullet，两处句式本来不同）。
// 单一事实源仍是 pointsTermByLang 那张表——这里只换措辞，绝不另抄一份词表（同官网报价那条教训）。
func pointsTranslationLine(uiLang string) string {
	c := canonicalLang(uiLang)
	if c == "" || c == "zh" {
		return ""
	}
	term := pointsTermByLang[c]
	if term == "" {
		return ""
	}
	return "计费单位口径：原文里的「积分」在译文中一律写作「" + term +
		"」（官网该语种界面用的就是这个词），不许译成 integral 之类对不上账的说法，" +
		"也不许把中文「积分」两字原样留在译文里。\n"
}

// replyLangBlock 【回复语言】段：拼在系统提示词最末尾（离「直接回复用户」最近的一段，
// 优先级最高，且不会被上面任何一段中文素材带跑）。
//
// 三条口径按重要性排：
//  1. 界面语言打底 —— 送进来的 lang 决定默认作答语言；
//  2. 访客输入语言优先 —— 访客这一轮明显换了语言就跟着换（现网真实场景：中文界面里贴英文合同片段）；
//  3. 品牌名分语言 —— 见 brandNamingLine：这是 082x 用户指令第一条
//     （「品牌英文名叫 LangCross，不叫 nengyan」）在模型侧的落点。
func replyLangBlock(uiLang string) string {
	var sb strings.Builder
	sb.WriteString("【回复语言】（这一段管「用哪种语言说」，和【承诺边界】一样优先于说话方式）\n")
	if l := langLabel(uiLang); l != "" {
		sb.WriteString("- 访客界面语言：" + l + "。默认用这个语言写回复。\n")
	} else {
		sb.WriteString("- 没拿到访客的界面语言：按访客这一轮输入所用的语言作答。\n")
	}
	sb.WriteString("- 访客输入的语言和界面语言不一致时，**跟访客输入走**（他用哪种语言问，就用那种语言答），界面语言只作默认值。\n")
	sb.WriteString("- 【相关知识】【系统现值】里的素材是中文写的：面向非中文访客时先译成对方的语言再说，不许把中文原句直接贴出去（术语、品牌名、文件后缀这类专有写法除外）。\n")
	sb.WriteString(brandNamingLine(uiLang))
	sb.WriteString(pointsTermLine(uiLang))
	sb.WriteString("- 最后一行的功能入口标记【go:key】保持原样，key 不翻译、不改写。\n")
	return sb.String()
}
