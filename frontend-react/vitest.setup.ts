// ============ vitest.setup.ts · 职责说明 ============
// Vitest 全局前置：为 node 环境补齐浏览器全局（localStorage），
// 使 i18n 模块（模块加载期读取 localStorage）可被安全导入。
// =============================================

// 内存版 localStorage 桩（仅需 getItem/setItem/removeItem 最小接口）
const store = new Map<string, string>()
// ★ 2026-09-20 测试默认钉 zh：i18n 冷启动在无 app_lang 时改走「浏览器语言自动检测」，
// 测试环境 navigator 语言标不可控（jsdom=en-US / node=无），不钉死会让大批
// 断言中文文案的用例随机翻红；需要其他语种的用例自行 setLang 或 removeItem。
store.set('app_lang', 'zh')
;(globalThis as Record<string, unknown>).localStorage = {
  getItem: (k: string) => (store.has(k) ? store.get(k)! : null),
  setItem: (k: string, v: string) => { store.set(k, String(v)) },
  removeItem: (k: string) => { store.delete(k) },
  clear: () => { store.clear() },
  key: (i: number) => Array.from(store.keys())[i] ?? null,
  get length() { return store.size },
}

// ★ F11 扩展：sessionStorage / window 最小桩（api/core 与组件链模块顶层读取）
const sstore = new Map<string, string>()
;(globalThis as Record<string, unknown>).sessionStorage = {
  getItem: (k: string) => (sstore.has(k) ? sstore.get(k)! : null),
  setItem: (k: string, v: string) => { sstore.set(k, String(v)) },
  removeItem: (k: string) => { sstore.delete(k) },
  clear: () => { sstore.clear() },
}
if (typeof (globalThis as Record<string, unknown>).window === "undefined") {
  ;(globalThis as Record<string, unknown>).window = {
    location: { href: 'http://localhost/' },
    matchMedia: () => ({ matches: false, addEventListener: () => { /* noop */ } }),
    addEventListener: () => { /* noop */ },
    removeEventListener: () => { /* noop */ },
    innerWidth: 1280,
  }
}

// ★ 2026-10-17 批 A（后台面板词条改懒加载）：大批组件用例直接渲染后台面板并断言中文文案，
//   而面板词条现在要 `ensureAppPanels()` 才到手 —— 这里统一当作"已到位"，
//   免得上百条断言各自补一次 await（在测试里逐条补，等于把"懒加载"这件事测成"永不发生"）。
//   懒加载本身的口径由 src/i18n/appPanelSplit.test.ts 守，那一份用 `vi.resetModules()` 取**新实例**
//   做判据，刻意绕过这里的合并 —— 所以这里不是"把闸门关掉"，而是把两种用例的前提分开。
//   ⚠️ 必须**动态** import：静态 import 会被 ESM 提升到文件最前，于是 i18n 模块在上面那句
//   `store.set('app_lang','zh')` **之前**就评估完了 —— 它读不到存值、走浏览器检测拿到 en，
//   全站断言中文文案的用例会集体翻红（本批首跑真踩：127 条红，读数全是英文回退值）。
//   ⚠️ 被测代码里那扇门仍然在：App 外壳（src/App.tsx 的 Root）对已登录访客照旧等它。
const { ensureAppPanels } = await import('./src/i18n/index')
await ensureAppPanels()
