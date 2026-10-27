// ============================================================================
// lib/captchaLoad.ts — 人机验证脚本「自适应装载」的唯一实现（2026-10-27 多提供方批）
//
// 为什么存在：D-02 那条修法（截止按链路自适应／到期时链路还在动就再等一拍／放弃必须出声）
//   是**装载等待策略**，与"哪家提供方"无关。极验接入时如果在本文件之外再写一份等待逻辑，
//   就是 AGENTS §一·3 那一族「同一件事六处六个时刻」——下一批改截止只会改一家，
//   另一家的用户继续撞 12 秒静默空壳。所以这里只有策略，提供方各自把自己的
//   脚本地址与"API 到手判据"传进来（lib/turnstile.ts／lib/geetest.ts）。
//
// 状态机：loading=还在等这一跳 → ready=API 到手 | unavailable=确定加载不出来（可重试）。
// 一个提供方一个通道（channel）：各自一份轮询与订阅表，互不串台；
//   生产一个部署只认一家（后端 captcha_provider 单值），所以两通道不会同时有人等。
// ============================================================================

/** CaptchaLoadState 装载状态（提供方无关）：unavailable 是"确定这一跳坏了"，不是"还没到" */
export type CaptchaLoadState = 'loading' | 'ready' | 'unavailable'

/** LoadChannelCfg 通道入参：脚本地址＋注入标记＋"API 到手"判据，三者共同定义一家提供方。
    T＝那家提供方的全局 API 形状（各家自己声明，本文件不认识任何具体 API）。 */
export interface LoadChannelCfg<T> {
  url: string // 完整脚本 src（含 query）
  injectedFlag: string // window 上的"已注入"标记名（全局只注入一份的判据）
  dataAttr: string // 脚本节点上的 data-* 标记名（小写，直接拼进属性名；重试时按它摘掉失败那份）
  probe: () => T | undefined // 读全局 API；返回真值即 ready
}

/** LoadChannel 通道对外形状：读状态／登记等待／用户点重试 */
export interface LoadChannel<T> {
  state(): CaptchaLoadState
  load(onReady: (api: T) => void, onState?: (s: CaptchaLoadState) => void): () => void
  retry(): void
}

// 轮询步长：150ms 一拍，够密以让「API 迟到」到「挂件出现」之间不留可感知缝
export const POLL_MS = 150
// 默认截止：与 D-02 改造前的 80 拍等值（12s），快链上不改变任何行为
export const BASE_DEADLINE_MS = 12_000
// 慢链/省流量档截止：这类链路里第三方脚本 15~25s 是常态，按默认档判"坏"＝把可救的一跳打死
export const SLOW_DEADLINE_MS = 30_000
// 「再等一拍」的绝对上限：超过它才允许判 unavailable（无限等待比红字更坏，见 §一·5 批⑤）
export const HARD_LIMIT_MS = 60_000
// 一次延长的拍长：链路在动就多给这一段时间，不动则放弃
export const EXTEND_MS = 2_000

/** loadDeadlineMs 按链路给截止：`navigator.connection` 报慢／省流量 → 30s，否则 12s。
    读不到 connection（Safari／旧内核）按默认档，不许因"读不到"就抬到上限。 */
export function loadDeadlineMs(): number {
  const c = (navigator as unknown as {
    connection?: { effectiveType?: string; saveData?: boolean }
  }).connection
  if (!c) return BASE_DEADLINE_MS
  const t = (c.effectiveType || '').toLowerCase()
  if (c.saveData || t === 'slow-2g' || t === '2g' || t === '3g') return SLOW_DEADLINE_MS
  return BASE_DEADLINE_MS
}

/** createLoadChannel 建一家提供方的装载通道：注入一次、轮询到点、放弃出声、重试重拨。
    内部状态只活在闭包里（不落全局变量），所以两家提供方各建一条互不影响。 */
