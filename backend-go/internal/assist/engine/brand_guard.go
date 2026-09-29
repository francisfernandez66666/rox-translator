// ============ brand_guard.go · 职责说明 ============
// 品牌名的**出栈保证**：客户在任何语种的气泡里看到的品牌名，必须等于该语种档的写法。
//
// 为什么"写进提示词"不算修好（★ 092x 红腿三，2026-09-29 现网复问第四条）：
// brandNamingLine 早就写了「一律写作『能言』、不许写拼音」，可现网日文轮还是把品牌
// 写成了「能与」——提示词是**概率性请求**，不是保证（同一族的还有【回复语言】段：
// 见 reply_lang_check.go 文件头那条"命中率不是 0 也不是 1"的实测）。
// 专有名词恰恰是最不能靠请求的一类：客户不会认为"模型少写了一个字"是措辞问题，
// 他会认为**这家公司的名字就叫能与**。
//
// 两条腿，各管一条链：
//  1. normalizeBrandForms —— 挂在**每一条**出站回复上（engine.go Respond）：
//     把已知的错形（拼音 Nengyan／日文字形近错「能与」）按语种档改成正确写法。
//     这一条直接治现网那条「能与」，因为它不依赖模型是否照提示词办。
//  2. protectBrandForTranslation + restoreBrandAfterTranslation —— 挂在**翻译路**上
//     （localize.go translateOnce）：送翻前把品牌写法换成不可译占位符 ⟦BRAND⟧，
//     出栈再按语种还原。翻译模型面对一个占位符不会"顺手意译"，
//     而面对「能言」两个字会（能→与 只差一个笔画，正是小模型最容易翻的车道）。
//     占位符被整块吃掉时**只 WARN、不判失败**（理由见 restoreBrandAfterTranslation：
//     判失败＝访客退回看中文，比少一个品牌自称严重得多），但残渣必须清掉。
//
// ⚠️ 新增错形只从 WARN 里学，别凭想象堆表：这张表每一条都要有实证形态，
// 收得越宽，把正常文案当成品牌错形改掉的风险越大（误改客户正文比留一个错字严重）。
// =============================================
package engine

import (
	"context"
	"regexp"
	"strings"

	"translator/internal/observability"
)

// brandToken 送翻译途中替代品牌名的不可译占位符。
// 用全角方括号＋大写英文而不是 ⟪…⟫/{{…}}：小模型会把双花括号当模板语法吃掉或原样留下花括号，
// 而「⟦BRAND⟧」这种在训练语料里几乎不出现的形状，要么原样保留、要么整块丢失，
// 两种都落在本文件的可判分支里（原样保留→还原；整块丢失→判失败出原文）。
const brandToken = "⟦BRAND⟧"

// brandSourceForms 源文里会出现的品牌写法（送翻前都要换成占位符）。
// 两份都收是因为中文素材与英文素材都可能自带品牌名（知识条目里两种都见过），
// 漏一份就是"这一份还得靠模型别翻坏"——那正是本文件要消灭的状态。
var brandSourceForms = []string{"LangCross", "能言"}

// brandPinyinForms 拼音错形（用户 082x 指令第一条点名的形态：「品牌英文名叫 LangCross，不叫 nengyan」）。
// 大小写各列一条而不是靠 EqualFold：出栈替换要按字面命中，
// EqualFold 会把 "nengyang"（能扬的拼音）这类正常罗马字一起吃掉。
var brandPinyinForms = []string{"Nengyan", "NengYan", "NENGYAN", "nengyan"}

// brandJaMisForm 日文轮的字形近错（★ 092x 现网实证：「能言」被写成「能与」）。
// 言／与 在手写体与语料里都近形，是小模型最典型的一类翻车。
const brandJaMisForm = "能与"

// brandCJKLocalesForFix 允许做「能与→能言」纠正的语种档。
// **刻意不含 zh／zh_hant**：中文正文里「能与」完全可能是正常说法（「能与你」「能够与之」），
// 在中文轮做这个替换就是误改客户正文；而日文正文里「能与」不是词，只能是品牌错形。
var brandCJKLocalesForFix = map[string]bool{"ja": true}

// brandTokenRuleLine 占位符规则那一句（只在源文真含品牌名时才拼，白话源文不提它）。
// 单独抽成函数是为了让「这句会不会出现」本身可断言：源文没有品牌名时提示词里不该冒出 ⟦BRAND⟧，
// 否则模型会以为要它自己补一个占位符进正文（那才是把品牌名弄没的第二种方式）。
func brandTokenRuleLine(hasBrand bool) string {
	if !hasBrand {
		return ""
	}
	return "文本里的 " + brandToken + " 是品牌名的占位符：" +
		"原样保留这七个字符，不许翻译、不许改写、不许删掉、也不许再插入第二个。\n"
}

// normalizeBrandForms 把出站正文里的已知品牌错形改成该语种档写法，返回「改后的正文 + 改了几处」。
// 判据全部按字面命中，不做模糊匹配：宁可漏改（还有 WARN 那条观测腿攒证据），不可误改。
func normalizeBrandForms(text, answerLang string) (string, int) {
	if text == "" {
		return text, 0
	}
	want := brandNameFor(answerLang)
	n := 0
	out := text
	// ① 拼音错形：任何语种档都改（拼音在哪一档都不是词）
	for _, bad := range brandPinyinForms {
		if strings.Contains(out, bad) {
			out = strings.ReplaceAll(out, bad, want)
			n++
		}
	}
	// ② 日文档的字形近错：只在日文轮改（见 brandCJKLocalesForFix 为什么排除中文轮）
	if brandCJKLocalesForFix[canonicalLang(answerLang)] && want == "能言" && strings.Contains(out, brandJaMisForm) {
		out = strings.ReplaceAll(out, brandJaMisForm, want)
		n++
	}
	// ③ 汉字档却留着 LangCross？不改。
	//    「标题写能言、正文写 LangCross」确实是缺陷，但那一档由 brandNamingLine 管着，
	//    而 LangCross 在日文正文里也可能是访客自己提的英文名——把别人给的词改掉就不是卫生了。
	return out, n
}

