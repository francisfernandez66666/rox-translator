// ============================================================================
// e2e-manual/chat_balance_realtime.spec.ts — 真模型「扣费后 3s 顶栏翻动」人工探针
// （★ F-48③，〇-U 批 I-5 补断言口径的第三条，2026-09-27 落档）
//
// 为何必须手工（AGENTS §一·6）：
//   F-48 的时序竞态**只有真模型才存在**——mock LLM 毫秒级返回，
//   「扣费发生在流终帧前 flush、前端 2s debounce 刷新」这条链在 mock 下
//   结构上测不出来（修复文档 §5.1 原话：现有 e2e 的等待窗在真模型下会偏短）。
//   真模型 + 真扣费要消耗真实积分与真实模型配额，发布闸门既无凭据也不该扣钱，
//   故整文件用 env 守卫，缺凭据即 skip，永不拖红。
//
// 与已落两刀的关系（批 I-5 改法 ①② 已有 CI 锁，本探针补第 ③ 条）：
//   ① 流终态触发刷新（useChat onDone/错误分支显式 loadBalance）——jsdom 锁在
//     ChatWindow.dom.test.tsx（myPackage 桩真数据 + 流结束余额变）；
//   ② 窄屏余额可见——Playwright 视口锁在 e2e/（等值锁）；
//   ③ 本文件：**真模型端到端**验证「译文上屏（流终态）后 3s 内，余额/今日已耗
//     至少一条发生翻动」——3s = 2s debounce + 1s 渲染余量，是被锁的契约不是拍脑袋。
//
// 运行（指向任一真实配好模型路由的站点；账号需有可见余额的积分）：
//   BASE_URL=https://langcross.lexicorn.cn E2E_USER=<用户名> E2E_PASS=<口令> \
//     npx playwright test -c playwright.config.ts e2e-manual/chat_balance_realtime.spec.ts
//   ⚠️ 口令一律走环境变量注入，禁止写进本文件或任何提交物（UAT 红线）。
// ============================================================================
import { test, expect, type Page } from '@playwright/test'

const BASE = (process.env.BASE_URL || '').replace(/\/+$/, '')
const USER = process.env.E2E_USER || ''
const PASS = process.env.E2E_PASS || ''

test.skip(!BASE || !USER || !PASS, '手工探针：需 BASE_URL + E2E_USER + E2E_PASS（真模型+真扣费），不进发布闸门')

async function login(page: Page, user: string, pass: string) {
  const res = await page.request.post(`${BASE}/api/auth/login`, { data: { username: user, password: pass } })
  const body = await res.json()
  expect(body.success, `登录失败（HTTP ${res.status()}）`).toBeTruthy()
  await page.addInitScript((tk) => sessionStorage.setItem('auth_token', tk), body.token)
  await page.addInitScript(() => localStorage.setItem('app_lang', 'zh'))
}

/** 读积分条数值：取文本第一个数字（与 i18n 文案解耦，口径同 e2e/translate_flow.spec.ts） */
async function readPoints(page: Page, testid: string): Promise<number> {
  const txt = await page.getByTestId(testid).innerText({ timeout: 10000 })
  const m = txt.match(/[\d,]+/)
  expect(m, `${testid} 未渲染出积分数值：${txt.slice(0, 120)}`).toBeTruthy()
  return Number((m as RegExpMatchArray)[0].replace(/,/g, ''))
}

test('F-48③ 真模型：译文上屏后 3s 内余额/今日已耗必须翻动（顶栏不停旧值）', async ({ page }) => {
  // 真模型一轮翻译可能 30s+，给足墙钟
  test.setTimeout(180_000)
  await login(page, USER, PASS)
  await page.goto(`${BASE}/`)

  // 前置：余额/用量条在场（不在场=登录态或套餐数据有问题，探针自身先红，不误判成竞态回归）
  await expect(page.getByTestId('chat-balance'), '余额条未渲染（账号需有可见积分）')
    .toBeVisible({ timeout: 20000 })
  await expect(page.getByTestId('chat-usage'), '今日已耗条未渲染').toBeVisible({ timeout: 20000 })
  const bal0 = await readPoints(page, 'chat-balance')
  const used0 = await readPoints(page, 'chat-usage')

  // 发起一轮真实即时翻译（短文本，控消耗）
  const input = page.getByTestId('translate-input')
  await expect(input, '工作台输入框未渲染').toBeVisible({ timeout: 20000 })
  await input.fill('探针短句：余额刷新时序验证。')
  await page.getByRole('button', { name: /^(发送|翻译|Send|Translate)$/ }).click()

  // 流终态判据：译文结果表出现且目标语行非空——**以界面证据为准**，不用固定 sleep
  await expect(page.locator('.translation-results')).toHaveCount(1, { timeout: 120_000 })
  const out = (await page.locator('.lang-text').last().innerText()).trim()
  expect(out.length, '译文为空，链路未走完').toBeGreaterThan(0)

  // ★ 核心断言：从「译文上屏」这一刻起 3s 内，两条积分读数至少一条翻动。
  //   旧缺陷形态＝扣费慢一拍：length 触发点错过流终态 → 顶栏停旧值直到下一次发消息。
  //   expect.poll 每 250ms 读一次；超 3s 未翻动即红，红文案直给排查方向。
  await expect
    .poll(() => `${readPoints(page, 'chat-balance')}:${readPoints(page, 'chat-usage')}`, {
      timeout: 3_000,
      message: '★ F-48 复现形态：真模型扣费后 3s 内顶栏未翻动（刷新触发点又回到流中途/发消息时）',
    })
    .not.toBe(`${bal0}:${used0}`)
})
