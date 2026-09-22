// ============================================================================
// e2e/multilang_zip.spec.ts — 多语言文件工单打包下载回归（2026-09-15 P2）
//   覆盖前端唯一的多语言文件翻译交付链：工单页上传 .txt → LangMultiSelect 选
//   「英语+日语」→ 创建入队（mock LLM 异步跑完）→ 完成行点「⬇ 下载」→
//   拦截 /api/tickets/download 响应断言：zip 魔数 + ≥2 本地文件头 + 条目名含
//   e2e_m1_en / e2e_m1_ja（每语言独立产物打包，历史缺口：zip 路径仅 OpenAPI
//   有 API 断言，UI 链路无覆盖）+ ★ #65 条目名无内部落盘时间戳标记。
// 运行：BASE_URL=http://127.0.0.1:8899 npx playwright test e2e/multilang_zip.spec.ts
// ============================================================================
import { test, expect, Page } from '@playwright/test';

// BASE：被测实例地址，默认取 run_uat.sh 起的本地 8899（红线：e2e 不得写死生产域名）。
const BASE = process.env.BASE_URL || 'http://127.0.0.1:8899';

/** API 登录并种入会话 token（与 dark_admin_upload 同法） */
// 模块级 TOKEN：浏览器里的 token 种在 sessionStorage（页面内可见），但下面要用
// page.request（Node 侧请求上下文）直接抓下载响应体，那边读不到页面的 storage，
// 所以登录时顺手留一份裸 token 给请求头用。
let TOKEN = '';
// login：走 /api/auth/login 拿 token，种进页面 storage 并留一份给 Node 侧请求头（见上）。
async function login(page: Page, user: string, pass: string) {
  const res = await page.request.post(`${BASE}/api/auth/login`, { data: { username: user, password: pass } });
  const body = await res.json();
  expect(body.success, `登录失败:${JSON.stringify(body)}`).toBeTruthy();
  // addInitScript：每次导航前重种，保证用例内多次 goto 不会掉登录态（掉一次就会被弹回登录页假红）。
  TOKEN = body.token;
  await page.addInitScript((tk) => sessionStorage.setItem('auth_token', tk), body.token);
}

/** 在 LangMultiSelect（★ 2026-09-15 任务⑤重构：Popup 自绘下拉）中确保勾选某语言：
 *  点触发器开面板 → 选项行已是选中态则跳过（工单页默认已选 en，toggle 会反选）→
 *  否则直接点选项行勾选 → 复核面板勾 ✓ 与外部 chip 行（选中结果唯一展示位）。 */
async function pickLang(page: Page, label: string) {
  await page.locator('[data-testid="lang-multi-trigger"]').first().click();
  const panel = page.locator('[data-testid="lang-multi-panel"]').first();
  await expect(panel).toBeVisible({ timeout: 5000 });
  const opt = panel.locator('[role="option"]', { hasText: label }).first();
  await expect(opt).toBeVisible({ timeout: 5000 });
  if ((await opt.getAttribute('aria-selected')) !== 'true') await opt.click();
  await expect(opt).toHaveAttribute('aria-selected', 'true', { timeout: 5000 });
  await page.locator('[data-testid="lang-multi-trigger"]').first().click(); // 收起面板
  // 收尾复核走「外部 chip 行」而不是面板：面板勾只证明点了，chip 由工单页的选中列表渲染
  // （LangChips 的 langs 即建单时提交的 target_langs 同源数据）。两处分开断，
  // 能区分「面板不响应点击」与「面板勾了但没回写到表单状态」两类缺陷。
  await expect(page.locator('[data-testid="lang-chips"] .tag', { hasText: label }).first())
    .toBeVisible({ timeout: 5000 });
}

