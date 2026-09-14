// ============================================================================
// core.test.ts — API 基础设施回归（★ F11：E4 headers 合并 / E5 401 处理 /
// 超时语义 / 统一错误码透传）
// node 环境：先 stub sessionStorage/window，再动态 import core（模块顶层读 storage）。
// ============================================================================
import { beforeAll, describe, expect, it, vi } from 'vitest'

type Core = typeof import('./core')
let core: Core
let fakeWindow: { location: { href: string } }

beforeAll(async () => {
  const mem = new Map<string, string>()
  vi.stubGlobal('sessionStorage', {
    getItem: (k: string) => mem.get(k) ?? null,
    setItem: (k: string, v: string) => { mem.set(k, v) },
    removeItem: (k: string) => { mem.delete(k) },
  })
  fakeWindow = { location: { href: 'http://localhost/' } }
  vi.stubGlobal('window', fakeWindow)
  core = await import('./core')
})

function jsonResponse(body: unknown, status = 200): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    json: async () => body,
    text: async () => JSON.stringify(body),
  } as unknown as Response
}

describe('api/core', () => {
  it('authHeaders：登录 token 与生效租户头', () => {
    core.setAuthToken('tk-test')
    core.setActiveTenantId(42)
    const h = core.authHeaders()
    expect(h.Authorization).toBe('Bearer tk-test')
    expect(h['X-Tenant-ID']).toBe('42')
  })

  it('E4：调用方自带 headers 不再吞掉 Content-Type/认证头（合并在展开之后）', async () => {
    let seen: Record<string, string> | undefined
    vi.stubGlobal('fetch', async (_u: string, init: RequestInit) => {
      seen = init.headers as Record<string, string>
      return jsonResponse({ success: true })
    })
    await core.request('/x', { method: 'POST', headers: { 'X-Custom': '1' }, body: '{}' })
    expect(seen!['X-Custom']).toBe('1')
    expect(seen!['Content-Type']).toBe('application/json')
    expect(seen!.Authorization).toBe('Bearer tk-test')
  })

  it('★ 2026-09-14 multipart 回归：FormData 请求不得预设 Content-Type（否则 boundary 丢失 → 后端 400「文件解析失败或超过大小上限」）', async () => {
    let seen: Record<string, string> | undefined
    vi.stubGlobal('fetch', async (_u: string, init: RequestInit) => {
      seen = init.headers as Record<string, string>
      return jsonResponse({ success: true })
    })
    const fd = new FormData()
    fd.append('files', new Blob(['hi'], { type: 'text/plain' }), 'a.txt')
    await core.request('/api/tickets/create-file', { method: 'POST', headers: core.authHeaders(), body: fd })
    expect(seen!['Content-Type'], 'FormData 请求必须交由浏览器自动生成 multipart boundary').toBeUndefined()
    expect(seen!.Authorization).toBe('Bearer tk-test') // 认证头不因此丢失
  })

  it('非 2xx：ApiError 携带 message 与稳定 code（含 error_code 别名）', async () => {
    vi.stubGlobal('fetch', async () => jsonResponse({ success: false, message: '余额不足', error_code: 'insufficient_balance' }, 402))
    await expect(core.request('/pay')).rejects.toMatchObject({
      name: 'ApiError', message: '余额不足', code: 'insufficient_balance', status: 402,
    })
  })

  it('E5：401 清 token 并跳登录一次（authRedirecting 去重）', async () => {
    core.setAuthToken('will-expire')
    expect(core.getAuthToken()).toBe('will-expire')
    fakeWindow.location.href = 'http://localhost/'
    let redirectSeen = 0
    vi.stubGlobal('fetch', async () => {
      const r = jsonResponse({ success: false }, 401)
      return r
    })
    await expect(core.request('/api/me')).rejects.toBeTruthy()
    redirectSeen = fakeWindow.location.href === '/' ? 1 : 0
    expect(core.getAuthToken()).toBe('')
    expect(redirectSeen).toBe(1)
    // 二次 401：token 仍清、不再重复跳转（去重标志）
    fakeWindow.location.href = 'http://localhost/'
    await expect(core.request('/api/me2')).rejects.toBeTruthy()
    expect(fakeWindow.location.href).toBe('http://localhost/')
  })

  it('E5：登录接口自身 401 不触发跳转/清 token', async () => {
    core.setAuthToken('keep-me')
    fakeWindow.location.href = 'http://localhost/'
    vi.stubGlobal('fetch', async () => jsonResponse({ success: false, message: '密码错误' }, 401))
    await expect(core.request('/api/auth/login', { method: 'POST' })).rejects.toBeTruthy()
    expect(core.getAuthToken()).toBe('keep-me')
    expect(fakeWindow.location.href).toBe('http://localhost/')
  })

  it('超时：timeoutMs 内未决 → 明确中文错误（区别于外部 abort）', async () => {
    vi.stubGlobal('fetch', (_u: string, init: RequestInit) => new Promise((_res, rej) => {
      init.signal!.addEventListener('abort', () => rej(new DOMException('aborted', 'AbortError')))
    }))
    await expect(core.request('/slow', { timeoutMs: 20 })).rejects.toThrow(/超时/)
  })
})
