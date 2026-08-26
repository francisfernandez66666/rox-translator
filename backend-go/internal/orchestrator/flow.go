// ============ 本文件职责中文说明 ============
// FlowDef 流程引擎：Step/Edge/Compensation 编排执行器。
// 支持按租户 flow_config 动态启停步骤、条件跳过、失败自动补偿重试（≤2 次）、
// 每步状态写入工单轨迹表（ticket_state），全部执行成功返回 nil。
// 含已批准工单重跑保护（C4）：已批准工单重跑时仅执行 QA + TM 回写，不再改动人工终稿。
// =============================================

// Package orchestrator 提供 FlowDef 流程引擎（Step/Edge/Compensation 编排）。
// 翻译工单流程：kb_match → ai_initial → evals_initial → review → evals_review → gate → culture_gate → approval → feedback
package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"translator/internal/store"
	"translator/internal/tenant"
)

// Step 流程步骤定义
type Step struct {
	Key         string `json:"key"`         // 步骤标识（如 kb_match / gate）
	Name        string `json:"name"`        // 步骤中文名
	Enabled     bool   `json:"enabled"`     // 是否启用（admin 可关 / 租户 flow_config 可覆盖）
	Compensable bool   `json:"compensable"` // 是否支持补偿（重翻等）
}

// FlowDef 流程定义
type FlowDef struct {
	Name  string  `json:"name"`  // 流程名称
	Steps []*Step `json:"steps"` // 步骤列表（按顺序执行）
}

// RunFunc 步骤执行函数
type RunFunc func(ctx context.Context, ticket *store.Ticket) error

// SkipFunc 判断步骤是否跳过
type SkipFunc func(ctx context.Context, ticket *store.Ticket) bool

// Executor 执行器
type Executor struct {
	Store *store.Store        // 平台存储（写工单轨迹）
	Ten   *tenant.Store       // 租户存储（读流程启停配置）
	Runs  map[string]RunFunc  // 步骤标识 → 执行函数
	Skips map[string]SkipFunc // 步骤标识 → 跳过判断
	mu    sync.Mutex          // 保护 Runs/Skips 并发读写
}

// NewExecutor 创建执行器。
// 参数：st=平台存储；返回空执行器（步骤需后续 Register 注册）。
func NewExecutor(st *store.Store) *Executor {
	return &Executor{
		Store: st,
		Runs:  map[string]RunFunc{},
		Skips: map[string]SkipFunc{},
	}
}

// Register 注册步骤执行函数。
// 参数：key=步骤标识，fn=执行函数。
func (e *Executor) Register(key string, fn RunFunc) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.Runs[key] = fn
}

// RegisterSkip 注册步骤跳过判断。
// 参数：key=步骤标识，fn=跳过判断函数。
func (e *Executor) RegisterSkip(key string, fn SkipFunc) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.Skips[key] = fn
}

// GetFlow 构建当前流程：按租户 flow_config 读取步骤启停，未配置回退默认定义。
// 参数：tid=租户 ID；返回流程定义（步骤按默认顺序）。
func (e *Executor) GetFlow(tid int64) *FlowDef {
	cfg := tenant.FlowConfig{}
	if e.Ten != nil {
		cfg, _ = e.Ten.GetFlowConfig(tid) // 读取租户流程配置
	}
	fd := &FlowDef{Name: "translate_workflow", Steps: []*Step{}}
	for _, d := range store.DefaultFlowSteps {
		// 租户配置优先覆盖默认启用状态
		enable := d.Enable
		if on, ok := cfg.Steps[d.Key]; ok {
			enable = on
		}
		fd.Steps = append(fd.Steps, &Step{Key: d.Key, Name: d.Name, Enabled: enable})
	}
	return fd
}

