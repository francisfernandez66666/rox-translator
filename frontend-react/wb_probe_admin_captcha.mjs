// wb_probe_admin_captcha.mjs — 现网探针 v3（修复后复验）：主站 /login 与 /admin 的人机验证实况
// 修复（2026-10-23c）：Login.tsx 的 registerConfig 拉取与登录屏挂件挂载两处 effect 从
// mode==='home' 放宽到 home|admin。修复后 /admin 应当：拉得到配置、渲染挂件容器、
// 注入 challenges.cloudflare.com api.js、window.turnstile 就绪。
import { chromium } from 'playwright'

const BASE = 'https://langcross.lexicorn.cn'
const PAGES = [
  { name: 'login_home', url: `${BASE}/login` },
  { name: 'admin', url: `${BASE}/admin` },
]

async function openPage(browser, url) {
  let lastErr = null
  for (let i = 0; i < 3; i++) {
    const ctx = await browser.newContext({ locale: 'zh-CN' })
    const page = await ctx.newPage()
    const cfReqs = []
    page.on('response', (r) => { if (r.url().includes('challenges.cloudflare.com')) cfReqs.push(`${r.status()} ${r.url().slice(0, 110)}`) })
    try {
      await page.goto(url, { waitUntil: 'domcontentloaded', timeout: 60000 })
      await page.waitForSelector('.lc-auth-card', { timeout: 30000 })
      return { ctx, page, cfReqs }
    } catch (e) { lastErr = e; await ctx.close() }
  }
  throw lastErr
}

const browser = await chromium.launch({ headless: true })
const out = {}
for (const p of PAGES) {
  try {
    const { ctx, page, cfReqs } = await openPage(browser, p.url)
    const errors = []
    page.on('pageerror', (e) => errors.push('pageerror: ' + e.message))
    // 配置拉取是异步的：等挂件容器出现（captchaOn 置真后才会渲染），再留余量给 api.js
    await page.waitForSelector('#__ts_login_widget__', { timeout: 15000 }).catch(() => {})
    await page.waitForTimeout(6000)
    const r = await page.evaluate(() => {
      const box = document.querySelector('#__ts_login_widget__')
      return {
        url: location.href,
        hasTsApi: typeof window.turnstile !== 'undefined',
        widgetBoxExists: !!box,
        widgetIframes: box ? box.querySelectorAll('iframe').length : -1,
        anyTsIframe: document.querySelectorAll('iframe[src*="challenges.cloudflare.com"]').length,
        cardFields: [...document.querySelectorAll('.lc-auth-card input')].map((i) => i.getAttribute('placeholder')).filter(Boolean),
      }
    })
    out[p.name] = { ...r, cfReqs: cfReqs.slice(0, 6), errors: errors.slice(0, 3) }
    await ctx.close()
  } catch (e) {
    out[p.name] = { fatal: String(e).slice(0, 160) }
  }
}
await browser.close()
console.log(JSON.stringify(out, null, 2))
const ok = (n) => out[n]?.widgetBoxExists === true && out[n]?.hasTsApi === true && (out[n]?.cfReqs?.length || 0) > 0
console.log(`PROBE_VERDICT login_widget=${ok('login_home') ? 'OK' : 'MISSING'} admin_widget=${ok('admin') ? 'OK' : 'MISSING'}`)
process.exit(ok('login_home') && ok('admin') ? 0 : 1)
