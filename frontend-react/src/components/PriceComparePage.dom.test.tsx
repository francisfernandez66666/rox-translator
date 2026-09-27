// ============================================================================
// PriceComparePage.dom.test.tsx — 公开「比价与算价」页 /compare 的取值与红线锁（★ 〇-X #55）
//
// 这个页面是**对外承诺**：访客会拿它算出来的数字做预算，所以本文件锁的四条全是「页面说的话
// 必须等于后端做的事」：
//   ① 数字来源锁：卡面金额必须由 /api/pricing/meta 返回的系数算出（mock 一份已知系数，
//      断言结果等于同一式子的手工预期值）——防止有人把价写回前端常量；
//   ② 公示公式 == 扣费公式：cmp 页算的是 fixed + chars/1000 × per1k × langs 再取整，
//      与后端 estimateTicketTokens→PointsFromTokens 同一条式子（后端侧由
//      pricing_meta_test.go 的等式锁钉，本文件钉前端这一侧，两侧合起来才是闭环）；
//   ③ 零 token 裸值（AGENTS §一·5 的前端延伸）：整页文本不得出现 token、内部配置键名、
//      汇率/尺子字面值——公开页只讲积分与钱；
//   ④ 人工对照价的口径锁：区间上下限必须由组件常量（0.20 / 0.30 元/源字符）算出，
//      且来源与采集日期必须留在页面上（用户明确要求「标注来源与时间」）。
// 外加一条不可用态负向锁：接口没给数时页面必须明说取不到，**不得**渲染任何金额卡
//   （F-12「三口径打架」的公开页版本就是靠前端兜底值养出来的）。
// 运行：npx vitest run src/components/PriceComparePage.dom.test.tsx
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, cleanup, fireEvent, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { readFileSync } from 'node:fs'
import { setLang, t, tpl } from '@/i18n'
import { fmtInt, fmtNum } from '../lib/format'

// META 是本测试唯一的事实源：mock 给接口的返回值与断言预期都由它推，
// 数值刻意取产线出厂档（pro 每千字 400 积分 / 固定 7.5、fast 150 / 3、每积分 0.099668 元），
// 这样「测试期望」同时也是「F-78 之后官网应当显示的价」的一份读数。
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
// 部分替换 '@/api/core'：只接管 request，其余导出（API_BASE / getAuthToken 等）走原实现，
// 否则被连带 import 的组件会在取不到常量时报错，把「链路没通」误读成「组件坏了」
vi.mock('@/api/core', async (importOriginal) => ({
  ...(await importOriginal() as object),
  request: apiMocks.request,
}))

import PriceComparePage, { calcEstimatePoints } from './PriceComparePage'

// 组件侧常量（与 PriceComparePage.tsx 内部一致）：0.20 / 0.30 元/源字符、8 万字 × 1 语示例。
// 这里重新写字面值是有意的——测试要能抓住「有人改了组件常量却忘了改出处文案」，
// 若从组件 import 就变成自证。
const HUMAN_LO = 0.20
const HUMAN_HI = 0.30
const EXAMPLE_CHARS = 80000
const EXAMPLE_LANGS = 1

/** renderPage 挂进 MemoryRouter（页面用 useNavigate），并把 body 文本一次性取出来 */
function renderPage() {
  const r = render(<MemoryRouter><PriceComparePage /></MemoryRouter>)
  const text = () => document.body.textContent ?? ''
  return { ...r, text }
}

/** money 与页面同一格式化口径（千分位 + 2 位小数），避免拿字符串硬拼格式 */
const money = (v: number) => '¥' + fmtNum(v, { min: 2, max: 2 })

beforeEach(() => {
  cleanup()
  setLang('zh')
  apiMocks.request.mockReset()
  apiMocks.request.mockImplementation(async (url: string) =>
    String(url).includes('/api/pricing/meta') ? META : { success: true })
  // 品牌钩子会自行 fetch /api/tenant/branding：给一个必定失败的桩，
  // 让页面走「无注入品牌 → 回落 land.brand」这条确定性分支
  vi.stubGlobal('fetch', vi.fn(async () => { throw new Error('no network in dom test') }))
})