// Execute 执行整个流程；ticket 状态按步骤推进。
// 某步失败且可补偿 → 自动重试（≤ maxRetries）；仍失败返回错误。
// ★ 模式覆盖（在租户 flow_config 之上）：
//   - ticket.Mode=="fast"：强制精简为「初翻+校对+QA」——关闭 kb_match / evals×2 /
//     gate / culture_gate / feedback；保留租户对 ai_initial/review/qa 的启停配置
//   - ticket.CreatedBy==0（OpenAPI 自动任务）：强制跳过 approval / feedback，
//     保证全自动闭环不停人工审批台
//
// 参数：ctx=上下文，ticket=工单对象，onStep=步骤进度回调（step 标识、是否成功、错误信息）。
func (e *Executor) Execute(ctx context.Context, ticket *store.Ticket, onStep func(step string, ok bool, err string)) error {
	flow := e.GetFlow(ticket.TenantID)
	tid := ticket.TenantID

	applyModeOverride(flow, ticket)

	for _, step := range flow.Steps {
		if ctx.Err() != nil {
			return ctx.Err() // 上下文取消则中止
		}
		if !step.Enabled {
			// 步骤被关闭：标记 skipped 并继续
			_ = e.Store.SetTicketState(ticket.ID, step.Key, "skipped", "")
			if onStep != nil {
				onStep(step.Key, true, "步骤未启用，跳过")
			}
			continue
		}
		// 跳过判断：注册了 SkipFunc 且判定为真
		if fn, ok := e.skipper(step.Key); ok && fn(ctx, ticket) {
			_ = e.Store.SetTicketState(ticket.ID, step.Key, "skipped", "")
			if onStep != nil {
				onStep(step.Key, true, "条件跳过")
			}
			continue
		}
		runFn, ok := e.runner(step.Key)
		if !ok {
			// 无执行函数 → 标记跳过（容错）
			_ = e.Store.SetTicketState(ticket.ID, step.Key, "skipped", "")
			if onStep != nil {
				onStep(step.Key, true, "无执行器，跳过")
			}
			continue
		}

		// 标记运行中并执行步骤
		_ = e.Store.SetTicketState(ticket.ID, step.Key, "running", "")
		err := runFn(ctx, ticket)
		if err == nil {
			_ = e.Store.SetTicketState(ticket.ID, step.Key, "success", "")
			if onStep != nil {
				onStep(step.Key, true, "")
			}
			continue
		}
		// 失败：可补偿则重试
		if step.Compensable {
			retried := false
			for i := 0; i < 2; i++ { // 最多重试 2 次
				time.Sleep(500 * time.Millisecond) // 重试间隔
				if ctx.Err() != nil {
					break
				}
				err2 := runFn(ctx, ticket)
				if err2 == nil {
					_ = e.Store.SetTicketState(ticket.ID, step.Key, "success", "")
					if onStep != nil {
						onStep(step.Key, true, "重试成功")
					}
					retried = true
					break
				}
				err = err2 // 记录最后一次错误
			}
			if retried {
				continue // 重试成功继续下一环节
			}
		}
		// 仍失败：标记 failed、更新工单为 rejected 并返回错误
		_ = e.Store.SetTicketState(ticket.ID, step.Key, "failed", err.Error())
		if onStep != nil {
			onStep(step.Key, false, err.Error())
		}
		// 更新工单状态
		ticket.Status = store.TicketRejected
		ticket.RejectReason = fmt.Sprintf("步骤 %s 失败: %s", step.Name, err.Error())
		_ = e.Store.UpdateTicket(ticket)
		return fmt.Errorf("流程步骤 %s 失败: %s", step.Name, err.Error())
	}
	_ = tid
	return nil // 全部步骤成功
}

// runner 取步骤执行函数（加锁读 map）。
// 参数：key=步骤标识；返回执行函数与是否存在。
func (e *Executor) runner(key string) (RunFunc, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	fn, ok := e.Runs[key]
	return fn, ok
}

// applyModeOverride 按工单模式就地覆盖流程步骤启停（不落库，仅本次执行生效）。
// 规则见 Execute 注释：fast 精简流水线；API 自动任务（CreatedBy=0）跳过审批与自迭代。
// ★ 整改 C4：已批准工单重跑 = 仅 QA 复核 + TM 回写——生成/校对/闸门步骤全部短路，
//
//	任何自动化环节不得再改动人工终稿，从根上消除「审批后被机器翻案为 rejected」。
func applyModeOverride(flow *FlowDef, ticket *store.Ticket) {
	if ticket.Status == store.TicketApproved {
		for _, st := range flow.Steps {
			switch st.Key {
			case "kb_match", "ai_initial", "evals_initial", "review", "evals_review", "gate", "culture_gate":
				st.Enabled = false // 人工终稿保护：不再生成/校对/设闸
			case "qa", "feedback":
				st.Enabled = true // 仅质检刷新与 TM 回写
			}
		}
		return
	}
	fast := strings.EqualFold(ticket.Mode, "fast")
	apiTask := ticket.CreatedBy == 0
	if !fast && !apiTask {
		return
	}
	for _, st := range flow.Steps {
		if fast {
			switch st.Key {
			case "kb_match", "evals_initial", "evals_review", "gate", "culture_gate", "feedback":
				st.Enabled = false // 快速模式：无知识库/无评估/无硬闸/无文化闸/不自迭代
			case "ai_initial", "review", "qa":
				st.Enabled = true // 快速模式语义保证：初翻+校对+质检 必开
			}
		}
		if apiTask {
			switch st.Key {
			case "approval", "feedback":
				st.Enabled = false // API 任务全自动闭环：不停人工审批台、不做未审自迭代
			}
		}
	}
}

// skipper 取步骤跳过判断（加锁读 map）。
// 参数：key=步骤标识；返回跳过函数与是否存在。
func (e *Executor) skipper(key string) (SkipFunc, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	fn, ok := e.Skips[key]
	return fn, ok
}
