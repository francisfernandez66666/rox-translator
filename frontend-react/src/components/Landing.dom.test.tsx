// ============================================================================
// Landing.dom.test.tsx — 落地页转化入口与演示卡回归（#22 → ★ 2026-09-20 更正）
// 锁定需求：HeroDemo 三检查点演示卡【常驻】——#22 曾将其退役换成留资引导卡，属误解需求，
//   用户要的是把换词动效【复制】到产品加载态（WordSwap），首页原卡原样保留；
//   引导卡同步撤销（页面底部 #cta 留资表单全页唯一）。
//   转化入口口径不变：全页零「预约演示」，≥2 处锚到 #cta 真表单。
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

describe('落地页 · 演示卡常驻与转化入口（#22 更正，2026-09-20）', () => {
  it('① Hero 演示卡在位（hd-* 节点恢复），「预约演示」文案仍零出现，引导卡不留残骸', () => {
    render(<Landing />)
    const text = document.body.textContent ?? ''
    expect(text).not.toContain('预约演示')
    expect(text).not.toContain('预约产品演示')
    // 演示卡整组节点归位：卡壳 + data-hd 演出锚点（20+ 个）必须存在
    expect(document.querySelector('.hd-panel')).toBeTruthy()
    expect(document.querySelectorAll('[data-hd]').length).toBeGreaterThan(15)
    // ★ 更正点：Hero 右栏只此一张演示卡，#22 的留资引导卡（及其按钮）已撤销
    expect(document.querySelector('.lc-hero-lead')).toBeNull()
    expect(text).not.toContain('不确定翻译效果')
  })

  it('② 「留言获取方案」入口 ≥2 处且全部锚到页内 #cta 真表单（全页唯一一份）', () => {
    render(<Landing />)
    const label = t('land.ctaLead') // zh 态 = 「留言获取方案」
    const links = [...document.querySelectorAll('a[href="#cta"]')].filter(
      (a) => a.textContent?.includes(label),
    )
    // Hero 副 CTA + 页脚列；引导卡按钮随卡撤销后不再计数，只锚 #cta 不跳注册
    expect(links.length).toBeGreaterThanOrEqual(2)
    expect(document.querySelector('.lc-lead')).toBeTruthy() // #cta 区确有 LeadForm 表单
    expect(document.querySelectorAll('.lc-lead-btn').length).toBe(1) // 留资表单全页唯一（LeadForm 非 <form> 标签，按提交按钮计）
  })

  it('③ 演示卡实装非空壳：题面/定稿 ghost、行业标签、完成态文案齐备', () => {
    render(<Landing />)
    const card = document.querySelector('.hd-panel') as HTMLElement
    const text = card.textContent ?? ''
    // ghost 占位为静态 JSX（打字真身为空节点），测试不依赖计时链跑到哪一拍
    expect(text).toContain('竞品对标') // DEMO_SRC 题面
    expect(text).toContain('benchmark') // DEMO_FINAL 定稿译文
    expect(text).toContain(t('land.demoTag')) // 「汽车行业 · ZH → EN」
    expect(text).toContain(t('land.demoDone')) // 「翻译完成」
    expect(text).toContain(t('land.demoMeta')) // 「3 / 3 处术语已注入译文」
    expect(card.querySelectorAll('[data-hd="cn"]').length).toBe(1) // 量尺标签行由 DEMO_TERMS 生成
    expect(text).toContain('发布启动会')
  })

  it('④ 留资表单按钮已改口径：提交留言而非预约演示', () => {
    render(<Landing />)
    const btn = document.querySelector('.lc-lead-btn') as HTMLButtonElement
    expect(btn.textContent).toContain(t('land.leadSubmit')) // 「提交留言，获取方案」
    expect(btn.textContent).not.toContain('演示')
  })

  it('⑤ 英文态：Book a demo 零出现、演示卡词条为英文、入口 Get a tailored plan', () => {
    setLang('en')
    render(<Landing />)
    const text = document.body.textContent ?? ''
    expect(text.toLowerCase()).not.toContain('book a demo')
    expect(text.toLowerCase()).not.toContain('book a product demo')
    expect(text).toContain('Automotive · ZH → EN') // 演示卡英文词条（land.demoTag）
    const links = [...document.querySelectorAll('a[href="#cta"]')].filter((a) =>
      a.textContent?.includes(t('land.ctaLead')),
    )
    expect(links.length).toBeGreaterThanOrEqual(2)
  })
})

