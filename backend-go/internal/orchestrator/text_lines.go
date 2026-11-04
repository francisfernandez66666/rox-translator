// ============ 本文件职责中文说明 ============
// DEF-02（2026-10-24 云端 UAT）：文本工单逐行翻译与「只补缺失/错误行」。
//
// 现网形态：源文两段（第二行本身是「请翻译成英文并保留段落结构」这类元指令），
// 整篇一次调用时模型把它当指令吞掉，translations.en 只有第一段；qa 的 length 规则
// 因为译文总长不短而放行。本文件把文本工单改为逐行批量翻译：
//   - 源文按 \n 切成行（空行保留占位），非空行才送模型；
//   - 每行独立过 TextTranslationUsable（与文件链路同一把尺子，含指令回显）；
//   - 重发时先读回旧译文：正确行直接复用，只有缺失/错误行才重新调用模型。
//
// ============================================
package orchestrator

import (
	"context"
	"sort"
	"strings"

	"translator/internal/engine"
	"translator/internal/observability"
)

// textPerLineEnabled 文本工单是否启用逐行翻译（system_config 默认 1，0=回退整篇旧行为）。
func (w *Workflow) textPerLineEnabled() bool {
	if w == nil || w.Store == nil {
		return false
	}
	// 0＝回退整篇是方案里写明的应急开关，必须走 ConfigIntAllowZero：
	// ConfigInt 的「≤0 视为未配置」会把这一档读回默认 1，开关按不下去。
	return w.Store.ConfigIntAllowZero("text_ticket_per_line", 1) != 0
}

// ============ 切分/拼回：薄委托到 engine（★ 2026-11-01 DEF-P1-05 批） ============
// 这三个函数原来在本文件里各自实现一份，而即时链（engine/text.go）后来要用的正是同一对
// "按分隔符切 / 拼回去"的判据。两份实现迟早分家（本仓「多把尺子」那一族），所以收进
// engine/textlines.go 做唯一事实源；这里保留同名薄委托，**调用点一字不改**。
// ⚠️ 反向 import 不许出现：engine 不 import orchestrator（会成环），所以方向只能是"工单委托引擎"。

// composeLineOutput 把逐行译文拼回整篇（行数与源文一致；行内换行压成空格，防一段变多段）。
func composeLineOutput(lines []string) string { return engine.ComposeLineOutput(lines) }

// splitLines 按 \n 切行并保留空行占位（末尾空行也保留，保证行数与源文一致）。
func splitLines(s string) []string { return engine.SplitLines(s) }

// nonEmptyLines 统计非空行数（与 qa.nonEmptyLineCount 同口径；"≥2 才逐行折腾"的门限吃这个数）。
func nonEmptyLines(s string) int { return engine.NonEmptyLines(s) }

