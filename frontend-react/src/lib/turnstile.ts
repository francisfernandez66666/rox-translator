// ============================================================================
// lib/turnstile.ts — Cloudflare Turnstile 显式渲染的共用装载器
// 背景（2026-09-24 AI 注册发码 403 事故复盘）：登录页传统表单与 AI 注册面板此前
//   各自注入 api.js?render=explicit，`__tsLoading` 守卫只管自己那份注入——脚本由
//   一方注入时，另一方的挂载回调永远等不到（表现为组件「悄悄不出现」、提交必 403）。
//   收敛到本文件：全局只注入一次，后来者按「已就绪 → 直挂；注入中 → 轮询等待」处理，
//   不依赖任何一方的 onload 接力。
// 令牌是一次一用（服务端 siteverify 消费后旧 token 立即失效），每次请求发起后
//   必须 reexecTurnstile() 重新取码，否则同一次验证会被复用到第二个接口而判失败。
// ============================================================================

/** Turnstile 全局 API 的最小子集（官方类型不随脚本发布，此处按用到的三个方法声明） */
interface TurnstileApi {
  render: (el: HTMLElement, opts: Record<string, unknown>) => string | number
  execute: (id: string | number) => void
  /** ★ F-03（2026-09-25 批G）：把组件复位回未验证态、作废当前 token；
      execute 前必须先 reset，否则组件停留在「已验证」态时 execute 可能直接重放旧 token */
  reset: (id: string | number) => void
}

declare global {
  interface Window {
    turnstile?: TurnstileApi
    __tsInjected?: boolean
  }
}

const SCRIPT_SRC = 'https://challenges.cloudflare.com/turnstile/v0/api.js?render=explicit'

// 等待上限：150ms × 80 拍 ≈ 12s。超时静默放弃——人机验证组件加载不出来时，
// 业务按钮的「先验证再提交」守卫仍然拦着，不至于放裸请求过去。
const WAIT_TICKS = 80

/** 等 Turnstile API 就绪（已就绪立即回调；否则确保脚本已注入并轮询）。返回取消函数 */
export function loadTurnstile(onReady: (ts: TurnstileApi) => void): () => void {
  if (typeof window === 'undefined') return () => {}
  if (window.turnstile) { onReady(window.turnstile); return () => {} }
  let ticks = 0
  const timer = window.setInterval(() => {
    if (window.turnstile) {
      window.clearInterval(timer)
      onReady(window.turnstile)
      return
    }
    if (++ticks >= WAIT_TICKS) window.clearInterval(timer)
  }, 150)
  if (!window.__tsInjected) {
    window.__tsInjected = true
    const s = document.createElement('script')
    s.src = SCRIPT_SRC
    s.async = true
    document.head.appendChild(s)
  }
  return () => window.clearInterval(timer)
}

/**
 * 在容器里渲染验证组件。返回 widget id（供 reexecTurnstile 用）；
 * 容器已有子节点 = 已渲染过，直接返回 null 不重建（重建会清掉用户刚完成的验证）。
 * token 过期/验证出错一律回写空串，杜绝拿废 token 提交。
 */
export function renderTurnstile(el: HTMLElement, siteKey: string, onToken: (token: string) => void): string | number | null {
  const ts = typeof window !== 'undefined' ? window.turnstile : undefined
  if (!ts || !siteKey || el.childElementCount > 0) return null
  return ts.render(el, {
    sitekey: siteKey,
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
