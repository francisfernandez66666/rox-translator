// ============================================================================
// api/barefetch.test.ts — §4.2-2「裸 fetch 豁免清单」鉴权语义回归（node 环境）
// 背景：统一 client（core.request）之外，四类通道因需要二进制/SSE/独立服务而**正当**裸用 fetch：
//   ① referral.fetchReferralQrBlob（二维码 PNG blob）
//   ② tickets.ticketDownload（工单结果文件 blob）
//   ③ admin.downloadUserImportTemplate（导入模板 blob，boolean 契约）
//   ④ assist.*（独立 ai-assist 服务，会话能力令牌鉴权）
//   前三类必须与统一 client 的 401/403 语义**同源**（复用 handleUnauthorized/handleForbidden），
//   第四类必须**刻意不同源**（assist 的 401 只清本地会话，绝不能把用户从整个应用登出）。
// 为什么值得测：这些是「防回退的最后一道」——同事 #57 刚把散落的不一致鉴权收敛齐；
//   下一版很容易有人为了省事把 assist 也接上 handleUnauthorized（误踢全站登录），
//   或把前三类的 403 又折叠回静默 null/false（丢越权提示）。没有断言就会静默退化。
// 运行：npx vitest run src/api/barefetch.test.ts
// ============================================================================
import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'

type Core = typeof import('./core')
let core: Core

beforeAll(async () => {
  // 与 core.test.ts 同口径：先补 sessionStorage/window，再动态 import（core 顶层读 storage）
  const mem = new Map<string, string>()
  vi.stubGlobal('sessionStorage', {
    getItem: (k: string) => mem.get(k) ?? null,
    setItem: (k: string, v: string) => { mem.set(k, v) },
    removeItem: (k: string) => { mem.delete(k) },
  })
  vi.stubGlobal('window', { location: { href: 'http://localhost/' } })
  core = await import('./core')
})

/** fakeResp 造最小 fetch Response：按需给 json/text/blob/headers，状态码决定 ok */
function fakeResp(status: number, opts: { body?: unknown; text?: string; blob?: unknown } = {}): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    json: async () => (opts.body === undefined ? (() => { throw new SyntaxError('no json') })() : opts.body),
    text: async () => opts.text ?? '',
    blob: async () => opts.blob ?? { kind: 'blob' },
    headers: { get: () => '' },
  } as unknown as Response
}

beforeEach(() => {
  core.setAuthToken('valid-app-token')
  core.setUnauthorizedHandler(null)
  core.setForbiddenCopyResolver(null)
})

describe('§4.2-2 豁免①：referral.fetchReferralQrBlob 与统一 client 同源', () => {
  it('401 走 handleUnauthorized（清登录态、触发复位钩子）并静默返回 null', async () => {
    const { fetchReferralQrBlob } = await import('./referral')
    let hookCalls = 0
    core.setUnauthorizedHandler(() => { hookCalls++ })
    vi.stubGlobal('fetch', async () => fakeResp(401, { body: { message: '登录失效' } }))
    await expect(fetchReferralQrBlob()).resolves.toBeNull()
    expect(core.getAuthToken(), '401 必须清登录态（与 request() 一致）').toBe('')
    expect(hookCalls, '401 必须触发复位钩子').toBe(1)
  })

  it('403 抛 FORBIDDEN、不清登录态、文案走统一解析器', async () => {
    const { fetchReferralQrBlob } = await import('./referral')
    let hookCalls = 0
    core.setUnauthorizedHandler(() => { hookCalls++ })
    core.setForbiddenCopyResolver(() => '无权限访问')
    vi.stubGlobal('fetch', async () => fakeResp(403, { body: { message: '越权' } }))
    await expect(fetchReferralQrBlob()).rejects.toMatchObject({
      name: 'ApiError', status: 403, code: 'FORBIDDEN', message: '无权限访问',
    })
    expect(core.getAuthToken(), '403 不得清登录态').toBe('valid-app-token')
    expect(hookCalls, '403 不得触发 401 复位钩子').toBe(0)
  })

  it('非鉴权类失败（500）仍维持旧的「返回 null 静默降级」口径，不误抛', async () => {
    const { fetchReferralQrBlob } = await import('./referral')
    vi.stubGlobal('fetch', async () => fakeResp(500))
    await expect(fetchReferralQrBlob()).resolves.toBeNull()
    expect(core.getAuthToken(), '普通失败不动登录态').toBe('valid-app-token')
  })
})

