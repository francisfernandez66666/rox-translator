// @vitest-environment jsdom
// ============================================================================
// components/AiRegisterFlow.dom.test.tsx — AI 注册引导 · 用户名字段回归测试（jsdom）
// 背景（2026-09-18 用户裁定）：登录页填过的用户名带入注册流程后，原实现把输入框
// readOnly+disabled 锁死 —— 带入=省一次输入，不是不给填。本文件固化该裁定：
//   ① 带入场景：输入框可编辑、预填值保留、提示「已带入，可修改」；
//   ② 未带入场景：输入框可编辑、可正常输入、不出现「已带入」提示。
// ============================================================================

import { cleanup, fireEvent, render, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import AiRegisterFlow from './AiRegisterFlow'
import { sendEmailCode } from '@/api'
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
