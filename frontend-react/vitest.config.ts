// ============ vitest.config.ts · 职责说明 ============
// Vitest 单元测试配置：node 环境（测试对象为纯 TS 模块，无需浏览器 DOM），
// 覆盖 i18n 字典中英键值对等性等静态逻辑校验。
// =============================================
import { fileURLToPath, URL } from 'node:url'
import { defineConfig } from 'vitest/config'

export default defineConfig({
  // ★ F11：与被测组件源码共用 '@' 别名（MessageBubble 等模块级导入 '@/api'）
  resolve: { alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) } },
  test: {
    environment: 'node',
    setupFiles: ['./vitest.setup.ts'],
    // ★ 2026-09-16：*.dom.test.tsx 走 jsdom（文件级 @vitest-environment 注释兜底），
    //   admin 组件级测试（PlansP 收银台）加入闸门；其余保持 node 环境。
    include: ['src/**/*.test.ts', 'src/**/*.test.tsx'],
  },
})
