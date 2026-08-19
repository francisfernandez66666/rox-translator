package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"translator/internal/culture"
	"translator/internal/engine"
	"translator/internal/gate"
	"translator/internal/kb"
	"translator/internal/llm"
	"translator/internal/store"
	"translator/internal/tenant"
)

// Workflow 工单翻译工作流：把各业务步骤绑定到 FlowDef 执行器
type Workflow struct {
	Executor *Executor
	Engine   *engine.Engine
	Store    *store.Store
	Tenant   *tenant.Store
	KB       *kb.KBDatabase
}

// NewWorkflow 创建工作流并注册全部步骤执行器
func NewWorkflow(st *store.Store, eng *engine.Engine, ts *tenant.Store, kbdb *kb.KBDatabase) *Workflow {
	w := &Workflow{
		Executor: NewExecutor(st),
		Engine:   eng,
		Store:    st,
		Tenant:   ts,
		KB:       kbdb,
	}
	w.Executor.Ten = ts
	w.registerSteps()
	return w
}

// registerSteps 注册各流程步骤执行函数
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

	// approval：人工审批（工单转为待审批；由审批台决定批准/驳回）
	ex.Register("approval", w.runApproval)

	// feedback：自迭代写库（审批批准后写 KB）
	ex.Register("feedback", w.runFeedback)
}

// 工单翻译中间结果（存于 ticket state payload）
type ticketPayload struct {
	SourceText  string            `json:"source_text"`
	TargetLangs []string          `json:"target_langs"`
	Translations map[string]string `json:"translations"`
	Sources     map[string]string `json:"sources"`
	Mode        string            `json:"mode"`
	EvalScores  map[string]float64 `json:"eval_scores"`
	Gate        *gate.GateResult  `json:"gate"`
	Culture     *culture.CultureResult `json:"culture"`
}

// parseTicketLang 从 target_langs 解析语言列表
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

func (w *Workflow) savePayload(t *store.Ticket, p *ticketPayload) {
	data, _ := json.Marshal(p)
	t.FinalResult = string(data)
	_ = w.Store.UpdateTicket(t)
}

// runKBMatch 知识库匹配
// 四层查找：先查 kb_packages/kb_entries（企业包→行业包按层 L1术语>L2 TM>L3安全句>L4碎片），
// 再交给 engine 的 npz 语义库兜底，缺失语言由模型补齐
func (w *Workflow) runKBMatch(ctx context.Context, t *store.Ticket) error {
	langs := parseTicketLang(t.TargetLangs)
	if len(langs) == 0 {
		langs = []string{"en"}
	}
	p := &ticketPayload{SourceText: t.SourceText, TargetLangs: langs, Translations: map[string]string{}, Sources: map[string]string{}}
	tid := t.TenantID

	// 1. 平台 KB 包四层查找（企业包优先，按层排序）
	if w.Store != nil {
		if entries, err := w.Store.FindEntriesBySource(tid, "zh", t.SourceText); err == nil {
			for _, ent := range entries {
				if _, ok := p.Translations[ent.TargetLang]; ok {
					continue // 高优包/高层已命中
				}
				if strings.TrimSpace(ent.TargetText) == "" {
					continue
				}
				p.Translations[ent.TargetLang] = ent.TargetText
				p.Sources[ent.TargetLang] = "kb"
			}
		}
	}

	// 2. engine 兜底（npz 语义库），只取 KB 命中结果
	ctx = tenant.WithTenant(ctx, tid)
	res, _ := w.Engine.TranslateOne(ctx, t.SourceText, langs, true)
	for lc, v := range res.Translations {
		if strings.TrimSpace(v) == "" {
			continue
		}
		if _, ok := p.Translations[lc]; ok {
			continue
		}
		p.Translations[lc] = v
		p.Sources[lc] = "kb"
	}
	p.Mode = res.Mode
	w.savePayload(t, p)
	return nil
}

