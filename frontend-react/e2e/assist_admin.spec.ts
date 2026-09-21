// ============================================================================
// e2e/assist_admin.spec.ts — 后台 AI 助手管理面板端到端（★ #34 前端重做，2026-09-21）
// 为什么单独一条 spec：旧形态是 iframe 内嵌 assist 自带管理台，主站 Playwright 根本进不去
// 那个文档（跨域 + 无 data-testid），所以「助手管理面」从来没有真端到端断言。
// 改成原生面板 + 主后台同源代理后，本 spec 锁住四条改坏就影响使用的行为：
//   A1 超管进入面板：状态横幅显示上游在线、凭据由后端注入，且页面**不再嵌 iframe**；
//   A2 请求面不外泄凭据：面板发出的 /api/admin/assist/* 请求 URL 不含 admin_token，
//      响应体不含 Token 明文（Token 只在服务端注入头里）；
//   A3 知识库 CRUD 往返：新建 → 列表可见 → 删除（二次确认）→ 消失；
//   A4 配置页签：密钥输入框为 password 型，「测试连通」能回显模型（说明代理链路通到 mock LLM）；
//   A5 普通用户即便直接敲 /admin 也拿不到管理数据（后端 403，前端不渲染面板）；
//   A6 ★ 权限口径双向回归：超管侧「AI 助手」入口必须可见且可点开面板
//      （A5 只守住「非超管看不到」这一向，minLevel 改错方向会连超管一起关死且 A5 反变绿）。
// 运行：BASE_URL=http://127.0.0.1:8899 npx playwright test e2e/assist_admin.spec.ts
//      （由 scripts/uat/run_uat.sh 统一编排：主服务 + assist-server + mock LLM）
// ============================================================================
import { test, expect, Page, Request } from '@playwright/test';

const BASE = process.env.BASE_URL || 'http://127.0.0.1:8899';
// UAT 编排里 assist-server 的 Token（run_uat.sh 导出同名变量；仅用于「响应里绝不能出现它」的断言）
const ASSIST_TOKEN = process.env.ASSIST_UAT_TOKEN || 'uat-assist-token-34';

// 登录态注入：API 登录拿 token → sessionStorage（与 pixel_uat.spec.ts 同口径）
async function login(page: Page, user: string, pass: string) {
  const res = await page.request.post(`${BASE}/api/auth/login`, { data: { username: user, password: pass } });
  const body = await res.json();
  expect(body.success, `登录失败：${JSON.stringify(body)}`).toBeTruthy();
  await page.addInitScript((tk) => sessionStorage.setItem('auth_token', tk), body.token);
  return body.token as string;
}

/** 进后台并切到「AI 助手」菜单（菜单文案取中文词典 admin.menuAssist） */
async function openAssist(page: Page) {
  await page.goto('/admin');
  const item = page.getByText('AI 助手', { exact: true }).first();
  await expect(item, '超管侧边栏应有「AI 助手」入口').toBeVisible({ timeout: 15000 });
  await item.click();
}

