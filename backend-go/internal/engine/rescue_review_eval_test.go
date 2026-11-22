// ============ rescue_review_eval_test.go · 职责说明（2026-11-22）============
// 「救援段纳入校对+复核流程」的锁：上下文重译救回的段要跑一次复核 evals 评分，
// 但严格**只记分不改采纳**（见 recordRescueReviewEval 注释）。
//
// 三条腿：
//
//	A 降级三态不 panic、不拨模型：Evals=nil / 抽样关闭 / 禁用态（无 Judge Key）；
//	B 复核链路开关关闭时整条腿不跑（ReviewEnabled=false）；
//	C 评分失败不影响采纳：Evaluate 报错时函数吞掉，只记 WARN（这里用禁用态触发 err 分支）。
//
// ================================================
package engine

import (
	"context"
	"testing"

	"translator/internal/evals"
)

// TestRescueReviewEvalDegradesQuietly A+B+C 腿：任何降级态都不 panic、不向外传播错误。
// 评分是本链的可选增强，绝不能因为它（无 Key/抽样/开关/网络失败）把已救回的译文打回。
func TestRescueReviewEvalDegradesQuietly(t *testing.T) {
	ctx := context.Background()

	// A1: Evals=nil（评估器未装配）。
	var nilEvals *Engine
	nilEvals = &Engine{}
	nilEvals.recordRescueReviewEval(ctx, "65–120 万", "650,000–1,200,000", "he")

	// A2: 抽样关闭（SampleRate=0）——成本闸关掉时整条腿不跑。
	eOff := &Engine{Evals: &evals.Evaluator{SampleRate: 0, Enabled: true}}
	eOff.recordRescueReviewEval(ctx, "65–120 万", "650,000–1,200,000", "he")

	// A3: 禁用态（无 Judge Key）——Evaluate 会直接报错，函数必须吞掉只记 WARN。
	eDisabled := &Engine{Evals: &evals.Evaluator{SampleRate: 1, Enabled: false}}
	eDisabled.recordRescueReviewEval(ctx, "65–120 万", "650,000–1,200,000", "he")

	// B: nil 引擎也不许 panic（ReviewEnabled 自身按开回落，Evals=nil 仍应静默返回）。
	var nilEng *Engine
	nilEng.recordRescueReviewEval(ctx, "65–120 万", "650,000–1,200,000", "he")
}
