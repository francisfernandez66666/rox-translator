// ============ scrape_quality.go · 职责说明（2026-11-23 tier3 质量治理）============
// 采集侧语义闸 api 适配层：把共享判据 internal/qualityjudge 接成 crawler.QualityJudge。
//
// ★ 判据（HY 回译 + evals 打分）的唯一实现在 internal/qualityjudge，
//
//	实时采集闸与云端清洗 job（cmd/kb-clean）共用那一份，本文件只做接口适配，
//	不复制任何 Prompt/阈值/Key 解密逻辑（复制＝同一判据两个时刻两个结果）。
//
// 语义闸调用方：packscraper.go（定时采集）与 admin_scrape.go（手动立即采集）两处注入。
// 任一步无法判定 ⇒ fail-closed 丢弃 + WARN；主判有效低分才进入 Repair，不能把上游抖动当合格。
// ============================================================
package api

import (
	"context"
	"fmt"

	"translator/internal/config"
	"translator/internal/crawler"
	"translator/internal/llm"
	"translator/internal/observability"
	"translator/internal/qualityjudge"
	"translator/internal/store"
)

// 回译模型缺省值由共享判据包单一声明，API 层只做兼容别名。
const defaultBacktranslateModel = qualityjudge.DefaultBacktranslateModel

// qualityJudgeAPI 是适配层需要的最小判据面，便于验证救回元数据没有在接线时丢失。
type qualityJudgeAPI interface {
	Evaluate(ctx context.Context, source, translation, targetLang string) (qualityjudge.Result, error)
	Repair(ctx context.Context, source, targetLang string) (string, bool, error)
}

// apiQualityJudge 把 qualityjudge.Judge 适配成 crawler.QualityJudge：
// 判据无法判定时 fail-closed（丢弃 + WARN），不能把上游抖动当质量合格自动审批。
type apiQualityJudge struct {
	j qualityJudgeAPI
}

// JudgeTerm 适配采集语义闸，并保留复核救回读数供排障。
func (a *apiQualityJudge) JudgeTerm(ctx context.Context, source, translation, targetLang string) (bool, string, error) {
	r, err := a.j.Evaluate(ctx, source, translation, targetLang)
	if err != nil {
		return false, "语义闸无法判定: " + err.Error(), err
	}
	if !r.Conclusive {
		err := fmt.Errorf("语义闸未给出有效裁决: %s", r.Reason)
		return false, "语义闸无法判定: " + err.Error(), err
	}
	if !r.Pass {
		return false, r.Reason, nil
	}
	return true, r.ReviewNote, nil
}

// RepairTerm 判错后修正：中文源词经 HY 正向重译 + 再验证（判据在 qualityjudge 包）。
func (a *apiQualityJudge) RepairTerm(ctx context.Context, source, targetLang string) (string, bool, error) {
	return a.j.Repair(ctx, source, targetLang)
}

// newQualityJudge 由 api 层构造并注入 crawler。cfg/client/store 缺失时返回 nil，采集侧按 fail-closed 丢弃。
func newQualityJudge(cfg *config.Config, client *llm.Client, st *store.Store) crawler.QualityJudge {
	j := qualityjudge.New(cfg, client, st)
	if j == nil {
		observability.Warn(context.Background(), "tier3 语义质量门未注入（cfg/client/store 缺失），采集候选将按 fail-closed 丢弃")
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