// translateTextPerLine 文本工单逐行翻译/增量补译。
// 返回 true 表示本函数已接管（调用方保存 payload 后返回）；false 表示回退旧整篇路径。
func (w *Workflow) translateTextPerLine(ctx context.Context, p *ticketPayload) bool {
	if w == nil || w.Engine == nil || p == nil || !w.textPerLineEnabled() {
		return false
	}
	srcLines := splitLines(p.SourceText)
	if nonEmptyLines(p.SourceText) < 2 {
		return false // 单段不折腾（与旧路径等价，少两次模型调用）
	}
	// ★★ 2026-11-03 用户裁决①：工单通道接入内容合规闸，处置＝跳过。
	// 判定**每单一次、语种循环之外**（理由与"不许按语种重复判"同 §23 那条领域词表的现网教训：
	// 判定只吃源文，放进语种循环＝同一份源文重复判 N 次，而留痕会跟着变成每语种一条）。
	// 闸门未开时这里回 nil，下面所有分支逐字等价于改造前（关档＝没有这个特性）。
	pl := w.planTicketCompliance(p)
	skippedByLang := map[string]int{}
	var skipItems []ComplianceItem
	// nonEmptySrc 是"这一单到底有几行可译内容"的唯一计数，给来源分派用：
	// 只有**全部可译行都被合规跳过**才允许把该语种写成 compliance_skipped。
	// 不这么收就会在"部分复用旧译文＋部分被跳"的语种上谎称整语种是合规跳过的
	// （而客户手里那半段确实是译文）。
	nonEmptySrc := nonEmptyLines(p.SourceText)
	// 载荷里的 Translations 可能是 nil（FinalResult 是一段没带 translations 键的 JSON，
	// loadPayload 只保证反序列化成功）。与下面 Sources 的守卫同一形态——缺这一句会在写入点
	// panic「assignment to entry in nil map」，把整条流水线打挂。
	if p.Translations == nil {
		p.Translations = map[string]string{}
	}
	reused, retranslated, unresolved := 0, 0, 0
	unresolvedByLang := map[string]int{}
	deadByLang := map[string]int{}
	// truncByLang 记「被 max_tokens 截断」那一档的行数（八-3c）。它与 deadByLang 是**两族**：
	// 一族重拨有用（抬高预算），一族重拨一万次也不会好——交付面那句话必须说得出是哪一族。
	truncByLang := map[string]int{}
	deadLangs := 0
	// ★ 2026-10-26 裁决八-1「上游问题我可以接受，就诚实表达问题就好」＋方案 §七早停的第二层：
	// 上游是**所有目标语种共用的那一条腿**（同一 base／key／模型）。某一个语种整批回
	// `[翻译失败]` ⇒ 后面每个语种只会同样整批失败，而每一批在引擎内部还要**逐段再兜三枪**
	// （BatchTranslate 的空段兜底；2026-10-26 实测：四行批全断＝1 次批量 + 12 次单发）。
	// 判过上游断之后剩余语种**一次上游都不拨**、直接按原文出货：客户少等 N 倍时间，
	// 积分少扣 N 倍，而交付物字节完全一样（都是原文＋点名）。
	// ⚠️ 只挡"需要重译"的语种：整语种都从知识库或旧译文复用来的（`needSrc` 为空）照走原路径——
	//   那是零模型调用的分支，把它一起挡等于把自己手上的成品也打成原文。
	upstreamDeadSeen := false
	for _, lc := range p.TargetLangs {
		existing := splitLines(p.Translations[lc])
		outLines := make([]string, len(srcLines))
		var needSrc []string
		var needIdx []int
		for i, src := range srcLines {
			if strings.TrimSpace(src) == "" {
				outLines[i] = ""
				continue
			}
			// ★ 2026-11-03 裁决①：命中禁词的行**不进模型**（保住"上游零暴露"这条收益），
			//   该位按原文出货（不留白不丢行——留空会让交付行数比源文少，而 qa 的 structure/
			//   数字规则正是按行数与数字差判整单驳回的，BUG-01/06 的病文本体）。
			// ⚠️ 这一支排在"旧译文可用则复用"**之前**：合规判定不因上一轮已经翻出来过而让路，
			//   否则闸上线当天，历史载荷里那一行的旧译文会继续投给客户（§一·13 那条「换件没修」
			//   在交付面一侧的形态），而"这单已合规"的读数却说拦住了。
			if pl.blocked(i + 1) {
				outLines[i] = src
				skippedByLang[lc]++
				skipItems = append(skipItems, ComplianceItem{
					Lang: lc, Line: i + 1, Terms: pl.hitsByLine[i+1], Side: ComplianceSideInput,
					Lead: engine.SensitiveSkippedLead,
				})
				continue
			}
			if i < len(existing) && strings.TrimSpace(existing[i]) != "" &&
				w.Engine.TextTranslationUsable(ctx, src, existing[i], lc) {
				outLines[i] = existing[i]
				reused++
				continue
			}
			needSrc = append(needSrc, src)
			needIdx = append(needIdx, i)
		}
		langDead := false
		if len(needSrc) > 0 && upstreamDeadSeen {
			// 上游已判断 ⇒ 本语种一枪都不拨，整语种按原文出货（取向见上面那段注释）。
			// 计数仍逐行落：回填行数是交付面那句话的读数来源，不能因为"没打"就记 0。
			for j, idx := range needIdx {
				outLines[idx] = needSrc[j]
				unresolved++
				unresolvedByLang[lc]++
				deadByLang[lc]++
			}
			deadLangs++
			langDead = true
			observability.Warn(ctx, "文本工单逐行翻译：上游已判断，本语种整语种跳过模型调用并按原文回填",
				"lang", lc, "backfilled_lines", len(needSrc))
		} else if len(needSrc) > 0 {
			// 本单领域词表已经挂在 ctx 上（见 workflow.go 的文本走期入口），批量层在 opts 为空时回落 ctx。
			batch, truncated := w.Engine.BatchTranslateMarked(ctx, needSrc, lc, 15, nil)
			// isPlaceholderish 这一槽是不是「上游压根没吐」那一档（空串／失败占位，且**不是**被截断）。
			//  ★ 八-3c／九-3 的分档点就落在这一句上：截空与"没返回"在字节上同形，
			//    但截空**可救**（单独重拨、预算抬高），把它计进上游断＝一行截空攒够三行
			//    就把整个语种误判成上游不通、可救的行集体按原文出货。
			isPlaceholderish := func(j int) bool {
				if j >= len(batch) {
					return true // 批里压根没有这一槽（连占位都没写回来）＝上游没吐
				}
				return engine.IsFailurePlaceholder(batch[j]) && !truncated[j]
			}
			upstreamDeadPre := allSlotsFailurePlaceholder(batch, truncated)
			// ★ 2026-10-26 用户追加裁决八-1「上游问题我可以接受，就诚实表达问题就好」：
			// 整批零候选（每一槽位都是空串或 `[翻译失败]`）＝**上游整条不通**（401／欠费／超时／熔断），
			// 这时逐行补译一万次也不会好，客户却白等白扣。判据分两层：
			//   ① 整批全空 ⇒ 立刻判上游断，一枪补译都不打（上面那一行）；
			//   ② 批内有内容但**连续 N 槽**是空/占位 ⇒ 同样判上游断（下面循环里的 deadStreak）。
			// ⚠️ `max_tokens` 截空**不走这里**（八-3c）：那种槽位与"上游没返回"在字节上同形，
			//   要由出栈侧带截断标记单独分档，否则三行截空就把整语种误判成上游断、可救的行集体打成原文。
			// ★ 2026-10-26 用户令①＋方案 §七「上游断早停」：
			// 批量链连续 N 行回空/`[翻译失败]` ⇒ 这不是"译文不合格"，是**上游压根没吐东西**，
			// 后面的行再逐行补译也只可能是白烧（现网 9B 上游欠费/401 时整批全空，
			// 旧形态要为一份永远翻不出来的单打 N 行 × 3 轮调用）。
			// 命中即把**剩余未译行一次性**按原文回填并计数，整语种只出一行 WARN。
			deadStreak, deadBackfilled := 0, 0
			upstreamDead := upstreamDeadPre
			for j, idx := range needIdx {
				var out string
				if j < len(batch) {
					out = batch[j]
				}
				if isPlaceholderish(j) {
					deadStreak++
				} else {
					deadStreak = 0
				}
				if deadStreak >= w.textLineDeadStreakMin() {
					upstreamDead = true
				}
				if upstreamDead {
					// ★ 早停省的是**后续补译那一百多枪**，不是"把手里已有的可用译文扔掉"。
					// 同一批（≤15 行）后面的槽位常常已经翻出来了——那些是本单**已经付过钱**的成品，
					// 无条件回落原文等于把已到货的译文丢回给客户看中文。
					// 所以这一支仍然过同一把判据（纯函数，零模型调用），可用就用；不可用才回填并计数。
					if !engine.IsFailurePlaceholder(out) && w.Engine.TextTranslationUsable(ctx, needSrc[j], out, lc) {
						outLines[idx] = out
						retranslated++
						continue
					}
					outLines[idx] = needSrc[j]
					unresolved++
					unresolvedByLang[lc]++
					// ★ 八-3c：回填行**按成因分档，且两档互斥**——被截断那一行归截断档，
					//   不许同时记进上游断档。交付面那两句（"上游未返回可用译文"／"输出被截断"）
					//   问的就是成因；同一行记两次＝两个数加起来比总行数还大，读的人无从对账。
					if truncated[j] {
						truncByLang[lc]++
					} else {
						deadBackfilled++
					}
					continue
				}
				if !w.Engine.TextTranslationUsable(ctx, needSrc[j], out, lc) {
					if v, ok := w.resolveLine(ctx, needSrc[j], lc); ok {
						out = v
					} else {
						// ★ 2026-10-26 用户令①「不留白、不整句丢失」：补译全部失败也**绝不留空行**。
						// 旧形态留 "" ⇒ 行数天生比源文少（BUG-01「12 段译 10 段」、BUG-06「源 12 译 10」），
						// 而被抹掉那一行的**数字也一起消失** ⇒ 硬闸按数字差判整单驳回（BUG-06 的驳回本体）。
						// 现在的顺序是：批量 → 极简补译若干轮 → 仍不合格才按原文回落（不留白、不丢行，
						// 且这一行被计入 untranslated_lines 并打 WARN，交付面不是静默的）。
						// 为什么回落原文而不是留那个"看着像译文"的坏候选：坏候选里最可能的形态是
						// **提示词回显**（DEF-01 现网就是它），把回显交给客户比把原文交给客户更坏。
						out = needSrc[j]
						unresolved++
						unresolvedByLang[lc]++
						if truncated[j] {
							truncByLang[lc]++
						}
						// ★ 八-3d／W9：回落必须点名**成因**。长度爆炸那一族的特征是"6 个字的格吐出一整封邮件"，
						//   它的病因在**批量上下文**（同批别的段串进来），所以补救动作是把这一行单独送
						//   （`resolveLine` 走的就是单段极简腿，天然不复用那一批）；
						//   读数（源/译字数与比值）随行报出来，否则下一位只能看见"回落了"却分不清是哪一族。
						observability.Warn(ctx, "文本工单逐行补译仍不合格，按原文回落（未留白/未丢行）",
							"lang", lc, "line", idx+1, "rounds", w.textLineRetryMax(),
							"truncated", truncated[j],
							"explosion", engine.IsLengthExplosion(needSrc[j], out),
							"length_info", engine.LengthExplosionInfo(needSrc[j], out))
					}
				}
				outLines[idx] = out
				retranslated++
			}
			if upstreamDead {
				deadLangs++
				langDead = true
				// 把这一档传给**后面的语种**（同一条上游腿，见函数头那条注释）
				upstreamDeadSeen = true
				deadByLang[lc] = deadBackfilled
				// 整语种一行：早停这件事必须被值班看见（它同时意味着"这一单的上游是坏的"），
				// 但不逐行刷屏——回填行数已经进了 UnresolvedLines。
				observability.Warn(ctx, "文本工单逐行翻译判上游断，剩余未译行一次性按原文回填",
					"lang", lc, "backfilled_lines", deadBackfilled,
					"streak", w.textLineDeadStreakMin())
			}
		}
		p.Translations[lc] = composeLineOutput(outLines)
		// ★ 2026-11-03 裁决①的输出侧：撤下"整行没译"之后还要撤下"翻出来了但翻出了违禁内容"。
		// 排在来源分派**之前**——它可能把某一行的译文换成原文，来源那一列答的是
		// "这个语种的译文哪来的"，必须按撤完的最终形态写。
		// 返回值＝被撤下的行数，来源分派要把它算进"交付位有多少行是原文"（见下 switch 首支）。
		sweptOut := w.sweepComplianceOnOutput(ctx, p, srcLines, lc, pl)
		if p.Sources == nil {
			p.Sources = map[string]string{}
		}
		// ★ 2026-10-26 用户追加裁决八-1：来源列必须**如实**反映回填，不许在上游断时仍写 model。
		// 三档判据（取向写明，免得下一位按"多一行译文就换个档"去微调）：
		//   langDead ⇒ upstream_failed——**哪怕断之前已经译出了几行**。这一列给客户与管理台读的是
		//     "这一语种的译文哪来的"，早停后多数行是原文；写成 model_with_source_fallback
		//     等于把"上游断了"报成"只兜了几行"，而具体有几行**另有其口**（UpstreamDeadLines），
		//     不该由这一列兼顾精度却牺牲诚实。
		//   有回填但没判死 ⇒ model_with_source_fallback
		//   零回填 ⇒ model
		// ⚠️ 老形态在这里无条件写 "model"，于是管理台来源列、按来源统计与自迭代写库都把
		//    按原文回落的行当模型译文消费——AGENTS §一·19 那条「读数为失败背书」。
		//    同一句还把**知识库直配命中**的语种也刷成了 model（本函数一行模型都没为它打）：
		//    那是旧形态就存在的第二处背书，本轮顺手按"是否真打过模型"分开。
		switch {
		case nonEmptySrc > 0 && skippedByLang[lc]+sweptOut >= nonEmptySrc:
			// ★ 2026-11-03 裁决①：**交付位全是原文** ⇒ 落第六档。
			//   两个来源合起来数这一条：输入侧跳过的行（skippedByLang）与输出侧撤下的行
			//   （sweptOut）。只看前者会漏掉"模型翻出来了、但翻出了违禁内容被整篇撤下"
			//   那一形态——那时这一语种交付给客户的每一行同样是原文。
			//   这里**不许**写 model（那是 §19 那条"回填过原文不许再写 model"的同一族读数背书），
			//   也不许写 upstream_failed（上游完全正常，写了会把值班支去查一条好腿）。
			//   排在 KB 那一支**之前**：全部可译行都已回落原文时，这一轮一行复用都没交付，
			//   载荷里残留的历史 `kb` 不能替本轮的合规跳过说话（同一条「读数为失败背书」）。
			p.Sources[lc] = SourceComplianceSkipped
		case len(needSrc) == 0 && p.Sources[lc] == SourceKB:
			// KB 命中且整语种复用 ⇒ 来源保持 kb（这一语种零模型调用，管理台按它算"本单零成本"）
		case langDead:
			p.Sources[lc] = SourceUpstreamFailed
		case unresolvedByLang[lc] > 0 || sweptOut > 0:
			// 有按原文回落的行（上游没译出 / 合规撤下）⇒ 这一档，不许写 model。
			p.Sources[lc] = SourceModelWithFallback
		default:
			p.Sources[lc] = SourceModel
		}
	}
	if unresolved > 0 {
		if p.UnresolvedLines == nil {
			p.UnresolvedLines = map[string]int{}
		}
		for lc, n := range unresolvedByLang {
			p.UnresolvedLines[lc] = n
		}
	}
	if len(truncByLang) > 0 {
		if p.TruncatedLines == nil {
			p.TruncatedLines = map[string]int{}
		}
		for lc, n := range truncByLang {
			p.TruncatedLines[lc] = n
		}
	}
	if len(deadByLang) > 0 {
		if p.UpstreamDeadLines == nil {
			p.UpstreamDeadLines = map[string]int{}
		}
		for lc, n := range deadByLang {
			p.UpstreamDeadLines[lc] = n
		}
	}
	// ★★ 2026-11-03 用户裁决①/②：把输入侧跳过的计数与点名素材并进载荷。
	// 输出侧（`sweepComplianceOnOutput`）已经**边跑边写**进 p 的两个键，所以这里必须 `+=` 而不是赋值——
	// 直接赋值会把输出侧那一族撤下的行数抹掉，两个数加起来对不上交付面点名的条数。
	if len(skippedByLang) > 0 {
		if p.ComplianceSkipped == nil {
			p.ComplianceSkipped = map[string]int{}
		}
		for lc, n := range skippedByLang {
			p.ComplianceSkipped[lc] += n
		}
	}
	if len(skipItems) > 0 {
		p.ComplianceItems = append(p.ComplianceItems, skipItems...)
		sortComplianceItems(p.ComplianceItems) // 交付面按语种内行号顺序点名，不靠 append 顺序
		// 留痕一次（**每一族成因各一条**，不是每一行/每一语种一条）：
		// 逐语种刷一条审计＝把一次命中刷成 N 条告警（告警中心＋群机器人＋站内信一起灌脏）。
		w.recordOnce(ctx, pl, "ticket_input", pl.sampleTerms(), "工单逐行腿按合规跳过")
	}
	observability.Info(ctx, "文本工单逐行翻译/增量补译完成",
		"reused_lines", reused, "retranslated_lines", retranslated,
		"fallback_source_lines", unresolved, "upstream_dead_langs", deadLangs,
		"truncated_lines", sumMapCounts(truncByLang),
		"compliance_skipped", sumMapCounts(skippedByLang),
		"langs", len(p.TargetLangs))
	return true
}

