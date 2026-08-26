import { fileURLToPath, URL } from 'node:url'
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// 与 Vue 版 vite 配置完全对齐（仅插件与端口不同：5174 避免与旧前端并存冲突）
export default defineConfig({
  plugins: [react()],
  base: '/',
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  server: {
    port: 5174,
    allowedHosts: true,
    proxy: {
      '/api': { target: 'http://127.0.0.1:8787', changeOrigin: true },
      '/openapi': { target: 'http://127.0.0.1:8787', changeOrigin: true },
      '/office': { target: 'http://127.0.0.1:8787', changeOrigin: true },
      '/metrics': { target: 'http://127.0.0.1:8787', changeOrigin: true },
      '/status': { target: 'http://127.0.0.1:8787', changeOrigin: true },
    },
  },
})
