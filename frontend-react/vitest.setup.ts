// ============ vitest.setup.ts · 职责说明 ============
// Vitest 全局前置：为 node 环境补齐浏览器全局（localStorage），
// 使 i18n 模块（模块加载期读取 localStorage）可被安全导入。
// =============================================

// 内存版 localStorage 桩（仅需 getItem/setItem/removeItem 最小接口）
const store = new Map<string, string>()
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
