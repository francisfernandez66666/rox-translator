// ============================================================================
// api/chatImage.test.ts — 图片翻译客户端的报文契约锁（★ 2026-10-17 用户令新增）
//
// 锁三件事（与后端 chat_image.go 的 chatImageResp 逐字对应）：
//   ① 成功：multipart 里带齐 file/target_langs/mode/lang，解析出 text/translations/image 等字段。
//   ② "能力缺失/图里没字"是 HTTP 200 + {error,reason} —— 客户端必须转成 ImageError 抛出
//      （否则界面会把一次"没有产出"当成成功结果卡渲染）。
//   ③ 非 2xx 与 200+error 两条失败通道都要带 reason（界面按 reason 取 12 语种词典）。
//
// 运行：npx vitest run src/api/chatImage.test.ts
// ============================================================================
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'

// ★ 必须在 import 客户端之前桩掉 core：API_BASE 取 window.location.origin，
//   jsdom 下有值，但 authHeaders 需要 token 存储——本文件只验报文，桩成空头即可。
vi.mock('./core', () => ({ API_BASE: 'http://test.local', authHeaders: () => ({ Authorization: 'Bearer t' }) }))

import { chatImageTranslate, ImageError } from './chatImage'

// realFetch 原生的 fetch（afterEach 里复位，避免本文件的桩泄漏到其它用例）
const realFetch = globalThis.fetch

beforeEach(() => { vi.restoreAllMocks() })
afterEach(() => { globalThis.fetch = realFetch })

// mockFetch 把 fetch 桩成"回一份 JSON 体 + 指定状态码"，返回桩函数供断言请求要素
function mockFetch(body: unknown, status = 200) {
  const fn = vi.fn(async () => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))
  globalThis.fetch = fn as unknown as typeof fetch
  return fn
}

describe('chatImageTranslate', () => {
  it('① 成功：上报 file/target_langs/mode/lang，解析全部出参字段', async () => {
    const fetchMock = mockFetch({
      text: '你好', translations: { en: 'Hello', ja: 'こんにちは' }, image_lang: 'en',
      image: 'data:image/png;base64,AAAA', image_error: '', line_count: 2,
      points_used: 3, mode: 'pro', target_langs: ['en', 'ja'],
    })
    const file = new File([new Uint8Array([1, 2])], 'a.png', { type: 'image/png' })
    const res = await chatImageTranslate(file, ['en', 'ja'], 'pro', 'zh')
    expect(res.image).toBe('data:image/png;base64,AAAA')
    expect(res.translations.ja).toBe('こんにちは')
    expect(res.line_count).toBe(2)
    // multipart 之间不得手设 Content-Type（boundary 由浏览器带），这里只验请求要素
    const [url, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit]
    expect(url).toBe('http://test.local/api/chat/image')
    expect(init.method).toBe('POST')
    const fd = init.body as FormData
    expect(fd.get('target_langs')).toBe('en,ja')
    expect(fd.get('mode')).toBe('pro')
    expect(fd.get('lang')).toBe('zh')
    expect(fd.get('file')).toBeInstanceOf(File)
  })

  it('② 200 + {error,reason} 必须抛出 ImageError（能力缺失是"没有产出"，不是成功）', async () => {
    mockFetch({ error: 'OCR 不可用', reason: 'capability_missing' }, 200)
    await expect(chatImageTranslate(new File([new Uint8Array([1])], 'a.png'), ['en'], 'pro', 'zh'))
      .rejects.toMatchObject({ reason: 'capability_missing' })
    await expect(chatImageTranslate(new File([new Uint8Array([1])], 'a.png'), ['en'], 'pro', 'zh'))
      .rejects.toBeInstanceOf(ImageError)
  })

  it('③ 图里没字（no_text）同样抛 ImageError 且带档位', async () => {
    mockFetch({ error: '图片里没有可翻译的文字', reason: 'no_text' }, 200)
    try {
      await chatImageTranslate(new File([new Uint8Array([1])], 'a.png'), ['en'], 'pro', 'zh')
      throw new Error('本应抛出')
    } catch (e) {
      expect((e as ImageError).reason).toBe('no_text')
    }
  })
})
