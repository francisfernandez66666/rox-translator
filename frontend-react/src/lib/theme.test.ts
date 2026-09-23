// ============================================================================
// theme.test.ts — 主题纯逻辑（★ 〇-N：全站只有暗色一档）
// 旧版这里测的是 light/dark/auto 三态与循环顺序；三态已按用户令删除，
// 留下的契约只有一条：无论本地残留什么偏好，落到 <html> 上的恒为 dark。
// ============================================================================
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { applyTheme } from './theme'

/** 打桩 DOM：记录 documentElement 上真正写下去的属性值，并模拟一个带历史偏好的 localStorage */
function stubGlobals(legacyPref: string | null) {
  const store = new Map<string, string>()
  if (legacyPref) store.set('app_theme', legacyPref)
  const removed: string[] = []
  const attrs: Record<string, string> = {}
  const style: Record<string, string> = {}
  vi.stubGlobal('localStorage', {
    getItem: (k: string) => store.get(k) ?? null,
    removeItem: (k: string) => { removed.push(k); store.delete(k) },
  })
  vi.stubGlobal('document', { documentElement: { setAttribute: (k: string, v: string) => { attrs[k] = v }, style } })
  return { attrs, style, removed }
}

describe('theme（〇-N 恒暗一档）', () => {
  beforeEach(() => { vi.unstubAllGlobals() })

  it('data-theme 与 color-scheme 恒写 dark', () => {
    const g = stubGlobals(null)
    applyTheme()
    expect(g.attrs['data-theme']).toBe('dark')
    expect(g.style.colorScheme).toBe('dark')
  })

  it('历史 light/auto 偏好被清除，且不参与判定', () => {
    for (const pref of ['light', 'auto', 'dark']) {
      vi.unstubAllGlobals()
      const g = stubGlobals(pref)
      applyTheme()
      expect(g.removed).toEqual(['app_theme'])
      expect(g.attrs['data-theme']).toBe('dark')
    }
  })
})
