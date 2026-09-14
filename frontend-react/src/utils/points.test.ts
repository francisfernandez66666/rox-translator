// S1 积分换算器单测：默认汇率、注入校验、四舍五入与最小 1 分、千分位格式化。
import { describe, expect, it, afterEach } from 'vitest'
import { fmtPoints, pointsOf, pointsRate, setPointsRate } from './points'

describe('points 换算器', () => {
  afterEach(() => setPointsRate(300))

  it('默认汇率 300，非法注入被忽略', () => {
    expect(pointsRate()).toBe(300)
    setPointsRate(0); expect(pointsRate()).toBe(300)
    setPointsRate(-5); expect(pointsRate()).toBe(300)
    setPointsRate(null); expect(pointsRate()).toBe(300)
    setPointsRate(600); expect(pointsRate()).toBe(600)
  })

  it('token→积分：四舍五入 + 非零最少显示 1', () => {
    setPointsRate(300)
    expect(pointsOf(0)).toBe(0)
    expect(pointsOf(300)).toBe(1)
    expect(pointsOf(10)).toBe(1) // 极小消耗不显示 0
    expect(pointsOf(449)).toBe(1)
    expect(pointsOf(450)).toBe(2)
    expect(pointsOf(300000)).toBe(1000)
  })

  it('fmtPoints 输出千分位积分串', () => {
    setPointsRate(300)
    expect(fmtPoints(1234500)).toBe('4,115')
    expect(fmtPoints(0)).toBe('0')
  })
})
