// 零 JS 静态留资表单（★ DEF-P0-03，2026-11-01）的运行时守卫
//
// 静态侧口径与反证见 src/bootStorageGate.test.ts 的「启动链锁 E」；本文件补的是**只有真浏览器量得出**的四件事：
//   ① 入口 JS 没跑起来时，表单**真的可见、真的点得动**（pointer-events 是被骨架容器压制的，
//      静态锁只能看源码里写没写 auto，看不出计算样式最终落到哪一档）；
//   ② 三档视口下表单自己与它的滚动祖先都不许横向溢出（AGENTS §一·5 那条「判浮层要遍历滚动祖先」同口径——
//      只看 documentElement 在这一族里结构性恒真）；
//   ③ 界面挂载后表单必须被撤，且 React 那份 LeadForm 在位（两份并存＝同一页两个留资入口）；
//   ④ 原生提交真的能落回 /?lead=ok —— 这一条**必须有后端**，见下面的可达探针（AGENTS §一·6：
//      前端普遍有离线兜底，链路断了界面照样渲染，断言会对着兜底态一路绿灯）。
import { test, expect } from '@playwright/test'

const BASE = process.env.BASE_URL || 'http://127.0.0.1:5173'
// 入口名带内容哈希（哈希字母表含 -/_，见 AGENTS §三 第三族），只按形状匹配
const ENTRY_RE = /\/assets\/index-[A-Za-z0-9_-]+\.js$/
const LEAD_API = '/api/lead/public'

// noOverflowEverywhere 遍历 el 的**滚动祖先**逐层判 scrollWidth ≤ clientWidth。
// 只问 documentElement 会把"某个容器自己横向溢出"读成"页面没事"（本仓踩过两次）。
async function noOverflowEverywhere(page: import('@playwright/test').Page, sel: string, label: string) {
  const bad = await page.evaluate((s) => {
    const el = document.querySelector(s) as HTMLElement | null
    if (!el) return ['目标不存在：' + s]
    const out: string[] = []
    let n: HTMLElement | null = el
    while (n) {
      if (n.scrollWidth > n.clientWidth + 1) {
        out.push(`${n.id || n.className || n.tagName}: scrollWidth=${n.scrollWidth} > clientWidth=${n.clientWidth}`)
      }
      n = n.parentElement
    }
    if (document.documentElement.scrollWidth > document.documentElement.clientWidth + 1) {
      out.push(`documentElement: ${document.documentElement.scrollWidth} > ${document.documentElement.clientWidth}`)
    }
    return out
  }, sel)
  expect(bad, `${label}：横向溢出（容器自己被顶宽，用户看到的就是"表单缺半边"）`).toEqual([])
}

// builtShape 问的是「BASE_URL 指向的是不是构建产物」：dev 服务没有 /assets/index-<hash>.js 这一件，
// 入口拦截与"界面挂载后撤表单"两条腿会结构性落空——那测出来的是空转，不是绿灯。
async function builtShape(request: import('@playwright/test').APIRequestContext): Promise<boolean> {
  const res = await request.get(BASE).catch(() => null)
  if (!res) return false
  return /\/assets\/index-[A-Za-z0-9_-]+\.js/.test(await res.text())
}

test('入口 JS 被打死：静态留资必须可见、可点、不溢出（1440 / 390 / 320 三档）', async ({ page, request }) => {
  test.skip(!(await builtShape(request)), `⇒ ${BASE} 不是构建产物（没有 /assets/index-<hash>.js）：入口拦截腿测不了，先起预览服务`)
  await page.route(ENTRY_RE, (route) => route.abort()) // 真失败形态（不是慢）
  for (const vp of [{ width: 1440, height: 900 }, { width: 390, height: 844 }, { width: 320, height: 640 }]) {
    await page.setViewportSize(vp)
    await page.goto(BASE, { waitUntil: 'commit' })
    const form = page.locator('#__boot_lead')
    await expect(form, `视口 ${vp.width}：入口失败这一档连唯一那条客户入口都没有`).toBeVisible()
    // 点得动＝计算后的 pointer-events 不能是被继承的 none
    expect(await form.evaluate((el) => getComputedStyle(el).pointerEvents),
      `视口 ${vp.width}：留资表单 pointer-events 落成了 none（骨架容器压下来的）`).toBe('auto')
    await noOverflowEverywhere(page, '#__boot_lead', `视口 ${vp.width} 的留资表单`)
    // 蜜罐必须"看不见但仍在布局流里"：按可见性判（isVisible 为 false）而不是按 count（display:none 才算真隐藏）
    const honey = page.locator('#__boot_lead input[name="site"]')
    expect(await honey.count(), '蜜罐字段不在 DOM 里 ⇒ 后端那条假成功判据失去输入').toBe(1)
    const box = await honey.boundingBox()
    // 位移隐藏：还在渲染流（有尺寸）但被推到屏幕外
    expect(box, '蜜罐没有布局盒（很可能被改成 display:none ⇒ bot 也填不到）').not.toBeNull()
    if (box) expect(box.x + box.width, '蜜罐没被移出视口（真人看得见＝它就不是蜜罐了）').toBeLessThan(0)
    expect(await form.getAttribute('action'), 'action 必须同源（CSP 的 form-action self 已在位）').toBe(LEAD_API)
    expect(await form.getAttribute('method'), 'method 必须是 post').toBe('post')
  }
})

