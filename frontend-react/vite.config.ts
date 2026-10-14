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
  //   ★ 不带该 env 时**完全不碰命名**——注意这条「不碰」有实现坑（2026-10-12 首跑真踩）：
  //   原来写的是 `output: { entryFileNames: undefined }`，键**存在但值为 undefined** 时
  //   Vite 不再补自己的默认（`assets/[name]-[hash].js`），Rollup 退到它的默认 `[name].js`，
  //   产物当场变成**根目录固定名 `/index.js`**（无哈希、不在 /assets/ 下）＝
  //   ① 名字不随内容变，immutable 长缓存彻底失效；② 它落在 Caddy `/assets/*` 那条
  //   「文件真在才发长缓存」的射程外，只能按 no-cache 每次回源；③ 换源脚本按名找入口会找空。
  //   正确形态＝**不设 env 时整段 rollupOptions 都不出现**（下面用条件展开），
  //   机械锁在 scripts/check_frontend_tar.sh 的 T5 腿（入口必须带哈希且在 /assets/ 下）。
  //   根因那一层在 Caddy 侧（header 必须只对 200 生效），见 deploy/caddy/translator.conf 的 /assets 段。
  //
  // ★ 2026-10-14 这一档从「只改入口名」扩到「入口＋静态资产名」，起因是预压缩侧车事故：
  //   `.br` 用了 brotli 大窗口参数，浏览器一律解不开 ⇒ 入口脚本与首页那份 CSS 的 **br 变体**
  //   被 Cloudflare 按 `immutable, max-age=31536000` 钉在边缘（实测 cf-cache-status: HIT、age 23236s）。
  //   修好侧车、换上新文件**也救不回那两个 URL**——边缘只认「URL＋Accept-Encoding」这把缓存键。
  //   所以必须换名逃生，而首页 `index.html` 里恰好引用两样东西：
  //     `src="/assets/index-<hash>.js"` ＋ `href="/assets/index-<hash>.css"`
  //   （实测 uniq 就这两条；语种 chunk 与路由 chunk 是运行时才动态 import 的，
  //    而入口解不开时应用从没启动过 ⇒ 那些 chunk 压根没被浏览器要过，边缘里没有它们的坏变体，不用 churn）。
  //   只换入口会留下那份**同样被钉死的 CSS**，页面变成"脚本活了、样式还是 404 级失败"＝半修。
  //   ⇒ `assetFileNames` 一起加标记。它必须与 `entryFileNames` **同一个条件分支里整段展开**，
  //     不许学 10-12 那次写成"键在值为 undefined"（那会让 Vite 关掉默认命名，见上面那段坑）。
  //   ⚠️ `chunkFileNames` 刻意**不设**：动态 chunk 不在被污染的那一批里，而发版执行体
  //     `deploy/swap_web_and_binary.sh` 的语种 chunk 判据按 `^[a-z]{2}(-[A-Za-z]+)?-[A-Za-z0-9_-]{8}\.js$`
  //     数份数——给 chunk 名字插一段标记会把那条锁一起打断，为了不存在的问题去动发版闸门，方向错了。
  build: process.env.CACHE_BUST
    ? {
        rollupOptions: {
          output: {
            entryFileNames: `assets/index-${process.env.CACHE_BUST}-[hash].js`,
            assetFileNames: `assets/[name]-${process.env.CACHE_BUST}-[hash][extname]`,
          },
        },
      }
    : {},
})
