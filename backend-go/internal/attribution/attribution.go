// ============ attribution.go · 职责说明 ============
// 二期六包平移第三刀（attribution）的引擎层：回复归因与素材效果统计的服务化封装。
// 构造注入 *store.Store，所有对外函数收 ctx + 显式 tid，tid<=0 一律 fail-closed
// （照 salesflow.NewScope 口径），不认任何 API 层兜底租户。
//
// ★ 一期终局档只许 inquiry（salesflow.OutcomeStageInquiry）：写其它值即返回错误、不落库（A2）。
// ★ 终态列 ArrivedAt/DealtAt 在落库行里保留但本层不写——到店/成交属后续自动化链路，
//
//	用未定义的终局去排序素材会把「没结果」写成结论。
//
// 本包零框架依赖、零行业分支、零裸日志：日志只走 observability，行业差异只经 Config 注入。
// ================================================
package attribution

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"translator/internal/observability"
	"translator/internal/salesflow"
	"translator/internal/store"
)

// Snapshot 回复时生效的物料包快照（纯数据，不依赖任何框架或 DB）。
type Snapshot struct {
	PackageID string
	Version   string
}

// Binding 租户当前绑定的物料包（纯函数入参，不依赖 DB）。
type Binding struct {
	PackageID         string
	AppliedVersion    string
	EnterprisePackID  int64
	EnterpriseCode    string
	EnterpriseVersion string
}

// SnapshotFromBinding 纯函数：优先企业包，无企业包时回退行业包。
// 参数：bind=租户绑定（nil 返回空快照）；返回：物料包快照。
func SnapshotFromBinding(bind *Binding) Snapshot {
	if bind == nil {
		return Snapshot{}
	}
	if bind.EnterprisePackID > 0 {
		return Snapshot{PackageID: bind.EnterpriseCode, Version: bind.EnterpriseVersion}
	}
	return Snapshot{PackageID: bind.PackageID, Version: bind.AppliedVersion}
}

// attributionStore 引擎依赖的数据访问子集（由 *store.Store 实现；测试可注入假件）。
type attributionStore interface {
	// RecordReply 幂等写一条回复归因。
	RecordReply(row store.ReplyAttributionRow) error
	// MarkHookedBeforeMessage 标记锚点消息之前最近一条已归因回复已接钩。
	MarkHookedBeforeMessage(tid, conversationID, customerMessageID int64) error
	// MarkLeadCaptured 标记最近一条已归因回复带来留资。
	MarkLeadCaptured(tid, conversationID, customerID int64) error
	// MarkPendingHuman 标记最近一条已归因回复触发待人工接管。
	MarkPendingHuman(tid, conversationID, customerID int64) error
	// MarkOutcome 回填终局档。
	MarkOutcome(tid, messageID int64, stage string) error
	// SyncPackStats 现算并物化素材效果快照。
	SyncPackStats(tid int64) error
}

// Engine 归因引擎服务（构造注入 *store.Store）。
type Engine struct {
	st            attributionStore
	outcomeSource OutcomeSource
}

// NewEngine 构造归因引擎。参数：st=数据访问（生产传 *store.Store；nil 时只保留纯逻辑）。
func NewEngine(st *store.Store) *Engine {
	var s attributionStore
	if st != nil {
		s = st
	}
	return &Engine{st: s}
}

// SetOutcomeSource 注入终局数据源（nil 表示未装配，回填时跳过并告警）。
func (e *Engine) SetOutcomeSource(src OutcomeSource) {
	e.outcomeSource = src
}

// RecordReply 幂等写一条回复归因；tid<=0 fail-closed，显式 tid 覆盖 Row 里的租户号防越权。
// 参数：ctx=上下文；tid=租户；row=归因行；返回：写库错误。
func (e *Engine) RecordReply(ctx context.Context, tid int64, row store.ReplyAttributionRow) error {
	scope, err := salesflow.NewScope(ctx, tid)
	if err != nil {
		return err
	}
	if row.MessageID <= 0 {
		return errors.New("归因记录缺少有效消息 ID")
	}
	if e.st == nil {
		return errors.New("归因引擎缺少落库口")
	}
	row.TenantID = scope.TenantID()
	return e.st.RecordReply(row)
}

// MarkHookedBeforeMessage 标记锚点消息之前最近一条已归因回复已接钩。
// 参数：ctx=上下文；tid=租户；conversationID=会话；customerMessageID=锚点客户消息；返回：写库错误。
func (e *Engine) MarkHookedBeforeMessage(ctx context.Context, tid, conversationID, customerMessageID int64) error {
	if _, err := salesflow.NewScope(ctx, tid); err != nil {
		return err
	}
	if e.st == nil {
		return errors.New("归因引擎缺少落库口")
	}
	return e.st.MarkHookedBeforeMessage(tid, conversationID, customerMessageID)
}

// MarkLeadCaptured 标记最近一条已归因回复带来留资。
// 参数：ctx=上下文；tid=租户；conversationID=会话；customerID=客户；返回：写库错误。
func (e *Engine) MarkLeadCaptured(ctx context.Context, tid, conversationID, customerID int64) error {
	if _, err := salesflow.NewScope(ctx, tid); err != nil {
		return err
	}
	if e.st == nil {
		return errors.New("归因引擎缺少落库口")
	}
	return e.st.MarkLeadCaptured(tid, conversationID, customerID)
}

// MarkPendingHuman 标记最近一条已归因回复触发待人工接管。
// 参数：ctx=上下文；tid=租户；conversationID=会话；customerID=客户；返回：写库错误。
func (e *Engine) MarkPendingHuman(ctx context.Context, tid, conversationID, customerID int64) error {
	if _, err := salesflow.NewScope(ctx, tid); err != nil {
		return err
	}
	if e.st == nil {
		return errors.New("归因引擎缺少落库口")
	}
	return e.st.MarkPendingHuman(tid, conversationID, customerID)
}

// MarkOutcome 回填终局档；一期只许 inquiry（A2），写其它值一律返回错误且不落库。
// 参数：ctx=上下文；tid=租户；messageID=消息；stage=终局档；返回：写库错误。
func (e *Engine) MarkOutcome(ctx context.Context, tid, messageID int64, stage string) error {
	if _, err := salesflow.NewScope(ctx, tid); err != nil {
		return err
	}
	if strings.TrimSpace(stage) != salesflow.OutcomeStageInquiry {
		return fmt.Errorf("终局档一期只许 %s，其它值一律拒绝（收到 %q）", salesflow.OutcomeStageInquiry, stage)
	}
	if e.st == nil {
		return errors.New("归因引擎缺少落库口")
	}
	return e.st.MarkOutcome(tid, messageID, salesflow.OutcomeStageInquiry)
}

// SyncPackStats 现算并物化该租户的素材效果快照。
// 参数：ctx=上下文；tid=租户；返回：写库错误。
func (e *Engine) SyncPackStats(ctx context.Context, tid int64) error {
	scope, err := salesflow.NewScope(ctx, tid)
	if err != nil {
		return err
	}
	if e.st == nil {
		return errors.New("归因引擎缺少落库口")
	}
	if err := e.st.SyncPackStats(tid); err != nil {
		return err
	}
	observability.Info(scope.Context(), "素材效果快照物化完成", "tenant_id", tid)
	return nil
}
