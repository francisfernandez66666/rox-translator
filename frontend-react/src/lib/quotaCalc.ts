// ============================================================================
// lib/quotaCalc.ts — 额度换算纯函数（★ F11 自服务估价换算抽纯可测）
// token 为唯一真账（C26），「句数」为展示口径：句≈token÷换算率。
// ============================================================================

/** 兜底换算率：无余额数据时按 500 token/句 估算（历史经验值） */
export const DEFAULT_SENTENCE_RATE = 500

/** sentenceRateOf 从余额行「可用 token ÷ ≈句数」反推实际换算率；非法/缺失回退默认 */
export function sentenceRateOf(tokens: unknown, approxSentences: unknown): number {
  if (typeof tokens === 'number' && tokens > 0 && typeof approxSentences === 'number' && approxSentences > 0) {
    return tokens / approxSentences
  }
  return DEFAULT_SENTENCE_RATE
}

/** approxSentencesOf token 数换算 ≈句数（向下取整；rate<=0 返回 0 防除零） */
export function approxSentencesOf(tokens: number, rate = DEFAULT_SENTENCE_RATE): number {
  return rate > 0 ? Math.floor((tokens || 0) / rate) : 0
}
