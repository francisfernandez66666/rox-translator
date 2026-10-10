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
import { render, cleanup, fireEvent } from '@testing-library/react'
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

// ★ D-5（2026-09-29）timeout 15_000：本页 render 的是 2277 行落地页（含 ~1100 行内联 style 模板串），
//   vitest 全量并跑时单条 ① 实测在 379ms–7261ms 之间波动，默认 5000ms 会被「机器负载」而不是「回归」打红。
//   这里只抬高**时间预算**，断言本体一字未松（放宽断言＝把 flaky 换成假绿，D-5 明令禁止）。
describe('落地页 · 演示卡常驻与转化入口（#22 更正，2026-09-20）', { timeout: 15_000 }, () => {
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
    expect(text).toContain('Automotive · EN → ZH') // 演示卡英文词条（land.demoTag，英语 UI 演示 EN→ZH）
    const links = [...document.querySelectorAll('a[href="#cta"]')].filter((a) =>
      a.textContent?.includes(t('land.ctaLead')),
    )
    expect(links.length).toBeGreaterThanOrEqual(2)
  })
})

// ★ 2026-09-20 用户反馈批次回归锁：①②质量验证数字卡改口径（降本 80%–90%/质量可验证），
// ④落地页给外国访客的语言切换入口 + 非中文语种整页英文回退
describe('落地页 · 质量数字口径与多语言入口（2026-09-20）', { timeout: 15_000 }, () => {
  it('⑥ 质量数字卡改口径：含「降本 80%–90%」「质量可验证」，旧「逐段/几十万」大数字退役', () => {
    render(<Landing />)
    const text = document.body.textContent ?? ''
    expect(text).toContain('降本 80%–90%')
    expect(text).toContain('质量可验证')
    expect(text).not.toContain('显著占优（专业大模型交叉校验结论）') // 旧长句零残留
  })

  it('⑦ 顶部导航挂 LangSelect（12 语种切换入口对访客可见）', () => {
    // ★ 判据翻转（用户拍板⑦，非回归）：触发钮固定词「语言」（app.langBtn），自称名只在菜单选中项
    render(<Landing />)
    const btn = document.querySelector('header .lang-sel-btn') as HTMLElement
    expect(btn).toBeTruthy()
    expect(btn.textContent).toContain(t('app.langBtn')) // 固定词「语言」
    expect(btn.textContent).not.toContain('简体中文') // 自称名不许回占触发钮
    // title 悬停腿保留当前语种信息
    expect(btn.getAttribute('title')).toBe('简体中文')
    // 展开菜单：选中项（is-cur）携带自称名「简体中文」
    fireEvent.click(btn)
    const cur = document.querySelector('.lang-sel-menu .lang-sel-item.is-cur') as HTMLElement
    expect(cur).toBeTruthy()
    expect(cur.textContent).toContain('简体中文')
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

// ★ 2026-09-24 用户反馈批次回归锁：文件直出区重排——①第一屏 hero 卖点带迁入动效上方做居中标题容器；
// ②侧栏文字列退役（演出曾把文字/容器挤动）；③舞台限宽固定、演出零布局位移（源 chip 不再飞入吸附引擎）；
// ④顶部独立进度条与底部「上传/翻译/回写/下载」步进行删除；⑤下载按钮改纯 icon 长在译文 chip 内。
describe('落地页 · 文件直出区通栏固定舞台重排（2026-09-24）', { timeout: 15_000 }, () => {
  it('⑩ 标题容器在位：heroPoint2 主标题 + heroPoint2Note 副题居中挂在动效上方', () => {
    render(<Landing />)
    const head = document.querySelector('.lc-fd-head')
    expect(head).toBeTruthy()
    expect(head?.querySelector('.lc-fd-title')?.textContent).toContain(t('land.heroPoint2'))
    expect(head?.querySelector('.lc-fd-sub')?.textContent).toContain(t('land.heroPoint2Note'))
  })

  it('⑪ 第一屏卖点带已迁移：hero 不再渲染 .lc-hero-point2（避免同一卖点两处出现）', () => {
    render(<Landing />)
    expect(document.querySelector('.lc-hero-point2')).toBeNull()
  })

  it('⑫ 侧栏文字列与旧双栏布局退役：.lc-fd-in / .lc-fd-copy 零残留', () => {
    render(<Landing />)
    expect(document.querySelector('.lc-fd-in')).toBeNull()
    expect(document.querySelector('.lc-fd-copy')).toBeNull()
  })

  it('⑬ 固定舞台五元素在位：源/译文 chip + 引擎 + 左右两条链路（含双向数据包锚点）', () => {
    render(<Landing />)
    const stage = document.querySelector('.lc-fd-stage')
    expect(stage).toBeTruthy()
    expect(stage?.querySelector('.lc-fd-chip.lc-fd-src')).toBeTruthy()
    expect(stage?.querySelector('.lc-fd-chip.lc-fd-out')).toBeTruthy()
    expect(stage?.querySelector('.lc-fd-eng')).toBeTruthy()
    // 左右链路各挂一个数据包（pl=源→引擎，pr=引擎→译文）：吸附动效退役后改由数据包表达「流入」
    expect(document.querySelectorAll('[data-fd="pl"]').length).toBe(1)
    expect(document.querySelectorAll('[data-fd="pr"]').length).toBe(1)
  })

  it('⑭ 旧演出附件零残留：顶部进度条 / 步进行 / 飞入吸附类全部退役', () => {
    render(<Landing />)
    expect(document.querySelector('[data-fd="barfill"]')).toBeNull() // 顶部独立进度条
    expect(document.querySelector('.lc-fd-steps')).toBeNull() // 底部步骤文字行
    expect(document.querySelector('.fd-seg')).toBeNull() // 步进轨道线段
    expect(document.querySelector('.lc-fd-src.fly')).toBeNull() // 源 chip 吸附引擎的飞行终态
  })

  it('⑮ 下载改纯 icon：无文字内容、可读名走 title，且长在译文 chip 内部（绝对定位不占布局流）', () => {
    render(<Landing />)
    const out = document.querySelector('.lc-fd-chip.lc-fd-out')
    const dl = out?.querySelector('.lc-fd-dl') as HTMLButtonElement | null
    expect(dl).toBeTruthy() // 下载 icon 必须长在译文 chip 里
    expect(dl?.textContent?.trim()).toBe('') // 只留 icon，不带「下载」文字
    expect(dl?.getAttribute('title')).toBe(t('land.fdStep4'))
    expect(out?.querySelector('.lc-fd-dlzone')).toBeTruthy()
  })
})

// ★ 〇-Y #64（2026-09-28）：价格方案区下方新增「快速算价」卡（用户原话「比价和算价计算器
// 放首页一下，供用户快速计算，但细节要点击进入现在的比价和算价链接」）。
// 本段只锁三件**接线**的事，数值等值归 PriceQuickCalc.dom.test 与 e2e/homepage_price_calc.spec：
//   ① 卡确实挂在 #pricing 区内（用户要的是「价格方案旁边」，挂在别的区等于没做）；
//   ② 它与 #39「套餐卡面零具体金额」口径互不侵犯——卡上的钱来自 /api/pricing/meta 的现算试算，
//      不是套餐价目；套餐卡那三条 .lc-plan-price 仍然一个字都不许多出来；
//   ③ 系数取不到时卡片自动降级为播报态，不许留一个空壳输入区骗访客填数。
describe('落地页 · 快速算价卡挂载与 #39 口径共存（★ 〇-Y #64）', { timeout: 15_000 }, () => {
  // META 桩：Landing 测试整体替换了 '@/api'，而 usePricingMeta 走 '@/api/core' 的 request()，
  // 其出口就是全局 fetch —— 这里只喂 fetch，不额外 mock 模块，避免把 api 层的
  // 401/403 收敛逻辑一起换掉（那会让「链路坏了」在测试里看起来是好的）。
  const META_RESP = {
    ok: true, status: 200,
    json: async () => ({
      success: true, unit: 'points', points_price_money: 0.099668,
      modes: [
        { code: 'fast', points_per_1k_chars: 150, points_fixed: 3 },
        { code: 'pro', points_per_1k_chars: 400, points_fixed: 7.5 },
      ],
    }),
  }

  it('⑯ 卡在 #pricing 区内、含两个输入与一个 /compare 详情入口', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => META_RESP))
    const { waitFor } = await import('@testing-library/react')
    render(<Landing />)
    await waitFor(() => expect(document.querySelector('.lc-qc [data-qc="out"]')).toBeTruthy())
    const sec = document.querySelector('#pricing')
    const card = document.querySelector('.lc-qc')
    expect(card, '⇒ 首页没渲染快速算价卡').toBeTruthy()
    expect(sec?.contains(card ?? null), '⇒ 快速算价卡没挂在价格方案区（#pricing）内').toBe(true)
    // 快查侧只留「字数 + 语种数」两个框：档位切换/公式/系数属于细节，按用户口径归 /compare
    expect(card?.querySelectorAll('input').length).toBe(2)
    expect(card?.querySelector('a[data-qc="more"]')?.getAttribute('href')).toBe('/compare')
    vi.unstubAllGlobals()
  })

  it('⑰ 与 #39 共存：算价卡显示现算金额，套餐三档卡面依旧零具体金额', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => META_RESP))
    const { waitFor } = await import('@testing-library/react')
    render(<Landing />)
    await waitFor(() => expect(document.querySelector('.lc-qc [data-qc="out"]')).toBeTruthy())
    // 卡内确实有钱（否则上一条绿灯可能只是「卡是空的」）
    expect((document.querySelector('[data-qc="money"]')?.textContent ?? ''))
      .toMatch(/^¥[\d,.]+$/)
    // 套餐卡面一条都不许多出金额：#39 判据原样再跑一遍，
    // 这条锁的意义是「以后有人把现算金额挪进套餐卡」——那才是价目事实源漂移
    const cards = Array.from(document.querySelectorAll('.lc-plan-price'))
    expect(cards.length).toBeGreaterThanOrEqual(3)
    for (const el of cards) {
      const txt = (el.textContent ?? '').trim()
      const money = txt.match(/[¥$€]\s*\d+(?:[.,]\d+)*/g) ?? []
      expect(money.filter((m) => !/^[¥$€]\s*0$/.test(m)), `套餐卡出现具体金额：「${txt}」`).toEqual([])
    }
    vi.unstubAllGlobals()
  })

  it('⑱ 系数取不到时降级为播报态：不渲染输入框（不许留空壳骗访客填数）', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => ({ ok: true, status: 200, json: async () => ({ success: false }) })))
    const { waitFor } = await import('@testing-library/react')
    render(<Landing />)
    await waitFor(() => expect(document.querySelector('.lc-qc [data-qc="unavailable"]')).toBeTruthy())
    const card = document.querySelector('.lc-qc') as HTMLElement
    expect(card.querySelectorAll('input').length).toBe(0)
    expect(card.textContent).not.toContain('¥') // 不可用态整张卡零金额
    vi.unstubAllGlobals()
  })
})
