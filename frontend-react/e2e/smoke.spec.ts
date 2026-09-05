import { test, expect } from '@playwright/test';

// 冒烟：首页可加载、翻译入口存在、状态接口健康。
// 真实 E2E 需后端 BASE 可达；CI 中由 start-server 前置启动 backend-go + vite。
// 运行：BASE_URL=<前端地址> API_URL=<后端地址> npx playwright test
const BASE = process.env.BASE_URL || 'http://127.0.0.1:5173';
const API = process.env.API_URL || 'http://127.0.0.1:8787';

test.describe('翻译助手冒烟', () => {
  test('首页加载且含翻译入口（登录态）', async ({ page }) => {
    // 工作台为登录后页面：先经 API 登录注入会话（等价真实登录）
    const res = await page.request.post(`${API}/api/auth/login`, {
      data: { username: 'uatuser_a', password: 'uatpass123' },
    });
    const body = await res.json();
    await page.addInitScript((tk) => localStorage.setItem('auth_token', tk), body.token);
    await page.goto('/');
    await expect(page).toHaveTitle(/翻译平台|翻译助手|langcross/i);
    const input = page.getByTestId('translate-input');
    await expect(input).toBeVisible();
  });

  test('后端 /status 健康', async ({ request }) => {
    const res = await request.get(`${API}/status`);
    expect(res.ok()).toBeTruthy();
    const body = await res.json();
    expect(body.ok).toBe(true);
  });

  test('未登录访问工作台渲染登录页', async ({ page }) => {
    await page.goto('/');
    await expect(page.locator('body')).toBeVisible();
    await expect(page.getByText(/登录/, { exact: false }).first()).toBeVisible();
  });
});
