// ============ 本文件职责中文说明 ============
// DEF-P1-05 段落能力（★ D-1 拍板"全部要做流式"，2026-11-01）：即时/对话翻译的**逐段执行体**。
//
// 现网形态与病症（三条独立成因，本文件收前两条）：
//
//	① 整篇一次调用：模型把多段压成一坨、或只翻第一段（工单链已按逐行治过，即时链没有）；
//	② 句级切分拼回时**无分隔拼接**（已修，见 placeholder.go singleLang 那条注释）；
//	③ 专业档"初翻新流式、done 静默被审校覆盖"⇒ 用户读到两版（实测 complete→absolute）。
//	D-1 的裁决把 ③ 翻成唯一自洽的形态：**初翻静默、审校逐段接流式**，
//	于是"流式吐的就是最终稿"——两档（fast/pro）都成立，fast 是初翻出流、pro 是审校出流。
//
// 三条口径，改这块前必读：
//
//	A. **判定只有一处**：api 包的 `chatEstimateAndPlan`（api/chat_seg_plan.go，预估侧与执行侧共用
//	   同一次调用；引擎这一侧只经 ChatSegPlanFromCtx 读它算好的计划，自己**不判**）。
//	   两处各判一次＝两把尺子（AGENTS §一·5④／§一·11／§23 那一族），而且会出现
//	   "横幅按分段报价、执行按整篇跑"或反过来——客户按预估备的钱不够花，比报错更坏。
//	B. **成本上限回落不是失败**：预估超上限 ⇒ 本次走整篇，不报错、不加钱、照常交付，
//	   只出一条读数 `whole_text_fallback:points_cap`（写进日志）。把它做成拒服务是把
//	   成本保护变成了客户可见故障；悄悄在回落而不改预估才是真事故。
//	C. **词表/品牌只判一次**：判定永远在语种循环与段循环之外（AGENTS §23 那条同形教训）。
//
// =============================================
package engine

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"translator/internal/config"
	"translator/internal/observability"
)

// ChatSegPlan 一次即时翻译的分段执行计划（预估侧与执行侧传的就是这一个结构）。
type ChatSegPlan struct {
	Use        bool     // true ⇒ 逐段执行
	Paragraphs []string // 段数组（Use=false 时可能仍非空，供读数用）
	Reason     string   // 对外排障契约：paragraph | single_paragraph | off | whole_text_fallback:points_cap
	MinTokens  int64    // 本次形态的预估下限（未乘模式倍率；与横幅同一个函数算出来）
	MaxTokens  int64    // 本次形态的预估上限（未乘模式倍率）
	MaxPoints  int64    // 上限档折成的积分读数（0＝不设上限）
}

// chatSegPlanKey ctx 键单例：计划由 api 侧（唯一拿得到模式倍率的解析口）算好后挂进 ctx。
// ⚠️ 没有这个标记 ⇒ **不分段**（回旧路径）。这是"引擎不该自己猜价格"的取向：
// 引擎手里没有租户策略倍率，自己算就会长出第二把尺子；漏挂标记的代价是退回旧行为，不是报错。
type chatSegPlanKeyT struct{}

// chatSegPlanKey ＝ 上面那个类型的单例变量（context key 必须用包私有类型，字符串键会跨包撞车）。
var chatSegPlanKey chatSegPlanKeyT

// WithChatSegPlan 挂分段计划（唯一写点）。
func WithChatSegPlan(ctx context.Context, plan ChatSegPlan) context.Context {
	return context.WithValue(ctx, chatSegPlanKey, plan)
}

// ChatSegPlanFromCtx 取分段计划（第二个返回值＝ctx 里有没有这个决定）。导出的理由：
// 门禁要按"每个 HandleText 调用点都真的挂过计划"判全集，静态腿读不到 ctx，只能读这个唯一入口。
func ChatSegPlanFromCtx(ctx context.Context) (ChatSegPlan, bool) {
	p, ok := ctx.Value(chatSegPlanKey).(ChatSegPlan)
	return p, ok
}

