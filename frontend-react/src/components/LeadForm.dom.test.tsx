// ============================================================================
// LeadForm.dom.test.tsx — 官网留资表单回归（★ P1-3，2026-09-18）
// 覆盖：字段渲染与蜜罐、必填校验（公司/邮箱）、语言胶囊进载荷、
//       成功态整块替换、后端错误文案回显、Turnstile 条件渲染（未配置零挂载）。
// 运行：npx vitest run src/components/LeadForm.dom.test.tsx
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, cleanup, fireEvent, waitFor } from '@testing-library/react'
import { setLang, t } from '@/i18n'

// vi.hoisted：mock 工厂在模块图加载期执行（早于测试文件顶层 const），
// 被引用的 fn 必须放进 hoisted 块，否则撞 TDZ 报 "cannot access before initialization"
const mocks = vi.hoisted(() => ({
  registerConfig: vi.fn(async (): Promise<unknown> => ({ success: true })),
  createLead: vi.fn(async (_p: Record<string, string>): Promise<unknown> => ({ success: true, message: 'ok' })),
}))
// '@/api' 整体替换：LeadForm 只消费 registerConfig / createLead 两个入口
vi.mock('@/api', () => ({
  registerConfig: mocks.registerConfig,
  createLead: mocks.createLead,
}))

import { LeadForm } from './LeadForm'

const form = () => document.querySelector('.lc-lead') as HTMLElement
const btn = () => document.querySelector('.lc-lead-btn') as HTMLButtonElement
const input = (label: string) =>
  [...document.querySelectorAll('.lc-lead-field')].find((l) => l.textContent?.includes(label))
    ?.querySelector('input,textarea') as HTMLInputElement

beforeEach(() => {
  cleanup()
  setLang('zh')
  mocks.registerConfig.mockReset().mockResolvedValue({ success: true })
  mocks.createLead.mockReset().mockResolvedValue({ success: true, message: 'ok' })
})

describe('留资表单 · 渲染与校验', () => {
  it('① 渲染公司/邮箱/语言/留言四组字段，蜜罐在 DOM 中但为空且禁焦', () => {
    render(<LeadForm source="landing" />)
    expect(form()).toBeTruthy()
    expect(input(t('land.leadCompany'))).toBeTruthy()
    expect(input(t('land.leadEmail'))).toBeTruthy()
    expect(document.querySelectorAll('.lc-lead-chip').length).toBeGreaterThan(0)
    const hp = document.querySelector('.lc-lead-hp input') as HTMLInputElement
    expect(hp).toBeTruthy()
    expect(hp.value).toBe('')
    expect(hp.tabIndex).toBe(-1)
  })

  it('② 公司留空提交：报必填且不发出请求', () => {
    render(<LeadForm />)
    fireEvent.click(btn())
    expect(form().querySelector('.lc-lead-err')?.textContent).toBe(t('land.leadNeedCompany'))
    expect(mocks.createLead).not.toHaveBeenCalled()
  })

  it('③ 邮箱非法（缺域名点）：报格式错误且不发请求', () => {
    render(<LeadForm />)
    fireEvent.change(input(t('land.leadCompany')), { target: { value: '某车企' } })
    fireEvent.change(input(t('land.leadEmail')), { target: { value: 'a@b' } })
    fireEvent.click(btn())
    expect(form().querySelector('.lc-lead-err')?.textContent).toBe(t('land.leadNeedEmail'))
    expect(mocks.createLead).not.toHaveBeenCalled()
  })
})

describe('留资表单 · 提交链路', () => {
  it('④ 合法提交：载荷含公司/小写邮箱/胶囊语言/来源，蜜罐恒空，成功后整块换回执', async () => {
    render(<LeadForm source="landing" />)
    fireEvent.change(input(t('land.leadCompany')), { target: { value: ' 某车企 ' } })
    fireEvent.change(input(t('land.leadEmail')), { target: { value: 'Buyer@Demo.COM' } })
    const chips = [...document.querySelectorAll('.lc-lead-chip')] as HTMLButtonElement[]
    fireEvent.click(chips[0])
    fireEvent.click(chips[1])
    expect(chips[0].getAttribute('aria-pressed')).toBe('true')
    fireEvent.click(btn())
    await waitFor(() => expect(mocks.createLead).toHaveBeenCalledTimes(1))
    const payload = mocks.createLead.mock.calls[0][0]
    expect(payload.company).toBe('某车企') // 首尾空白被裁
    expect(payload.email).toBe('buyer@demo.com') // 前端小写归一，与后端落库口径一致
    expect(payload.langs).toBe('English,日本語')
    expect(payload.source).toBe('landing')
    expect(payload.site).toBe('')
    await waitFor(() => expect(document.querySelector('.lc-lead')).toBeNull())
    expect(document.querySelector('.lc-lead-ok')?.textContent).toContain(t('land.leadSuccess'))
  })

  it('⑤ 后端拒绝（如 429 限流文案）：回执不出现，错误原文回显且表单保留可重试', async () => {
    mocks.createLead.mockRejectedValueOnce(new Error('提交过于频繁，请稍后再试'))
    render(<LeadForm />)
    fireEvent.change(input(t('land.leadCompany')), { target: { value: '某车企' } })
    fireEvent.change(input(t('land.leadEmail')), { target: { value: 'a@b.co' } })
    fireEvent.click(btn())
    await waitFor(() => expect(form().querySelector('.lc-lead-err')?.textContent).toBe('提交过于频繁，请稍后再试'))
    expect(document.querySelector('.lc-lead-ok')).toBeNull()
    expect(input(t('land.leadCompany')).value).toBe('某车企')
  })

  it('⑥ Turnstile 条件渲染：register-config 未开验证码时零挂载、无第三方 script', async () => {
    render(<LeadForm />)
    await waitFor(() => expect(mocks.registerConfig).toHaveBeenCalled())
    await new Promise((r) => setTimeout(r, 0)) // 放行微任务：确保即便有分支也已执行完
    expect(document.head.querySelector('script[src*="turnstile"]')).toBeNull()
    expect(document.querySelector('.lc-lead-captcha')).toBeNull()
  })

  it('⑦ 后台开启人机验证：出现验证码挂载点并注入 explicit script', async () => {
    mocks.registerConfig.mockResolvedValueOnce({
      success: true, captcha_enabled: true, captcha_site_key: '0xTEST',
    })
    render(<LeadForm />)
    await waitFor(() => expect(document.querySelector('.lc-lead-captcha')).toBeTruthy())
    await waitFor(() => expect(
      document.head.querySelector('script[src*="turnstile"]'),
    ).toBeTruthy())
  })
})
