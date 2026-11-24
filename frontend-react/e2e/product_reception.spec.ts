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
//
// ★ 四期收口 G3.5（2026-11-15）：把施工图 R5「320 档对外页挂件+留资表单不横向溢出」落到本文件。
//   旧形态是这条腿**根本不存在**：`e2e/pixel_uat.spec.ts` 里对整个文件 grep `.na-fab` / `/p/` / `AiAssist`
//   都是 0 命中（审计 §4.5 的实测读数），而 pixel_uat 的溢出判据只问 `documentElement`
//   ——那在「浮层撑破某个中间容器」的形态下**结构性恒真**（AGENTS §一·5 那条纪律的原话：
//   「页面整体没有横向溢出」是一句不完整的话，必须问**是哪个容器**）。
//   本文件本来就有 `/p/` 的真夹具与 `.na-fab`（AiAssist 在 PublicRoot 的 /p/ 分支里与
//   ProductPublicPage 同处一个 MarketingShell），所以判据搬到这里才有靶子。
// ==============================================================================
import { expect, test } from '@playwright/test'
// ★ R5 度量函数单一真值：运行时量测（本文件）与 vitest 判据锁
//   （src/api/assistOverflow.lock.test.ts，M0359）共用同一份实现。
import { TOL_PX, overflowProblems, type Rect } from '../src/api/assistOverflow'

// 产品租户短码由 run_uat 夹具现生成。
const TENANT = process.env.PRODUCT_TENANT_CODE || ''
// 产品页面短码由同一夹具现生成。
const PAGE = process.env.PRODUCT_PAGE_CODE || ''
// 公开事实值同时作为正向命中靶子。
const THIS_FACT = process.env.PRODUCT_PUBLIC_VALUE || ''
// 另一产品/另一类事实串：优先夹具显式给的「另一产品公开事实」，回退同一夹具的受控事实
// （PRODUCT_GATED_VALUE：受控值按设计绝不对匿名出栈，是一支真实存在、非空转的负向靶）。
const OTHER_FACT = process.env.PRODUCT_OTHER_VALUE || process.env.PRODUCT_GATED_VALUE || ''
// 默认问题可由夹具覆盖。
const QUESTION = process.env.PRODUCT_RECEPTION_QUESTION || '从下单到发出要多久？'
const EXPECT_MODEL = process.env.PRODUCT_EXPECT_MODEL || ''
// 对外页路径只由夹具短码派生。
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
    const healthBefore = await (await page.request.get('/api/health')).json()

    // 负向对照射程自证：两串都得真存在、且不同，否则下面「不许混入」是恒真空转。
    expect(OTHER_FACT, '负向对照没靶子：需 PRODUCT_OTHER_VALUE 或 PRODUCT_GATED_VALUE ⇒ 否则该腿空转').not.toBe('')
    expect(OTHER_FACT, '另一产品事实串与本产品相同 ⇒ 负向对照没判别力').not.toBe(THIS_FACT)

    // 提问，等助手回复里出现本产品的公开事实。
    await page.locator('.na-input input').fill(QUESTION)
    const chatResponse = page.waitForResponse((res) => res.url().includes('/assist-api/api/assist/chat') && res.request().method() === 'POST')
    await page.locator('.na-send').click()
    const chatBody = await (await chatResponse).json()
    if (EXPECT_MODEL) expect(chatBody.attribution?.model, '本地 assist 未跟随主服务模型热更新').toBe(EXPECT_MODEL)
    const replies = page.locator('.na-row.ai .na-bubble')
    await expect(replies.last(), `真链路：提问没命中该产品事实「${THIS_FACT.slice(0, 16)}」⇒ 事实集没按 /p/ 作用域注入`)
      .toContainText(THIS_FACT, { timeout: 30000 })
    const healthAfter = await (await page.request.get('/api/health')).json()
    expect(healthAfter.sales_facts.scope_anon, '匿名产品页作用域没有在提问时解析')
      .toBeGreaterThanOrEqual(healthBefore.sales_facts.scope_anon + 1)
    expect(healthAfter.sales_facts.injected, '本地接待链没有记录事实注入 ⇒ 只看回复文本会漏掉静默半态')
      .toBeGreaterThanOrEqual(healthBefore.sales_facts.injected + 1)

    // 负向对照：断言里混入另一产品的事实串必须判红 —— 回复不许串进它，
    // 否则说明作用域/可见性没收敛（W1 产品事实隔离被打破）。
    const replyText = await replies.last().innerText()
    expect(replyText, `负向对照失效：回复混入了另一产品/受控事实串「${OTHER_FACT.slice(0, 16)}」⇒ 事实作用域没按 /p/ 收敛`)
      .not.toContain(OTHER_FACT)
  })
})