export function createLoadChannel<T>(cfg: LoadChannelCfg<T>): LoadChannel<T> {
  const urlRoot = cfg.url.split('?')[0] // Resource Timing 按去掉 query 的地址前缀认这一跳
  let loadState: CaptchaLoadState = 'loading'
  let pollTimer: ReturnType<typeof setInterval> | null = null
  let startedAt = 0
  let deadlineAt = 0
  let seenScriptEntries = -1
  const readyWaiters = new Set<(api: T) => void>()
  const stateListeners = new Set<(s: CaptchaLoadState) => void>()

  /** scriptEntryCount 数「这一跳的 Resource Timing 条目数」。
      条目只在资源**结束**（成功或失败）时出现，所以它的**变化量**就是"链路还在动"的读数；
      拿它而不是 `script.complete`——后者在慢链上与"没这条目"同形。 */
  function scriptEntryCount(): number {
    try {
      return performance.getEntriesByType('resource').filter((e) => e.name.startsWith(urlRoot)).length
    } catch {
      return -1 // 读不到性能条目＝按"没动"处理（放弃路径，不会无限延长）
    }
  }

  /** publishState 状态唯一写点：值没变就不通知（同 §一·5 那条「订阅必须让快照变值」） */
  function publishState(next: CaptchaLoadState): void {
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

  /** fireReady 把 API 发给所有在等的消费方（含后来者），并把状态钉成 ready */
  function fireReady(api: T): void {
    stopPoll()
    publishState('ready')
    for (const fn of Array.from(readyWaiters)) fn(api)
  }

  /** tick 每一拍问三件事：API 到了没 → 截止到了没 → 这一跳还在不在动。
      「到了」与「不动了」是仅有的两个终态；除此之外一律继续等，不做猜测性放弃。 */
  function tick(): void {
    const api = typeof window !== 'undefined' ? cfg.probe() : undefined
    if (api) { fireReady(api); return }
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

  /** ensureScript 注入脚本，全局只注入一次；已经注入过但失败了由 retry 摘掉旧节点再来。
      ★ D-02 两条腿（缺一条就会把"慢"误判成"坏"且永不复原）：
      · `load` —— 脚本**晚于放弃时刻**到达时也要把 ready 追回来（否则用户在 12s 看到「加载不出来」，
        18s 脚本其实到了，界面却永远停在没有出路的旧形态）；
      · `error` —— 这一跳**确定被拒**（DNS/断连/插件拦截）时当场出声，不必再白等满窗口。
      两个事件都只动状态机，不各自再挂一遍挂件（挂载始终由消费方的 onReady 负责）。 */
  function ensureScript(replace: boolean): void {
    if (typeof document === 'undefined') return
    const attr = `data-${cfg.dataAttr}`
    const existing = Array.from(document.head.querySelectorAll<HTMLScriptElement>(`script[${attr}]`))
    if (replace) for (const el of existing) el.remove()
    const injected = (window as unknown as Record<string, boolean | undefined>)[cfg.injectedFlag]
    if (!replace && (injected || existing.length > 0)) return
    ;(window as unknown as Record<string, boolean>)[cfg.injectedFlag] = true
    const s = document.createElement('script')
    s.src = cfg.url
    s.async = true
    s.setAttribute(attr, '1')
    s.addEventListener('load', () => {
      const api = cfg.probe()
      if (api) fireReady(api)
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
    const api = cfg.probe()
    if (api) { publishState('ready'); for (const fn of Array.from(readyWaiters)) fn(api); return }
    if (pollTimer !== null) return
    startedAt = Date.now()
    deadlineAt = startedAt + loadDeadlineMs()
    seenScriptEntries = scriptEntryCount()
    ensureScript(false)
    publishState('loading')
    pollTimer = setInterval(tick, POLL_MS)
  }

  /** currentState 读数：全局 API 已在就是 ready，不看那份可能过期的状态变量
      （脚本由别的入口注入成功时，本通道可能还停在 loading——以实际到手为准） */
  function currentState(): CaptchaLoadState {
    return typeof window !== 'undefined' && cfg.probe() ? 'ready' : loadState
  }

  /** loadChannel 登记等待：已就绪立即回调，否则共享一轮装载；返回退订函数（ready＋state 两路一起退） */
  function loadChannel(onReady: (api: T) => void, onState?: (s: CaptchaLoadState) => void): () => void {
    if (typeof window === 'undefined') return () => {}
    const api = cfg.probe()
    if (api) { onReady(api); onState?.('ready'); return () => {} }
    readyWaiters.add(onReady)
    const stateCb = onState
    if (stateCb) stateListeners.add(stateCb)
    startPoll()
    stateCb?.(currentState())
    return () => {
      readyWaiters.delete(onReady)
      if (stateCb) stateListeners.delete(stateCb)
    }
  }

  /** retryChannel 用户点「重试」的唯一入口：摘掉失败那份脚本、重新起表（不整页刷新） */
  function retryChannel(): void {
    if (typeof window === 'undefined') return
    const api = cfg.probe()
    if (api) { fireReady(api); return }
    stopPoll()
    ;(window as unknown as Record<string, boolean>)[cfg.injectedFlag] = false
    ensureScript(true)
    startPoll()
  }

  return { state: currentState, load: loadChannel, retry: retryChannel }
}
