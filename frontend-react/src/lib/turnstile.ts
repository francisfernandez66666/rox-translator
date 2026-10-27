// ============================================================================
// lib/turnstile.ts — Cloudflare Turnstile 显式渲染的共用装载器
// 背景（2026-09-24 AI 注册发码 403 事故复盘）：登录页传统表单与 AI 注册面板此前
//   各自注入 api.js?render=explicit，`__tsLoading` 守卫只管自己那份注入——脚本由
//   一方注入时，另一方的挂载回调永远等不到（表现为组件「悄悄不出现」、提交必 403）。
//   收敛到本文件：全局只注入一次，后来者按「已就绪 → 直挂；注入中 → 轮询等待」处理，
//   不依赖任何一方的 onload 接力。
// 令牌是一次一用（服务端 siteverify 消费后旧 token 立即失效），每次请求发起后
//   必须 reexecTurnstile() 重新取码，否则同一次验证会被复用到第二个接口而判失败。
// ★★ 2026-10-27 D-02 批：装载窗口不再「到点停表且静默放弃」。
//   旧形态 150ms×80 拍＝12 秒后 clearInterval 一走了之，onReady 永不触发、
//   容器永远空壳、界面上只剩一行「请先完成人机验证」的死循环红字——
//   现网复现读数（本机 /login，无凭证、不拦网络）：CF 脚本 **+13.998s** 才 200 到达、
//   `window.turnstile` **+14.987s** 才出现，而停表点在 12s ⇒ 到 +63s 容器仍 `iframe:0`。
//   ⇒ 三条新口径（与本仓 §一·5 批⑤「慢与坏必须分开报、产物要能撤」同一纪律）：
//   ① **截止按链路自适应**（默认 12s；慢链路／省流量档 30s），不再拿一个常数当"坏"判决；
//   ② **到期时链路还在动（Resource Timing 条目数又变了）就再等一拍**，硬上限 60s；
//   ③ **放弃必须出声**：状态机 `loading → ready | unavailable` 对外可订阅，
//      消费方按状态给「正在加载（只提示、不拦人）」或「加载不出来 ＋ 重试钮」，
//      禁止再让守卫只留一句没有出路的红字。
//   ★ 尺寸（normal 固有宽 300px）也收在这里：`pickTurnstileSize` 是唯一实现，
//     四处挂件（留资／登录／注册／AI 面板）一律按它选档并在容器上带宽度兜底——
//     此前只有 LeadForm 自己数了一遍，另外三处拿到的是 300px 硬宽（320 档会顶破页面）。
// ============================================================================

/** Turnstile 全局 API 的最小子集（官方类型不随脚本发布，此处按用到的方法声明） */
interface TurnstileApi {
  render: (el: HTMLElement, opts: Record<string, unknown>) => string | number
  execute: (id: string | number) => void
  /** ★ F-03（2026-09-25 批G）：把组件复位回未验证态、作废当前 token；
      execute 前必须先 reset，否则组件停留在「已验证」态时 execute 可能直接重放旧 token */
  reset: (id: string | number) => void
  /** 跨档重挂时销毁旧组件（normal↔compact 换尺寸必须 remove 后重 render，旧 token 随之作废） */
  remove?: (id: string | number) => void
}

/** 装载状态机：loading=还在等这一跳；ready=API 到手；unavailable=确定加载不出来（可重试） */
export type TurnstileLoadState = 'loading' | 'ready' | 'unavailable'

/** 挂件尺寸档：normal 固有宽 300px，compact 约 150px（窄容器／窄视口用 compact） */
export type TurnstileSize = 'normal' | 'compact'

declare global {
  interface Window {
    turnstile?: TurnstileApi
    /** 脚本已注入标记（全局只注入一份的唯一判据；不许再造第二个同名守卫） */
    __tsInjected?: boolean
  }
}

const SCRIPT_SRC = 'https://challenges.cloudflare.com/turnstile/v0/api.js?render=explicit'

// 轮询步长：150ms 一拍，够密以让「API 迟到」到「挂件出现」之间不留可感知缝
const POLL_MS = 150
// 默认截止：与改造前的 80 拍等值（12s），快链上不改变任何行为
const BASE_DEADLINE_MS = 12_000
// 慢链/省流量档截止：这类链路里 CF 那一跳 15~25s 是常态，按默认档判"坏"＝把可救的一跳打死
const SLOW_DEADLINE_MS = 30_000
// 「再等一拍」的绝对上限：超过它才允许判 unavailable（无限等待比红字更坏，见 §一·5 批⑤）
const HARD_LIMIT_MS = 60_000
// 一次延长的拍长：链路在动就多给这一段时间，不动则放弃
const EXTEND_MS = 2_000
// normal 档固有宽度判据：容器比这还窄就必须换 compact
const NORMAL_MIN_WIDTH = 300
// 视口窄于该值一律 compact（容器宽度尚未定型时的兜底）
const COMPACT_VIEWPORT_MAX = 400

