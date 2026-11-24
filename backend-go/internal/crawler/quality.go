// ============ quality.go · 职责说明（2026-11-23 tier3 质量治理）============
// 采集侧（crawler）产出落正式库前的两道质量闸：
//
//	闸2 结构化校验：书写系统不符 / 拉丁转写 / 全角标点 / 源译同形 → 确定性拒绝。
//	闸3 语义质量门：回译腿（HY 把译文译回中文）+ evals 打分腿对每条候选打
//	    term/semantic 分，低于阈值拒绝；Judge 未注入/无法判定/报错 → fail-closed 丢弃，
//	    只记 WARN，不把上游抖动当质量合格自动审批。
//
// 依赖倒置：语义门只依赖 QualityJudge 接口，具体 evaluator 由 api 层注入，
// 让 crawler 包可单测（替身）且不 import evals/evaluator 具体实现。
// ============================================================
package crawler

import (
	"context"

	"translator/internal/gate"
	"translator/internal/store"
)

// QualityJudge 语义质量判定（由 api 层两 leg Judge 适配而来：HY 回译 + evals 打分）。
// JudgeTerm 返回 pass=true 表示质量达标可落库；reason 供 WARN 留痕。
// RepairTerm ★ 2026-11-23：判错不直接丢，先用中文源词经 HY 正向重译生成正确译文，
//
//	再把新译文过一遍回译+打分验证；验证通过返回 (新译文, true, nil)。
//	重译或验证失败 ⇒ 返回 ("", false, err)，调用方才丢弃该条。
type QualityJudge interface {
	JudgeTerm(ctx context.Context, source, translation, targetLang string) (pass bool, reason string, err error)
	RepairTerm(ctx context.Context, source, targetLang string) (fixed string, ok bool, err error)
}

// qualityGateConfig 两道闸的运行参数（由 system_config 读取，缺省安全值）。
type qualityGateConfig struct {
	MinScore float64 // term/semantic 双维最低分（>0=开启，0=关闭语义门）
	Sample   float64 // 语义门抽样率 0~1（0=不抽，1=全量）
}

// loadQualityConfig 读采集质量配置。缺省：语义门开、全量。
// 配 scrape_tier3_quality_min=0 关闭语义门（回滚开关）。
// 注：回译+evals 打分形态下此值是 term/semantic 双维最低分（默认 70）。
func loadQualityConfig(st *store.Store) qualityGateConfig {
	cfg := qualityGateConfig{MinScore: 70, Sample: 1}
	if st == nil {
		return cfg
	}
	// 用 AllowZero 变体：min=0 是关闭语义门的回滚开关，普通 ConfigInt 会把 0 当缺省吃掉
	if v := st.ConfigIntAllowZero("scrape_tier3_quality_min", 70); v >= 0 {
		cfg.MinScore = float64(v)
	}
	// 抽样率以百分整数存（100=全量），ConfigInt 口径与项目其它抽样键一致
	if v := st.ConfigIntAllowZero("scrape_tier3_quality_sample", 100); v >= 0 && v <= 100 {
		cfg.Sample = float64(v) / 100
	}
	return cfg
}

// structuralReject 闸2：返回非空字符串表示该候选应被拒绝（说明原因）。
// 只依赖确定性纯函数，无 LLM 成本。
func structuralReject(srcLang, srcText, tgtLang, tgtText string) string {
	return gate.StructuralReject(srcLang, srcText, tgtLang, tgtText)
}
