// ============================================================================
// lib/chunkRecovery.dom.test.tsx — ★ 链路批④（#29）分片恢复腿的闸门
//
// 锁的五件事（每条都对应现网「切工单报 Importing a module script failed.」的一处修法口径）：
//  A) 认得出分片失败：三种浏览器措辞 + Vite 的 CSS 预载措辞 + 包一层 reason 的事件 payload
//     都要认；同时**不相干**的错误（网络失败、请求超时）一律不许被误认（误认的代价是把用户
//     正在填的表单整页洗掉）。负向腿配自证：拿「只有 failed 字样」的近邻样本点名射程。
//  B) 一次故障只许自动重拨一次（循环护栏）：第一次分片失败⇒整页重载一次且 URL 落下护栏标记；
//     同一路径上再失败⇒**不再**重拨（否则发版半途、chunk 真不存在时浏览器会变成无限刷新）。
//  C) 用户手点「重新加载页面」＝归还预算：摘掉标记再重载，之后同一路径还能再自动重拨一次。
//  D) 不许 preventDefault（口径 ②）：拦下事件会让 Vite 把「分片取不到」当成解析成功，
//     于是 React.lazy 拿到 undefined 组件、错误形态从「取不到分片」洗成「组件渲染异常」，
//     边界那条人话文案就永远不会出现。这条用**事件对象的 defaultPrevented**实测，不靠读源码。
//  (E/F 两段「接线在位 + 不碰浏览器存储」的静态锁在同目录 chunkRecovery.wiring.test.ts，
//   那边要读源文件文本、必须跑 node 环境——jsdom 里 import.meta.url 是 http: URL，
//   fileURLToPath 会当场抛「The URL must be of scheme file」，整个 suite 起不来。)
//
// ★ 本模块**一个浏览器存储都不碰**（护栏用 URL hash），那条负向锁在 wiring 文件里钉：
//   AGENTS §一·5 批⑤「入口包顶层不许有裸的浏览器存储读」——那个 bug 的表现是整页黑屏，
//   而恢复腿自己也踩同一个雷就是「故障时补一条新故障」。
//
// 运行：npx vitest run src/lib/chunkRecovery.dom.test.tsx
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, beforeAll, beforeEach, afterEach, vi } from 'vitest'
import {
  CHUNK_RETRY_MARK, isChunkLoadError, chunkRetryUsed, reloadForChunkFailure,
  installChunkRecovery, __resetChunkRecoveryForTest, __setChunkReloaderForTest,
} from './chunkRecovery'

/** 现网实证的那一条（Chrome/Edge 把 index.html 当 JS 模块评估时的原生措辞） */
const REAL_NOW = 'Importing a module script failed.'
/** 其余浏览器与 Vite 自己的措辞：漏一条＝那一家的用户仍然看技术原文 */
const WORDINGS = [
  REAL_NOW,
  'error loading dynamically imported module',
  'Failed to fetch dynamically imported module http://x/assets/EditorPage-abc123.js',
  'Unable to preload CSS for /assets/EditorPage-abc123.css',
  'Cannot find module "assets/index-xyz.js"',
]
/** 不相干失败：这些**必须**判否（整页重载的代价太大，误判比漏判坏） */
const NOT_CHUNK = [
  'Failed to load resource: the server responded with a status of 500',
  'TypeError: Cannot read properties of undefined (reading \'map\')',
  new Error('Request failed with status code 404'),
  { message: 'connection timed out' },
  undefined,
  null,
  '',
]

beforeAll(() => {
  // 生产入口就装一次；本文件靠它做事件派发（installed 标记在 F/E 段另有断言）
  __resetChunkRecoveryForTest()
  expect(installChunkRecovery()).toBe(true)
})

beforeEach(() => {
  // 每条用例都从「没有护栏标记」的干净 URL 起步
  window.history.replaceState(null, '', '/')
})

afterEach(() => {
  __setChunkReloaderForTest(null)
})

/** 发一个 vite:preloadError 事件并回报：重拨了几次、事件有没有被拦截、护栏在不在 */
function firePreloadError(payload: unknown) {
  const before = window.location.href
  const ev = new Event('vite:preloadError', { cancelable: true }) as Event & { payload?: unknown }
  ev.payload = payload
  window.dispatchEvent(ev)
  return { before, after: window.location.href, prevented: ev.defaultPrevented, used: chunkRetryUsed() }
}

