import { defineConfig, devices } from '@playwright/test';

// Playwright E2E 冒烟配置（路线图 P2 前端 E2E）
// 安装：cd frontend-react && npm i -D @playwright/test && npx playwright install chromium
// 运行：npx playwright test
export default defineConfig({
  testDir: './e2e',
  timeout: 30000,
  fullyParallel: true,
  retries: 1,
  use: {
    baseURL: process.env.BASE_URL || 'http://127.0.0.1:5173',
    trace: 'on-first-retry',
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
});
