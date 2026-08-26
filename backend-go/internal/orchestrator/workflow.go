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
	"strconv"
	"strings"

	"translator/internal/config"
	"translator/internal/culture"
	"translator/internal/engine"
	"translator/internal/gate"
	"translator/internal/kb"
	"translator/internal/llm"
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
	SourceText       string                 `json:"source_text"`                  // 源文本
	TargetLangs      []string               `json:"target_langs"`                 // 目标语言列表
	Translations     map[string]string      `json:"translations"`                 // 语言 → 译文
	Sources          map[string]string      `json:"sources"`                      // 语言 → 来源（kb/model）
	Mode             string                 `json:"mode"`                         // 匹配模式标识
	EvalScores       map[string]float64     `json:"eval_scores"`                  // 语言 → 评估总分
	ReviewEvalScores map[string]float64     `json:"review_eval_scores,omitempty"` // 语言 → 校对评估总分
	Gate             *gate.GateResult       `json:"gate"`                         // Gate 校验结果
	Culture          *culture.CultureResult `json:"culture"`                      // 语言文化闸门结果
	QAReport         *qa.Report             `json:"qa_report,omitempty"`          // 确定性 QA 质检报告
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
func (w *Workflow) savePayload(t *store.Ticket, p *ticketPayload) {
	data, _ := json.Marshal(p)
	t.FinalResult = string(data)
	// ★ TM 自闭环计数：模型/审校产出的最终译文按 (原文,语言,译文) 累计，达阈值进待审池
	if w.Store != nil && len(p.Translations) > 0 && t.SourceText != "" {
		th := int64(100)
		if v, _ := w.Store.GetConfig("tm_review_threshold"); v != "" {
			if x, e := strconv.ParseInt(v, 10, 64); e == nil && x > 0 {
				th = x
			}
		}
		for lc, tr := range p.Translations {
			if _, _, e := w.Store.BumpTmHit(t.TenantID, t.SourceText, lc, tr, th); e == nil {
			}
		}
	}
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
		if entries, err := w.Store.FindEntriesBySource(tid, srcLang, t.SourceText); err == nil {
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
		for lc, v := range res.Translations {
			if strings.TrimSpace(v) == "" || strings.TrimSpace(p.Translations[lc]) != "" {
				continue // 空命中跳过 / 已有 KB 或人工译文不覆盖
			}
			p.Translations[lc] = v
			p.Sources[lc] = "kb"
		}
		if p.Mode == "" {
			p.Mode = res.Mode
		}
	} else if p.Mode == "" {
		p.Mode = "知识库匹配"
	}
	w.savePayload(t, p)
	return nil
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
	if strings.TrimSpace(t.RejectReason) != "" {
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

	// 正常流程：只翻译缺失语言
	var need []string
	for _, lc := range p.TargetLangs {
		if strings.TrimSpace(p.Translations[lc]) == "" {
			need = append(need, lc)
		}
	}
	if len(need) > 0 {
		w.Engine.TranslateLangsInto(ctx, t.SourceText, need, p.Translations, p.Sources, srcLang, config.StageAIInitial)
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
		total, scores, err := w.Engine.Evals.Evaluate(ctx, p.SourceText, tr, lc, "translate")
		if err == nil && p.EvalScores == nil {
			p.EvalScores = map[string]float64{}
		}
		if err == nil {
			p.EvalScores[lc] = total // 记录总分
			_, _ = w.Engine.Evals.SaveRecord(ctx, t.TenantID, t.CreatedBy, t.ID, "translate", lc, p.SourceText, tr, scores, total, "passed")
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
		total, scores, err := w.Engine.Evals.Evaluate(ctx, p.SourceText, tr, lc, "review")
		if err == nil && p.ReviewEvalScores == nil {
			p.ReviewEvalScores = map[string]float64{}
		}
		if err == nil {
			p.ReviewEvalScores[lc] = total
			_, _ = w.Engine.Evals.SaveRecord(ctx, t.TenantID, t.CreatedBy, t.ID, "review", lc, p.SourceText, tr, scores, total, "passed")
		}
	}
	w.savePayload(t, p)
	return nil
}

// runGate 8 项硬校验。
// 参数：ctx=上下文，t=工单对象；任一语言校验不通过则返回错误。
func (w *Workflow) runGate(ctx context.Context, t *store.Ticket) error {
	p := w.loadPayload(t)
	if p == nil {
		return fmt.Errorf("缺少翻译结果")
	}
	for lc, tr := range p.Translations {
		if tr == "" {
			continue
		}
		g := gate.Run(p.SourceText, lc, tr)
		if !g.Pass {
			// 校验失败：返回首条失败项的详情
			return fmt.Errorf("Gate 校验失败 [%s]: %s", lc, firstFail(g.Checks))
		}
	}
	return nil
}

// runCultureGate 语言文化包输出闸门（反查译文）。
// 参数：ctx=上下文，t=工单对象；译文命中安全句则打回。
func (w *Workflow) runCultureGate(ctx context.Context, t *store.Ticket) error {
	p := w.loadPayload(t)
	if p == nil {
		return nil
	}
	safety, _ := w.Store.ListSafetyPhrases(t.TenantID) // 读取租户安全句
	for lc, tr := range p.Translations {
		if tr == "" {
			continue
		}
		c := culture.Run(lc, tr, safety)
		if !c.Pass {
			return fmt.Errorf("语言文化闸门打回 [%s]: %s", lc, strings.Join(c.Reasons, ";"))
		}
	}
	return nil
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
	for lc, tr := range p.Translations {
		if tr == "" {
			continue
		}
		_, _ = w.KB.SaveBack(p.SourceText, map[string]string{lc: tr}, "approved", t.TenantID)
		if w.Engine != nil {
			w.Engine.InvalidateKBCaches() // ★ 审批译文入正式 TM：失效 CJK 缓存保即时可见
		}
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
