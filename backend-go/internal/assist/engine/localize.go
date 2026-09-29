// ============ localize.go · 职责说明 ============
// 挂件「非中文访客看到的中文 canned 文案」的翻译层：欢迎词 + 快捷提问 chips。
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
//   - **可被运营看见并改**：键名 i18n:welcome:<lang>，值是「源文指纹 + 换行 + 译文」。
//     机翻不满意，直接在管理台把这段改掉即可——改完后指纹不匹配会被重新翻译覆盖吗？
//     不会：见 localize 的放行分支，人工改过的译文（指纹后面还跟着 "!manual" 标记）永久保留。
//     这条是刻意的：运营修订比机翻质量高，自动覆盖等于把人家改的东西吃回去。
//
// 失败口径照【系统现值】那条硬规矩来：**取不到就原样出中文，绝不编一份译文**。
// 中文原文难看，但它是真话；机翻失败时返回半句假译文是事故。
// =============================================
package engine

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"strings"

	"translator/internal/assist/llm"
	"translator/internal/observability"
)

// localizeMaxTokens 译文一次生成的额度上限。欢迎词+chips 合起来也就两三行，
// 但主模型是思维链模型（GLM-Z1 一类，见 engine.go 074x 那条注释），
// 思维链要吃掉一部分额度，400 是「正文两行 + 链子」的实测安全档，不是随手取的。
const localizeMaxTokens = 400

// localizeTemperature 翻译用的温度取 0.2（对话用 0.7/1.0）。
// 翻译要的是照抄语义，不要发挥；这条和「温度不是音色旋钮」（080x）是同一件事的另一面——
// 温度管不了语气，但管得住改写幅度。
const localizeTemperature = 0.2

// manualMark 人工改过的译文在指纹后追加这个标记，localize 见到就永久放行不再重翻。
const manualMark = "!manual"

// srcFingerprint 源文本指纹（sha1 前 12 位）。
// 为什么不用整串 sha1 也不用品内容本身：缓存键要短到能一眼读出来（管理台列 configs 时不糊屏），
// 又要长到不会撞——12 个 hex 字符 = 48 bit，在「一个运营改十几次欢迎词」的量级上够用。
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
	fp := srcFingerprint(text)
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
	prompt := "把下面这段产品欢迎语/短问句翻译成 " + label +
		"，用于网站右下角的 AI 客服挂件。要求：保持原意与行数，一行输入对应一行输出，" +
		"不要加解释、不要加引号、不要输出思考过程。\n品牌名口径（与对话回复同一张表，见 brandNameFor）：" +
		"本轮界面语言为 " + label + "，品牌名一律写作「" + brandNameFor(uiLang) + "」，" +
		"任何情况下都不许写成 Nengyan、NengYan 之类拼音。\n\n---\n" + text + "\n---"
	out, _, _, err := client.Chat(ctx, localizeTemperature, localizeMaxTokens,
		[]llm.Message{{Role: "user", Content: prompt}})
	out = cleanTranslated(out)
	if err != nil || out == "" {
		// 失败原样出中文（见文件头「绝不编一份译文」）。这一行日志是这条软路径唯一的露面机会：
		// greet 界面看不出「没翻成」，没有它就只能等访客截图来报。
		observability.Warn(ctx, "assist.engine canned 文案翻译失败，原样出中文",
			"kind", kind, "lang", uiLang, "err", err)
		return text
	}
	_ = e.db.SetConfig(key, fp+"\n"+out)
	return out
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
