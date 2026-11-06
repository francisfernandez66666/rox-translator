// ============ e2e/product_shelf.spec.ts · 职责说明 ============
// 一期卖家产品货架 /products（施工_一期 §5.7／§8.1／§9.4）的**真浏览器**运行时锁。
//
// 三条硬口径（对应施工图 §9.4）：
//  ① 移动档必须**显式设 viewport**：默认 project 只有 Desktop Chrome 1280×720，
//     不显式设就等于没测移动档；
//  ② 判溢出用 `spillersIn()` 形态（**逐后代比右边界**：rect.right > box.right），
//     不许只判 documentElement（结构性恒真，AGENTS P7 那条）；
//  ③ 链路型断言自带**可达探针**：先直连产品货架数据接口取真值，取不到就点名「链路没通」，
//     绝不放行兜底空态（否则离线兜底一路绿灯）。
//
// 货架是登录态页面（与对外页 /p/ 匿名面相对），所以本文件**必须**带 AUTH_STATE
// （Playwright storageState 路径）才能跑；不带就整段 skip。
// ⚠️ 本文件需要真后端 + 已构建 dist + 登录态 storageState 才能跑；未纳入 vitest，
//   也不在 tsc 的 include 内（与 e2e/product_public.spec.ts 同形态）。
// ==============================================================
import { expect, test } from '@playwright/test'

/** 移动档显式视口（不依赖默认 project 的 Desktop Chrome 1280×720） */
const MOBILE = { width: 390, height: 844 }
const SHELF_URL = process.env.PRODUCT_SHELF_URL || '/products'
const AUTH_STATE = process.env.PRODUCT_SHELF_AUTH_STATE || ''

/** 逐后代比**右边界**：与 e2e/ticket_delivery.spec.ts 的 spillersIn 同形（溢出＝右边界越出容器右边界）。
 *  ★ 只问右不问左：收起态的移动抽屉与蜜罐输入框都合法地处负左坐标，加左判据＝把设计判成缺陷
 *     （本轮 product_public 那条实测红在 `INPUT left=-9999` 的蜜罐上）。
 *  ★ 靶子必须是货架自己那个容器：登录态外壳 FrontPage 不渲染 `<main>`（`grep '<main' src` 只有
 *    公开面与定价/比价页有），旧写法 `querySelector('main')` 在 /products 上恒为 null ⇒
 *    这条诊断腿自己就是假的（"一切正常"读自"什么都没量到"）。 */
const SHELF_BOX = '[data-testid="product-shelf"]'

async function spillersIn(page: import('@playwright/test').Page): Promise<string[]> {
  return page.evaluate((sel) => {
    const box = document.querySelector(sel) as HTMLElement | null
    if (!box) return [`货架容器 ${sel} 不存在（可达探针失败：页面没真渲染）`]
    const br = box.getBoundingClientRect()
    const out: string[] = []
    box.querySelectorAll<HTMLElement>('*').forEach((k) => {
      const r = k.getBoundingClientRect()
      if (r.right > br.right + 1) {
        const tid = k.getAttribute('data-testid')
        out.push(`${k.tagName}${tid ? `[testid=${tid}]` : ''} left=${Math.round(r.left)} right=${Math.round(r.right)} > box=${Math.round(br.right)}`)
      }
    })
    return out.sort((a, b) => b.localeCompare(a)).slice(0, 8)
  }, SHELF_BOX)
}

test.describe('卖家产品货架 /products（§5.7／§9.4）', () => {
  const TOKEN = process.env.PRODUCT_TOKEN || ''
  test.skip(!AUTH_STATE || !TOKEN, '需 PRODUCT_SHELF_AUTH_STATE ＋ PRODUCT_TOKEN 才能打登录态货架')

  test.use({ viewport: MOBILE, storageState: AUTH_STATE })

  test('可达探针 + 货架真渲染 + 逐后代无横向溢出（移动档 390×844）', async ({ page, request }) => {
    // ① 链路型可达探针：接口腿必须显式带 Bearer——Playwright 的 APIRequestContext 只带 cookie，
    //   而本仓登录态住在 sessionStorage（core.ts）里 ⇒ storageState 对 request.get 完全无效，
    //   不带这个头会得 401 并被读成"链路没通"（判据没错，凭证通道拿错了）。
    const probe = await request.get('/api/product/list', { headers: { Authorization: `Bearer ${TOKEN}` } })
    expect(probe.ok(), '链路没通：货架数据接口不可达（401 ⇒ 凭证通道没接对，不能拿兜底空态当通过）').toBeTruthy()
    const body = (await probe.json().catch(() => null)) as { success?: boolean; products?: unknown[] } | null
    expect(body && body.success === true, '链路探针：货架接口未返回 success（数据没真取到）').toBeTruthy()
    expect(Array.isArray(body?.products) && (body?.products?.length ?? 0) > 0,
      '夹具里没有产品：货架会停在空态，下面两条判据就成了空转').toBeTruthy()

    // ★ 登录态**显式注入 sessionStorage**，不只靠 storageState：入口分诊在 `main.tsx` 模块初始化时
    //   读一次 `getAuthToken()`（core.ts 的模块级变量），实测本轮 storageState 里那份 sessionStorage
    //   没能赶在模块初始化前落地 ⇒ 分诊按"未登录"走公开壳，货架整颗不渲染，
    //   红字是 "element(s) not found"（读起来像路由坏了，其实是登录态没赶上第一帧）。
    //   `addInitScript` 在任何页面脚本之前执行，是唯一"一定赶得上"的形态。
    await page.addInitScript((t) => {
      try { sessionStorage.setItem('auth_token', t) } catch { /* 站点数据被拒＝按未登录起步，下面的判据自己会红 */ }
    }, TOKEN)

    await page.goto(SHELF_URL)
    // 正向对照：登录态货架确实渲染，而且**齐备度现算真出了格**（空态文案不算渲染）
    await expect(page.locator(SHELF_BOX)).toBeVisible()
    expect(await page.locator('[data-testid^="readiness-"]').count(),
      '齐备度一格都没出（接口通了但六格现算没落地）').toBeGreaterThan(0)

    // ② 逐后代比右边界（不是只判 documentElement）
    const spill = await spillersIn(page)
    expect(spill, `移动档逐后代横向溢出：${JSON.stringify(spill)}`).toEqual([])
  })
})
