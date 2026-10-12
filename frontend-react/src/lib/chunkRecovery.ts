// ============================================================================
// lib/chunkRecovery.ts — 懒加载分片（chunk）取不到时的「自动补一次真重载」恢复腿
//
// 现网症状（★ 2026-10-13 用户报，任务 #29）：已开着的工作台里切换翻译工单，整页翻成
// 「页面出现异常 / Importing a module script failed.」。
//
// 根因是**交付链的时序**，不是页面代码坏了：
//   前端按路由做代码分割（App.tsx 里一批 React.lazy），每个页面的 JS 是一个带哈希名的
//   chunk 文件，名字写在**首页 HTML** 里；发版换源（《部署指南》§五 的 tar 落位）会把
//   web 根整个换成新的一份，于是**用户浏览器里那一页还是上一代的 HTML**，它记着的哈希名
//   在新目录里不存在。切工单＝第一次要去拉 `/assets/EditorPage-<旧哈希>.js`，
//   而 `spa.go` 对**磁盘上不存在的路径**回退成 index.html（状态码仍是 200，见 AGENTS §一·6
//   那条「兜底陷阱」）⇒ 浏览器拿到一份 HTML 当 JS 模块评估，当场抛
//   "Importing a module script failed."。
//
// 为什么现有的两条恢复路都不够：
//   · ErrorBoundary 的「重试」只是把边界状态复位、让 React 重渲染一次——React.lazy 对
//     **已经 reject 的 import promise 是永久缓存的**，同一片 chunk 第二次也不会去重拨；
//   · 「返回首页」是 SPA 内跳转，拿的还是同一份旧 HTML，换个页面照样撞同一个不存在的名字。
//   ⇒ 唯一能把「新 HTML」换进来的是**整页重新加载**，本模块就只补这一条腿。
//
// 三条硬口径：
//  ① 一次故障**只许自动重拨一次**。护栏用 URL hash（不用 localStorage/sessionStorage：
//     浏览器拒站点数据时读存储会抛 SecurityError，正是启动链黑屏批那一族，AGENTS §一·5 批⑤）；
//     hash 在任何一次正常路由跳转后自然消失＝这笔预算会自动归还，不需要再造一份过期逻辑。
//  ② **不许** `preventDefault()` 那个事件：Vite 的 `__vitePreload` 只在「没人拦截」时才把
//     异常照旧抛出（见其源码 handlePreloadError），而我们要让**路由级 ErrorBoundary** 仍能在
//     「自动那一枪也已经用掉」的第二次失败里出包人话。拦下来会让懒加载**当成解析成功**、
//     组件类型变成 undefined，把「取不到分片」这个真缺陷洗成「组件渲染异常」这个假症状。
//  ③ 只认**分片取不到**这一族错误文案（下面 CHUNK_MARKS 名单），别的面（图片 404、CSS 之外的
//     资源失败）不许把整页 reload——那会把用户正在填的表单洗掉。
//
// 闸门＝src/lib/chunkRecovery.test.ts（判据等值 + 六枚反证）
//       src/components/ErrorBoundary.dom.test.tsx（守卫级：真重载 / 第二次不重载 / 复位 / 出文案）
//       src/bootStorageGate.test.ts A 段（顶层零裸存储读——本模块刻意一个都不碰）。
// ============================================================================

/** 自动重拨护栏标记：出现在当前 URL 的 hash 里＝这一跳已经烧掉唯一一次自动重载 */
export const CHUNK_RETRY_MARK = 'chunk-retry'

/**
 * 分片加载失败的错误文案名单（**逐语种浏览器读数不同**，所以按小写子串判，不做等值）。
 * 前两条是现网实证（Chromium／Safari 各自的措辞），后三条是同一失败形态的已知变体
 * （Firefox 措辞 + Vite 自己包装的 CSS 预载失败 + Rollup 运行时）。
 */
const CHUNK_MARKS = [
  'importing a module script failed', // Chrome/Edge：HTML 冒充 JS 模块时的原生措辞（本批现网读数）
  'error loading dynamically imported module', // Safari
  'failed to fetch dynamically imported module', // Firefox
  'unable to preload css', // Vite：__vitePreload 里 CSS link 的 error 事件
  'cannot find module', // 兜底档：哈希名被清理后偶见的措辞
]

/** 取错误体里的可读消息：字符串、Error、以及只有 message 属性的类异常都要认。 */
function messageOf(err: unknown): string {
  if (typeof err === 'string') return err
  if (err && typeof err === 'object') {
    const m = (err as { message?: unknown; reason?: unknown }).message
    if (typeof m === 'string' && m) return m
    const r = (err as { reason?: unknown }).reason // vite:preloadError 的 payload 有时包一层
    if (r && typeof r === 'object') {
      const rm = (r as { message?: unknown }).message
      if (typeof rm === 'string') return rm
    }
  }
  return ''
}

/**
 * isChunkLoadError 判「这一串异常是不是懒加载分片取不到」。
 * 参数 err 任意异常形态（Error／字符串／事件 payload／undefined）。
 * 返回 true＝属本模块射程（可以据此整页重拨）；false＝不相干，一律不动页面。
 *
 * 判据刻意只问文案：这一族在三种浏览器里的措辞各不相同，而「网络断了」「500」这类
 * 正常错误**不许**被整页重载掩盖（代价是洗掉用户正在填的表单）。
 */