// 两个运行期档位的键名与钳位区间（唯一事实源；判定本体在 api 的 chatEstimateAndPlan——
// 那里才拿得到模式倍率的唯一解析口，引擎自己读库再算一遍就是第二把尺子，见 chat_seg_plan.go 文件头）。
const (
	CfgKeyChatPerParagraph  = "text_chat_per_paragraph" // 0＝回退整篇旧路径（必须走 ConfigIntAllowZero）
	CfgKeyChatSegmentMaxPts = "chat_segment_max_points" // 分段形态的总积分上限；0＝不设上限
	// ChatPerParagraphDefault 开关档的代码缺省（默认开分段）。单独成常量的理由与下面那三个一样：
	// **读点与配置口必须引用同一个数**——两处各写一遍字面量 1，改一侧漏一侧就是"界面显示开、链路按关跑"
	// （或反过来），而前端那份镜像由 chatSegTiers.crossSide.lock.test.ts 三方等值钉住。
	ChatPerParagraphDefault = 1
	// ChatSegmentMaxPtsDefault 代码缺省：够 3–5 段的常规长文走分段，几十段的整篇文档按上限回落。
	// ⚠️ 上线首日要按施工方案 §J-1 的实测法把 3/10/30 段三档的积分量出来再定档（这是初值，不是结论）。
	ChatSegmentMaxPtsDefault = 60
	// ChatSegmentMinParagraphs 少于这个段数一律不折腾（与工单侧"单段不折腾"同口径：不多花一次调用）。
	ChatSegmentMinParagraphs = 2
	// ChatSegmentMaxPtsLo/Hi 积分上限的合法区间（读侧钳位；写侧若配越界值同样落回这里，不静默收脏值）。
	ChatSegmentMaxPtsLo = 0
	ChatSegmentMaxPtsHi = 100000
)

// EstimateParagraphTokens 分段形态的预估区间（唯一实现；预估横幅、免费档计量、分段判定三个消费者都引它）。
// 与整篇版 EstimateChatTokens 的差别只有一件：**固定指令开销按段数重发**（每段各一次初翻、
// pro 档每段各一次审校），其余口径逐字沿用（1:1 折中、pro 的 1.5 倍质检重译余量）。
// ⚠️ 这条成本真实存在：分段把固定开销乘段数，所以 D-1b 才要"总积分上限"这道闸；
//
//	也所以预估必须跟着换——分段执行、整篇报价＝客户按预估备的钱不够花（AGENTS §一·5④）。
func EstimateParagraphTokens(sourceText string, targetLangs []string, mode, sourceLang, uiLang string) (int64, int64) {
	segs := SplitParagraphs(sourceText)
	langs := normalizeLangList(targetLangs)
	if len(segs) == 0 || len(langs) == 0 {
		return 1, 1
	}
	fast := isFastMode(mode)
	var minTok, maxTok int64
	for _, seg := range segs {
		src := ScriptTokens(seg)
		if src < 1 {
			src = 1 // 与整篇版同一兜底：估 0 会让"零消耗"冒充合法读数
		}
		for _, lc := range langs {
			initial, review := ChatLegFixedTokens(sourceLang, lc, uiLang)
			leg1 := initial + src + src // 初翻腿：指令 + 源 + 译
			minTok += leg1
			maxTok += leg1
			if fast {
				continue // 快速档不做校对 ⇒ 上限就是初翻那一腿
			}
			maxTok += review + src + src + src // 审校腿：指令 + 原文 + 上一版译文 + 本版译文
		}
	}
	if !fast {
		maxTok += maxTok / 2
	}
	if minTok < 1 {
		minTok = 1
	}
	if maxTok < minTok {
		maxTok = minTok
	}
	return minTok, maxTok
}

