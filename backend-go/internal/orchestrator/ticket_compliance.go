// ============ 本文件职责中文说明 ============
// ★★ 2026-11-03 用户裁决：工单通道接入内容合规闸，处置＝**跳过**（不是整单拒、不是留白）。
//
// 裁决原文两条口径：
//  1. 「文本命中禁词后跳过禁词翻译」——命中那一行**不进模型**（保住上游零暴露），
//     该位置**不交付译文**；其余行照常译完，工单照常完成。
//  2. 「md 文件里写明跳过的禁词」——交付面逐条点名：第几行、哪一语种、命中哪个词。
//
// 为什么必须收在编排层而不是引擎底层原语：
//
//	即时链（engine.HandleText）与文件链（engine/file.go）的**内层也走同一批原语**
//	（TranslateLangsInto → translateLangsConcurrent → singleLangRaw）。把闸下沉到原语层，
//	这两条通道会各过两次 ⇒ 一次命中写两条审计、两条告警（告警中心＋群机器人＋站内信一起灌脏），
//	而且"整段拒译／段级占位／工单跳过"三种交付语义在同一层没法同时成立。
//	⇒ 每条通道在自己的入口各接一次，判据只有词包那一份（Checker.Hits，词边界口径见 §18）。
//
// 计数刻意与既有三档分开（同 §19 族一"三档互斥、混成一味就无从对账"）：
//
//	unresolved/dead/truncated 答的是"想翻但没翻出来"；
//	compliance_skipped 答的是"平台按合规要求决定不翻"——把设计记进失败计数＝
//	把不是缺陷的事给客户看，还会抬起「质检存疑」徽标（§14 音频那批已翻过一次这个案）。
//
// 交付语义取向（一条放宽就造出更坏形态，逐条写在这）：
//
//	· **不留白、不丢行**：跳过位一律按原文出货。留空会让交付行数天生比源文少，
//	  而 qa 的 structure/数字规则按行数与数字差判整单驳回（BUG-01/06 的病文本体）。
//	· **整篇型调用遇到任一命中 ⇒ 整语种跳过**（单段工单，或应急档 text_ticket_per_line=0）：
//	  整篇一次调用的输入**必然含那一行违禁原文**，"只跳那一行"在这个形态里物理上做不到。
//	  取向＝宁可少翻，也不把违禁原文送出去。这条决定让应急档**让位给合规**，写在锁里。
//	· **输出侧不回显词面**：模型自产的违禁译文被丢弃时只点名"第几行"，
//	  回显等于把违禁译文写进客户成品——比不点名更坏。
//	· **词面回显是数据不是猜测**：直接取 Checker.Hits 的原词（最多 5 项，那是检测器
//	  为审计留证定的档，本批不放宽、也不在文案里承诺"全部列出"）。
//	· **fail-open**：词包读不到／引擎未装配 ⇒ 一律照常翻译，绝不在"读不到清单"时
//	  判所有内容违规（§22 那条「拿不到清单＝不知道什么是编造，不是什么都算编造」同一取向）。
//
// ============================================
package orchestrator

import (
	"context"
	"strings"

	"translator/internal/engine"
	"translator/internal/observability"
)

// ticketCompliancePlan 一次执行期的合规判定结果，**每单判一次、语种循环之外**。
//
// 生命周期＝这一次 runAIInitial 的执行期：它**不写进落库载荷**（载荷里落的是
// ComplianceSkipped/ComplianceItems 那两个事实），所以 `recorded` 这个幂等标记
// 天生随执行结束而消失。
// ⚠️ 不许把它做成包级变量或 Workflow 字段：那是跨工单串状态（上一单的"已留痕"会把
// 下一单的真命中吞成零留痕），也是 §三 那条并发纪律点名的形态。
//
// 为什么必须在语种循环之外（同 §23 那条领域词表的现网教训）：判定只吃源文，与目标语种无关；
// 放进语种循环＝同一份源文按语种重复判 N 次，除了白烧 CPU 没有第二个收益，
// 而留痕会跟着变成每语种一条——「一次命中恰一条审计」那条锁正是为此而设。
type ticketCompliancePlan struct {
	// hitsByLine：源文行号（1 起）→ 命中词面。只收**有命中的行**，其余行缺席。
	hitsByLine map[int][]string
	// recordedCh：**每个成因档各一条**留痕的幂等标记（channel → 是否已记）。
	// 为什么按 channel 切而不是整单一条：输入侧命中与模型自产违禁是**两族成因**
	// （处置不同：前者改源文措辞、后者查模型/换档），混成一条就把其中一族藏了；
	// 而同一族内无论涉及几行、几个语种，都只记一条（那才是"翻倍灌脏告警"要防的形态）。
	recordedCh map[string]bool
}

