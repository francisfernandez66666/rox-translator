import { defineConfig, devices } from '@playwright/test';

// ============================================================================
// playwright.manual.config.ts — 手工探针专用配置（★ 2026-09-22，修 e2e-manual 不可运行）
//
// 为什么单独一份：AGENTS.md 一.6 规定「需要人工环境（生产探针、手工 Token）的用例一律放
// frontend-react/e2e-manual/，并在文件头加 test.skip(!env) 守卫」。但主配置
// playwright.config.ts 的 testDir 是 './e2e'，`npx playwright test e2e-manual/x.spec.ts`
// 只会得到 "No tests found"（本仓 Playwright 版本也没有 --dir 覆盖项）——
// 也就是说 e2e-manual 里的用例此前**只能靠改主配置才能跑**，等于事实上的死目录。
//
// 用法（用例自带 env 守卫，缺 env 时整文件 skip，不会误红）：
//   REAL_LLM_BASE=https://<host> REAL_LLM_KEY=<key> \
//     npx playwright test -c playwright.manual.config.ts
//   ASSIST_TOK=<tok> npx playwright test -c playwright.manual.config.ts assist_admin_probe
//
// 与主配置的差异（刻意）：
//   - testDir 指向 ./e2e-manual；
//   - retries: 0 —— 手工探针要的是「一次真实结论」，重试会把偶发的真缺陷洗成绿；
//   - timeout 放宽到 120s —— 真模型/真站点的往返延迟不是本地 mock 的量级；
//   - 不配 webServer：本目录的用例只打已部署环境，绝不自起本地服务。
// ============================================================================
export default defineConfig({
  testDir: './e2e-manual',
  timeout: 120000,
  fullyParallel: false, // 真环境探针串行跑，避免并发把限流/配额打满造成假失败
  retries: 0,
  workers: 1,
  expect: { timeout: 30000 },
  use: {
    baseURL: process.env.REAL_LLM_BASE || process.env.BASE_URL || 'http://127.0.0.1:5173',
    actionTimeout: 30000,
    navigationTimeout: 60000,
    trace: 'retain-on-failure',
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
});
