// ============================================================================
// Landing.trial.dom.test.tsx — 〇-Z hero「点演示卡→就地试用」与全站 CTA 落点（★ 2026-09-28）
//
// 钉三件事：
//   1 演示卡与试用卡的**互斥**：默认只演 HeroDemo，点热区后 HeroDemo 整棵子树卸载、TrialPanel 挂载；
//     「返回演示」再换回来。为什么强调"卸载"：两张卡同时在 DOM 里，HeroDemo 的打字计时链会继续跑，
//     试用卡下面就会长出一段偷偷演出的动画（jsdom 里看不见，浏览器里就是掉帧）。
//   2 热区不许抢走卡内动作：复制按钮的点击属于演示卡自己，命中 a/button/input/select 时放行，
//     否则"我想复制这句译文"会变成"我要试用"。
//   3 全站转化落点（用户口径：已经有账号的人不该被要求再注册一次）：
//     落地页整页零 href="/register"，「免费试用」三处（导航/hero/收尾）一律开试用面板、
//     href 兜到 /login，其余注册类按钮直落 /login。
//
// 运行：npx vitest run src/components/Landing.trial.dom.test.tsx
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { fireEvent, render, screen, cleanup } from '@testing-library/react'
import { setLang, t } from '@/i18n'

// LeadForm 挂载即探测人机验证配置；TrialPanel 挂载即拉语种盘——两路网络都在这里断掉，
// 本文件只测"谁替换了谁"，不测请求（请求分支见 TrialPanel.dom.test.tsx）
const mocks = vi.hoisted(() => ({
  registerConfig: vi.fn(async (): Promise<unknown> => ({ success: true })),
  createLead: vi.fn(async (): Promise<unknown> => ({ success: true })),
  trialTranslate: vi.fn(async (): Promise<unknown> => ({ success: false })),
  trialLangs: vi.fn(async (): Promise<unknown> => ({ ok: true, langs: [{ code: 'en', name: '英语', name_en: 'English' }] })),
}))
vi.mock('@/api', () => ({ registerConfig: mocks.registerConfig, createLead: mocks.createLead }))
vi.mock('@/api/trial', () => ({ TRIAL_MAX_CHARS: 300, trialTranslate: mocks.trialTranslate, trialLangs: mocks.trialLangs }))

import Landing from './Landing'

beforeEach(() => {
  cleanup()
  setLang('zh')
  mocks.trialLangs.mockClear()
})
afterEach(() => { cleanup(); setLang('zh') })

const hot = () => document.querySelector<HTMLElement>('.lc-hero-hot')

describe('落地页 · 点演示卡切试用（〇-Z #74/#75）', () => {
  it('① 默认态：演示卡在位且被热区包着，试用卡不提前挂载', () => {
    render(<Landing />)
    expect(document.querySelector('.hd-panel')).toBeTruthy()
    expect(hot()).toBeTruthy()
    expect(document.querySelector('[data-trial="1"]')).toBeNull()
    // 提示行是"可点"的唯一可见信号（描边不加亮，避免抢演示本身）
    expect(hot()?.textContent).toContain(t('land.trial.hint'))
  })

  it('② 点热区 → HeroDemo 整棵卸载、试用卡挂载（两张卡不得同时在场）', () => {
    render(<Landing />)
    fireEvent.click(hot()!)
    expect(document.querySelector('[data-trial="1"]')).toBeTruthy()
    expect(document.querySelector('.hd-panel')).toBeNull()
    expect(document.querySelector('.lc-hero-hot')).toBeNull()
  })

  it('③ 「返回演示」→ 演示卡回位、试用卡卸载', () => {
    render(<Landing />)
    fireEvent.click(hot()!)
    fireEvent.click(screen.getByRole('button', { name: t('land.trial.back') }))
    expect(document.querySelector('.hd-panel')).toBeTruthy()
    expect(document.querySelector('[data-trial="1"]')).toBeNull()
  })

  it('④ 卡内复制按钮不该被热区抢走：点它不切试用卡', () => {
    render(<Landing />)
    const dl = document.querySelector<HTMLButtonElement>('.hd-dlbtn')
    expect(dl).toBeTruthy()
    fireEvent.click(dl!)
    expect(document.querySelector('[data-trial="1"]')).toBeNull()
    expect(document.querySelector('.hd-panel')).toBeTruthy()
  })

  it('⑤ 键盘可达：热区是 role=button 且 Enter 能切（鼠标之外必须有第二条路）', () => {
    render(<Landing />)
    const h = hot()!
    expect(h.getAttribute('role')).toBe('button')
    expect(h.getAttribute('aria-label')).toBe(t('land.trial.click'))
    fireEvent.keyDown(h, { key: 'Enter' })
    expect(document.querySelector('[data-trial="1"]')).toBeTruthy()
  })

  it('⑥ 三颗「免费试用」都开试用面板，且 href 兜到 /login（无 JS／中键新窗口仍有落点）', () => {
    render(<Landing />)
    const cta = t('land.ctaFree')
    const btns = [...document.querySelectorAll<HTMLAnchorElement>('a.lc-mkt-btn')].filter((a) => a.textContent?.includes(cta))
    expect(btns.length).toBeGreaterThanOrEqual(3) // 导航 + hero 主投 + 收尾 lg
    for (const b of btns) expect(b.getAttribute('href')).toBe('/login')
    fireEvent.click(btns[0])
    expect(document.querySelector('[data-trial="1"]')).toBeTruthy()
  })

  it('⑦ 全站转化落点：落地页零 href="/register"（注册页只在试用引导卡里出现）', () => {
    render(<Landing />)
    const text = document.body.innerHTML
    expect(text.includes('href="/register"'), '仍有一键直落注册页：' + (text.match(/href="\/register"/g) ?? []).length).toBe(false)
    // 反向对照②：/login 确实在页上（否则上一条的"零"是页面没渲染出来的假绿）
    expect(document.querySelectorAll('a[href="/login"]').length).toBeGreaterThanOrEqual(4)
  })
})