// ★ 2026-09-20 用户反馈批次回归锁：①②质量验证数字卡改口径（降本 80%–90%/质量可验证），
// ④落地页给外国访客的语言切换入口 + 非中文语种整页英文回退
describe('落地页 · 质量数字口径与多语言入口（2026-09-20）', () => {
  it('⑥ 质量数字卡改口径：含「降本 80%–90%」「质量可验证」，旧「逐段/几十万」大数字退役', () => {
    render(<Landing />)
    const text = document.body.textContent ?? ''
    expect(text).toContain('降本 80%–90%')
    expect(text).toContain('质量可验证')
    expect(text).not.toContain('显著占优（专业大模型交叉校验结论）') // 旧长句零残留
  })

  it('⑦ 顶部导航挂 LangSelect（12 语种切换入口对访客可见）', () => {
    render(<Landing />)
    const btn = document.querySelector('header .lang-sel-btn') as HTMLElement
    expect(btn).toBeTruthy()
    expect(btn.textContent).toContain('简体中文') // 当前语种自称
  })

  it('⑧ 俄语界面落地页：land.* 词条直接出俄语（★ 2026-09-20 全站十语种后不再走英文回退）', () => {
    setLang('ru')
    render(<Landing />)
    // LANDING_CSS 以 <style> 注入，其中文注释会混进 textContent——先摘掉样式节点再取可见文本
    document.querySelectorAll('style').forEach((s) => s.remove())
    const text = document.body.textContent ?? ''
    expect(text).toContain('Дешевле на 80–90%') // 质量数字卡俄语全量口径（land.qs4.n）
    expect(text).not.toContain('80–90% lower cost') // 英文回退链不应再被触发
    // 中文词条零出现（演示数据例外：术语对照卡/curl 示例按产品设计保留中文源）
    const zhOnly = ['免费试用', '留言获取方案', '价格方案', '常见问题', '质量验证', '支持哪些语言']
    for (const s of zhOnly) expect(text, `俄语界面不应出现中文文案「${s}」`).not.toContain(s)
    setLang('zh')
  })

  // ★ #39（2026-09-21 评审缺陷）：落地页价格卡禁止出现具体金额数字。
  //   真实价目唯一事实源是 /api/plans（由 /pricing 渲染）；落地页一旦抄进「¥99/月」这类
  //   数字，后台调价就会造成首页与定价页两个价格源打架，且改价要动 12 份词典 + 发版。
  //   本断言把这条口径钉死：只有引流档的「¥0 起步」允许出现货币数字。
  it('⑨ 价格卡零具体金额（价格源唯一归 /api/plans，落地页不抄数字）', () => {
    render(<Landing />)
    const cards = Array.from(document.querySelectorAll('.lc-plan-price'))
    expect(cards.length).toBeGreaterThanOrEqual(3)
    for (const el of cards) {
      const txt = (el.textContent ?? '').trim()
      // 只放行「¥0」这一种数字（引流档）；任何非零金额都算价目外泄
      const money = txt.match(/[¥$€]\s*\d+(?:[.,]\d+)*/g) ?? []
      const offending = money.filter((m) => !/^[¥$€]\s*0$/.test(m))
      expect(offending, `落地页价格卡出现具体金额，价目事实源漂移：「${txt}」`).toEqual([])
    }
  })
})