// blocked 某一源文行是否被输入侧命中（1 起行号）。
func (pl *ticketCompliancePlan) blocked(line1 int) bool {
	if pl == nil {
		return false
	}
	_, ok := pl.hitsByLine[line1]
	return ok
}

// any 是否存在输入侧命中行。
func (pl *ticketCompliancePlan) any() bool {
	return pl != nil && len(pl.hitsByLine) > 0
}

// sampleTerms 取一组命中词给留痕文案用（只为让人认出"是哪一族词触发的"，不是全量清单）。
func (pl *ticketCompliancePlan) sampleTerms() []string {
	if pl == nil {
		return nil
	}
	for _, t := range pl.hitsByLine {
		return t
	}
	return nil
}

// planTicketCompliance 对源文逐行判定禁词命中。
//
// 返回值约定（这条约定本身是判据的一部分）：
//   - **闸门未开／引擎未装配 ⇒ nil**：调用方据此完全跳过本特性，
//     关档必须与"没有这个特性"逐字等价（§22 表头上下文那条不变式；
//     否则上线当天所有历史工单的载荷与交付面都会多出一节空话）。
//   - 闸门开着 ⇒ **恒回非 nil**（哪怕零命中）。零命中回的是 `len(hitsByLine)==0` 的计划，
//     这样输出侧那一腿（吃的是译文，与源文计划无关）也能挂同一个执行期幂等标记，
//     不必为它再造第二个"每单一次"的状态。
func (w *Workflow) planTicketCompliance(p *ticketPayload) *ticketCompliancePlan {
	if w == nil || w.Engine == nil || p == nil {
		return nil
	}
	if !w.Engine.TicketSensitiveEnabled() {
		return nil
	}
	pl := &ticketCompliancePlan{hitsByLine: map[int][]string{}, recordedCh: map[string]bool{}}
	for i, ln := range splitLines(p.SourceText) {
		if strings.TrimSpace(ln) == "" {
			continue // 空行没有可译内容，也不进点名清单
		}
		if hits := w.Engine.TicketSensitiveHits(ln); len(hits) > 0 {
			pl.hitsByLine[i+1] = hits // 行号 1 起，与交付面「未译出内容」那一节同口径
		}
	}
	return pl
}

// recordOnce 留痕：**每个成因档每次执行至多一条**（审计＋告警各一条）。
//
// 为什么按 channel 切、而不是整单只记一条：输入侧命中与"模型自产违禁译文"是**两族成因**，
// 处置完全不同（前者改源文措辞或改词包、后者查模型/换档），混成一条就把其中一族藏起来
// （§19 族一「三档混成一味，读的人无从对账」是同一条病）。
// 而同一族内无论涉及几行、几个语种、几批调用，都**只记一条**——
// 逐语种各刷一条才是"一次命中被刷成 N 条告警"那个要防的形态。
//
// ⚠️ 只在真跳过/真撤下之后调用：没跳过还留痕＝读数给失败背书（§一·3 那条 `from` 档位同族）。
func (w *Workflow) recordOnce(ctx context.Context, pl *ticketCompliancePlan, channel string, hits []string, snippet string) {
	if pl == nil || pl.recordedCh[channel] {
		return
	}
	pl.recordedCh[channel] = true
	w.Engine.TicketSensitiveRecord(ctx, channel, hits, snippet)
}

// skipOneLang 把一个语种里被合规跳过的若干行落成最终交付形态：
// 该位按原文出货，计数与点名素材另记，**其余行一字不动**。
//
// 行号不齐时整段回退成原文，**不按下标猜行**（§19 第五族那条"编辑器不许退回按行号猜段"
// 的同一条纪律——猜错行的代价是把译文配到错误的原文上，比整段回退坏得多）。
// 返回本次为该语种跳过的处数。
func (w *Workflow) skipOneLang(p *ticketPayload, srcLines []string, lc string, blocked map[int][]string, side string) int {
	if len(blocked) == 0 {
		return 0
	}
	outLines := splitLines(p.Translations[lc])
	if len(outLines) != len(srcLines) {
		outLines = append([]string(nil), srcLines...)
	}
	if p.ComplianceSkipped == nil {
		p.ComplianceSkipped = map[string]int{}
	}
	n := 0
	for line1, terms := range blocked {
		idx := line1 - 1
		if idx < 0 || idx >= len(srcLines) || idx >= len(outLines) {
			continue
		}
		outLines[idx] = srcLines[idx] // 按原文出货：不留白、不丢行
		item := ComplianceItem{Lang: lc, Line: line1, Side: side, Lead: engine.SensitiveSkippedLead}
		if side == ComplianceSideInput {
			item.Terms = terms // 用户令：写明跳过的禁词（仅输入侧回显词面）
		}
		p.ComplianceItems = append(p.ComplianceItems, item)
		n++
	}
	if n == 0 {
		return 0
	}
	p.ComplianceSkipped[lc] += n
	p.Translations[lc] = composeLineOutput(outLines)
	return n
}