// ---- 装载器的共享状态（脚本全局只有一份 ⇒ 状态也只能有一份）----
let loadState: TurnstileLoadState = 'loading'
let pollTimer: ReturnType<typeof setInterval> | null = null
let startedAt = 0
let deadlineAt = 0
let seenScriptEntries = -1
// readyWaiters：等"API 到手"的消费方集合（登录/注册/留资/AI 面板各一个回调，装载成功一起叫醒）
const readyWaiters = new Set<(ts: TurnstileApi) => void>()
// stateListeners：装载状态订阅者（loading/ready/unavailable 的变化要让界面换脸，不落库、只活在内存）
const stateListeners = new Set<(s: TurnstileLoadState) => void>()

/** loadDeadlineMs 按链路给截止：`navigator.connection` 报慢／省流量 → 30s，否则 12s。
    读不到 connection（Safari／旧内核）按默认档，不许因"读不到"就抬到上限。 */
function loadDeadlineMs(): number {
  const c = (navigator as unknown as {
    connection?: { effectiveType?: string; saveData?: boolean }
  }).connection
  if (!c) return BASE_DEADLINE_MS
  const t = (c.effectiveType || '').toLowerCase()
  if (c.saveData || t === 'slow-2g' || t === '2g' || t === '3g') return SLOW_DEADLINE_MS
  return BASE_DEADLINE_MS
}

/** scriptEntryCount 数「这一跳的 Resource Timing 条目数」。
    条目只在资源**结束**（成功或失败）时出现，所以它的**变化量**就是"链路还在动"的读数；
    拿它而不是 `script.complete`——后者在慢链上与"没这条目"同形。 */
function scriptEntryCount(): number {
  try {
    return performance
      .getEntriesByType('resource')
      .filter((e) => e.name.startsWith(SCRIPT_SRC.split('?')[0])).length
  } catch {
    return -1 // 读不到性能条目＝按"没动"处理（放弃路径，不会无限延长）
  }
}

/** publishState 状态唯一写点：值没变就不通知（同 §一·5 那条「订阅必须让快照变值」）。 */
function publishState(next: TurnstileLoadState): void {
  if (next === loadState) return
  loadState = next
  for (const fn of Array.from(stateListeners)) fn(next)
}

/** stopPoll 收摊：任何走到终态的路径都必须显式停表，不许留孤儿 interval */
function stopPoll(): void {
  if (pollTimer !== null) {
    clearInterval(pollTimer)
    pollTimer = null
  }
}

/** fireReady 把 API 发给所有在等的消费方（含后来者），并把状态钉成 ready。 */
function fireReady(ts: TurnstileApi): void {
  stopPoll()
  publishState('ready')
  for (const fn of Array.from(readyWaiters)) fn(ts)
}

/** tick 每一拍问三件事：API 到了没 → 截止到了没 → 这一跳还在不在动。
    「到了」与「不动了」是仅有的两个终态；除此之外一律继续等，不做猜测性放弃。 */
function tick(): void {
  const ts = typeof window !== 'undefined' ? window.turnstile : undefined
  if (ts) { fireReady(ts); return }
  const now = Date.now()
  if (now < deadlineAt) return
  const entries = scriptEntryCount()
  if (entries !== seenScriptEntries && now < HARD_LIMIT_MS + startedAt) {
    seenScriptEntries = entries
    deadlineAt = now + EXTEND_MS
    return
  }
  // 确定加载不出来：出声（消费方据此显示「重试」），并停表——退避交给用户点，不自动狂重拨
  stopPoll()
  publishState('unavailable')
}

/** ensureScript 注入脚本，全局只注入一次；已经注入过但失败了由 retryTurnstile 摘掉旧节点再来。
    ★ D-02 两条腿（缺一条就会把"慢"误判成"坏"且永不复原）：
    · `load` —— 脚本**晚于放弃时刻**到达时也要把 ready 追回来（否则用户在 12s 看到「加载不出来」，
      18s 脚本其实到了，界面却永远停在没有出路的旧形态）；
    · `error` —— 这一跳**确定被拒**（DNS/断连/插件拦截）时当场出声，不必再白等满窗口。
    两个事件都只动状态机，不各自再挂一遍挂件（挂载始终由 loadTurnstile 的 onReady 消费者负责）。 */
function ensureScript(replace: boolean): void {
  if (typeof document === 'undefined') return
  const existing = Array.from(document.head.querySelectorAll<HTMLScriptElement>('script[data-ts-api]'))
  if (replace) for (const el of existing) el.remove()
  if (!replace && (window.__tsInjected || existing.length > 0)) return
  window.__tsInjected = true
  const s = document.createElement('script')
  s.src = SCRIPT_SRC
  s.async = true
  s.dataset.tsApi = '1'
  s.addEventListener('load', () => {
    const ts = window.turnstile
    if (ts) fireReady(ts)
  })
  s.addEventListener('error', () => {
    stopPoll()
    publishState('unavailable')
  })
  document.head.appendChild(s)
}

