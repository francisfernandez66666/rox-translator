// ============ e2e/sales_console.spec.ts · 职责说明 ============
// 二期卖家面「说法草稿」inbox /sales/drafts（施工_二期 §9.4）的**真浏览器**运行时锁。
//
// 三条硬口径（与 e2e/product_shelf.spec.ts 同形）：
//  ① 移动档必须**显式设 viewport**（默认 project 只有 Desktop Chrome 1280×720）；
//  ② 判溢出用 spillersIn() 形态（逐后代比**右边界** rect.right > box.right），
//     不许只判 documentElement（结构性恒真，AGENTS P7 那条）；
//  ③ 链路型断言自带**可达探针**：先直连 /api/sales/drafts 取真值，取不到就点名
//     「链路没通」，绝不放行兜底空态。
//
// 登录态页面 ⇒ 必须带 AUTH_STATE（storageState）＋ TOKEN 才能跑；不带整段 skip。
// ⚠️ 需真后端 + 已构建 dist + 登录态 storageState；未纳入 vitest，也不在 tsc include 内。
// ⚠️ 「转正前／后召回命中 0→1」那一腿依赖销售引擎召回池（S2 六包平移后才存在），
//    本文件暂不写空转判据；转正写路径已由 SalesDrafts.noAutoApprove.lock.test.tsx（E 腿）
//    与 backend-go/internal/api/sales_draft_gate_test.go 双侧覆盖，召回腿随引擎迁入补。
// ==============================================================
import { expect, test } from '@playwright/test'

/** 移动档显式视口（不依赖默认 project 的 Desktop Chrome 1280×720） */
const MOBILE = { width: 390, height: 844 }
/** 草稿 inbox 路径（可由部署域覆盖，默认同源 /sales/drafts） */
const DRAFTS_URL = process.env.SALES_DRAFTS_URL || '/sales/drafts'
/** 登录态 storageState 路径；为空整段 skip（与一期货架 e2e 同形态） */
const AUTH_STATE = process.env.SALES_DRAFTS_AUTH_STATE || ''
/** 横向溢出判据的容器靶：组件根节点 */
const PAGE_BOX = '[data-testid="sales-drafts-page"]'

/** 逐后代比右边界（与 product_shelf／ticket_delivery 的 spillersIn 同形；只问右不问左） */
async function spillersIn(page: import('@playwright/test').Page): Promise<string[]> {
  return page.evaluate((sel) => {
    const box = document.querySelector(sel) as HTMLElement | null
    if (!box) return [`草稿页容器 ${sel} 不存在（可达探针失败：页面没真渲染）`]
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
  }, PAGE_BOX)
}

test.describe('卖家说法草稿 inbox /sales/drafts（§9.4）', () => {
  const TOKEN = process.env.SALES_TOKEN || ''
  test.skip(!AUTH_STATE || !TOKEN, '需 SALES_DRAFTS_AUTH_STATE ＋ SALES_TOKEN 才能打登录态草稿 inbox')

  test.use({ viewport: MOBILE, storageState: AUTH_STATE })

  test('可达探针 + inbox 真渲染 + 逐后代无横向溢出（移动档 390×844）', async ({ page, request }) => {
    // ① 可达探针：APIRequestContext 只带 cookie，登录态住在 sessionStorage ⇒ 必须显式 Bearer
    const probe = await request.get('/api/sales/drafts', { headers: { Authorization: `Bearer ${TOKEN}` } })
    expect(probe.ok(), '链路没通：草稿接口不可达（401 ⇒ 凭证通道没接对，不能拿兜底空态当通过）').toBeTruthy()
    const body = (await probe.json().catch(() => null)) as { success?: boolean; drafts?: unknown[] } | null
    expect(body && body.success === true, '链路探针：草稿接口未返回 success（数据没真取到）').toBeTruthy()
    expect(Array.isArray(body?.drafts), '草稿列表必须是数组（空集出 []，不出 null）').toBeTruthy()

    // 登录态显式注入 sessionStorage（入口分诊在 main.tsx 模块初始化时读一次 getAuthToken）
    await page.addInitScript((t) => {
      try { sessionStorage.setItem('auth_token', t) } catch { /* 站点数据被拒＝按未登录起步，下面判据自己会红 */ }
    }, TOKEN)

    await page.goto(DRAFTS_URL)
    await expect(page.locator(PAGE_BOX)).toBeVisible()
    await expect(page.locator('[data-testid="sales-drafts-list"]')).toBeVisible()

    // ② 逐后代比右边界
    const spill = await spillersIn(page)
    expect(spill, `移动档逐后代横向溢出：${JSON.stringify(spill)}`).toEqual([])
  })

  test('能力三态读数与后端一致（半套配置不许呈现为已生效）', async ({ page, request }) => {
    const cap = await request.get('/api/sales/capability', { headers: { Authorization: `Bearer ${TOKEN}` } })
    expect(cap.ok(), '能力面不可达').toBeTruthy()
    const cb = (await cap.json().catch(() => null)) as { success?: boolean; status?: string } | null
    expect(cb && cb.success === true && typeof cb.status === 'string', '能力面未返回三态状态词').toBeTruthy()
    expect(['off', 'half', 'on'], `能力状态词必须是三态之一，实得 ${cb?.status}`).toContain(cb!.status)

    await page.addInitScript((t) => {
      try { sessionStorage.setItem('auth_token', t) } catch { /* 同上 */ }
    }, TOKEN)
    await page.goto(DRAFTS_URL)
    // ★ 等值判据必须取状态词本身（data-state），不能拿本地化文案比：
    //   译文随语种变，拿文案做等值必然假绿；旧版只判「文案非空」更是空转锁。
    const row = page.locator('[data-testid="sales-capability-state"]')
    await expect(row).toBeVisible()
    expect((await row.textContent())?.trim().length, '能力行文案为空').toBeGreaterThan(0)
    expect(await row.getAttribute('data-state'), '能力行状态必须与后端现读等值（不许前端自造「已生效」）').toBe(cb!.status)
  })
})