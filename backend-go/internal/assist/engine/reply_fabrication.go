// ============ reply_fabrication.go · 职责说明 ============
// ★ 0AR 第 4 波 ⑲：挂件**编造承诺**的出站守卫（ hallucination guard ）。
//
// 现网实证三条（台账《修改文档》⑲，全部来自真实对话取证）：
//   - 承诺不存在的「API sandbox」；
//   - 承诺返回「cross-referenced Excel」（该形态只在字幕/数据文件那条口径里成立，文档翻译没有）；
//   - 编造存量与词条：「汽车零件术语库存了 **327 个标准词**」、「**"火花塞"固定译 "ignition plug"**」（错译，正确是 spark plug）。
//
// 为什么已有的两道不够用（这条守卫的存在理由，不是重复造闸）：
//   - `guardReplyQuote` 只管**钱**：它的判据是「算式与总额能不能由服务端系数复算」，
//     术语条数与词条对应关系根本不在它的射程里；
//   - `promise.go` 的【承诺边界】是**提示词**，而 081x 那一批的现网结论就写在那儿——
//     「光靠语气规范里『数字照抄知识原文』拦不住功能清单外的事」：提示词是概率性请求，
//     9B 档模型照旧会顺口补一个数字。出站判据才是保证。
//
// 三条判据（任一命中即**整句丢弃**＋一行 WARN，分档名见下面三个常量）：
//
//	① fabr_count_claim —— 具数声称：数字紧挨着「术语/词条/标准词/词库/行业词/高频词」这类
//	   存量名词。这一族**永远不该出现在正文里**：库里真有多少条不是对客户讲的事，
//	   【相关知识】也没写过，所以出现即编造（不需要查语料）。
//	② fabr_unknown_deliverable —— 具体交付物／环境名词，且**在本租户当前的能力清单里找不到**：
//	   能力清单＝`feature_links` 启用行的 name＋description ＋ `kb_entries` 启用行的 title＋content＋keywords。
//	   ★ 这一条刻意做成**派生判据**而不是写死名单：现网取证里「在线编辑器」既是模型的顺口话、
//	   也是库里真有的功能卡（seed 的 `features` 就有 `editor`），写死名单必然误伤；
//	   而「sandbox」在 30 条启用知识里零命中（本轮实测 `Excel`/`xlsx`/`API`/`SDK`/`批量` 都在知识里、
//	   `sandbox` 与「对照表」以外的环境词都不在）——**问库**才是这条判据的正确形态。
//	③ fabr_term_example —— 词条对应关系示例：「X」固定译 Y 这种形态（引号包词＋「固定/统一/标准/约定/官方」＋译）。
//	   这一条也过能力清单：**两个词都在启用知识里出现**才算引用语料（放行），
//	   否则就是模型自己配的一对译法——它配错的那一次（火花塞→ignition plug）就是现网取证。
//
// ⚠️ 三条纪律（与 §一·13 那条咽喉的既有口径逐字对齐）：
//   - **只挂 `Engine.Respond`**，不许塞进纯函数 `sanitizeVisitorText`：那条还服务历史回放与补翻，
//     语料口径不同（回放里出现「327 个标准词」是**已经发出去的历史**，删它会让客户看到的
//     记录与当时收到的不一致，而这条守卫的判据本身要问"此刻的能力清单"）。
//   - **只跑 `Source=="llm"`**：话术直配／流程／兜底都是人写的文案，不经模型、不可能编造。
//   - **只改对话正文出栈，不碰 `localize()` 缓存 ⇒ 本腿不需要抬 `cannedPromptRev`**
//     （§一·13 那条「改 canned 译文最终形态要抬档」的射程是欢迎词／chips 那一路，不是这里）。
//
// 两条 fail-soft（误删客户正文的代价高于漏掉一句编造）：
//   - **能力清单取不到就整条腿不判**（②③ 依赖它；① 不依赖，照判）：
//     表读失败／清单为空 ⇒ 无判据可依，把正文删一半不是"安全方向"。
//   - **拒绝句豁免（只护 ②③，不护 ①）**：句子里带「不支持／没有／无法／不能／不保证／做不到／暂…」
//     这类否定标记时跳过能力档与词条档。第三节教模型「问到就直说不支持」，那些句子**必须能发出去**；
//     把「我们不支持语音翻译」整句删掉，比放过一句编造严重得多（现网形态是回答缺了拒绝那半句，
//     客户只会以为"能言默认支持"）。① 不享受这一豁免的理由见 `fabricationReasonFor` 那段。
//   - **剥空即回退**：整条回复全被判编造属异常形态（多半是清单被误清空），宁可不发空气泡。
//
// 射程边界（登记，不假装覆盖）：
//   - 「返回 cross-referenced Excel」那一形态里 **Excel 本身是真交付物**（`xlsx` 在启用知识里 5 次命中），
//     所以 ② 抓不到它；那一句的真缺陷是**缺前提**（对照表只在字幕/数据文件口径成立），
//     属 promise_rules 的口径面。要用机械判据覆盖它，得把「交付形态 ↔ 文件类型」做成一张对照表再核，
//     本批不做（写进落地账待决），先靠提示词那一档。
//   - ① 的中文档依赖「术语/词条/…」这套中文名词；非中文正文里的同类编造（"327 standard terms"）
//     由拉丁档那条正则兜，拉丁档名词集合刻意窄（terms/glossary/terminology/entries/words），
//     再宽就会顶掉合法报价（报价句里的数字接的是积分/元，见 TestGuardReplyFabricationKeepsLegalQuote）。
//   - ② 的拉丁档**按整词问库**，于是"库里只写了 xlsx、正文说 excel"会被判编造（两种写法是两个词）。
//     现网 `seed/seed.json` 里 excel／xlsx／xls／csv 四种写法都在启用知识里出现（本轮实测 3/5/7/2 次命中），
//     所以今天没有实伤；真要根治是让库里把交付形态的常见写法写全（与 ⑱ 那条「name 是对外文案」同源），
//     **不是**把判据退化成"名单里挨着的词互相豁免"——那等于取消这一档。
//   - ② 的能力清单只到**租户级挂件库**（feature_links／kb_entries），不含主服务那边的套餐表；
//     所以「套餐里含 Notion 同步」这种**第三方集成**说法不在射程（它既不在名单里，也不该由这条腿判）。
//
// =============================================
package engine

