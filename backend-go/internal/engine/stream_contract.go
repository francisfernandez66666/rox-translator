// ============ 本文件职责中文说明 ============
// DEF-P1-04（2026-11-01 云端 UAT 发布前复核）：**delta 流把输出契约标签 <t>…</t> 直接投给客户屏幕**。
//
// 现网形态（实测帧序）：上游第一帧 = `<t`、第二帧 = `>This`、整段没有闭合标签 ⇒
// 最终腿（done.reply）干净而流式（delta）脏。原因在两条腿的处理不对称：
//
//	· 最终腿：singleLangRaw 返回前调 PostProcessTranslation（extract → stripPseudoTags）；
//	· delta 腿：engine.go 把上游 token 原样交给 sink，api/stream.go 原样写成 delta 帧——**一个字都没洗**。
//
// ⇒ 本文件只补 delta 这一侧（不动最终腿，也不在 done 时整篇重译：重译＝制造第三份文本，正是 DEF-P1-05 的病症）。
//
// 尺子＝ extractContractTranslation + stripPseudoTags 这两层的**流式等价物**；
// ⚠️ 射程只有这两层：术语替换／去中文／尾段截断／品牌归一／审校改写**都不在 delta 上做**
// （前四类维持现状，审校改写在 pro 档永远不进 delta，见施工方案 §B2-0 结论）。
// =============================================
package engine

import "strings"

// ContractFilter 流式输出契约过滤器：只把 <t>…</t> 内的正文吐给下游，
// 标签本身与标签外的注释块丢弃；从未出现过契约的流原样透传（与 extract 的"违约即原样返回"同口径）。
//
// ★ 实例必须**每次上游调用各建一个**（装配点见 singleLangRaw）：finish_reason=length 或空内容会
// 递归重试，共享实例会把上一轮的 inside 状态带进新一轮 ⇒ 整轮正文被吞掉——比现状更坏的静默形态。
type ContractFilter struct {
	in     string // 尚未处理的输入
	cand   string // 正在判定的标签候选（一定以 '<' 开头；判定完即清空）
	inside bool   // 是否处于 <t>…</t> 内
	// sawBlock ＝ 进过契约块。它决定「块外文本」的处置：
	// 一次都没进过块（模型违约不给标签）⇒ 整段原样透传；进过块 ⇒ 块外的是注释，丢弃。
	sawBlock bool
	// blockBody ＝ 当前块已吐出正文。用于剥块首空白（extract 对每个块做 TrimSpace）。
	blockBody bool
	// hadBody  ＝ 上一个块吐过正文。用于多块之间补 "\n"（extract 用 strings.Join(parts,"\n")）。
	hadBody bool
	pendSep bool   // 下一块的首个正文字符前需要补一个换行
	holdWS  string // 块尾空白先扣着：闭合时才判得出它是不是要 TrimSpace 掉的那一段
}

// NewContractFilter 新建一个过滤器实例（一次上游流式调用一份，禁止包级共享）。
func NewContractFilter() *ContractFilter { return &ContractFilter{} }

// Push 喂进上游一个 token，返回"这一拍可以吐给客户的正文增量"（可能为空串＝还在判定标签）。
func (f *ContractFilter) Push(chunk string) string {
	f.in = f.holdWS + f.in + chunk
	f.holdWS = ""
	return f.process(false)
}

// Flush 流结束：把缓冲里剩下的东西结算掉。
// ★ 边界①：只吐了 `<t>` 一直没吐 `</t>`（实测就是这个形态）⇒ inside 残留**全部透传**，
//
//	绝不因为"没闭合"就丢正文——丢了比现状更坏。
func (f *ContractFilter) Flush() string {
	f.in = f.holdWS + f.in
	f.holdWS = ""
	out := f.process(true)
	// 流结束仍在 inside：残留按正文出栈（同上①）
	if f.inside && f.in != "" {
		out += f.emitBody(f.in, true)
		f.in = ""
	} else if f.in != "" {
		out += f.emitOutside(f.in)
		f.in = ""
	}
	return out
}

