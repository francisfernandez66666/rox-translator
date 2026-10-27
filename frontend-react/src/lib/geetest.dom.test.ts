// ============================================================================
// lib/geetest.dom.test.ts — 极验 v4（GeeTest）前端接线的协议级判据（2026-10-27 多提供方批）
//
// 这一层的价值：极验的线上密钥我们**现在没有**（启用前要先用极验测试密钥真机验一次），
//   所以这里问的不是"能不能连上极验"，而是三件在我们这一侧就能钉死的事：
//   ① **调用契约**：`initGeetest4(options, cb)` —— options 是对象、captchaId 在里面、
//      `product:'popup'`、实例用 `appendTo(el)`。写成位置参数（`initGeetest4(captchaId, …)`）
//      在真机上永远起不来，而界面上只会看到"验证框不出现"。
//   ② **令牌形状**：四件套 lot_number/captcha_output/pass_token/gen_time 原样序列化；
//      缺任一个字段必须回空串（服务端那份签名与之逐字对应，见 captchaProvider.crossSide.lock.test.ts）。
//   ③ **一枚一换**：服务端消费掉 token 后再要验证时，顺序必须是 reset → showCaptcha
//      （先复位作废旧码、再出挑战），与 Turnstile 的 reset→execute 同源（F-03 批G）。
// 假 SDK **只认协议**：第一个参数不是带 captchaId 的对象就直接抛错——
//   否则"按我以为的契约实现桩"会给缺陷发证件（AGENTS §三 那条真踩过的形态）。
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { renderGeetest, geetestTokenJson, loadGeetest, retryGeetest, geetestLoadState, type GeetestInstance } from './geetest'

// FakeLog 记下假 SDK 收到的每一次调用：initArgs（形状断言用）、calls（顺序断言用）、fire（真触发成功态）
interface FakeLog {
  initArgs: Array<{ type: string; captchaId?: string; product?: string }>
  appended: HTMLElement[]
  calls: string[]
  fireSuccess: () => void
}

// installFakeGt 装一枚假 initGeetest4：参数形状不对就抛（"桩只认协议"的落地位置）。
function installFakeGt(validate: () => Record<string, string>): FakeLog {
  const log: FakeLog = { initArgs: [], appended: [], calls: [], fireSuccess: () => {} }
  const init = (options: unknown, cb: (gt: GeetestInstance) => void) => {
    const o = options as { captchaId?: string; product?: string }
    if (!o || typeof o !== 'object' || Array.isArray(o) || !o.captchaId) {
      // 位置参数形态（记错契约/旧写法）在这里当场被抓住：本仓接线必须走对象
      throw new Error('initGeetest4 契约不符：第一个参数必须是含 captchaId 的配置对象')
    }
    log.initArgs.push({ type: typeof options, captchaId: o.captchaId, product: o.product })
    const gt: GeetestInstance = {
      appendTo: (pos: HTMLElement | string) => {
        const el = typeof pos === 'string' ? document.querySelector<HTMLElement>(pos) : pos
        if (!el) throw new Error('appendTo 目标不存在')
        log.appended.push(el)
        log.calls.push('appendTo')
        const box = document.createElement('div')
        box.dataset.gtBox = '1'
        el.appendChild(box)
      },
      getValidate: () => validate(),
      reset: () => { log.calls.push('reset') },
      showCaptcha: () => { log.calls.push('showCaptcha') },
      onReady: (f: () => void) => { log.calls.push('onReady'); f() },
      onSuccess: (f: () => void) => { log.calls.push('onSuccess'); log.fireSuccess = f },
    }
    cb(gt)
  }
  ;(window as unknown as { initGeetest4?: unknown }).initGeetest4 = init
  return log
}

// hostEl 造一个挂载容器并塞进 body（jsdom 里 appendTo 要求元素真的在文档里）
function hostEl(): HTMLElement {
  const el = document.createElement('div')
  document.body.appendChild(el)
  return el
}