// sortComplianceItems 把点名素材排成**稳定序**（语种码 → 行号 → 成因档）。
//
// 为什么要显式排：素材是两个地方追加的（输出侧在语种循环内、输入侧在循环外），
// 而交付面那一节是逐条列给客户看的。顺序不稳 ⇒ 同一份载荷两次渲染出两种顺序，
// 客户会读成"内容变了"；跨端等值锁（服务端 .md 尾节 vs 前端详情区）也会随机红。
func sortComplianceItems(items []ComplianceItem) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Lang != items[j].Lang {
			return items[i].Lang < items[j].Lang
		}
		if items[i].Line != items[j].Line {
			return items[i].Line < items[j].Line
		}
		return items[i].Side < items[j].Side
	})
}

// sumMapCounts 把一个 map[string]int 的计数加总（只给 INFO 日志出一行总数，不进载荷）。
func sumMapCounts(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}

// allSlotsFailurePlaceholder 整批零候选（每一槽位都是空串或 `[翻译失败]`）⇒ 判上游整条不通。
// 与循环里的连续档（`textLineDeadStreakMin`）是同一件事的两层：那一档抓"翻到一半断"，
// 这一档抓"第一枪就全空"——后者不必等凑满 N 行才停，补译那一百多枪全是白烧。
// 判据只问 `engine.IsFailurePlaceholder`（空/占位），**不问**回显、拒译：那些是"有内容的坏答案"，
// 属于能靠补译救的那一族，混进来会把可救的语种误判成上游断、整语种打成原文。
func allSlotsFailurePlaceholder(batch []string, truncated map[int]bool) bool {
	if len(batch) == 0 {
		return false
	}
	for j, v := range batch {
		if !engine.IsFailurePlaceholder(v) {
			return false
		}
		// ★ 八-3c：只要这一批里有**任何一槽是被截断**的，就不能判"上游整条不通"。
		//   截断是预算问题（可救），上游断是通道问题（不可救）——把前者算进后者，
		//   后果是整个语种跳过补译直接按原文出货，客户拿到一份本该翻得出来的成品。
		if truncated[j] {
			return false
		}
	}
	return true
}

