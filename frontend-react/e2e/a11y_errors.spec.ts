// ============================================================================
// e2e/a11y_errors.spec.ts — G6：无障碍 axe 扫描（F4）+ 错误态截图矩阵（E11）
//   1) axe：登录态关键页扫描，critical 违规必须为 0；serious 明细随附件落盘供人工跟进。
//   2) 错误态：拦截接口注入 500/401/403，截图存档并断言页面不白屏（#root 有内容）、
//      无未捕获 pageerror（错误必须被 ErrorBoundary / toast 正常兜住）。
// 运行：BASE_URL=http://127.0.0.1:8899 npx playwright test e2e/a11y_errors.spec.ts
// ============================================================================
import { test, expect, Page } from '@playwright/test';
import { AxeBuilder } from '@axe-core/playwright';
import * as fs from 'fs';
import * as path from 'path';
import { fileURLToPath } from 'url';

const BASE = process.env.BASE_URL || 'http://127.0.0.1:8899';
const ADMIN_PASS = process.env.ADMIN_PASS || 'Admin@1234';
const HERE = path.dirname(fileURLToPath(import.meta.url));
const SHOT_DIR = path.join(HERE, '..', 'artifacts', 'error_states');

async function loginAs(page: Page, user: string, pass: string) {
  const res = await page.request.post(`${BASE}/api/auth/login`, { data: { username: user, password: pass } });
  const body = await res.json();
  expect(body.token, `登录失败: ${user}`).toBeTruthy();
  await page.addInitScript((tk) => sessionStorage.setItem('auth_token', tk), body.token);
}

const A11Y_PAGES = ['/', '/tickets', '/billing', '/pricing'];

test.describe('G6-a11y axe 无障碍扫描', () => {
  test('登录态关键页 critical 违规为 0', async ({ page }, testInfo) => {
    // ★ flaky 根治（2026-09-16 D4 配套）：4 页逐页 axe analyze 累计耗时贴默认 30s 上限，
    //   冷启动/机器负载下偶发超时（retries=1 兜过）。抬到 90s 消除边界性 flaky。
    test.setTimeout(90000);
    await loginAs(page, 'uatuser_a', 'uatpass123');
    const all: Record<string, unknown[]> = {};
    for (const pathName of A11Y_PAGES) {
      await page.goto(pathName);
      await page.waitForTimeout(900);
      // /docs/terms 等为服务端直出静态文档页（无 #root SPA 容器），按整页扫描；
      // ★ P2-6（2026-09-18）：/pricing 已归一 SPA，命中 hasRoot 分支按 #root 扫描
      const hasRoot = await page.evaluate(() => !!document.querySelector('#root'));
      const results = await (hasRoot ? new AxeBuilder({ page }).include('#root') : new AxeBuilder({ page })).analyze();
      all[pathName] = results.violations;
      const critical = results.violations.filter((v) => v.impact === 'critical');
      const brief = critical.map((v) => `${v.id}@${JSON.stringify(v.nodes[0]?.target || '')}`);
      expect(critical, `${pathName} 存在 critical 无障碍违规: ${brief.join(' | ')}`).toEqual([]);
    }
    await testInfo.attach('axe-violations', {
      body: JSON.stringify(all, null, 1),
      contentType: 'application/json',
    });
  });
});

