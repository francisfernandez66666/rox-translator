// ============ e2e/trial_public.spec.ts · 职责说明 ============
// 〇-Z「免登录即时翻译试用」的**运行时链路**锁（★ 2026-09-28 #74/#75）：
//   未登录访客在首页点演示卡 → 就地出试用面板 → 输入一句 → 拿到真译文 → 界面显示「剩 4 句」，
//   全程地址栏停在 `/`、一个登录框都没出现过。
//
// 为什么 jsdom 那 15 个用例（TrialPanel.dom.test.tsx 10 例 ＋ Landing.trial.dom.test.tsx 7 例）不算够：
//   它们喂的全是**假回包**——「前端按 code 分支」那一层测得很细，但
//   ① 路由有没有真的注册进公网那张表、匿名请求会不会被鉴权 middleware 拦在门口，
//   ② 语种下拉是不是真的从 /api/translation/langs 拉到了名单（假名单永远非空，测不出接线断），
//   ③ 走过 vite build 与 spa.go 之后的**这份 dist** 上，点热区是否还切得动卡，
//   这三条只有真浏览器 + 真后端能回答。后端账目（配额、成本归租户 0）另有 T66 数库里行，两层不重复。
//
// ★ 链路型用例按 AGENTS §6 自带可达探针：先用一条**不消耗配额**的非法请求（空文本 → 400
//   VALIDATION_ERROR）证明 /api/trial/translate 这条路是通的，再进界面点。
//   少了这道探针，链路断掉时面板会显示「暂时联系不上」的红色兜底态，而"界面有字"式断言会一路绿灯。
// =============================================
import { expect, test } from '@playwright/test'

const BASE = process.env.BASE_URL || 'http://127.0.0.1:5173'

test('免登录试用真链路：访客在首页翻出一句真译文，全程不碰登录页', async ({ page, request }) => {
  // —— 可达探针（400 是**预期**结果：证明路由在、鉴权没拦、校验在工作；空文本一句都不计配额） ——
  const probe = await request.post(`${BASE}/api/trial/translate`, {
    data: { text: '', target_lang: 'en', device_id: 'e2etrialprobe01' },
  })
  expect(probe.status(), '⇒ /api/trial/translate 路由没通（期待 400 校验拒，实得别的码）').toBe(400)
  const pj = await probe.json()
  expect(pj.code, '⇒ 拒收码不是 VALIDATION_ERROR（说明请求没走到试用 handler）').toBe('VALIDATION_ERROR')

  // —— 访客态进首页：Playwright 每个用例都是干净 context（无会话、无 localStorage 设备号） ——
  await page.goto(`${BASE}/`)
  await expect(page.locator('.lc-hero-hot'), '⇒ 演示卡热区不在（试用入口的载体）').toBeVisible({ timeout: 30000 })
  expect(await page.locator('.lc-trial').count(), '⇒ 试用卡提前挂载（默认该是演示卡）').toBe(0)
  await page.locator('.lc-hero-hot').click()

  // —— 切卡：试用卡在场、演示卡整棵卸载（两张卡不该同时在场） ——
  await expect(page.locator('.lc-trial'), '⇒ 点热区没切出试用面板').toBeVisible()
  expect(await page.locator('.lc-hero-hot').count(), '⇒ 热区没让位').toBe(0)
  // 「剩 N 句」在拿到成功响应之前**不许出现**：计数按「成功才计」，首发显示只能是假数字
  expect(await page.locator('.lc-trial-foot').count(), '⇒ 还没翻译就报了剩余句数').toBe(0)

  // —— 语种盘来自真接口：中文界面默认目标应是 English，且选项不止兜底那几个（名单真拉到了） ——
  const opts = await page.locator('.lc-trial-select option').count()
  expect(opts, '⇒ 语种下拉几乎是空的（/api/translation/langs 没接上时只剩兜底 5 档）').toBeGreaterThan(5)
  await expect(page.locator('.lc-trial-select')).toHaveValue('en')

  // —— 翻一句：跑两趟模型（初翻＋校对），慢是正常态，给足超时 ——
  const SRC = '这款产品的评审会改到下午三点。'
  await page.locator('.lc-trial-input').fill(SRC)
  await page.locator('.lc-trial-btn--pri').click()
  await expect(page.locator('.lc-trial-out-text'), '⇒ 译文区一直空着（链路或引擎没通）')
    .not.toBeEmpty({ timeout: 60000 })
  const out = (await page.locator('.lc-trial-out-text').innerText()).trim()
  expect(out.length, '⇒ 译文是空白串').toBeGreaterThan(0)
  // ⚠ 判据是「与原文不相等」，不是「不含汉字」：UAT 的 mock LLM 回 `TranslatedEN(<源文前 30 字>)`，
  //   括号里就是中文源文本身（口径同 e2e/translate_flow.spec.ts:76），断「无汉字」会假红。
  expect(out.replace(/\s+/g, ' '), '⇒ 译文与原文一字不差（等于没翻）').not.toBe(SRC.replace(/\s+/g, ' '))
  // 兜底红色提示一件都不许在场（err 与 out 同时出现＝把失败态当成了成功）
  expect(await page.locator('.lc-trial-err').count(), '⇒ 成功却带着错误提示').toBe(0)
  // 成功后才更新额度：5 句档用过一句 → 剩 4
  await expect(page.locator('.lc-trial-foot')).toContainText('4')

  // —— 全程没有登录墙：URL 停在 `/`，密码框计数为 0 ——
  expect(page.url(), '⇒ 试用过程把访客弹去了登录页').toMatch(new RegExp(`^${BASE}/(\\?.*)?$`))
  expect(await page.locator('input[type="password"]').count(), '⇒ 试用链路上出现了登录表单').toBe(0)

  await page.screenshot({ path: 'artifacts/trial_public_success.png', fullPage: true })
})
