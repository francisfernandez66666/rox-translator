// ============================================================================
// lib/geetest.ts — GeeTest v4（极验）装载与挂载（2026-10-27 多提供方批）
//
// 为什么接它：D-02 的本体是「CF 那一跳在国内跨境链路上可能压根走不完」，
//   装载器只能把"慢"救回来，救不了"这家在我这儿不通"。极验是**正式解**（用户 2026-10-27 裁决）：
//   运营把 `captcha_provider` 存成 `geetest`，前端换脚本源、换令牌形状，四个业务调用点一字不动。
//
// 协议口径（逐条核过官方 Web API 参考页，不是按记忆拼的）：
//   · 入口 `initGeetest4(options, callback)`——**options 是配置对象**，`captchaId` 在里面，
//     `product: 'popup' | 'float' | 'bind'`；写成 `initGeetest4(captchaId, {...}, cb)` 永远起不来。
//   · 实例方法只认这套名字：`appendTo / getValidate / reset / showCaptcha / onReady /
//     onNextReady / onSuccess / onFail / onError / onClose / destroy`。
//     popup 档 `appendTo(el)` 会在宿主元素里放极验自己的"点击验证"钮，点它才弹框——
//     所以本文件**不造自己的按钮**（零新视觉，且不依赖 t() 文案）。
//   · 成功回执走 `onSuccess` + `getValidate()`，返回**四个字段**
//     lot_number / captcha_output / pass_token / gen_time；本仓把它们原样序列化成一段 JSON 当 token，
//     因此 `captcha_token` 入参形状不变，服务端签名与二次校验见
//     `backend-go/internal/api/captcha_geetest.go`。
// ★ 等待策略不在这里（lib/captchaLoad.ts 是唯一实现）：两家共用同一把尺子，
//   这里只声明极验这一家的脚本地址与"API 到手"判据。
// ============================================================================
import { createLoadChannel, type CaptchaLoadState, type LoadChannel } from './captchaLoad'

/** GeetestValidate getValidate() 的四件套（服务端只认这四个键；多余键不参与签名） */
export interface GeetestValidate {
  lot_number?: string
  captcha_output?: string
  pass_token?: string
  gen_time?: string
}

/** GeetestInstance 极验实例里本仓用到的最少方法集（官方类型不随脚本发布，按用到的形态声明） */
export interface GeetestInstance {
  appendTo: (position: HTMLElement | string) => void
  getValidate: () => GeetestValidate | false
  reset?: () => void
  showCaptcha?: () => void
  onReady: (cb: () => void) => void
  onSuccess: (cb: () => void) => void
  onFail?: (cb: (err?: unknown) => void) => void
  onError?: (cb: (err?: unknown) => void) => void
  onClose?: (cb: () => void) => void
  destroy?: () => void
}

/** InitGeetest4 极验 v4 的全局入口：第一个参数是配置对象（captchaId 必需），第二个是实例回调 */
export type InitGeetest4 = (
  options: { captchaId: string; product?: 'popup' | 'float' | 'bind'; [k: string]: unknown },
  cb: (gt: GeetestInstance) => void,
) => void

declare global {
  interface Window {
    initGeetest4?: InitGeetest4
    /** 极验脚本已注入标记（与 __tsInjected 同款纪律：全局只注入一份） */
    __gtInjected?: boolean
  }
}

/** GeetestHandle 极验挂载句柄：show＝用户主动弹框；revalidate＝作废上一枚 token 后重来一次 */
export interface GeetestHandle {
  show: () => void
  revalidate: () => void
}

const GT_SCRIPT_SRC = 'https://static.geetest.com/v4/gcaptcha4.js'

// gtChannel：极验这一家的装载通道（与 CF 那条互不串台；生产一个部署只认一家）
const gtChannel: LoadChannel<InitGeetest4> = createLoadChannel<InitGeetest4>({
  url: GT_SCRIPT_SRC,
  injectedFlag: '__gtInjected',
  dataAttr: 'gt-api',
  probe: () => (typeof window !== 'undefined' ? window.initGeetest4 : undefined),
})

