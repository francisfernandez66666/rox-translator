// ============ 本文件职责中文说明 ============
// 工单翻译工作流实现：把各业务步骤（知识库匹配→AI 初翻→评估→审校→Gate 校验→
// 语言文化闸门→人工审批→自迭代写库）绑定到 FlowDef 执行器。
// 中间结果以 JSON 存于工单 FinalResult（ticketPayload），供各步骤读取/更新；
// 支持驳回后按意见重翻全部语言，以及已批准工单重跑时复用并保护人工终稿（C4）。
// =============================================
package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"

	"translator/internal/config"
	"translator/internal/culture"
	"translator/internal/engine"
	"translator/internal/gate"
	"translator/internal/kb"
	"translator/internal/llm"
	"translator/internal/notify"
	"translator/internal/qa"
	"translator/internal/store"
	"translator/internal/tenant"
)

// Workflow 工单翻译工作流：把各业务步骤绑定到 FlowDef 执行器
type Workflow struct {
	Executor *Executor      // 流程执行器
	Engine   *engine.Engine // 翻译引擎（初翻/审校/语义检索）
	Store    *store.Store   // 平台存储
	Tenant   *tenant.Store  // 租户存储
	KB       *kb.KBDatabase // 知识库（自迭代写库）
}

// NewWorkflow 创建工作流并注册全部步骤执行器。
// 参数：st=平台存储，eng=翻译引擎，ts=租户存储，kbdb=知识库。
// 返回：工作流实例。
func NewWorkflow(st *store.Store, eng *engine.Engine, ts *tenant.Store, kbdb *kb.KBDatabase) *Workflow {
	w := &Workflow{
		Executor: NewExecutor(st),
		Engine:   eng,
		Store:    st,
		Tenant:   ts,
		KB:       kbdb,
	}
	w.Executor.Ten = ts // 执行器需要租户存储读取流程配置
	w.registerSteps()
	return w
}

// registerSteps 注册各流程步骤执行函数。
func (w *Workflow) registerSteps() {
	ex := w.Executor

	// kb_match：知识库四层查找（企业包→行业包）+ 语义检索，结果写入 ticket state
	ex.Register("kb_match", w.runKBMatch)

	// ai_initial：AI 初翻（术语注入 + 缩句双稿）
	ex.Register("ai_initial", w.runAIInitial)

	// evals_initial：初翻评估（Judge；无 Key 跳过）
	ex.Register("evals_initial", w.runEvalsInitial)

	// review：审校 Agent（patch 输出）
	ex.Register("review", w.runReview)

	// evals_review：审校评估
	ex.Register("evals_review", w.runEvalsReview)

	// gate：ConstraintGate 8 项硬校验
	ex.Register("gate", w.runGate)

	// culture_gate：语言文化包输出闸门（反查译文）
	ex.Register("culture_gate", w.runCultureGate)

	// qa：确定性质检（数字/占位符/漏翻等纯规则，报告写入 payload，不阻断流程）
	ex.Register("qa", w.runQA)

	// approval：人工审批（工单转为待审批；由审批台决定批准/驳回）
	ex.Register("approval", w.runApproval)

	// feedback：自迭代写库（审批批准后写 KB）
	ex.Register("feedback", w.runFeedback)
}

// 工单翻译中间结果（存于 ticket state payload）
type ticketPayload struct {
	SourceText       string             `json:"source_text"`                  // 源文本
	TargetLangs      []string           `json:"target_langs"`                 // 目标语言列表
	Translations     map[string]string  `json:"translations"`                 // 语言 → 译文
	Sources          map[string]string  `json:"sources"`                      // 语言 → 来源（见下方 Source* 三档名单）
	Mode             string             `json:"mode"`                         // 匹配模式标识
	Examples         []*kb.Row          `json:"examples,omitempty"`           // 知识库命中例句（供 AI 初翻注入术语参考）
	EvalScores       map[string]float64 `json:"eval_scores"`                  // 语言 → 评估总分
	ReviewEvalScores map[string]float64 `json:"review_eval_scores,omitempty"` // 语言 → 校对评估总分
	// ★ 改造 4（2026-09-17）评估不合格处置：低于 evals_fail_threshold（默认 60）的语言
	//   打标（工单详情透出「质检存疑」徽标）+ 告警中心/群机器人提醒（同单同语言同阶段幂等一次）
	QualityFlaggedLangs []string `json:"quality_flagged_langs,omitempty"` // 评估不达标语言列表
	// ★ 2026-10-26 用户令①/⑦：逐行链路补译到最后一轮仍不合格的**行数**（按语言）。
	// 这些行**按原文回落**（不留白、不丢行），计数落这里给交付面记录"哪几行没译出"，
	// 只数行数不落原文（工单内容属客户资料，与 §13 那条日志不留原文同口径）。
	UnresolvedLines map[string]int `json:"unresolved_lines,omitempty"`
	// ★ 2026-10-26 用户追加裁决八-1「上游问题可以接受，就诚实表达问题就好」：
	// 回填行**按族分档**计数——`unresolved_lines` 是总数，这一档只数**判上游断**那一部分
	// （差值即"模型翻得出但形态不合格"那一档）。两档混成一味的代价：客户与值班看到的是
	// 同一句"部分未译出"，而一族需要等上游、另一族需要改判据，处置完全不同。
	UpstreamDeadLines map[string]int `json:"upstream_dead_lines,omitempty"`
	// ★ 2026-10-26 用户追加裁决八-3c＋九-3（W8 与本批一起上）：只数**被 max_tokens 截断**那一档。
	// 它与 upstream_dead 是两族——截断抬高预算单独重拨能救，上游断重拨一万次也不会好。
	// 交付面那句"哪一语种、哪一档、几行"要的就是这一维；三档各记各的，不许混成一味。
	TruncatedLines map[string]int         `json:"truncated_lines,omitempty"`
	EvalNotified   map[string]bool        `json:"eval_notified,omitempty"` // 提醒幂等标记（key=阶段:语言）
	Gate           *gate.GateResult       `json:"gate"`                    // Gate 校验结果
	Culture        *culture.CultureResult `json:"culture,omitempty"`       // 语言文化闸门结果
	QAReport       *qa.Report             `json:"qa_report,omitempty"`     // 确定性 QA 质检报告
	RetryCount     map[string]int         `json:"retry_count,omitempty"`   // 语言 → 硬闸自动重译次数
	GateHints      map[string]string      `json:"gate_hints,omitempty"`    // 语言 → 最近一次硬闸打回原因（供审批参考）
	// ★ 2026-10-24 DEF-02 的整单重发计数**不在这里**：它落 `tickets.repair_attempts` 列
	//   （§10.1.2；判据见 Workflow.StructureRetryNeeded）。原先写成载荷字段会被任何一次
	//   「整段重写载荷」的路径抹回 0，于是上限变软、坏单无限重发并每轮真扣费。
}