// ============ R5：320 / 390 档对外页不横向溢出（挂件浮球 + 留资表单）============
// 判据形态照 AGENTS §一·5「按右边界点名越界后代」：
//   ① 遍历浮球与表单的**滚动祖先**逐层判 scrollWidth <= clientWidth（只问 documentElement 是空转）；
//   ② 浮球四边夹进视口；表单内每个后代按右边界点名（越界要指名到 tag.class[left,right]）。
test.describe('R5 度量函数自证（不依赖后端，永不被 skip）', () => {
  test('正常读数放行 + 越界读数必须被点名', () => {
    // 正向：320×740 档，56px 浮球贴右下（right=306 / bottom=716），祖先未撑宽 ⇒ 零问题。
    const ok = overflowProblems(
      320,
      740,
      [{ label: '.na-fab', rect: { left: 250, right: 306, top: 660, bottom: 716 } }],
      [{ label: 'html', scrollWidth: 320, clientWidth: 320 }],
    )
    expect(ok, `正常读数却被判越界 ⇒ 判据过严（${ok.join('；')}）`).toEqual([])
    // 反证①：右边界越出视口 26px（本批要抓的就是这一形态）。
    const badRight = overflowProblems(
      320,
      740,
      [{ label: '.na-fab', rect: { left: 290, right: 346, top: 660, bottom: 716 } }],
      [],
    )
    expect(badRight.length, '反证失效：right 越界却没被点名 ⇒ 本锁空转').toBeGreaterThan(0)
    // 反证②：某个滚动祖先被撑宽（「页面整体没溢出、中间容器溢出」那一族）。
    const badScroll = overflowProblems(320, 740, [], [{ label: '.lc-pp-lead', scrollWidth: 348, clientWidth: 320 }])
    expect(badScroll.length, '反证失效：容器被撑宽却没被点名 ⇒ 本锁空转').toBeGreaterThan(0)
  })
})

for (const vw of [320, 390]) {
  test.describe(`R5 ${vw} 档对外页横向溢出（挂件浮球 + 留资表单）`, () => {
    test.skip(!TENANT || !PAGE, '需 PRODUCT_TENANT_CODE / PRODUCT_PAGE_CODE 真夹具')

    test(`@${vw} 滚动祖先逐层不越界 + 浮球四边夹进视口`, async ({ page }) => {
      await page.setViewportSize({ width: vw, height: 740 })
      await page.goto(URL)

      // 靶子先在位：表单是 /p/ 页的静态件，浮球来自 AiAssist（懒加载 chunk）——
      // 两者缺一，下面的量测都是空转（AGENTS §一·5「挂件没真渲染出来时量的无溢出是空转」）。
      await expect(page.locator('[data-testid="lead-form"]'),
        '/p/ 页没渲染留资表单 ⇒ R5 靶子不在，量测将空转').toBeVisible({ timeout: 20000 })
      await page.locator('.na-fab').waitFor({ state: 'visible', timeout: 20000 })

      const m = await page.evaluate((tol: number) => {
        const fab = document.querySelector('.na-fab') as HTMLElement | null
        const form = document.querySelector('[data-testid="lead-form"]') as HTMLElement | null
        const rectOf = (el: Element | null) => {
          if (!el) return null
          const r = el.getBoundingClientRect()
          return { left: r.left, right: r.right, top: r.top, bottom: r.bottom }
        }
        const labelOf = (el: Element) =>
          el.tagName.toLowerCase() + (el.className ? '.' + String(el.className).trim().split(/\s+/)[0] : '')

        // ① 滚动祖先：从浮球与表单各自往上，逐层收 overflow 不是 visible 的那些（去重）。
        const ancestors: Array<{ label: string; scrollWidth: number; clientWidth: number }> = []
        const seen = new Set<Element>()
        for (const start of [fab, form]) {
          for (let el: Element | null = start ? start.parentElement : null; el; el = el.parentElement) {
            if (seen.has(el)) break
            seen.add(el)
            const cs = getComputedStyle(el)
            if (!/(auto|scroll|hidden)/.test(cs.overflowX) && !/(auto|scroll|hidden)/.test(cs.overflowY)) continue
            ancestors.push({
              label: el === document.documentElement ? 'html' : el === document.body ? 'body' : labelOf(el),
              scrollWidth: el.scrollWidth,
              clientWidth: el.clientWidth,
            })
          }
        }

        // ② 表单内后代按右边界点名（越界要指名到 tag.class[left,right]，别只说"有溢出"）。
        const spillers: string[] = []
        if (form) {
          for (const n of Array.from(form.querySelectorAll('*')).slice(0, 400)) {
            const r = n.getBoundingClientRect()
            if (r.width === 0 && r.height === 0) continue
            if (r.right > window.innerWidth + tol) {
              spillers.push(`${labelOf(n)}[${Math.round(r.left)},${Math.round(r.right)}]`)
            }
          }
        }

        return {
          vw: window.innerWidth,
          vh: window.innerHeight,
          fab: rectOf(fab),
          form: rectOf(form),
          ancestors,
          spillers,
          docOver: document.documentElement.scrollWidth - document.documentElement.clientWidth,
        }
      }, TOL_PX)

      expect(m.fab, '浮球量不到边界（本档判据会空转）').not.toBeNull()
      expect(m.form, '留资表单量不到边界（本档判据会空转）').not.toBeNull()

      const problems = overflowProblems(
        m.vw,
        m.vh,
        [
          { label: `.na-fab`, rect: m.fab as Rect },
          { label: '[data-testid=lead-form]', rect: m.form as Rect },
        ],
        m.ancestors,
      )
      expect(problems, `${vw} 档横向溢出（逐层滚动祖先 + 四边夹边）：${problems.join('；')}`).toEqual([])
      expect(m.spillers, `${vw} 档留资表单内有后代越出视口右边界（指名）：${m.spillers.join('；')}`).toEqual([])
      // 文档级读数只作**辅助**（它单独恒真，不当唯一判据，仅用来在红时区分"整体"还是"局部"）。
      expect(m.docOver, `${vw} 档文档级横向可滚动 ${m.docOver}px`).toBeLessThanOrEqual(TOL_PX)
    })
  })
}
