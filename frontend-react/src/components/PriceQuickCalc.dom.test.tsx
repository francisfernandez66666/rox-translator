// ============================================================================
// PriceQuickCalc.dom.test.tsx — 官网首页「快速算价」卡的取值与接线锁（★ 2026-09-28 〇-Y #64）
//
// 这张卡是**对外承诺的第二张脸**：访客在首页看到的钱，会和他点进 /compare 看到的钱直接对比。
// 所以本文件锁的全是「两张脸必须说同一个数」这一类关系，而不是页面有没有字：
//   ① 数字来源锁：卡面金额与积分必须由 mock 的 /api/pricing/meta 系数算出
//      （防止有人把价写回前端常量——首页那份一旦写死，超管调档后官网永远报旧价）；
//   ② **跨页等值锁**：同一组输入下，首页卡的金额必须等于 /compare 主卡的金额。
//      这条才是 #64 的真正风险点：两侧各写一遍式子时，各自 dom 测试都能绿，
//      客户看到的却是两个价（F-12「三口径打架」的前端版）。
//   ③ 细节入口锁：详情链接必须指向**现有的** /compare（用户原话「细节要点击进入现在的
//      比价和算价链接」）——挂在别的锚点上就是「快查有数、细节无处可查」。
//   ④ 不可用态零金额锁：接口没回来时卡片只许播报「取不到」，**整张卡不得出现 ¥**，
//      也不得留下输入框（给一个填了数却算不出钱的框，比不显示更糟）。
//   ⑤ 空输入不给半截报价（与 /compare 同口径）。
//   ⑥ 公开面零 token 裸值（AGENTS §一·5）：首页是全流量最大的一页，
//      内部计量常数与 token 字样一律不许出现在这一页。
// 运行：npx vitest run src/components/PriceQuickCalc.dom.test.tsx
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, cleanup, fireEvent, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { setLang, t } from '@/i18n'
import { fmtInt, fmtNum } from '../lib/format'

// META 与 PriceComparePage.dom.test 取同一份系数（产线出厂档），两个文件才能对得上账。
// 期望值一律由这里现推，不写死数字——写死的期望值会在超管调档后变成假红，
// 而「假红多了就放宽判据」正是历史上门闸钝化的方式。
const META = {
  success: true,
  unit: 'points',
  points_price_money: 0.099668,
  modes: [
    { code: 'fast', points_per_1k_chars: 150, points_fixed: 3 },
    { code: 'pro', points_per_1k_chars: 400, points_fixed: 7.5 },
  ],
}

const apiMocks = vi.hoisted(() => ({ request: vi.fn() }))
// 只接管 request，其余导出（API_BASE / authHeaders 等）走原实现：
// 整包替换会让被连带 import 的组件在取不到常量时报错，把「链路没通」误读成「组件坏了」
vi.mock('@/api/core', async (importOriginal) => ({
  ...(await importOriginal() as object),
  request: apiMocks.request,
}))

import PriceQuickCalc from './PriceQuickCalc'
import PriceComparePage from './PriceComparePage'

// 测试侧独立写一遍常量（不 import 组件里那份）：这样「有人改了 lib/priceCalc 的预置量
// 或人工单价却忘了同步对外文案」会在这里红灯，而不是和组件一起改成同一个错。
const HUMAN_MIN_PER_CHAR = 0.20
const EXAMPLE_CHARS = 80000
const EXAMPLE_LANGS = 1

/** money 与页面同一格式化口径（千分位 + 两位小数），不拿字符串硬拼格式 */
const money = (v: number) => '¥' + fmtNum(v, { min: 2, max: 2 })

/** 首页卡的专业档预估积分（与后端建单预检同一条两段式：固定项 + 千字线性项 × 语种数） */
function expectPoints(chars: number, langs: number): number {
  const pro = META.modes.find((m) => m.code === 'pro')!
  return Math.round(pro.points_fixed + (chars / 1000) * pro.points_per_1k_chars * langs)
}

/** renderCard 只挂首页卡；外层 MemoryRouter 是为了能和 /compare 页同树渲染时不报 hook 缺失 */
function renderCard() {
  const r = render(<MemoryRouter><PriceQuickCalc /></MemoryRouter>)
  return { ...r, card: () => document.querySelector('.lc-qc') as HTMLElement }
}