describe('极验 v4 接线（协议级）', () => {
  beforeEach(() => {
    document.body.innerHTML = ''
  })
  afterEach(() => {
    delete (window as unknown as { initGeetest4?: unknown }).initGeetest4
  })

  it('① 调用契约：init 收对象（captchaId + product:popup），实例 appendTo 到**我们的**容器', () => {
    const log = installFakeGt(() => ({ lot_number: 'a', captcha_output: 'b', pass_token: 'c', gen_time: 'd' }))
    const host = hostEl()
    const h = renderGeetest(host, 'uat-captcha-id', () => {})
    expect(h, '挂件没挂上').not.toBeNull()
    expect(log.initArgs).toEqual([{ type: 'object', captchaId: 'uat-captcha-id', product: 'popup' }])
    expect(log.appended[0], 'appendTo 必须落到我们给的容器里，否则极验会自己往别处挂').toBe(host)
    expect(host.childElementCount).toBe(1)
  })

  it('② 令牌链路：用户通过后 onToken 收到四件套 JSON（服务端那四个键逐字对应）', () => {
    const tokens: string[] = []
    const log = installFakeGt(() => ({ lot_number: 'lot-1', captcha_output: 'out-1', pass_token: 'pass-1', gen_time: '1700000000' }))
    const host = hostEl()
    renderGeetest(host, 'cid', (tk) => tokens.push(tk))
    // 先确认 renderGeetest 真的登记了成功回调，再模拟"验证通过"这一步
    expect(log.calls, '没登记 onSuccess ⇒ 通过后回调无处可去').toContain('onSuccess')
    log.fireSuccess()
    expect(tokens).toEqual([JSON.stringify({
      lot_number: 'lot-1', captcha_output: 'out-1', pass_token: 'pass-1', gen_time: '1700000000',
    })])
  })

  it('②b geetestTokenJson：齐全才出 JSON，缺任一个字段一律空串（不许送出缺字段的 JSON）', () => {
    expect(geetestTokenJson(null)).toBe('')
    expect(geetestTokenJson(false)).toBe('')
    expect(geetestTokenJson({ lot_number: 'a', captcha_output: 'b', pass_token: 'c', gen_time: 'd' }))
      .toBe(JSON.stringify({ lot_number: 'a', captcha_output: 'b', pass_token: 'c', gen_time: 'd' }))
    for (const miss of ['lot_number', 'captcha_output', 'pass_token', 'gen_time'] as const) {
      const full: Record<string, string> = { lot_number: 'a', captcha_output: 'b', pass_token: 'c', gen_time: 'd' }
      delete full[miss]
      expect(geetestTokenJson(full as never), `缺 ${miss} 时仍出 JSON ⇒ 服务端会判"验证过了却说我没验"`).toBe('')
    }
  })

  it('③ 一枚一换：revalidate 的顺序必须是 reset → showCaptcha', () => {
    const log = installFakeGt(() => ({ lot_number: 'a', captcha_output: 'b', pass_token: 'c', gen_time: 'd' }))
    const host = hostEl()
    const h = renderGeetest(host, 'cid', () => {})!
    log.calls.length = 0
    h.revalidate()
    expect(log.calls).toEqual(['reset', 'showCaptcha'])
  })

  it('④ 幂等：容器里已有挂件就不再 init（重建会清掉用户刚完成的验证）', () => {
    const log = installFakeGt(() => ({ lot_number: 'a', captcha_output: 'b', pass_token: 'c', gen_time: 'd' }))
    const host = hostEl()
    host.appendChild(document.createElement('div'))
    expect(renderGeetest(host, 'cid', () => {})).toBeNull()
    expect(log.initArgs.length).toBe(0)
  })

  it('⑤ 装载通道与 CF 那条分开：未就绪时注入的脚本只带极验地址与自己的标记，且放弃必须出声', () => {
    vi.useFakeTimers()
    try {
      delete (window as unknown as { initGeetest4?: unknown }).initGeetest4
      const off = loadGeetest(() => { /* 装载成功后由容器自己挂 */ })
      const tags = Array.from(document.head.querySelectorAll('script[data-gt-api]'))
      expect(tags.length, '没注入极验脚本').toBe(1)
      expect(tags[0].getAttribute('src')).toContain('static.geetest.com')
      // 再登记一次不会再注入第二份（全局只注入一份）
      const off2 = loadGeetest(() => {})
      expect(document.head.querySelectorAll('script[data-gt-api]').length).toBe(1)
      // 一轮窗口走完 ⇒ 状态机必须**出声**（unavailable），不许静默挂着
      vi.advanceTimersByTime(70_000)
      expect(geetestLoadState()).toBe('unavailable')
      off(); off2()
    } finally {
      vi.useRealTimers()
      // 收尾：把通道钉回可用态，避免后续用例继承 unavailable
      ;(window as unknown as { initGeetest4?: unknown }).initGeetest4 = () => {}
      retryGeetest()
      expect(geetestLoadState()).toBe('ready')
    }
  })
})
