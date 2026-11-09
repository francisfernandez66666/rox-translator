// ============ outcome_backfill.go · 职责说明 ============
// 终局标签回填的注入式平移：外仓直接读 Customer 旅程表，主仓没有该表，
// 因此这里只定义 OutcomeSource 接口，由组合根注入数据源；**绝不自建或伪造任何 DB 表**。
// 数据源未注入（nil）时整轮跳过并出一条 WARN（不是静默 return）；一期只认可 inquiry，
// 其它终局档一律不写（A2）。终态时间列不写——终局推进属后续自动化。
// ================================================
package attribution

import (
	"context"
	"fmt"

	"translator/internal/observability"
	"translator/internal/salesflow"
)

// OutcomeSignal 一条待回填的终局信号（由注入的数据源给出）。
type OutcomeSignal struct {
	MessageID    int64
	OutcomeStage string
}

// OutcomeSource 终局阶段数据源（组合根注入）。主仓无客户旅程表，本包绝不伪造；
// 未注入时回填跳过并告警，不静默降级。
type OutcomeSource interface {
	// ListOutcomes 返回指定租户、最多 limit 条待回填的终局信号。
	// 参数：ctx=上下文；tid=租户；limit=单轮上限；返回：终局信号与错误。
	ListOutcomes(ctx context.Context, tid int64, limit int) ([]OutcomeSignal, error)
}

// BackfillOutcomes 按注入的数据源回填终局档；一期只写 inquiry。
// tid<=0 fail-closed；数据源为 nil 时 WARN 后跳过，绝不伪造 DB 表。
// 参数：ctx=上下文；tid=租户；limit=单轮上限；返回：写入条数与错误。
func (e *Engine) BackfillOutcomes(ctx context.Context, tid int64, limit int) (int64, error) {
	scope, err := salesflow.NewScope(ctx, tid)
	if err != nil {
		return 0, err
	}
	if e.outcomeSource == nil {
		observability.Warn(scope.Context(),
			"终局回填数据源未注入，本轮跳过（主仓无客户旅程表，绝不伪造）",
			"tenant_id", scope.TenantID())
		return 0, nil
	}
	signals, err := e.outcomeSource.ListOutcomes(scope.Context(), scope.TenantID(), limit)
	if err != nil {
		return 0, fmt.Errorf("读取终局数据源失败: %w", err)
	}
	var written int64
	for _, sig := range signals {
		if sig.OutcomeStage != salesflow.OutcomeStageInquiry {
			continue // 一期只认可 inquiry，其它终局档不写（A2）
		}
		if e.st == nil {
			continue
		}
		if err := e.st.MarkOutcome(scope.TenantID(), sig.MessageID, salesflow.OutcomeStageInquiry); err != nil {
			return written, err
		}
		written++
	}
	return written, nil
}