// markComplianceOutputSource 输出侧撤下译文之后修正来源档（整篇型腿共用）。
//
// 为什么必须改：`TranslateLangsInto` 在调用前会把这一语种写成 `model`，而撤下之后
// 客户手里那张成品里已经没有这几行模型译文了。继续写 `model` 就是 AGENTS §19 那条
// **「回填过原文就不许再写 model」**（读数为失败背书）——来源列答的是"这一语种的译文哪来的"。
//
// 两档判据（与逐行腿同一把尺子，两处共用 `reverted >= srcTotal` 这一条）：
//
//	reverted >= srcTotal ⇒ 整语种交付位全是原文 ⇒ compliance_skipped
//	否则（部分撤下）   ⇒ model_with_source_fallback（"含按原文回落的行"正是这个档的定义）
func markComplianceOutputSource(p *ticketPayload, lc string, reverted, srcTotal int) {
	if p == nil || reverted <= 0 {
		return
	}
	if p.Sources == nil {
		p.Sources = map[string]string{}
	}
	if srcTotal > 0 && reverted >= srcTotal {
		p.Sources[lc] = SourceComplianceSkipped
		return
	}
	p.Sources[lc] = SourceModelWithFallback
}

// skipLangsWholeTextByCompliance 整篇型调用（单段工单／应急档 text_ticket_per_line=0／驳回重翻）
// 遇到任一输入侧命中 ⇒ 对这些语种**整语种跳过**：一次模型都不拨，交付位是原文，逐行点名。
//
// 返回 true＝这批语种已被合规全部接管，**调用方必须不要再发起整篇模型调用**。
// 这是"宁可少翻，也不把违禁原文送出去"的取舍——整篇一次调用的输入物理上无法排除那一行。
func (w *Workflow) skipLangsWholeTextByCompliance(ctx context.Context, p *ticketPayload, langs []string, pl *ticketCompliancePlan) bool {
	if !pl.any() || len(langs) == 0 {
		return false
	}
	srcLines := splitLines(p.SourceText)
	total := 0
	for _, lc := range langs {
		total += w.skipOneLang(p, srcLines, lc, pl.hitsByLine, ComplianceSideInput)
	}
	if total == 0 {
		return false
	}
	// 来源档：整语种零模型调用、交付位是原文 ⇒ 必须落**第六档**。
	// 写 model＝给失败背书；写 upstream_failed＝谎报上游断了，会把值班支去查一条完全正常的上游。
	// ⚠️ KB 命中的语种保持 kb：那一句是知识库直配的既有成品，不该被合规档盖掉（同 §19 那条
	//    "KB 命中且本轮零模型调用 ⇒ 保持 kb"）。
	for _, lc := range langs {
		if p.Sources == nil {
			p.Sources = map[string]string{}
		}
		if p.Sources[lc] == SourceKB {
			continue
		}
		p.Sources[lc] = SourceComplianceSkipped
	}
	w.recordOnce(ctx, pl, "ticket_input", pl.sampleTerms(), "工单整篇腿按合规跳过")
	observability.Warn(ctx, "文本工单整篇腿命中禁词，整语种跳过模型调用并按原文交付",
		"langs", strings.Join(langs, ","), "skipped_lines", len(pl.hitsByLine))
	return true
}

