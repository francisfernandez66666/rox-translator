// ============================================================================
// lib/slowEstimate.ts — ★ 决策⑪① 长文本发起前三档预检（2026-10-10）
// ============================================================================
// 口径来源（计划稿 §14.2 / 决策⑪「宁误拦」）：
//   预估耗时（秒）＝ 流水线段数 ×（基础 6s ＋ 每字符 4ms）＋ 排队深度 × 15s
//   - 段数：fast＝2（初翻＋校对）；pro＝5（初翻＋校对＋质检＋文化闸＋重翻余量）。
//     段数与 engine 侧流水线一致（不新造尺子，见 §14.2「预估接口不新造尺子」）。
//   - 排队深度：后端 /api/translation/estimate 的 llm_queue_depth（饱和代理，口径见
//     api/stream.go llmQueueDepth 注释——inflight 含 embedding，信号量无等待数，
//     真实排队深度不可低成本测得）。
//   - **宁误拦**：排队深度字段缺失（老后端/接口降级/未选中目标语种）时按 2 档兜底——
//     保守方向的误差只会把"其实能译完"判成"较慢"，不会把会超时的放过去。
// 三档阈值对齐 §14.2 的 90s deadline：>90s 直接拒（发不出去必然白等），>45s 明示
// 「预计较慢，建议转工单」（仍可发送，用户自担）；其余放行。
// ============================================================================

/** 预检结论：ok＝直接译；slow＝明示较慢＋工单入口（不拦发送）；reject＝直接拒（拦发送） */
export type SlowVerdict = 'ok' | 'slow' | 'reject'

/** 排队深度未知时的保守兜底档（宁误拦：见文件头注释） */
export const SLOW_QUEUE_FALLBACK = 2

/** 三档预检纯函数（无副作用，单测直测）
 *  @param chars      输入字符数（trim 后）
 *  @param mode       当前模式（fast/pro，决定流水线段数）
 *  @param queueDepth 后端排队深度代理值；undefined/负数＝未知，走保守兜底 */
export function slowVerdict(chars: number, mode: 'fast' | 'pro', queueDepth?: number): SlowVerdict {
  const segments = mode === 'pro' ? 5 : 2
  const qd = typeof queueDepth === 'number' && queueDepth >= 0 ? queueDepth : SLOW_QUEUE_FALLBACK
  const estSeconds = segments * (6 + chars * 0.004) + qd * 15
  if (estSeconds > 90) return 'reject'
  if (estSeconds > 45) return 'slow'
  return 'ok'
}
