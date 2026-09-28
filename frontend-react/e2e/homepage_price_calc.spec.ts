// ============ e2e/homepage_price_calc.spec.ts · 职责说明 ============
// 官网首页「快速算价」卡（★ 2026-09-28 〇-Y #64）的**运行时**锁。
// 需求原话：「比价和算价计算器放首页一下，供用户快速计算，但是细节要点击进入现在的比价
//             和算价链接」——所以本文件只锁两件运行时事实：
//   H1 访客打开首页就能看到卡、卡上的钱＝线上 /api/pricing/meta 那套系数现算的值；
//   H2 改量后当场重算，点「详情」真的落到现有的 /compare 全量页，且把两页置成同一组输入后报同一个数。
//      ⚠ 两页各持一份输入状态（都是组件本地 useState，没有共享 store、也不走 URL 传参），
//        所以"同一组输入"必须由用例在两侧分别 fill 出来，不能假设首页填了 /compare 就跟着变。
//
// 为什么 jsdom 那两份（PriceQuickCalc.dom.test 7 例 + Landing.dom.test ⑯⑰⑱）不算够：
//  ① 它们喂的是 mock 系数，测不到「首页那个数 == 线上接口那套系数算出来的数」。
//     这条同源关系正是这张卡存在的全部意义：公示的价必须就是扣费的价，否则超管调档后
//     官网一直报旧价（F-12「三口径打架」的公开页版本）。
//  ② 它们没经过 vite build 与 spa.go 的真实路由表：「首页点详情跳不进 /compare」
//     「/compare 访客被兜底送去登录页」这类错只在真浏览器 + 真后端下暴露
//     （App.tsx 的访客放行是一份硬编码路径白名单，新增公开页漏写一条，单测一颗都不会红）。
//  ③ jsdom 不看布局与动效：首页的 .lc-reveal 在真实 IntersectionObserver 下才会显形，
//     卡在首屏下方时的实际可见性只有运行时能证。
//
// ★ 链路型断言按 AGENTS.md §6 自带可达探针：先直连 GET /api/pricing/meta 取真系数，
//   取不到就点名「链路没通」，绝不放行兜底态——系数拿不到时卡片渲染的是「暂时取不到」
//   空态 + 零个金额，那种形态对着"页面有字"之类的弱断言会一路绿灯。
//
// 数值不写死：期望值全部由**本次接口返回值**现算，所以超管改档不会把这条锁判红，
//   而「前端写死了一份价」一旦复发（.lc-qc-money 的数对不上现算值）立刻红灯。
// =============================================
import { expect, test } from '@playwright/test'

// BASE 前端站点地址：run_uat 以 BASE_URL 喂真后端直出的 dist；本地手跑回落 vite 预览端口
const BASE = process.env.BASE_URL || 'http://127.0.0.1:5173'

/** /api/pricing/meta 单档系数（积分口径；接口侧零 token 裸值） */
interface MetaMode {
  code: string
  points_per_1k_chars: number
  points_fixed: number
}
/** /api/pricing/meta 出参（只声明本用例用到的字段，接口加字段不必改这里） */
interface PricingMeta {
  success: boolean
  points_price_money: number
  modes: MetaMode[]
}

/** num 从渲染文本里取出数值：剥货币符与千分位逗号后按十进制解析。
 *  ⚠ 分隔符口径按 zh-CN 钉（playwright.config.ts 把 locale 钉成 zh-CN，本用例不切语言）：
 *    显示形如「¥3,190.17」「32,008 积分」，逗号是千分位、点是 Decimal 分隔符。
 *    与 compare_public.spec.ts 同一条判据，两个文件必须一起改。 */
function num(txt: string): number {
  return Number(txt.replace(/,/g, '').replace(/[^\d.]/g, ''))
}

/** 页面侧算式（与后端建单预检同一条两段式）：固定项 + 千字线性项 × 语种数，最后取整。
 *  与 src/lib/priceCalc.ts 的 calcEstimatePoints 逐字同式——首页卡、/compare 页、后端三处
 *  必须算出同一个数，这条式子在测试里重写一遍是**有意的**：
 *  从被测代码 import 过来就变成自证，抓不到「前端整批算错但自洽」。 */