test('界面正常挂载：静态表单必须被撤，React 那份 LeadForm 在位（两份不许并存）', async ({ page, request }) => {
  test.skip(!(await builtShape(request)), `⇒ ${BASE} 不是构建产物：本腿要量的是"入口到了之后撤表单"`)
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto(BASE, { waitUntil: 'domcontentloaded' })
  await expect(page.locator('#root > *').first(), '界面没挂载（本腿没有射程，先查预览/联调服务）').toBeVisible({ timeout: 40000 })
  await expect.poll(() => page.locator('#__boot_lead').count(),
    { message: '界面起来了但静态留资还盖在底部（与 React 的 LeadForm 并存两份）', timeout: 20000 }).toBe(0)
  // React 那份在位＝撤腿不是"把留资整个删掉"（只判存在，不判它能否提交——提交由后端用例与下一条守）
  await expect(page.locator('.lc-lead, [class*=\'lc-lead\']').first(),
    '静态表单撤了，但 React 的留资表单也不在 ⇒ 这一档反而一条线索都留不下').toBeVisible({ timeout: 20000 })
})

test('原生表单提交（零 JS）能落回 /?lead=ok —— 本条必须先过可达探针', async ({ page, request }) => {
  // ★ AGENTS §一·6：链路型用例自带可达探针。探针用**服务端会拒的形态**（缺必填）——
  //   它只问"这一跳有没有人应答并按契约回话"，不产生任何一条真线索（校验失败不落库、不计数）。
  const probe = await request.post(BASE + LEAD_API, {
    form: { company: '', email: '' },
    headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
    maxRedirects: 0,
  }).catch(() => null)
  // ⚠️ 只判 status 会被 SPA 兜底骗过去：dev/预览服务对**不存在的路径**回 200 + 整页 index.html
  //   （本仓 §一·6 那条老判据）。所以 alive 必须同时问"回的不是 HTML"。
  const ct = probe ? (probe.headers()['content-type'] || '') : ''
  const alive = !!probe && probe.status() < 500 && !/text\/html/.test(ct)
  test.skip(!alive, `⇒ ${BASE + LEAD_API} 没应答（联调服务没起 / 后端是旧件）：这一条测不了，不是产品缺陷`)
  expect(alive, '探针必须拿到一个非 5xx 应答才有射程').toBe(true)

  await page.route(ENTRY_RE, (route) => route.abort()) // 零 JS 形态：入口被打死
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto(BASE, { waitUntil: 'commit' })
  const company = 'E2E探针' + Date.now()
  await page.locator('#__boot_lead input[name="company"]').fill(company)
  await page.locator('#__boot_lead input[name="email"]').fill('probe@example.com')
  await Promise.all([
    page.waitForURL((u) => u.searchParams.get('lead') !== null, { timeout: 20000 }),
    page.locator('#__boot_lead button[type="submit"]').click(),
  ])
  const u = new URL(page.url())
  // ⚠️ RATE_LIMITED 是**环境碰撞**，不是产品缺陷：本端点的最小间隔是 10s/IP，
  //   同一台联调地址上别人（或上一轮 retry、或手工 curl 探针）刚提交过就会吃到它。
  //   只有这一种码允许 skip；其余 lead=err（例如 VALIDATION_ERROR）就是真坏了，必须判红——
  //   放宽成"只要不是 ok 就 skip"等于把这条腿的射程关掉（AGENTS §一·6 的兜底陷阱同族）。
  if (u.searchParams.get('lead') === 'err' && u.searchParams.get('t') === 'RATE_LIMITED') {
    test.skip(true, `⇒ 本机 10 秒内已有同 IP 提交（配额最小间隔生效中）：这一条测不了，等一会复跑即可`)
  }
  expect(u.searchParams.get('lead'), `提交后出路不对：${u.search}`).toBe('ok')
  // 探针账号自证：这一条测的是"真提交过去了"，不是"URL 恰好带个 lead"
  expect(u.pathname, '出路必须落在首页（HTML 面）').toBe('/')
  void company
})

// ④ 出路提示态（★ C-1.3）：`?lead=ok|err` 必须被 React 读成一句人话，并且**读完就清 query**
//    （刷新不许再提示一次）。这一条不提交表单，只测前端读法——与上一条合起来才是完整的一条腿。
test('出路提示态：?lead=ok/err 显示一次性提示并清掉 query', async ({ page, request }) => {
  test.skip(!(await builtShape(request)), `⇒ ${BASE} 不是构建产物：本条要量 React 挂载后的读法`)
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto(BASE + '/?lead=ok', { waitUntil: 'domcontentloaded' })
  const ok = page.locator('.lc-lead-notice')
  await expect(ok, '?lead=ok 没出成功提示（出路字面量与前端取值分家了）').toBeVisible({ timeout: 20000 })
  const okText = (await ok.innerText()).trim()
  expect(okText.length, '成功提示是空句').toBeGreaterThan(3)
  await expect.poll(() => page.evaluate(() => new URL(location.href).searchParams.get('lead')),
    { message: '读完没清 query：刷新会再提示一次' }).toBeNull()
  await page.goto(BASE + '/?lead=err&t=VALIDATION_ERROR', { waitUntil: 'domcontentloaded' })
  const bad = page.locator('.lc-lead-notice')
  await expect(bad, '?lead=err 没出失败提示').toBeVisible({ timeout: 20000 })
  expect(await bad.getAttribute('data-kind'), '失败态必须带 data-kind=err（颜色判据靠它，不靠文案）').toBe('err')
  const errText = (await bad.innerText()).trim()
  expect(errText.length, '失败提示是空句').toBeGreaterThan(3)
  expect(errText, '两种态必须给出**不同**的句子（同句＝客户分不清成功失败）').not.toBe(okText)
})
