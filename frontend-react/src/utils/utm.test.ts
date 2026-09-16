// S4 归因捕获单测：localStorage('utm') 读取过滤（长度/类型/坏 JSON）与一次性清除。
import { beforeEach, describe, expect, it } from 'vitest'
import { clearUtm, readUtm } from './utm'

/** store 模拟 location.search 解析结果（免全局 DOM） */
const store = new Map<string, string>()
;(globalThis as Record<string, unknown>).localStorage = {
  getItem: (k: string) => (store.has(k) ? store.get(k)! : null),
  setItem: (k: string, v: string) => { store.set(k, v) },
  removeItem: (k: string) => { store.delete(k) },
}

describe('utm 捕获', () => {
  beforeEach(() => store.clear())

  it('空存储返回 {}', () => {
    expect(readUtm()).toEqual({})
  })

  it('合法五参读取', () => {
    store.set('utm', JSON.stringify({ utm_source: 'a', utm_medium: 'b', utm_campaign: 'c', utm_term: 'd', utm_content: 'e' }))
    expect(readUtm()).toEqual({ utm_source: 'a', utm_medium: 'b', utm_campaign: 'c', utm_term: 'd', utm_content: 'e' })
  })

  it('过滤超长值、非字符串值与坏 JSON', () => {
    store.set('utm', JSON.stringify({ utm_source: 'x'.repeat(121), utm_medium: 42, utm_content: 'ok' }))
    expect(readUtm()).toEqual({ utm_content: 'ok' })
    store.set('utm', '{坏 json')
    expect(readUtm()).toEqual({})
  })

  it('clearUtm 一次性消费', () => {
    store.set('utm', JSON.stringify({ utm_source: 'a' }))
    expect(readUtm().utm_source).toBe('a')
    clearUtm()
    expect(readUtm()).toEqual({})
  })
})