function pointsOf(m: MetaMode, chars: number, langs: number): number {
  return Math.round(m.points_fixed + (chars / 1000) * m.points_per_1k_chars * langs)
}

/** 取本次接口的系数；顺带把「接口通了但数据不可用」也判成链路红灯（宁缺勿错） */
async function loadMeta(request: import('@playwright/test').APIRequestContext): Promise<PricingMeta> {
  const res = await request.get(`${BASE}/api/pricing/meta`)
  expect(res.ok(), '⇒ /api/pricing/meta 链路没通（HTTP 非 2xx）').toBeTruthy()
  const meta = (await res.json()) as PricingMeta
  expect(meta.success, '⇒ 公开算价接口未回 success').toBe(true)
  expect(meta.points_price_money, '⇒ 每积分单价不可用').toBeGreaterThan(0)
  for (const code of ['fast', 'pro']) {
    const m = meta.modes.find((x) => x.code === code)
    expect(m, `⇒ 接口缺 ${code} 档系数`).toBeTruthy()
    expect(m!.points_per_1k_chars, `⇒ ${code} 每千字系数非正数`).toBeGreaterThan(0)
  }
  return meta
}

/** 等首屏下方的算价卡真正显形（.lc-reveal 由 IntersectionObserver 放行，
 *  不滚动时元素虽在 DOM 里但仍未进入视口） */
async function openCalcCard(page: import('@playwright/test').Page): Promise<void> {
  await page.goto(`${BASE}/`)
  const card = page.locator('.lc-qc')
  await expect(card, '⇒ 首页没渲染快速算价卡').toBeVisible({ timeout: 30000 })
  await card.scrollIntoViewIfNeeded()
  // 兜底态不许出现：探针已在上面拿到系数，这里还显示「取不到」就是前端接线断了
  await expect(card.locator('[data-qc="out"]'), '⇒ 系数已取得却仍渲染不可用态').toBeVisible()
}

