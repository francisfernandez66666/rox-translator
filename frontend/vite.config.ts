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
    allowedHosts: true,
    proxy: {
      '/api': {
        target: 'http://127.0.0.1:8787',
        changeOrigin: true,
      },
      // 开放 API（含 /openapi/docs 文档页）：不代理会落入 SPA 兜底返回 index.html，
      // 导致管理后台「API Key → 查看文档」新开页显示异常
      '/openapi': {
        target: 'http://127.0.0.1:8787',
        changeOrigin: true,
      },
      // Office 划词插件页面与监控端点：开发环境与生产 Caddy 反代行为对齐
      '/office': {
        target: 'http://127.0.0.1:8787',
        changeOrigin: true,
      },
      '/metrics': {
        target: 'http://127.0.0.1:8787',
        changeOrigin: true,
      },
      '/status': {
        target: 'http://127.0.0.1:8787',
        changeOrigin: true,
      },
    },
    hmr: {
      overlay: false,
    },
  },
})
