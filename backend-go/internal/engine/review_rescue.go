// ============ review_rescue.go · 问题段落复核救援（2026-11-20 P3 核心） ============
//
// 用户拍板（三次澄清）："充分利用复核模型的推理能力和翻译质量提升能力，发现问题的部分
// 全部抽出、以复核模型的结果为准、不走初翻链路。"
//
// 落点：凡被闸门判有问题的段落 → 丢弃初翻 → 用 translateWithFeedbackEx(stage=StageReview)
// 基于【原文+诊断】重译（该提示词天然不带初翻）→ 结果直接采纳；
// 复核也救不了 → 回落原文 + md 标注（用户："复核+推理都救不了，初翻也救不了"）。
// ========================================
package engine

import (
	"context"
	"sync"

	"translator/internal/config"
	"translator/internal/observability"
)

// ProblemSegment 一个被闸门判有问题的源段。
type ProblemSegment struct {
	Source    string // 源文（重译的唯一输入）
	Diagnosis string // 诊断意见（来自 firstGateFail 等）
}

// RescueProblemSegments 用复核模型批量救援问题段落。
// 返回"源段 → 复核重译结果"映射；只含成功产出且过可用性/纯度判据的段。
// 未出现在映射里的段由调用方按"回落原文 + md 标注"处理（不回落初翻）。
//
// 并发上限 4（与 text.go 复核链一致），失败不 panic（recoverPipeline 由调用方包）。
func (e *Engine) RescueProblemSegments(ctx context.Context, problems []ProblemSegment, lang string) map[string]string {
	if len(problems) == 0 {
		return nil
	}
	// ★ 2026-11-21：复核链路关闭时不救援，调用方按既有出口「回落原文 + md 标注」处理。
	if !e.ReviewEnabled(ctx) {
		return nil
	}
	out := make(map[string]string, len(problems))
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for _, p := range problems {
		if p.Source == "" || p.Diagnosis == "" {
			continue
		}
		wg.Add(1)
		go func(seg ProblemSegment) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()
			// ★ 核心：stage=StageReview，输入只有原文+诊断（translateWithFeedbackEx 的提示词
			//   只有【原文】，不带初翻）——"不走初翻链路"由函数结构保证。
			fixed := e.translateWithFeedbackEx(ctx, seg.Source, lang, seg.Diagnosis, config.StageReview, nil)
			if fixed == "" {
				return // 复核失败：调用方回落原文+标注
			}
			// 采纳门槛：与文件写入点同一把尺子（含 P0 骨架判据 + 指令回显 + 长文爆炸）+ 后处理纯度清洗。
			fixed = PostProcessTranslation(fixed, lang)
			if !e.fileTranslationUsable(ctx, seg.Source, fixed, lang) {
				observability.Warn(ctx, "复核救援产物未过可用性判据，回落原文",
					"lang", lang, "src_len", len([]rune(seg.Source)))
				return
			}
			mu.Lock()
			out[seg.Source] = fixed
			mu.Unlock()
		}(p)
	}
	wg.Wait()
	return out
}
