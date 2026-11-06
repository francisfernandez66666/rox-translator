// ============ e2e/product_public.spec.ts · 职责说明 ============
// 一期对外公开页 /p/{tenantCode}/{pageCode}（施工_一期 §5.7／§5.6）的**真浏览器**运行时锁。
//
// 三条硬口径（对应施工图 §5.7 与 §9.4）：
//  ① 移动档必须**显式设 viewport**：默认 project 只有 Desktop Chrome 1280×720，
//     不显式设就等于没测移动档（「测了」是假的）。这里按 390×844 单独给；
//  ② 判溢出用 `spillersIn()` 形态（**逐后代比右边界**：rect.right > box.right），
//     不许只判 documentElement（结构性恒真，AGENTS P7 那条）；
//  ③ 链路型断言自带**可达探针**：先直连产品页数据接口取真值，取不到／not_found
//     就点名「链路没通」，绝不放行兜底空态（否则离线兜底一路绿灯）。
//
// 匿名／登录两态各打同一组两段短码（§5.6 V4 靶子在对外页再验一次）：
//  匿名态 ＝ gated 字段值字面量不出栈 + 替身文案 + 真实留资出口在；
//  登录态 ＝ 受控值在**登录态那个口**（/api/product/fact/list）读得到（正向对照，否则负向恒真是空转）。
//
// ★ 两面各用各的口（本轮收的契约裂缝）：`/p/...` 是**给人和爬虫看的 HTML 面**（后端注入
//   title／JSON-LD／无 JS 首屏，见 backend-go/internal/api/product_ssr.go），
//   `/api/product/public` 才是**数据口**；对返回 HTML 的路径要 JSON，
//   页面只会永远显示"未找到该产品页"，而 vitest 全量 mock 掉 api 层看不见这一层。
// ★ 受控值负向锁的**靶子由夹具交给判据**（PRODUCT_GATED_VALUE）：旧写法从匿名出栈的 facts 里
//   filter `visibility==='gated'` 来"发现"受控值，而那一个口按设计根本不回受控行
//   ⇒ gated 数组恒空、负向锁恒真＝空转（AGENTS §三「输入不在射程 ⇒ 判据恒真」的前端第三形态）。
//
// 短码与值一律由环境提供（PRODUCT_TENANT_CODE／PRODUCT_PAGE_CODE／PRODUCT_ID／
// PRODUCT_PUBLIC_VALUE／PRODUCT_GATED_VALUE），**不写死、不猜**：两段短码由后端 crypto/rand
// 签发，猜一个号正是本设计要堵的口子（夹具见 scripts/uat/run_uat.sh 的「5.9 产品对外页 e2e 夹具」段）。
// ⚠️ 本文件需要真后端 + 真短码 + 已构建 dist 才能跑；未纳入 vitest，也不在 tsc 的 include 内。
// ==============================================================
import { expect, test } from '@playwright/test'

/** 移动档显式视口（不依赖默认 project 的 Desktop Chrome 1280×720） */
const MOBILE = { width: 390, height: 844 }
const TENANT = process.env.PRODUCT_TENANT_CODE || ''
const PAGE = process.env.PRODUCT_PAGE_CODE || ''
const PRODUCT_ID = process.env.PRODUCT_ID || ''
const PUBLIC_VALUE = process.env.PRODUCT_PUBLIC_VALUE || ''
const GATED_VALUE = process.env.PRODUCT_GATED_VALUE || ''
const AUTH_STATE = process.env.PRODUCT_AUTH_STATE || ''
const URL = `/p/${TENANT}/${PAGE}`
/** 数据口（与 HTML 面分开；短码走 query，由 productCodePair 读） */
const DATA_URL = `/api/product/public?tenant_code=${encodeURIComponent(TENANT)}&page_code=${encodeURIComponent(PAGE)}`

/** 逐后代比**右边界**：与 e2e/ticket_delivery.spec.ts 的 spillersIn 同形（溢出＝右边界越出容器右边界）。
 *  ★ 判据刻意只问右，不问左：本仓有两件"天生负左坐标"的合法形态——① 蜜罐输入框必须**位移隐藏**
 *     （`left:-9999`；`display:none` 会让 bot 也填不到＝蜜罐失效，AGENTS §20 与 §25 都钉过），
 *     ② 收起态的移动抽屉在画布外（`translateX(-100%)`）。加一条左边界判据＝把这两件设计判成缺陷，
 *     本轮实测就红在蜜罐那一枚上（`INPUT left=-9999`）。左溢出真要抓，抓的是"页面出现横向滚动条"，
 *     那由 pixel_uat 的滚动祖先腿负责，不在这里重复一把尺子。
 *  靶子是对外页自己的 `<main>`（ProductPublicPage 确实渲染它）；卖家面板那些页由 FrontPage 出、
 *  **不渲染 main**，所以货架那条用例按 data-testid 取容器，不照抄这里。 */
async function spillersIn(page: import('@playwright/test').Page): Promise<string[]> {
  return page.evaluate(() => {
    const box = document.querySelector('main') as HTMLElement | null
    if (!box) return ['对外页 <main> 不存在（可达探针失败：页面没真渲染）']
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
  })
}