// sweepComplianceOnOutput 输出侧兑底：已交付的译文行里若有模型自产的违禁内容 ⇒
// 撤下该行的译文、换成原文，并点名"第几行被撤下"（**不回显词面**）。
//
// 输入侧已跳过的行必须排除在外（`pl.hitsByLine`）：那一行的交付位本来就是原文，
// 再判一次会把同一行记进两个成因档——§19 那条「两档同计会让三个数加起来大于总行数，
// 读的人无从对账」说的就是这个。
func (w *Workflow) sweepComplianceOnOutput(ctx context.Context, p *ticketPayload, srcLines []string, lc string, pl *ticketCompliancePlan) int {
	if pl == nil || !w.Engine.TicketSensitiveEnabled() {
		return 0
	}
	trLines := splitLines(p.Translations[lc])
	if len(trLines) != len(srcLines) {
		return 0 // 行数不齐不做行级撤下（同上：不按下标猜行）
	}
	hits := map[int][]string{}
	for i, tr := range trLines {
		if strings.TrimSpace(tr) == "" || pl.blocked(i+1) {
			continue
		}
		if h := w.Engine.TicketSensitiveHits(tr); len(h) > 0 {
			hits[i+1] = h
		}
	}
	if len(hits) == 0 {
		return 0
	}
	if p.ComplianceSkipped == nil {
		p.ComplianceSkipped = map[string]int{}
	}
	n := 0
	for line1 := range hits {
		idx := line1 - 1
		trLines[idx] = srcLines[idx] // 撤下违禁译文、按原文出货（不留白不丢行）
		p.ComplianceItems = append(p.ComplianceItems,
			ComplianceItem{Lang: lc, Line: line1, Side: ComplianceSideOutput, Lead: engine.SensitiveSkippedLead})
		n++
	}
	p.ComplianceSkipped[lc] += n
	p.Translations[lc] = composeLineOutput(trLines)
	var sample []string
	for _, h := range hits {
		sample = h
		break
	}
	w.recordOnce(ctx, pl, "ticket_output", sample, "模型产出被合规撤下")
	observability.Warn(ctx, "文本工单译文出栈命中禁词，该译文已撤下并按原文交付",
		"lang", lc, "dropped_lines", n)
	return n
}

// sweepComplianceOnOutputWhole 整篇型腿（整篇翻译／驳回重翻）的输出侧兑底。
//
// 为什么不能直接复用逐行版：那一条要求「译文行数 == 源文行数」，否则整段跳过——而整篇腿
// 恰恰没有这个保证（模型合并/拆分换行是常事）。若就此放手，**这两条腿的输出侧永远为空**，
// 而驳回重翻正是「命中内容最常走的路」（模型拿到驳回意见后重写整段，最可能把违禁词写出来）。
//
// 分层判据（能精确就精确，不能精确才降级）：
//
//	① 行数对得齐 ⇒ 委托逐行版，行号精确、只撤命中那一行；
//	② 行数不齐 ⇒ 整篇判：译文里只要命中，该语种**整篇**按原文交付（Line 记 0 ＝ 整篇，
//	   这是 ComplianceItem.Line 的既定约定，FF/服务端渲染面按它出「整篇」而不是「第 0 行」）。
//
// 降级档必须出声：它把整篇译文换成原文，客户拿到的可用译文明显少于预期，
// 只写进载荷不写日志会让值班把它读成「模型没翻」。
func (w *Workflow) sweepComplianceOnOutputWhole(ctx context.Context, p *ticketPayload, srcText, lc string, pl *ticketCompliancePlan) int {
	if p == nil || pl == nil || !w.Engine.TicketSensitiveEnabled() {
		return 0
	}
	tr := p.Translations[lc]
	if strings.TrimSpace(tr) == "" {
		return 0
	}
	srcLines := splitLines(srcText)
	if len(splitLines(tr)) == len(srcLines) {
		return w.sweepComplianceOnOutput(ctx, p, srcLines, lc, pl)
	}
	hits := w.Engine.TicketSensitiveHits(tr)
	if len(hits) == 0 {
		return 0
	}
	p.Translations[lc] = srcText
	if p.ComplianceSkipped == nil {
		p.ComplianceSkipped = map[string]int{}
	}
	// 处数只数**有内容的行**（空格子不是"一处跳过"，同逐行版只给非空行点名）。
	n := nonEmptyLines(srcText)
	if n == 0 {
		n = 1 // 理论上到不了（tr 非空 ⇒ 源文非空），但计数口径不能回 0：0 会被读成"没撤下任何东西"
	}
	p.ComplianceSkipped[lc] += n
	p.ComplianceItems = append(p.ComplianceItems,
		ComplianceItem{Lang: lc, Line: 0, Side: ComplianceSideOutput, Lead: engine.SensitiveSkippedLead})
	w.recordOnce(ctx, pl, "ticket_output", hits, "模型产出被合规撤下（整篇型腿）")
	observability.Warn(ctx, "文本工单整篇型译文命中禁词，该语种已整篇按原文交付",
		"lang", lc, "src_lines", len(srcLines), "tr_lines", len(splitLines(tr)))
	// 返回值口径＝**被撤下的行数**（整篇撤下就是可译行数），调用方据此判来源档：
	// 「撤下的行数 ≥ 该语种可译行数」⇒ 整语种交付位全是原文 ⇒ compliance_skipped；
	// 否则是 model_with_source_fallback（同逐行版那套判据，两处共用同一把尺子）。
	return n
}
