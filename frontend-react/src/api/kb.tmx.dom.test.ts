// ============================================================================
// api/kb.tmx.dom.test.ts — ★ #38 TMX 导出下载链路回归（2026-09-21）
// 被测：kb.tmxExport()。它是「后端端点早已存在、前端没有入口」那类缺陷的补口，
//   最易回归的三点：① 必须带鉴权头（裸 <a href> 下载会被 401 挡下）；
//   ② 「仅已审核」开关要真的映射到 module=approved，否则是个假复选框；
//   ③ 失败必须抛出后端 message（旧写法直接吐 blob，用户点了没反应）。
// 运行：npx vitest run src/api/kb.tmx.dom.test.ts
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'

// core 只做最小替身：鉴权头 / 401 拦截 / API_BASE 都不该被本用例真实触发
vi.mock('./core', () => ({
  API_BASE: '',
  authHeaders: () => ({ Authorization: 'Bearer test-token' }),
  handleUnauthorized: vi.fn(),
}))

const { tmxExport } = await import('./kb')
const { handleUnauthorized } = await import('./core')

interface Resp { ok?: boolean; status?: number; disposition?: string; json?: unknown; body?: string }

/** 装一个 fetch 响应替身（只实现 tmxExport 真正用到的那几个成员） */
function makeResp(r: Resp) {
  const headers = new Headers()
  if (r.disposition) headers.set('Content-Disposition', r.disposition)
  return {
    ok: r.ok ?? true, status: r.status ?? 200, headers,
    blob: async () => new Blob([r.body ?? '<tmx/>'], { type: 'application/x-tmx' }),
    json: async () => (r.json ?? {}) as never,
  }
}

const clicked: { download: string }[] = []
let fetchImpl: (url: string, init?: RequestInit) => Promise<unknown> = async () => makeResp({})
const fetchMock = vi.fn((url: string, init?: RequestInit) => fetchImpl(url, init))

/** 下一个请求返回指定响应，并返回可断言调用参数的 fetch mock */
function respondWith(r: Resp) {
  fetchImpl = async () => makeResp(r)
  return fetchMock
}

beforeEach(() => {
  clicked.length = 0
  fetchMock.mockClear()
  fetchImpl = async () => makeResp({})
  vi.stubGlobal('fetch', fetchMock)
  // jsdom 没有 createObjectURL/revokeObjectURL：给个假 URL 即可，下载动作由 anchor.click 承接
  Object.defineProperty(URL, 'createObjectURL', { value: () => 'blob:fake', configurable: true })
  Object.defineProperty(URL, 'revokeObjectURL', { value: () => { /* noop */ }, configurable: true })
  vi.spyOn(window.HTMLAnchorElement.prototype, 'click').mockImplementation(function (this: HTMLAnchorElement) {
    clicked.push({ download: this.download })
  })
})

afterEach(() => { vi.unstubAllGlobals(); vi.restoreAllMocks() })

describe('kb.tmxExport', () => {
  it('① 带鉴权头请求导出端点，并按 Content-Disposition 命名下载文件', async () => {
    const m = respondWith({ disposition: 'attachment; filename="langcross_tm_20260921.tmx"' })
    await tmxExport()
    expect(m).toHaveBeenCalledTimes(1)
    expect(String(m.mock.calls[0][0])).toBe('/api/translation/export-tmx')
    expect((m.mock.calls[0][1] as RequestInit).headers).toEqual({ Authorization: 'Bearer test-token' })
    expect(clicked).toHaveLength(1)
    expect(clicked[0].download).toBe('langcross_tm_20260921.tmx')
  })

  it('② 「仅已审核」开关必须真的映射到 module=approved', async () => {
    const m = respondWith({})
    await tmxExport({ module: 'approved' })
    expect(String(m.mock.calls[0][0])).toBe('/api/translation/export-tmx?module=approved')
  })

  it('③ 失败时抛出后端 message，不静默产出空文件', async () => {
    respondWith({ ok: false, status: 403, json: { success: false, message: '需要部门管理员权限' } })
    await expect(tmxExport()).rejects.toThrow('需要部门管理员权限')
    expect(clicked).toHaveLength(0)
  })

  it('④ 401 走统一登录失效拦截（与其余接口同口径）', async () => {
    respondWith({ ok: false, status: 401, json: { success: false } })
    await expect(tmxExport()).rejects.toThrow()
    expect(handleUnauthorized).toHaveBeenCalled()
  })

  it('⑤ 响应缺 Content-Disposition 时回落本地时间戳文件名（仍是 .tmx 后缀）', async () => {
    respondWith({})
    await tmxExport()
    expect(clicked[0].download).toMatch(/^langcross_tm_\d{14}\.tmx$/)
  })
})
