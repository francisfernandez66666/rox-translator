// ============================================================================
// quotaCalc.test.ts — 额度换算纯函数（★ F11：自服务估价换算回归锚）
// ============================================================================
import { describe, expect, it } from 'vitest'
import { sentenceRateOf, approxSentencesOf, DEFAULT_SENTENCE_RATE } from './quotaCalc'

describe('quotaCalc（C26 token 真账句数展示口径）', () => {
  it('正常反推：token ÷ ≈句数', () => {
    expect(sentenceRateOf(10000, 20)).toBe(500)
    expect(sentenceRateOf(90000, 100)).toBe(900)
  })
  it('非法输入回退默认 500（缺失/0/负数/非数字）', () => {
    expect(sentenceRateOf(undefined, 10)).toBe(DEFAULT_SENTENCE_RATE)
    expect(sentenceRateOf(0, 10)).toBe(DEFAULT_SENTENCE_RATE)
    expect(sentenceRateOf(100, 0)).toBe(DEFAULT_SENTENCE_RATE)
    expect(sentenceRateOf('x', 5)).toBe(DEFAULT_SENTENCE_RATE)
  })
  it('句数换算向下取整、防除零', () => {
    expect(approxSentencesOf(1200)).toBe(2)
    expect(approxSentencesOf(1199)).toBe(2)
    expect(approxSentencesOf(1000, 0)).toBe(0)
    expect(approxSentencesOf(0)).toBe(0)
  })
})
