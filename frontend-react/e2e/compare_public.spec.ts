// ============ e2e/compare_public.spec.ts · 职责说明 ============
// 官网「比价与算价」页 /compare（★ 〇-X #55，2026-09-28）的**运行时**锁：
//   C1 访客可达 + 主卡报价与后端系数同源 + 公开面零 token 裸值；
//   C2 真浏览器里改量、切档，页面数字仍等于按同一套系数现算的值。
//
// 为什么 jsdom 那份（src/components/PriceComparePage.dom.test.tsx，9 例）不算够：
//  ① 它测的是源码组件，没经过 vite build 与 spa.go 的真实路由表。
//     「未登录访问 /compare 被兜底送去登录页」这一类错只会在真浏览器 + 真后端下暴露——
//     App.tsx 的访客放行是一份**硬编码路径白名单**（/pricing、/compare、/ 各写一处），
//     以后再加公开页漏写一条，单测一颗都不会红，客户看到的却是登录墙。
//  ② 它喂的是假 meta，测不到「页面上那个数 == 线上接口那套系数算出来的数」。
//     这条同源关系正是这个页存在的全部意义：公示公式必须就是扣费公式，
//     否则超管调档后官网会一直报旧价（F-12「三口径打架」的公开页版本）。
//
// ★ 链路型断言按 AGENTS.md §6 自带可达探针：先直连 GET /api/pricing/meta 取真系数，
//   取不到就点名「链路没通」，绝不放行兜底态——系数拿不到时页面渲染的是
//   「暂时取不到」空态 + 零个金额卡，那种形态对着"页面有字"之类的弱断言会一路绿灯。
//
// 数值不写死：期望值全部由**本次接口返回值**现算，所以超管改档不会把这条锁判红，
//   而「前端写死了一份价」一旦复发（.lc-cmp-big 的数对不上现算值）立刻红灯。
// =============================================
import { expect, test } from '@playwright/test'

// BASE 前端站点地址：run_uat 以 BASE_URL 喂真后端直出的 dist；本地手跑回落 vite 预览端口
const BASE = process.env.BASE_URL || 'http://127.0.0.1:5173'

/** /api/pricing/meta 单档系数（积分口径；接口侧就不下发 token 裸值） */
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
 *    若以后要在德/法语种下复用这条判据，得先把分隔符语义换成 Intl 解析，
 *    否则 "3.190,17" 会被这里读成 3.19017——那是判据错，不是页面错。 */
function num(txt: string): number {
  return Number(txt.replace(/,/g, '').replace(/[^\d.]/g, ''))
}

/** 页面侧算式（与后端建单预检同一条两段式）：固定项 + 千字线性项 × 语种数，最后取整 */
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

test.describe('官网比价与算价页 /compare（访客可达·报价与后端同源）', () => {
  test('C1 未登录直达出自算器而非登录页，主卡金额＝后端系数现算值，页面无 token 裸值', async ({ page, request }) => {
    const meta = await loadMeta(request)
    const pro = meta.modes.find((x) => x.code === 'pro')!

    // 访客态：不带任何 sessionStorage/localStorage 会话，直接撞 /compare
    await page.goto(`${BASE}/compare`)
    await expect(page.locator('.lc-cmp-title'), '⇒ /compare 未渲染标题（路由是否把访客送去登录页了？）')
      .toBeVisible({ timeout: 30000 })
    // 登录页特征件必须为 0：这一条才真正区分「放行的公开页」与「兜底送进来的登录页」
    expect(await page.locator('input[type="password"]').count(), '⇒ 访客被兜底送进了登录页').toBe(0)
    // 兜底空态不许出现：探针已在上面拿到系数，这里还显示「取不到」就是前端接线断了
    expect(await page.locator('.lc-cmp-warn').count(), '⇒ 系数已取得却仍渲染不可用态').toBe(0)

    // 等值锁：默认样例 80,000 源字符 × 1 语种 × 专业档（与组件初值 EXAMPLE_CHARS/EXAMPLE_LANGS 同口径）
    const expPoints = pointsOf(pro, 80000, 1)
    const expMoney = expPoints * meta.points_price_money
    const shownPoints = num(await page.locator('.lc-cmp-card.main .lc-cmp-meta').innerText())
    const shownMoney = num(await page.locator('.lc-cmp-card.main .lc-cmp-big').innerText())
    expect(shownPoints, '⇒ 主卡积分对不上后端系数现算值（公示公式与扣费公式脱钩）').toBe(expPoints)
    // 金额渲染固定两位小数，判据按「四舍五入到分」等值，不给浮点误差留口子
    expect(Math.round(shownMoney * 100), '⇒ 主卡金额对不上现算值').toBe(Math.round(expMoney * 100))

    // 公示公式区块：公式原文 + 三条系数行（两档各一行 + 每积分单价一行）
    await expect(page.locator('.lc-cmp-formula'), '⇒ 公示公式没渲染').toBeVisible()
    expect(await page.locator('.lc-cmp-coef li').count(), '⇒ 系数行数 ≠ 3（fast/pro/每积分单价）').toBe(3)

    // 公开面零 token 裸值（AGENTS §一·5）：整页文本里既不许出现 token 字样，
    // 也不许出现内部计量常数——24917 是充值尺子（分/百万 token）、33222 是它的上一版，
    // 都不该以裸值形态出现在客户看到的任何一页上。
    const body = await page.locator('body').innerText()
    expect(body.toLowerCase(), '⇒ 公开页出现 token 字样').not.toMatch(/token/)
    expect(body, '⇒ 公开页泄漏内部计量常数').not.toMatch(/24917|33222/)

    await page.screenshot({ path: 'artifacts/compare_public_c1.png', fullPage: true })
  })

  test('C2 改量与切档后，页面数字仍等于按同一套系数现算的值', async ({ page, request }) => {
    const meta = await loadMeta(request)
    const fast = meta.modes.find((x) => x.code === 'fast')!
    const pro = meta.modes.find((x) => x.code === 'pro')!

    await page.goto(`${BASE}/compare`)
    await expect(page.locator('.lc-cmp-card.main')).toBeVisible({ timeout: 30000 })

    // ① 改量：1,000 源字符 × 2 个语种，专业档
    const charsInput = page.locator('.lc-cmp-field input').nth(0)
    const langsInput = page.locator('.lc-cmp-field input').nth(1)
    await charsInput.fill('1000')
    await langsInput.fill('2')
    let exp = pointsOf(pro, 1000, 2)
    expect(num(await page.locator('.lc-cmp-card.main .lc-cmp-meta').innerText()),
      '⇒ 改量后积分未随输入重算').toBe(exp)

    // ② 切档：同一组输入切到快速档，必须换数（切档钮没接线是最容易漏的一种假绿）
    const expPro = exp
    await page.locator('.lc-cmp-mode').nth(0).click()
    exp = pointsOf(fast, 1000, 2)
    expect(num(await page.locator('.lc-cmp-card.main .lc-cmp-meta').innerText()),
      '⇒ 切到快速档后仍显示专业档的价').toBe(exp)
    expect(exp, '⇒ 两档系数相同，本锁失去区分度（先核接口再核用例）').not.toBe(expPro)

    // ③ 清空输入：回到「需要输入」提示，且不许留任何金额卡（半截报价比不报价更危险）
    await charsInput.fill('')
    await expect(page.locator('.lc-cmp-hint'), '⇒ 空输入未给出提示态').toBeVisible()
    expect(await page.locator('.lc-cmp-card').count(), '⇒ 空输入仍在展示金额卡').toBe(0)
  })
})
