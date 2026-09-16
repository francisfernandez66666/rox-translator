import { defineConfig, devices } from '@playwright/test';

// Playwright E2E 冒烟配置（路线图 P2 前端 E2E）
// 安装：cd frontend-react && npm i -D @playwright/test && npx playwright install chromium
// 运行：npx playwright test
export default defineConfig({
  testDir: './e2e',
  timeout: 30000,
  fullyParallel: true,
  retries: 1,
  // ★ P2（2026-09-16）：动作/导航显式超时收敛——旧版仅靠全局 30s，慢响应时
  // 单条 expect 可占满超时；显式 actionTimeout 让失败更快暴露、减少 worker 拖尾。
  expect: { timeout: 10000 },
  use: {
    baseURL: process.env.BASE_URL || 'http://127.0.0.1:5173',
    actionTimeout: 12000,
    navigationTimeout: 20000,
    // retain-on-failure 比 on-first-retry 省：首败即留 trace，重试不重复采集
    trace: 'retain-on-failure',
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
});
