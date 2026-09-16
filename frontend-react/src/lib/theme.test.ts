// ============================================================================
// theme.test.ts — 明暗主题纯逻辑（★ F11 / F5：三态 + 跟随系统）
// ============================================================================
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { getTheme, isDark, cycleTheme } from './theme'

/** stubGlobals 打桩 matchMedia（按 systemDark 模拟系统深色偏好） */
function stubGlobals(systemDark: boolean) {
  const store = new Map<string, string>()
  vi.stubGlobal('localStorage', {
    getItem: (k: string) => store.get(k) ?? null,
    setItem: (k: string, v: string) => { store.set(k, v) },
  })
  vi.stubGlobal('window', { matchMedia: () => ({ matches: systemDark, addEventListener: () => { /* noop */ } }) })
  vi.stubGlobal('document', { documentElement: { setAttribute() {}, style: {} as Record<string, string> } })
}

describe('theme（F5 三态）', () => {
  beforeEach(() => { vi.unstubAllGlobals() })

  it('无持久化偏好默认 auto', () => {
    stubGlobals(false)
    expect(getTheme()).toBe('auto')
  })
  it('auto 跟随系统', () => {
    stubGlobals(true)
    expect(isDark('auto')).toBe(true)
    stubGlobals(false)
    expect(isDark('auto')).toBe(false)
  })
  it('light/dark 显式覆盖系统', () => {
    stubGlobals(true)
    expect(isDark('light')).toBe(false)
    expect(isDark('dark')).toBe(true)
  })
  it('循环顺序 auto→light→dark→auto 且持久化', () => {
    stubGlobals(false)
    expect(cycleTheme()).toBe('light')
    expect(cycleTheme()).toBe('dark')
    expect(cycleTheme()).toBe('auto')
    expect(getTheme()).toBe('auto')
  })
})
