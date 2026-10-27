// ============================================================================
// e2e/captcha_provider.spec.ts — 「后台换提供方，前端真的换家」真浏览器判据（2026-10-27 多提供方批）
//
// 为什么必须有这一层：单测能证明 lib/captcha 的 `parseCaptchaProvider` 判得对，
//   锁能证明脚本源只写在 lib 一处——但**没有任何一层**能证明"注册配置里那个字段
//   真把挂件换成了另一家"。而这条链的失效形态正是静默的：
//   字段读了没用上 ⇒ 后台把 captcha_provider 切成 geetest，前端仍去取 CF 那支脚本，
//   CF 在国内不通 ⇒ 又回到 D-02 那个"框永远不出现"，而且这次连提示都不会有（状态还是 ready）。
//
// 两条用例互为对照（缺任一条都证明不了"是字段在起作用"）：
//   P1 `captcha_provider: 'geetest'` ⇒ 只拨极验脚本、容器里挂出极验的框、init 收对象带 captchaId；
//   P2 不给该字段（＝缺省/老后端）⇒ 只拨 CF 脚本（口径①：未知按 turnstile）。
// 两家第三方都由 page.route 本地桩提供，不写任何外部域名做真实请求（AGENTS §一·6 e2e 红线）。
// ============================================================================
import { test, expect, Page, Route } from '@playwright/test'

const GT_ID = 'uat-geetest-id'
const TS_KEY = '0x4AAAA-uat-stub-key'

// GT_STUB 极验替身脚本：只认协议（第一个参数必须是含 captchaId 的对象），
// 把收到的配置与实例都挂到 window 上供断言读，appendTo 时往宿主里塞一个带标记的框。
const GT_STUB = `window.initGeetest4=function(opts,cb){` +
  `window.__gtOpts=opts;` +
  `var box=document.createElement('div');box.setAttribute('data-gt-box','1');` +
  `var gt={appendTo:function(el){var e=(typeof el==='string')?document.querySelector(el):el;e&&e.appendChild(box);},` +
  `getValidate:function(){return {lot_number:'l',captcha_output:'o',pass_token:'p',gen_time:'g'};},` +
  `reset:function(){},showCaptcha:function(){},` +
  `onReady:function(f){f();},onSuccess:function(f){window.__gtFire=f;}};` +
  `window.__gt=gt;cb(gt);};`

// TS_STUB CF 替身脚本：塞一个 300px iframe，够判"挂件真渲染出来了"
const TS_STUB = `window.turnstile={render:function(el,o){var f=document.createElement('iframe');` +
  `f.style.width='300px';f.style.height='65px';f.setAttribute('data-ts-size',(o&&o.size)||'normal');` +
  `el.appendChild(f);return 'uat-widget';},reset:function(){},execute:function(){},remove:function(){}};`

// mockConfig 只开一件事：人机验证开＋站点钥匙（provider 由各用例决定给不给）。
async function mockConfig(page: Page, provider?: string) {
  await page.route('**/api/auth/register-config', (r: Route) => r.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({
      success: true,
      captcha_enabled: true,
      captcha_site_key: provider === 'geetest' ? GT_ID : TS_KEY,
      ...(provider ? { captcha_provider: provider } : {}),
    }),
  }))
}

// trackDials 数两家脚本各被拨了几次（判据靠它，看界面元素会被"两边都长得像"骗过去）
function trackDials(page: Page) {
  const hits = { gt: 0, ts: 0 }
  page.on('request', (req) => {
    const u = req.url()
    if (u.startsWith('https://static.geetest.com/')) hits.gt += 1
    if (u.startsWith('https://challenges.cloudflare.com/')) hits.ts += 1
  })
  return hits
}

test.describe('人机验证提供方切换', () => {
  test('P1 captcha_provider=geetest ⇒ 只拨极验脚本，容器里挂出极验的框', async ({ page }) => {
    await mockConfig(page, 'geetest')
    const hits = trackDials(page)
    await page.route('**/static.geetest.com/**', (r: Route) => r.fulfill({
      status: 200, contentType: 'application/javascript', body: GT_STUB,
    }))
    // 反向对照：若前端没换家，这里会真的去拨 CF —— 也拦掉，免得用例因外部网络抖动红
    await page.route('**/challenges.cloudflare.com/**', (r: Route) => r.fulfill({
      status: 200, contentType: 'application/javascript', body: TS_STUB,
    }))

    await page.goto('/login', { waitUntil: 'domcontentloaded' })

    // ① 极验的框真的挂进了登录那一格容器（不是"脚本到了但没人渲染"）
    await expect(page.locator('#__ts_login_widget__ [data-gt-box]')).toHaveCount(1, { timeout: 20000 })
    // ② 只拨了极验这一家
    expect(hits.gt, '极验脚本一次都没拨 ⇒ provider 字段没被用上').toBeGreaterThan(0)
    expect(hits.ts, '仍然拨了 CF 脚本 ⇒ 前端没换家（后台怎么配都等于没配）').toBe(0)
    // ③ 调用契约：init 收的是对象，captchaId 就是配置下发的那一个
    const opts = await page.evaluate(() => (window as unknown as { __gtOpts?: { captchaId?: string; product?: string } }).__gtOpts)
    expect(opts?.captchaId).toBe(GT_ID)
    expect(opts?.product).toBe('popup')
  })

  test('P2 不给 captcha_provider（缺省/老后端）⇒ 仍走 CF（口径①：未知按 turnstile）', async ({ page }) => {
    await mockConfig(page)
    const hits = trackDials(page)
    await page.route('**/challenges.cloudflare.com/**', (r: Route) => r.fulfill({
      status: 200, contentType: 'application/javascript', body: TS_STUB,
    }))
    await page.route('**/static.geetest.com/**', (r: Route) => r.fulfill({
      status: 200, contentType: 'application/javascript', body: GT_STUB,
    }))

    await page.goto('/login', { waitUntil: 'domcontentloaded' })

    await expect(page.locator('#__ts_login_widget__ iframe')).toHaveCount(1, { timeout: 20000 })
    expect(hits.ts, '缺省档没走 CF').toBeGreaterThan(0)
    expect(hits.gt, '缺省档不该拨极验').toBe(0)
  })
})
