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

// api 桩：只桩本组件用到的五个出口，注册/登录一律 success:false（本测试不走到提交成功）
vi.mock('@/api', () => ({
  authRegister: vi.fn(async () => ({ success: false })),
  login: vi.fn(async () => ({ success: false })),
  sendEmailCode: vi.fn(async () => ({ success: true })),
  setAuthToken: vi.fn(),
  setActiveTenantId: vi.fn(),
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

/** mount 以指定 prefill（模拟登录页带入的用户名）挂载组件；onDone/onClose 本测试不关心，给空实现即可 */
function mount(prefill: string) {
  return render(
    <AiRegisterFlow
      prefillUsername={prefill}
      dedicatedRegister={false}
      onDone={() => {}}
      onClose={() => {}}
    />,
  )
}

/** 走到账号信息表单步：点第一个选项（个人版），等 .ar-form 出现 */
async function reachForm(container: HTMLElement) {
  await waitFor(() => {
    const opts = container.querySelectorAll<HTMLButtonElement>('.ar-opt')
    if (opts.length === 0) throw new Error('options not ready')
    fireEvent.click(opts[0])
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
