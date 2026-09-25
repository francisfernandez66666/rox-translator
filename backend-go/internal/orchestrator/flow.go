// ============ 本文件职责中文说明 ============
// FlowDef 流程引擎：Step/Edge/Compensation 编排执行器。
// 支持按租户 flow_config 动态启停步骤、条件跳过、失败自动补偿重试（≤2 次）、
// 每步状态写入工单轨迹表（ticket_state），全部执行成功返回 nil。
// 步骤内部 panic 由 runGuarded 兜成该步失败（写 failed 轨迹 + 工单 rejected），
// 绝不让工单停在 running/in_progress 的中间态。
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

	"translator/internal/observability"
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

	bypass := applyModeOverride(flow, ticket)
	// ★ P0-5（2026-09-18）：模式旁路落轨迹（step=mode_override），工单详情/审批台
	// 经 TicketStates 自动可见「本单未经哪些闸门」，旁路不再静默。
	if bypass != "" {
		_ = e.Store.SetTicketState(ticket.ID, "mode_override", "success",
			fmt.Sprintf(`{"bypass":%q,"note":"按工单模式关闭部分流程步骤（设计内旁路，仅供审计与披露）"}`, bypass))
	}

	for _, step := range flow.Steps {
		if ctx.Err() != nil {
			// ★ F-42-a（2026-09-25 UAT 修复批）：cause 不能被压扁。
			//   实时计费的余额耗尽中止走 engine.WithUsageRecorder 的 WithCancelCause
			//   （engine.go:232/236 注入 store.ErrInsufficientBalance），而旧写法返回裸
			//   ctx.Err()＝context.Canceled——真因留在 cause 里没人读，工单只落一条
			//   'context canceled' 的驳回理由：租户看不到「欠费」、OpenAPI 侧取不到错误码、
			//   且该理由随后被 runAIInitial 当成「人工驳回意见」用（F-42 假 completed 第一环）。
			//   context.Cause 有 cause 时返 cause、无 cause 时等价 ctx.Err()，
			//   用户主动取消语义不变（取消态由 service/ticket.go 的 cancelled 守卫先行拦截）。
			return context.Cause(ctx)
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
		err := runGuarded(step.Key, runFn, ctx, ticket)
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
				tm := time.NewTimer(500 * time.Millisecond) // ★ D9：间隔可被取消打断
				select {
				case <-tm.C:
				case <-ctx.Done():
					tm.Stop()
				}
				if ctx.Err() != nil {
					break
				}
				err2 := runGuarded(step.Key, runFn, ctx, ticket)
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
		// ★ F-42-b（2026-09-25 UAT 修复批）：本处写的是系统失败原因，来源必须标 'system'，
		//   否则 runAIInitial 的重翻判据（旧＝reject_reason 非空即人工意见）会在载荷全空时
		//   整轮 continue、零 LLM 调用 return nil，把失败单刷成假 completed。
		ticket.RejectSource = store.RejectSourceSystem
		_ = e.Store.UpdateTicket(ticket)
		// ★ F-42-a：%s → %w 保住错误链，service/ticket.go 侧才能 errors.Is(runErr,
		//   store.ErrInsufficientBalance)（文案逐字不变，通知体与日志无差异）。
		return fmt.Errorf("流程步骤 %s 失败: %w", step.Name, err)
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

// runGuarded 执行单个步骤函数并兜住其 panic（★ #56 装配回归补齐，2026-09-23 评审 §4.4）。
//
// 为什么必须有这层：Execute 此前直接 `runFn(ctx, ticket)`，任一步骤内部 panic 会沿调用栈
// 冒到上游 goroutine（service.runTextTicket 的 worker / admin 手工重跑的 HTTP 处理），后果是
//  1. 该步轨迹永远停在 running、工单状态永远停在 in_progress——既不成功也不失败，
//     只能等 20 分钟卡死巡检（StartStallSweep）重排，用户侧表现为「无限处理中」；
//  2. 后续步骤（含 approval/feedback）连同 onStep 进度回调一起蒸发，审批台看不到任何失败原因。
//
// 现把 panic 折算成与该步骤 `return err` 完全等价的错误值，交给 Execute 既有失败路径处理
// （写 failed 轨迹 + 工单置 rejected + 返回错误），不新增语义分支；与 engine.recoverPipeline
// （文件/文本管线 goroutine 兜底）同一口径：**只兜崩溃，绝不吞掉工单状态更新**。
// 参数：key=步骤标识（仅用于日志与错误文案定位），fn=步骤执行函数。
// 返回：步骤自身错误，或 "步骤内部崩溃: <panic 值>"。
func runGuarded(key string, fn RunFunc, ctx context.Context, ticket *store.Ticket) (err error) {
	defer func() {
		if r := recover(); r != nil {
			observability.Error(ctx, "流程步骤执行 panic 已兜底为失败", "step", key, "ticket_id", ticket.ID, "panic", fmt.Sprint(r))
			err = fmt.Errorf("步骤内部崩溃: %v", r)
		}
	}()
	return fn(ctx, ticket)
}

// applyModeOverride 按工单模式就地覆盖流程步骤启停（不落库，仅本次执行生效）。
// 规则见 Execute 注释：fast 精简流水线；API 自动任务（CreatedBy=0）跳过审批与自迭代。
// ★ 整改 C4：已批准工单重跑 = 仅 QA 复核 + TM 回写——生成/校对/闸门步骤全部短路，
//
//	任何自动化环节不得再改动人工终稿，从根上消除「审批后被机器翻案为 rejected」。
//
// ★ P0-5（2026-09-18）：返回旁路标识（fast/api_task/approved_rerun），由 Execute 写入
//
//	工单步骤轨迹 mode_override 行——旁路上仍是设计内行为，但必须对审批台与租户可见。
func applyModeOverride(flow *FlowDef, ticket *store.Ticket) string {
	if ticket.Status == store.TicketApproved {
		for _, st := range flow.Steps {
			switch st.Key {
			case "kb_match", "ai_initial", "evals_initial", "review", "evals_review", "gate", "culture_gate":
				st.Enabled = false // 人工终稿保护：不再生成/校对/设闸
			case "qa", "feedback":
				st.Enabled = true // 仅质检刷新与 TM 回写
			}
		}
		return "approved_rerun"
	}
	fast := strings.EqualFold(ticket.Mode, "fast")
	apiTask := ticket.CreatedBy == 0
	if !fast && !apiTask {
		return ""
	}
	labels := []string{}
	if fast {
		labels = append(labels, "fast")
		for _, st := range flow.Steps {
			switch st.Key {
			case "kb_match", "evals_initial", "evals_review", "gate", "culture_gate", "feedback":
				st.Enabled = false // 快速模式：无知识库/无评估/无硬闸/无文化闸/不自迭代
			case "ai_initial", "review", "qa":
				st.Enabled = true // 快速模式语义保证：初翻+校对+质检 必开
			}
		}
	}
	if apiTask {
		labels = append(labels, "api_task")
		for _, st := range flow.Steps {
			switch st.Key {
			case "approval", "feedback":
				st.Enabled = false // API 任务全自动闭环：不停人工审批台、不做未审自迭代
			}
		}
	}
	return strings.Join(labels, "+")
}

// skipper 取步骤跳过判断（加锁读 map）。
// 参数：key=步骤标识；返回跳过函数与是否存在。
func (e *Executor) skipper(key string) (SkipFunc, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	fn, ok := e.Skips[key]
	return fn, ok
}