export function isChunkLoadError(err: unknown): boolean {
  const msg = messageOf(err)
  if (!msg) return false
  const low = msg.toLowerCase()
  return CHUNK_MARKS.some((k) => low.includes(k))
}

/** 安全读当前 hash：jsdom／异常宿主下 location 取不到就按「没有标记」处理（不许抛）。 */
function currentHash(): string {
  try {
    return typeof window !== 'undefined' && window.location ? window.location.hash || '' : ''
  } catch {
    return '' // 取不到就当作没用过护栏：最坏结果是多重拨一次，不是卡死
  }
}

/** chunkRetryUsed 问「这一跳的唯一一次自动重载是不是已经用掉了」。 */
export function chunkRetryUsed(): boolean {
  return currentHash().slice(1).split(/[&=]/).includes(CHUNK_RETRY_MARK)
}

/** 把标记从既有 hash 里摘掉（保留其余片段），用于手动重载时归还预算。 */
function stripRetryMark(hash: string): string {
  const raw = hash.replace(/^#/, '')
  if (!raw) return ''
  const kept = raw.split('&').filter((seg) => seg && seg !== CHUNK_RETRY_MARK)
  return kept.length ? `#${kept.join('&')}` : ''
}

/**
 * reloadForChunkFailure 烧掉护栏并整页重载（真重载＝重新取首页 HTML，chunk 名跟着换新一代）。
 * 参数 force 是否为**用户点按钮**触发（true＝无视护栏，并先摘掉标记把预算归还）。
 * 返回 true＝已发出重载；false＝护栏判定这一跳已自动重拨过一次，交给界面出「手动重载」文案。
 *
 * ⚠️ 这里的顺序是「先落护栏、后重载」：反过来（先 reload 再写标记）标记就随着旧文档一起没了，
 *    下一次失败会再重拨，形成现网最难查的那种「每 30 秒刷一次页面」。
 * ⚠️ replaceState 用 try 包住：个别沙箱／特殊协议下它会抛，护栏写不进去也不能把恢复腿整个卡住
 *    （那种情况下最坏是多重拨一次，而 location.reload() 本身就是我们要的效果）。
 */
export function reloadForChunkFailure(force = false): boolean {
  if (!force && chunkRetryUsed()) return false
  let next = ''
  try {
    const href = window.location.href
    // 保留 hash 里**不属于本护栏**的那一段（业务偶尔用 hash 定位），只动我们自己的标记
    const kept = stripRetryMark(currentHash())
    // 手动那一支（force）：摘掉标记＝归还预算，下一次失败还能再自动重拨一次
    // 自动那一支：追加标记＝烧掉这一跳唯一一次自动重拨
    const newHash = force ? kept : kept ? `${kept}&${CHUNK_RETRY_MARK}` : `#${CHUNK_RETRY_MARK}`
    next = `${href.split('#')[0]}${newHash}`
    window.history.replaceState(null, '', next)
  } catch {
    next = '' // 护栏没写成也照样重拨一次：这台的浏览器存储/历史API 坏了不该让人打不开页面
  }
  try {
    reloader()
  } catch (e) {
    console.error('[chunkRecovery] 整页重载失败，请手动刷新', e)
    return false
  }
  return true
}

/** 模块级「只装一次」标记：StrictMode 与测试重复 import 都不该挂出第二个监听器。 */
let installed = false

/**
 * 整页重载动作的唯一出口（默认就是 `window.location.reload()`）。
 * ⚠️ 之所以隔一层：jsdom 的 `location.reload()` 是「Not implemented: navigation」空桩，
 *    闸门既数不到重载次数也报不出错——本模块最关键的那条判据（**只许重拨一次**）
 *    在测试里没法靠真导航取证。留这一个可替换的出口，护栏与决策仍走生产同一条代码路径，
 *    被换掉的只有「导航」这个测试环境里本来就不存在的动作。
 */
let reloader: () => void = () => { window.location.reload() }

/** 仅供测试：换上／还原整页重载动作（传 null 即恢复生产实现）。 */
export function __setChunkReloaderForTest(fn: (() => void) | null): void {
  reloader = fn ?? (() => { window.location.reload() })
}

/**
 * installChunkRecovery 挂 vite:preloadError 监听（在 main.tsx 挂载应用之前调用一次）。
 * 返回是否真的挂上了（第二次调用回 false，供测试与排障读数）。
 *
 * 为什么必须挂在**这里**而不是只靠 ErrorBoundary：CSS 预载失败那一族根本不会走到 React
 * 渲染（懒样式是 link 标签的 error 事件），只有这个事件能看见；两者互补，不是重复。
 */
export function installChunkRecovery(): boolean {
  if (installed || typeof window === 'undefined' || !window.addEventListener) return false
  installed = true
  window.addEventListener('vite:preloadError', ((ev: Event & { payload?: unknown }) => {
    const err = ev.payload ?? ev
    if (!isChunkLoadError(err)) return // 不相干的失败别动整页（口径 ③）
    // 口径 ②：不调 ev.preventDefault()，让 Vite 把异常照旧抛给懒加载调用方
    if (reloadForChunkFailure()) {
      console.warn('[chunkRecovery] 页面组件没取到（多为网站刚发过版），已自动重新加载一次')
    }
  }) as EventListener)
  return true
}

/** 仅供测试：复位「只装一次」标记（模块级监听器在同一 jsdom 里会互相遮挡）。 */
export function __resetChunkRecoveryForTest(): void {
  installed = false
}
