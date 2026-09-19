// 积分格式化器单测（2026-09-19）：API 出参即积分值，前端只格式化、不换算。
import { describe, expect, it } from 'vitest'
import { fmtPoints } from './points'

describe('fmtPoints 纯格式化', () => {
  it('千分位输出，不再做任何除法', () => {
    expect(fmtPoints(1234500)).toBe('1,234,500')
    expect(fmtPoints(0)).toBe('0')
    expect(fmtPoints(999)).toBe('999')
    expect(fmtPoints(1000)).toBe('1,000')
  })

  it('非法输入回退 0，小数为四舍五入取整', () => {
    expect(fmtPoints(Number.NaN)).toBe('0')
    expect(fmtPoints(undefined as unknown as number)).toBe('0')
    expect(fmtPoints(-3.4)).toBe('-3')
    expect(fmtPoints(12.6)).toBe('13')
  })
})
