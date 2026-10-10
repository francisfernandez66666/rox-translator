// ============================================================================
// slowEstimate.test.ts — ★ 决策⑪① 三档预检纯函数单测（2026-10-10）
// 钉住三条口径：
//   ① 阈值方向：>90s 拒（发出去必白等 90s deadline）、>45s 明示较慢、其余放行；
//   ② 模式差档：pro 流水线段数 5 段、fast 2 段（与 engine 侧流水线段数同口径）；
//   ③ 宁误拦：排队深度未知（undefined/负数）按保守档 2 兜底——误判方向只许「把能过
//      的判慢」，不许「把会超时的放行」。
// ============================================================================
import { describe, it, expect } from 'vitest'
import { slowVerdict, SLOW_QUEUE_FALLBACK } from './slowEstimate'

describe('长文本发起前三档预检（决策⑪① 宁误拦）', () => {
  it('① 短文本放行；45–90s 明示较慢；超 90s 直接拒', () => {
    expect(slowVerdict(50, 'pro', 0)).toBe('ok')      // 5×(6+0.2)=31s
    expect(slowVerdict(800, 'pro', 0)).toBe('slow')   // 5×(6+3.2)=46s
    expect(slowVerdict(800, 'fast', 0)).toBe('ok')    // 2×(6+3.2)=18.4s
    expect(slowVerdict(3000, 'pro', 0)).toBe('slow')  // 5×18=90s → 不大于 90，slow
    expect(slowVerdict(3001, 'pro', 0)).toBe('reject') // 刚过 90s 线即拒
  })

  it('② 排队深度计入耗时：同字符数排队越深档位越高', () => {
    expect(slowVerdict(1000, 'fast', 0)).toBe('ok')     // 2×(6+4)=20s
    expect(slowVerdict(1000, 'fast', 2)).toBe('slow')   // 20+30=50s
    expect(slowVerdict(1000, 'fast', 4)).toBe('slow')   // 20+60=80s
    expect(slowVerdict(1000, 'fast', 6)).toBe('reject') // 20+90=110s > 90 ⇒ 拒
  })

  it('③ 宁误拦：排队深度未知按保守档兜底（不低于放行方向的乐观假设）', () => {
    expect(SLOW_QUEUE_FALLBACK).toBeGreaterThan(0)
    // 未知档必须**不低于**已知空队列档：宁可错拦，不可错放
    const chars = 700
    const unknown = slowVerdict(chars, 'pro', undefined)
    const empty = slowVerdict(chars, 'pro', 0)
    expect(unknown === 'slow' || unknown === 'reject').toBe(true)
    expect(empty).toBe('ok') // 对照：同文本空队列是可放行的
  })

  it('④ 边界自洽：阈值线上恰好不越档（90s 整不拒、45s 整不提示）', () => {
    // fast 2 段、空队列：45s 整 ⇒ 2×(6+c×0.004)=45 ⇒ c=4125
    expect(slowVerdict(4125, 'fast', 0)).toBe('ok')
    expect(slowVerdict(4126, 'fast', 0)).toBe('slow')
    // 90s 整 ⇒ 拒档阈值是「大于 90」：2×(6+c×0.004)=90 ⇒ c=9750
    expect(slowVerdict(9750, 'fast', 0)).toBe('slow')
    expect(slowVerdict(9751, 'fast', 0)).toBe('reject')
  })
})
