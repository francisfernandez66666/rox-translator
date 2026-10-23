// 现网浏览器冒烟：租户定制页 / 必须直落 /login（不再展示平台营销落地页），
// 主站 / 仍展示落地页。判据只看「最终 pathname」与「登录面关键元素」，不看像素。
import { chromium } from 'playwright'

const CASES = [
  { url: 'https://rox-test.lexicorn.cn/', want: '/login', name: '租户定制页（演示站）' },
  { url: 'https://langcross.lexicorn.cn/', want: '/', name: '主站根域名' },
]

const browser = await chromium.launch()
const ctx = await browser.newContext({ locale: 'zh-CN', viewport: { width: 1440, height: 900 } })
const out = []
for (const c of CASES) {
  const page = await ctx.newPage()
  const errs = []
  page.on('pageerror', e => errs.push(String(e).slice(0, 120)))
  let path = '<nav-failed>'
  try {
    await page.goto(c.url, { waitUntil: 'domcontentloaded', timeout: 45000 })
    await page.waitForTimeout(2500)
    path = new URL(page.url()).pathname
  } catch (e) {
    errs.push('nav: ' + String(e).slice(0, 120))
  }
  const hasLoginForm = await page.locator('input[type="password"], input[placeholder]').count().catch(() => 0)
  const ok = path === c.want
  out.push({ name: c.name, url: c.url, finalPath: path, want: c.want, ok, inputs: hasLoginForm, errs })
  await page.close()
}
await browser.close()
console.log(JSON.stringify(out, null, 2))
const allOk = out.every(o => o.ok)
console.log(allOk ? 'TENANT_LANDING_SMOKE_ALL_OK=1' : 'TENANT_LANDING_SMOKE_ALL_OK=0')
process.exit(allOk ? 0 : 1)