// textLineRetryMax 单行补译的极简指令重试轮数（system_config text_line_retry_max，默认 3，钳 1~5）。
// 为什么要有这一档：现网两条 bug 的代价不对称——少翻一行＝客户拿到缺段成品（BUG-01/06），
// 多打三枪＝这一行多花几千 token。后者远小于一次整单驳回重发的成本，所以默认敢给到 3。
func (w *Workflow) textLineRetryMax() int {
	if w == nil || w.Engine == nil {
		return 3
	}
	return w.Engine.LineRetryMax()
}

// textLineDeadStreakMin 判定「上游断了」所需的**连续**空/失败占位行数
// （system_config text_line_upstream_dead_streak，默认 3，钳 2~10）。
// 为什么取「连续」而不是「累计」：单行偶发失败很常见（超时/供应商抖动），下一行恢复正常就该继续翻；
// 连续三行全空才是"整批上游没在返回"的形态。阈值下界给 2 是为了不让配置成 1——
// 一行失败就把整语种打成回填，等于把一次抖动放大成一份缺全文（比留白好一点，但仍是不该发生的降级）。
func (w *Workflow) textLineDeadStreakMin() int {
	if w == nil || w.Engine == nil {
		return 3
	}
	// 委托 engine（唯一实现）：图片原位链的早停与文本链必须是同一个阈值，
	// 否则同一份库配置在两条链上对应两个灵敏度（本仓"多把尺子"那一族的复发点）。
	return w.Engine.LineDeadStreakMin()
}

