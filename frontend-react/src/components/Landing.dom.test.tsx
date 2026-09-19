// ============================================================================
// Landing.dom.test.tsx — 落地页转化入口回归（★ #22，2026-09-19）
// 锁定需求：「预约演示」两颗按钮与 HeroDemo 演示卡整体退役，转化入口统一为
// 「留言获取方案」——全页 ≥2 处锚到 #cta 真表单，旧演示卡节点/词条零残留。
// 同时兜住 #24 前置：动画唯一来源已迁到 WordSwap，页面不得再挂双演示动效。
// 运行：npx vitest run src/components/Landing.dom.test.tsx
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, cleanup } from '@testing-library/react'
import { setLang, t } from '@/i18n'

// LeadForm 挂载即调 registerConfig（Turnstile 探测）：整体替换 '@/api'，
// 与 LeadForm.dom.test.tsx 同口径，测试不碰真网络
const mocks = vi.hoisted(() => ({
  registerConfig: vi.fn(async (): Promise<unknown> => ({ success: true })),
  createLead: vi.fn(async (): Promise<unknown> => ({ success: true })),
}))
vi.mock('@/api', () => ({
  registerConfig: mocks.registerConfig,
  createLead: mocks.createLead,
}))

import Landing from './Landing'

beforeEach(() => {
  cleanup()
  setLang('zh')
  mocks.registerConfig.mockReset().mockResolvedValue({ success: true })
})
afterEach(() => {
  cleanup()
  setLang('zh') // 别把英文态漏给后面的用例
})

describe('落地页 · 演示入口退役（#22）', () => {
  it('① 全页零「预约演示」文案、零 HeroDemo 节点（旧按钮/演示卡彻底退役）', () => {
    render(<Landing />)
    const text = document.body.textContent ?? ''
    expect(text).not.toContain('预约演示')
    expect(text).not.toContain('预约产品演示')
    // 演示卡整组类名（hd-* / data-hd）不应再出现在 DOM
    expect(document.querySelector('[data-hd]')).toBeNull()
    expect(document.querySelector('[class^="hd-"], [class*=" hd-"]')).toBeNull()
  })

  it('② 「留言获取方案」入口 ≥2 处且全部锚到页内 #cta 真表单', () => {
    render(<Landing />)
    const label = t('land.ctaLead') // zh 态 = 「留言获取方案」
    const links = [...document.querySelectorAll('a[href="#cta"]')].filter(
      (a) => a.textContent?.includes(label),
    )
    // Hero 双 CTA + 引导卡按钮 + 页脚列，至少 3 处；只锚 #cta，不再有跳注册的假演示
    expect(links.length).toBeGreaterThanOrEqual(3)
    expect(document.querySelector('.lc-lead')).toBeTruthy() // #cta 区确有 LeadForm 表单
  })

  it('③ Hero 右栏留资引导卡：标题/副题 + 三条承诺齐全（实装非空壳）', () => {
    render(<Landing />)
    const card = document.querySelector('.lc-hero-lead') as HTMLElement
    expect(card).toBeTruthy()
    expect(card.textContent).toContain(t('land.heroLeadTitle'))
    expect(card.textContent).toContain(t('land.heroLeadSub'))
    expect(card.querySelectorAll('.lh-pts li').length).toBe(3)
  })

  it('④ 留资表单按钮已改口径：提交留言而非预约演示', () => {
    render(<Landing />)
    const btn = document.querySelector('.lc-lead-btn') as HTMLButtonElement
    expect(btn.textContent).toContain(t('land.leadSubmit')) // 「提交留言，获取方案」
    expect(btn.textContent).not.toContain('演示')
  })

  it('⑤ 英文态同样零 Book a demo、入口为 Get a tailored plan', () => {
    setLang('en')
    render(<Landing />)
    const text = document.body.textContent ?? ''
    expect(text.toLowerCase()).not.toContain('book a demo')
    expect(text.toLowerCase()).not.toContain('book a product demo')
    const links = [...document.querySelectorAll('a[href="#cta"]')].filter((a) =>
      a.textContent?.includes(t('land.ctaLead')),
    )
    expect(links.length).toBeGreaterThanOrEqual(3)
  })
})
