// ============================================================================
// bizResp.test.ts — bizResp 收敛器的行为锁（★ F-64① 批 I-7，2026-09-26）
//
// 为什么单独一份：批 I-7 把收款/账务七个后端文件的「HTTP 200 承载失败」改成诚实状态码
// （400/401/403/404/409/500/503）。后端一改，request() 就会对这些响应**抛异常**，
// 而全站数百处调用点的约定是 `if (!r.success)` / `toastResp(r)`。
// bizResp 是接上这两边的唯一薄层：它决定「哪些失败还原成响应体、哪些必须照旧抛出」。
//
// 这个取舍一旦反向，两种事故都不显眼：
//   ① 收敛过度（把网络层失败也还原成 success:false）⇒ 断网时界面显示「操作失败」而不是
//      「连不上服务」，运维排查方向被带偏；
//   ② 收敛不足（4xx 也抛出去）⇒ 点「订阅」失败变成未捕获 rejection，界面毫无提示。
// 所以两侧都要钉：能还原的逐字段核（含 details 摊平与信封优先级），不能还原的必须抛。
//
// node 环境：与 core.test.ts 同口径——先 stub sessionStorage，再动态 import
// （core 模块顶层读 sessionStorage，静态 import 会在 node 环境解析期即崩）。
// ============================================================================
import { beforeAll, describe, expect, it, vi } from 'vitest'

type Core = typeof import('./core')
let core: Core

beforeAll(async () => {
  const mem = new Map<string, string>()
  vi.stubGlobal('sessionStorage', {
    getItem: (k: string) => mem.get(k) ?? null,
    setItem: (k: string, v: string) => { mem.set(k, v) },
    removeItem: (k: string) => { mem.delete(k) },
  })
  vi.stubGlobal('window', { location: { href: 'http://localhost/' } })
  core = await import('./core')
})

/** jsonResp 构造 fetch mock 的 Response（status 决定 ok，与真实 fetch 语义一致） */
function jsonResp(body: unknown, status: number): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    json: async () => body,
    text: async () => JSON.stringify(body),
  } as unknown as Response
}

/** textResp 构造「非 JSON 响应」的 fetch mock（网关/CF 错误页形态） */
function textResp(text: string, status: number, contentType = 'text/html'): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    json: async () => { throw new Error('Unexpected token < in JSON') },
    text: async () => text,
    headers: { get: (k: string) => (k.toLowerCase() === 'content-type' ? contentType : null) },
  } as unknown as Response
}

/** mockFetch 让 request() 走指定响应 */
function mockFetch(r: Response | (() => Promise<never>)) {
  vi.stubGlobal('fetch', async () => {
    if (typeof r === 'function') return r()
    return r
  })
}