// 工单载荷 Sources[lang] 的**唯一合法值名单**（★ 2026-10-26 用户追加裁决八-1）。
// ⚠️ 这一档含 `approved`：它由审批面（internal/api/tickets.go）写，不在本包，但仍进同一张名单——
//
//	名单锁要扫的是**全部生产写点**，按包切一半等于给下一处裸字面量留盲区。
//
// 为什么要把这几档钉成常量＋一条名单锁：这一列是**对外契约**（管理台来源列、按来源统计、
// 自迭代写库都读它），而"回填过原文却仍写 model"正是本仓反复踩过的「读数为失败背书」形态
// （AGENTS §一·3 那条 `from` 档位同族）。名单之外不许长出第四种值。
const (
	SourceKB                = "kb"                         // 知识库直配命中
	SourceModel             = "model"                      // 模型译文，零回填
	SourceModelWithFallback = "model_with_source_fallback" // 模型译文，但含按原文回落的行
	SourceUpstreamFailed    = "upstream_failed"            // 该语种判上游断，未译行全部按原文回填
	SourceApproved          = "approved"                   // 审批终稿覆盖（人工改稿后落的那一语种；api 侧写入）
)

// parseTicketLang 从 target_langs 逗号分隔字符串解析语言列表。
// 参数：s=目标语言串（如 "en,zh_hant"）；返回语言代码切片。
func parseTicketLang(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// savePayload 把中间结果 JSON 序列化写入工单 FinalResult 并落库。
// 参数：t=工单对象，p=中间结果结构体。
// ★ TM 自闭环计数不在此处做：此前每步 savePayload 都会 BumpTmHit，导致同一译文在单工单内
// 被计约 7 次（kb_match/ai_initial/review/evals×2/qa），阈值语义失真。现统一由工单收尾的
// service.BumpTmHitsBatch（ticket.go）一次性批量累计，每个 (原文,语言,译文) 仅计一次（整改 R6）。
func (w *Workflow) savePayload(t *store.Ticket, p *ticketPayload) {
	data, _ := json.Marshal(p)
	t.FinalResult = string(data)
	_ = w.Store.UpdateTicket(t)
}

// usefulTermSource 过滤「泛词/过短词」：子串命中不该把中文/验收/翻译这类通用词当术语例句。
// CJK 至少 2 字、拉丁至少 3 字母；再排除常见泛词。只是**读取侧过滤**，不改 KB 数据。
func usefulTermSource(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	runes := []rune(s)
	if len(runes) < 2 {
		return false
	}
	latinOnly := true
	for _, r := range runes {
		if r > 127 {
			latinOnly = false
			break
		}
	}
	if latinOnly && len(runes) < 3 {
		return false
	}
	generic := map[string]bool{
		"中文": true, "验收": true, "翻译": true, "内容": true, "文本": true, "语言": true,
		"产品": true, "服务": true, "公司": true, "客户": true, "销售": true, "系统": true,
		"平台": true, "管理": true, "数据": true, "信息": true, "质量": true, "价格": true,
		"订单": true, "用户": true, "功能": true, "支持": true, "时间": true, "工作": true,
		"项目": true, "团队": true, "市场": true,
	}
	return !generic[s]
}

// runKBMatch 知识库匹配。
// 四层查找：先查 kb_packages/kb_entries（企业包→行业包按层 L1术语>L2 TM>L3安全句>L4碎片），
// 再交给 engine 的 npz 语义库兜底，缺失语言由模型补齐。
// 参数：ctx=上下文，t=工单对象。
func (w *Workflow) runKBMatch(ctx context.Context, t *store.Ticket) error {
	langs := parseTicketLang(t.TargetLangs)
	if len(langs) == 0 {
		langs = []string{"en"} // 无目标语言默认英语
	}
	// ★ 整改 C4：优先复用既有载荷（含审批员终稿修订），仅对缺失语言做 KB 兜底——
	//   此前无条件新建载荷覆写 FinalResult，审批修订在批准重跑时被静默丢弃。
	p := w.loadPayload(t)
	if p == nil || p.Translations == nil {
		p = &ticketPayload{SourceText: t.SourceText, TargetLangs: langs, Translations: map[string]string{}, Sources: map[string]string{}}
	}
	if p.SourceText == "" {
		p.SourceText = t.SourceText
	}
	if len(p.TargetLangs) == 0 {
		p.TargetLangs = langs
	}
	tid := t.TenantID
	srcLang := engine.DetectSourceLang(t.SourceText) // 检测实际源语言（zh/en），用于 KB 匹配与初翻

	// 1. 平台 KB 包四层查找（企业包优先，按层排序，按实际源语言匹配；已有译文不覆盖）
	if w.Store != nil {
		// 整改 R2：按创建人所属部门施加 KB 可见性隔离，避免跨部门读到其他部门私有术语
		orgID := int64(0)
		if t.CreatedBy > 0 {
			if u, uerr := w.Store.GetUser(t.CreatedBy, tid); uerr == nil && u != nil {
				orgID = u.OrgID
			}
		}
		if entries, err := w.Store.FindEntriesBySourceScoped(tid, orgID, srcLang, t.SourceText); err == nil {
			for _, ent := range entries {
				if _, ok := p.Translations[ent.TargetLang]; ok {
					continue // 高优包/高层/既有译文已命中
				}
				if strings.TrimSpace(ent.TargetText) == "" {
					continue // 空译文跳过
				}
				p.Translations[ent.TargetLang] = ent.TargetText
				p.Sources[ent.TargetLang] = SourceKB // 标记来源为知识库
			}
		}
		// ★ 术语子串匹配（2026-09-09 硬闸 RAG 治本）：整段精确匹配无法命中嵌在长句里的
		//   单条 L1 术语（如「山海无界，极石致远」中的「极石」）。对源文做术语子串检索，
		//   把命中的术语条目并入 Examples，供 ai_initial 初翻注入 prompt 强制遵循
		//   （知识库已定义 极石→ar→ROX / 极石→ru→ROX，此前模型自由发挥成 «جي شي» (ROX)）。
		if terms, terr := w.Store.FindTermsBySubstring(tid, orgID, srcLang, t.SourceText); terr == nil && len(terms) > 0 {
			seen := map[string]bool{}
			for _, term := range terms {
				if term == nil || strings.TrimSpace(term.SourceText) == "" {
					continue
				}
				// ★ O-04（2026-10-24）：子串命中会带出「中文/验收/翻译」这类泛词与 40+ 条跨语种噪声。
				//   只保留真正像术语的（去泛词/短词），并封顶 30 条；prompt 侧另有 5 条上限。
				if !usefulTermSource(term.SourceText) {
					continue
				}
				if seen[term.SourceText+"|"+term.TargetLang] {
					continue
				}
				seen[term.SourceText+"|"+term.TargetLang] = true
				if len(p.Examples) >= 30 {
					break
				}
				p.Examples = append(p.Examples, &kb.Row{
					Zh:     term.SourceText,
					Module: term.Module,
					Langs:  map[string]string{term.TargetLang: term.TargetText},
				})
			}
		}
	}

	// 2. engine 兜底（npz 语义库），只对缺失语言取 KB 命中（langOnly=true 不调模型）
	missing := []string{}
	for _, lc := range langs {
		if strings.TrimSpace(p.Translations[lc]) == "" {
			missing = append(missing, lc)
		}
	}
	if len(missing) > 0 {
		ctx = tenant.WithTenant(ctx, tid) // 注入租户上下文供引擎租户隔离
		res, _ := w.Engine.TranslateOne(ctx, t.SourceText, missing, true, config.StageKBMatch)
		if res != nil {
			// ★ 治本整改：KB 命中例句（res.Examples）随载荷持久化，供 ai_initial 初翻注入术语参考——
			//   此前只抄 Translations/Mode，例句被丢弃，KB「部分命中」的术语（极石→ROX、车主→owner）
			//   从未进入 AI prompt，直接丢给模型自由发挥。
			if len(res.Examples) > 0 {
				p.Examples = res.Examples
			}
		}
		for lc, v := range res.Translations {
			if strings.TrimSpace(v) == "" || strings.TrimSpace(p.Translations[lc]) != "" {
				continue // 空命中跳过 / 已有 KB 或人工译文不覆盖
			}
			p.Translations[lc] = v
			p.Sources[lc] = SourceKB
		}
		if p.Mode == "" && res != nil {
			p.Mode = res.Mode
		}
	} else if p.Mode == "" {
		p.Mode = "知识库匹配"
	}
	w.savePayload(t, p)
	return nil
}

// payloadHasTranslation ★ F-42-b（2026-09-25 UAT 修复批）：载荷里是否存在任一非空译文。
// 用途：runAIInitial 的「人工驳回重翻」分支前置判据（双保险之一）。载荷全空时重翻循环
// 逐语言 continue、零 LLM 调用后 return nil，等于向流水线谎报「本步成功」，末尾被刷成
// completed 而产物为空（OpenAPI 侧照发空 translations）。空载荷必须落回正常翻译分支。
// 参数：p=工单中间载荷（nil 视为无译文）。返回：true=至少有一种语言的译文非空。
func payloadHasTranslation(p *ticketPayload) bool {
	if p == nil {
		return false
	}
	for _, tr := range p.Translations {
		if strings.TrimSpace(tr) != "" {
			return true
		}
	}
	return false
}

// runAIInitial AI 初翻（对缺失语言模型翻译；被驳回工单则按驳回意见重翻全部）。
// 参数：ctx=上下文，t=工单对象。
func (w *Workflow) runAIInitial(ctx context.Context, t *store.Ticket) error {
	p := w.loadPayload(t)
	if p == nil {
		// ★ kb_match 步骤被跳过时（fast 模式 / 租户关闭该步）由此自建空载荷，
		// 初翻对全部目标语言生效——不再硬性依赖前置 KB 步骤。
		langs := parseTicketLang(t.TargetLangs)
		if len(langs) == 0 {
			langs = []string{"en"}
		}
		p = &ticketPayload{
			SourceText:   t.SourceText,
			TargetLangs:  langs,
			Translations: map[string]string{},
			Sources:      map[string]string{},
			Mode:         "无知识库直翻",
		}
	}
	tid := t.TenantID
	ctx = tenant.WithTenant(ctx, tid)
	srcLang := engine.DetectSourceLang(t.SourceText) // 源语言（用于初翻指令方向）

	// 驳回重翻循环：按驳回意见重新翻译全部语言
	// ★ F-42-b（2026-09-25 UAT 修复批）判据由「reject_reason 非空」收紧为三条件：
	//   ① 原因非空 ② 来源确为人工驳回（reject_source='human'）③ 载荷里确有可重翻译文。
	//   旧判据只看非空：系统失败原因（'context canceled'/'步骤 X 失败'）会被当成审批意见
	//   喂给模型；而载荷全空时循环逐语言 continue、一次 LLM 都不调，随后 savePayload +
	//   return nil，流水线后半段照常把工单刷成 completed —— 生产任务 90 的假 completed 即此环。
	//   非 human 来源或空载荷一律落回下方正常流程（对缺失语言全量翻译），绝不空转返回成功。
	if strings.TrimSpace(t.RejectReason) != "" && t.RejectSource == store.RejectSourceHuman && payloadHasTranslation(p) {
		for _, lc := range p.TargetLangs {
			if strings.TrimSpace(p.Translations[lc]) == "" {
				continue
			}
			rev := w.Engine.TranslateWithFeedback(ctx, p.SourceText, lc, t.RejectReason, config.StageAIInitial)
			if rev != "" {
				p.Translations[lc] = rev
				p.Sources[lc] = SourceModel // 来源标记为模型
			}
		}
		w.savePayload(t, p)
		return nil
	}

	// ★ 2026-10-30：文本工单链**第一次**接领域词表（此前只进「即时/工作台」与「文件/图片」两条链；
	//   现网读数＝同一次探针里即时链有 domain_glossary、这条腿 0 行）。
	//   位置在**两条腿分叉之前**、每单只判一次（样本＝整单源文、语种＝全部目标语种）：
	//     · 整篇腿 `TranslateLangsInto` → `singleLangRaw` 自己从 ctx 读词表（已经有了）；
	//     · 逐行腿 `translateTextPerLine` → 批量层，那里在 opts 为空时**回落 ctx**。
	//   开关关/判不出 ⇒ 不挂 ctx（下游逐字不变）。
	if prof, maxTerms := w.Engine.PrepareTicketGlossary(ctx, p.SourceText, p.TargetLangs); prof != nil {
		ctx = engine.WithGlossary(ctx, prof, maxTerms)
	}

	// ★ DEF-02（2026-10-24）：文本工单逐行翻译/增量补译。
	//   - 首次：按 \n 切行，非空行批量翻译，行数保持；
	//   - 重发：读回旧 payload，正确行复用，只把缺失/错误行送模型；
	//   - 单段或配置 text_ticket_per_line=0 时返回 false，走下方旧整篇路径。
	if w.translateTextPerLine(ctx, p) {
		w.savePayload(t, p)
		return nil
	}

	// 正常流程：只翻译缺失语言（★ 治本整改：把 kb_match 阶段收集的 KB 命中例句注入初翻，
	// 使模型在翻译时沿用知识库标准术语译法——此前传 nil，KB 部分命中从未进入 prompt）
	var need []string
	for _, lc := range p.TargetLangs {
		if strings.TrimSpace(p.Translations[lc]) == "" {
			need = append(need, lc)
		}
	}
	if len(need) > 0 {
		w.Engine.TranslateLangsInto(ctx, t.SourceText, need, p.Examples, p.Translations, p.Sources, srcLang, config.StageAIInitial)
	}
	w.savePayload(t, p)
	return nil
}

// StructureRetryNeeded 文本工单结构失败时决定是否自动整单重发。
// 返回 true 表示**计数已在库里加过一格**，调用方应 requeue + enqueue。
// 只认 qa 的 `structure` error；其他 QA error（数字/占位符）不触发整单重发（由既有硬闸/重译链处理）。
//
// ★ 2026-10-25 对账（§10.1.2）：计数从载荷字段换成 `tickets.repair_attempts` 列，四条理由：
//
//	① 运维一句 `WHERE repair_attempts>=3` 就能数出「哪些单在被打」，不必解析 final_result JSON；
//	② 计数与译文挤在同一段 blob 里 ⇒ 任何一次「整段重写载荷」（人工重跑、驳回重翻、补译）
//	   都会顺手把它抹回 0，上限变软、坏单无限重发真扣钱；
//	③ 列是唯一事实源，载荷里那个字段已随本次改动删除（不留第二把尺子）；
//	④ 读不到/加不上就**不重发**：这一支的失败语义必须是「停止花钱」，
//	   「计数写不上还照发」等于把上限摘掉后继续扣费，比少发一单严重。
func (w *Workflow) StructureRetryNeeded(t *store.Ticket) bool {
	if w.Store == nil || t == nil || t.ID == 0 {
		return false // 没有 store 就没有计数口，宁可不发也不发一条没人记账的单
	}
	p := w.loadPayload(t)
	if p == nil || p.Translations == nil {
		return false
	}
	// 先判问题类型再看次数：非 structure 的 error 一律不许吃掉一格预算。
	rep := qa.Check(p.SourceText, p.Translations)
	hit := false
	for _, iss := range rep.Issues {
		if iss.Rule == "structure" {
			hit = true
			break
		}
	}
	if !hit {
		return false
	}
	// 0 是一档**真实档位**（＝不重发，应急回退开关），所以走 ConfigIntAllowZero 而不是
	// ConfigInt——后者把 ≤0 读成"没配"并回落默认，运营在管理台填 0 也按不下这个按钮。
	max := w.Store.ConfigIntAllowZero("ticket_repair_max", 3)
	cur, err := w.Store.TicketRepairAttempts(t.ID)
	if err != nil || cur >= max {
		return false
	}
	if _, err = w.Store.IncTicketRepairAttempts(t.ID); err != nil {
		return false
	}
	return true
}

// runEvalsInitial 初翻评估。
// 参数：ctx=上下文，t=工单对象；对每语言译文调用 Judge 评分并保存记录。
func (w *Workflow) runEvalsInitial(ctx context.Context, t *store.Ticket) error {
	p := w.loadPayload(t)
	if p == nil {
		return nil
	}
	if w.Engine.Evals == nil {
		return nil // 未初始化评估器则跳过
	}
	for lc, tr := range p.Translations {
		if tr == "" {
			continue
		}
		// 抽样率控制（成本敏感）：未命中抽样则跳过本次 Judge（整改 R5）
		if !w.Engine.Evals.ShouldSample() {
			continue
		}
		total, scores, err := w.Engine.Evals.Evaluate(ctx, p.SourceText, tr, lc, "translate")
		if err == nil && p.EvalScores == nil {
			p.EvalScores = map[string]float64{}
		}
		if err == nil {
			p.EvalScores[lc] = total // 记录总分
			// ★ 改造 4：低于阈值 → 打标 + 运营提醒（返回 failed 供评估记录落库）
			disp := w.applyEvalDisposition(t, p, lc, total, "initial")
			_, _ = w.Engine.Evals.SaveRecord(ctx, t.TenantID, t.CreatedBy, t.ID, "translate", lc, p.SourceText, tr, scores, total, disp)
		}
	}
	w.savePayload(t, p)
	return nil
}

// runReview 审校 Agent（对已有译文用 LLM 审校，修正术语/语法）。
// 参数：ctx=上下文，t=工单对象。
func (w *Workflow) runReview(ctx context.Context, t *store.Ticket) error {
	p := w.loadPayload(t)
	if p == nil {
		return nil
	}
	// ★ DEF-02：文本工单逐行审校（保留行数）；单段/关闭开关时走下方旧整篇 review。
	if w.reviewTextPerLine(ctx, p) {
		w.savePayload(t, p)
		return nil
	}
	for lc, tr := range p.Translations {
		if tr == "" {
			continue
		}
		revised := w.Engine.ReviewTranslation(ctx, p.SourceText, tr, lc, config.StageReview)
		if revised != "" {
			p.Translations[lc] = revised // 用审校结果覆盖
		}
	}
	w.savePayload(t, p)
	return nil
}

// runEvalsReview 校对评估（与初翻评估同流程，但 taskType=review → 使用校对 Evals 模型）。
// 参数：ctx=上下文，t=工单对象。
func (w *Workflow) runEvalsReview(ctx context.Context, t *store.Ticket) error {
	p := w.loadPayload(t)
	if p == nil {
		return nil
	}
	if w.Engine.Evals == nil {
		return nil
	}
	for lc, tr := range p.Translations {
		if tr == "" {
			continue
		}
		// 抽样率控制（成本敏感）：未命中抽样则跳过本次 Judge（整改 R5）
		if !w.Engine.Evals.ShouldSample() {
			continue
		}
		total, scores, err := w.Engine.Evals.Evaluate(ctx, p.SourceText, tr, lc, "review")
		if err == nil && p.ReviewEvalScores == nil {
			p.ReviewEvalScores = map[string]float64{}
		}
		if err == nil {
			p.ReviewEvalScores[lc] = total
			// ★ 改造 4：校对评估同样执行阈值处置（打标 + 提醒，阶段标记 review）
			disp := w.applyEvalDisposition(t, p, lc, total, "review")
			_, _ = w.Engine.Evals.SaveRecord(ctx, t.TenantID, t.CreatedBy, t.ID, "review", lc, p.SourceText, tr, scores, total, disp)
		}
	}
	w.savePayload(t, p)
	return nil
}

// applyEvalDisposition 评估不合格处置（★ 改造 4，2026-09-17）：
// 评估总分低于 system_config.evals_fail_threshold（默认 60；配置为 0 或负数=关闭处置仅记录。
// 注意不用 ConfigInt——其既有约定把 <=0 回落默认值，与本开关语义冲突）时：
//  1. 该语言计入 payload.QualityFlaggedLangs（工单详情「质检存疑」徽标数据源）；
//  2. 告警中心（alerts 表，kind=eval_quality，幂等去重）+ 群机器人四渠道提醒运营；
//     可用 system_config.evals_alert_enabled=0 单独关掉「提醒」（打标与落库保留）——
//     与 evals_fail_threshold<=0 的「整体关闭处置」是两个粒度；
//  3. 同工单同语言同阶段只提醒一次（payload.EvalNotified 幂等标记）。
//
// 不做自动打回重译——gate 硬闸负责确定性规则重译；Judge 主观分重译易震荡烧钱，仅人工决策。
// 参数：t=工单，p=工单 payload（本函数会修改其打标/提醒字段），lc=目标语言，total=评估总分，
//
//	taskType="initial"（初翻评估）/"review"（校对评估）。
//
// 返回：评估记录 status（"passed"/"failed"）。
func (w *Workflow) applyEvalDisposition(t *store.Ticket, p *ticketPayload, lc string, total float64, taskType string) string {
	const defaultThreshold = 60
	status := "passed"
	if w.Store == nil {
		return status
	}
	// raw 读取（非 ConfigInt）：空=默认 60；解析成功且 <=0 = 处置关闭
	threshold := defaultThreshold
	if raw, err := w.Store.GetConfig("evals_fail_threshold"); err == nil && strings.TrimSpace(raw) != "" {
		if n, perr := strconv.Atoi(strings.TrimSpace(raw)); perr == nil {
			threshold = n
		}
	}
	if threshold <= 0 {
		return status // 处置关闭：仅记录分数，不打标不提醒
	}
	if total >= float64(threshold) {
		return status
	}
	status = "failed"
	// 打标：去重追加不达标语言
	flagged := false
	for _, f := range p.QualityFlaggedLangs {
		if f == lc {
			flagged = true
			break
		}
	}
	if !flagged {
		p.QualityFlaggedLangs = append(p.QualityFlaggedLangs, lc)
	}
	// ★ 改造 4：工单表 quality_flagged=1（列表/详情接口零成本透出，前端「质检存疑」徽标数据源）
	if err := w.Store.SetTicketQualityFlagged(t.ID); err != nil {
		log.Printf("[evals] 质检存疑打标落库失败（不影响评估）: %v", err)
	}
	// ★ 改造 4：提醒单独开关（默认开）。置 0 = 只打标不打扰运营（灰度期静默观察用）。
	// 判定放在幂等标记之前：未真正提醒就不该记 EvalNotified，否则开回开关后这条再也不会提醒。
	if raw, err := w.Store.GetConfig("evals_alert_enabled"); err == nil {
		if v := strings.TrimSpace(raw); v == "0" || strings.EqualFold(v, "false") || strings.EqualFold(v, "off") {
			return status
		}
	}
	// 运营提醒：同单同语言同阶段幂等一次
	key := taskType + ":" + lc
	if p.EvalNotified == nil {
		p.EvalNotified = map[string]bool{}
	}
	if p.EvalNotified[key] {
		return status
	}
	p.EvalNotified[key] = true
	stageLabel := "初翻"
	if taskType == "review" {
		stageLabel = "校对"
	}
	no := t.TicketNo
	if no == "" {
		no = fmt.Sprintf("#%d", t.ID)
	}
	// ⚠️ 这句会进**告警中心、群机器人与站内信**（客户与运营都读得到），所以措辞是对外契约：
	//   本仓没有人工复核环节（2026-10-26 用户明令），旧句尾那半句承诺了一个不存在的动作
	//   ——客户会以为有人在看这一单。现在只报事实：打了什么标、界面出什么徽标。
	msg := fmt.Sprintf("工单 %s 语言 %s %s评估总分 %.1f 低于阈值 %d，已标记「质检存疑」", no, lc, stageLabel, total, threshold)
	if err := w.Store.CreateAlert(t.TenantID, "warning", "eval_quality", msg); err != nil {
		log.Printf("[evals] 质量告警写入失败（不影响处置）: %v", err)
	}
	notify.Bots(w.Store, "翻译质量低于阈值", msg)
	log.Printf("[evals] %s", msg)
	return status
}

// kbTermHits 按源文子串匹配 L1 术语（同 runKBMatch 的口径，供硬闸校验与重译参考复用）。
// 参数：t=工单对象，lc=目标语言。返回：术语要求列表（gate 校验用）与术语参考行（重译 prompt 用）。
func (w *Workflow) kbTermHits(t *store.Ticket, lc string) ([]gate.TermRequirement, []*kb.Row) {
	var reqs []gate.TermRequirement
	var rows []*kb.Row
	if w.Store == nil {
		return reqs, rows
	}
	srcLang := engine.DetectSourceLang(t.SourceText)
	orgID := int64(0)
	if t.CreatedBy > 0 {
		if u, uerr := w.Store.GetUser(t.CreatedBy, t.TenantID); uerr == nil && u != nil {
			orgID = u.OrgID
		}
	}
	if ents, err := w.Store.FindTermsBySubstring(t.TenantID, orgID, srcLang, t.SourceText); err == nil {
		for _, ent := range ents {
			if ent == nil || strings.TrimSpace(ent.SourceText) == "" || strings.TrimSpace(ent.TargetText) == "" {
				continue
			}
			if ent.TargetLang != lc {
				continue // 只取当前语言的术语要求
			}
			reqs = append(reqs, gate.TermRequirement{Source: ent.SourceText, Target: ent.TargetText})
			rows = append(rows, &kb.Row{
				Zh:     ent.SourceText,
				Module: ent.Module,
				Langs:  map[string]string{ent.TargetLang: ent.TargetText},
			})
		}
	}
	return reqs, rows
}

// runGate 8 项硬校验 + KB 术语遵循校验。校验失败时自动附 KB 提示重译（硬闸护栏，≤ gate_retry_max 次），
// 仍失败才返回错误置 rejected。
// 参数：ctx=上下文，t=工单对象。
func (w *Workflow) runGate(ctx context.Context, t *store.Ticket) error {
	p := w.loadPayload(t)
	if p == nil {
		return fmt.Errorf("缺少翻译结果")
	}
	maxRetry := w.gateRetryMax()
	if p.RetryCount == nil {
		p.RetryCount = map[string]int{}
	}
	if p.GateHints == nil {
		p.GateHints = map[string]string{}
	}
	for lc, tr := range p.Translations {
		if tr == "" {
			continue
		}
		// KB 术语要求（硬闸第 9 项）：源文命中术语须在译文中体现
		termReqs, termRows := w.kbTermHits(t, lc)
		// 硬闸循环：校验 → 失败则附 KB 提示重译 → 再校验，直到通过或达到上限
		cursor := tr
		// ★ H1：首轮前先做术语强制覆写（译文残留源术语字面时零成本合规）
		if forced, n := gate.ForceTerms(p.SourceText, cursor, termReqs); n > 0 {
			cursor = forced
			log.Printf("[gate] 工单 %d/%s 术语强制替换 %d 处", t.ID, lc, n)
		}
		for attempt := 0; attempt <= maxRetry; attempt++ {
			g := gate.RunWithTerms(p.SourceText, lc, cursor, termReqs)
			if g.Pass {
				p.Gate = g
				p.Translations[lc] = cursor
				break
			}
			p.Gate = g
			reason := fmt.Sprintf("Gate 校验失败 [%s]: %s", lc, firstFail(g.Checks))
			p.GateHints[lc] = reason
			// ★ 数字类失败短重试（★ 2026-10-10 批次 ⑫，现网实证工单 T20261010132330UEM）：
			//   本次失败 Checks 命中「数字保持」时，该语言本轮重译上限压到 2 次——数字类失败
			//   要么重译立刻改对、要么是模型对数字形态系统性失能，8 轮只是白烧钱；
			//   其余检查项失败仍走 gate_retry_max。上限取 min(2, gate_retry_max)：
			//   运营把 gate_retry_max 配得更小（如 1）时尊重更小值，保证「已自动重译 N 次」
			//   文案如实反映该轮实际用的上限。
			roundMax := maxRetry
			if gateNumberFail(g.Checks) && roundMax > gateNumberRetryMax {
				roundMax = gateNumberRetryMax
			}
			if attempt >= roundMax {
				return fmt.Errorf("%s（已自动重译 %d 次仍不通过）", reason, roundMax)
			}
			feedback := gateRetranslateFeedback(g.Checks)
			rev := w.retranslateWithKB(ctx, t, lc, cursor, feedback, termRows)
			if rev == "" {
				return fmt.Errorf("%s，且附带 KB 提示重译失败", reason)
			}
			p.RetryCount[lc] = attempt + 1
			p.Translations[lc] = rev
			p.Sources[lc] = SourceModel
			cursor = rev
		}
	}
	w.savePayload(t, p)
	return nil
}

// runCultureGate 语言文化包输出闸门（反查译文）。命中避雷/数字/语气不合规则时，
// 附 KB 安全句提示自动重译（硬闸护栏，≤ gate_retry_max 次），仍失败才返回错误。
// 参数：ctx=上下文，t=工单对象。
func (w *Workflow) runCultureGate(ctx context.Context, t *store.Ticket) error {
	p := w.loadPayload(t)
	if p == nil {
		return nil
	}
	// 语言文化包按租户读取，含待审审批后热加载的已批准安全句
	safety, _ := w.Store.ListSafetyPhrasesFilter(t.TenantID, "approved")
	maxRetry := w.gateRetryMax()
	if p.RetryCount == nil {
		p.RetryCount = map[string]int{}
	}
	if p.GateHints == nil {
		p.GateHints = map[string]string{}
	}
	for lc, tr := range p.Translations {
		if tr == "" {
			continue
		}
		cursor := tr
		for attempt := 0; attempt <= maxRetry; attempt++ {
			c := culture.Run(lc, cursor, safety)
			if c.Pass {
				p.Culture = c
				p.Translations[lc] = cursor
				break
			}
			p.Culture = c
			reason := fmt.Sprintf("语言文化闸门打回 [%s]: %s", lc, strings.Join(c.Reasons, ";"))
			// ★ 2026-10-24：同一原因连续两次即停——确定性失败（某个避雷词模型每轮都踩）
			//   重译 8 次只会烧钱，不会再改结果。首次失败照常把原因写入 hint，第二轮相同即停。
			if attempt > 0 && p.GateHints[lc] == reason {
				p.GateHints[lc] = reason + "（同一原因重复，停止自动重译）"
				return fmt.Errorf("%s（同一原因重复，已停止自动重译）", reason)
			}
			p.GateHints[lc] = reason
			if attempt >= maxRetry {
				return fmt.Errorf("%s（已自动重译 %d 次仍不通过）", reason, maxRetry)
			}
			rev := w.retranslateWithKB(ctx, t, lc, cursor, reason, nil)
			if rev == "" {
				return fmt.Errorf("%s，且附带 KB 提示重译失败", reason)
			}
			p.RetryCount[lc] = attempt + 1
			p.Translations[lc] = rev
			p.Sources[lc] = SourceModel
			cursor = rev
		}
	}
	w.savePayload(t, p)
	return nil
}

// gateRetryMax 读取硬闸自动重译上限（默认 8 次）。
// 参数：无；返回最大重译次数。
func (w *Workflow) gateRetryMax() int {
	if w.Store != nil {
		return w.Store.ConfigInt("gate_retry_max", 8)
	}
	return 8
}

// gateNumberRetryMax 数字类失败（「数字保持」未过）的短重译上限：2 次。
// 现网实证（工单 T20261010132330UEM）：数字形态换算类失败重译 8 轮全部同样死法，
// 纯烧钱不解决问题；2 次已足够覆盖「模型偶发写错」的正常抖动。
const gateNumberRetryMax = 2

// gateNumberFail 判断本次硬闸失败项里是否有「数字保持」未过（数字类失败识别，
// 供 runGate 对该语言本轮压短重译上限）。检查项名引用 gate.CheckNumberKeep 常量，
// 与 gate 包的对外文案同源，避免两处字面量漂移。
func gateNumberFail(checks []gate.Check) bool {
	for _, c := range checks {
		if !c.Pass && c.Name == gate.CheckNumberKeep {
			return true
		}
	}
	return false
}

// retranslateWithKB 附 KB 提示重译单语言译文（硬闸护栏核心）。
// 依据 Gate 失败项/语言文化打回原因 + 命中的 KB 参考（源文本在租户 KB 中的标准译法），
// 用 TranslateWithFeedbackEx 修正重译。参数：ctx=上下文，t=工单对象（取创建人/租户做
// KB 可见性隔离），lc=目标语言，tr=当前译文，reason=打回原因，extraRows=调用方已命中的
// 术语参考行（如 runGate 术语遵循打回时注入的 KB 术语，重译时必须沿用）；返回修正后译文（失败返回 ""）。
func (w *Workflow) retranslateWithKB(ctx context.Context, t *store.Ticket, lc, tr, reason string, extraRows []*kb.Row) string {
	// KB 在知识库匹配阶段已按租户/部门隔离；此处取源文本命中的标准译法作为重译参考
	var examples []*kb.Row
	examples = append(examples, extraRows...)
	if w.Store != nil {
		srcLang := engine.DetectSourceLang(t.SourceText)
		orgID := int64(0)
		if t.CreatedBy > 0 {
			if u, uerr := w.Store.GetUser(t.CreatedBy, t.TenantID); uerr == nil && u != nil {
				orgID = u.OrgID
			}
		}
		if ents, err := w.Store.FindEntriesBySourceScoped(t.TenantID, orgID, srcLang, t.SourceText); err == nil {
			for _, ent := range ents {
				if strings.TrimSpace(ent.TargetText) == "" {
					continue
				}
				examples = append(examples, &kb.Row{
					Zh:     ent.SourceText,
					Module: ent.Module,
					Langs:  map[string]string{ent.TargetLang: ent.TargetText},
				})
			}
		}
	}
	return w.Engine.TranslateWithFeedbackEx(ctx, t.SourceText, lc, reason, config.StageAIInitial, examples)
}

// gateRetranslateFeedback 把 Gate 校验失败项拼成重译修正意见。
// 参数：checks=Check 失败项列表；返回意见字符串（如 "存在残留乱码、数字未保持"）。
func gateRetranslateFeedback(checks []gate.Check) string {
	var fails []string
	for _, c := range checks {
		if !c.Pass {
			if c.Detail != "" {
				fails = append(fails, c.Name+":"+c.Detail)
			} else {
				fails = append(fails, c.Name)
			}
		}
	}
	if len(fails) == 0 {
		return "请重新按原文准确翻译"
	}
	return strings.Join(fails, "；")
}

// runQA 确定性质检：对最终译文跑纯规则检查（数字/占位符/漏翻等），
// 报告写入 payload 的 qa_report 供下载对照表与审批参考；不阻断流程（error 由人工审批环节裁决）。
// 参数：ctx=上下文，t=工单对象。
func (w *Workflow) runQA(ctx context.Context, t *store.Ticket) error {
	p := w.loadPayload(t)
	if p == nil || len(p.Translations) == 0 {
		return nil
	}
	p.QAReport = qa.Check(p.SourceText, p.Translations)
	w.savePayload(t, p)
	// ★ 改造 5（2026-09-17）：质检摘要落 tickets 列，供工单列表零成本渲染质检徽标
	//   （此前前端全仓零透出，用户仅下载 xlsx 才能看到 QA 列）。
	if err := w.Store.SetTicketQASummary(t.ID, p.QAReport.Errors, p.QAReport.Warnings); err != nil {
		log.Printf("[qa] 质检摘要落库失败（不影响质检报告）: %v", err)
	}
	return nil
}

// runApproval 转人工审批。
// 参数：ctx=上下文，t=工单对象；已批准/已完成则直接返回，否则置为待审批。
func (w *Workflow) runApproval(ctx context.Context, t *store.Ticket) error {
	if t.Status == store.TicketApproved || t.Status == store.TicketCompleted {
		return nil // 已被批准/完成则跳过审批
	}
	t.Status = store.TicketPendingAppr
	return w.Store.UpdateTicket(t)
}

// runFeedback 审批批准后自迭代写库（按企业包写入 tm_segments）。
// 参数：ctx=上下文，t=工单对象；仅在工单已批准时执行。
func (w *Workflow) runFeedback(ctx context.Context, t *store.Ticket) error {
	if t.Status != store.TicketApproved {
		return nil // 未批准不回写
	}
	p := w.loadPayload(t)
	if p == nil || w.KB == nil {
		return nil
	}
	// 写入企业包（tm_segments 归租户）
	// ★ H4 TM 自动审核：反馈句先过 QA 预筛（数字/占位符/长度比/未翻译），
	//   达标直写正式 TM；低分转 tm_review 人审池（人工批准后经现有
	//   handleTmReviewApprove 链路入库），避免带错误终稿沉淀进翻译记忆。
	thr := config.C.TMFeedbackMinQAScore
	if thr <= 0 {
		thr = 80
	}
	auto, reviewed, skipped := 0, 0, 0
	for lc, tr := range p.Translations {
		if tr == "" {
			continue
		}
		// ★ 2026-10-26 用户令①「不留白、不整句丢失」的**直接副产品**（本轮实测抓出的新通道，必须同批堵）：
		// 回填过原文的语种**一条都不写 TM**。这一对里混着原文，写进去就是「中文 → 中文」这种对子，
		// 此后每次命中都直出＝把一次上游抖动固化成客户记忆里长期复发——比漏一行坏（漏一行只坏这一单）。
		// ⚠️ 为什么现成的预筛**拦不住**这一档（读数逐条实证，不是推测）：
		//   · `qa/qa.go:94` 那条同文规则的 `Level` 是 **`warning`**，不是 error；
		//   · `qa/screener.go:20` 打分＝`100 - 40*Errors - 8*Warnings` ⇒ 同文整篇仍拿 **92 分**；
		//   · 阈值 `tm_feedback_min_qa_score` 默认 80（上面那段 `thr <= 0 → 80`）⇒ 92 判"达标直写"。
		//   旧形态这里没有回填行所以走不到；本轮"绝不留空行"落地后回填成为常态，这条路就从没被走到变成常走。
		// 粒度＝**整语种跳过**：这一对是整篇配对的，个别行混原文也不构成干净对子，
		// 宁可少沉淀 11 行好译文，不许沉淀 1 行原文冒充译文（沉淀的代价是复发，少沉淀的代价只是一次没学到）。
		if p.UnresolvedLines[lc] > 0 {
			skipped++
			continue
		}
		res := qa.ScreenPair(p.SourceText, tr)
		if res.Score >= thr {
			_, _ = w.KB.SaveBack(p.SourceText, map[string]string{lc: tr}, "approved", t.TenantID)
			if w.Engine != nil {
				w.Engine.InvalidateKBCaches() // ★ 审批译文入正式 TM：失效 CJK 缓存保即时可见
			}
			auto++
			continue
		}
		// 低分 → 人审池（同对已有 pending/approved 时跳过，防重复审稿）
		reviewed++
		if w.Store != nil && !w.Store.HasActiveTmReview(t.TenantID, p.SourceText, lc, tr) {
			_ = w.Store.CreateTmReview(&store.TmReview{
				TenantID: t.TenantID, Zh: p.SourceText, Lang: lc, Trans: tr,
				Source: "feedback", RefType: "ticket", RefID: t.ID, Status: "pending",
			})
		}
	}
	if w.Store != nil && (auto > 0 || reviewed > 0 || skipped > 0) {
		// `跳过N语种` 必须出现在这一行里：回填跳过是一条**故意什么都不写**的分支，
		// 不留痕就与「这一单没触发反馈写库」在日志上同形（本仓反复踩过的"静默少写"那一族）。
		w.Store.LogAudit(t.TenantID, 0, "tm_feedback_screen", "tickets",
			fmt.Sprintf("%s 自动入库%d句/转人审%d句/跳过%d语种(含原文回填，不写TM)(阈值%d)", t.TicketNo, auto, reviewed, skipped, thr))
	}
	t.Status = store.TicketCompleted // 写库完成置工单为已完成
	_ = w.Store.UpdateTicket(t)
	return nil
}

// loadPayload 从工单 FinalResult 解析中间结果 JSON。
// 参数：t=工单对象；返回中间结果结构体（无结果/解析失败返回 nil）。
func (w *Workflow) loadPayload(t *store.Ticket) *ticketPayload {
	if t.FinalResult == "" {
		return nil
	}
	var p ticketPayload
	if err := json.Unmarshal([]byte(t.FinalResult), &p); err != nil {
		return nil
	}
	return &p
}

// firstFail 返回校验列表中的首个失败项描述。
// 参数：checks=Gate 校验项列表；返回首个失败项的 "名称: 详情"；
// Detail 为空时只出名称（旧形态无条件拼 ": " 会产出「数字保持: 」这类悬挂冒号）。
func firstFail(checks []gate.Check) string {
	for _, c := range checks {
		if !c.Pass {
			if c.Detail == "" {
				return c.Name
			}
			return c.Name + ": " + c.Detail
		}
	}
	return ""
}

// Ensure llm 引用（Evals 使用）
var _ = llm.NewClient