/** geetestLoadState 读极验脚本装载状态（首帧初值用；禁止组件自己再判一次超时） */
export function geetestLoadState(): CaptchaLoadState {
  return gtChannel.state()
}

/** retryGeetest 用户点「重试」的极验入口：与 CF 那条同一实现（摘失败脚本 → 重注入 → 重起表） */
export function retryGeetest(): void {
  gtChannel.retry()
}

/**
 * loadGeetest 等极验 API 就绪：已就绪立即回调，否则共享一轮装载并订阅状态。
 * 返回取消函数（ready 与 state 两路一起退，同 loadTurnstile 那条纪律）。
 */
export function loadGeetest(
  onReady: (init: InitGeetest4) => void,
  onState?: (s: CaptchaLoadState) => void,
): () => void {
  return gtChannel.load(onReady, onState)
}

/**
 * geetestTokenJson 把四件套收成服务端认的那一段 JSON。
 * ★ 缺任一个字段一律回**空串**（＝这次没验过），不许拼一段缺字段的 JSON 出去——
 *   服务端会拒（那是正确行为），但用户看到的是"验证过了还说我没验"，比空 token 更难排查。
 *   `getValidate()` 在非成功态回 false，所以入参类型带 false。
 */
export function geetestTokenJson(v: GeetestValidate | false | undefined | null): string {
  if (!v) return ''
  const lot = String(v.lot_number || '').trim()
  const out = String(v.captcha_output || '').trim()
  const pass = String(v.pass_token || '').trim()
  const gen = String(v.gen_time || '').trim()
  if (!lot || !out || !pass || !gen) return ''
  return JSON.stringify({ lot_number: lot, captcha_output: out, pass_token: pass, gen_time: gen })
}

/**
 * renderGeetest 在容器里挂极验实例（popup 档）。返回句柄供"再验一次"用；
 * 容器已有子节点＝已挂过，直接回 null 不重建（重建会把用户刚完成的验证清掉）。
 * 参数 el: 宿主容器（极验在里面放它自己的触发钮）；captchaId: 极验 ID（＝后台 captcha_site_key）；
 *      onToken: 拿到四件套 JSON 的回调（缺字段回空串）。
 */
export function renderGeetest(
  el: HTMLElement,
  captchaId: string,
  onToken: (token: string) => void,
): GeetestHandle | null {
  const init = typeof window !== 'undefined' ? window.initGeetest4 : undefined
  if (!init || !captchaId || el.childElementCount > 0) return null
  let gt: GeetestInstance | null = null
  const show = () => {
    if (gt) { gt.showCaptcha?.(); return }
    // 脚本到了但 initGeetest4 的回调还没跑到（首次初始化在途）就点钮：补一次初始化，不静默吞掉
    init({ captchaId, product: 'popup' }, (inst) => {
      gt = inst
      inst.appendTo(el)
      inst.onSuccess(() => onToken(geetestTokenJson(inst.getValidate())))
      inst.onReady(() => inst.showCaptcha?.())
    })
  }
  // 行为验证要读业务页面加载时的操作数据 ⇒ 到手就初始化一次，不等用户点（官方部署文档口径）
  init({ captchaId, product: 'popup' }, (inst) => {
    gt = inst
    inst.appendTo(el)
    inst.onSuccess(() => onToken(geetestTokenJson(inst.getValidate())))
    // popup 档不自动弹框：页面一进来盖一层弹框＝把表单挡在人前面；极验自己的钮在容器里，由用户点
    inst.onReady(() => { /* 仅就绪，不自动弹框 */ })
  })
  // revalidate 是"服务端已消费掉上一枚 token、要再来一次"那一档：先 reset 复位（作废旧 token），
  // 再 showCaptcha 重新出挑战——顺序与 Turnstile 的 reset→execute 同源（F-03 批G）。
  return {
    show,
    revalidate: () => {
      if (!gt) { show(); return }
      try { gt.reset?.(); gt.showCaptcha?.() } catch { /* 实例已随面板卸载：重验失败不阻断业务流程 */ }
    },
  }
}