afterEach(() => {
  cleanup()
  setLang('zh')
  vi.unstubAllGlobals()
})

describe('比价/算价页 · 数字一律来自接口（★ 〇-X #55）', () => {
  it('① 默认示例（8 万字 × 1 语 · 专业档）的积分与金额，等于按公示公式手算的那个数', async () => {
    const { text } = renderPage()
    // 式子独立复算一次：7.5 + 80000/1000 × 400 × 1 = 32007.5 → 取整 32008
    const points = 32008
    await waitFor(() => expect(text()).toContain(fmtInt(points)))
    expect(text()).toContain(money(points * META.points_price_money))
    // 反向确认页面没有把接口值当摆设：卡面数字必须就是这个金额
    expect(document.querySelector('.lc-cmp-card.main')?.textContent).toContain(money(points * META.points_price_money))
  })

  it('② calcEstimatePoints 与公示公式逐字同式（含小数固定项的四舍五入方向）', () => {
    const pro = { per1k: 400, fixed: 7.5 }
    expect(calcEstimatePoints(pro, 80000, 1)).toBe(32008) // 32007.5 → 32008（半值向上）
    expect(calcEstimatePoints(pro, 1000, 2)).toBe(808)    // 7.5 + 800 = 807.5 → 808
    expect(calcEstimatePoints(pro, 19, 1)).toBe(15)       // 短单由固定项兜底：7.5 + 7.6 = 15.1 → 15
    expect(calcEstimatePoints({ per1k: 150, fixed: 3 }, 80000, 1)).toBe(12003)
    // 非法输入一律 0，不进算式（负数/NaN/0 都会把「预估」变成无意义的钱数）
    expect(calcEstimatePoints(pro, 0, 1)).toBe(0)
    expect(calcEstimatePoints(pro, -5, 1)).toBe(0)
    expect(calcEstimatePoints(pro, Number.NaN, 1)).toBe(0)
    expect(calcEstimatePoints(pro, 100, 0)).toBe(0)
  })

  it('③ 切到快速档后，同一批字数按 fast 系数重算（证明档位真的参与算式）', async () => {
    const { text } = renderPage()
    await waitFor(() => expect(text()).toContain(fmtInt(32008)))
    const fastBtn = [...document.querySelectorAll('.lc-cmp-mode')].find((b) => b.textContent === t('cmp.modeFast'))
    expect(fastBtn, '快速档切换按钮未渲染').toBeTruthy()
    fireEvent.click(fastBtn!)
    await waitFor(() => expect(text()).toContain(fmtInt(12003)))
    expect(text()).not.toContain(fmtInt(32008))
  })

  it('④ 改输入即改结果：1000 字符 × 2 语种（专业档）＝808 积分', async () => {
    const { text } = renderPage()
    await waitFor(() => expect(text()).toContain(fmtInt(32008)))
    const inputs = [...document.querySelectorAll('.lc-cmp-field input')] as HTMLInputElement[]
    expect(inputs.length).toBe(2) // 字数 + 语种数
    fireEvent.change(inputs[0], { target: { value: '1000' } })
    fireEvent.change(inputs[1], { target: { value: '2' } })
    await waitFor(() => expect(text()).toContain(fmtInt(808)))
  })

  it('⑤ 接口失败 / 系数残缺 → 明说取不到，且一张金额卡都不出（不许拿兜底值报价）', async () => {
    apiMocks.request.mockImplementation(async () => ({ success: false }))
    const { text } = renderPage()
    await waitFor(() => expect(text()).toContain(t('cmp.unavailable')))
    expect(document.querySelectorAll('.lc-cmp-card').length).toBe(0)
    expect(text()).not.toContain('¥') // 不可用态连货币符号都不该出现
    // 残缺包（只有 pro 没有 fast / 系数非数）同样判不可用：半个系数也能算出一个「看起来很确定」的价
    apiMocks.request.mockImplementation(async () => ({
      ...META, modes: [{ code: 'pro', points_per_1k_chars: 400, points_fixed: 7.5 }],
    }))
    cleanup()
    const r2 = renderPage()
    await waitFor(() => expect(r2.text()).toContain(t('cmp.unavailable')))
    expect(document.querySelectorAll('.lc-cmp-card').length).toBe(0)
  })
})

