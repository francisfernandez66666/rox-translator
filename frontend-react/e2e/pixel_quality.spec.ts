// ============================================================================
// e2e/pixel_quality.spec.ts — 像素级 UAT 补强：运行时健康体检
// 对全部关键页面采集：控制台 error、未捕获异常、网络请求失败（4xx/5xx 业务异常除外）
// 判定标准：任何 console.error / pageerror 即失败（前端代码运行时崩溃/告警）
// ============================================================================
import { test, expect, Page } from '@playwright/test';

const BASE = process.env.BASE_URL || 'http://127.0.0.1:8899';
const PAGES = ['/', '/tickets', '/editor', '/billing', '/invites', '/packages', '/my', '/pricing', '/docs/terms'];

async function login(page: Page) {
  const res = await page.request.post(`${BASE}/api/auth/login`, { data: { username: 'uatuser_a', password: 'uatpass123' } });
  const body = await res.json();
  await page.addInitScript((tk) => localStorage.setItem('auth_token', tk), body.token);
}

test.describe('运行时健康体检', () => {
  test('登录态关键页无控制台错误与请求失败', async ({ page }) => {
    await login(page);
    const errors: string[] = [];
    page.on('console', (m) => { if (m.type() === 'error') errors.push(m.text().slice(0, 200)); });
    page.on('pageerror', (e) => errors.push('PAGEERROR: ' + e.message.slice(0, 200)));
    page.on('requestfailed', (r) => errors.push('REQFAIL: ' + r.url().slice(0, 160)));
    for (const path of PAGES) {
      await page.goto(path);
      await page.waitForTimeout(600);
    }
    expect(errors.filter((e) => !e.includes('favicon')), '存在运行时错误: ' + errors.join(' || ')).toEqual([]);
  });

  test('匿名公开页无控制台错误', async ({ page }) => {
    const errors: string[] = [];
    page.on('console', (m) => { if (m.type() === 'error') errors.push(m.text().slice(0, 200)); });
    page.on('pageerror', (e) => errors.push('PAGEERROR: ' + e.message.slice(0, 200)));
    for (const path of ['/', '/pricing', '/docs/terms']) {
      await page.goto(path);
      await page.waitForTimeout(400);
    }
    expect(errors, '存在运行时错误: ' + errors.join(' || ')).toEqual([]);
  });
});
