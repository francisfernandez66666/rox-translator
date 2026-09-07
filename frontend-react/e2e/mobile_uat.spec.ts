// ============================================================================
// e2e/mobile_uat.spec.ts — 移动端自适应核对脚本
// 职责：以手机视口（390×844）验证主站与演示站的移动端体验：
//   - 后台侧边栏在窄屏转抽屉（汉堡唤起 / 遮罩关闭 / 点菜单关闭）
//   - 聊天工作台输入栏可换行、无横向溢出
//   - 全站关键页面（自服务/工单/对照编辑/公开定价）无横向溢出
// 前提：本地后端 + 构建后的前端 dist 已在 BASE_URL 服务（参考 run_uat.sh 启动方式）。
// ============================================================================
import { test, expect } from '@playwright/test';

const BASE = process.env.BASE_URL || 'http://127.0.0.1:8899';
test.use({ viewport: { width: 390, height: 844 } });

// 登录小件：admin 模式进后台（/admin），home 模式进前台（/）——沿用固定超管账号
async function login(page: import('@playwright/test').Page, mode: 'admin' | 'home') {
  await page.goto(`${BASE}/${mode === 'admin' ? 'admin' : ''}`, { waitUntil: 'networkidle' });
  await page.waitForSelector('.login-card', { timeout: 15000 });
  // 账号 + 密码输入框（注册表单亦有同类输入框，登录卡先渲染）
  const inputs = page.locator('.login-card input').first();
  await inputs.fill('admin');
  await page.locator('.login-card input[type="password"]').first().fill('Admin@1234');
  await page.locator('.login-card button').first().click();
  await page.waitForTimeout(2000);
}

// 移动端后台核对：汉堡可见 → 侧栏默认移出屏外 → 点击滑入 + 遮罩出现 → 点菜单关闭 → 点遮罩关闭 → 无水平溢出
test('移动端后台：侧边栏转抽屉（汉堡唤起/遮罩关闭/无溢出）', async ({ page }) => {
  await login(page, 'admin');
  await page.waitForSelector('.admin-shell', { timeout: 15000 });
  await page.waitForTimeout(1200);

  // 汉堡按钮可见
  const toggle = page.locator('.admin-nav-toggle');
  await expect(toggle).toBeVisible();
  await expect(toggle).toBeVisible(); // display:inline-flex（桌面隐藏）
  const toggleDisplay = await toggle.evaluate((el) => getComputedStyle(el).display);
  console.log('汉堡 display:', toggleDisplay);
  expect(toggleDisplay).not.toBe('none');

  // 侧边栏默认移出屏外
  const side = page.locator('.admin-side');
  const x0 = await side.evaluate((el) => el.getBoundingClientRect().x);
  console.log('侧边栏初始 x:', x0);
  expect(x0).toBeLessThan(0);

  // 点击汉堡 → 抽屉滑入
  await toggle.click();
  await page.waitForTimeout(400);
  const x1 = await side.evaluate((el) => el.getBoundingClientRect().x);
  console.log('侧边栏打开后 x:', x1);
  expect(x1).toBeGreaterThanOrEqual(0);
  await expect(page.locator('.admin-side-mask')).toBeVisible();

  // 点击菜单项 → 抽屉关闭（点击非当前激活项，onChange 触发关闭）
  await page.locator('.admin-side .t-menu__item').nth(1).click();
  await page.waitForTimeout(400);
  const x2 = await side.evaluate((el) => el.getBoundingClientRect().x);
  expect(x2).toBeLessThan(0);

  // 重新打开，点遮罩关闭
  await toggle.click();
  await page.waitForTimeout(400);
  await page.locator('.admin-side-mask').click({ position: { x: 380, y: 400 } });
  await page.waitForTimeout(400);
  const x3 = await side.evaluate((el) => el.getBoundingClientRect().x);
  expect(x3).toBeLessThan(0);

  // 水平溢出检测
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth + 2);
  console.log('后台页水平溢出:', overflow);
  expect(overflow).toBe(false);
  await page.screenshot({ path: 'artifacts/mobile-admin.png' });
});

// 移动端工作台核对：输入栏可见、页面无水平溢出（验证 .chat-input-row 换行生效）
test('移动端工作台：输入栏可换行、无横向溢出', async ({ page }) => {
  await login(page, 'home');
  await page.waitForSelector('.chat-scroll, .app-header', { timeout: 15000 });
  await page.waitForTimeout(1500);
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth + 2);
  console.log('工作台水平溢出:', overflow);
  expect(overflow).toBe(false);
  // 输入栏存在
  await expect(page.locator('.chat-inputbar')).toBeVisible();
  await page.screenshot({ path: 'artifacts/mobile-workbench.png' });
});

// 移动端全站巡检：登录后依次访问自服务/工单/对照编辑各页 + 公开定价页，逐页断言无水平溢出
test('移动端全站页面无横向溢出巡检', async ({ page }) => {
  await login(page, 'home');
  await page.waitForSelector('.app-header', { timeout: 15000 });
  await page.waitForTimeout(1000);
  const routes = ['/billing', '/invites', '/packages', '/my', '/tickets', '/editor'];
  for (const r of routes) {
    await page.goto(`${BASE}${r}`, { waitUntil: 'networkidle' });
    await page.waitForTimeout(1200);
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth + 2);
    console.log(`${r} 水平溢出:`, overflow);
    expect(overflow, `${r} 不应横向溢出`).toBe(false);
  }
  // 公开定价页
  await page.goto(`${BASE}/pricing`, { waitUntil: 'networkidle' });
  await page.waitForTimeout(1500);
  const pricingOverflow = await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth + 2);
  console.log('/pricing 水平溢出:', pricingOverflow);
  expect(pricingOverflow, '/pricing 不应横向溢出').toBe(false);
});
