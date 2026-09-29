// ============ localize.go · 职责说明 ============
// 挂件「非中文访客看到的中文文本」的翻译层，现下三个消费者：
//   - greeting：configs.welcome（运营在管理台写的中文欢迎词）或话术表里 stype=greeting 那条；
//   - chips：configs.quick_chips（逗号分隔的中文快捷提问）；
//   - reply（★ 082x 增补批）：LocalizeReply —— 模型/兜底已经把中文答案吐出来时，
//     出站再翻一次兜住。走 translateOnce 这条共用底座，但**不进缓存**：
//     对话正文每轮都是新句子，缓存键无从下手（欢迎词一年改不了几次，才值得落库）。
//
// ★ 082x（2026-09-29，用户指令「不能根据用户的前台语言和使用语言来回复，一律用中文」）：
//
// 对话正文有【回复语言】段管着（见 reply_lang.go），但开场那两样东西不走对话路径：
//   - greeting：configs.welcome（运营在管理台写的中文欢迎词）或话术表里 stype=greeting 那条；
//   - chips：configs.quick_chips（逗号分隔的中文快捷提问）。
//
// 它们是 greet 一次直出的文本，英文站访客打开挂件看到的**第一口气**就是这两行中文，
// 只修回复链路等于把门牌换了、屋里还挂着中文。
//
// 三种可选做法里选了「按需翻译 + 落库缓存」：
//  1. 前端词典兜底（给 12 语种各写一份欢迎词）——运营在管理台改的中文就永远不生效了，
//     把「后台改口即生效」这条现网口径直接废掉，不做。
//  2. 让运营自己填 12 份——现网一个人运营（见项目记忆），这是把工程问题推成体力活，不做。
//  3. 服务端按 lang 翻一次、缓存、原文变了就失效——本文件。
//
// 缓存落在 configs 表而不是进程内存，两个理由：
//   - 重启不重付：greet 在访客打开挂件的关键路径上，每次冷启都现翻一遍等于给首响加一次 LLM 往返；
//   - **可被运营看见并改**：键名 i18n:welcome:<lang>，值是「源文＋口径指纹 + 换行 + 译文」。
//     机翻不满意，直接在管理台把这段改掉即可——改完后指纹不匹配会被重新翻译覆盖吗？
//     不会：见 localize 的放行分支，人工改过的译文（指纹后面还跟着 "!manual" 标记）永久保留。
//     这条是刻意的：运营修订比机翻质量高，自动覆盖等于把人家改的东西吃回去。
//
// ★ 082x 第七条：指纹里必须带**翻译口径**（cannedPromptRev + 品牌名/计费单位两张表），只算原文不够。
//
//	上一批术语口径上线后现网英文首屏仍念 "integral"，就是因为缓存只认原文、原文常年不改，
//	换件对这条软路径**完全无效**（界面还一切正常）。详见 localizeContract 的注释与 localize_test.go 的断言。
//
// 失败口径照【系统现值】那条硬规矩来：**取不到就原样出中文，绝不编一份译文**。
// 中文原文难看，但它是真话；机翻失败时返回半句假译文是事故。
// ★ 同一条规矩现在也管「半句没翻」：译文里留着没翻的中文词时先补翻一次，补不动就整条判失败出原文
// （判残与补翻见 han_residue.go——082x 现网抓到英文首屏 "credits充值"、日文首屏「翻訳什么？」）。
// =============================================
package engine

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"translator/internal/assist/llm"
	"translator/internal/observability"
)

// localizeMaxTokens **canned 文案**（欢迎词 / chips）译文一次生成的额度上限。两三行足够。
// 但主模型是思维链模型（GLM-Z1 一类，见 engine.go 074x 那条注释），思维链要吃掉一部分额度，
// 400 是「正文两行 + 链子」的实测安全档，不是随手取的。
// ⚠️ 别把这个数拿去过对话正文——那一路用 replyLocalizeMaxTokens（见 reply_lang_check.go），
// 一份二十行的回答在 400 额度下会被截成半句译文，比整段中文更糟。
const localizeMaxTokens = 400

// localizeTemperature 翻译用的温度取 0.2（对话用 0.7/1.0）。
// 翻译要的是照抄语义，不要发挥；这条和「温度不是音色旋钮」（080x）是同一件事的另一面——
// 温度管不了语气，但管得住改写幅度。
const localizeTemperature = 0.2

// manualMark 人工改过的译文在指纹后追加这个标记，localize 见到就永久放行不再重翻。
const manualMark = "!manual"