import (
	"context"
	"regexp"
	"strings"

	"translator/internal/observability"
)

// 三个分档名是**对外排障契约**（与补翻那七个 reject* 同等待遇）：日志里的 reason 决定运维
// 该去收紧提示词、该去补知识库条目，还是该把这判据当缺陷报上来。单测逐字钉字面量。
const (
	fabrCountClaim     = "fabr_count_claim"         // ① 编造存量数字
	fabrUnknownDeliver = "fabr_unknown_deliverable" // ② 能力清单外的交付物／环境
	fabrTermExample    = "fabr_term_example"        // ③ 自配的词条对应关系
)

// fabricationSentLimit 参与判据的**最短**句长（归一前的原始 rune 数）。
// 取 6：「支持Excel」这类 5 字以内的短句在本挂件里几乎只出现在按钮名／chips（那两路有自己的闸门），
// 而正文里的编造承诺都是成句的。太短的片段判它只会被子串命中顶成误伤。
const fabricationSentLimit = 6

// countedClaimCJK ① 的中文档：数字（含全角、千分位、"10 万+"这种量级写法）后面紧接存量名词。
// 名词集合刻意封闭且窄：只收「库里条数」这一族，**不收**积分/元/折/页/字这些报价单位
// （那是 guardReplyQuote 的射程，两条闸抢同一句就会互相顶掉）。
var countedClaimCJK = regexp.MustCompile(
	`[0-9０-９][0-9０-９,，.．\s]*(?:万|千)?\s*[+＋]?\s*(?:个|条|份|种|款|组)?\s*(?:高频|标准|专业|核心|常用)?` +
		`(术语|词条|标准词|词库|行业词|高频词)`)

