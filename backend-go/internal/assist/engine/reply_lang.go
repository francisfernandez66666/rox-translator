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
//   - effectiveReplyLang：★ 082x 第九条（2026-09-29 用户实测「中文前台+英文问题，回复的还是中文」）
//     ——中文界面里英文提问的访客。上面两道都只认界面语言，对这类访客两道全不生效，
//     于是中文话术照送、出站补翻照关。本文件把「本轮作答语言」按**访客这句话的语种**接管一次，
//     下游三处（cannedOK / 【回复语言】段 / enforceReplyLang）都改吃接管后的值，判据与
//     不对称口径见下面 detectInputLang / effectiveReplyLang 的注释。
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

import (
	"strings"
	"unicode"
)

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
// 判据只有一条：**本轮作答语言是中文系**（zh / zh_hant）或未定出语种。
// ★ 082x 第九条起，注意这里的入参是 effectiveReplyLang 的产物（本轮作答语言），
// 不是挂件送上来的裸界面语言——「访客这句用的是哪种语言」由服务端**已经判过一次**，
// 本函数只管「这段中文 canned 文案这一轮能不能原样送出去」。
// 之前这里写着「不用访客输入判，那是模型的活」，现网实测证明那句不成立：
// 模型确实按输入改了口，但中文 canned 路根本不经过它，混排残渣也没人收拾（见下面第九条说明）。
//
// 空 code 一律算中文：老缓存包、脚本客户端、以及 082x 之前的挂件都不带 lang，
// 把它们判成「非中文」会让全站话术直配在升级瞬间集体失效（那是行为回退，不是修复）。
// 访客输入是英文的老客户端不在此列——那条会被 detectInputLang 判成 en 接管走。
func visitorWantsChinese(uiLang string) bool {
	c := canonicalLang(uiLang)
	return c == "" || c == "zh" || c == "zh_hant"
}

// ============================================================
// ★ 082x 第九条（2026-09-29，用户实测指令「中文前台+英文问题，回复的还是中文，应该回复英文」，
//   并当场把口径定死：「默认的打开词根据用户前台语言，但后续用户用什么语言，就回复什么语言」）
//
// 拆开看是两条规则，两条的**事实源不同**，不许合并成一条：
//   - 打开词（欢迎词 / chips）＝前台界面语言。访客还没开口，界面语言是他唯一的信号，
//     而且这一句要缓存、要让运营在管理台改（见 localize.go 的 i18n:welcome:<lang>）——按输入翻着走。
//   - 后续每一条回复＝**访客这一句话用的语言**，双向都跟输入走（英文界面里问中文就答中文）。
//
// 缺陷就出在第二条：下面的 visitorWantsChinese 与 reply_lang_check.go 的 replyLangMismatch
// 两道都只认**界面语言**，于是中文界面里的英文提问被整套机制当成本国人说话——
//   - cannedOK 恒真 ⇒ 中文话术直配、中文流程照样抢在模型前面直出；
//   - replyLangMismatch 对 zh 恒判合格 ⇒ 出站补翻整条路根本不启动。
//
// 现网实测（zh 界面 + 「what kinds of features do you have?」）落下来的正是混排形态：
// 模型确实按【回复语言】段的「跟访客输入走」改了口，但中文素材把它的语域往回拽，
// 气泡里一句英文后面跟着「翻译后版式基本还原（注意加粗位置可能错位…）」这种半截中文——
// 因为那句中文残渣本该由出站补翻收拾，而补翻根本没被叫醒。**提示词那一条是请求，不是保证**，
// 这条和 082x 文件头第 2 道「只补提示词等于没补」是同一条教训的两个方向。
//
// 修法：在 Respond 这个唯一咽喉上，先把访客这句话的语种判出来，用它当**本轮作答语言**，
// 下游三处（cannedOK、【回复语言】段、出站补翻）都改吃这个值。判不出就回落界面语言，
// 回落态与本条之前的行为逐字相同（所以粗判只会「少修」，不会「修坏」）。
// ============================================================

// inputLangMinLatinLetters 拉丁文接管的最小字母数（不含空格与标点）。
// 低于它说明对方只甩了个产品名或缩写（「PDF」「Word」），那不构成「换语种」的证据。
const inputLangMinLatinLetters = 4

// inputLangMinEnglishWords 拉丁文判成 en 所需的**互不相同**的英文常用词个数。
// 要 2 个而不是 1 个：单个词太容易撞车（法/德/西/意 与英语共用大量短词），
// 撞上一次就把一个法文访客的正文拖去「补翻成英文」——那是把对的译文改成错的。
const inputLangMinEnglishWords = 2

