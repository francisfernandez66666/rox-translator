// ============ e2e/landing_off_gate.spec.ts · 职责说明 ============
// 〇-Z「演示站不进主页」门面开关的**运行时**锁（★ 2026-09-28 #69/#70）。
//
// 后端那半边（curl 首页看有没有那段标记）由 scripts/uat/api_uat_txn.sh 的 T67 锁；
// 本文件锁的是只有真浏览器才暴露的三件事：
//   ① 标记为 landing:false 时，未登录访客打开 `/` 确实**落到登录页且地址栏变成 /login**
//      （用户口径「跳到登录页、地址栏跟着变」——用 <Navigate replace> 而不是原地渲染 <Login>
//       才满足后半句；原地渲染会留下 `/` 这个 URL，用户后退就再次撞 `/` 形成来回跳）；
//   ② 标记缺省（＝主站形态）时首页一字不变，仍然是官网落地页；
//   ③ 开关的射程只有 `/`：/pricing、/compare 这类营销门面即使关了主页也必须可达
//      （体验机也要让人看到价——这条一旦被人「顺手」扩大成整站拦截， jsdom 与 Go 两侧都看不见）。
//
// ★ 为什么用 addInitScript 而不是改后端策略：真部署里「关不关主页」是演示单元的一个环境变量
//   （LANDING_DISABLED）或后台一个开关，E2E 矩阵跑的是主站形态（必须展示主页）。
//   在这里注入 window.__SITE_FLAGS__ 等于把「后端直出的那串字节」在浏览器侧等价复现，
//   测的是**前端消费端**这一层；生产形态本身由 T67 + deploy/smoke_brand_homepage.sh 判据 H 覆盖。
//   （两侧合起来才是完整链：只看注入值会漏掉「后端根本没注入」，只看 curl 会漏掉「前端读错了」。）
//
// ⚠ 底档：playwright.config.ts 把 locale 钉成 zh-CN、vitest.setup 预置 app_lang=zh，
//   本文件所有文案断言按中文态写。
// =============================================
import { expect, test } from '@playwright/test'

const BASE = process.env.BASE_URL || 'http://127.0.0.1:5173'

/** 把后端直出的站点门面标记在页面脚本执行前塞进 window（等价复现那段 <script id="__site_flags__">） */
async function seedFlags(page: import('@playwright/test').Page, flags: unknown): Promise<void> {
  await page.addInitScript(`window.__SITE_FLAGS__ = ${JSON.stringify(flags)};`)
}

test.describe('门面开关：演示站首页直落登录注册页', () => {
  test('G1 landing:false → 访客打开 `/` 重定向到 /login（地址栏跟着变，落地页一件都不渲染）', async ({ page }) => {
    await seedFlags(page, { landing: false })
    await page.goto(`${BASE}/`)
    // ① URL 判据：等值而不是「包含」——停在 /?xxx 或 /login?redirect=/ 都算没满足「地址栏跟着变」
    await expect(page).toHaveURL(/\/login$/)
    // ② 登录页特征件真的在场（只判 URL 会被「导航过去了但渲染空白」骗过）
    await expect(page.locator('input[type="password"]'), '⇒ 到了 /login 却没有登录表单').toBeVisible()
    // ③ 落地页特征件必须为 0：这一条才区分「重定向」与「登录页浮在首页之上」
    expect(await page.locator('.lc-nav-cta').count(), '⇒ 首页 CTA 仍在渲染＝没真跳走').toBe(0)
    expect(await page.locator('.lc-hero-h1').count(), '⇒ 落地页 hero 仍在渲染').toBe(0)
    // ④ 后退不许把访客再送回主页形成来回跳（replace 的语义就在这一步上）
    await page.goBack()
    expect(await page.locator('.lc-hero-h1').count(), '⇒ 后退又落回主页（应 replace 压掉这一条历史）').toBe(0)
    await page.screenshot({ path: 'artifacts/landing_off_gate_g1.png', fullPage: true })
  })

  test('G2 主站形态（未注入标记）：首页照常是官网落地页，绝不进登录页', async ({ page }) => {
    await page.goto(`${BASE}/`)
    await expect(page.locator('.lc-hero-h1'), '⇒ 默认档下主页被收掉了（读不到标记应按「开放」处理）')
      .toBeVisible()
    await expect(page).toHaveURL(`${BASE}/`)
    expect(await page.locator('input[type="password"]').count(), '⇒ 主站首页直落了登录表单').toBe(0)
  })

  test('G3 标记被污染（"false" 字符串／null／缺 landing 键）一律按缺省处理，不许误关主页', async ({ page }) => {
    // 三种脏形态逐个跑：readSiteFlags 的口径是「只有朴素布尔 false 才关」，
    // 把字符串 "false" 或别的怪形态当成关闭，等于让一次插件污染把对外营业的门面关掉。
    // 写法说明：addInitScript 是**按注册顺序依次执行**的，所以同一个 page 上反复注入，
    //   最后一份生效——不需要为每个形态另开 context（开 context 反而会把 zh-CN locale 钉底丢掉）。
    for (const bad of [{ landing: 'false' }, null, {}, []]) {
      await seedFlags(page, bad)
      await page.goto(`${BASE}/`)
      await expect(page.locator('.lc-hero-h1'), `⇒ 脏标记 ${JSON.stringify(bad)} 被当成「关闭主页」`)
        .toBeVisible({ timeout: 30000 })
      expect(page.url(), `⇒ 脏标记 ${JSON.stringify(bad)} 把访客送进了登录页`).not.toMatch(/\/login$/)
    }
  })

  test('G4 射程只有 `/`：关了主页，/pricing 与 /compare 对访客仍然可达', async ({ page }) => {
    await seedFlags(page, { landing: false })
    await page.goto(`${BASE}/compare`)
    await expect(page.locator('.lc-cmp-title'), '⇒ /compare 被门面开关一起收掉了（应只管首页）')
      .toBeVisible({ timeout: 30000 })
    await expect(page).toHaveURL(/\/compare$/)
    expect(await page.locator('input[type="password"]').count(), '⇒ 访客在 /compare 撞见登录墙').toBe(0)

    await page.goto(`${BASE}/pricing`)
    await expect(page).toHaveURL(/\/pricing$/)
    expect(await page.locator('input[type="password"]').count(), '⇒ 访客在 /pricing 撞见登录墙').toBe(0)
    // 落地页特有的 hero 也不该出现在这两页上（串页了说明分支写在了外层而不是 `/` 那一支）
    expect(await page.locator('.lc-hero-h1').count()).toBe(0)
  })
})