// countedClaimCJKReversed ① 的**反序**中文档：「高频行业词大概 3000+ 个」——名词在前、数字在后。
// 不补这一支就会放过同一缺陷的另一种语序，而模型这两种说法都真说过（现网取证是正序，
// 但「我们有 327 个标准词」和「标准词大概 327 个」是同一句话的两种表层形式）。
// 间距刻意收到 ≤6 个非数字字符，并且结尾**必须**是量词（个/条/份/种/款/组）：
// 「术语按 1000 字符计费」里 1000 后面接的是「字」，不落在量词集合里，判不中——
// 报价侧的说法仍然整段留给 guardReplyQuote（两条闸的分工见 guardReplyFabrication 那段）。
var countedClaimCJKReversed = regexp.MustCompile(
	`(?:高频|标准|专业|核心|常用)?(?:术语|词条|标准词|词库|行业词|高频词)[^0-9０-９。；;，,]{0,6}` +
		`[0-9０-９][0-9０-９,，.．\s]*(?:万|千)?\s*[+＋]?\s*(?:个|条|份|种|款|组)`)

// countedClaimLatin ① 的拉丁档（非中文正文同样会编存量）。名词集合窄到"术语/词条"这一族。
var countedClaimLatin = regexp.MustCompile(
	`(?i)\b[0-9][0-9,\.]*\s*(?:k|thousand|million)?\s*\+?\s*` +
		`(standard\s+terms?|glossary\s+terms?|glossary\s+entr(?:y|ies)|terminolog\w*\s+entr(?:y|ies)|terms?|entr(?:y|ies)|vocabulary)\b`)

// deliverableCJK ② 去能力清单里查的**具体交付物／环境名词**（汉字档：子串匹配就够，汉字没有词边界）。
//
// 这张名单只负责"哪些词值得问一次库"，**不负责**判它有没有——判据是库。
// 所以名单可以放宽到"产品形态类名词"而不误伤：库里有的（`editor`／`xlsx`／`API`／`SDK`／`插件`／`对照表`／`webhook`）
// 一律放行，库里没有的（现网那一条 `sandbox`）才被丢。
// ⚠️ 名单里**不放**三类词，都是本轮逐条对着现网口径挑出来的：
//   - 泛用名词（文件／表格／页面／链接）：任何正文都可能出现，误删代价太高；
//   - 「真交付物的近义写法」（双栏表／双语对照／加载项／office 加载）：Word 加载项与对照表都是**平台真有**的
//     形态（`internal/api/office.go` 的 taskpane、`text_formats.go` 的 WriteComparisonXlsx），
//     它们没原样出现在启用知识里就会被判编造——这类"名单写法与库里写法不同"的假阳性，
//     正是本判据必须问库、而名单必须窄的理由；
//   - 客户自己环境的说法（测试环境／生产环境／staging／demo）：企业客户谈的是**他们自己的**环境，
//     跟平台能力无关（「先在测试环境跑一批」是正常的）。
//
// ⚠️ 后两类名单里的每一条都对应「promise.go 第三节」或「`internal/engine/file.go` 的 allowedExt 里没有」，
// 也就是每一条都有代码实证；将来要加词，先做一次代码核实，别把"模型说过"当成"平台没有"。
// 每一条都另核过**种子库现值**（本轮实测：`试用环境／体验环境／预览环境／在线试用／沙箱／回调接口／
// 批量接口／开放接口／命令行／扫描件／实时协作／协同编辑／音频翻译／数学公式` 在 `seed/seed.json` 全量文本里
// 均 0 命中，`对照表` 1 次、`编辑器` 8 次、`webhook` 1 次＝库里真有的那些不在这张名单里）。
// ⚠️ 「公式」单字不收、只收「数学公式」：正文里出现公式绝大多数是**算价公式**（合法报价句，
// 由 guardReplyQuote 那档管），而「数学公式原样保留」是 promise.go 第三节明写没有的能力。
var deliverableCJK = []string{
	"沙箱", "试用环境", "体验环境", "预览环境", "在线试用",
	"对照表", "回调接口", "批量接口", "开放接口", "命令行",
	"语音翻译", "音频翻译", "视频翻译", "图片翻译", "扫描件",
	"实时协作", "协同编辑", "动画", "切换效果", "数学公式",
}