// resolveLine 单行补译梯——**薄委托**到 engine.RetryLineUntilUsable（全仓唯一实现）。
// ★ 2026-10-26：图片原位链也要做同一件事（裁决 5「未翻译项先充分重试」），两处各写一份
//
//	就会长出"文本一行三枪、图片一行五枪"这种同动作不同成本的形态；委托之后档位只有一处读点。
func (w *Workflow) resolveLine(ctx context.Context, src, lc string) (string, bool) {
	return w.Engine.RetryLineUntilUsable(ctx, src, lc)
}

// reviewTextPerLine 文本工单逐行审校：保留行数，避免整篇审校把某一段合并/吞掉。
// 返回 true 表示已接管；false 走旧整篇 review。
func (w *Workflow) reviewTextPerLine(ctx context.Context, p *ticketPayload) bool {
	if w == nil || w.Engine == nil || p == nil || !w.textPerLineEnabled() {
		return false
	}
	if nonEmptyLines(p.SourceText) < 2 {
		return false
	}
	srcLines := splitLines(p.SourceText)
	for lc, tr := range p.Translations {
		trLines := splitLines(tr)
		if len(trLines) != len(srcLines) {
			continue // 行数不符：不冒险逐行审校，交给结构 QA/重发
		}
		var srcs, tgts []string
		var idxs []int
		for i := range srcLines {
			if strings.TrimSpace(srcLines[i]) == "" {
				continue
			}
			srcs = append(srcs, srcLines[i])
			tgts = append(tgts, trLines[i])
			idxs = append(idxs, i)
		}
		if len(srcs) == 0 {
			continue
		}
		rev := w.Engine.ReviewTranslationBatch(ctx, srcs, tgts, lc, "review")
		for j, i := range idxs {
			if j < len(rev) && strings.TrimSpace(rev[j]) != "" &&
				w.Engine.TextTranslationUsable(ctx, srcLines[i], rev[j], lc) {
				trLines[i] = strings.ReplaceAll(strings.TrimSpace(rev[j]), "\n", " ")
			}
		}
		p.Translations[lc] = composeLineOutput(trLines)
	}
	return true
}