test.describe('官网首页快速算价卡（访客可见·与后端同源·详情跳 /compare）', () => {
  test('H1 未登录直达首页出算价卡，卡面金额＝后端系数现算值', async ({ page, request }) => {
    const meta = await loadMeta(request)
    const pro = meta.modes.find((x) => x.code === 'pro')!

    // 访客态：不带任何 sessionStorage/localStorage 会话，直接撞首页
    await openCalcCard(page)
    const card = page.locator('.lc-qc')
    // 登录页特征件必须为 0：这一条才真正区分「放行的公开页」与「兜底送进来的登录页」
    expect(await page.locator('input[type="password"]').count(), '⇒ 访客被兜底送进了登录页').toBe(0)

    // 等值锁：默认预置量 80,000 源字符 × 1 语种 × 专业档
    // （与 src/lib/priceCalc.ts 的 EXAMPLE_CHARS/EXAMPLE_LANGS 同口径，也是 /compare 页初值）
    const expPoints = pointsOf(pro, 80000, 1)
    const expMoney = expPoints * meta.points_price_money
    const shownPoints = num(await card.locator('[data-qc="points"]').innerText())
    const shownMoney = num(await card.locator('[data-qc="money"]').innerText())
    expect(shownPoints, '⇒ 首页卡积分对不上后端系数现算值（公示公式与扣费公式脱钩）').toBe(expPoints)
    // 金额渲染固定两位小数，判据按「四舍五入到分」等值，不给浮点误差留口子
    expect(Math.round(shownMoney * 100), '⇒ 首页卡金额对不上现算值').toBe(Math.round(expMoney * 100))

    // 人工对照只显示**低档**那一头（省比只对低档取），且卡上确有这一行
    const humanText = await card.locator('[data-qc="human"]').innerText()
    expect(Math.round(num(humanText) * 100), '⇒ 人工对照价不是按 0.20 元/源字符的低档算的')
      .toBe(Math.round(80000 * 0.20 * 100))

    // 快查侧不许出现公示公式与系数明细：细节全部留给 /compare（用户口径）
    const cardText = await card.innerText()
    expect(cardText, '⇒ 首页卡抄了公示公式明细').not.toContain('每次建单固定积分')
    expect(await card.locator('.lc-cmp-coef').count(), '⇒ 首页卡渲染了 /compare 的系数表').toBe(0)

    // 卡片自身的文本零 token 裸值（AGENTS §一·5）：整页文本里有 SDK 代码示例的合法字样，
    // 所以这一条只圈卡片，别把首页其他区块的既有内容误判成泄漏
    expect(cardText.toLowerCase(), '⇒ 快速算价卡出现 token 字样').not.toMatch(/token/)
    expect(cardText, '⇒ 快速算价卡泄漏内部计量常数').not.toMatch(/24917|33222/)

    await page.screenshot({ path: 'artifacts/homepage_price_calc_h1.png', fullPage: true })
  })

  test('H2 改量当场重算，点详情落到 /compare 且两页报同一个数', async ({ page, request }) => {
    const meta = await loadMeta(request)
    const pro = meta.modes.find((x) => x.code === 'pro')!

    await openCalcCard(page)
    const card = page.locator('.lc-qc')

    // ① 改量：1,000 源字符 × 2 个语种（专业档——首页不给切档钮，切档属于细节）
    const charsInput = card.locator('.lc-qc-field input').nth(0)
    const langsInput = card.locator('.lc-qc-field input').nth(1)
    await charsInput.fill('1000')
    await langsInput.fill('2')
    const exp = pointsOf(pro, 1000, 2)
    expect(num(await card.locator('[data-qc="points"]').innerText()),
      '⇒ 改量后首页卡积分未随输入重算').toBe(exp)
    const qcMoney = num(await card.locator('[data-qc="money"]').innerText())

    // ② 详情入口：点击必须真的落到现有的 /compare（硬编码 <a href> 坏了会退回首页自己）
    await card.locator('a[data-qc="more"]').click()
    await expect(page, '⇒ 点详情没落到 /compare').toHaveURL(/\/compare$/)
    await expect(page.locator('.lc-cmp-title'), '⇒ /compare 未渲染标题').toBeVisible({ timeout: 30000 })

    // ③ 跨页等值：把 /compare 自己的输入也置成同一组（1,000 × 2），它主卡的钱必须等于首页刚报的钱。
    //    ★ 前提要说清楚：这两张卡**各持一份输入状态**（都是 React 本地 useState，初值同为
    //    EXAMPLE_CHARS/EXAMPLE_LANGS，但谁也不读写对方的状态，也没走 URL 传参）。所以"同一组输入"
    //    必须由本用例在两侧分别置出来——首版漏了这一步，跑到这里 /compare 仍停在 80,000×1 的默认样例，
    //    于是 PG 全量闸门真红了一次（首页 ¥80.53 对 /compare ¥3,190.17）。**那次红是断言写错，
    //    不是产品缺陷**：两侧各自的数都对得上后端现算值（首页那条重算锁就在上一行）。
    //    这一条对照仍然是本用例存在的理由：两侧各测各的数值锁抓不到「两个页面报两个价」，
    //    而那正是把计算器复制到首页之后最容易发生的新缺陷（同源＝同一套系数 + 同一条式子）。
    await page.locator('.lc-cmp-field input').nth(0).fill('1000')
    await page.locator('.lc-cmp-field input').nth(1).fill('2')
    const cmpMoney = num(await page.locator('.lc-cmp-card.main .lc-cmp-big').innerText())
    expect(Math.round(cmpMoney * 100), '⇒ 首页与 /compare 对同一组输入报出不同的钱')
      .toBe(Math.round(qcMoney * 100))
    expect(Math.round(qcMoney * 100), '⇒ 两页金额相等但都对不上后端现算值（同源锁失效）')
      .toBe(Math.round(exp * meta.points_price_money * 100))

    await page.screenshot({ path: 'artifacts/homepage_price_calc_h2.png', fullPage: true })
  })
})