describe('A 分片失败识别：五家措辞全认，不相干失败全不认', () => {
  for (const w of WORDINGS) {
    it(`认得出：${w.slice(0, 42)}`, () => {
      expect(isChunkLoadError(new Error(w))).toBe(true)
    })
  }
  it('包一层 reason 的 payload 也认（事件形态不止 Error 一种）', () => {
    expect(isChunkLoadError({ reason: new Error(REAL_NOW) })).toBe(true)
    expect(isChunkLoadError(REAL_NOW)).toBe(true) // 字符串形态
  })
  it('反向对照：网络/HTTP/类型错误一律判否（近邻样本点名射程，防「含 failed 就重载」）', () => {
    for (const n of NOT_CHUNK) {
      expect(isChunkLoadError(n), `不该判成分片失败：${String(n)}`).toBe(false)
    }
    // 自证「样本确实落在射程边上」：这条含 failed 但不是分片措辞，判否靠的是名单而不是子串宽泛匹配
    expect(REAL_NOW.toLowerCase()).toContain('failed')
    expect(isChunkLoadError('Download failed')).toBe(false)
  })
})

describe('B+C 循环护栏：自动只一枪、手动归还预算', () => {
  it('第一次分片失败⇒整页重载一次，并在 URL 落下护栏标记', () => {
    const reload = vi.fn()
    __setChunkReloaderForTest(reload)
    const r = firePreloadError(new Error(REAL_NOW))
    expect(reload).toHaveBeenCalledTimes(1)
    expect(r.used).toBe(true)
    expect(r.after).toContain(CHUNK_RETRY_MARK)
    expect(r.prevented).toBe(false) // 口径 ②：绝不拦事件
  })
  it('同一路径再失败⇒不再重拨（否则 chunk 真缺失时浏览器无限刷新）', () => {
    const reload = vi.fn()
    __setChunkReloaderForTest(reload)
    firePreloadError(new Error(REAL_NOW))
    const second = firePreloadError(new Error(REAL_NOW))
    expect(second.used, '第二次失败时护栏必须已是「用过」').toBe(true)
    expect(reload).toHaveBeenCalledTimes(1) // 只此一次
  })
  it('手动重载（force）＝摘掉标记再导航，把自动那一枪的预算归还', () => {
    const reload = vi.fn()
    __setChunkReloaderForTest(reload)
    firePreloadError(new Error(REAL_NOW))
    expect(chunkRetryUsed()).toBe(true)
    expect(reloadForChunkFailure(true)).toBe(true)
    expect(reload).toHaveBeenCalledTimes(2)
    expect(chunkRetryUsed(), '手动那一支必须把标记洗掉').toBe(false)
    // 归还后再来一次自动失败⇒应当又重拨一次（预算真的回来了）
    firePreloadError(new Error(REAL_NOW))
    expect(reload).toHaveBeenCalledTimes(3)
  })
  it('不相干的失败不许动整页（重拨次数保持为 0）', () => {
    const reload = vi.fn()
    __setChunkReloaderForTest(reload)
    const r = firePreloadError(new Error('Failed to load resource: 500'))
    expect(reload).toHaveBeenCalledTimes(0)
    expect(r.used).toBe(false)
    expect(r.after).toBe(r.before) // URL 一字未动
  })
  it('installChunkRecovery 幂等：第二次调用回 false 且不会多挂一个监听器', () => {
    const reload = vi.fn()
    __setChunkReloaderForTest(reload)
    expect(installChunkRecovery(), '已装过必须回 false').toBe(false)
    firePreloadError(new Error(REAL_NOW))
    expect(reload).toHaveBeenCalledTimes(1) // 若挂了两个监听器，这里会是 2
  })
  it('护栏写不进（history.replaceState 抛）时仍然重拨：恢复腿不许被历史 API 故障卡死', () => {
    const reload = vi.fn()
    __setChunkReloaderForTest(reload)
    const spy = vi.spyOn(window.history, 'replaceState').mockImplementation(() => { throw new Error('SecurityError') })
    expect(reloadForChunkFailure()).toBe(true)
    expect(reload).toHaveBeenCalledTimes(1)
    spy.mockRestore()
  })
})
