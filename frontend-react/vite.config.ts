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
      // ★ autosales：ai-assist 独立服务（AI 销售/客服），开发期代理到 8790
      '/assist-api': { target: 'http://127.0.0.1:8790', changeOrigin: true, rewrite: (p) => p.replace(/^\/assist-api/, '') },
    },
  },
  // ★ 2026-10-12 现网抓到的**交付链缺陷**留的逃生阀，不是日常开关：
  //   `/assets/*` 的 `Cache-Control: public, max-age=31536000, immutable` 在 Caddy 里**不分成败一律照发**，
  //   于是**一次 404 也会被 Cloudflare 钉在边缘一整年**（现网实证：换源前探测新入口名拿到 404，
  //   换源后该 URL 仍回 `HTTP/2 404`＋`cf-cache-status: HIT`＋`age: 6494`＋那条 immutable，
  //   同一 URL 带 `?cb=1` 立刻 200/605,065 B ⇒ 洞在 CDN 缓存里，origin 与磁盘都是好的）。
  //   修法有两层，这一档只管救急：**换 URL**——内容一字未变也能得到一个 CDN 从没缓存过的入口名。
  //     用法 `CACHE_BUST=<标记> npx vite build`（标记建议用日期，如 `20261012`）。
  //   ★ 不带该 env 时**完全不碰命名**（undefined ⇒ Vite 默认 `assets/index-[hash].js`，构建仍确定性、
  //   同源码两次构建同产物）；带了 env 也只改**入口**那一份的名字，语种 chunk／异步 chunk 一律不动。
  //   根因那一层在 Caddy 侧（header 必须只对 200 生效），见 deploy/caddy/translator.conf 的 /assets 段。
  build: {
    rollupOptions: {
      output: {
        entryFileNames: process.env.CACHE_BUST
          ? `assets/index-${process.env.CACHE_BUST}-[hash].js`
          : undefined,
      },
    },
  },
})
