// ============================================================================
// e2e/turnstile_load_state.spec.ts — D-02「装载慢/不可达时的界面形态」真浏览器判据（2026-10-27 批）
//
// 为什么必须有这一层：静态锁看得见"代码形状"，看不见"面板到底盖不盖"
// （AGENTS §一·5 批⑤ 那条同一口径：这类只在真浏览器里现形）。
//
// 三条用例各钉一个失败模式：
//   T1 **慢不等于坏**：CF 脚本 18s 才到 ⇒ 12s 那一刻界面必须有脸（不许静默空壳），
//      而脚本真到达后挂件必须**追回来**并**撤掉提示**（撤腿失效＝用户界面盖着一句"加载不出来"）。
//      —— 这条就是本机实测抓到的形态（同一跳 +13.998s 通、+14.987s API 出现）。
//   T2 **确定不可达要给出路**：一律 abort ⇒ 界面出「重试」钮；点它必须**真的再拨一次**
//      （只改文案不重拨＝出路是假的）。同时钉住守卫仍然拦着：不许放一个裸 token 的发码请求出网。
//   T3 **320 档不顶破页面**：挂件真渲染出来后再量（reduced 终态那种"没有 iframe 的空容器"当靶子是空转，
//      AGENTS P2f 那条演示范式陷阱），且必须**遍历滚动祖先**逐层判，不只问 documentElement。
//
// 不碰真实 Cloudflare：三个形态都由 page.route 本地桩提供；不写任何外部域名（e2e 红线）。
// ============================================================================
import { test, expect, Page, Route } from '@playwright/test'

const SITE_KEY = '0x4AAAA-uat-stub-key'
const CF_API = 'https://challenges.cloudflare.com/turnstile/v0/api.js'

// stubApiJs 是替身脚本正文：按 size 档塞不同宽度的 iframe，并把档位写进 data 属性供判据读。
const STUB_API_JS = `window.turnstile={render:function(el,o){var f=document.createElement('iframe');` +
  `f.style.width=(o&&o.size==='compact')?'150px':'300px';f.style.height='65px';f.style.border='0';` +
  `f.setAttribute('data-ts-size',(o&&o.size)||'normal');el.appendChild(f);return 'uat-widget';},` +
  `reset:function(){},execute:function(){},remove:function(){}};`

// mockConfig 只开一件事：人机验证开＋给出站点钥匙。其余接口一律不在判据射程内。
async function mockConfig(page: Page) {
  await page.route('**/api/auth/register-config', (r: Route) => r.fulfill({
    status: 200, contentType: 'application/json',
    body: JSON.stringify({ success: true, captcha_enabled: true, captcha_site_key: SITE_KEY }),
  }))
}

// dialCount 数"这一跳被拨了几次"（重试腿的判据就靠它，看文案会被空改动骗过去）
function trackDials(page: Page) {
  const state = { n: 0 }
  page.on('request', (req) => { if (req.url().startsWith(CF_API)) state.n += 1 })
  return state
}