// inputLangMinChineseVoice 判「访客自己在写中文」所需的汉字数（拉丁字母在场时的门槛）。
// 取 4 ≈ 一句最短的中文指令（「帮我翻译」「这个怎么收费」）。
// 为什么拉丁在场时要把汉字数量当证据而不是「有汉字就算」：
// 中文访客贴一段英文合同来问「这段翻成中文多少钱」，英文部分是**材料**、中文部分才是**他自己的话**；
// 反过来「how much is 积分?」里那 2 个汉字只是他顺口引用我们界面上的词，这个人说的是英语。
const inputLangMinChineseVoice = 4

// englishFunctionWords 只收「日常几乎只有英语在用」的虚词与高频问句词。
// ⚠️ 刻意不收：产品名（word/excel/pdf——中文界面里访客随时会甩这些词问格式）、
// 以及和法/德/西同形的词（no/ok/via/unit/design/cheaper 之类不收）。
// 这张表**只用于判「是不是英语」**，不参与作答内容的生成，判不出就一律不接管（回 ""）。
var englishFunctionWords = map[string]bool{
	"the": true, "you": true, "your": true, "yours": true, "are": true, "is": true,
	"what": true, "how": true, "why": true, "when": true, "where": true, "which": true,
	"can": true, "could": true, "would": true, "should": true, "do": true, "does": true,
	"did": true, "have": true, "has": true, "had": true, "will": true, "for": true,
	"and": true, "but": true, "or": true, "with": true, "from": true, "this": true,
	"that": true, "there": true, "they": true, "them": true, "their": true, "we": true,
	"our": true, "she": true, "his": true, "her": true, "my": true,
	"me": true, "us": true, "please": true, "thanks": true, "thank": true, "hello": true,
	"price": true, "pricing": true, "cost": true, "costs": true, "much": true,
	"many": true, "support": true, "translate": true, "translation": true,
	"document": true, "file": true, "format": true, "layout": true, "refund": true,
	"trial": true, "plan": true, "package": true, "character": true, "characters": true,
}

// countEnglishWords 数文本里互不相同的英文常用词个数（按字母成词切分、统一小写）。
func countEnglishWords(s string) int {
	seen := map[string]bool{}
	var w strings.Builder
	flush := func() {
		if w.Len() == 0 {
			return
		}
		if englishFunctionWords[w.String()] {
			seen[w.String()] = true
		}
		w.Reset()
	}
	for _, r := range s {
		if unicode.IsLetter(r) {
			w.WriteRune(unicode.ToLower(r))
			continue
		}
		flush()
	}
	flush()
	return len(seen)
}

// detectInputLang 判「访客这句话用的是哪种语言」，判不出**一律回空串**（＝不接管，回落界面语言）。
//
// 分三档，口径完全不同：
//   - 假名／谚文／西里尔／阿拉伯／泰文：这几套文字各自只服务一种语言，中文正文里永远不会出现，
//     **命中一个字母即可定性**（现网要修的就是这一类确定性最强的形态）。
//   - 汉字在场且够四个：访客自己在写中文（哪怕句子里贴着一大段英文材料）→ 判 zh。
//   - 拉丁字母：一套字母供着英／法／德／西／葡／意六七个语种，本服务没有语言识别模型，
//     所以要求「字母量够 + 至少两个互不相同的英文常用词」才敢判成 en。
//     达不到就不接管——宁可让这一轮继续按界面语言走，也绝不把法文正文补翻成英文。
func detectInputLang(input string) string {
	s := strings.TrimSpace(input)
	if s == "" {
		return ""
	}
	var han, kana, hangul, cyril, arab, thai, latin int
	for _, r := range s {
		switch {
		case r >= 0x4E00 && r <= 0x9FFF, r >= 0x3400 && r <= 0x4DBF:
			han++
		case r >= 0x3040 && r <= 0x30FF:
			kana++
		case r >= 0xAC00 && r <= 0xD7A3, r >= 0x1100 && r <= 0x11FF:
			hangul++
		case r >= 0x0400 && r <= 0x04FF:
			cyril++
		case r >= 0x0600 && r <= 0x06FF, r >= 0x0750 && r <= 0x077F:
			arab++
		case r >= 0x0E00 && r <= 0x0E7F:
			thai++
		case unicode.IsLetter(r) && unicode.Is(unicode.Latin, r):
			latin++
		}
	}
	switch {
	case kana > 0:
		return "ja"
	case hangul > 0:
		return "ko"
	case cyril > 0:
		return "ru"
	case arab > 0:
		return "ar"
	case thai > 0:
		return "th"
	}
	// 汉字与拉丁的争夺：汉字够多就算「他在写中文」，汉字只有零星一两个而英文成句就算「他在用英语」，
	// 两头都不像（「翻这个 document」这种）→ 回空串，交给界面语言打底，别猜。
	if han >= inputLangMinChineseVoice {
		return "zh"
	}
	if latin >= inputLangMinLatinLetters && countEnglishWords(s) >= inputLangMinEnglishWords {
		// 零星汉字（0～3 个）配成句英文也算数：那多半是他顺口引用我们界面上的中文词
		// （现网真实形态「how much is 积分?」）。判 en 而不是 zh，
		// 否则中文界面里的英文提问永远修不掉。
		return "en"
	}
	if han > 0 {
		return "zh" // 「多少钱」这种三个字以下的纯中文短句
	}
	return ""
}

