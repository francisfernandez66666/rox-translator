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
import { authRegister, sendEmailCode, profileSetup, setAuthToken, type AuthUser } from '@/api'
import { toastError } from '@/lib/toastBus'

// api 桩：只桩本组件用到的出口，注册/登录一律 success:false（本测试不走到提交成功）
// ★ 2026-10-22：补 profileSetup（complete 模式个人分支走它补全；register 模式用例不碰它）
vi.mock('@/api', () => ({
  authRegister: vi.fn(async () => ({ success: false })),
  login: vi.fn(async () => ({ success: false })),
  sendEmailCode: vi.fn(async () => ({ success: true })),
  profileSetup: vi.fn(async () => ({ success: false })),
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
    // ⚠️ 本测试是"缺组织编码"的用例，不关心协议勾选（P1-4 另有专门用例）；
    // 但 agreed 守卫（2026-10-21 IAM 重做 P1-4）排在 orgCode 之前，若连协议都未勾
    // 会把"缺编码"混成"未同意"。所以两轮都先把协议勾上，让本用例只测编码这一维。
    const cb = container.querySelector<HTMLInputElement>('.ar-form input.lc-checkbox')!
    expect(cb, 'AI 表单必须渲染协议勾选（P1-4）').not.toBeNull()
    fireEvent.click(cb)
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
// ★ 2026-10-21 IAM 重做 P1-4：AI 注册流的协议勾选（旧形态硬编码 agreed:true，
//   把"我同意"替用户点了，且三处表单三套口径）。本 describe 固化同形：
//   ① 表单必须渲染协议勾选（lc-checkbox）；
//   ② 未勾选点提交 → 前端拦截、authRegister 不出网、toast 提示需要同意；
//   ③ 勾选后提交体 agreed 字段必须随勾选态传（不再恒 true）。
// ============================================================================
describe('AiRegisterFlow P1-4 · 协议勾选', () => {
  it('账号表单渲染协议勾选，且未勾选点提交被拦、不发注册请求并提示', async () => {
    vi.mocked(authRegister).mockClear()
    vi.mocked(toastError).mockClear()
    const { container } = mount('')
    await reachForm(container) // 个人版账号表单
    const cb = container.querySelector<HTMLInputElement>('.ar-form input.lc-checkbox')
    expect(cb, 'AI 表单必须渲染协议勾选').not.toBeNull()
    // 填齐必填（用户名/密码/邮箱/6 位验证码）但**不勾协议**
    const inputs = container.querySelectorAll<HTMLInputElement>('.ar-form input.lc-input')
    fireEvent.change(inputs[0], { target: { value: 'p1_4_user' } })
    const pwd = container.querySelector<HTMLInputElement>('.ar-form input[type=password]')!
    fireEvent.change(pwd, { target: { value: 'P14Pass2026!' } })
    fireEvent.change(inputs[2], { target: { value: 'p1_4@example.com' } })
    const cells = container.querySelectorAll<HTMLInputElement>('.ar-otp')
    '654321'.split('').forEach((d, i) => fireEvent.change(cells[i], { target: { value: d } }))
    const submit = Array.from(container.querySelectorAll<HTMLButtonElement>('.ar-form button'))
      .find((b) => (b.textContent || '').includes('注册并登录'))!
    fireEvent.click(submit)
    await waitFor(() => { expect(toastError).toHaveBeenCalledWith('请阅读并同意《用户协议》与《隐私协议》') })
    expect(authRegister, '未勾协议不得发出注册请求').not.toHaveBeenCalled()
    expect(cb!.checked, '未勾选时原生 checked 必须是 false（提交体才拿得到假）').toBe(false)
  })

  it('勾选协议后提交体 agreed 随勾选态传 true（不再硬编码恒真）', async () => {
    vi.mocked(authRegister).mockClear()
    const { container } = mount('')
    await reachForm(container)
    const cb = container.querySelector<HTMLInputElement>('.ar-form input.lc-checkbox')!
    fireEvent.click(cb)
    expect(cb.checked).toBe(true)
    const inputs = container.querySelectorAll<HTMLInputElement>('.ar-form input.lc-input')
    fireEvent.change(inputs[0], { target: { value: 'p1_4b_user' } })
    const pwd = container.querySelector<HTMLInputElement>('.ar-form input[type=password]')!
    fireEvent.change(pwd, { target: { value: 'P14Pass2026!' } })
    fireEvent.change(inputs[2], { target: { value: 'p1_4b@example.com' } })
    const cells = container.querySelectorAll<HTMLInputElement>('.ar-otp')
    '123456'.split('').forEach((d, i) => fireEvent.change(cells[i], { target: { value: d } }))
    const submit = Array.from(container.querySelectorAll<HTMLButtonElement>('.ar-form button'))
      .find((b) => (b.textContent || '').includes('注册并登录'))!
    fireEvent.click(submit)
    await waitFor(() => {
      expect(authRegister).toHaveBeenCalledWith(expect.objectContaining({ agreed: true }))
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

// ============================================================================
// ★ 2026-10-22 一致性核查修复（P1-3「一种错误位」）：AI 注册流的卡内红字
//   方案 §四·3 原文：「错误一律卡内红字 `.auth-err`（AI 流保留 toast 但**必须
//   同时**在卡内落一条）」。修复前本组件 8 处错误全走 toastError、卡内零落位——
//   toast 一消失用户看到的就是"点了没反应"，而传统注册卡（Login.tsx）早就有
//   .auth-err，同一件事两种形态。本 describe 固化三条约口径：
//   ① fail() 两个出口同源同文：卡内红字与 toast 文案逐字一致；
//   ② 每次提交先清上一轮红字（setFormErr('') 在所有前置校验之后、请求之前），
//      否则改完再提，旧错误会跟着新请求一起显示，指错方向；
//   ③ 形态锚点：红字紧贴提交钮上方——用户点完按钮目光就落在这里，不必回滚找提示。
// ============================================================================
describe('AiRegisterFlow P1-3 · 卡内红字错误位（.auth-err）', () => {
  /** 走到个人版账号表单并填齐除协议勾选外的全部必填项（用户名/密码/邮箱/6 位验证码） */
  async function fillExceptAgree(container: HTMLElement) {
    await reachForm(container)
    const inputs = container.querySelectorAll<HTMLInputElement>('.ar-form input.lc-input')
    fireEvent.change(inputs[0], { target: { value: 'p1_3_user' } }) // 用户名
    const pwd = container.querySelector<HTMLInputElement>('.ar-form input[type=password]')!
    fireEvent.change(pwd, { target: { value: 'P13Pass2026!' } }) // 口令
    fireEvent.change(inputs[2], { target: { value: 'p1_3@example.com' } }) // 邮箱
    const cells = container.querySelectorAll<HTMLInputElement>('.ar-otp')
    '123456'.split('').forEach((d, i) => fireEvent.change(cells[i], { target: { value: d } }))
    return container.querySelector('.ar-form') as HTMLElement
  }

  /** 表单内的提交钮（文案取 auth.registerAndLogin「注册并登录」） */
  const submitBtn = (form: HTMLElement) =>
    Array.from(form.querySelectorAll<HTMLButtonElement>('button'))
      .find((b) => (b.textContent || '').includes('注册并登录'))!

  it('未勾协议点提交：卡内 .auth-err 落位、与 toast 同源同文、注册请求不出网', async () => {
    vi.mocked(authRegister).mockClear()
    vi.mocked(toastError).mockClear()
    const { container } = mount('')
    const form = await fillExceptAgree(container)
    fireEvent.click(submitBtn(form))
    // 卡内红字落位（修复前这里查询为 null：错误只走 toast）
    const err = form.querySelector('.auth-err') as HTMLElement
    expect(err, '卡内必须有红字错误位（不能只弹 toast）').not.toBeNull()
    expect(err.textContent).toBe('请阅读并同意《用户协议》与《隐私协议》')
    // 同源同文：toast 与卡内是 fail() 的同一个 msg，逐字一致
    expect(toastError).toHaveBeenCalledWith('请阅读并同意《用户协议》与《隐私协议》')
    expect(authRegister, '未勾协议不得发出注册请求').not.toHaveBeenCalled()
  })

  it('卡内红字紧贴提交钮上方（形态锚点：点完按钮目光落点，不必回滚找提示）', async () => {
    const { container } = mount('')
    const form = await fillExceptAgree(container)
    fireEvent.click(submitBtn(form)) // 先制造一条红字
    const err = form.querySelector('.auth-err') as HTMLElement
    expect(err).not.toBeNull()
    // 红字与提交钮同在 .ar-form 内，且红字是提交钮的前一个兄弟节点
    expect(err.closest('.ar-form')).toBe(form)
    expect(err.nextElementSibling, '红字必须紧贴提交钮上方').toBe(submitBtn(form))
  })

  it('红字落位后补齐再提交：旧红字先清、请求出网（不指错方向）', async () => {
    // authRegister 挂一个永不落定的 Promise：把"提交瞬间清红字"这个同步动作
    // 定格在请求在途态，避开 success:false 分支马上又写一条新红字的竞态
    vi.mocked(authRegister).mockClear()
    vi.mocked(authRegister).mockImplementation(() => new Promise(() => {}))
    try {
      const { container } = mount('')
      const form = await fillExceptAgree(container)
      fireEvent.click(submitBtn(form)) // 第一轮：未勾协议 → 红字落位
      expect(form.querySelector('.auth-err')).not.toBeNull()
      // 第二轮：补勾协议 → 放行；提交瞬间必须先把旧红字清掉
      const cb = form.querySelector<HTMLInputElement>('input.lc-checkbox')!
      fireEvent.click(cb)
      fireEvent.click(submitBtn(form))
      await waitFor(() => {
        expect(form.querySelector('.auth-err'), '新提交必须先清旧红字').toBeNull()
      })
      expect(authRegister, '补齐后注册请求必须出网').toHaveBeenCalledTimes(1)
    } finally {
      // 还原顶层 mock 的默认实现，别把"永不落定"漏给后续用例
      vi.mocked(authRegister).mockReset()
      vi.mocked(authRegister).mockImplementation(async () => ({ success: false }))
    }
  })
})

// ============================================================================
// ★ 2026-10-22 统一登录批：complete（完善信息）模式——主站登录后由 ProfileSetupGate 挂载。
// 与 register 模式的差异全在本 describe 固化：
//   ① 话术整体换「完善信息」口径（标题/首问/选项/尾款/完成卡，共享键一个不改）；
//   ② 表单没有邮箱与验证码两步（登录时已验证过邮箱），个人分支连协议勾选也没有；
//   ③ 个人提交走 profileSetup（补既有空白账号），企业提交走 authRegister 升级通道
//      （带登录邮箱、不带 email_code）；两条路都要消费重签/新签的 token。
// ============================================================================
describe('AiRegisterFlow complete · 完善信息模式（2026-10-22 统一登录批）', () => {
  const loginEmail = 'temporary@example.com'

  /** complete 模式挂载：登录邮箱 + 当前登录用户（兜底用）由上层（ProfileSetupGate）传入 */
  function mountComplete(prefill: string, captcha?: { enabled: boolean; siteKey: string }) {
    const sessionUser = { id: 7, username: prefill, role: 'user', tenant_id: 3, email: loginEmail } as AuthUser
    return render(
      <AiRegisterFlow
        prefillUsername={prefill}
        dedicatedRegister={false}
        mode="complete"
        loginEmail={loginEmail}
        sessionUser={sessionUser}
        captcha={captcha}
        onDone={() => {}}
        onClose={() => {}}
      />,
    )
  }

  /** 在最后一个选项组里按文本点选项（complete 模式选项标题不同，按新话术找） */
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

  it('话术换「完善信息」口径：标题/首问/选项/尾款全部改词，租户注册词不出现', async () => {
    const { container } = mountComplete('temporary')
    // 标题与首问
    await waitFor(() => {
      expect(container.querySelector('.ar-nav-t')?.textContent).toBe('AI 完善助理')
    })
    expect(document.body.textContent).toContain('要完善哪类资料？')
    // 选项：个人资料 / 企业资料（不是「个人注册 / 企业注册」）
    const optTexts = [...container.querySelectorAll('.ar-opt-t')].map((el) => el.textContent)
    expect(optTexts).toContain('个人资料')
    expect(optTexts).toContain('企业资料')
    expect(document.body.textContent).not.toContain('个人注册')
    // 注册话术关键词整屏不出现（共享键未改，只是这个模式不走它们）
    expect(document.body.textContent).not.toContain('AI 注册助理')
    // 走进个人分支：尾款话术也是完善口径（「设个密码…个人资料就齐了」）
    await pickOpt(container, '个人资料')
    await pickOpt(container, '暂不选择')
    await waitFor(() => {
      expect(container.querySelector('.ar-form'), '个人分支应渲染账号表单').not.toBeNull()
    }, { timeout: 3000 })
    expect(document.body.textContent).toContain('设个密码')
    expect(document.body.textContent).toContain('保存并进入') // 提交钮 = auth.profileDone
  })

  it('个人分支表单：没有邮箱框、没有验证码六格与发码钮、没有协议勾选', async () => {
    const { container } = mountComplete('temporary')
    await pickOpt(container, '个人资料')
    await pickOpt(container, '暂不选择')
    await waitFor(() => {
      expect(container.querySelector('.ar-form')).not.toBeNull()
    }, { timeout: 3000 })
    // 只剩用户名 + 密码两枚输入（邮箱字段隐退）
    const inputs = container.querySelectorAll<HTMLInputElement>('.ar-form input.lc-input')
    expect(inputs.length).toBe(2)
    expect(container.querySelector('.ar-otp-row')).toBeNull()
    expect(container.querySelector('.ar-otp')).toBeNull()
    // 个人补全不是注册：协议勾选隐退（企业分支仍要，见下一个用例）
    expect(container.querySelector('.ar-form input.lc-checkbox')).toBeNull()
  })

  it('个人分支提交走 profileSetup（用户名/密码/职业角色），消费重签 token，完成卡展示登录邮箱', async () => {
    vi.mocked(profileSetup).mockClear()
    vi.mocked(profileSetup).mockResolvedValueOnce({
      success: true, token: 'ps-tok', user: { id: 7, username: 'real-name', role: 'user', tenant_id: 3, email: loginEmail, profile_setup_required: 0 },
    } as never)
    const { container } = mountComplete('temporary')
    await pickOpt(container, '个人资料')
    await pickOpt(container, '暂不选择')
    await waitFor(() => { expect(container.querySelector('.ar-form')).not.toBeNull() }, { timeout: 3000 })
    const inputs = container.querySelectorAll<HTMLInputElement>('.ar-form input.lc-input')
    fireEvent.change(inputs[0], { target: { value: 'real-name' } }) // 用户名
    fireEvent.change(container.querySelector<HTMLInputElement>('.ar-form input[type=password]')!, { target: { value: 'P13Pass2026!' } })
    const submit = Array.from(container.querySelectorAll<HTMLButtonElement>('.ar-form button'))
      .find((b) => (b.textContent || '').includes('保存并进入'))!
    fireEvent.click(submit)
    await waitFor(() => {
      expect(profileSetup).toHaveBeenCalledWith({ username: 'real-name', password: 'P13Pass2026!', job_role: undefined })
    }, { timeout: 3000 })
    // 重签的 token 必须消费（旧 token 的 token_version 已递增，不换就 401）
    await waitFor(() => { expect(vi.mocked(setAuthToken)).toHaveBeenCalledWith('ps-tok') })
    // 完成卡：完善完成 + 邮箱行只读展示登录邮箱（表单里根本没有邮箱框）
    await waitFor(() => {
      expect(container.querySelector('.ar-done-t')?.textContent).toBe('完善完成')
    }, { timeout: 5000 })
    const emailRow = [...container.querySelectorAll('.ar-kv')].find((kv) => (kv.textContent || '').includes('邮箱'))
    expect(emailRow?.textContent).toContain(loginEmail)
    // 企业注册语义的检查点（创建账号/发欢迎邮件）不得出现
    expect(document.body.textContent).not.toContain('创建账号')
    expect(document.body.textContent).not.toContain('发送欢迎邮件')
  })

  it('企业·管理员分支：组织字段在、协议勾选在，提交走 authRegister 升级通道（带登录邮箱、无 email_code）', async () => {
    vi.mocked(authRegister).mockClear()
    vi.mocked(authRegister).mockResolvedValueOnce({
      success: true, token: 'up-tok', user: { id: 9, username: 'uat_complete', role: 'tenant_admin', tenant_id: 11, email: loginEmail, profile_setup_required: 0 },
    } as never)
    const { container } = mountComplete('temporary')
    await pickOpt(container, '企业资料')      // 账号类型（完善口径）
    await pickOpt(container, '管理员')        // 企业身份
    await pickOpt(container, '电商')          // 行业
    await pickOpt(container, '暂不选择')      // 职业角色跳过
    await waitFor(() => {
      expect(container.querySelector('.ar-form'), '管理员分支应渲染账号表单').not.toBeNull()
    }, { timeout: 3000 })
    // 字段：用户名/密码/组织编码/组织中文名/组织英文名（没有邮箱框）
    const inputs = container.querySelectorAll<HTMLInputElement>('.ar-form input.lc-input')
    expect(inputs.length).toBe(5)
    expect(container.querySelector('.ar-otp')).toBeNull()
    // 企业分支是「真注册」：协议勾选保留
    const cb = container.querySelector<HTMLInputElement>('.ar-form input.lc-checkbox')!
    expect(cb).not.toBeNull()
    fireEvent.change(inputs[0], { target: { value: 'uat_complete' } }) // 用户名
    fireEvent.change(container.querySelector<HTMLInputElement>('.ar-form input[type=password]')!, { target: { value: 'Uat2026Pass!' } })
    fireEvent.change(inputs[2], { target: { value: 'uatorg01' } }) // 组织编码
    fireEvent.change(inputs[3], { target: { value: '优测物流' } }) // 组织中文名
    fireEvent.change(inputs[4], { target: { value: 'Utest Logistics' } }) // 组织英文名
    fireEvent.click(cb) // 勾协议
    const submit = Array.from(container.querySelectorAll<HTMLButtonElement>('.ar-form button'))
      .find((b) => (b.textContent || '').includes('保存并进入'))!
    fireEvent.click(submit)
    await waitFor(() => {
      expect(authRegister).toHaveBeenCalledWith(expect.objectContaining({
        type: 'enterprise', role_choice: 'admin', code: 'uatorg01',
        email: loginEmail, agreed: true,
      }))
    }, { timeout: 3000 })
    // 升级通道：不带 email_code（后端对本人空白账号豁免 register-purpose 验码）
    const body = vi.mocked(authRegister).mock.calls[0]?.[0] as Record<string, unknown>
    expect(body.email_code).toBeUndefined()
    // 换会话：新 token 必须落 core
    await waitFor(() => { expect(vi.mocked(setAuthToken)).toHaveBeenCalledWith('up-tok') })
  })
})