test.describe('D-02 Turnstile 装载形态', () => {
  test('T1 慢链（18s 才通）：先出声、到后追回来并撤掉提示', async ({ page }) => {
    test.setTimeout(90000)
    await mockConfig(page)
    const dials = trackDials(page)
    await page.route('**/challenges.cloudflare.com/**', async (r) => {
      await new Promise((res) => setTimeout(res, 18000)) // 钉的就是"比默认窗口慢"这一档
      await r.fulfill({ status: 200, contentType: 'application/javascript', body: STUB_API_JS })
    })
    await page.goto('/login', { waitUntil: 'domcontentloaded' })

    // ① 出声：装载窗口（默认 12s）内是 loading，过点是 unavailable——两种都比"什么都没有"强
    await expect.poll(async () => page.locator('[data-testid="captcha-notice"]').count(),
      { message: '近 8s 内界面必须出现装载提示（旧形态这里静默空壳）', timeout: 8000, intervals: [500] }).toBeGreaterThan(0)
    expect(await page.locator('#__ts_login_widget__ iframe').count(), '脚本还没到，不该有挂件').toBe(0)

    // ② 追回：脚本到达后挂件必须真挂上（onReady 那一条腿在放弃之后仍然接得住）
    await expect.poll(async () => page.locator('#__ts_login_widget__ iframe').count(),
      { message: '18s 那一次到达后挂件必须挂载 ⇒ 装载器的 load 事件腿没接住', timeout: 40000 }).toBeGreaterThan(0)

    // ③ 撤腿：界面不许继续盖着"加载不出来"（AGENTS 批⑤ 第八批：哨兵产物必须有"好了就撤"那一腿）
    await expect(page.locator('[data-testid="captcha-notice"]'), '挂件已就位，提示必须撤掉').toHaveCount(0, { timeout: 10000 })
    expect(dials.n, '这一跳只该拨一次（重试是用户点出来的，不是自动狂重拨）').toBe(1)
  })

  test('T2 确定不可达：出「重试」钮，点它必须真再拨一次，且守卫仍然拦着裸请求', async ({ page }) => {
    await mockConfig(page)
    const dials = trackDials(page)
    let codesDialed = 0
    page.on('request', (req) => { if (req.url().includes('/api/email/code')) codesDialed += 1 })
    await page.route('**/challenges.cloudflare.com/**', (r) => r.abort('failed'))
    await page.goto('/login', { waitUntil: 'domcontentloaded' })

    // 被拒当场就该出声（error 事件腿），不必白等满 12s
    await expect(page.getByTestId('captcha-retry'), '不可达必须给「重试」出路').toBeVisible({ timeout: 20000 })
    expect(dials.n).toBe(1)

    await page.getByTestId('captcha-retry').click()
    await expect.poll(() => dials.n, { message: '点重试后必须重新发起这一跳', timeout: 10000 }).toBe(2)

    // 同屏钉住另一头：出路给了，但**没有 token 就不许把发码请求放出去**（放行会换来后端 403，
    // 更坏的是把"人机验证"变成一句提示；两条腿必须同时成立）
    await page.locator('input[type="email"]').first().fill('uat-nobody@example.com')
    await page.getByRole('button', { name: /发送验证码/ }).click()
    await page.waitForTimeout(1500)
    expect(codesDialed, '没有 token 时请求根本不该出网').toBe(0)
  })

  test('T3 320 档：挂件按容器选 compact，渲染出来后页面与各级滚动祖先都不越界', async ({ page }) => {
    await mockConfig(page)
    // 挂件立即可用（同步替身）：判据要的是"真渲染出来之后"的形状，不是空容器
    await page.addInitScript(() => {
      (window as unknown as { turnstile?: unknown }).turnstile = {
        render: (el: HTMLElement, o?: { size?: string }) => {
          const f = document.createElement('iframe')
          f.style.width = o?.size === 'compact' ? '150px' : '300px'
          f.style.height = '65px'
          f.setAttribute('data-ts-size', o?.size || 'normal')
          el.appendChild(f)
          return 'uat-widget'
        },
        reset: () => {}, execute: () => {}, remove: () => {},
      }
    })
    await page.setViewportSize({ width: 320, height: 640 })
    await page.goto('/login', { waitUntil: 'domcontentloaded' })
    await expect(page.locator('#__ts_login_widget__ iframe')).toBeAttached({ timeout: 20000 })

    const m = await page.evaluate(() => {
      const de = document.documentElement
      const box = document.querySelector('#__ts_login_widget__') as HTMLElement | null
      const iframe = document.querySelector('#__ts_login_widget__ iframe') as HTMLElement | null
      // 逐层量滚动祖先：只问 documentElement 是一句不完整的话（AGENTS §一·5 那条浮层判据同族）
      const ancestors: Array<{ cls: string; sw: number; cw: number }> = []
      let n: HTMLElement | null = box
      while (n && ancestors.length < 8) {
        ancestors.push({ cls: `${n.tagName}.${(n.className || '').toString().slice(0, 30)}`, sw: n.scrollWidth, cw: n.clientWidth })
        n = n.parentElement
      }
      return {
        scrollWidth: de.scrollWidth, innerWidth: window.innerWidth,
        size: iframe?.getAttribute('data-ts-size') || null,
        boxRight: box ? Math.round(box.getBoundingClientRect().right * 10) / 10 : null,
        iframeWidth: iframe ? Math.round(iframe.getBoundingClientRect().width) : null,
        ancestors,
      }
    })
    expect(m.size, '320 档容器放不下 300px ⇒ 必须选 compact').toBe('compact')
    // 判据问的是"选到的档位有没有真的落到渲染入参"，所以**上界**才是判据：
    // compact 声明 150px，容器更窄时浏览器还会再收（实测 146）——把 150 当等值锁是自伤。
    expect(m.iframeWidth as number).toBeGreaterThan(0)
    expect(m.iframeWidth as number).toBeLessThanOrEqual(151)
    expect(m.scrollWidth, `页面级横向溢出（${m.scrollWidth}>${m.innerWidth}）`).toBeLessThanOrEqual(320)
    expect(m.boxRight as number).toBeLessThanOrEqual(320.5)
    for (const a of m.ancestors) {
      expect(a.sw, `容器 ${a.cls} 被挂件顶出横向滚动`).toBeLessThanOrEqual(a.cw + 1)
    }
  })

  // 第二条**可达面**：2026-10-22 统一登录批之后 `/register` 落的就是同一张「邮箱+验证码」卡，
  // 挂件容器是 #__ts_login_widget__（不是传统多字段注册表单那支的 #__ts_widget__）。
  // 那条旧容器与 AI 面板容器（.ar-ts）在本档不可达（要先把多字段表单/AI 问答走一遍），
  // 它们的宽度兜底与选档由 LeadForm.turnstileSize.lock.test.ts 的源码级锁按**三处全集**判
  // （CSS 那条声明把两个 id 一起收了；只钉可达档＝另一档放假条，所以这里补可达面、静态锁兜全集）。
  test('T4 320 档注册路由：可达那张卡的容器同样选 compact 且不顶破页面', async ({ page }) => {
    await mockConfig(page)
    await page.addInitScript(() => {
      (window as unknown as { turnstile?: unknown }).turnstile = {
        render: (el: HTMLElement, o?: { size?: string }) => {
          const f = document.createElement('iframe')
          f.style.width = o?.size === 'compact' ? '150px' : '300px'
          f.style.height = '65px'
          f.setAttribute('data-ts-size', o?.size || 'normal')
          el.appendChild(f)
          return 'uat-widget'
        },
        reset: () => {}, execute: () => {}, remove: () => {},
      }
    })
    await page.emulateMedia({ reducedMotion: 'reduce' })
    await page.setViewportSize({ width: 320, height: 640 })
    await page.goto('/register', { waitUntil: 'domcontentloaded' })
    await expect(page.locator('#__ts_login_widget__ iframe')).toBeAttached({ timeout: 20000 })
    const m = await page.evaluate(() => {
      const box = document.querySelector('#__ts_login_widget__') as HTMLElement | null
      const iframe = document.querySelector('#__ts_login_widget__ iframe') as HTMLElement | null
      return {
        scrollWidth: document.documentElement.scrollWidth,
        size: iframe?.getAttribute('data-ts-size') || null,
        right: box ? Math.round(box.getBoundingClientRect().right * 10) / 10 : null,
        w: iframe ? Math.round(iframe.getBoundingClientRect().width) : null,
      }
    })
    expect(m.size, '注册路由那张卡同样放不下 300px ⇒ 必须 compact').toBe('compact')
    expect(m.w as number).toBeLessThanOrEqual(151)
    expect(m.scrollWidth).toBeLessThanOrEqual(320)
    expect(m.right as number).toBeLessThanOrEqual(320.5)
  })
})