// cannedPromptRev canned 译文所依据的「固定句式」版本号。
// 改 translateOnce 里那段与语种无关的固定要求（行数/不加解释/温度/max_tokens 口径）时 +1，
// 它进缓存指纹（见 localizeContract），一改就让全网 canned 译文在下一次 greet 时重翻。
//
// ★ 082x 第八条抬到 082x-4 的**不是提示词**，而是译文出栈后的卫生（剥口径复述）：
// 库里已经躺着一份带脏尾巴的日文欢迎词，它指纹匹配、会被原样命中，光加剥逻辑救不到它。
// 抬一档版本号＝让那一份旧译文自动作废、下一次 greet 用新卫生重翻——
// ⚠️ 这是这条链的通用口径：**凡是改"译文出栈后的处理"，都要顺手抬这个版本号**，
// 否则改动只对新生成的译文生效，老缓存会一直把修前的形态投给客户（同"换件没修"那一族）。
//
// ★ 082x 第十条（2026-09-29 用户第三次带截图报同族症状）**没有抬这一档，理由是射程**：
// 这一批改的是**对话正文出栈**的词表（`sanitizeVisitorText` 那条链），而对话正文根本不落缓存；
// canned 欢迎词／chips 的译文文本与契约段一字未动（它们走的是另一张 `translationEchoMarkers`），
// 缓存里没有需要作废的形态 ⇒ 抬档只会让每台挂件首启现翻一遍，零收益（同《部署指南》〇-AC 那条口径）。
// ⚠️ 将来若动的是 `translationEchoMarkers`／`translateContract`／`translateOnce` 的出栈处理，
// 按上面那条通用口径**必须抬**，别拿本段当"这族改动都可以不抬"的先例。
//
// ★ 092x 红腿三（2026-09-29）**这一批就属于"必须抬"那一档**：改的是 translateOnce 的出栈处理
// （品牌名以 ⟦BRAND⟧ 过桥、出栈按语种档还原，见 brand_guard.go）。
// 库里那份日文欢迎词正是带着「能与」躺着的状态——它指纹匹配、会被原样命中，
// 只加还原逻辑救不到它，必须抬一档让旧译文自动作废、下一次 greet 用新链路重翻。
const cannedPromptRev = "092x-1"

// translationEchoMarkers 翻译模型复述**指令本身**时的说法（★ 082x 第八条，现网日文首屏实证）。
//
// 与 internalEchoMarkers 的区别：那张表管"对话模型把提示词段名吐进正文"（【系统现值】这类），
// 这张表管"翻译模型把我给它的翻译要求当内容写进译文"（"行数一致""不许""翻译成"）。
// 两类都只在**括号段**里出现才删，且收的都是中文指令用词——
// 翻成英/俄/泰的正文里不可能自然出现这些字串，误伤面接近零。
// ⚠️ 刻意不收「品牌名」「计费单位」这两个词本身：它们是**话题**而不是指令，
// 客户问"你们品牌名怎么来的"时译文里真会出现「品牌名」，收了就是把答案吃掉。
var translationEchoMarkers = []string{
	"行数一致", "行数不变", "保持原意", "不要加解释", "不许", "禁止", "按语种",
	"译文", "翻译成", "译文里", "目标语言", "要求：", "上一版", "界面语言", "原文照抄",
}

// translateContract 翻译路上那两条**对外口径**（品牌名 + 计费单位），拼提示词和算缓存指纹都用它。
// 单一事实源仍是 brandNameFor／pointsTranslationLine 那两张表，这里只负责"把它们合成一段文本"。
func translateContract(uiLang string) string {
	return "品牌名口径（与对话回复同一张表，见 brandNameFor）：" +
		"本轮界面语言为 " + langLabel(uiLang) + "，品牌名一律写作「" + brandNameFor(uiLang) + "」，" +
		"任何情况下都不许写成 Nengyan、NengYan 之类拼音。\n" +
		pointsTranslationLine(uiLang)
}

// localizeContract canned 译文缓存的口径指纹成分。
//
// ★ 082x 第七条（2026-09-29 换件后现网复问实证，这条不是假设）：
//
//	术语口径（积分→credits/ポイント）上一批已经写进翻译提示词，代码也真上线了，
//	可英文首屏照样念 "integral"、日文首屏照样写「インテグレーション」——
//	因为 canned 译文**落库缓存**，而旧指纹只算中文原文。
//	原文一年不改一次，口径改了十几次，缓存一次都不会失效：
//	**换件等于没修**，而且界面看不出任何异常（它确实"翻好了"，只是翻的是旧口径）。
//
//	所以指纹必须把「这版译文是按哪一版口径翻出来的」一起算进去：
//	cannedPromptRev 管固定句式，translateContract 管品牌名与计费单位两张表——
//	改这两处任一，旧译文自动作废、下一次 greet 现翻（运营手工改过的 !manual 档不受影响，
//	那条放行在指纹比对之前）。
func localizeContract(uiLang string) string {
	return cannedPromptRev + "\x00" + translateContract(uiLang)
}

