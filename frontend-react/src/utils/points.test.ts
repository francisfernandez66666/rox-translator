// S1 积分换算器单测：默认汇率、注入校验、四舍五入与最小 1 分、千分位格式化、
// 积分→token 反算（超管表单以积分录入的落库口径，2026-09-15 任务1）。
import { describe, expect, it, afterEach } from 'vitest'
import { fmtPoints, pointsOf, pointsRate, pointsToTokens, setPointsRate } from './points'

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

  it('积分→token 反算：随汇率、负值钳 0、四舍五入', () => {
    setPointsRate(300)
    expect(pointsToTokens(0)).toBe(0)
    expect(pointsToTokens(1000)).toBe(300000)
    expect(pointsToTokens(-5)).toBe(0)
    setPointsRate(600)
    expect(pointsToTokens(2)).toBe(1200)
  })

  it('往返一致性：整积分数经 token 落库再展示不失真', () => {
    setPointsRate(300)
    for (const pts of [1, 10, 500, 1000, 123456]) {
      expect(pointsOf(pointsToTokens(pts))).toBe(pts)
    }
  })
})