test.describe('对外公开页 /p/{tenantCode}/{pageCode}（§5.7）', () => {
  test.skip(!TENANT || !PAGE, '需 PRODUCT_TENANT_CODE / PRODUCT_PAGE_CODE 两段真实短码')

  test.describe('匿名态（移动档 390×844）', () => {
    test.use({ viewport: MOBILE })

    test('可达探针 + 受控字段不出栈 + 留资出口在 + 逐后代无横向溢出', async ({ page, request }) => {
      // ① 数据口可达探针：拿不到真数据就点名，绝不放行兜底态
      const probe = await request.get(DATA_URL)
      expect(probe.ok(), '链路没通：/api/product/public 不可达（不能拿兜底空态当通过）').toBeTruthy()
      const body = await probe.json().catch(() => null) as {
        not_found?: boolean
        product?: { name?: string }
        facts?: Array<{ key?: string; text?: string }>
        gated_count?: number
      } | null
      expect(body && body.not_found !== true, '链路探针：两段短码解不出产品（not-found 空态不能当通过）').toBeTruthy()
      const name = body?.product?.name || ''
      expect(name, '夹具产品名为空：下面的判据会退化成"任何页面都算过"').not.toBe('')

      // ② HTML 面：产品名必须在**未执行 JS 的原始字节**里（§5.8 首屏静态 HTML 的最后一里）
      const rawResp = await request.get(URL)
      expect(rawResp.status(), '直出 HTML 不是 200 ⇒ 短码解析或路由白名单断了一环').toBe(200)
      const raw = await rawResp.text()
      expect(raw, '直出 HTML 不含产品名 ⇒ serveIndexHTML 上的产品元数据注入腿没挂上').toContain(name)
      if (GATED_VALUE) expect(raw, '受控值泄漏到直出 HTML（title／meta／JSON-LD／首屏四处之一）').not.toContain(GATED_VALUE)

      await page.goto(URL)
      // 正向对照：真实留资出口在（匿名面唯一可行动出口）
      await expect(page.getByTestId('lead-form')).toBeVisible()

      const html = await page.content()
      // ③ 公开值真出现在 DOM —— 负向锁的对照；缺它则"受控不出栈"恒真＝空转
      if (PUBLIC_VALUE) expect(html, '正向对照失效：公开事实值一个都没出现').toContain(PUBLIC_VALUE)
      // ④ 受控值负向锁（靶子来自夹具，不来自公开口的"自我审查"）
      if (GATED_VALUE) expect(html, `受控值泄漏到匿名 DOM：${GATED_VALUE.slice(0, 20)}`).not.toContain(GATED_VALUE)
      else expect(body?.gated_count ?? 0, 'PRODUCT_GATED_VALUE 没给，夹具里也没有受控行 ⇒ 这一段的负向锁没有靶子').toBeGreaterThan(0)
      // 替身文案：有受控行才出，出的是文案不是值
      if ((body?.gated_count ?? 0) > 0) await expect(page.getByTestId('gated-tip')).toBeVisible()

      // ② 逐后代比右边界（不是只判 documentElement）
      const spill = await spillersIn(page)
      expect(spill, `移动档逐后代横向溢出：${JSON.stringify(spill)}`).toEqual([])

      // ⑥ 公开面负向锁（§8.7）：判**可见文本**，与词典禁词闸同口径（raw HTML 里本就带脚本 URL）
      const visible = await page.locator('main').innerText()
      expect(visible).not.toMatch(/24917|33222|400\s*每积分/)
      expect(visible.toLowerCase()).not.toContain('token')
    })
  })

  test.describe('登录态正向对照（受控行在 fact/list 读得到）', () => {
    const TOKEN = process.env.PRODUCT_TOKEN || ''
    test.skip(!AUTH_STATE || !PRODUCT_ID || !TOKEN, '需 PRODUCT_AUTH_STATE ＋ PRODUCT_ID ＋ PRODUCT_TOKEN 才能打登录态正向对照')
    test.use({ viewport: MOBILE, storageState: AUTH_STATE })

    test('卖家看得到受控值，同一对外页仍然看不到', async ({ page, request }) => {
      // ★ 接口腿必须显式带 Bearer：Playwright 的 APIRequestContext 只带 cookie，
      //   本仓登录态住在 sessionStorage（core.ts）里 ⇒ storageState 对 request.get 完全无效，
      //   不带这个头会得到 401 并被读成"链路没通"（判据没错，是凭证通道拿错了）。
      const owner = await request.get(`/api/product/fact/list?product_id=${PRODUCT_ID}`, {
        headers: { Authorization: `Bearer ${TOKEN}` },
      })
      expect(owner.ok(), '登录态链路没通：/api/product/fact/list 不可达（401 ⇒ 凭证通道没接对）').toBeTruthy()
      const ownerTxt = await owner.text()
      if (GATED_VALUE) {
        expect(ownerTxt, '正向对照失效：卖家自己看不到受控值 ⇒ 匿名态"不出栈"没什么可证的').toContain(GATED_VALUE)
      }
      await page.goto(URL)
      await expect(page.locator('main h1')).toBeVisible()
      const html = await page.content()
      if (GATED_VALUE) expect(html, '受控值不许出现在对外页 DOM（即便访客是登录着的卖家）').not.toContain(GATED_VALUE)
    })
  })
})