// srcFingerprint 源文本指纹（sha1 前 12 位）。
// 为什么不用整串 sha1 也不用品内容本身：缓存键要短到能一眼读出来（管理台列 configs 时不糊屏），
// 又要长到不会撞——12 个 hex 字符 = 48 bit，在「一个运营改十几次欢迎词」的量级上够用。
//
// ⚠️ 传进来的字符串必须已经带上口径（见 localizeContract）：只算原文就是上面那条事故。
func srcFingerprint(s string) string {
	sum := sha1.Sum([]byte(s))
	return hex.EncodeToString(sum[:])[:12]
}

// localize 把中文 canned 文本翻成访客界面语言；下列任一情况**原样返回中文**：
// uiLang 是中文系 / 文本为空 / LLM 未接入 / 上游失败 / 译文空。
// 命中缓存（同指纹且非人工档）直接回缓存；指纹不符（运营改过原文）重翻并覆盖。
func (e *Engine) localize(ctx context.Context, kind, text, uiLang string) string {
	if strings.TrimSpace(text) == "" || visitorWantsChinese(uiLang) {
		return text
	}
	label := langLabel(uiLang)
	if label == "" {
		return text // 未知语言代码：不猜，原样出中文（同 reply_lang.go 的空档口径）
	}
	key := "i18n:" + kind + ":" + canonicalLang(uiLang)
	fp := srcFingerprint(text + "\x00" + localizeContract(uiLang))
	if cached := e.db.GetConfig(key, ""); cached != "" {
		if head, body, ok := splitCachedTranslation(cached); ok {
			if strings.HasSuffix(head, manualMark) {
				return body // 人工改过：永久放行（见文件头第 3 条）
			}
			if head == fp {
				return body
			}
		}
	}
	client := e.ensureLLM(ctx)
	if !client.Enabled() {
		return text
	}
	out, err := e.translateOnce(ctx, client, text, uiLang, localizeMaxTokens,
		"产品欢迎语/短问句", "网站右下角的 AI 客服挂件的首屏")
	if err != nil {
		// 失败原样出中文（见文件头「绝不编一份译文」）。这一行日志是这条软路径唯一的露面机会：
		// greet 界面看不出「没翻成」，没有它就只能等访客截图来报。
		observability.Warn(ctx, "assist.engine canned 文案翻译失败，原样出中文",
			"kind", kind, "lang", uiLang, "err", err)
		return text
	}
	_ = e.db.SetConfig(key, fp+"\n"+out)
	return out
}