describe('§4.2-2 豁免②：tickets.ticketDownload 与统一 client 同源', () => {
  it('401 触发 handleUnauthorized 副作用并抛错（旧实现把 401 当普通 Error 丢语义）', async () => {
    const { ticketDownload } = await import('./tickets')
    let hookCalls = 0
    core.setUnauthorizedHandler(() => { hookCalls++ })
    vi.stubGlobal('fetch', async () => fakeResp(401, { body: { message: '未登录' } }))
    await expect(ticketDownload(1)).rejects.toBeTruthy()
    expect(core.getAuthToken()).toBe('')
    expect(hookCalls).toBe(1)
  })

  it('403 抛 ApiError FORBIDDEN（走统一文案），与 request() 口径一致', async () => {
    const { ticketDownload } = await import('./tickets')
    core.setForbiddenCopyResolver(() => '无管理权限')
    vi.stubGlobal('fetch', async () => fakeResp(403, { body: { message: '越权下载' } }))
    await expect(ticketDownload(2)).rejects.toMatchObject({
      name: 'ApiError', status: 403, code: 'FORBIDDEN', message: '无管理权限',
    })
    expect(core.getAuthToken(), '403 不清登录态').toBe('valid-app-token')
  })
})

describe('§4.2-2 豁免③：admin.downloadUserImportTemplate（boolean 契约）', () => {
  it('401 清登录态 + 触发钩子，并回落 false（不抛未捕获拒绝）', async () => {
    const { downloadUserImportTemplate } = await import('./admin')
    let hookCalls = 0
    core.setUnauthorizedHandler(() => { hookCalls++ })
    vi.stubGlobal('fetch', async () => fakeResp(401, { body: {} }))
    await expect(downloadUserImportTemplate()).resolves.toBe(false)
    expect(core.getAuthToken()).toBe('')
    expect(hookCalls).toBe(1)
  })

  it('403 触发统一解析器后回落 false（越权文案已收口，仍维持 boolean 契约）', async () => {
    const { downloadUserImportTemplate } = await import('./admin')
    const resolver = vi.fn(() => '无权限')
    core.setForbiddenCopyResolver(resolver)
    vi.stubGlobal('fetch', async () => fakeResp(403, { body: { message: '越权' } }))
    await expect(downloadUserImportTemplate()).resolves.toBe(false)
    expect(resolver, '403 文案必须经统一解析器收口').toHaveBeenCalledTimes(1)
    expect(core.getAuthToken(), '403 不清登录态').toBe('valid-app-token')
  })
})

// ★ 反向外溢防线：assist 是「独立服务 + 会话能力令牌」鉴权模型，它的 401/403
//   只能做会话级失效（清本地 sid/tok 走重新 greet 自愈），**绝不能**复用 app 级
//   handleUnauthorized——否则一次会话令牌过期就把用户从整个应用踢下线。
//   这条断言专门锁死「assist 不外溢主站登录态」，是防回退的核心。
describe('§4.2-2 豁免④：assist 与主站鉴权刻意不同源（不得外溢清登录态）', () => {
  it('assist 401 只清本地会话（sid/tok），不动主站 token、不触发 401 复位钩子', async () => {
    const { setAssistSid, getAssistSid, assistHistory } = await import('./assist')
    setAssistSid('sess-abc') // setAssistSid 会写 sid；令牌由 greet 落存，这里只验会话被清
    let hookCalls = 0
    core.setUnauthorizedHandler(() => { hookCalls++ })
    vi.stubGlobal('fetch', async () => fakeResp(401))
    const msgs = await assistHistory('sess-abc')
    expect(msgs, '401 走空数组（重新 greet 自愈）而非抛错').toEqual([])
    expect(getAssistSid(), '会话令牌失效应清本地 sid').toBe('')
    expect(core.getAuthToken(), 'assist 失效不得清主站登录态').toBe('valid-app-token')
    expect(hookCalls, 'assist 失效不得触发主站 401 复位钩子').toBe(0)
  })

  it('assist 403 同样仅会话级失效（清 sid），不外溢主站、不抛 FORBIDDEN', async () => {
    const { setAssistSid, getAssistSid, assistFeatures } = await import('./assist')
    setAssistSid('sess-xyz')
    let hookCalls = 0
    core.setUnauthorizedHandler(() => { hookCalls++ })
    vi.stubGlobal('fetch', async () => fakeResp(403))
    await expect(assistFeatures()).resolves.toEqual([])
    expect(getAssistSid()).toBe('')
    expect(core.getAuthToken()).toBe('valid-app-token')
    expect(hookCalls).toBe(0)
  })
})