describe('比价/算价页 · 公开口径红线', () => {
  it('⑥ 整页零 token 裸值与内部键名（公开页只讲积分与钱）', async () => {
    const { text } = renderPage()
    await waitFor(() => expect(text()).toContain(fmtInt(32008)))
    const body = text()
    expect(body).not.toMatch(/token/i)
    for (const internal of ['est_tokens_per_char', 'est_tokens_fixed', 'points_tokens_rate', 'price_fen'])
      expect(body, `内部配置键名外露：${internal}`).not.toContain(internal)
    // 尺子与汇率的字面档不许出现在页面上：它们一旦外露，
    // 就等于把「每积分 = 400」这条内部换算重新塞回公开口径
    for (const bare of ['33222', '24917', '400 每积分']) expect(body).not.toContain(bare)
  })

  it('⑦ 公式区块、系数读数与浮动说明全部在位（用户点名「公式放出来 + 说明会浮动」）', async () => {
    const { text } = renderPage()
    await waitFor(() => expect(text()).toContain(fmtInt(32008)))
    expect(text()).toContain(t('cmp.formula'))
    // 三条系数读数：固定项 7.50、线性档 400、每积分单价 0.0997（都来自接口而非写死）
    expect(text()).toContain(tpl('cmp.coefFixed', { mode: t('cmp.modePro'), v: fmtNum(7.5, { min: 2, max: 2 }) }))
    expect(text()).toContain(tpl('cmp.coefLinear', { mode: t('cmp.modePro'), v: fmtInt(400) }))
    expect(text()).toContain(tpl('cmp.coefPrice', { p: fmtNum(META.points_price_money, { min: 4, max: 4 }) }))
    // 浮动说明（小语种 / 专业度·抽象程度 / 结构）与「不构成逐单承诺」的边界句
    expect(text()).toContain(t('cmp.varBody'))
    expect(text()).toContain(t('cmp.disclaimer'))
  })

  it('⑧ 人工对照区间由组件常量算出，且来源与采集日期同屏（用户要求「标注来源与时间」）', async () => {
    const { text } = renderPage()
    await waitFor(() => expect(text()).toContain(fmtInt(32008)))
    const lo = EXAMPLE_CHARS * EXAMPLE_LANGS * HUMAN_LO // 16,000.00
    const hi = EXAMPLE_CHARS * EXAMPLE_LANGS * HUMAN_HI // 24,000.00
    expect(text()).toContain(money(lo))
    expect(text()).toContain(money(hi))
    // 省比只对**低档**取（对高档算出来的"省"更大、更接近吹牛）
    const pct = Math.round((1 - (32008 * META.points_price_money) / lo) * 100)
    expect(text()).toContain(tpl('cmp.save', { pct }))
    expect(pct).toBe(80)
    // 出处两条 + 采集日期 + 口径边界（不是均价也不是上限）+ 后编辑口径，一句都不许少
    expect(text()).toContain(t('cmp.humanSrc1'))
    expect(text()).toContain(t('cmp.humanSrc2'))
    expect(text()).toContain('2026-09-27')
    expect(text()).toContain(t('cmp.humanScope'))
    expect(text()).toContain(t('cmp.humanFloor'))
    expect(text()).toContain(t('cmp.postBody'))
    expect(text()).toContain(t('cmp.scopeBody'))
  })

  it('⑨ 两个公开页互相引流：/pricing 与首页导航都有 /compare 入口', () => {
    // 路径按 cwd（vitest 的根＝frontend-react）取，与 parity.test.ts 的 F2 扫描同一口径——
    // jsdom 环境下 import.meta.url 不是 file: 协议，fileURLToPath 会直接报 TypeError
    const prc = readFileSync('src/components/PricingPage.tsx', 'utf8')
    const land = readFileSync('src/components/Landing.tsx', 'utf8')
    expect(prc).toContain('href="/compare"')
    expect(land).toContain('href="/compare"')
  })
})