// process 状态机主循环：每轮要么消费一段文本、要么判定一个标签，直到输入用尽。
func (f *ContractFilter) process(final bool) string {
	var out strings.Builder
	for {
		// 1) 先结算在途候选（上一拍没收口的标签）
		if f.cand != "" {
			d, ok := f.classify(f.cand, final)
			if !ok {
				// 还需要更多字节才能判定：候选扣住，正文不许越过它
				if len(f.in) == 0 {
					return out.String()
				}
				f.cand += f.in[:1]
				f.in = f.in[1:]
				continue
			}
			out.WriteString(f.apply(d, &out))
			continue
		}
		// 2) 没有候选：找下一个 '<'
		if f.in == "" {
			return out.String()
		}
		i := strings.IndexByte(f.in, '<')
		if i < 0 {
			if f.inside {
				out.WriteString(f.emitBody(f.in, final))
			} else {
				out.WriteString(f.emitOutside(f.in))
			}
			f.in = ""
			return out.String()
		}
		head := f.in[:i]
		if head != "" {
			if f.inside {
				out.WriteString(f.emitBody(head, final))
			} else {
				out.WriteString(f.emitOutside(head))
			}
		}
		f.in = f.in[i:]
		f.cand = "<"
		f.in = f.in[1:]
	}
}

// tagKind 标签判定结果。
type tagKind int

// 四档判定结果（顺序即优先级；tkText 是兜底档＝"这不是标签，照正文走"）。
const (
	tkOpen  tagKind = iota // 契约开标签（<t>／<t …>／走形的 <t+正文）
	tkClose                // 契约闭标签（</t>／走形的 </t+正文）
	tkDrop                 // 白名单里别的伪标签（<tt>／<target>…）：与最终腿 stripPseudoTags 同处置＝丢弃
	tkText                 // 不是标签（<div>／正文里的裸 <）：原样透传
)

// tagDecision 判定结论：消费多少字节、属于哪一类。
type tagDecision struct {
	kind     tagKind
	consumed int  // 从候选里吃掉的字节数
	rest     bool // consumed < len(cand) 时，剩下的字节退回输入继续处理
}

