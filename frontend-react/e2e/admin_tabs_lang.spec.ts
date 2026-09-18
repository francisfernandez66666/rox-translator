// ============================================================================
// e2e/admin_tabs_lang.spec.ts — 2026-09-15 任务③⑤回归防护（2026-09-16 随 autosales 更新）
//   T1 后台一级菜单合并：20 个一级 Tab 精简为 9 个（计费与套餐 / 系统与运维
//      Hub 收纳旧菜单 + autosales 新增「AI 助手」），旧「套餐与订单」「租户管理」
//      「成本对账」等不得再以一级菜单出现；Hub 内子 tab 可切换渲染。
//   T2 外部调用 Hub：api / webhooks / SDK 三子 tab，SDK 页含三端安装内容。
//   T3 语言多选去重（LangMultiSelect Popup 重写）：选中中文后 chip 区恰 1 个、
//      下拉面板内不得再有已选中态残留（历史缺陷：Select 下拉与外部 chip 双展示）。
// 运行：BASE_URL=http://127.0.0.1:8899 npx playwright test e2e/admin_tabs_lang.spec.ts
// ============================================================================
import { test, expect, Page } from '@playwright/test';

const BASE = process.env.BASE_URL || 'http://127.0.0.1:8899';

/** API 登录并种入会话 token（与 dark_admin_upload 同法） */
async function login(page: Page, user: string, pass: string) {
  const res = await page.request.post(`${BASE}/api/auth/login`, { data: { username: user, password: pass } });
  const body = await res.json();
  expect(body.success, `登录失败:${JSON.stringify(body)}`).toBeTruthy();
  await page.addInitScript((tk) => sessionStorage.setItem('auth_token', tk), body.token);
}

test.describe('后台 Tab 合并 + 语言多选去重', () => {
  test('T1 一级菜单收敛为 9 项，旧计费菜单收进 Hub 子 tab', async ({ page }) => {
    await login(page, 'admin', 'Admin@1234');
    await page.goto('/admin');
    // ★ 2026-09-18 UI 迁移：后台外壳 TDesign Menu → ui/langcross AdminShell，
    //   锚点 .admin-side→.lc-sidebar、.t-menu__item→.lc-side-item（面板 .panel-card 不变）
    const side = page.locator('.lc-sidebar');
    // 新一级菜单存在（按 i18n 中文标签匹配）
    for (const label of ['计费与套餐', '系统与运维', '外部调用', '组织与成员', '总览']) {
      await expect(side.getByText(label).first(), `一级菜单缺「${label}」`).toBeVisible();
    }
    // 一级导航条目总数=9（overview/tickets/personal/kb/org/external/billing/system
    // + assist：2026-09-16 autosales 批次新增「🤖 AI 助手」，断言随菜单同步）
    const n = await side.locator('.lc-side-item').count();
    expect(n, `一级菜单数=${n}，应收敛为 9`).toBe(9);
    // AI 助手菜单可见（超管 L4 满足 minLevel 3）
    await expect(side.getByText('AI 助手').first(), '一级菜单缺「AI 助手」').toBeVisible();
    // 进入计费 Hub：三个子 tab 平铺且可切换出内容
    await side.getByText('计费与套餐').first().click();
    await expect(page.getByText('成本对账').first()).toBeVisible();
    await page.getByText('租户管理').first().click();
    await expect(page.locator('.panel-card, .t-table').first()).toBeVisible();
    await page.getByText('套餐与订单').first().click();
    await expect(page.locator('.panel-card, .t-table').first()).toBeVisible();
  });

  test('T2 外部调用 Hub 含 SDK 子 tab', async ({ page }) => {
    await login(page, 'admin', 'Admin@1234');
    await page.goto('/admin');
    // ★ 2026-09-18 UI 迁移：.admin-side→.lc-sidebar
    await page.locator('.lc-sidebar').getByText('外部调用').first().click();
    await expect(page.getByText('官方 SDK').first()).toBeVisible();
    await page.getByText('官方 SDK').first().click();
    // SDK 页三端安装标识（TS / Python / 桌面端）
    await expect(page.getByText('npm', { exact: false }).first()).toBeVisible();
    await expect(page.getByText('pip', { exact: false }).first()).toBeVisible();
  });

  test('T3 聊天语言多选：选中即入 chip，下拉无重复展示位', async ({ page }) => {
    await login(page, 'uatuser_a', 'uatpass123');
    await page.goto('/');
    const trigger = page.getByTestId('lang-multi-trigger');
    await trigger.click();
    const panel = page.getByTestId('lang-multi-panel');
    await expect(panel).toBeVisible();
    // 默认态：新会话兜底已选英语（useChat.loadLangs 默认 ["en"]）——chip 恰 1 个
    await expect(page.getByTestId('lang-chips').locator('.tag-lang')).toHaveCount(1);
    await panel.locator('input').first().fill('日语');
    await page.keyboard.press('Enter');
    // Enter 勾选首个过滤命中（日语）：面板内选中态=默认英语+日语 共 2（勾选态仅供增删，不是展示位）
    await expect(panel.locator('[role="option"][aria-selected="true"]')).toHaveCount(2);
    await page.keyboard.press('Escape');
    // chip 区同步 2 个已选语言（选中结果唯一展示位=chip 行）
    await expect(page.getByTestId('lang-chips').locator('.tag-lang')).toHaveCount(2);
    // 触发器永远不渲染已选语言标签（历史缺陷：Select 下拉与外部 chip 双展示的回归锁）
    const triggerText = await trigger.textContent();
    expect(triggerText || '').not.toContain('日语');
    expect(triggerText || '').not.toContain('英语');
  });
});