// deliverableLatin ② 的拉丁档名单。**这一族必须按词匹配，不能用子串**：
// 子串会把正常英文单词判成交付物 —— 本轮逐条核过的高危碰撞对是
// `cli` ⊂ `client`、`ocr` ⊂ `democracy`（客户真会送来讲政治制度的文档）、`xls` ⊂ `xlsx`。
// 两侧的"有没有"都用同一个词边界正则问，问句与问库同一把尺子（§一·13 那条「判据两侧同口径」）。
var deliverableLatin = []string{
	"sandbox", "excel", "xlsx", "xls", "csv", "webhook", "restful", "cli", "ocr",
}

// deliverableLatinPat 拉丁档的**整词**匹配（\b 词边界；名单为空时编译出一条永不命中的表达式）。
// 小写源＝调用方一律先 ToLower 再匹配（`deliverableMentions` 负责），所以这里不用带 (?i)。
var deliverableLatinPat = compileWordPat(deliverableLatin)

// compileWordPat 把小写名单编成一条 `\b(?:a|b|c)\b` 正则（每一项过 QuoteMeta，名单里出现元字符也不炸）。
// 空名单回 nil（本包的名单是包级字面量，不会为空；判 `nil` 只是让"名单被清空"退化成不判，
// 而不是编出一条语义不明的正则）。
func compileWordPat(words []string) *regexp.Regexp {
	if len(words) == 0 {
		return nil
	}
	alt := make([]string, 0, len(words))
	for _, w := range words {
		alt = append(alt, regexp.QuoteMeta(strings.ToLower(w)))
	}
	return regexp.MustCompile(`\b(?:` + strings.Join(alt, "|") + `)\b`)
}

// deliverableMentions 这段文字里点到了哪些交付物名词（去重、原样小写返回）。
// 正文侧与能力清单侧共用它，两档名单分别走子串（汉字）与整词（拉丁）。
func deliverableMentions(text string) []string {
	lower := strings.ToLower(text)
	seen := make(map[string]struct{}, len(deliverableCJK)+len(deliverableLatin))
	out := make([]string, 0, 4)
	for _, c := range deliverableCJK {
		if strings.Contains(lower, c) {
			if _, dup := seen[c]; !dup {
				seen[c] = struct{}{}
				out = append(out, c)
			}
		}
	}
	if deliverableLatinPat != nil {
		for _, m := range deliverableLatinPat.FindAllString(lower, -1) {
			if _, dup := seen[m]; !dup {
				seen[m] = struct{}{}
				out = append(out, m)
			}
		}
	}
	return out
}

// fabricationRefusalMarkers 拒绝／前提句豁免标记（见到任一即跳过 ②③ 两档；① 不享受豁免，见下面说明）。
// 口径与 promise.go 第三节同源：那一节教模型「问到就直说不支持」，这类句子必须发得出去。
// 「不了／不会／不保留／不含」也在这里：第三节那些"翻不了""不会镜像重排"的正当拒绝句
// 用的正是这一族说法，漏了它们就等于把客户的**正确答案**删掉。
//
// ⚠️ 刻意**不收**「别」这一个字：它会命中「特别／别的／识别／级别」这类正常词，
// 一条豁免标记把正文里一大片句子都放行，等于把这条守卫调成静默。
// 要豁免「别说我们有 sandbox」那种祈使否定，得靠整句形态判，不是靠单字。
// ⚠️ 拉丁档同理**不收**裸 "not"／裸 "no"：前者会被 "not only"、后者会被 "no setup required"
// 这类正常句子领走，等于给外文正文关掉这一档。这里只收**成词的否定**（do not／does not／not available…），
// 每一条都是"两个词以上、语义就是否定"的形态；代价是外文拒绝句的措辞空间比中文宽，
// 偶有漏豁免（那种句子被删时 WARN 里带原文，现网按日志补词条即可）——
// 这一族与汉字档同条纪律：宁可漏豁免少数，不可把判据调成静默。
var fabricationRefusalMarkers = []string{
	"不支持", "没有", "无法", "不能", "不保证", "做不到", "不做", "不在", "暂未", "暂不", "尚无", "还没", "未经",
	"不了", "不会", "不保留", "不含", "不算", "不作", "只当",
	"只能", "仅限", "需要", "前提", "除非", "请勿",
	"do not", "does not", "don't", "doesn't", "cannot", "can't", "unable", "unavailable",
	"not support", "not available", "not offer", "not provide", "no support", "never", "without", "lack",
}

