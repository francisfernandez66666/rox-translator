// 现网冒烟：演示站/租户站登录面与主站对齐（★ 2026-10-23）
// 判据：① 「输入邮箱，获取一次性验证码…」那段说明在三站一律消失；
//       ② 邮箱/邮箱验证码两枚字段都有可见标签；
//       ③ 邮箱框 placeholder 是自动建号提示（auth.autoCreateHint）。
// 只读页面 DOM，不登录、不改数据。
import { chromium } from 'playwright'

const SENT = '输入邮箱，获取一次性验证码即可登录；未注册的邮箱会自动创建空白账号。'
const CASES = [
  { url: 'https://rox-test.lexicorn.cn/login', name: '演示站（租户站同源）' },
  { url: 'https://langcross.lexicorn.cn/login', name: '主站' },
]

const browser = await chromium.launch()
const ctx = await browser.newContext({ locale: 'zh-CN', viewport: { width: 1440, height: 900 } })
const out = []
for (const c of CASES) {
  const p = await ctx.newPage()
  await p.goto(c.url, { waitUntil: 'commit', timeout: 90000 })
  // 'commit' 之下 document.body 可能还是 null（文档未解析完），先等 body 再等输入框出现
  await p.waitForSelector('body', { timeout: 60000 })
  await p.waitForSelector('input', { timeout: 60000 }).catch(() => {})
  await p.waitForTimeout(3000)
  const r = await p.evaluate((sent) => {
    const card = document.querySelector('.lc-auth-card') || document.body
    const labels = [...card.querySelectorAll('.lc-field__label')].map((e) => e.textContent.trim())
    const inputs = [...card.querySelectorAll('input')].map((i) => ({
      ph: i.getAttribute('placeholder'),
      al: i.getAttribute('aria-label'),
    }))
    return {
      sentenceGone: !(card.textContent || '').includes(sent),
      labels,
      inputs,
      hasSelfRegister: (card.textContent || '').includes('自助注册'),
    }
  }, SENT)
  out.push({ name: c.name, url: c.url, ...r })
  await p.close()
}
await browser.close()
console.log(JSON.stringify(out, null, 2))
const ok = out.every((o) =>
  o.sentenceGone &&
  o.labels.includes('邮箱') &&
  o.labels.includes('邮箱验证码') &&
  o.inputs.some((i) => i.ph === '未注册的邮箱会自动创建空白账号'))
console.log(ok ? 'LOGIN_ALIGN_ALL_OK=1' : 'LOGIN_ALIGN_ALL_OK=0')
process.exit(ok ? 0 : 1)
