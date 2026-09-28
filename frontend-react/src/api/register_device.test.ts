// ============================================================================
// register_device.test.ts — 注册请求必须随附合规设备号（★ 2026-09-28 〇-Z · F-81）
// ----------------------------------------------------------------------------
// 要锁的东西：后端 F-81 的「同一浏览器每日新建免费账号」那一档防薅账（rate_limits 的 reg_dev），
// 它的**唯一数据来源就是注册体里的 device_id**。字段一旦在某次重构里被丢掉，后端不会报错——
// 设备号取不到就当没有（刻意不返 400，见 register.go 的第三条理由），于是那一档静默变成只记平台账，
// 刷号脚本换浏览器成本的这条防线整条消失，而前端全绿。这类「两端各自没错、接线断了」正是本文件的射程。
//
// 三条判据各自独立（缺一条就有一类退化看不见）：
//   ① 合规：必须满足服务端白名单 ^[A-Za-z0-9_-]{8,64}$（trialDeviceIDRe）——脏值会污染限流表 key；
//   ② 稳定：同一台浏览器连续两次注册拿到的是**同一个**值——每次现造等于自免设备档，
//      而且它必须就是试用那一份（localStorage 的 lc_trial_device），刻意不新增第二个键：
//      「先刷 5 句试用、再批量注册领免费额度」这类连号行为只在一本账上才看得见；
//   ③ 存储不可用（无痕禁用 storage／配额满）时**照样带一个合规值**，不能退化成不带字段——
//      不带字段＝这笔注册在设备档上免记账，等于给刷号留一个免记账的口子。
// node 环境：先 stub fetch 与 window/localStorage，再动态 import（模块顶层会读存储）。
// ============================================================================
import { beforeEach, describe, expect, it, vi } from 'vitest'

/** 服务端设备号白名单（backend-go/internal/api/trial.go 的 trialDeviceIDRe）：改这里必须同时改那边 */
const SERVER_DEVICE_RE = /^[A-Za-z0-9_-]{8,64}$/

/** 捕获到的注册请求（一次 import 周期内可能发多笔，按调用顺序存） */
type Sent = { url: string; body: Record<string, unknown> }

/** 建一份可开关可用性的 localStorage（throwMode=true 模拟无痕禁用存储） */
function makeStorage(store: Map<string, string>, throwMode = false) {
  return {
    getItem: (k: string) => { if (throwMode) throw new Error('securityerror'); return store.get(k) ?? null },
    setItem: (k: string, v: string) => { if (throwMode) throw new Error('quota'); store.set(k, v) },
    removeItem: (k: string) => { if (throwMode) throw new Error('denied'); store.delete(k) },
  }
}

let sent: Sent[] = []

/** 装配环境并取回 auth 模块（每次调用重置模块态，避免上一例的 storage 值串进来） */
async function loadAuth(opts: { throwStorage?: boolean } = {}) {
  vi.resetModules()
  const store = new Map<string, string>()
  const ls = makeStorage(store, !!opts.throwStorage)
  vi.stubGlobal('localStorage', ls)
  // trialDevice.ts 走的是 window.localStorage，core.ts 的部分读法走裸 localStorage，两边都给
  vi.stubGlobal('window', { localStorage: ls, location: { href: 'http://localhost/' } })
  vi.stubGlobal('sessionStorage', makeStorage(new Map<string, string>()))
  sent = []
  vi.stubGlobal('fetch', vi.fn(async (url: string, init?: { body?: string }) => {
    sent.push({ url: String(url), body: JSON.parse(String(init?.body ?? '{}')) })
    return { ok: true, status: 200, json: async () => ({ success: true }), text: async () => '{"success":true}' } as unknown as Response
  }))
  return import('./auth')
}

describe('api/auth · F-81 注册设备号', () => {
  beforeEach(() => { vi.unstubAllGlobals() })

  it('① 未显式传 device_id 时出口兜上，且值满足服务端白名单', async () => {
    const auth = await loadAuth()
    await auth.authRegister({ username: 'u_probe', password: 'x'.repeat(8), agreed: true })
    expect(sent).toHaveLength(1)
    const body = sent[0].body
    expect(typeof body.device_id).toBe('string')
    expect(String(body.device_id)).toMatch(SERVER_DEVICE_RE)
    // 反证「兜的是试用那一份标识」：同一键、同一值（新增第二个键＝两本账，连号行为就断了）
    expect(globalThis.localStorage.getItem('lc_trial_device')).toBe(String(body.device_id))
  })

  it('② 同一浏览器两次注册拿到同一个设备号（不得每次现造）', async () => {
    const auth = await loadAuth()
    await auth.authRegister({ username: 'u_a', password: 'x'.repeat(8), agreed: true })
    await auth.authRegister({ username: 'u_b', password: 'x'.repeat(8), agreed: true })
    expect(sent).toHaveLength(2)
    const d1 = String(sent[0].body.device_id)
    const d2 = String(sent[1].body.device_id)
    expect(d1).toMatch(SERVER_DEVICE_RE)
    // 这条是防薅账成立的前提：每次新生成＝设备档恒为 1，刷号脚本毫无成本
    expect(d2).toBe(d1)
  })

  it('③ 存储不可用时仍带合规设备号（不得退化成不带字段）', async () => {
    const auth = await loadAuth({ throwStorage: true })
    await auth.authRegister({ username: 'u_nostore', password: 'x'.repeat(8), agreed: true })
    const body = sent[0].body
    expect(typeof body.device_id).toBe('string')
    expect(String(body.device_id)).toMatch(SERVER_DEVICE_RE)
  })
})