// protectBrandForTranslation 送翻译前把源文里的品牌写法换成占位符；返回「换后的文本 + 是否换到过」。
func protectBrandForTranslation(text string) (string, bool) {
	hit := false
	out := text
	for _, f := range brandSourceForms {
		if strings.Contains(out, f) {
			out = strings.ReplaceAll(out, f, brandToken)
			hit = true
		}
	}
	return out, hit
}

// restoreBrandAfterTranslation 译文出栈时还原品牌名。
// 三条分支的优先级是刻意的：占位符在→照它还原（最强证据）；
// 占位符不在但正文里还剩某种品牌形态→按语种档归一（模型没留占位符但没把名字丢了）；
// 一个痕迹都没有→**只记 WARN、照发译文**（第三种见 stripBrandTokenResidue：占位符被改坏时残渣绝不能发给客户）。
//
// ★ 为什么"整块丢失"不判翻译失败（这条是我上一版写重了，逐条想过才翻档）：
// 失败的后果是 fail-soft 出**中文原文**——英文访客的欢迎词从此整段变中文，
// 那是把「语言保证」这条更重的保证打回原形（082x 那批修的就是它）。
// 而问候语里少个自称品牌名只是 cosmetics：客户仍然看到一句自己语言的、内容正确的回答。
// 两害相权：品牌缺失走 WARN 攒证据（真在现网高频出现，再由下一批把提示词那句加固），
// 不许用"客户退回看中文"来换。
func restoreBrandAfterTranslation(text, uiLang string) (string, bool) {
	if text == "" {
		return "", false
	}
	if strings.Contains(text, brandToken) {
		return strings.ReplaceAll(text, brandToken, brandNameFor(uiLang)), true
	}
	for _, f := range brandSourceForms {
		if strings.Contains(text, f) {
			fixed, _ := normalizeBrandForms(text, uiLang)
			return fixed, true
		}
	}
	for _, p := range brandPinyinForms {
		if strings.Contains(text, p) {
			fixed, _ := normalizeBrandForms(text, uiLang)
			return fixed, true
		}
	}
	// 日文档的字形近错同样是"品牌痕迹"：模型没留占位符、把名字翻成了「能与」，
	// 内容还在（不该判失败），但必须当场归一（这正是现网那条的形态）。
	// 只在 brandCJKLocalesForFix 那一档成立——中文档里「能与」是正常词，不能当痕迹。
	if brandCJKLocalesForFix[canonicalLang(uiLang)] && strings.Contains(text, brandJaMisForm) {
		fixed, _ := normalizeBrandForms(text, uiLang)
		return fixed, true
	}
	return text, false
}

// stripBrandTokenResidue 剥掉译文里被模型改坏的品牌占位符残渣（⟦BRAND⟧／[[BRAND]]／⟦brand⟧ 这类）。
//
// 为什么单独一条腿：占位符是**我们塞进去的**记号，它在客户屏幕上以任何形态出现都是内部机制外露
// （同【go:key】控制序列必须摘干净那条）。模型大概率是把它改坏了而不是原样留着，
// 所以还原那一步命中不了它时，这里兜底删掉——宁可那句没有品牌名，也不许露一串方括号。
func stripBrandTokenResidue(text string) string {
	if !strings.Contains(strings.ToUpper(text), "BRAND") {
		return text
	}
	return brandTokenResiduePat.ReplaceAllString(text, "")
}

// brandTokenResiduePat 方括号/书名号包住 BRAND（大小写不限）的残渣形态。
// 只删「括号里就是这个词」的段，不碰其它括号内容（那是客户要看的补充说明）。
var brandTokenResiduePat = regexp.MustCompile(`[⟦［\[【〔]{1,2}\s*BRAND\s*[⟧］\]】〕]{1,2}`)

// guardReplyBrand 出站咽喉上的品牌归一（每一条回复都过，与是否经过翻译无关）。
// 改动计数>0 才记 WARN：这条链的发生率必须看得见——如果哪天错形从「能与」换成别的字形，
// WARN 里带着原文，下一批才知道要补哪一条，而不是等第五次用户截图。
//
// 只改品牌形态，不动其它任何字：替换的候选串全是"品牌名的写法"这一族
// （拼音／能与），它们在正常客服话术里不成立（见上面逐条注释），
// 所以这条腿的误伤半径是"品牌名本身"，不会吃掉答案。
func (e *Engine) guardReplyBrand(ctx context.Context, answerLang string, rep *Reply) *Reply {
	if rep == nil || rep.Content == "" {
		return rep
	}
	fixed, n := normalizeBrandForms(rep.Content, answerLang)
	if n == 0 || fixed == rep.Content {
		return rep
	}
	observability.Warn(ctx, "assist.engine 品牌名错形已按语种档归一（词表外的新错形请从这里补）",
		"lang", canonicalLang(answerLang), "source", rep.Source, "fixes", n,
		"want", brandNameFor(answerLang), "before", firstRunes(rep.Content, 200))
	rep.Content = fixed
	return rep
}

// firstRunes 取前 n 个字符（日志里带原文要用它：按字节切会把中文切成半个 rune）。
func firstRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
