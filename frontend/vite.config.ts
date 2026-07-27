// ============================================================================
// vite.config.ts — Vite 构建配置
// ============================================================================
// 【关键配置】
// 1. Vue 插件：让 Vite 能处理 .vue 文件
// 2. 路径别名：@ → src/，方便 import
// 3. 开发代理：前端 5173 → 后端 8000，解决跨域问题
// ============================================================================

import { fileURLToPath, URL } from 'node:url'
import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig({
  plugins: [
    vue(),
  ],
  base: '/',
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  server: {
    port: 5173,
    proxy: {
      '/api': {
        target: 'http://127.0.0.1:8000',
        changeOrigin: true,
      },
    },
    hmr: {
      overlay: false,
    },
  },
})