test.describe('多语言文件工单 → zip 打包下载', () => {
  test('M1 上传 txt 选英/日双语 → 完成 → 下载 zip 含双语言产物', async ({ page }) => {
    test.setTimeout(180_000); // mock LLM 异步工单：排队+执行+轮询窗口
    await login(page, process.env.UAT_USER || 'uatuser_a', process.env.UAT_PASS || 'uatpass123');
    await page.goto('/tickets');
    // 切「文件」模式并选文件（复用 U1 已验证的 #tk-file-input 约定）
    // ★ 2026-09-18 UI 迁移：模式按钮 emoji → i18n 文案「文件」
    await page.getByRole('button', { name: /^文件$/ }).click();
    const input = page.locator('#tk-file-input');
    await expect(input).toBeAttached({ timeout: 10000 });
    await input.setInputFiles([{
      name: 'e2e_m1.txt', mimeType: 'text/plain',
      buffer: Buffer.from('天空是蓝色的。\n书籍是人类进步的阶梯。\n'),
    }]);
    // 勾选两个目标语言（默认仅 en；en/ja 分属 KB 与其他语言分组，弹窗内滚动可达）
    await pickLang(page, '英语');
    await pickLang(page, '日语');
    // pickLang 收尾已经点过触发器收起浮层（LangMultiSelect 的收起路径是「再点触发器 / 点组件外部」，
    // 并未实现 Esc 关闭），这里再按一次 Esc 纯属双保险。
    // .catch(() => {}) 是必须的——这个多余动作没有资格决定整条用例的红绿。
    await page.keyboard.press('Escape').catch(() => {});
    // 建单入队
    const title = page.locator('input[placeholder*="工单标题"]');
    await expect(title).toBeVisible();
    await title.fill('E2E多语言打包单');
    await page.getByRole('button', { name: /创建并入队/ }).click();
    // 用「列表里出现这一行」而不是 toast 判定建单成功：行是轮询回来的真实状态，toast 会自己消失。
    await expect(page.locator('text=E2E多语言打包单').first()).toBeVisible({ timeout: 20000 });
    // 等该行出现「⬇ 下载」按钮 = 工单完成（列表 5s 轮询驱动行态刷新）
    const row = page.locator('tr', { hasText: 'E2E多语言打包单' }).first();
    const dl = row.getByRole('button', { name: /下载/ }).first();
    await expect(dl, '150s 内工单未完成（检查 mock LLM/worker）').toBeVisible({ timeout: 150_000 });
    // 点击触发下载（UI 链路完整性：不应弹「下载失败」toast），同时抓取工单 id 供字节级复验
    // 为什么要 id：zip 内容必须拿原始字节才能数本地文件头，而浏览器点下载拿不到 buffer，
    // 只能再用 page.request 直连接口。列表字段兼容 tickets/list/data 三种返回形态，
    // 是因为该接口历史上换过包装，钉死一种会让本用例在无关改动里假红。
    const tid = await page.evaluate(async () => {
      const r = await fetch('/api/tickets', { headers: { Authorization: `Bearer ${sessionStorage.getItem('auth_token')}` } });
      const d = await r.json();
      const rows = d.tickets || d.list || d.data || [];
      const hit = rows.find((x: any) => (x.title || '').includes('E2E多语言打包单'));
      return hit ? hit.id : 0;
    });
    // 定位失败时返回 0，会让后面的下载请求打到「id=0」并给出难懂的错；这里先红在正确的位置。
    expect(tid, '未能定位测试工单 id').toBeGreaterThan(0);
    // 先挂响应监听、再点按钮（Promise.all 同发）：反过来会有响应早于监听的竞态，
    // 表现为偶发「30s 等不到 /api/tickets/download」。
    await Promise.all([
      page.waitForResponse((r) => r.url().includes('/api/tickets/download'), { timeout: 30000 }),
      dl.click(),
    ]);
    // zip 结构断言走请求上下文直取字节（浏览器 fetch 消费响应体后 Playwright 取不到 buffer）
    const resp = await page.request.get(`${BASE}/api/tickets/download?id=${tid}`, {
      headers: { Authorization: `Bearer ${TOKEN}` },
    });
    expect(resp.status(), '下载响应码').toBe(200);
    const buf = await resp.body();
    // 魔数只查前两字节 'PK'：这一步要防的是「把 JSON 错误体当文件发回来」这种历史缺陷形态，
    // 两字节已足够区分；完整签名 0x04034b50 的核对交给下面逐字节计数，两处不重复设闸。
    expect(buf.slice(0, 2).toString('latin1'), 'zip 魔数 PK').toBe('PK');
    // 本地文件头签名 0x04034b50 计数 ≥2（每语言一个产物条目）
    // 逐字节扫而不是解析 zip 目录：断言层不引第三方 unzip 依赖，也避免「条目名对但内容缺」。
    let heads = 0;
    for (let i = 0; i + 4 <= buf.length; i++) {
      if (buf[i] === 0x50 && buf[i + 1] === 0x4b && buf[i + 2] === 0x03 && buf[i + 3] === 0x04) heads++;
    }
    expect(heads, `zip 本地文件头数量应≥2，实际 ${heads}`).toBeGreaterThanOrEqual(2);
    // latin1（逐字节 1:1 映射）而不是 utf8：文件名区段里混着非 UTF-8 字节，
    // utf8 解码会把它们替换成 U+FFFD，导致下面的条目名断言「明明有却说不含」的假红。
    const names = buf.toString('latin1');
    expect(names, '应含英语产物 e2e_m1_en').toContain('e2e_m1_en');
    expect(names, '应含日语产物 e2e_m1_ja').toContain('e2e_m1_ja');
    // ★ #65（2026-09-22）：条目名必须是「原件展示名 + 语言标记」，不得带内部落盘时间戳标记。
    // 旧口径 zip 条目长这样：`1790026522069352000_e2e_m1_en.txt`——那串 19 位数字是磁盘上
    // 防重名用的内部标识，泄漏进交付物既难看又暴露实现细节。分目录改造后唯一性由产物子目录
    // 承担（translated/<落盘名>/），交付名得以剥净，本条断言即那道锁。
    expect(names, 'zip 条目名含内部时间戳标记（15 位以上数字紧跟下划线）').not.toMatch(/[0-9]{15}_/);
    await page.screenshot({ path: 'artifacts/m1_multilang_zip.png' });
  });
});
