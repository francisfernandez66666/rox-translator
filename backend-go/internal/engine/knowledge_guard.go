// ============ knowledge_guard.go · 职责说明 ============
// 一期《确定的多语言能力》§7 知识层答案的出站判据（**唯一实现，两侧共用**）。
//
// ★ 位置纪律（施工文档 §7.1 审阅修正）：RenderSafeTranslations 在
//
//	internal/engine/translation_guard.go:53（导出），deliveryTranslations 在 :735
//	（小写未导出、跨包不可调）；internal/assist/engine 里这两个符号都不存在。
//	⇒ 知识层答案的判据只能住在 internal/engine，由 api 与 assist 两个调用方共用；
//	  **禁止**在 assist 包再抄一份（那是「第二把尺子」，两处词表迟早分家）。
//
// ★ 只判两件事，不改写任何字：
//
//	① 答案里出现的每个数字／认证词，必须能在「已确认事实值」里找到同值来源；
//	② 找不到就把**该句整句丢**（不部分改写、不补写、不交给模型重写）；
//	   丢空则回退到「已备物料原文」，并出 know_stripped_all 分档。
//	合法已审句一字不动（G3 用多句靶子钉住，防「剥空即回退」掩护改写）。
//
// 本文件是**纯函数**：不碰数据库、不碰 ctx、不调模型，因此天然满足 §7.3 的零 LLM 判据。
// ================================================
package engine

import (
	"context"
	"regexp"
	"strings"
)

// knowledgeCtxKey 知识层答案上下文的私有键（跨包只经下面两个导出函数存取）。
type knowledgeCtxKey struct{}

// knowledgeCtx 一次知识层问答的出站校验素材。
type knowledgeCtx struct {
	facts    []string // 已确认（status='confirmed'）事实值
	fallback string   // 已备物料原文
}

// WithKnowledgeFacts 向 ctx 注入知识层答案的校验素材，供出站咽喉处的守卫消费。
// ★ 未注入 ⇒ 守卫原样放行（所有既有客服/翻译链零改动，与 E1 老租户等值同族纪律）。
// 参数 ctx=上下文；facts=已确认事实值；fallback=已备物料原文；返回派生 ctx。
func WithKnowledgeFacts(ctx context.Context, facts []string, fallback string) context.Context {
	return context.WithValue(ctx, knowledgeCtxKey{}, knowledgeCtx{facts: facts, fallback: fallback})
}

// KnowledgeFactsFromContext 取知识层校验素材；未注入返回 ok=false。
// 参数 ctx=上下文；返回事实值、兜底原文、是否注入。
func KnowledgeFactsFromContext(ctx context.Context) ([]string, string, bool) {
	v, ok := ctx.Value(knowledgeCtxKey{}).(knowledgeCtx)
	if !ok {
		return nil, "", false
	}
	return v.facts, v.fallback, true
}

// 知识层答案分档名（对外契约：出栈与审计共用同一组字面）。
const (
	KnowReasonOK        = "" // ok 出口刻意回空串：非空即表示「被剥过」，调用方只需判一个字段
	KnowUnsourcedNumber = "know_unsourced_number"
	KnowUnsourcedCert   = "know_unsourced_cert"
	KnowStrippedAll     = "know_stripped_all"
)

// knowNumberRe 答案里的阿拉伯数字（含小数）。型号里的数字同样受审——
// 「A4 纸」与「A7 纸」只差一位，编造型号与编造报价是同一族事故。
var knowNumberRe = regexp.MustCompile(`\d+(?:\.\d+)?`)

// knowCertRe 认证／合规声明词表。★ 与 §5.6 的「cert 归受控档」同族：
// 发错一句认证的责任重于少发一句，因此认证词一律要求 confirmed 事实来源。
var knowCertRe = regexp.MustCompile(`(?i)\b(CE|FDA|ISO\s?\d{4,5}|UL|RoHS|CCC|SGS|REACH|FCC|KC|PSE|UKCA|GS|TUV|TÜV)\b`)

// knowSentenceSep 句子边界（中英文句读与换行）；切分时保留分隔符本身。
var knowSentenceSep = regexp.MustCompile(`[。！？!?\n；;]`)

// GuardKnowledgeAnswer 校验知识层答案：无来源数字／认证词整句丢，丢空回退已备物料原文。
// 参数 text=待出栈答案；confirmedFacts=已确认（status='confirmed'）事实值集合；
// fallback=已备物料原文（可为空）；返回出栈文本与分档名（KnowReasonOK 表示一字未动）。
func GuardKnowledgeAnswer(text string, confirmedFacts []string, fallback string) (string, string) {
	if strings.TrimSpace(text) == "" {
		return text, KnowReasonOK
	}
	// 来源集合归一：事实值逐条小写去空白，比对用「包含」而不是等值——
	// 事实值 "12V/24V" 应能为答案里的 "12V" 背书，但不能反过来给 "99V" 背书。
	src := make([]string, 0, len(confirmedFacts))
	for _, f := range confirmedFacts {
		if v := strings.ToLower(strings.TrimSpace(f)); v != "" {
			src = append(src, v)
		}
	}
	hasSource := func(token string) bool {
		t := strings.ToLower(strings.TrimSpace(token))
		if t == "" {
			return true
		}
		for _, v := range src {
			if strings.Contains(v, t) {
				return true
			}
		}
		return false
	}

	kept := strings.Builder{}
	reason := KnowReasonOK
	stripped := 0
	// 逐句切分（保留分隔符），只对「数字／认证词」判来源，句子其余部分一个字都不改。
	start := 0
	locs := knowSentenceSep.FindAllStringIndex(text, -1)
	segments := make([]string, 0, len(locs)+1)
	for _, l := range locs {
		segments = append(segments, text[start:l[1]])
		start = l[1]
	}
	if start < len(text) {
		segments = append(segments, text[start:])
	}
	for _, seg := range segments {
		bad := ""
		for _, m := range knowCertRe.FindAllString(seg, -1) {
			if !hasSource(m) {
				bad = KnowUnsourcedCert
				break
			}
		}
		if bad == "" {
			for _, m := range knowNumberRe.FindAllString(seg, -1) {
				// 数字要在**全句**里找来源（事实值可能带单位、带范围），但绝不允许
				// 用另一个句子的数字给本句背书：来源比对按句子做，出处才追得回。
				if !hasSource(m) {
					bad = KnowUnsourcedNumber
					break
				}
			}
		}
		if bad != "" {
			stripped++
			reason = bad
			continue
		}
		kept.WriteString(seg)
	}
	if stripped == 0 {
		return text, KnowReasonOK
	}
	out := kept.String()
	if strings.TrimSpace(out) == "" {
		return fallback, KnowStrippedAll
	}
	return out, reason
}
