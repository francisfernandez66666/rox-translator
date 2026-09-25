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
	Sources          map[string]string  `json:"sources"`                      // 语言 → 来源（kb/model）
	Mode             string             `json:"mode"`                         // 匹配模式标识
	Examples         []*kb.Row          `json:"examples,omitempty"`           // 知识库命中例句（供 AI 初翻注入术语参考）
	EvalScores       map[string]float64 `json:"eval_scores"`                  // 语言 → 评估总分
	ReviewEvalScores map[string]float64 `json:"review_eval_scores,omitempty"` // 语言 → 校对评估总分
	// ★ 改造 4（2026-09-17）评估不合格处置：低于 evals_fail_threshold（默认 60）的语言
	//   打标（工单详情透出「质检存疑」徽标）+ 告警中心/群机器人提醒（同单同语言同阶段幂等一次）
	QualityFlaggedLangs []string               `json:"quality_flagged_langs,omitempty"` // 评估不达标语言列表
	EvalNotified        map[string]bool        `json:"eval_notified,omitempty"`         // 提醒幂等标记（key=阶段:语言）
	Gate                *gate.GateResult       `json:"gate"`                            // Gate 校验结果
	Culture             *culture.CultureResult `json:"culture,omitempty"`               // 语言文化闸门结果
	QAReport            *qa.Report             `json:"qa_report,omitempty"`             // 确定性 QA 质检报告
	RetryCount          map[string]int         `json:"retry_count,omitempty"`           // 语言 → 硬闸自动重译次数
	GateHints           map[string]string      `json:"gate_hints,omitempty"`            // 语言 → 最近一次硬闸打回原因（供审批参考）
}

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
				p.Sources[ent.TargetLang] = "kb" // 标记来源为知识库
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
				if seen[term.SourceText+"|"+term.TargetLang] {
					continue
				}
				seen[term.SourceText+"|"+term.TargetLang] = true
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
			p.Sources[lc] = "kb"
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
				p.Sources[lc] = "model" // 来源标记为模型
			}
		}
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
	msg := fmt.Sprintf("工单 %s 语言 %s %s评估总分 %.1f 低于阈值 %d，已标记待人工复核", no, lc, stageLabel, total, threshold)
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
			if attempt >= maxRetry {
				return fmt.Errorf("%s（已自动重译 %d 次仍不通过）", reason, maxRetry)
			}
			feedback := gateRetranslateFeedback(g.Checks)
			rev := w.retranslateWithKB(ctx, t, lc, cursor, feedback, termRows)
			if rev == "" {
				return fmt.Errorf("%s，且附带 KB 提示重译失败", reason)
			}
			p.RetryCount[lc] = attempt + 1
			p.Translations[lc] = rev
			p.Sources[lc] = "model"
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
			p.Sources[lc] = "model"
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
	auto, reviewed := 0, 0
	for lc, tr := range p.Translations {
		if tr == "" {
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
	if w.Store != nil && (auto > 0 || reviewed > 0) {
		w.Store.LogAudit(t.TenantID, 0, "tm_feedback_screen", "tickets",
			fmt.Sprintf("%s 自动入库%d句/转人审%d句(阈值%d)", t.TicketNo, auto, reviewed, thr))
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
// 参数：checks=Gate 校验项列表；返回首个失败项的 "名称: 详情"。
func firstFail(checks []gate.Check) string {
	for _, c := range checks {
		if !c.Pass {
			return c.Name + ": " + c.Detail
		}
	}
	return ""
}

// Ensure llm 引用（Evals 使用）
var _ = llm.NewClient