// translateOnce 真正打一次「把这段中文翻成目标语言」的上游调用；回 error＝不可用
// （语种未知 / LLM 未接入 / 上游出错 / 译文空 / **被 max_tokens 截断**），调用方按 fail-soft 出中文。
//
// ★ 082x 增补（2026-09-29 现网复问取证）：从 localize 里抽出来是为了**对话正文也能复用同一条翻译路**。
//
//	现网实测 lang=en 的访客问价格，模型这一轮直接回了一整段中文（同一会话下一轮又回英文）——
//	说明【回复语言】段只是「请求」，不是「保证」：思维链模型会被上面五段中文素材的语域带跑。
//	与其再补一句更凶的提示词（那仍然是请求），不如在出站处按语种判一次、不合格就翻
//	（判据与调用点见 reply_lang_check.go 的 enforceReplyLang）。
//
// purpose/scene 由调用方给，是为了让模型知道该保留多少口语色彩：把一段带温度的回答按
// 「短问句」翻，很容易被压成一行说明书——那是把修好的东西再弄坏一次。
//
// maxTokens 也交给调用方，并且**截断一律判失败**：主模型是思维链模型，思维链吃掉的是
// 「链子 + 正文」的共用额度（074x 那条现网实证），一句欢迎词两行就够、一条回答却可能二十行，
// 拿同一个 400 去翻长回答会稳定翻出半句——半句译文比整段中文更接近事故（对外错报）。
// 所以 finish_reason=length 时不返回那份残缺译文，直接判失败让上层出原文。
func (e *Engine) translateOnce(ctx context.Context, client *llm.Client, text, uiLang string, maxTokens int,
	purpose, scene string) (string, error) {
	label := langLabel(uiLang)
	switch {
	case label == "":
		return "", errors.New("界面语言未知，不猜语种")
	case !client.Enabled():
		return "", errors.New("LLM 未接入")
	case strings.TrimSpace(text) == "":
		return "", errors.New("源文为空")
	}
	// ★ 092x 红腿三：品牌名不在翻译途中过桥，出栈就没有保证（现网实证：日文轮把「能言」写成「能与」）。
	// 送翻前换成不可译占位符，出栈再按语种档还原（机制与判据见 brand_guard.go）。
	src, srcHasBrand := protectBrandForTranslation(text)
	prompt := "把下面这段" + purpose + "翻译成 " + label +
		"，用途：" + scene + "。要求：保持原意、行数与语气，一行输入对应一行输出，" +
		"不要加解释、不要加引号、不要输出思考过程，也不许补原文没有的信息。\n" +
		// ★ 082x 增补：计费单位也要进翻译口径。现网实测补翻把「积分」写成 "integral"，
		// 而官网各界面写的是 credits／ポイント／кредитов——客户拿这个词跟账单核对，
		// 一个叫法对不上就是对外错报（判据表与交叉锁见 reply_lang.go 的 pointsTermByLang）。
		// 这段与品牌名口径一起收进 translateContract：提示词与 canned 缓存指纹共用同一份，不分叉。
		translateContract(uiLang) +
		brandTokenRuleLine(srcHasBrand) +
		"\n---\n" + src + "\n---"
	out, _, usage, err := client.Chat(ctx, localizeTemperature, maxTokens,
		[]llm.Message{{Role: "user", Content: prompt}})
	if err != nil {
		return "", err
	}
	if usage.Truncated {
		return "", fmt.Errorf("译文被 max_tokens=%d 截断（completion_tokens=%d），按失败处理", maxTokens, usage.CompletionTokens)
	}
	out = cleanTranslated(out)
	if out == "" {
		return "", errors.New("译文为空")
	}
	// ★ 082x 第八条：译文里混进**对翻译要求的复述**时剥掉那一段（现网实证：日文首屏尾巴上挂着
	// 「（行数一致、品牌名「能言」保持、ポイント…）」——模型把指令原文当内容写进了译文，
	// 而这条译文会被 localize 落库缓存，于是脏尾巴在访客屏幕上常驻，直到口径版本号再抬一档）。
	// 只剥括号段而不是整条判失败：括号外那半句是**好译文**，扔掉它等于让英文/日文访客退回看中文欢迎词。
	// 剥完什么都不剩才判失败（走调用方的 fail-soft 出中文原文，与「绝不编一份译文」同一条口径）。
	if stripped := dropParentheticals(out, translationEchoMarkers); stripped != out {
		observability.Warn(ctx, "assist.engine 译文夹带翻译口径复述，已剥掉该括号段",
			"kind", purpose, "lang", uiLang, "before", len(out), "after", len(stripped))
		out = strings.TrimSpace(stripped)
		if out == "" {
			return "", errors.New("译文除口径复述外没有内容")
		}
	}
	// ★ 082x 第七条：译文里留着**没翻的中文词**时补翻一次（现网实证：英文首屏 "credits充值"、
	// 日文首屏「翻訳什么？」——术语翻对了，句子却只翻半句，同一类事故的另一面）。
	// 补翻只许改善、不许换坏：判残数没减少、或行数变了，一律保留上一稿并记一行 Warn，
	// 让它在日志里露面（greet 界面看不出"半句没翻"，没有这行就只能等客户截图来报）。
	if leaks := hanResidueRuns(uiLang, text, out); len(leaks) > 0 {
		if fixed, ok := e.repairHanResidue(ctx, client, uiLang, text, out, leaks, maxTokens); ok {
			observability.Info(ctx, "assist.engine 译文汉字残留补翻生效",
				"lang", uiLang, "before", len(leaks), "after", len(hanResidueRuns(uiLang, text, fixed)), "leaks", strings.Join(leaks, ","))
			out = fixed
		} else {
			observability.Warn(ctx, "assist.engine 译文汉字残留补翻未改善，保留上一稿",
				"lang", uiLang, "count", len(leaks), "leaks", strings.Join(leaks, ","))
		}
	}
	// ★ 092x 红腿三：品牌名还原放在**最后一步**（补翻之后）。
	// 顺序是刻意的：占位符在译文里时，上面那两条判残/剥括号都把它当普通拉丁串放过，
	// 不会被误剥；等这些都做完了再还原成该语种档的品牌名，还原出来的字形就不会再经过任何改写。
	//
	// 占位符与任何品牌痕迹都不在＝模型把名字整块吃了：这里**只 WARN 不判失败**（口径与理由见
	// brand_guard.go 的 restoreBrandAfterTranslation）——判失败的后果是让访客退回看中文原文，
	// 那是拿更重的「语言保证」去换一个字面上的自称缺失，方向反了。
	// 但占位符残渣必须清掉：那是我们的内部记号，以任何形态出现在客户屏幕上都是机制外露。
	if srcHasBrand {
		restored, ok := restoreBrandAfterTranslation(out, uiLang)
		if !ok {
			observability.Warn(ctx, "assist.engine 译文里没有品牌名（占位符被模型吃掉），按译文发出并留证据",
				"kind", purpose, "lang", uiLang, "before", firstRunes(out, 120))
		}
		out = stripBrandTokenResidue(restored)
	}
	return out, nil
}