// termExamplePat ③ 的形态：「X」＋（固定／统一／标准／约定／官方／推荐／规范）＋ 译…＋ Y。
// 两个捕获组分别是被断言的源语词与目标语词；引号族覆盖「」『』“”与半角引号。
//
// ⚠️ 「固定/统一/标准…」这一档**不许改成可选**：判据要抓的是"断言了一条对应关系"，
// 而「这段译为中文即可」那种正常说法不带限定词，一旦可选，正则会把整段回答扫成编造。
// 现网取证那一形态（「火花塞」固定译 ignition plug）带限定词，窄档足够。
//
// ★ 「译」后面这一截是三条分支，不是"译为/译成/译作"一条走到底：
//   - `法[是为]` ＝「标准译法为 ebook」；
//   - `[^文本者]` ＝ 裸「译」＋后面那个非「译文／译本／译者」的字，**这一分支才是现网取证那一形态**
//     （「火花塞」固定译 "ignition plug" 里 译 之后直接是空格或引号，没有「为」）。
//     只写 `译[为成作]` 的话，这一句一个字符都命不中，③ 整条腿对现网缺陷无感——
//     而单测如果只测「固定译为 X」那种带「为」的写法，就会把它一直当"已覆盖"。
//   - 「统一翻译为中文」那种**正常说法**在这一档必须不命中：限定词与「译」之间只容空白，
//     所以「统一」后面紧跟的是「翻」，正则当场断在半路（这一条是 ③ 的反向对照锁）。
var termExamplePat = regexp.MustCompile(
	`[「『“"']([^「」『』“”"'。\s]{1,24})[」』“"'][\s，,、]{0,6}(?:的)?[\s]{0,4}` +
		`(?:固定|统一|标准|约定|官方|推荐|规范)\s*译(?:法[是为]|[^文本者])\s{0,4}` +
		`[「『“"']?([^「」『』“”"'。；;，,、]{1,24})[」』“"']?`)

