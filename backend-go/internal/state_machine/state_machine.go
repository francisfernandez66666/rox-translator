// ============ state_machine.go · 流程状态机服务 ============
// 二期 S2 六包平移第二刀：把外仓 internal/state_machine 迁入主仓。
//
// 职责边界（与外仓一致）：只记录流程实例进度；只被流程引擎读写；不暴露 HTTP。
// 行为：读写 + version 乐观锁 + 心跳；巡检由 SweepOnce 负责。
//
// ★ 迁入即换：
//   - 取数入口从外仓 gorm db.DB 换成主仓 *store.Store（双方言 SQL 走 db.Exec/Query）。
//   - 巡检的 requeue 开关与 flow_drive 发布改成可注入接口，便于单测、不绑架主仓 mq/runtimecfg。
//   - 日志全走 observability（零 log.Printf），满足 LG1 棘轮闸。
//   - 业务唯一约束显式带 tenant_id（修复外仓跨租户污染坑）。
//
// ================================================
package state_machine

import (
	"context"
	"errors"
	"strconv"
	"time"

	"translator/internal/observability"
	"translator/internal/store"
)

// ErrVersionConflict 乐观锁版本冲突：并发更新状态机时以此提示调用方重试（F2 必回、不许吞）。
var ErrVersionConflict = errors.New("状态机版本冲突（并发更新），请重试")

// ConfigReader 巡检 requeue 开关读取（注入，便于测试；主仓接 system_config）。
type ConfigReader interface {
	GetBool(key string, def bool) bool
}

// Publisher 超时实例的断点续跑发布（注入；主仓接消息总线）。
type Publisher interface {
	PublishFlowDrive(ctx context.Context, tenantID int64, oneID, instanceID string, taskType string, payload map[string]any) error
}

// Service 流程状态机服务。
type Service struct {
	store *store.Store
	cfg   ConfigReader
	pub   Publisher
}

// NewService 构造服务。cfg/pub 可为 nil：SweepOnce 在禁用 requeue 时不会触碰 pub。
func NewService(st *store.Store, cfg ConfigReader, pub Publisher) *Service {
	return &Service{store: st, cfg: cfg, pub: pub}
}

// Get 按租户＋流程实例读取（不存在返回 nil,nil）。
func (svc *Service) Get(ctx context.Context, tid, fiid int64) (*store.FlowStateMachineRow, error) {
	return svc.store.GetFlowStateMachine(tid, fiid)
}

// Init 幂等初始化：已存在返回现有行。
func (svc *Service) Init(ctx context.Context, tid int64, oneID string, fiid int64, startNode string) (*store.FlowStateMachineRow, error) {
	return svc.store.InitFlowStateMachine(tid, oneID, fiid, startNode)
}

// Advance 推进状态机：写当前节点 + 自增版本（乐观锁）。expectedVersion 与库不一致即冲突。
func (svc *Service) Advance(ctx context.Context, tid, fiid int64, currentNode, stateJSON string, expectedVersion int64) error {
	n, err := svc.store.AdvanceFlowStateMachine(tid, fiid, currentNode, stateJSON, expectedVersion)
	if err != nil {
		return err
	}
	// ★ F2：版本冲突必须显式返回可辨错误，绝不许把 RowsAffected==0 当成成功（静默吞冲突）。
	if n == 0 {
		observability.Warn(ctx, "状态机版本冲突，推进被拒（调用方应重试）",
			"tenant_id", tid, "flow_instance_id", fiid, "expected_version", expectedVersion)
		return ErrVersionConflict
	}
	return nil
}

// Heartbeat 心跳续期。
func (svc *Service) Heartbeat(ctx context.Context, tid, fiid int64) error {
	return svc.store.HeartbeatFlowStateMachine(tid, fiid)
}

// Complete 标记完成。
func (svc *Service) Complete(ctx context.Context, tid, fiid int64) error {
	return svc.store.CompleteFlowStateMachine(tid, fiid)
}

