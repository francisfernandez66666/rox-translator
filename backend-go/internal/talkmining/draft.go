// ============ draft.go · 职责说明 ============
// 二期 S2 talkmining 的**出稿层**：高转候选簇 → 注入式 LLM 归纳 → 恒 draft 的说法草稿。
//
// 三条硬口径：
//
//	① 样本不足簇一律拒出稿（噪声不许占用人审人力）；
//	② 只写 draft 态，且**只经 DraftWriter.CreateMinedDraft 一个落库口**——本包不认转正，
//	   不许出现第二个写侧函数（§7 M1/M2：人审是「越用越准」与「越用越像幻觉」的分界）；
//	③ 幂等按「租户＋分组键＋簇签名」算出的确定性 ID：同簇重跑不重复出稿。
//
// 迁入即换：LLM 与落库都走注入（组合根装配），日志走 observability 的 slog（零裸日志）；
// 作用域用 salesflow.Scope 收 ctx＋显式 tid，tid<=0 fail-closed，不认 API 层兜底租户。
// ================================================
package talkmining

import (
	"errors"
	"fmt"
	"hash/fnv"
	"strings"

	"translator/internal/observability"
	"translator/internal/salesflow"
)

// ErrInvalidDraftScope 出稿缺少有效租户作用域（fail-closed，不回落默认租户）。
var ErrInvalidDraftScope = errors.New("出稿作用域缺少有效租户")

// GenerateDraftFunc 由组合根注入：把候选簇样本归纳成一条可复用说法。
// 参数：scope=引擎作用域；prompt=已脱敏样本组成的提示词；返回：建议说法与错误。
// 未注入时整轮跳过并留一条 Info（离线增强能力缺它主链零影响，不 panic）。
var GenerateDraftFunc func(scope salesflow.Scope, prompt string) (string, error)

// DraftStat 一轮出稿的账目（过程量不许静默吞）。
type DraftStat struct {
	Created             int // 新落库的草稿数
	SkippedInsufficient int // 样本不足被拒的簇数
	SkippedExisting     int // 幂等命中（该簇已出过稿）
	SkippedLLM          int // 归纳失败／空返回被跳过的簇数
}

// DraftWriter 出稿落库的唯一写口；由 store.Store 实现（接口在本包，避免基础包反向依赖）。
type DraftWriter interface {
	// CreateMinedDraft 幂等写一条 draft；返回 created=false 表示同 ID 已存在。
	CreateMinedDraft(tid int64, id, anchor, source, suggested, attribution string) (bool, error)
}

// minedDraftID 由 (租户, 分组键, 簇签名) 生成确定性草稿 ID（fnv32a 跨进程可复现）。
// 参数：tid=租户号；c=候选簇；返回：以 sdr_m_ 开头的稳定 ID。
func minedDraftID(tid int64, c Cluster) string {
	h := fnv.New32a()
	_, _ = fmt.Fprintf(h, "%d|%d|%d|%s", tid, c.Key.ActorID, c.Key.AnchorType, c.Key.Stage)
	return fmt.Sprintf("sdr_m_%08x_%s", h.Sum32(), c.Signature)
}

// anchorLabel 锚位标签的兜底形状：不带任何行业词，真实标签由调用方注入。
// 参数：t=锚位类型号；返回：通用的锚位描述。
func anchorLabel(t int) string {
	if t == Unanchored {
		return "锚位未知（按阶段归并）"
	}
	return fmt.Sprintf("锚位类型 %d", t)
}

// BuildDraftPrompt 组装归纳提示词（纯函数）。
// 约束写进提示词：产出通用可复用句式、去掉个例信息、占位符保留位；样本已在 Mine 内脱敏。
// 参数：c=候选簇；label=锚位标签函数（nil 用通用兜底）；返回：提示词。
func BuildDraftPrompt(c Cluster, label func(int) string) string {
	if label == nil {
		label = anchorLabel
	}
	return "你是说法提炼器。下面是同一场景下终局表现最好的一条代表性人工回复。\n" +
		fmt.Sprintf("场景：阶段=%s，%s；统计：样本数=%d，目标档(%s)达成率=%.0f%%。\n\n",
			c.Key.Stage, label(c.Key.AnchorType), c.Samples, c.TargetOutcome, c.ConvRate*100) +
		"代表性回复：\n" + c.Representative + "\n\n" +
		"请归纳成一条可复用的说法模板：保留句式结构与推进动作，去掉具体人名／号码／价格等个例信息，" +
		"个例位置改用 {{customer_name}} / {{item}} / {{price}} 类占位符。只输出模板正文，不要解释。"
}

// DraftTemplates 把候选簇归纳为草稿并幂等落库。
// 参数：scope=引擎作用域（tid<=0 直接拒）；clusters=Mine 的输出；maxDrafts=本轮上限（<=0 不限）；
// writer=唯一写口。返回：账目与错误（写库失败不吞，不把故障伪装成「已存在」）。
func DraftTemplates(scope salesflow.Scope, clusters []Cluster, maxDrafts int, writer DraftWriter) (DraftStat, error) {
	var stat DraftStat
	if scope.TenantID() <= 0 {
		return stat, ErrInvalidDraftScope
	}
	if writer == nil {
		return stat, errors.New("出稿缺少落库写口")
	}
	if GenerateDraftFunc == nil {
		observability.Info(scope.Context(), "talkmining 归纳函数未注入，本轮跳过出稿",
			"tenant_id", scope.TenantID(), "clusters", len(clusters))
		return stat, nil
	}
	for _, c := range clusters {
		if c.Insufficient {
			stat.SkippedInsufficient++
			continue
		}
		if maxDrafts > 0 && stat.Created >= maxDrafts {
			break
		}
		out, err := GenerateDraftFunc(scope, BuildDraftPrompt(c, nil))
		if err != nil || strings.TrimSpace(out) == "" {
			stat.SkippedLLM++
			observability.Warn(scope.Context(), "talkmining 归纳失败或空返回，跳过该簇",
				"signature", c.Signature, "err", err)
			continue
		}
		id := minedDraftID(scope.TenantID(), c)
		created, err := writer.CreateMinedDraft(scope.TenantID(), id,
			fmt.Sprintf("anchor:%d/%s", c.Key.AnchorType, c.Key.Stage),
			c.Representative, strings.TrimSpace(out), c.Signature)
		if err != nil {
			return stat, fmt.Errorf("talkmining 落库失败（草稿 %s）: %w", id, err)
		}
		if !created {
			stat.SkippedExisting++
			continue
		}
		stat.Created++
	}
	return stat, nil
}