describe('bizResp（F-64① 状态码诚实的前端薄层）', () => {
  it('① 404 结构化失败 → 还原成 {success:false, code, message, status}，调用点的 if (!r.success) 走得到', async () => {
    mockFetch(jsonResp({ success: false, code: 'NOT_FOUND', message: '订单不存在', trace_id: 't-1' }, 404))
    const r = (await core.bizResp(() => core.request('/api/pay/status?order_id=1'))) as Record<string, unknown>
    expect(r.success).toBe(false)
    expect(r.code).toBe('NOT_FOUND')
    expect(r.message).toBe('订单不存在')
    expect(r.status).toBe(404) // 诚实状态码不丢：调用点想按 404 停轮询仍然读得到
    expect(r.trace_id).toBe('t-1') // 信封其余字段透传（排查用）
  })

  it('② details 摊平到顶层（旧 200 壳的 order_no/coupon_error 附加字段形状不变）', async () => {
    mockFetch(jsonResp({
      success: false, code: 'VALIDATION_ERROR', message: '券码不适用于本单',
      details: { order_no: 'LC20260926X', coupon_error: 'kind_mismatch' },
    }, 400))
    const r = (await core.bizResp(() => core.request('/api/pay/create'))) as Record<string, unknown>
    expect(r.order_no).toBe('LC20260926X') // 建单失败留 pending，收银台要拿单号提示人工处理
    expect(r.coupon_error).toBe('kind_mismatch')
    expect(r.details).toBeUndefined() // 已摊平，不再留嵌套壳（否则调用点两套读法并存）
  })

  it('③ 优先级锁：details 里若有同名字段，不许盖掉错误信封的 success/message/code', async () => {
    // 后端理论上不会这么发，但这是**摊平顺序**唯一会被写反的地方（{...rest, ...flat} 就中了），
    // 写反的后果是 success 被 details.success 覆盖成 true —— 静默假成功，最难查的一类。
    mockFetch(jsonResp({
      success: false, code: 'CONFLICT', message: '订单状态已变更',
      details: { success: true, message: '来自 details 的伪造文案' },
    }, 409))
    const r = (await core.bizResp(() => core.request('/api/package/subscribe'))) as Record<string, unknown>
    expect(r.success).toBe(false)
    expect(r.message).toBe('订单状态已变更')
    expect(r.code).toBe('CONFLICT')
  })

  it('④ 401 必须照旧抛出（不许收敛）：request() 已触发全局清态回登录页，调用方要感知「这次没做成」', async () => {
    mockFetch(jsonResp({ success: false, code: 'UNAUTHORIZED', message: '未登录' }, 401))
    await expect(core.bizResp(() => core.request('/api/pay/create'))).rejects.toBeInstanceOf(core.ApiError)
    // 反证：不能是「resolve 了一个 success:false」——那会让用户停在收银台上以为还能继续操作
    const settled = await core.bizResp(() => core.request('/api/pay/create')).then(() => 'resolved', () => 'rejected')
    expect(settled).toBe('rejected')
  })

  it('⑤ 403 必须照旧抛出：越权与登录态失效是两类问题，收敛口径不同（见 core.ts §4.2-3）', async () => {
    mockFetch(jsonResp({ success: false, code: 'FORBIDDEN', message: '无权访问' }, 403))
    await expect(core.bizResp(() => core.request('/api/admin/orders/refund'))).rejects.toBeInstanceOf(core.ApiError)
  })

  it('⑥ 网关/HTML 错误页（无结构化体）不得伪装成业务失败', async () => {
    // Cloudflare 5xx 页、网关超时 HTML：body 解析不出 JSON → ApiError.body 为 undefined，
    // 若此时收敛，界面会显示「请求失败 (502): <html…>」这种被当成后端文案的东西。
    mockFetch(textResp('<html><body>Bad Gateway</body></html>', 502))
    await expect(core.bizResp(() => core.request('/api/pay/create'))).rejects.toBeInstanceOf(core.ApiError)
  })

  it('⑦ 网络层失败（fetch reject）不得收敛', async () => {
    mockFetch(() => Promise.reject(new TypeError('fetch failed')))
    await expect(core.bizResp(() => core.request('/api/pay/create'))).rejects.toThrow()
  })

  it('⑧ 成功响应原样透传（bizResp 不许给成功体加任何字段）', async () => {
    const body = { success: true, order: { order_no: 'LC1', amount_points: 200 }, channel: 'mock' }
    mockFetch(jsonResp(body, 200))
    const r = await core.bizResp(() => core.request('/api/pay/create'))
    expect(r).toEqual(body) // 深相等：多挂一个 status/code 键都算改契约
  })

  it('⑨ 200 + success:false（历史业务失败，仍不走异常）同样原样返回', async () => {
    // 钉的是「bizResp 不主动翻成功/失败」：②档③档尚未清扫的接口仍回 200 壳，
    // 收敛器若顺手把 200 也处理一遍，就会与调用点的 toastResp(r) 抢口径。
    const body = { success: false, message: '余额不足' }
    mockFetch(jsonResp(body, 200))
    expect(await core.bizResp(() => core.request('/api/coupon/preview'))).toEqual(body)
  })
})