// SweepOnce 单轮巡检：心跳超时的 running 实例——先收口终态脏数据，再按开关决定
// 仅记录（默认）或发 flow_drive 断点续跑。返回本轮回填的 requeue 数。
func (svc *Service) SweepOnce(ctx context.Context, heartbeatTimeout time.Duration) int {
	deadline := time.Now().UTC().Add(-heartbeatTimeout).Format(time.RFC3339)
	stale, err := svc.store.ListStaleFlowStateMachines(deadline)
	if err != nil {
		observability.Error(ctx, "状态机巡检扫描失败", "error", err)
		return 0
	}
	// 先收口终态脏数据：流程早已 completed 但状态机漏标，这批行不该进 requeue 分支，
	// 收口后它们 status 不再是 running，下轮不再被扫到，扫描配额让给真正卡住的实例。
	stale = svc.terminalReconcile(ctx, stale)

	requeueEnabled := svc.cfg != nil && svc.cfg.GetBool("sm_sweep_requeue_enabled", false)
	requeued := 0
	for _, sm := range stale {
		if !requeueEnabled {
			if sm.LastEventID != "sweep_noticed" {
				observability.Info(ctx, "状态机实例心跳超时（未启用requeue，仅记录）",
					"flow_instance_id", sm.FlowInstanceID, "tenant_id", sm.TenantID)
			}
			// 写失败不再吞——刷不上心跳+标记意味着下轮还会重扫重打这条日志，必须看得见。
			if err := svc.store.TouchFlowStateMachine(sm.ID); err != nil {
				observability.Warn(ctx, "状态机实例心跳/标记写入失败（下轮将重新扫描）",
					"flow_instance_id", sm.FlowInstanceID, "error", err)
			}
			continue
		}
		observability.Info(ctx, "状态机实例心跳超时，发 flow_drive 断点续跑",
			"flow_instance_id", sm.FlowInstanceID, "tenant_id", sm.TenantID)
		if svc.pub == nil {
			continue
		}
		if err := svc.pub.PublishFlowDrive(ctx, sm.TenantID, sm.OneID,
			formatInt(sm.FlowInstanceID), "requeue",
			map[string]any{"current_node": sm.CurrentNode, "last_event_id": sm.LastEventID}); err != nil {
			observability.Warn(ctx, "flow_drive 发布失败（不重置心跳，下轮重试）", "error", err)
			continue
		}
		if err := svc.store.TouchFlowStateMachine(sm.ID); err != nil {
			observability.Warn(ctx, "状态机实例心跳未刷新（下轮将重发，消费端需幂等）",
				"flow_instance_id", sm.FlowInstanceID, "error", err)
		}
		requeued++
	}
	if len(stale) > 0 {
		observability.Info(ctx, "状态机巡检本轮扫描", "scanned", len(stale), "requeued", requeued)
	}
	return requeued
}

// terminalReconcile 把「状态机还停在 running、但流程实例其实早已 completed」的脏数据收口。
// 三条实现约束（踩过的坑，写进注释防后人"简化"）：
//  1. 不逐行查库：一次 WHERE id IN ? 批量联查，否则 100 行 = 100 次往返。
//  2. 跨租户安全：巡检是跨租户扫描，联查不提前按单租户过滤，而在收口侧按
//     (instanceID, tenantID) 配对判定——flow_instance_id 非全局唯一，只按 id 判会跨租户误标。
//  3. 收口失败必出声：DB 报错必须 WARN，静默失败会让这批行永久滞留、吃扫描配额。
func (svc *Service) terminalReconcile(ctx context.Context, stale []store.FlowStateMachineRow) []store.FlowStateMachineRow {
	if len(stale) == 0 {
		return stale
	}
	ids := make([]int64, 0, len(stale))
	byID := make(map[string]store.FlowStateMachineRow, len(stale))
	for _, sm := range stale {
		// 便宜的本地预筛：只有终态节点才可能是收口对象，省掉绝大多数行联查。
		if sm.CurrentNode != "end" {
			continue
		}
		ids = append(ids, sm.FlowInstanceID)
		byID[sm.ID] = sm
	}
	if len(ids) == 0 {
		return stale
	}

	done, err := svc.store.CompletedFlowInstanceIDs(ids)
	if err != nil {
		// 查不到就当没有可收口的：宁可不收口走原 requeue 分支，也不误标完成。
		observability.Warn(ctx, "终态收口联查失败（本轮跳过收口）", "candidates", len(ids), "error", err)
		return stale
	}
	if len(done) == 0 {
		return stale
	}

	reconciled := map[string]bool{}
	failed := 0
	for id, sm := range byID {
		// 按 (instanceID, tenantID) 配对：只收口租户一致的同一行，杜绝跨租户误标。
		if tid, ok := done[sm.FlowInstanceID]; !ok || tid != sm.TenantID {
			continue
		}
		if err := svc.store.ReconcileTerminalFlowStateMachine(id); err != nil {
			observability.Warn(ctx, "终态收口写失败（下轮重试）",
				"flow_instance_id", sm.FlowInstanceID, "tenant_id", sm.TenantID, "error", err)
			failed++
			continue
		}
		reconciled[id] = true
	}
	if len(reconciled) > 0 || failed > 0 {
		observability.Info(ctx, "终态收口：流程早已跑完但状态机漏标的实例已补标 completed",
			"reconciled", len(reconciled), "failed", failed)
	}
	if len(reconciled) == 0 {
		return stale
	}
	// 把已收口的行从待处理集合剔除：它们不该再进 requeue/重置心跳分支。
	remaining := make([]store.FlowStateMachineRow, 0, len(stale))
	for _, sm := range stale {
		if reconciled[sm.ID] {
			continue
		}
		remaining = append(remaining, sm)
	}
	return remaining
}

// formatInt 把流程实例 ID 转字符串（发布 payload 用）。
func formatInt(v int64) string {
	return strconv.FormatInt(v, 10)
}