// LocalizeReply 把「模型答错了语言」的正文翻成访客语言（★ 082x 增补，见 reply_lang_check.go）。
// 与 canned 文案那条差两件事：**不落缓存**（欢迎词全网就一份、值得缓存；对话正文每条都不一样，
// 缓存既永不命中、又会把 configs 表写成垃圾场）、**额度按整条回答给**（replyLocalizeMaxTokens）。
func (e *Engine) LocalizeReply(ctx context.Context, text, uiLang string) (string, error) {
	return e.translateOnce(ctx, e.ensureLLM(ctx), text, uiLang, replyLocalizeMaxTokens,
		"AI 客服的一条回答", "网站右下角 AI 客服挂件的对话气泡")
}

// LocalizeGreeting 欢迎词按访客语言出（greet 关键路径；见文件头）
func (e *Engine) LocalizeGreeting(ctx context.Context, text, uiLang string) string {
	return e.localize(ctx, "welcome", text, uiLang)
}

// LocalizeChips 快捷提问 chips 按访客语言出。
// 入参是逗号分隔的原文串（与 configs.quick_chips 的存储形态一致），整串一次翻完再按原顺序拆回，
// 逐条各翻一次会把 greet 打成 N 次 LLM 往返。
// 拆回口径：**按原文条数取**——模型偶尔会把两行并成一行或多送一行，
// 条数对不上就整串原样返回（宁可看到中文，也不要错位串案的 chips）。
func (e *Engine) LocalizeChips(ctx context.Context, csv, uiLang string) string {
	if strings.TrimSpace(csv) == "" || visitorWantsChinese(uiLang) {
		return csv
	}
	src := []string{}
	for _, p := range strings.Split(csv, ",") {
		if p = strings.TrimSpace(p); p != "" {
			src = append(src, p)
		}
	}
	if len(src) == 0 {
		return csv
	}
	tr := e.localize(ctx, "chips", strings.Join(src, "\n"), uiLang)
	lines := []string{}
	for _, l := range strings.Split(tr, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) != len(src) {
		return csv
	}
	return strings.Join(lines, ",")
}

// splitCachedTranslation 拆缓存值：首行=指纹（可带 !manual 标记），其余=译文。
// 第三返回值假得表示「这行缓存不是本模块写的形态」，此时按未命中处理。
func splitCachedTranslation(v string) (head, body string, ok bool) {
	i := strings.IndexByte(v, '\n')
	if i <= 0 {
		return "", "", false
	}
	head = strings.TrimSpace(v[:i])
	body = strings.TrimSpace(v[i+1:])
	if head == "" || body == "" {
		return "", "", false
	}
	return head, body, true
}

// cleanTranslated 收口模型返回的译文：裁首尾空白，并剥掉**成对**的包裹引号。
//
// 只剥成对：提示词里已经写了「不要加引号」，但小模型仍会把整段用引号包起来；
// 单侧引号（"Hello 或 Hello"）不动它——那要么是内容本身，要么是被 max_tokens 截断，
// 两种情况都该原样留着或整条判失败，不该由这里悄悄改字节。
// 同理不剥 markdown 代码块围栏：出现围栏说明模型没照指令办，让它在管理台里被看见，
// 比被服务端抹平更好排查。
func cleanTranslated(s string) string {
	s = strings.TrimSpace(s)
	// 三对都试一遍：半角直引号、中文弯引号、英文弯引号——小模型三种都会用，
	// 而只认其中一种时，另一种就带着引号进客户屏幕（挂件气泡里多一圈 " 很难看）。
	pairs := [][2]string{{`"`, `"`}, {`“`, `”`}, {`‘`, `’`}}
	for _, p := range pairs {
		if len(s) > 2 && strings.HasPrefix(s, p[0]) && strings.HasSuffix(s, p[1]) {
			s = strings.TrimSpace(s[len(p[0]) : len(s)-len(p[1])])
			break
		}
	}
	return s
}