beforeEach(() => {
  cleanup()
  setLang('zh')
  apiMocks.request.mockReset()
  apiMocks.request.mockImplementation(async (url: string) =>
    String(url).includes('/api/pricing/meta') ? META : { success: true })
  // 品牌钩子会自行 fetch /api/tenant/branding：给一个必抛的桩，
  // 让组件走「无注入品牌 → 回落默认文案」这条确定性分支
  vi.stubGlobal('fetch', vi.fn(async () => { throw new Error('no network in dom test') }))
})

afterEach(() => {
  cleanup()
  setLang('zh')
  vi.unstubAllGlobals()
})

describe('首页快速算价卡 · 数字同源与 /compare 接线（★ 〇-Y #64）', () => {
  it('① 默认预置量的积分与金额＝按接口系数现算值（卡面价不许来自前端常量）', async () => {
    renderCard()
    await waitFor(() => expect(document.querySelector('[data-qc="out"]')).toBeTruthy())

    const expPoints = expectPoints(EXAMPLE_CHARS, EXAMPLE_LANGS)
    const expMoney = expPoints * META.points_price_money
    expect(document.querySelector('[data-qc="points"]')?.textContent).toContain(fmtInt(expPoints))
    expect(document.querySelector('[data-qc="money"]')?.textContent?.trim()).toBe(money(expMoney))

    // 人工对照：只显示**低档**那一头（0.20 元/源字符），省比也必须相对低档取
    const humanLo = EXAMPLE_CHARS * EXAMPLE_LANGS * HUMAN_MIN_PER_CHAR
    expect(document.querySelector('[data-qc="human"]')?.textContent?.trim()).toBe(money(humanLo))
    const expSave = Math.max(0, Math.round((1 - expMoney / humanLo) * 100))
    expect(document.querySelector('[data-qc="save"]')?.textContent).toContain(String(expSave))
  })

  it('② 改字数与语种数后当场重算（不重算＝卡面是个静态数字，比不显示更危险）', async () => {
    renderCard()
    await waitFor(() => expect(document.querySelector('[data-qc="out"]')).toBeTruthy())

    const inputs = [...document.querySelectorAll('.lc-qc-field input')] as HTMLInputElement[]
    expect(inputs.length).toBe(2) // 字数 + 语种数：快查只留这两个输入，档位切换属于细节
    fireEvent.change(inputs[0], { target: { value: '1000' } })
    fireEvent.change(inputs[1], { target: { value: '2' } })

    const expPoints = expectPoints(1000, 2)
    expect(document.querySelector('[data-qc="points"]')?.textContent).toContain(fmtInt(expPoints))
    expect(document.querySelector('[data-qc="money"]')?.textContent?.trim())
      .toBe(money(expPoints * META.points_price_money))

    // 上限防呆：输入超界按 CHARS_MAX/LANGS_MAX 收敛，不许把天文数字乘进报价
    fireEvent.change(inputs[0], { target: { value: '9999999999' } })
    expect(document.querySelector('[data-qc="money"]')?.textContent?.trim())
      .toBe(money(expectPoints(10_000_000, 2) * META.points_price_money))
  })

  it('③ 同一组输入下，首页卡的数＝/compare 主卡的数（跨页等值锁，#64 的核心风险）', async () => {
    // 两个消费方挂同一棵树、喂同一份 mock meta：这是「首页说的价和详情页说的价不一样」
    // 唯一能在本地被抓到的写法——两侧各自的 dom 测试都抓不到这种分歧。
    render(
      <MemoryRouter>
        <PriceQuickCalc />
        <PriceComparePage />
      </MemoryRouter>,
    )
    await waitFor(() => expect(document.querySelector('[data-qc="money"]')).toBeTruthy())
    const qcMoney = document.querySelector('[data-qc="money"]')?.textContent?.trim()
    const cmpMoney = document.querySelector('.lc-cmp-card.main .lc-cmp-big')?.textContent?.trim()
    expect(cmpMoney, '⇒ /compare 主卡没渲染，等值锁失去对照方').toBeTruthy()
    expect(qcMoney, '⇒ 首页卡与 /compare 页对同一组输入报出不同的钱').toBe(cmpMoney)

    const qcPoints = document.querySelector('[data-qc="points"]')?.textContent ?? ''
    const cmpPoints = document.querySelector('.lc-cmp-card.main .lc-cmp-meta')?.textContent ?? ''
    const n = fmtInt(expectPoints(EXAMPLE_CHARS, EXAMPLE_LANGS))
    expect(qcPoints).toContain(n)
    expect(cmpPoints).toContain(n)
  })

  it('④ 细节入口指向现有的 /compare；公式与系数明细不抄到首页', async () => {
    renderCard()
    await waitFor(() => expect(document.querySelector('[data-qc="out"]')).toBeTruthy())
    const more = document.querySelector('a[data-qc="more"]') as HTMLAnchorElement | null
    expect(more, '⇒ 首页卡没有详情入口').toBeTruthy()
    expect(more?.getAttribute('href'), '⇒ 详情入口没指向 /compare').toBe('/compare')

    // 首页这侧不许把细节抄过来（用户原话「细节要点击进入现在的比价和算价链接」）。
    // 判据取词典里那条公式原文做**反向**断言，而不是手写一个片段：
    // 有人把公式复制进首页时，两处用的是同一个键，手写片段会跟着一起改，锁就空转了。
    const formula = t('cmp.formula')
    expect(formula.length, '⇒ 公式键取空，本条负向锁会空转').toBeGreaterThan(10)
    const cardText = document.querySelector('.lc-qc')?.textContent ?? ''
    expect(cardText, '⇒ 首页卡抄了公示公式（公式只该在 /compare 出现）').not.toContain(formula)
    expect(document.querySelectorAll('.lc-cmp-coef').length, '⇒ 首页卡渲染了系数明细表').toBe(0)

    // 正例对照：同一条公式在 /compare 页确实渲染出来——没有这条，
    // 上面的负向锁可能只是「公式键在整个应用里都不显示」的假绿。
    cleanup()
    render(<MemoryRouter><PriceComparePage /></MemoryRouter>)
    await waitFor(() => expect(document.querySelector('.lc-cmp-formula')).toBeTruthy())
    expect(document.querySelector('.lc-cmp-formula')?.textContent).toContain(formula)
  })

  it('⑤ 系数取不到时只播报不可用，整张卡零金额、零输入框', async () => {
    apiMocks.request.mockImplementation(async (url: string) =>
      String(url).includes('/api/pricing/meta') ? { success: false } : { success: true })
    renderCard()
    await waitFor(() => expect(document.querySelector('[data-qc="unavailable"]')).toBeTruthy())
    const card = document.querySelector('.lc-qc') as HTMLElement
    // 播报必须就是词典里那句（而不是组件自己拼的半句话）：取不到时「说不说得清」也算对外承诺
    expect(card.textContent).toContain(t('cmp.unavailable'))
    expect(card.querySelectorAll('[data-qc="money"]').length).toBe(0)
    expect((card.textContent ?? '').includes('¥'), '⇒ 不可用态仍渲染了金额').toBe(false)
    expect(card.querySelectorAll('input').length, '⇒ 不可用态仍留输入框').toBe(0)
  })

  it('⑥ 空输入不给半截报价（提示态在、结果区整块消失）', async () => {
    renderCard()
    await waitFor(() => expect(document.querySelector('[data-qc="out"]')).toBeTruthy())
    const inputs = [...document.querySelectorAll('.lc-qc-field input')] as HTMLInputElement[]
    fireEvent.change(inputs[0], { target: { value: '' } })
    expect(document.querySelector('[data-qc="need"]')).toBeTruthy()
    expect(document.querySelectorAll('[data-qc="out"]').length).toBe(0)
    expect(document.querySelectorAll('[data-qc="money"]').length).toBe(0)
  })

  it('⑦ 首页公开面零 token 裸值：不出现 token 字样与内部计量常数', async () => {
    renderCard()
    await waitFor(() => expect(document.querySelector('[data-qc="out"]')).toBeTruthy())
    const text = document.body.textContent ?? ''
    expect(text.toLowerCase()).not.toMatch(/token/)
    // 24917＝充值尺子（分/百万 token）、33222＝它的上一版，都不该以裸值出现在客户页面上
    expect(text).not.toMatch(/24917|33222/)
  })
})