/** startPoll 起表：一次装载尝试的唯一入口（幂等——已在跑就复用，不并存两个递减源） */
function startPoll(): void {
  if (typeof window === 'undefined') return
  const ts = window.turnstile
  if (ts) { publishState('ready'); for (const fn of Array.from(readyWaiters)) fn(ts); return }
  if (pollTimer !== null) return
  startedAt = Date.now()
  deadlineAt = startedAt + loadDeadlineMs()
  seenScriptEntries = scriptEntryCount()
  ensureScript(false)
  publishState('loading')
  pollTimer = setInterval(tick, POLL_MS)
}

/** turnstileLoadState 读当前装载状态（消费方首帧取初值用，禁止各组件自己再判一次超时） */
export function turnstileLoadState(): TurnstileLoadState {
  return typeof window !== 'undefined' && window.turnstile ? 'ready' : loadState
}

/** retryTurnstile 用户点「重试」的唯一入口：摘掉失败那份脚本、重新起表（不整页刷新）。
    这是 D-02 的出路那一半——旧形态没有出路，红字永远指着一个永远不会出现的框。 */
export function retryTurnstile(): void {
  if (typeof window === 'undefined') return
  if (window.turnstile) { fireReady(window.turnstile); return }
  stopPoll()
  window.__tsInjected = false
  ensureScript(true)
  startPoll()
}

/**
 * loadTurnstile 等 Turnstile API 就绪：已就绪立即回调；否则登记等待并共享一轮装载。
 * onState 是**状态订阅**（loading/unavailable 要让界面上有对应的脸，不许静默）。
 * 返回取消函数：卸载时必须同时退订 ready 与 state 两路（只退一路＝组件已走、回调还在写它）。
 */
export function loadTurnstile(
  onReady: (ts: TurnstileApi) => void,
  onState?: (s: TurnstileLoadState) => void,
): () => void {
  if (typeof window === 'undefined') return () => {}
  const ts = window.turnstile
  if (ts) { onReady(ts); onState?.('ready'); return () => {} }
  readyWaiters.add(onReady)
  const stateCb = onState
  if (stateCb) stateListeners.add(stateCb)
  startPoll()
  stateCb?.(turnstileLoadState())
  return () => {
    readyWaiters.delete(onReady)
    if (stateCb) stateListeners.delete(stateCb)
  }
}

/** pickTurnstileSize 尺寸选档的**唯一实现**：容器放不下 normal 或视口过窄 → compact。
    判据按容器（不是按视口），理由同 AGENTS §一·5「宽度不由视口决定的自适应件不许用 vw」那一族。 */
export function pickTurnstileSize(el?: HTMLElement | null): TurnstileSize {
  const w = el?.clientWidth ?? 0
  const vw = typeof window !== 'undefined' ? window.innerWidth : 0
  return (w > 0 && w < NORMAL_MIN_WIDTH) || vw < COMPACT_VIEWPORT_MAX ? 'compact' : 'normal'
}

/**
 * 在容器里渲染验证组件。返回 widget id（供 reexecTurnstile 用）；
 * 容器已有子节点 = 已渲染过，直接返回 null 不重建（重建会清掉用户刚完成的验证）。
 * token 过期/验证出错一律回写空串，杜绝拿废 token 提交。
 * ★ D-02 批补 `size`：不传就按容器现选档——旧形态从不传 size，四处挂件一律拿 300px 硬宽。
 */
export function renderTurnstile(
  el: HTMLElement,
  siteKey: string,
  onToken: (token: string) => void,
  opts?: { size?: TurnstileSize },
): string | number | null {
  const ts = typeof window !== 'undefined' ? window.turnstile : undefined
  if (!ts || !siteKey || el.childElementCount > 0) return null
  const size = opts?.size ?? pickTurnstileSize(el)
  return ts.render(el, {
    sitekey: siteKey,
    size,
    callback: onToken,
    'expired-callback': () => onToken(''),
    'error-callback': () => onToken(''),
  })
}

/**
 * 消费掉当前 token 后强制重新挑战、领新 token（发码与注册提交要各用一枚）。
 * ★ F-03（2026-09-25 批G）：顺序改为 reset → execute——旧实现只调 execute，
 * 组件停留在「已验证」态时 execute 不保证重新出挑战，存在旧 token 被二次消费的窗口；
 * 先 reset 显式作废当前 token 并复位组件，再 execute 强制领一枚全新的。
 */
export function reexecTurnstile(id: string | number | null): void {
  const ts = typeof window !== 'undefined' ? window.turnstile : undefined
  if (!ts || id === null) return
  try { ts.reset(id); ts.execute(id) } catch { /* 组件已随面板卸载：重执行失败不阻断业务流程 */ }
}

/** removeTurnstile 销毁组件（跨档重挂用）：remove 后容器清空，token 一并作废。
    与 reexec 的区别是"换尺寸"而非"换令牌"，因此**不许**在这里顺带 execute。 */
export function removeTurnstile(id: string | number | null): void {
  const ts = typeof window !== 'undefined' ? window.turnstile : undefined
  if (!ts || id === null) return
  try { ts.remove?.(id) } catch { /* 已被卸载：销毁失败不阻断重挂 */ }
}
