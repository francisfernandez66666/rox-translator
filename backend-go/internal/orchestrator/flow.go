// Package orchestrator 提供 FlowDef 流程引擎（Step/Edge/Compensation 编排）。
// 翻译工单流程：kb_match → ai_initial → evals_initial → review → evals_review → gate → culture_gate → approval → feedback
package orchestrator

import (
	"context"
	"fmt"
	"sync"
	"time"

	"translator/internal/store"
	"translator/internal/tenant"
)

// Step 流程步骤定义
type Step struct {
	Key      string `json:"key"`
	Name     string `json:"name"`
	Enabled  bool   `json:"enabled"`
	Compensable bool `json:"compensable"` // 是否支持补偿（重翻等）
}

// FlowDef 流程定义
type FlowDef struct {
	Name  string  `json:"name"`
	Steps []*Step `json:"steps"`
}

// RunFunc 步骤执行函数
type RunFunc func(ctx context.Context, ticket *store.Ticket) error

// SkipFunc 判断步骤是否跳过
type SkipFunc func(ctx context.Context, ticket *store.Ticket) bool

// Executor 执行器
type Executor struct {
	Store *store.Store
	Ten   *tenant.Store
	Runs  map[string]RunFunc
	Skips map[string]SkipFunc
	mu    sync.Mutex
}

// NewExecutor 创建执行器
func NewExecutor(st *store.Store) *Executor {
	return &Executor{
		Store: st,
		Runs:  map[string]RunFunc{},
		Skips: map[string]SkipFunc{},
	}
}

// Register 注册步骤执行函数
func (e *Executor) Register(key string, fn RunFunc) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.Runs[key] = fn
}

// RegisterSkip 注册步骤跳过判断
func (e *Executor) RegisterSkip(key string, fn SkipFunc) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.Skips[key] = fn
}

// GetFlow 构建当前流程：按租户 flow_config 读取步骤启停，未配置回退默认定义
func (e *Executor) GetFlow(tid int64) *FlowDef {
	cfg := tenant.FlowConfig{}
	if e.Ten != nil {
		cfg, _ = e.Ten.GetFlowConfig(tid)
	}
	fd := &FlowDef{Name: "translate_workflow", Steps: []*Step{}}
	for _, d := range store.DefaultFlowSteps {
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
func (e *Executor) Execute(ctx context.Context, ticket *store.Ticket, onStep func(step string, ok bool, err string)) error {
	flow := e.GetFlow(ticket.TenantID)
	tid := ticket.TenantID

	for _, step := range flow.Steps {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !step.Enabled {
			_ = e.Store.SetTicketState(ticket.ID, step.Key, "skipped", "")
			if onStep != nil {
				onStep(step.Key, true, "步骤未启用，跳过")
			}
			continue
		}
		// 跳过判断
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
			for i := 0; i < 2; i++ {
				time.Sleep(500 * time.Millisecond)
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
				err = err2
			}
			if retried {
				continue
			}
		}
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
	return nil
}

func (e *Executor) runner(key string) (RunFunc, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	fn, ok := e.Runs[key]
	return fn, ok
}

func (e *Executor) skipper(key string) (SkipFunc, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	fn, ok := e.Skips[key]
	return fn, ok
}