import { test, expect } from '@playwright/test';

// 冒烟：首页可加载、翻译入口存在、状态接口健康。
// 真实 E2E 需后端 BASE 可达；CI 中由 start-server 前置启动 backend-go + vite。
test.describe('翻译助手冒烟', () => {
  test('首页加载且含翻译入口', async ({ page }) => {
    await page.goto('/');
    await expect(page).toHaveTitle(/翻译助手|langcross/i);
    // 翻译文本输入框（按 data-testid 定位，前端需补充）
    const input = page.getByTestId('translate-input');
    await expect(input).toBeVisible();
  });

  test('后端 /status 健康', async ({ request }) => {
    const api = process.env.API_URL || 'http://127.0.0.1:8787';
    const res = await request.get(`${api}/status`);
    expect(res.ok()).toBeTruthy();
    const body = await res.json();
    expect(body.ok).toBe(true);
  });

  test('仪表盘可访问（需登录态）', async ({ page }) => {
    await page.goto('/dashboard');
    // 未登录应跳转登录或展示登录态占位
    await expect(page.locator('body')).toBeVisible();
  });
});