// effectiveReplyLang 本轮**实际拿来决定「用什么语言说」的那个语种**。
//
// 口径就是用户当场定的那句：「默认的打开词根据用户前台语言，但后续用户用什么语言，就回复什么语言」——
// 所以这里是**双向**接管：中文界面里的英文提问答英文，英文界面里的中文提问答中文。
// 判不出语种（detectInputLang 回 ""）时回落界面语言，行为与本条之前逐字相同。
//
// 唯一需要额外处理的是简繁：汉字两边同形，粗判分不出访客写的是简体还是繁体，
// 于是判成中文时**书写档跟着界面语言走**（繁体界面的访客继续拿繁体，哪怕他自己打的是简体）——
// 简繁不是"语种"而是同一语种的两个书写档，界面已经替他选过一次，不该被一句简体输入改掉。
func effectiveReplyLang(uiLang, input string) string {
	base := canonicalLang(uiLang)
	switch d := detectInputLang(input); d {
	case "":
		return base
	case "zh", "zh_hant":
		if base == "zh_hant" {
			return "zh_hant"
		}
		return "zh"
	default:
		return d
	}
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
//
// ★ 092x 红腿三：汉字档这里额外把**字形**写出来（「能」＋「言」），并点名现网真出现过的错形「能与」。
//
//	这条只是把请求说得更具体，它本身不是保证——保证在 brand_guard.go 的出站归一那条腿上。
//	把它写进提示词的理由是成本：模型少犯一次，出站就少改一次、少一条 WARN。
func brandNamingLine(uiLang string) string {
	if brandNameFor(uiLang) == "能言" {
		return "- 品牌名：本轮用中文写法「能言」（该语种界面与挂件标题也是这个写法）；" +
			"就是「能」＋「言」两个字，**不许写成「能与」**，" +
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
// ★ 082x 第九条起，入参是**本轮作答语言**（effectiveReplyLang 的产物），不再是裸界面语言：
// 中文界面里的英文提问，这一段要教模型写英文、要钉 LangCross、要钉 credits——
// 全部按对方的语言走。界面语言只是它的打底值。
//
// 三条口径按重要性排：
//  1. 作答语言已由服务端定下 —— 界面语言打底、访客输入语言优先，这条判定在这里落地；
//  2. 访客输入语言优先 —— 服务端粗判不到的语种（法／德／西这类）仍由模型自己跟着输入走；
//  3. 品牌名分语言 —— 见 brandNamingLine：这是 082x 用户指令第一条
//     （「品牌英文名叫 LangCross，不叫 nengyan」）在模型侧的落点。
func replyLangBlock(answerLang string) string {
	var sb strings.Builder
	sb.WriteString("【回复语言】（这一段管「用哪种语言说」，和【承诺边界】一样优先于说话方式）\n")
	if l := langLabel(answerLang); l != "" {
		sb.WriteString("- 本轮作答语言：" + l +
			"。（服务端已按「界面语言打底、访客输入语言优先」定下来了，就照这个语言写整条回复，不要夹回另一种语言。）\n")
	} else {
		sb.WriteString("- 没定出作答语言（既没拿到界面语言、也判不出访客这句用的哪种）：按访客这一轮输入所用的语言作答。\n")
	}
	sb.WriteString("- 访客输入的语言和界面语言不一致时，**跟访客输入走**（他用哪种语言问，就用那种语言答），界面语言只作默认值。\n")
	sb.WriteString("- 【相关知识】【系统现值】里的素材是中文写的：面向非中文访客时先译成对方的语言再说，不许把中文原句直接贴出去（术语、品牌名、文件后缀这类专有写法除外）。\n")
	sb.WriteString(brandNamingLine(answerLang))
	sb.WriteString(pointsTermLine(answerLang))
	sb.WriteString("- 最后一行的功能入口标记【go:key】保持原样，key 不翻译、不改写。\n")
	return sb.String()
}