// classify 判定一个以 '<' 开头的候选。ok=false 表示"还判不出，需要更多字节"。
//
// ★ 边界③：`<t` 会被拆在两个 chunk 里 ⇒ 判定必须跨 chunk；
// ★ 边界（首字延迟）：判定只吃标签本身的字节，正文一个字都不许因为"等标签收口"而积压
//
//	（extract 会丢掉 <t> 之前的前置正文，而流式吐出去收不回——这是设计内的分叉，见 stream_contract_test.go 的分叉表）。
func (f *ContractFilter) classify(cand string, final bool) (tagDecision, bool) {
	slash := false
	i := 1
	if i < len(cand) && cand[i] == '/' {
		slash = true
		i++
	}
	j := i
	for j < len(cand) && isASCIILetter(cand[j]) {
		j++
	}
	run := strings.ToLower(cand[i:j])
	// 词元仍在延长且仍可能是某个白名单词元 ⇒ 等更多字节（除非流已结束）
	if j == len(cand) && pseudoNameHasPrefix(run) {
		if !final {
			return tagDecision{}, false
		}
		// 流结束：按"最长精确词元"结算（<t 独占结尾＝走形开标签，正文没有 ⇒ 只丢标签）
		return f.settleByLongestName(cand, i, slash, run, false), true
	}
	if j == len(cand) {
		// 词元已不可能是任何白名单名（如 <divx）→ 这不是标签
		return f.settleByLongestName(cand, i, slash, run, false), true
	}
	next := cand[j]
	if next == '>' {
		// 完整标签：先用契约正则，再用伪标签白名单，最后按正文
		tag := cand[:j+1]
		if contractOpenRe.MatchString(tag) {
			return tagDecision{kind: tkOpen, consumed: j + 1}, true
		}
		if contractCloseRe.MatchString(tag) {
			return tagDecision{kind: tkClose, consumed: j + 1}, true
		}
		if pseudoTagRe.MatchString(tag) {
			return tagDecision{kind: tkDrop, consumed: j + 1}, true
		}
		// 走形的最长名（<ta> 这类）仍按 <t 处理；彻底无关的（<div>）原样透传
		if d := f.settleByLongestName(cand, i, slash, run, false); d.kind != tkText {
			return d, true
		}
		return tagDecision{kind: tkText, consumed: 1}, true
	}
	if isASCIIWordByte(next) {
		// 词元后紧跟字母数字（\b 不成立）：模型把标签和正文黏在一起（实测 <tHello</t>）
		return f.settleByLongestName(cand, i, slash, run, false), true
	}
	// 词元后是空格/属性字符（<t lang="zh">）：取到 '>' 才算完整；等不到（或超预算）按走形结算
	k := strings.IndexByte(cand[j+1:], '>')
	if k < 0 {
		if !final && len(cand) < contractAttrCap {
			return tagDecision{}, false
		}
		if final {
			// 流在属性段中间结束：这一串**全是标签字节**（属性后面才可能是正文），
			// 整段丢弃比"把 lang=\"zh\" 这样的残渣吐到客户屏幕上"更对。
			return f.settleByLongestName(cand, i, slash, run, true), true
		}
		// 超预算但流还在：只丢标签名那一段，后面的当正文（宁可少丢，不可把正文吞掉）
		return f.settleByLongestName(cand, i, slash, run, false), true
	}
	tag := cand[:j+1+k+1]
	if contractOpenRe.MatchString(tag) {
		return tagDecision{kind: tkOpen, consumed: len(tag)}, true
	}
	if contractCloseRe.MatchString(tag) {
		return tagDecision{kind: tkClose, consumed: len(tag)}, true
	}
	if pseudoTagRe.MatchString(tag) {
		return tagDecision{kind: tkDrop, consumed: len(tag)}, true
	}
	return tagDecision{kind: tkText, consumed: 1}, true
}

// contractAttrCap 属性段最多等这么多字节（伪标签正则本身只容忍 60；这里给一点余量，
// 超了就不再攒——无限期扣住客户的第一个字比"多吐几个字符"坏得多）。
const contractAttrCap = 200

// settleByLongestName 词元"不干净"时的结算：取 run 里**最长的整词白名单词元**当标签名，
// 标签名之后的字节退回正文（实测形态：<t 后直接跟正文，以及 </t 后跟残渣）。
// 没有任何整词命中（如 <div）时按正文处理——只有契约/伪标签才配被丢弃。
//
// whole=true ⇒ 整段候选都是标签字节（流在属性段中间结束）⇒ 一并丢弃，不把 lang="zh" 吐上屏幕。
func (f *ContractFilter) settleByLongestName(cand string, nameStart int, slash bool, run string, whole bool) tagDecision {
	name, ok := longestPseudoName(run)
	if !ok {
		return tagDecision{kind: tkText, consumed: 1}
	}
	consumed := nameStart + len(name)
	if slash {
		consumed++ // '/'
	}
	if whole {
		consumed = len(cand)
	}
	switch {
	case name == "t":
		if slash {
			return tagDecision{kind: tkClose, consumed: consumed}
		}
		return tagDecision{kind: tkOpen, consumed: consumed}
	default:
		return tagDecision{kind: tkDrop, consumed: consumed}
	}
}

