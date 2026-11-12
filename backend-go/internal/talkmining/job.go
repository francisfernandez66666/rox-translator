// ============ job.go · 职责说明 ============
// 三期 W2 六包接线的最后一环：talkmining 的 **DB 装载契约 + 单轮编排**。
//
// 为什么需要这一层：mining.go / draft.go 迁入后一直是纯函数包——
// Mine 只认调用方递来的 []Record，DraftTemplates 只认调用方注入的 DraftWriter，
// 而「Record 从哪张表来」「一轮谁拨」全仓零生产调用（§25-c「能力已建未接线」）。
// 本文件把这两头收成一条可装配的链，但**不自己认任何具体表**：
//   - 装载口 RecordSource 是接口，实现由组合根/store 提供（基础包反向依赖禁令，§一·3）；
//   - W2 不挂周期任务、不伪造空数据源：人工回复记录表在 W6 CRM 批才落，
//     此刻若挂一个恒返回 0 行的 source，就是把「没接线」写成「接线了」，
//     正是本项目「靶不存在不预写锁」的硬纪律要挡的形态。
//   - W6 的成交流程/CRM 四表落地后，只需提供真实 RecordSource 并在组合根拨 RunOnce，
//     本文件一行不用改；M1（转正唯一入口）与 M2（产物恒 draft）由 draft.go 的
//     DraftWriter 单一写口保证，job 层绝不新增第二个落库路径。
//
// 三条口径：
//  1. **作用域显式**：tid<=0 直接拒（salesflow.NewScope 的 fail-closed），不许回落默认租户 1；
//  2. **装载失败不吞**：DB 读错就是读错，绝不按「本轮无样本」静默跳过——
//     否则库故障会伪装成「今天没挖到东西」；
//  3. **只有 draft 态产物**：出稿统一走 DraftTemplates，job 不认 approve/auto-enable。
// ================================================
package talkmining

import (
	"context"
	"errors"
	"fmt"

	"translator/internal/observability"
	"translator/internal/salesflow"
)

// ErrInvalidJobScope 挖掘作业缺少有效租户作用域（fail-closed，不回落默认租户）。
var ErrInvalidJobScope = errors.New("挖掘作业作用域缺少有效租户")

// RecordSource 人工回复批的装载口（接口在本包，实现在组合根/store 侧）。
// ★ 实现方必须按 tid 严格隔离，且只回「说话方」文本（客户原文属 PII，见 mining.go Record 注释）。
type RecordSource interface {
	// ListMiningRecords 按租户装载本轮挖掘样本；返回切片与读库错误。
	ListMiningRecords(ctx context.Context, tid int64) ([]Record, error)
}

// Job 一轮离线挖掘的装配集合（全部由组合根注入，本包不认 DB／LLM／具体表）。
type Job struct {
	// Source 样本装载口；nil 代表尚未接线（RunOnce 直接报错，不静默返回空账）。
	Source RecordSource
	// Writer 出稿唯一写口（store.Store 已实现 CreateMinedDraft）。
	Writer DraftWriter
	// Opts 挖掘参数；零值逐项回落 mining.go 的默认档。
	Opts Options
	// MaxDrafts 本轮出稿上限（<=0 不限）；防一次跑批把人审队列灌满。
	MaxDrafts int
}

// JobStat 一轮作业的排障读数（过程量不许静默吞：装载几条、成簇几条、出稿几条）。
type JobStat struct {
	Loaded   int        // 装载到的样本条数
	Clusters int        // Mine 产出的候选簇条数
	Draft    DraftStat  // DraftTemplates 的出稿账目
	Skipped  bool       // 归纳函数未注入导致整轮跳过（离线增强能力缺它主链零影响）
}

// RunOnce 单轮：装载 → 聚类 → 出稿。任一环失败即返回错误，绝不把故障写成空账。
// 参数 ctx=上下文；tid=租户号（必须 >0）；返回作业读数与错误。
func (j *Job) RunOnce(ctx context.Context, tid int64) (JobStat, error) {
	var stat JobStat
	if tid <= 0 {
		return stat, ErrInvalidJobScope
	}
	scope, err := salesflow.NewScope(ctx, tid)
	if err != nil {
		return stat, fmt.Errorf("挖掘作业作用域无效: %w", err)
	}
	if j == nil || j.Source == nil {
		return stat, errors.New("挖掘作业缺少样本装载口")
	}
	if j.Writer == nil {
		return stat, errors.New("挖掘作业缺少出稿写口")
	}
	records, err := j.Source.ListMiningRecords(ctx, tid)
	if err != nil {
		// ★ 不吞：库故障伪装成「今天没样本」比直接报错更难排查（§一·8 同族）。
		return stat, fmt.Errorf("挖掘样本装载失败: %w", err)
	}
	stat.Loaded = len(records)
	clusters := Mine(records, j.Opts)
	stat.Clusters = len(clusters)
	stat.Skipped = GenerateDraftFunc == nil
	draft, err := DraftTemplates(scope, clusters, j.MaxDrafts, j.Writer)
	if err != nil {
		return stat, fmt.Errorf("挖掘出稿失败: %w", err)
	}
	stat.Draft = draft
	if stat.Loaded > 0 || stat.Draft.Created > 0 {
		observability.Info(ctx, "talkmining 单轮完成",
			"tenant_id", tid, "loaded", stat.Loaded, "clusters", stat.Clusters,
			"created", stat.Draft.Created, "skipped_llm", stat.Skipped)
	}
	return stat, nil
}