// runAIInitial AI 初翻（对缺失语言模型翻译；被驳回工单则按驳回意见重翻全部）
func (w *Workflow) runAIInitial(ctx context.Context, t *store.Ticket) error {
	p := w.loadPayload(t)
	if p == nil {
		return fmt.Errorf("缺少 KB 匹配结果")
	}
	tid := t.TenantID
	ctx = tenant.WithTenant(ctx, tid)

	// 驳回重翻循环：按驳回意见重新翻译全部语言
	if strings.TrimSpace(t.RejectReason) != "" {
		for _, lc := range p.TargetLangs {
			if strings.TrimSpace(p.Translations[lc]) == "" {
				continue
			}
			rev := w.Engine.TranslateWithFeedback(ctx, p.SourceText, lc, t.RejectReason)
			if rev != "" {
				p.Translations[lc] = rev
				p.Sources[lc] = "model"
			}
		}
		w.savePayload(t, p)
		return nil
	}

	var need []string
	for _, lc := range p.TargetLangs {
		if strings.TrimSpace(p.Translations[lc]) == "" {
			need = append(need, lc)
		}
	}
	if len(need) > 0 {
		w.Engine.TranslateLangsInto(ctx, t.SourceText, need, p.Translations, p.Sources)
	}
	w.savePayload(t, p)
	return nil
}

// runEvalsInitial 初翻评估
func (w *Workflow) runEvalsInitial(ctx context.Context, t *store.Ticket) error {
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
		total, scores, err := w.Engine.Evals.Evaluate(ctx, p.SourceText, tr, lc, "translate")
		if err == nil && p.EvalScores == nil {
			p.EvalScores = map[string]float64{}
		}
		if err == nil {
			p.EvalScores[lc] = total
			_, _ = w.Engine.Evals.SaveRecord(ctx, t.TenantID, t.CreatedBy, t.ID, "translate", lc, p.SourceText, tr, scores, total, "passed")
		}
	}
	w.savePayload(t, p)
	return nil
}

// runReview 审校 Agent（对已有译文用 LLM 审校，修正术语/语法）
func (w *Workflow) runReview(ctx context.Context, t *store.Ticket) error {
	p := w.loadPayload(t)
	if p == nil {
		return nil
	}
	for lc, tr := range p.Translations {
		if tr == "" {
			continue
		}
		revised := w.Engine.ReviewTranslation(ctx, p.SourceText, tr, lc)
		if revised != "" {
			p.Translations[lc] = revised
		}
	}
	w.savePayload(t, p)
	return nil
}

// runEvalsReview 审校评估
func (w *Workflow) runEvalsReview(ctx context.Context, t *store.Ticket) error {
	return w.runEvalsInitial(ctx, t)
}

// runGate 8 项硬校验
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
			return fmt.Errorf("Gate 校验失败 [%s]: %s", lc, firstFail(g.Checks))
		}
	}
	return nil
}

// runCultureGate 语言文化包输出闸门（反查译文）
func (w *Workflow) runCultureGate(ctx context.Context, t *store.Ticket) error {
	p := w.loadPayload(t)
	if p == nil {
		return nil
	}
	safety, _ := w.Store.ListSafetyPhrases(t.TenantID)
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

// runApproval 转人工审批
func (w *Workflow) runApproval(ctx context.Context, t *store.Ticket) error {
	if t.Status == store.TicketApproved || t.Status == store.TicketCompleted {
		return nil
	}
	t.Status = store.TicketPendingAppr
	return w.Store.UpdateTicket(t)
}

// runFeedback 审批批准后自迭代写库（按企业包写入）
func (w *Workflow) runFeedback(ctx context.Context, t *store.Ticket) error {
	if t.Status != store.TicketApproved {
		return nil
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
	}
	t.Status = store.TicketCompleted
	_ = w.Store.UpdateTicket(t)
	return nil
}

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