// apply 执行判定：改状态、退回未消费的字节、产出这一拍的正文。
func (f *ContractFilter) apply(d tagDecision, out *strings.Builder) string {
	cand := f.cand
	f.cand = ""
	if d.consumed < len(cand) {
		f.in = cand[d.consumed:] + f.in
	} else if d.consumed > len(cand) {
		d.consumed = len(cand)
	}
	switch d.kind {
	case tkOpen:
		f.inside = true
		f.sawBlock = true
		f.blockBody = false
		f.pendSep = f.hadBody // 边界⑤：上一块已闭合、这一块开始 ⇒ 必须先发一个 \n
		f.settleHoldWS()
		return ""
	case tkClose:
		if f.inside {
			if f.blockBody {
				f.hadBody = true
			} else {
				// 空块（<t></t>）不进 parts：extract 会丢掉它，也不该占一个换行
				f.pendSep = f.hadBody
			}
		}
		f.inside = false
		f.blockBody = false
		f.settleHoldWS()
		return ""
	case tkDrop:
		f.settleHoldWS()
		return ""
	default: // tkText：不是标签
		if f.inside {
			return f.emitBody("<", true)
		}
		return f.emitOutside("<")
	}
}

// settleHoldWS 标签判定完成后，先前扣住的块尾空白不再可能是"块尾"了，补回输入最前面。
func (f *ContractFilter) settleHoldWS() {
	if f.holdWS != "" {
		f.in = f.holdWS + f.in
		f.holdWS = ""
	}
}

// emitOutside 块外文本：没进过契约块 ⇒ 原样透传（模型违约，extract 也原样返回）；
// 进过块 ⇒ 丢弃（那是标签外的注释/术语对照，extract 整段丢掉）。
func (f *ContractFilter) emitOutside(s string) string {
	if f.sawBlock {
		return ""
	}
	return s
}

// emitBody 块内正文：剥块首空白、块尾空白先扣着（等闭合或下一拍再判），
// 本块第一个字符前按 pendSep 补换行（边界⑤）。
func (f *ContractFilter) emitBody(s string, final bool) string {
	if !f.blockBody {
		s = strings.TrimLeft(s, " \t\r\n")
		if s == "" {
			return ""
		}
	}
	held := ""
	if !final {
		if cut := len(strings.TrimRight(s, " \t\r\n")); cut < len(s) {
			held = s[cut:]
			s = s[:cut]
			if s == "" {
				f.holdWS += held
				return ""
			}
		}
	}
	pre := ""
	if f.pendSep {
		pre = "\n"
		f.pendSep = false
	}
	f.blockBody = true
	if held != "" {
		f.holdWS = held
	}
	return pre + s
}

// isASCIILetter 词元扫描只认 ASCII 字母（白名单词元全是字母）。
func isASCIILetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// isASCIIWordByte \b 判定用的"单词字节"（字母/数字/下划线，与 Go 正则 \w 的 ASCII 部分同口径）。
func isASCIIWordByte(b byte) bool {
	return isASCIILetter(b) || (b >= '0' && b <= '9') || b == '_'
}

// pseudoNameList 白名单词元（直接从 pseudoTagName 派生，禁止再抄一份——抄两份迟早打架）。
var pseudoNameList = strings.Split(pseudoTagName, "|")

// pseudoNameHasPrefix run 是否还可能是某个白名单词元的前缀（大小写不敏感）。
func pseudoNameHasPrefix(run string) bool {
	if run == "" {
		return true
	}
	for _, n := range pseudoNameList {
		if strings.HasPrefix(n, run) {
			return true
		}
	}
	return false
}

// longestPseudoName 取 run 的**最长整词**前缀；没有整词命中则 ok=false（＝这不是标签）。
func longestPseudoName(run string) (string, bool) {
	for l := len(run); l >= 1; l-- {
		if pseudoNameExact(run[:l]) {
			return run[:l], true
		}
	}
	return "", false
}

// pseudoNameExact run 是否恰好等于某个白名单词元。
func pseudoNameExact(run string) bool {
	for _, n := range pseudoNameList {
		if n == run {
			return true
		}
	}
	return false
}
