// ============================================================================
// lib/safeJson.test.ts — 脏 JSON 解析守卫（★ 2026-09-26 〇-U 批 I-2 · 缺陷 F-45）
// ----------------------------------------------------------------------------
// 复现的真实事故：线上反馈列表里存在 translations 列值为字符串 "null" 的历史行，
// 超管点进详情 ⇒ 旧写法 `try { JSON.parse(x || '{}') } catch { return {} }` 里
// parse("null") **成功**并返回 null（不抛错，catch 形同虚设）⇒
// 渲染处 Object.entries(null) 抛 TypeError ⇒ 整块详情白屏（页面级崩溃，不是空态）。
// 本测试把「parse 成功不等于拿到对象」钉成断言，防止有人把守卫改回裸 try/catch。
// ============================================================================
import { describe, expect, it } from 'vitest'

import { parseStringMap } from './safeJson'

describe('parseStringMap（脏 JSON 必须降级为对象）', () => {
  it('正常映射原样取回键值', () => {
    const m = parseStringMap('{"en":"Hello","ar":"مرحبا"}')
    expect(m.en).toBe('Hello')
    expect(m.ar).toBe('مرحبا')
    expect(Object.keys(m)).toEqual(['en', 'ar'])
  })

  // ★ 本批缺陷本体：这三个脏值都能被 JSON.parse 成功解析，却不是可用映射
  it('"null" 是合法 JSON 但不是对象，必须降级', () => {
    expect(parseStringMap('null')).toEqual({})
    expect(() => Object.entries(parseStringMap('null'))).not.toThrow()
  })

  it('标量与数组同样降级（parse 成功不代表拿到映射）', () => {
    for (const raw of ['true', '123', '"text"', '[1,2]', '[]']) {
      expect(parseStringMap(raw)).toEqual({})
      expect(() => Object.entries(parseStringMap(raw))).not.toThrow()
    }
  })

  it('空串/空白/非字符串/非法 JSON 一律降级', () => {
    for (const raw of ['', '   ', '{"en":', undefined, null, 42, {}]) {
      expect(parseStringMap(raw)).toEqual({})
    }
  })

  it('调用方给的 fallback 生效，且默认值不共享实例（防被写脏）', () => {
    const fb = { zh: '默认' }
    expect(parseStringMap('null', fb)).toBe(fb)
    const a = parseStringMap('')
    a.injected = 'x'
    expect(parseStringMap('')).not.toHaveProperty('injected')
  })
})