// capabilityCorpus 拼出「本租户此刻对外承诺过的能力」文本（小写归一，含功能卡与启用知识）。
// 第二返回值假＝清单取不到（表读失败／一行启用都没有），调用方据此**整条腿不判**。
//
// ⚠️ 刻意**不含** configs.promise_rules：那一段的第三节整段在列举「平台没有的能力」
// （动画／公式／图片文字／OCR），把它的字面词灌进白名单就等于给同一句编造发通行证——
// 这是「白名单要从正集合取」的一个具体形态，别顺手加进来当"能力口径"。
// ⚠️ 与 `knownFeatureKeys`／`featureNameFor` 读同一张表的同一档（enabled），不另写一份名单。
func (e *Engine) capabilityCorpus() (string, bool) {
	var b strings.Builder
	links, lerr := e.db.List("feature_links", true)
	kbs, kerr := e.db.List("kb_entries", true)
	if lerr != nil && kerr != nil {
		return "", false // 两张表都读不到＝无判据可依
	}
	// ★ 字段之间**必须留分隔符**（本批单测抓出来的）：拉丁档问库走的是 `\b词\b` 整词匹配，
	// 而 name／description／key 三个字段直接拼起来会把相邻字段粘成一个长 token
	// （实测读数：卡片 name="API 联调 Sandbox"、key="sandbox" 拼成 "sandboxsandbox"，
	// 前一个 occurrence 尾部接 s、后一个头部接 x，两处词边界都不成立 ⇒ 库里明明有这一能力，
	// 判据却照样判成编造）。汉字档靠子串不受影响，所以这个坑只有整词那一档会踩。
	for _, r := range links {
		b.WriteString(asStr(r["name"]))
		b.WriteString(" ")
		b.WriteString(asStr(r["description"]))
		b.WriteString(" ")
		b.WriteString(asStr(r["key"]))
		b.WriteString("\n")
	}
	for _, r := range kbs {
		b.WriteString(asStr(r["title"]))
		b.WriteString(" ")
		b.WriteString(asStr(r["content"]))
		b.WriteString(" ")
		b.WriteString(asStr(r["keywords"]))
		b.WriteString("\n")
	}
	corpus := strings.ToLower(b.String())
	if strings.TrimSpace(corpus) == "" {
		return "", false // 清单为空（新库／被清空）：宁可不判，不许把正文删空
	}
	return corpus, true
}

// fabricationScope 一次守卫调用里共享的「能力清单」视图：清单原文（③ 要用）＋清单里确实出现过的
// 交付物名词集合（② 要用）。
//
// ★ 集合**每次调用算一次**，不是每句算一次：清单是整租户的知识正文（种子库现值 30 条启用行），
// 逐句在里面跑一遍名单＝把一次回复的开销乘上句数，而这条腿挂在每个请求都要走的出站咽喉上。
type fabricationScope struct {
	corpus string
	known  map[string]struct{}
	ok     bool
}

// buildFabricationScope 从清单原文算出共享视图；corpusOK 为假时**只带原文、判据整条腿不走**
// （②③ 都依赖清单，取不到就都不判，见文件头那条 fail-soft）。
func buildFabricationScope(corpus string, corpusOK bool) fabricationScope {
	sc := fabricationScope{corpus: corpus, ok: corpusOK}
	if !corpusOK {
		return sc
	}
	known := make(map[string]struct{}, len(deliverableCJK)+len(deliverableLatin))
	for _, m := range deliverableMentions(corpus) {
		known[m] = struct{}{}
	}
	sc.known = known
	return sc
}

// fabricationReasonFor 判这一句是否编造，返回分档名（空串＝放行）。
// 三条判据按「不依赖语料的先判」排序：① 恒可判；②③ 要问清单。
//
// ★ ① 排在拒绝句豁免**之前**（这一处顺序是判据，不是风格）：
// 「数字＋存量名词」本身就是缺陷形态，模型在任何语境下都不该对客报出库里条数——
// 同句里带一个「没有／需要／不会」就整句放行，等于把「我们有 327 个标准词，不过没有全行业覆盖」
// 这一族最容易漏的复合形态留在正文里。代价是「我们没有 327 个标准词」这类否认句也会被删掉半句，
// 但拒绝句在 promise_rules 第三节的正当形态是**不带具体数字的直说**（「这个我帮你确认下」），
// 一旦句中出现条数数字，那句本身就已经越界了。这一档由
// TestGuardReplyFabricationDropsCountedClaims 的最后一段钉住，防止将来被顺手挪回豁免之后。
func fabricationReasonFor(sent string, sc fabricationScope) (string, string) {
	if len([]rune(strings.TrimSpace(sent))) < fabricationSentLimit {
		return "", ""
	}
	lower := strings.ToLower(sent)
	if m := countedClaimCJK.FindString(sent); m != "" {
		return fabrCountClaim, m
	}
	if m := countedClaimCJKReversed.FindString(sent); m != "" {
		return fabrCountClaim, m // 反序语（「高频行业词大概 3000+ 个」），同一缺陷的另一层形式
	}
	if m := countedClaimLatin.FindString(sent); m != "" {
		return fabrCountClaim, m
	}
	for _, mk := range fabricationRefusalMarkers {
		if strings.Contains(lower, mk) {
			return "", "" // 拒绝／前提句豁免（只护 ②③，见上面那段）
		}
	}
	if sc.ok {
		// 正文侧点到的每一个交付物名词，都要在能力清单里以**同一种形态**（汉字子串／拉丁整词）出现才算真交付物。
		// 判据问的是库，不是这张名单 ⇒ 运营补一条功能卡就能让这一句合法发出去，不需要发版。
		for _, m := range deliverableMentions(sent) {
			if _, has := sc.known[m]; !has {
				return fabrUnknownDeliver, m
			}
		}
		if m := termExamplePat.FindStringSubmatch(sent); m != nil {
			// 放行条件＝**两个词都在**启用语料里（＝引用知识库给的译法）。
			// 刻意不做"源语词在库里就放行"那种宽松档：现网那条错译（火花塞→ignition plug）
			// 恰恰是"源语词在库里、目标语词是模型自己配的"，宽松档等于对这一族完全无感。
			zh, target := strings.TrimSpace(m[1]), strings.ToLower(strings.TrimSpace(m[2]))
			if zh != "" && target != "" &&
				!(strings.Contains(sc.corpus, zh) && strings.Contains(sc.corpus, target)) {
				return fabrTermExample, zh + "→" + target
			}
		}
	}
	return "", ""
}

