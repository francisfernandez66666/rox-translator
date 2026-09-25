// @vitest-environment jsdom
// ============================================================================
// components/AiRegisterFlow.dom.test.tsx — AI 注册引导 · 用户名字段回归测试（jsdom）
// 背景（2026-09-18 用户裁定）：登录页填过的用户名带入注册流程后，原实现把输入框
// readOnly+disabled 锁死 —— 带入=省一次输入，不是不给填。本文件固化该裁定：
//   ① 带入场景：输入框可编辑、预填值保留、提示「已带入，可修改」；
//   ② 未带入场景：输入框可编辑、可正常输入、不出现「已带入」提示。
// ============================================================================

import { act, cleanup, fireEvent, render, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import AiRegisterFlow from './AiRegisterFlow'
import { authRegister, sendEmailCode } from '@/api'
import { toastError } from '@/lib/toastBus'

// api 桩：只桩本组件用到的五个出口，注册/登录一律 success:false（本测试不走到提交成功）
vi.mock('@/api', () => ({
  authRegister: vi.fn(async () => ({ success: false })),
  login: vi.fn(async () => ({ success: false })),
  sendEmailCode: vi.fn(async () => ({ success: true })),
  setAuthToken: vi.fn(),
  setActiveTenantId: vi.fn(),
}))

// toast 总线桩：拦截器组件里 toastError 走这条通道，不 mock 的话 jsdom 下静默丢掉、断言无处落
vi.mock('@/lib/toastBus', () => ({
  toast: vi.fn(),
  toastSuccess: vi.fn(),
  toastWarn: vi.fn(),
  toastError: vi.fn(),
}))

beforeEach(() => {
  // 桩 matchMedia → 命中 prefers-reduced-motion 分支：文案直出终态，无打字机逐字等待
  Object.defineProperty(window, 'matchMedia', {
    writable: true,
    value: (q: string) => ({
      matches: String(q).includes('reduce'),
      media: String(q),
      onchange: null,
      addEventListener: () => {},
      removeEventListener: () => {},
    }),
  })
})

afterEach(() => {
  cleanup() // vitest 非全局模式不自动清理，手清防止上一例 DOM 残留污染 body.textContent
  vi.restoreAllMocks()
  vi.useRealTimers() // ★ 批G F-03：冷却用例用假定时器，兜底还原，避免泄漏给后续用例
})

/** mount 以指定 prefill（模拟登录页带入的用户名）挂载组件；onDone/onClose 本测试不关心，给空实现即可
    captcha 缺省不传 = 服务端未开人机验证（旧行为），传了则走 ★2026-09-24 的容器/守卫分支 */
function mount(prefill: string, captcha?: { enabled: boolean; siteKey: string }) {
  return render(
    <AiRegisterFlow
      prefillUsername={prefill}
      dedicatedRegister={false}
      captcha={captcha}
      onDone={() => {}}
      onClose={() => {}}
    />,
  )
}

/** 走到账号信息表单步：点第一个选项（个人版）→ 角色问答点「暂不选择」→ 等 .ar-form 出现
    （2026-09-19 起个人分支在账号表单前多一问职业角色，选项来自角色字典 + 跳过项） */
async function reachForm(container: HTMLElement) {
  await waitFor(() => {
    const opts = container.querySelectorAll<HTMLButtonElement>('.ar-opt')
    if (opts.length === 0) throw new Error('options not ready')
    fireEvent.click(opts[0])
  }, { timeout: 3000 })
  await waitFor(() => {
    const groups = container.querySelectorAll('.ar-opts')
    if (groups.length < 2) throw new Error('persona options not ready')
    const btns = Array.from(groups[groups.length - 1].querySelectorAll<HTMLButtonElement>('button'))
    const skip = btns.find((b) => (b.textContent || '').includes('暂不选择'))
    if (!skip) throw new Error('skip option missing')
    fireEvent.click(skip)
  }, { timeout: 3000 })
  await waitFor(() => {
    expect(container.querySelector('.ar-form')).not.toBeNull()
  }, { timeout: 3000 })
  return container.querySelector('.ar-form input.lc-input') as HTMLInputElement
}

describe('AiRegisterFlow 用户名字段（带入但可编辑）', () => {
  it('登录页带入的用户名：预填保留、输入框可编辑、提示「已带入，可修改」', async () => {
    // 断言意图：带入只该省一次输入，不该收走编辑权（回归防护：旧实现把带入框 readOnly+disabled 锁死）
    const { container } = mount('roxfan01')
    const input = await reachForm(container)
    // 不再锁死：无 disabled / readOnly
    expect(input.disabled).toBe(false)
    expect(input.readOnly).toBe(false)
    // 预填值保留
    expect(input.value).toBe('roxfan01')
    // 用户可以直接改
    fireEvent.change(input, { target: { value: 'roxfan02' } })
    expect(input.value).toBe('roxfan02')
    // 提示文案为「可修改」口径
    expect(document.body.textContent).toContain('已带入，可修改')
  })

  it('登录页未填用户名：注册表单给出可输入的用户名框，不出现「已带入」提示', async () => {
    const { container } = mount('')
    const input = await reachForm(container)
    expect(input.disabled).toBe(false)
    expect(input.readOnly).toBe(false)
    expect(input.value).toBe('')
    // 给填写机会：能正常输入
    fireEvent.change(input, { target: { value: 'newuser' } })
    expect(input.value).toBe('newuser')
    expect(document.body.textContent).not.toContain('已带入，可修改')
  })
})

// ============================================================================
// ★ 2026-09-24 发码 403 三连缺陷回归（线上表现：点「获取验证码」没反应）
//   ① 人机验证开启时表单必须给出 Turnstile 挂载容器（旧实现整条面板没挂验证，
//      发码必然 403「请完成人机验证」，而用户看不到任何提示）；
//   ② 没拿到 token 就点发码 → 必须被拦截并明确提示，且绝不能把请求打出去；
//   ③ 发码请求抛异常（网络/后端）→ 必须 catch 住转成 toast，不得留 Uncaught
//      (in promise)——那正是线上「点了没反应 + 控制台红一条」的原始形态。
// ============================================================================
describe('AiRegisterFlow 发码链路（人机验证容器 + 异常提示）', () => {
  /** 走到账号表单并返回 [邮箱输入框, 发码按钮] */
  async function reachEmailRow(container: HTMLElement) {
    await reachForm(container)
    const inputs = container.querySelectorAll<HTMLInputElement>('.ar-form input.lc-input')
    const email = inputs[2] // 自上而下：用户名/密码/邮箱
    const send = container.querySelector('.ar-otp-row button.lc-btn') as HTMLButtonElement
    return { email, send }
  }

  it('captcha 开启：渲染 .ar-ts 挂载容器；无 token 点发码被拦截、不发请求且给出提示', async () => {
    vi.mocked(sendEmailCode).mockClear()
    vi.mocked(toastError).mockClear()
    const { container } = mount('', { enabled: true, siteKey: 'x-site-key' })
    const { email, send } = await reachEmailRow(container) // 先走到账号表单步（.ar-ts 挂在表单内）
    expect(container.querySelector('.ar-ts'), '人机验证开启时表单必须给出挂载容器').not.toBeNull()
    fireEvent.change(email, { target: { value: 'a@b.com' } })
    fireEvent.click(send)
    await waitFor(() => {
      expect(toastError).toHaveBeenCalledWith('请先完成人机验证')
    })
    expect(sendEmailCode, '未过人机验证不得发出验证码请求').not.toHaveBeenCalled()
  })

  it('发码请求抛异常：catch 转 toast，不留未处理的 Promise 拒绝', async () => {
    vi.mocked(toastError).mockClear()
    // 用 rejected mock 复现「request() 抛 ApiError」的旧事故路径（旧实现整段无 try/catch）
    vi.mocked(sendEmailCode).mockRejectedValueOnce(new Error('请求失败 (500)'))
    const { container } = mount('')
    const { email, send } = await reachEmailRow(container)
    const onUnhandled = () => { throw new Error('出现未处理的 Promise 拒绝') }
    process.on('unhandledRejection', onUnhandled)
    try {
      fireEvent.change(email, { target: { value: 'a@b.com' } })
      fireEvent.click(send)
      await waitFor(() => {
        expect(toastError).toHaveBeenCalledWith('请求失败 (500)')
      })
    } finally {
      process.off('unhandledRejection', onUnhandled)
    }
    vi.mocked(sendEmailCode).mockReset()
    vi.mocked(sendEmailCode).mockImplementation(async () => ({ success: true }))
  })
})

// ============================================================================
// ★ 2026-09-25 发布前 UAT · F-06 回归（企业注册·管理员分支的组织编码）
//   线上事故形态：AI 流选「企业注册→管理员」后，账号表单压根没有组织编码字段、
//   提交体也没有 code，后端 register.go 无邀请绑定时 req.Code 为空必吃 400
//   「请提供租户编码」——企业自助注册主路径 100% 死。
//   本用例固化修复：① admin 分支表单渲染组织编码输入（复用既有键 auth.orgCode）；
//   ② 缺编码点提交必须被前端拦下、请求不出网；③ 填齐后提交体携带 code。
// ============================================================================
describe('AiRegisterFlow F-06 · 企业·管理员组织编码', () => {
  /** 在最后一个选项组里按文本点选项 */
  async function pickOpt(container: HTMLElement, text: string) {
    await waitFor(() => {
      const groups = container.querySelectorAll('.ar-opts')
      if (groups.length === 0) throw new Error('options not ready')
      const btn = Array.from(groups[groups.length - 1].querySelectorAll<HTMLButtonElement>('button'))
        .find((b) => (b.textContent || '').includes(text))
      if (!btn) throw new Error(`option "${text}" not ready`)
      fireEvent.click(btn)
    }, { timeout: 3000 })
  }

  /** 走到「企业注册·管理员」分支的账号信息表单 */
  async function reachAdminForm(container: HTMLElement) {
    await pickOpt(container, '企业注册')     // 第一步：账号类型
    await pickOpt(container, '管理员')        // 第二步：企业身份
    await pickOpt(container, '电商')          // 第三步：行业（电商出海）
    await pickOpt(container, '暂不选择')      // 第四步：职业角色跳过
    await waitFor(() => {
      expect(container.querySelector('.ar-form'), '管理员分支应渲染账号表单').not.toBeNull()
    }, { timeout: 3000 })
  }

  /** 按标签文本找表单字段输入框（label 与 input 同在一个 .ar-fgroup 内） */
  function fieldByLabel(container: HTMLElement, label: string) {
    const groups = [...container.querySelectorAll('.ar-fgroup')]
    const g = groups.find((x) => (x.textContent || '').includes(label))
    return g ? g.querySelector('input') as HTMLInputElement : null
  }

  /** 填齐非组织编码的必填项（用户名/密码/邮箱/6 位邮箱验证码） */
  function fillBasics(container: HTMLElement) {
    const inputs = container.querySelectorAll<HTMLInputElement>('.ar-form input.lc-input')
    fireEvent.change(inputs[0], { target: { value: 'uat_admin' } }) // 用户名
    const pwd = container.querySelector<HTMLInputElement>('.ar-form input[type=password]')!
    fireEvent.change(pwd, { target: { value: 'Uat2026Pass!' } })
    fireEvent.change(inputs[2], { target: { value: 'uat_admin@example.com' } }) // 邮箱
    const cells = container.querySelectorAll<HTMLInputElement>('.ar-otp')
    expect(cells.length).toBe(6)
    '123456'.split('').forEach((d, i) => fireEvent.change(cells[i], { target: { value: d } }))
  }

  it('管理员分支表单渲染「组织编码」必填输入（F-06 死洞的直接复现点）', async () => {
    const { container } = mount('')
    await reachAdminForm(container)
    const code = fieldByLabel(container, '组织编码')
    expect(code, 'admin 分支必须给出组织编码输入框').not.toBeNull()
    expect(code!.required ?? false, '输入框常驻可编辑（* 号做必填语义）').toBe(false) // 不做原生 required，校验走提交守卫
  })

  it('缺组织编码点提交：前端拦截、authRegister 不出网；补填后提交体携带 code', async () => {
    vi.mocked(authRegister).mockClear()
    const { container } = mount('')
    await reachAdminForm(container)
    fillBasics(container)
    const submit = Array.from(container.querySelectorAll<HTMLButtonElement>('.ar-form button'))
      .find((b) => (b.textContent || '').includes('注册并登录'))!
    // 第一轮：组织编码留空 → 必被拦下（旧实现此时会把无 code 的报文发出去吃 400）
    fireEvent.click(submit)
    await new Promise((r) => setTimeout(r, 100))
    expect(authRegister, '缺组织编码不得发出注册请求').not.toHaveBeenCalled()
    // 第二轮：补填组织编码 → 放行且提交体带 code
    const code = fieldByLabel(container, '组织编码')!
    fireEvent.change(code, { target: { value: 'uatorg01' } })
    fireEvent.click(submit)
    await waitFor(() => {
      expect(authRegister).toHaveBeenCalledWith(expect.objectContaining({
        type: 'enterprise', role_choice: 'admin', code: 'uatorg01',
      }))
    }, { timeout: 3000 })
  })
})

// ============================================================================
// ★ 2026-09-25 发布前 UAT · 批G F-28 / F-02 / F-03 回归（本 describe 固化三处修复）
//   F-28：OTP 格 filled 态类名拼接缺空格（'ar-otp'+'ar-otp--filled' 粘连成
//         "ar-otpar-otp--filled"，两个类都不命中、完成态样式全丢）→ 等值锁精确类名；
//   F-02：已发码标记曾是 useRef——发码成功改值不触发重渲染，按钮文案永远停在
//         「发送验证码」→ 改 useState 后必须真的翻成 auth.aiOnline（「在线」）；
//   F-03：取码必须 reset→execute（旧 token 先作废再重挑战，防二次消费）；
//         发码失败/异常后按钮进 60s 冷却禁用（与传统表单 useCountdown(60) 对齐）。
// ============================================================================
describe('AiRegisterFlow 批G · F-28/F-02/F-03 回归', () => {
  /** 走到账号表单并返回 [邮箱输入框, 发码按钮]（与上面发码链路用例同法，独立持有不动旧用例） */
  async function reachEmailRow(container: HTMLElement) {
    await reachForm(container)
    const inputs = container.querySelectorAll<HTMLInputElement>('.ar-form input.lc-input')
    const email = inputs[2] // 自上而下：用户名/密码/邮箱
    const send = container.querySelector('.ar-otp-row button.lc-btn') as HTMLButtonElement
    return { email, send }
  }

  it('F-28：OTP 格填入一位后 className 精确等于 "ar-otp ar-otp--filled"（等值锁，未填格="ar-otp"）', async () => {
    const { container } = mount('')
    await reachForm(container)
    const cells = Array.from(container.querySelectorAll<HTMLInputElement>('.ar-otp'))
    expect(cells.length).toBe(6) // 初始全空：六格类名都必须精确等于 'ar-otp'
    cells.forEach((c) => expect(c.className).toBe('ar-otp'))
    fireEvent.change(cells[0], { target: { value: '7' } })
    // 等值锁（处方口径）：filled 态恰好是「ar-otp ar-otp--filled」两个词、中间一个空格；
    // 旧拼接写法在这里会得到 "ar-otpar-otp--filled"，两头类名全丢
    expect(cells[0].className).toBe('ar-otp ar-otp--filled')
    expect(cells[1].className).toBe('ar-otp')
  })

  it('F-02：发码成功后按钮文案翻成 auth.aiOnline（「在线」），且成功路径不进冷却仍可点', async () => {
    vi.mocked(sendEmailCode).mockClear()
    vi.mocked(sendEmailCode).mockResolvedValue({ success: true })
    const { container } = mount('')
    const { email, send } = await reachEmailRow(container)
    fireEvent.change(email, { target: { value: 'a@b.com' } })
    expect(send.textContent).toBe('发送验证码')
    fireEvent.click(send)
    // 旧缺陷复现点：ref 改值不触发渲染，这一句在 codeSentRef 实现下会永远等不到而超时
    await waitFor(() => { expect(send.textContent).toBe('在线') })
    expect(send.disabled, '成功路径维持现行为：不触发 60s 冷却禁用').toBe(false)
  })

  it('F-02：发码业务失败（success:false）按钮文案回滚「发送验证码」并给出 toast', async () => {
    vi.mocked(sendEmailCode).mockClear()
    vi.mocked(sendEmailCode).mockResolvedValueOnce({ success: false, message: '邮箱格式不正确' })
    vi.mocked(toastError).mockClear()
    const { container } = mount('')
    const { email, send } = await reachEmailRow(container)
    fireEvent.change(email, { target: { value: 'bad-email' } })
    fireEvent.click(send) // 乐观置位先显示「在线」
    await waitFor(() => { expect(toastError).toHaveBeenCalledWith('邮箱格式不正确') })
    // 失败回滚：文案精确回到 auth.sendCode（重渲染由 useState 保证；60s 后可再点见下方 F-03 用例）
    await waitFor(() => { expect(send.textContent).toBe('发送验证码') })
    vi.mocked(sendEmailCode).mockReset()
    vi.mocked(sendEmailCode).mockImplementation(async () => ({ success: true }))
  })

  it('F-03：提交取码路径必须先 ts.reset 再 ts.execute（各精确一次），token 随请求带出', async () => {
    // mock window.turnstile 三 spy：render 捕获回调配置，供手动喂 token 模拟验证通过
    let tsOpts: Record<string, unknown> | null = null
    const renderSpy = vi.fn((_el: HTMLElement, opts: Record<string, unknown>) => { tsOpts = opts; return 'wid-f03' })
    const executeSpy = vi.fn()
    const resetSpy = vi.fn()
    window.turnstile = { render: renderSpy, execute: executeSpy, reset: resetSpy }
    vi.mocked(sendEmailCode).mockClear()
    vi.mocked(sendEmailCode).mockResolvedValue({ success: true })
    try {
      const { container } = mount('', { enabled: true, siteKey: 'f03-site-key' })
      const { email, send } = await reachEmailRow(container)
      expect(renderSpy, 'captcha 开启时表单挂载即渲染验证组件').toHaveBeenCalledTimes(1)
      // 模拟用户完成验证：Turnstile callback 写入一次性 token
      ;(tsOpts!.callback as (tk: string) => void)('tk-f03-1')
      fireEvent.change(email, { target: { value: 'a@b.com' } })
      fireEvent.click(send)
      await waitFor(() => { expect(sendEmailCode).toHaveBeenCalledTimes(1) })
      // 等值锁：reset 与 execute 各恰好一次、按 widget id 调用，且 reset 严格先于 execute
      //（旧实现只调 execute：组件停在「已验证」态时旧 token 存在被二次消费的窗口）
      expect(resetSpy).toHaveBeenCalledTimes(1)
      expect(executeSpy).toHaveBeenCalledTimes(1)
      expect(resetSpy).toHaveBeenCalledWith('wid-f03')
      expect(executeSpy).toHaveBeenCalledWith('wid-f03')
      expect(resetSpy.mock.invocationCallOrder[0]).toBeLessThan(executeSpy.mock.invocationCallOrder[0])
      // 领到的 token 原样随发码请求带出（一请求一消费）
      expect(sendEmailCode).toHaveBeenCalledWith('a@b.com', 'tk-f03-1')
    } finally {
      delete window.turnstile // 还原全局，别把假 turnstile 漏给后续用例
    }
  })

  it('F-03：发码异常后按钮 disabled，60s 内再点不发第二次请求；冷却走完后恢复可点', async () => {
    vi.mocked(sendEmailCode).mockClear()
    vi.mocked(sendEmailCode).mockRejectedValueOnce(new Error('请求失败 (500)'))
    const { container } = mount('')
    const { email, send } = await reachEmailRow(container)
    fireEvent.change(email, { target: { value: 'a@b.com' } })
    // 假定时器推进 60s 冷却（useCountdown 用 window.setInterval，同一套全局被替换）
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'setInterval', 'clearInterval'] })
    await act(async () => { fireEvent.click(send) }) // 触发失败分支：回滚文案 + codeCd.start()
    expect(send.textContent).toBe('发送验证码')
    expect(send.disabled, '发码失败后按钮必须进入禁用态').toBe(true)
    // 60s 内连点：一次真实请求都不许再出网（disabled 拦截 + onSendCode 冷却守卫双保险）
    fireEvent.click(send)
    fireEvent.click(send)
    expect(sendEmailCode).toHaveBeenCalledTimes(1)
    await act(async () => { vi.advanceTimersByTime(60_000) }) // 恰好走完 60 拍归零
    expect(send.disabled, '冷却归零后按钮恢复可点（失败后仍可重试）').toBe(false)
    vi.mocked(sendEmailCode).mockResolvedValueOnce({ success: true })
    await act(async () => { fireEvent.click(send) })
    expect(sendEmailCode).toHaveBeenCalledTimes(2)
    expect(send.textContent).toBe('在线') // 重试成功后 F-02 的文案翻转同样生效
  })
})