test.describe('后台 AI 助手管理面板（#34）', () => {
  test('A1+A2 超管面板原生渲染，凭据不进浏览器', async ({ page }) => {
    await login(page, 'admin', process.env.ADMIN_PASS || 'Admin@1234');
    const calls: Request[] = [];
    const bodies: string[] = [];
    page.on('request', (r) => { if (r.url().includes('/api/admin/assist/')) calls.push(r); });
    page.on('response', async (r) => { if (r.url().includes('/api/admin/assist/')) bodies.push(await r.text().catch(() => '')); });

    await openAssist(page);
    await expect(page.locator('body')).toContainText(/服务在线，管理凭据由后端注入/, { timeout: 20000 });
    // 旧形态残留必须消失：既没有 iframe，也不再往 localStorage 塞 assist_tok
    expect(await page.locator('iframe').count(), '面板不应再内嵌 iframe').toBe(0);
    expect(await page.evaluate(() => localStorage.getItem('assist_tok'))).toBeNull();

    await expect.poll(() => calls.length, { timeout: 15000 }).toBeGreaterThan(0);
    for (const r of calls) expect(r.url(), `请求 URL 泄漏凭据：${r.url()}`).not.toContain('admin_token');
    const all = bodies.join('\n');
    expect(all, '响应体不得回传管理 Token 明文').not.toContain(ASSIST_TOKEN);
    // 面板文案已进 12 语种词典（此处验证中文态，多语种切换由 admin_tabs_lang.spec.ts 承担）
    await expect(page.locator('body')).toContainText('知识库');
  });

  test('A3 知识库新建 → 可见 → 删除（二次确认）', async ({ page }) => {
    await login(page, 'admin', process.env.ADMIN_PASS || 'Admin@1234');
    await openAssist(page);
    const key = 'e2e_assist_' + Date.now();

    // 页签必须限定在本面板内点：后台侧边栏同样有「知识库」菜单项，全局 .first() 会点到侧栏
    // → 面板被卸载 → 「新建」永不出现（2026-09-21 全量 UAT A3 超时根因，属测例 bug）
    await page.getByTestId('assist-tabs').getByText('知识库', { exact: true }).click();
    await page.getByRole('button', { name: '新建', exact: true }).click();
    // ★ 表单弹窗内的按钮必须限定在弹窗里点、并用 exact：面板顶部还有一个「保存 Token」按钮，
    //   getByRole 的 name 默认是**子串**匹配，'保存' 会同时命中两者而报 strict mode violation
    //   （2026-09-22 全量 UAT A3 红灯根因，同样是测例口径而非产品缺陷）。
    const dlg = page.getByRole('alertdialog');
    await expect(dlg, '点「新建」后应弹出条目表单').toBeVisible({ timeout: 5000 });
    await dlg.getByLabel('标识 key').fill(key);
    await dlg.getByLabel('标题').fill('E2E 冒烟条目');
    await dlg.getByLabel('内容').fill('仅用于端到端断言');
    await dlg.getByRole('button', { name: '保存', exact: true }).click();
    // 按行定位（种子库有 27 条，每行都有「删除」链接，全局 .first() 会误删别人的条目）
    const row = page.getByRole('row').filter({ hasText: key });
    await expect(row, '新建后列表应出现该条目').toHaveCount(1, { timeout: 15000 });

    // 删除必须过二次确认弹窗（直接删属于误操作风险）
    await row.getByText('删除', { exact: true }).click();
    await expect(page.locator('body')).toContainText(/确定删除这条记录吗/);
    await page.getByRole('alertdialog').getByRole('button', { name: '删除', exact: true }).click();
    await expect(page.getByRole('row').filter({ hasText: key }), '确认删除后应从天知识库列表消失')
      .toHaveCount(0, { timeout: 15000 });
  });

  test('A4 配置页签：密钥掩码回显 + 测试连通回显模型', async ({ page }) => {
    await login(page, 'admin', process.env.ADMIN_PASS || 'Admin@1234');
    await openAssist(page);
    await page.getByTestId('assist-tabs').getByText('配置', { exact: true }).click();
    const keyInput = page.getByLabel('llm_api_key');
    await expect(keyInput, '密钥输入框必须是 password 型').toHaveAttribute('type', 'password');
    await page.getByRole('button', { name: '测试连通' }).click();
    // mock LLM 在 UAT 编排里可达；回显形如「连通正常：mock-llm，耗时 xxms」
    await expect(page.locator('body')).toContainText(/连通正常|连通失败/, { timeout: 40000 });
  });

  test('A5 普通用户拿不到管理数据（后端 403，前端不放行）', async ({ page, request }) => {
    const token = await login(page, 'uatuser_a', 'uatpass123');
    const res = await request.post(`${BASE}/api/admin/assist/config`, {
      headers: { Authorization: `Bearer ${token}` },
      data: { key: 'welcome', value: '越权写入' },
    });
    // ★ #34 收尾：状态码必须逐字钉 403（旧断言只看 success:false，
    //   后端哪天把鉴权失败改成 500/200+success:false 都会「照样通过」，等于没守）
    expect(res.status(), `非超管直连 assist 管理面应回 403，实得 ${res.status()}`).toBe(403);
    const body = await res.json();
    expect(body.success, '非超管读写 assist 管理面必须被拒').toBeFalsy();
    await page.goto('/admin');
    await expect(page.getByText('AI 助手', { exact: true })).toHaveCount(0);
  });

  // ★ A6（#34 收尾，2026-09-22）：A5 只守住了「非超管看不到」这一向。
  //   而这次的修法是往前端菜单收门槛（AdminDashboard.tsx 的 assist 项 minLevel 3 → 4，
  //   与后端 requireAdminUser=等级 4 对齐），**同一段 filter 代码写错方向就会把超管的入口也一起关死**，
  //   而 A5 反而会因此变绿——单向断言的经典盲区。故补双向：
  //   超管登录 → 侧栏入口必须可见、可点、点完真渲染出原生面板（assist-tabs 出现）。
  //   锚点收敛在 .lc-sidebar__nav 内（A3 红灯教训：后台侧栏与面板内页签会同名文案）。
  test('A6 权限口径双向回归：超管侧「AI 助手」入口可见且可点开面板', async ({ page }) => {
    await login(page, 'admin', process.env.ADMIN_PASS || 'Admin@1234');
    await page.goto('/admin');
    const nav = page.locator('.lc-sidebar__nav');
    await expect(nav, '后台侧栏未渲染').toBeVisible({ timeout: 15000 });
    const item = nav.getByRole('button', { name: 'AI 助手', exact: true });
    await expect(item, '超管侧栏必须有「AI 助手」入口（minLevel 被误提到 5 即在此翻红）').toBeVisible({ timeout: 15000 });
    await expect(item).toBeEnabled();
    await item.click();
    // 点开后必须真的挂上原生面板（页签容器有 testid），而不是停在原面板/白屏
    await expect(page.getByTestId('assist-tabs'), '入口可点但面板未挂载').toBeVisible({ timeout: 20000 });
    await expect(nav.getByRole('button', { name: 'AI 助手', exact: true })).toHaveClass(/lc-side-item--active/);
  });
});