// guardReplyFabrication 出站咽喉上的编造承诺过滤（挂点见 engine.go Respond，排在指令复述腿之后）。
//
// 与 guardReplyQuote 的分工（两条闸不许互相顶）：那条核**钱**（算式与总额能否由服务端系数复算），
// 这条核**能力与词条**，且本腿**先跑**。两条的边界要说实话，别写成"互不影响"：
//   - 报价句**独立成句**时本腿一字不动它（正向对照锁 TestGuardReplyFabricationKeepsLegalQuote），
//     报价腿拿到的还是原稿，复算得出就照发；
//   - 假存量与报价**混在同一句**里（「我们有 327 个标准词，1000 字只要 5 积分」）时本腿把整句删掉——
//     这是**安全方向**：分句器按句末标点切，一句里的前半句撒谎，后半句的报价也就没有可信的主体。
//     这一档不写成正向对照，写成注释就是假承诺，所以把它记在这里而不是锁里。
func (e *Engine) guardReplyFabrication(ctx context.Context, answerLang string, rep *Reply) *Reply {
	if rep == nil || rep.Content == "" || rep.Source != "llm" {
		return rep
	}
	corpus, corpusOK := e.capabilityCorpus()
	sc := buildFabricationScope(corpus, corpusOK)
	parts := splitNarrationSentences(rep.Content)
	kept := make([]string, 0, len(parts))
	type dropped struct {
		reason, detail, sent string
	}
	var gone []dropped
	for _, s := range parts {
		if strings.TrimSpace(s) == "" || strings.Contains(s, goMarkerHead) {
			kept = append(kept, s) // 控制序列所在句豁免：别把功能入口连坐删掉（同 guardReplyQuote）
			continue
		}
		reason, detail := fabricationReasonFor(s, sc)
		if reason == "" {
			kept = append(kept, s)
			continue
		}
		gone = append(gone, dropped{reason, detail, strings.TrimSpace(s)})
	}
	if len(gone) == 0 {
		return rep
	}
	joined := strings.TrimSpace(strings.Join(kept, ""))
	for _, d := range gone {
		observability.Warn(ctx, "assist.engine 正文出现编造承诺，已整句丢弃（幻觉守卫 ⑲）",
			"lang", canonicalLang(answerLang), "source", rep.Source,
			"reason", d.reason, "detail", d.detail, "sentence", d.sent)
	}
	if joined == "" {
		return rep // 全被判编造＝异常形态（多半是能力清单被清空），宁可不发空气泡
	}
	rep.Content = joined
	return rep
}
