// ============================================================================
// e2e/pixel_uat.spec.ts — 前端像素级 UAT（前端↔后端联调 + 视觉/布局/关键链路）
// 覆盖：
//   P1 登录页 / 注册页可渲染
//   P2 工作台（对话翻译）输入→发送→收到译文（真实后端+mock LLM 全链路）
//   P3 自服务页：余额/邀请/套餐/账号
//   P4 工单 / 对照编辑
//   P5 后台管理各面板（overview/models/kb/tickets/system/personal）
//   P6 公开页：pricing / docs
//   P7 像素级检查：关键元素可见 + 无横向溢出（scrollWidth<=clientWidth）
// 全部截图存 artifacts/ 供人工复核
// 运行：BASE_URL=http://127.0.0.1:8899 npx playwright test e2e/pixel_uat.spec.ts
// ============================================================================
import { test, expect, Page } from '@playwright/test';

const BASE = process.env.BASE_URL || 'http://127.0.0.1:8899';
// 截图小件：把页面截图落 artifacts/<name>.png，供人工复核像素渲染
const shot = (p: Page, name: string) => p.screenshot({ path: `artifacts/${name}.png`, fullPage: false });

// 登录态注入：API 登录拿 token → localStorage 种入（与产品实际登录态等价）
async function login(page: Page, user = 'uatuser_a', pass = 'uatpass123') {
  const res = await page.request.post(`${BASE}/api/auth/login`, { data: { username: user, password: pass } });
  const body = await res.json();
  expect(body.success, `登录失败:${JSON.stringify(body)}`).toBeTruthy();
  await page.addInitScript((tk) => localStorage.setItem('auth_token', tk), body.token);
}

test.describe('像素级 UAT', () => {
  test('P1 登录页渲染', async ({ page }) => {
    await page.goto('/');
    await expect(page.locator('body')).toBeVisible();
    const txt = await page.locator('body').innerText();
    expect(txt).toContain('登录');
    await shot(page, 'p1_login');
  });

  test('P2 工作台对话翻译全链路（发送→译文回流）', async ({ page }) => {
    await login(page);
    await page.goto('/');
    await expect(page.locator('body')).toBeVisible();
    const ta = page.getByPlaceholder(/输入要翻译的文本/);
    await expect(ta).toBeVisible();
    await ta.fill('今天天气怎么样，适合出门吗？');
    await page.getByRole('button', { name: '发送' }).click();
    await expect(page.getByText(/TranslatedEN|翻译结果/).first()).toBeVisible({ timeout: 60000 });
    await shot(page, 'p2_workbench');
  });

  test('P3 自服务页渲染（余额/邀请/套餐/账号）', async ({ page }) => {
    await login(page);
    for (const [path, expectTxt, name] of [
      ['/billing', /余额|token|充值/i, 'p3_billing'],
      ['/invites', /邀请|裂变|奖励/i, 'p3_invites'],
      ['/packages', /套餐|包|订阅/i, 'p3_packages'],
      ['/my', /账号|昵称|邮箱/i, 'p3_my'],
    ] as const) {
      await page.goto(path);
      await expect(page.locator('body')).toBeVisible();
      const txt = await page.locator('body').innerText();
      expect(txt, `${path} 应含 ${expectTxt}`).toMatch(expectTxt);
      const overflow = await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth + 2);
      expect(overflow, `${path} 横向溢出`).toBeFalsy();
      await shot(page, name);
    }
  });

  test('P4 工单页与对照编辑渲染', async ({ page }) => {
    await login(page);
    for (const [path, name] of [['/tickets', 'p4_tickets'], ['/editor', 'p4_editor']] as const) {
      await page.goto(path);
      await expect(page.locator('body')).toBeVisible();
      const overflow = await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth + 2);
      expect(overflow, `${path} 横向溢出`).toBeFalsy();
      await shot(page, name);
    }
  });

  test('P5 后台管理面板渲染（超管）', async ({ page }) => {
    await login(page, 'admin', 'Admin@1234');
    await page.goto('/admin');
    await expect(page.locator('body')).toBeVisible();
    await shot(page, 'p5_admin_overview');
    const panels: [string, RegExp, string][] = [
      ['模型配置', /模型|路由|密钥/i, 'p5_admin_models'],
      ['知识库', /知识|术语|包/i, 'p5_admin_kb'],
      ['工单管理', /工单/i, 'p5_admin_tickets'],
      ['系统设置', /系统|配置/i, 'p5_admin_system'],
      ['个人中心', /邀请|任务|我的/i, 'p5_admin_personal'],
    ];
    for (const [label, rx, name] of panels) {
      const item = page.getByText(label, { exact: true }).first();
      if (await item.count()) { await item.click(); } else { continue; }
      await page.waitForTimeout(400);
      const txt = await page.locator('body').innerText();
      expect(txt, `${label} 面板应含 ${rx}`).toMatch(rx);
      const overflow = await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth + 2);
      expect(overflow, `${label} 横向溢出`).toBeFalsy();
      await shot(page, name);
    }
  });

  test('P6 公开页渲染（价格/文档）', async ({ page }) => {
    for (const [path, name] of [['/pricing', 'p6_pricing'], ['/docs/terms', 'p6_terms']] as const) {
      await page.goto(path);
      await expect(page.locator('body')).toBeVisible();
      const txt = await page.locator('body').innerText();
      expect(txt.length, `${path} 空页面`).toBeGreaterThan(20);
      const overflow = await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth + 2);
      expect(overflow, `${path} 横向溢出`).toBeFalsy();
      await shot(page, name);
    }
  });

  test('P7 整体无横向溢出（登录态关键页）', async ({ page }) => {
    await login(page);
    for (const path of ['/', '/tickets', '/editor', '/billing', '/admin']) {
      await page.goto(path);
      await page.waitForTimeout(300);
      const overflow = await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth + 2);
      expect(overflow, `${path} 横向溢出`).toBeFalsy();
    }
  });
});
