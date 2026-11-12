// ============ vitest.config.ts · 职责说明 ============
// Vitest 单元测试配置：node 环境（测试对象为纯 TS 模块，无需浏览器 DOM），
// 覆盖 i18n 字典中英键值对等性等静态逻辑校验。
//
// ★★ 2026-11-01（DEF-P2-08）：显式设 testTimeout / hookTimeout，并把「扫描型静态锁」归入独立 project。
//   由来：默认 testTimeout=5000ms，而下面 SCAN_TESTS 那批文件要**遍历整个 src（有的还跨仓扫 backend-go）**，
//   在慢机器／CI 争用时会在"用例本身没错"的情况下超时假红——而红灯被习惯性忽略，
//   真正的回归就混在里面漏掉（同一族还有 §三 那条"钝化闸门敏感度"）。
//   ⚠️ 诚实记账：本机 2026-11-01 复跑 ticketSource.test.ts 只要 180ms，原报告的 23–162 秒
//   出自磁盘 97% 满那台机器 ⇒ **本条是防御性加固，不是在修一条正在发作的假红**。
//   两个 project 的超时分开给，是因为"真挂死"的用例如果也按 180s 等，发现成本太高：
//   unit 30s（够普通 jsdom 组件测试）、scan 180s（够全仓遍历）。
// =============================================
import { fileURLToPath, URL } from 'node:url'
import { defineConfig } from 'vitest/config'

// ★ 扫描型静态锁名单（判据：测试文件里出现目录遍历／跨语言现读全仓）。
//   这份名单**必须与 SCAN_TESTS 逐条等值**——由 src/vitestScanLayout.lock.test.ts 机械核对：
//   ① 新加一个含遍历的测试文件却不进这里 ⇒ 红（它在 30s 预算下会假红）；
//   ② 名单里留着一条已经不再遍历的退化条目 ⇒ 红（白占 180s 预算，且让"独立 project"变成迷信）。
export const SCAN_TESTS = [
  'src/lib/ticketSource.test.ts',
  'src/styles/readability.test.ts',
  'src/lib/navShim.test.ts',
  'src/lib/turnstileLoad.lock.test.ts',
  'src/bootStorageGate.test.ts',
  'src/i18n/missingKeyGate.test.ts',
  'src/i18n/auditActionCoverage.test.ts',
  'src/components/BrandTagline.lock.test.ts',
  'src/i18n/appPanelSplit.test.ts',
  'src/i18n/parity.test.ts',
  'src/i18n/brandName.test.ts',
  'src/i18n/noManualPromise.lock.test.ts',
  'src/i18n/hardcodedCjkGate.test.ts',
  'src/api/bizRespGate.test.ts',
  // ★ 2026-11-03 合规闸批：本锁跨语言现读（前端渲染 vs 后端 engine 常量的措辞等值、
  //   载荷两键与出栈键逐字核），是遍历型 ⇒ 必须吃 180s 预算而不是 30s。
  //   由 vitestScanLayout.lock.test.ts 当场抓出来（首跑就红在这里，属"新锁没进调度名单"，
  //   不是产品缺陷；这正是那条自指锁存在的理由）。
  'src/api/ticketsComplianceLock.test.ts',
  'src/components/admin/doubleConfirmGate.test.ts',
  // ★ 2026-11-06 一期收尾批：跨语言逐口现读（前端 product.ts 的 withQuery/postBody vs 后端
  //   handler 与助手的 URL.Query().Get／json 标签，还要花括号配平取体），是遍历型 ⇒ 180s 预算。
  'src/api/productQueryContract.lock.test.ts',
  // ★ 2026-11-06 H1/H3 批：这两把都是"遍历 src 全集"的静态锁
  //   （前者扫全部非测试源文件判"引导外壳是否只有一份实现"，后者跨文件现读 App.tsx 路由集 vs AiAssist 白名单）
  //   ⇒ 必须吃 180s 预算；漏进这里就是"新锁没进调度名单"，慢机器上会以随机假红的形态出现。
  'src/components/guidedchat/guidedChatSingleShell.lock.test.ts',
  // ★ 2026-11-11 三期 W1：A01 按目录枚举销售域页面全集（readdirSync），属遍历型 ⇒ 180s 预算。
  'src/ui/langcross/langcrossUiConformance.lock.test.ts',
  'src/vitestScanLayout.lock.test.ts', // 本锁自己就是遍历型的（自指腿：不在名单里就会拿 30s 预算去扫全仓）
]

const ALL = ['src/**/*.test.ts', 'src/**/*.test.tsx']
const root = fileURLToPath(new URL('.', import.meta.url))
// 别名只算一次，根配置与两个 project 各引同一份（写两遍迟早漂）
const alias = { '@': fileURLToPath(new URL('./src', import.meta.url)) }

export default defineConfig({
  // ★ F11：与被测组件源码共用 '@' 别名（MessageBubble 等模块级导入 '@/api'）
  resolve: { alias },
  test: {
    // ★ 2026-09-16：*.dom.test.tsx 走 jsdom（文件级 @vitest-environment 注释兜底），
    //   admin 组件级测试（PlansP 收银台）加入闸门；其余保持 node 环境。
    include: ALL,
    setupFiles: ['./vitest.setup.ts'],
    environment: 'node',
    testTimeout: 30000,
    hookTimeout: 60000,
    // 两个 project 各给一份 test 段（超时不同是有意的）；别名走同一个 alias 常量，不写第二份字面量。
    projects: [
      {
        // ★ 别名必须在 project 对象上再给一次（不是 extends: true —— 那会把根配置的 include 也并进来，
        //   结果"scan project"实际收了全部 176 个文件）。判据：跑 --project scan 必须只见到名单里那些文件。
        resolve: { alias },
        test: {
          name: 'unit',
          root,
          include: ALL,
          exclude: SCAN_TESTS,
          setupFiles: ['./vitest.setup.ts'],
          environment: 'node',
          testTimeout: 30000,
          hookTimeout: 60000,
        },
      },
      {
        resolve: { alias },
        test: {
          name: 'scan',
          root,
          include: SCAN_TESTS,
          setupFiles: ['./vitest.setup.ts'],
          environment: 'node',
          testTimeout: 180000,
          hookTimeout: 60000,
        },
      },
    ],
  },
})