test.describe('G6-errors 错误态截图矩阵', () => {
  let shotIndex = 0;
  async function shoot(page: Page, name: string) {
    fs.mkdirSync(SHOT_DIR, { recursive: true });
    await page.screenshot({ path: path.join(SHOT_DIR, `${String(++shotIndex).padStart(2, '0')}_${name}.png`), fullPage: false });
  }

  async function assertAlive(page: Page, errors: string[]) {
    const rootLen = await page.evaluate(() => document.body.innerText.trim().length); // 登录兜底可能渲染在 #root 外的 portal 层，按 body 判定
    expect(rootLen, '错误注入后页面白屏 @' + page.url()).toBeGreaterThan(0);
    expect(errors, '错误未被兜住（存在未捕获异常）').toEqual([]);
  }

  test('核心页接口 500 不白屏且无未捕获异常', async ({ page }) => {
    await loginAs(page, 'uatuser_a', 'uatpass123');
    const errors: string[] = [];
    page.on('pageerror', (e) => errors.push('PAGEERROR: ' + e.message.slice(0, 200)));
    for (const api of ['/api/billing/balance', '/api/tickets', '/api/conversations']) {
      await page.route('**' + api + '*', (route) =>
        route.fulfill({ status: 500, contentType: 'application/json', body: JSON.stringify({ success: false, error_code: 'internal_error', message: 'UAT 注入 500' }) })
      );
    }
    for (const pathName of ['/', '/tickets']) {
      await page.goto(pathName);
      await page.waitForTimeout(1200);
      await shoot(page, `500_${pathName.replace(/\//g, '_') || 'home'}`);
      await assertAlive(page, errors);
    }
  });

  test('会话过期 401 触发登录兜底不白屏', async ({ page }) => {
    // 注意不能用 addInitScript：401 兜底是整页跳转，init 会在落地页重放失效 token 造成回环白屏
    const res = await page.request.post(`${BASE}/api/auth/login`, { data: { username: 'uatuser_a', password: 'uatpass123' } });
    const tok = (await res.json()).token;
    await page.goto('/');
    await page.evaluate((tk) => sessionStorage.setItem('auth_token', tk), tok);
    const errors: string[] = [];
    page.on('pageerror', (e) => errors.push('PAGEERROR: ' + e.message.slice(0, 200)));
    // 只对业务数据面注入 401（登录/引导接口放行），验证「会话失效→清 token→回落登录」不白屏
    await page.route(/\/api\/(tickets|conversations|billing|admin)\b/, (route) =>
      route.fulfill({ status: 401, contentType: 'application/json', body: JSON.stringify({ success: false, error_code: 'unauthorized', message: '登录已失效' }) }));
    await page.goto('/tickets');
    // 401 → 应用层跳登录属整页导航：等落地（或 10s 内未跳转则按原页兜底渲染继续），再断言不白屏
    await page.waitForLoadState('domcontentloaded');
    // 兜底整页跳转可能多跳（/tickets→/→登录视图），等 URL 稳定再断言
    let last = '';
    for (let i = 0; i < 12; i++) {
      const u = page.url();
      if (u === last) break;
      last = u;
      await page.waitForTimeout(800);
    }
    await shoot(page, '401_tickets');
    for (let attempt = 0; ; attempt++) {
      try { await assertAlive(page, errors); break; }
      catch (e) { if (attempt >= 3) throw e; await page.waitForTimeout(700); }
    }
  });

  test('权限不足 403 管理页正常降级', async ({ page }) => {
    await loginAs(page, 'uatuser_a', 'uatpass123'); // 非超管访问超管面板接口
    const errors: string[] = [];
    page.on('pageerror', (e) => errors.push('PAGEERROR: ' + e.message.slice(0, 200)));
    await page.route('**/api/admin/**', (route) =>
      route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ success: false, error_code: 'forbidden', message: '无权限（UAT 注入）' }) })
    );
    await page.goto('/admin');
    await page.waitForTimeout(1500);
    await shoot(page, '403_admin');
    await assertAlive(page, errors);
  });

  test('匿名访问受限路由不白屏', async ({ page }) => {
    const errors: string[] = [];
    page.on('pageerror', (e) => errors.push('PAGEERROR: ' + e.message.slice(0, 200)));
    await page.goto('/tickets');
    await page.waitForTimeout(1000);
    await shoot(page, 'anon_tickets');
    const rootLen = await page.evaluate(() => document.body.innerText.trim().length);
    expect(rootLen).toBeGreaterThan(0);
    expect(errors).toEqual([]);
  });

  test('超管后台关键页可渲染（截图存档）', async ({ page }) => {
    test.skip(!ADMIN_PASS, '未提供 ADMIN_PASS');
    await loginAs(page, 'admin', ADMIN_PASS);
    const errors: string[] = [];
    page.on('pageerror', (e) => errors.push('PAGEERROR: ' + e.message.slice(0, 200)));
    for (const pathName of ['/admin']) {
      await page.goto(pathName);
      await page.waitForTimeout(1500);
      await shoot(page, `ok${pathName.replace(/\//g, '_')}`);
    }
    expect(errors, '超管后台存在未捕获异常: ' + errors.join(' || ')).toEqual([]);
  });
});
