// ============ e2e/product_reception.spec.ts · 职责说明 ============
// 四期 W2 · R3（施工图 §4.2.3 / §4.2.5）：访客在 /p/{tc}/{pc} 提问命中**该产品**事实（真链路）。
//
// ★ 链路型用例红线（AGENTS §一·6）：挂件有离线兜底，转发链断掉时界面照样渲染，
//   断言会对着兜底态一路绿灯。故**先断言离线标志元素 == 0、再断言拿到会话 sid**，
//   任一不满足直接点名「/assist-api 链路没通」，绝不放行兜底态。
// ★ 负向对照（R3 反证）：断言里若混入另一产品的事实串必须判红 —— 回复须命中本产品事实、
//   且不许混入另一条/另一产品的事实串；两串必须非空且不同（否则负向恒真是空转）。
//
// 需要真后端 + 真短码 + 真产品事实 + 已构建 dist（夹具见 run_uat.sh 的「5.9 产品对外页」段）。
// 短码与事实串一律由环境提供，不写死、不猜、不依赖外部域名；未纳入 vitest、不在 tsc include 内。
// ==============================================================================
import { expect, test } from '@playwright/test'

const TENANT = process.env.PRODUCT_TENANT_CODE || ''
const PAGE = process.env.PRODUCT_PAGE_CODE || ''
const THIS_FACT = process.env.PRODUCT_PUBLIC_VALUE || ''
// 另一产品/另一类事实串：优先夹具显式给的「另一产品公开事实」，回退同一夹具的受控事实
// （PRODUCT_GATED_VALUE：受控值按设计绝不对匿名出栈，是一支真实存在、非空转的负向靶）。
const OTHER_FACT = process.env.PRODUCT_OTHER_VALUE || process.env.PRODUCT_GATED_VALUE || ''
const QUESTION = process.env.PRODUCT_RECEPTION_QUESTION || '这个产品的交期大概是多久？'
const URL = `/p/${TENANT}/${PAGE}`

test.describe('R3 /p/ 托管页接待闭环（真链路）', () => {
  test.skip(!TENANT || !PAGE || !THIS_FACT, '需 PRODUCT_TENANT_CODE / PRODUCT_PAGE_CODE / PRODUCT_PUBLIC_VALUE 真夹具')

  test('可达探针（离线标志==0 → 拿到 sid）＋ 提问命中本产品事实 ＋ 不混入另一产品事实', async ({ page }) => {
    await page.goto(URL)
    // 先清本地会话三键：保证下面「拿到 sid」只可能来自本次 greet 真到达 ai-assist，
    // 而不是上一次测试遗留的 localStorage（否则可达探针会对着旧值假绿）。
    await page.evaluate(() => {
      localStorage.removeItem('ny_assist_sid')
      localStorage.removeItem('ny_assist_tok')
      localStorage.removeItem('ny_assist_msgs')
    })

    // 展开挂件触发 init/greet；等首条气泡出现（成功＝欢迎词，失败＝离线兜底，两者都会渲染气泡）。
    await page.locator('.na-fab').click()
    await expect(page.locator('.na-panel .na-bubble').first(), '挂件面板没渲染出任何气泡（链路探针没量到东西）')
      .toBeVisible({ timeout: 20000 })

    // ① 先断言离线标志元素 == 0（AGENTS §一·6 顺序）
    expect(await page.locator('.na-offline').count(),
      '链路没通：挂件落进离线兜底（.na-offline 出现）⇒ /assist-api 转发链没通，不能拿兜底空态当通过')
      .toBe(0)
    // ② 再断言拿到会话 sid（greet 真下发才有）
    const sid = await page.evaluate(() => localStorage.getItem('ny_assist_sid'))
    expect(sid, '链路没通：拿不到会话 sid ⇒ greet 没到达 ai-assist（/assist-api 没转发或返回了 SPA 兜底页）')
      .toBeTruthy()

    // 负向对照射程自证：两串都得真存在、且不同，否则下面「不许混入」是恒真空转。
    expect(OTHER_FACT, '负向对照没靶子：需 PRODUCT_OTHER_VALUE 或 PRODUCT_GATED_VALUE ⇒ 否则该腿空转').not.toBe('')
    expect(OTHER_FACT, '另一产品事实串与本产品相同 ⇒ 负向对照没判别力').not.toBe(THIS_FACT)

    // 提问，等助手回复里出现本产品的公开事实。
    await page.locator('.na-input input').fill(QUESTION)
    await page.locator('.na-send').click()
    const replies = page.locator('.na-row.ai .na-bubble')
    await expect(replies.last(), `真链路：提问没命中该产品事实「${THIS_FACT.slice(0, 16)}」⇒ 事实集没按 /p/ 作用域注入`)
      .toContainText(THIS_FACT, { timeout: 30000 })

    // 负向对照：断言里混入另一产品的事实串必须判红 —— 回复不许串进它，
    // 否则说明作用域/可见性没收敛（W1 产品事实隔离被打破）。
    const replyText = await replies.last().innerText()
    expect(replyText, `负向对照失效：回复混入了另一产品/受控事实串「${OTHER_FACT.slice(0, 16)}」⇒ 事实作用域没按 /p/ 收敛`)
      .not.toContain(OTHER_FACT)
  })
})
