// ============================================================================
// lib/captcha.ts — 人机验证「提供方」分发口（2026-10-27 多提供方批）
//
// 存在的理由：后端 `captcha_provider` 现在有两个已实现档位（turnstile / geetest），
//   而四个业务调用点（注册、发码、邮箱登录、留资）传的仍是同一个 `captcha_token` 字符串。
//   如果让每个消费方各写一遍「if (provider === 'geetest') … else …」，
//   就是 AGENTS §一·3 那一族「同一件事六处六个时刻」——提供方加一档要改三处、漏一处
//   那一处就永远在打旧家（现网表现是"某条腿静默 403"，日志一行都不给）。
//   ⇒ 分发只在这一处，消费方只说"挂在这个容器、令牌写到这个回调"。
//
// 口径三条：
//   ① **未知值一律按 turnstile**（不是"按关闭"、也不是"猜极验"）：register-config 的
//      captcha_provider 缺失＝老后端／老缓存包，而 captcha_enabled 为真说明后台确实开了验证；
//      这时按已有唯一实现（CF）走，最差退化成 D-02 那条有出路的装载失败，
//      比"挂件压根不出现、提交裸奔到后端被拒"好（同 §一·10「新增防护不拿可用性交押金」）。
//   ② 两家的**令牌形状**差异收在这里：CF 是一段裸串，极验是四件套 JSON；
//      对调用方两者都只是 `(token: string) => void`，所以提交侧零改动。
//   ③ 尺寸选档（normal/compact）只对 CF 有意义（极验 popup 档由 SDK 自己排版面），
//      因此 `size` 只往 turnstile 那条腿传；判档实现仍只有 lib/turnstile.ts 那一份。
// ============================================================================
import {
  loadTurnstile, renderTurnstile, reexecTurnstile, removeTurnstile, retryTurnstile,
  turnstileLoadState, pickTurnstileSize, type TurnstileLoadState, type TurnstileSize,
} from './turnstile'
import { loadGeetest, renderGeetest, retryGeetest, geetestLoadState } from './geetest'

/** CaptchaProvider 已实现的人机验证提供方（与后端 captcha.go captchaEnabled() 的名单逐字同值，等值锁见 captchaProvider.crossSide.lock.test.ts） */
export type CaptchaProvider = 'turnstile' | 'geetest'

/** CaptchaLoadState 装载状态（提供方无关；形状沿用 turnstile 那一套，两家共用同一状态机） */
export type CaptchaLoadState = TurnstileLoadState

/** CaptchaHandle 挂载句柄：按提供方分形态，调用方只把它当"不透明的一枚"回传给重验/销毁 */
export type CaptchaHandle =
  | { provider: 'turnstile'; id: string | number }
  | { provider: 'geetest'; show: () => void; revalidate: () => void }

/** MountOpts 挂载入参：size 只对 CF 有效；两者都可选（不传＝按容器现选档／极验无所谓） */
export interface CaptchaMountOpts {
  size?: TurnstileSize
}

/**
 * parseCaptchaProvider 把 register-config 送来的字符串收成档位。
 * 只有字面量 `geetest` 选极验，其余（含 undefined／''／`none`／将来新增的名字）一律 CF——理由见文件头口径①。
 */
export function parseCaptchaProvider(v?: string): CaptchaProvider {
  return (v || '').trim().toLowerCase() === 'geetest' ? 'geetest' : 'turnstile'
}

/** captchaLoadState 读当前提供方的装载状态（消费方首帧取初值用） */
export function captchaLoadState(provider: CaptchaProvider): CaptchaLoadState {
  return provider === 'geetest' ? geetestLoadState() : turnstileLoadState()
}

/**
 * loadCaptcha 等当前提供方的脚本/API 到手：已就绪立即回调，否则共享一轮装载并订阅状态。
 * 返回取消函数（两路一起退）。onReady 不回传 API 对象——挂载由 mountCaptcha 现读全局，
 * 消费方拿到 SDK 句柄只会各写一份渲染代码，那正是本文件要挡掉的形态。
 */
export function loadCaptcha(
  provider: CaptchaProvider,
  onReady: () => void,
  onState?: (s: CaptchaLoadState) => void,
): () => void {
  if (provider === 'geetest') return loadGeetest(() => onReady(), onState)
  return loadTurnstile(() => onReady(), onState)
}

/**
 * mountCaptcha 在容器里挂验证组件（token 一律经 onToken 回写；过期/失败回空串）。
 * 返回句柄（重验、销毁要用）；容器已有子节点或 API 没到手时回 null，调用方**不许**当成失败报错——
 * 那是幂等保护（重建会清掉用户刚完成的验证）。
 */
export function mountCaptcha(
  el: HTMLElement,
  provider: CaptchaProvider,
  key: string,
  onToken: (token: string) => void,
  opts?: CaptchaMountOpts,
): CaptchaHandle | null {
  if (provider === 'geetest') {
    const gt = renderGeetest(el, key, onToken)
    return gt ? { provider: 'geetest', show: gt.show, revalidate: gt.revalidate } : null
  }
  const id = renderTurnstile(el, key, onToken, { size: opts?.size ?? pickTurnstileSize(el) })
  return id === null ? null : { provider: 'turnstile', id }
}

/** revalidateCaptcha 作废上一枚 token 并重新出挑战（发码与注册提交要各用一枚）。 */
export function revalidateCaptcha(handle: CaptchaHandle | null): void {
  if (!handle) return
  if (handle.provider === 'geetest') { handle.revalidate(); return }
  reexecTurnstile(handle.id)
}

/** removeCaptcha 销毁组件（只在"换尺寸档"时用；极验 popup 档没有尺寸档，直接不动）。 */
export function removeCaptcha(handle: CaptchaHandle | null): void {
  if (!handle || handle.provider !== 'turnstile') return
  removeTurnstile(handle.id)
}

/** retryCaptcha 用户点「重试」的唯一入口：按提供方各摘各的失败脚本、重新起表。 */
export function retryCaptcha(provider: CaptchaProvider): void {
  if (provider === 'geetest') { retryGeetest(); return }
  retryTurnstile()
}