// chatInitialByParagraph 逐段初翻。返回 lang → 与 segs 等长的译文数组（**逐段**，拼回由调用方做）。
//
// 三条讲究：
//   - pro 档整支走 WithStreamSuppressed（初翻闭嘴；口径见文件头 ③）；fast 档照常出流；
//   - 段界经 emitStreamLead 补发 paraSeparator ⇒ delta 与 done 是同一份拼法；
//   - 品牌保护按**段**做（源文不同段命中的术语不同），但术语表/品牌词的**判定**在循环之外（口径 C）。
func (e *Engine) chatInitialByParagraph(ctx context.Context, segs []string, kbTarget, directOther []string,
	srcLang string, fast bool, brandTermsAll map[string]map[string]string, prog Progress,
	warnings *[]string,
) (map[string][]string, map[string]string) {
	initCtx := ctx
	if !fast {
		initCtx = WithStreamSuppressed(ctx)
	}
	all := append(append([]string{}, kbTarget...), directOther...)
	out := make(map[string][]string, len(all))
	for _, lc := range all {
		out[lc] = make([]string, len(segs))
	}
	srcOf := make(map[string]string, len(all))
	for _, lc := range all {
		srcOf[lc] = "model"
	}
	// kbAll[lang][i] ＝ 第 i 段是不是知识库直配命中（来源档按"每段都命中才算 kb"判）
	kbAll := make(map[string][]bool, len(kbTarget))
	for _, lc := range kbTarget {
		kbAll[lc] = make([]bool, len(segs))
	}
	for i, seg := range segs {
		if i > 0 {
			for _, lc := range all {
				emitStreamLead(initCtx, lc, paraSeparator)
			}
		}
		if strings.TrimSpace(seg) == "" {
			continue // 理论上 SplitParagraphs 不产空段；留着当"空段不拨模型"的保险
		}
		// 品牌保护（逐段）：与整篇路径同一函数，只是输入换成这一段
		protected := map[string]string{}
		for lc, terms := range brandTermsAll {
			if prot, changed := protectSourceByLang([]string{seg}, terms); changed {
				protected[lc] = prot[0]
			}
		}
		if len(kbTarget) > 0 {
			kbText := seg
			for _, lc := range kbTarget {
				if prot, ok := protected[lc]; ok {
					kbText = prot
					break
				}
			}
			kbCtx := WithProgressCallback(initCtx, func(phase string) {
				if phase == "ai_generating" {
					prog("AI生成中...", 2, 4)
				}
			})
			res, _ := e.TranslateOne(kbCtx, kbText, kbTarget, false, config.StageKBMatch)
			if res != nil {
				for _, lc := range kbTarget {
					out[lc][i] = res.Translations[lc]
					kbAll[lc][i] = res.MatchedZH != "" && strings.TrimSpace(res.Translations[lc]) != ""
					if !kbAll[lc][i] && strings.TrimSpace(res.Translations[lc]) != "" {
						srcOf[lc] = "model" // 这一段是模型翻的 ⇒ 整条降级（保守方向）
					}
				}
			}
		}
		for _, lc := range directOther {
			srcText := seg
			if prot, ok := protected[lc]; ok {
				srcText = prot
			}
			tr, _ := e.TranslateOtherLang(initCtx, srcText, lc, srcLang, config.StageAIInitial)
			out[lc][i] = tr
		}
		prog(fmt.Sprintf("第 %d/%d 段", i+1, len(segs)), 2, 4)
	}
	// ★ 用例 12 的落点：修复腿必须**逐段**跑，不能交给整篇那一遍。
	//   整篇版 repairChatTranslations 的输入是「整篇源文 vs 整篇译文」——三段里坏一段时它按整篇
	//   判"内容不缺"，于是退化串被静默交付，正是 P0-01 修过的形态被分段绕过去（分段把逐段判据丢了）。
	//   逐段判＝与整篇同一把尺子（chatUsabilityIssue）、同一组兜底（极简→fallback→清空并出声）。
	for i, seg := range segs {
		for lc, arr := range out {
			if strings.TrimSpace(arr[i]) == "" {
				continue
			}
			if e.chatUsabilityIssue(ctx, seg, srcLang, lc, arr[i]) == "" {
				continue
			}
			one := map[string]string{lc: arr[i]}
			e.repairChatTranslations(ctx, seg, srcLang, one, warnings)
			arr[i] = one[lc]
		}
	}

	// 来源档：某语种只要有一段是模型翻的，整条就不能标 kb——这一列答的是"这份译文哪来的"，
	// 混合形态按"最贵的那条腿"报（同 §19 那条「回填过原文就不许再写 model」的取向）。
	for lc, hits := range kbAll {
		if srcOf[lc] != "kb" {
			continue
		}
		for _, hit := range hits {
			if !hit {
				srcOf[lc] = "model" // 有一段不是库里来的 ⇒ 整条不标 kb
				break
			}
		}
	}
	return out, srcOf
}

// arrAllNonEmpty 每段都有内容（缺一段就不能说"整条来自库里"）。
func arrAllNonEmpty(arr []string) bool {
	for _, v := range arr {
		if strings.TrimSpace(v) == "" {
			return false
		}
	}
	return len(arr) > 0
}

