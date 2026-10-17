// 启动哨兵（index.html 内联脚本）的**运行时**守卫 + 反向对照
//
// 由来（★ 2026-10-17 现网第二次实证）：同一台访客又交回 ② 读数，而这次指向的是**刚换上去的新入口**
// （不是旧缓存件）⇒ 真身不是某个坏文件，而是这一跳确实慢：本机跨境实测同一份 163 KB 的 br 入口
// 12.8 s 才下完，另两次 60 s 都没下完（同一台打源站只要几十毫秒）。于是哨兵的两个缺陷把「慢」
// 变成了「坏」：① 6s 固定截止把「还在下载」误报成「下载没完成」；② 面板一旦显示就再也不撤
// （旧形态只在「#root 空着」时轮询收摊，`show()` 之后 #root 才填上的那一档没人管）——
// 界面其实已经挂载、用户脸上却盖着一块「页面没能加载起来」。
//
// 静态侧的口径与反证见 src/bootStorageGate.test.ts（自适应截止／慢链路走横幅／挂载后撤销）；
// 这里补的是**真浏览器里那两条腿真的成立**（静态锁看不见"面板到底盖不盖"）：
//   A) 把入口脚本拖到 10s 才回 ⇒ 哨兵只许出底部横幅（不许出全屏面板），且挂载后必须撤销；
//   B) 反向对照：入口脚本请求被中止（真失败）⇒ 必须是全屏面板且点明脚本没加载
//      （证明 A 的宽松不是"把判据关掉了"）。
// 两条都不依赖后端：路由拦截只动入口 JS，页面起不起来由本用例自己判。
import { test, expect } from '@playwright/test'

const BASE = process.env.BASE_URL || 'http://127.0.0.1:8899'
// 入口名带内容哈希，形状由构建决定（哈希字母表含 `-`/`_`，见 AGENTS §三 第三族那条），只按形状匹配
const ENTRY_RE = /\/assets\/index-[A-Za-z0-9_-]+\.js$/

test('慢链路入口：哨兵只出底部横幅（不挡界面），界面挂载后必须撤销', async ({ page }) => {
  test.setTimeout(90000)
  await page.route(ENTRY_RE, async (route) => {
    await new Promise((r) => setTimeout(r, 10000))
    await route.continue()
  })
  await page.goto(BASE, { waitUntil: 'domcontentloaded' })
  // 默认档截止 8s：此时入口还在下载 ⇒ 应有横幅
  await expect(page.locator('#__boot_fail'), '慢链路连一句提示都没有（哨兵读到不到"还在下载"）').toBeVisible({ timeout: 25000 })
  expect(await page.locator('#__boot_fail[data-kind="panel"]').count(),
    '慢链路被误判成确定失效：全屏面板盖住了**正在加载**的界面（这正是本次报障的真身）').toBe(0)
  // 入口最终评估完 ⇒ 界面挂载 ⇒ 哨兵产物必须撤掉
  await expect(page.locator('#root > *').first(), '入口脚本最终仍没挂载（本腿没有射程，先查预览/联调服务）').toBeVisible({ timeout: 40000 })
  await expect.poll(() => page.locator('#__boot_fail').count(),
    { message: '界面已经挂载，哨兵产物却还盖在脸上（旧缺陷②：show() 之后 #root 才填上的那一档没人管）', timeout: 10000 })
    .toBe(0)
})

test('反向对照：入口脚本真失败 ⇒ 出全屏面板并点明脚本没加载', async ({ page }) => {
  test.setTimeout(90000)
  await page.route(ENTRY_RE, (route) => route.abort())
  await page.goto(BASE, { waitUntil: 'domcontentloaded' })
  const panel = page.locator('#__boot_fail[data-kind="panel"]')
  await expect(panel, '脚本都失败了却没出面板（哨兵失效，A 的宽松就没有反证）').toBeVisible({ timeout: 25000 })
  await expect(panel, '面板没点明是脚本没加载（文案退化成"没抓到具体报错"）').toContainText('没能加载')
})
