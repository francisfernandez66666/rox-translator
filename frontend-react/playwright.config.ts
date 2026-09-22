import { defineConfig, devices } from '@playwright/test';

// Playwright E2E 冒烟配置（路线图 P2 前端 E2E）
// 安装：cd frontend-react && npm i -D @playwright/test && npx playwright install chromium
// 运行：npx playwright test
export default defineConfig({
  // testDir 只圈 ./e2e：需要人工环境（生产探针、手工 Token）的用例放 e2e-manual/，
  // 落在本目录之外才不会被默认矩阵捞起（见 AGENTS.md「e2e 断言红线」）。
  testDir: './e2e',
  // ★ #66（2026-09-22 用户反馈「加载动效要久一点，至少读完三个语言再载入」）：
  //   冷启动占位现在要演满 3 个语种拍次（约 9s）才放行进工作台，
  //   旧的 30s/10s 档会在「种 token → goto → 断言工作台」这类用例上贴着边界翻红，
  //   故整档放宽；显式传 timeout 的用例（如翻译回流 60s）不受影响。
  timeout: 60000,
  // 互不串会话（token 由 addInitScript 种进本 context 的 sessionStorage），因此可以完全并行；
  // 共享的只有 BASE_URL 指向的那一个后端。
  fullyParallel: true,
  // 重试 1 次兜的是环境抖动（后端刚起未预热、mock LLM 首请求慢、异步工单排队），
  // 不是用来掩盖产品缺陷：真正的回归由断言口径本身保证（失败即留 trace，见下方 trace 选项）。
  retries: 1,
  // ★ P2（2026-09-16）：动作/导航显式超时收敛——旧版仅靠全局 30s，慢响应时
  // 单条 expect 可占满超时；显式 actionTimeout 让失败更快暴露、减少 worker 拖尾。
  // expect 25s < 全局 60s 是有意的分层：断言级先红能直接指出「哪一条没等到」，
  // 用例级超时兜的是「闸门/异步流程整体卡死」，两者都留了 #66 那 9s 的冷启动余量。
  expect: { timeout: 25000 },
  use: {
    // baseURL 默认 5173（本地 vite dev）；UAT/CI 一律用 BASE_URL 注入 8899 这类真实联调地址，
    // 故 e2e 文件里都用相对路径 goto('/tickets')，不写死任何外部域名（AGENTS.md「e2e 断言红线」）。
    baseURL: process.env.BASE_URL || 'http://127.0.0.1:5173',
    actionTimeout: 15000,
    navigationTimeout: 25000,
    // ★ 2026-09-20：钉 zh-CN 浏览器语言。i18n 冷启动改为「无 app_lang 时按浏览器语言自动检测」
    // （反馈④外国人可读），Playwright 默认 en-US 会把所有中文断言的存量用例整批翻红；
    // 需要验证其他语种自动检测的用例在文件内用 test.use({ locale }) 显式覆盖。
    // 同一口径在单测侧由 vitest.setup.ts 预置 app_lang=zh 兜住：两处钉的是同一个风险
    // ——首访自动检测读的是宿主/浏览器环境，测试机上不唯一，不钉就会随机翻红。
    locale: 'zh-CN',
    // retain-on-failure 比 on-first-retry 省：首败即留 trace，重试不重复采集
    trace: 'retain-on-failure',
  },
  // 只跑 chromium 一个 project（后端联调型 e2e，不铺多浏览器矩阵，省 CI 时间与端口占用）。
  // Desktop Chrome 的 1280×720 视口是多条「字号放大后不许折行」锁的成立前提
  // （见 e2e/pixel_uat.spec.ts P2b：换行会把控件实高顶过 46px 而翻红），
  // 改视口宽度等于改这些锁的判据，必须同步复查那两个 e2e 文件。
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
});