// chatReviewByParagraph 逐段审校（pro 档，★ 流式）。就地返回 lang → 逐段审校结果，
// 不合格的那一段**保留初翻**（不覆盖），与整篇路径 reviewOutputRejectReason 那一族的取向一致。
//
// 三条硬口径（施工方案 §B2-1）：
//  1. 采纳前必须过**同一把**可用性判据（即时侧＝chatUsabilityIssue），不许为分段写第三把尺子；
//  2. **段数不许变**：逐段返回逐段拼，少一段就是结构缺陷；
//  3. 预算与 finish_reason 每段各自判（审校腿在 ReviewTranslation 内部已流式＋带截断/纯度退回）。
func (e *Engine) chatReviewByParagraph(ctx context.Context, segs []string, srcLang string,
	initByLang map[string][]string, prog Progress) map[string][]string {
	// ★ 先把待审校的语种**快照成切片**再并发（AGENTS §三）：map 的"边遍历边写"是 runtime fatal error，
	//   不是 panic——recover 兜不住、整个进程挂。整篇路径当年就是栽在这一条上（review_snapshot_test.go）。
	type job struct {
		lang string
		arr  []string
	}
	jobs := make([]job, 0, len(initByLang))
	for _, lc := range sortedLangKeys(initByLang) {
		arr := initByLang[lc]
		if len(arr) != len(segs) {
			continue // 段数对不上（理论不发生）：这一语种整支不审校，保留初翻
		}
		jobs = append(jobs, job{lang: lc, arr: arr})
	}
	out := make(map[string][]string, len(jobs))
	if len(jobs) == 0 {
		return out
	}
	sem := make(chan struct{}, 3) // 与整篇路径同一档并发限流（3 路）
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, j := range jobs {
		wg.Add(1)
		go func(lc string, initial []string) {
			defer wg.Done()
			defer recoverPipeline("chat_review_seg:" + lc)
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()
			res := make([]string, len(segs))
			copy(res, initial) // 缺省＝保留初翻（任何一段没被采纳都是这个值）
			for i, seg := range segs {
				tr := initial[i]
				if strings.TrimSpace(tr) == "" {
					continue // 空段没东西可审
				}
				if e.chatUsabilityIssue(ctx, seg, srcLang, lc, tr) != "" {
					continue // 已明显不可用的初翻不送审校（审校模型同样救不回来，白烧一次）
				}
				prog(fmt.Sprintf("AI 校对中·第 %d/%d 段", i+1, len(segs)), 3, 4)
				if i > 0 {
					emitStreamLead(ctx, lc, paraSeparator)
				}
				// 审校内部：带 sink ⇒ 流式吐审校稿；被截断/脚本不纯 ⇒ 返回 ""（本段保留初翻）
				revised := e.ReviewTranslation(ctx, seg, tr, lc, config.StageReview)
				if strings.TrimSpace(revised) == "" {
					observability.Warn(ctx, "该段审校未采纳，保留初翻出货",
						"lang", lc, "segment", i+1, "of", len(segs), "reason", "empty")
					continue
				}
				// ★ 审校产物也必须过**同一把**可用性判据（AGENTS §19 族四「审校产物仍过同一把尺子」）。
				//   这一腿不是装饰：审校模型完全可能把一段正常译文改成
				//   ① 与源文同形（回声）、② 目标语脚本不纯、③ 退化成 "and" 那一类坏形态——
				//   而初翻那一侧刚刚才是**通过**判据的。旧写法只判"非空即采纳"，
				//   等于分段路径上一颗专门把闸门自己拦下来的东西放进成品（P0-01 从审校这一侧复发）。
				//   ⚠️ 不许在这里改成"另写一套更宽的判据"：第二把尺子的坏法不是报错，是两档结论打架。
				if issue := e.chatUsabilityIssue(ctx, seg, srcLang, lc, revised); issue != "" {
					observability.Warn(ctx, "该段审校稿未过可用性判据，保留初翻出货",
						"lang", lc, "segment", i+1, "of", len(segs), "reason", issue)
					continue
				}
				res[i] = revised
			}
			mu.Lock()
			out[lc] = res
			mu.Unlock()
		}(j.lang, j.arr)
	}
	wg.Wait()
	return out
}

// sortedLangKeys 给并发任务一个**稳定顺序**（map 遍历顺序随机；不排序的话同一份输入的
// 流式段序会跟着抖，日志与门禁都不可复现——这是"读数要能对上"的最便宜的一条保障）。
func sortedLangKeys(m map[string][]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
