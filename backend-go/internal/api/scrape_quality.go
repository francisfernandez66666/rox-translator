// ============ scrape_quality.go · 职责说明（2026-11-23 tier3 质量治理）============
// 采集侧语义闸 api 适配层：把共享判据 internal/qualityjudge 接成 crawler.QualityJudge。
//
// ★ 判据（HY 回译 + evals 打分）的唯一实现在 internal/qualityjudge，
//
//	实时采集闸与云端清洗 job（cmd/kb-clean）共用那一份，本文件只做接口适配，
//	不复制任何 Prompt/阈值/Key 解密逻辑（复制＝同一判据两个时刻两个结果）。
//
// 语义闸调用方：packscraper.go（定时采集）与 admin_scrape.go（手动立即采集）两处注入。
// 任一步失败 ⇒ fail-open + WARN（沿用 evals 降级口径，不阻断采集）。
// ============================================================
package api

import (
	"context"

	"translator/internal/config"
	"translator/internal/crawler"
	"translator/internal/llm"
	"translator/internal/observability"
	"translator/internal/qualityjudge"
	"translator/internal/store"
)

const defaultBacktranslateModel = qualityjudge.DefaultBacktranslateModel

// apiQualityJudge 把 qualityjudge.Judge 适配成 crawler.QualityJudge：
// 判据报错时 fail-open（放行 + WARN），不因 Judge 抖动阻断采集。
type apiQualityJudge struct {
	j *qualityjudge.Judge
}

func (a *apiQualityJudge) JudgeTerm(ctx context.Context, source, translation, targetLang string) (bool, string, error) {
	r, err := a.j.Evaluate(ctx, source, translation, targetLang)
	if err != nil {
		return true, "", err // fail-open：err 非 nil 时 crawler.passSemanticGate 记 WARN 并放行
	}
	if !r.Pass {
		return false, r.Reason, nil
	}
	return true, "", nil
}

// RepairTerm 判错后修正：中文源词经 HY 正向重译 + 再验证（判据在 qualityjudge 包）。
func (a *apiQualityJudge) RepairTerm(ctx context.Context, source, targetLang string) (string, bool, error) {
	return a.j.Repair(ctx, source, targetLang)
}

// newQualityJudge 由 api 层构造并注入 crawler。cfg/client/store 缺失时返回 nil（只跑结构化闸）。
func newQualityJudge(cfg *config.Config, client *llm.Client, st *store.Store) crawler.QualityJudge {
	j := qualityjudge.New(cfg, client, st)
	if j == nil {
		observability.Warn(context.Background(), "tier3 语义质量门未注入（cfg/client/store 缺失），只跑结构化闸")
		return nil
	}
	return &apiQualityJudge{j: j}
}

// extractQualityJSON 保留为 api 包内的薄委托，供既有测试引用；判据实现在共享包。
func extractQualityJSON(s string) string {
	return qualityjudge.ExtractJSON(s)
}

// scoreThresholdPass 纯函数（测试用）：双维任一维低于阈值即不通过。
func scoreThresholdPass(term, semantic, min float64) bool {
	return !(term < min || semantic < min)
}
