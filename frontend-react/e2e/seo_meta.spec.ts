// ============================================================================
// e2e/seo_meta.spec.ts — DEF-06 服务端/客户端 SEO 路由 meta（2026-10-24）
//
// 锁三件事：
//   S1 五条公开路由 title 不再全站同一句；
//   S2 canonical 各指自身路径，且带 ?lang=<当前语种>（语言版本稳定地址）；
//   S3 内页含 description/og:url，且 /pricing、/compare 可收录（robots 不是 noindex）。
// 不依赖登录/后端数据；只读页面 head。需要先起前端 dev/preview（矩阵由 run_uat 负责）。
// ============================================================================
import { test, expect } from '@playwright/test'

const ROUTES = ['/', '/pricing', '/compare', '/login', '/register']

test.describe('SEO route meta', () => {
  test('S1/S2/S3 五条公开路由 title 独立、canonical 自指且带 lang', async ({ page }) => {
    const seen = new Map<string, string>()
    for (const path of ROUTES) {
      await page.goto(path, { waitUntil: 'domcontentloaded' })
      await page.waitForLoadState('networkidle').catch(() => {})
      const meta = await page.evaluate(() => ({
        title: document.title,
        canonical: document.querySelector('link[rel="canonical"]')?.getAttribute('href') || '',
        ogurl: document.querySelector('meta[property="og:url"]')?.getAttribute('content') || '',
        desc: document.querySelector('meta[name="description"]')?.getAttribute('content') || '',
        robots: document.querySelector('meta[name="robots"]')?.getAttribute('content') || '',
      }))
      expect(meta.title, `${path} title 为空`).toBeTruthy()
      expect(meta.canonical, `${path} canonical 缺失`).toContain(path === '/' ? '/?lang=' : `${path}?lang=`)
      expect(meta.ogurl, `${path} og:url 与 canonical 不一致`).toBe(meta.canonical)
      expect(meta.desc, `${path} description 为空`).toBeTruthy()
      seen.set(path, meta.title)
      if (path === '/pricing' || path === '/compare') {
        expect(meta.robots, `${path} 应为可收录`).not.toContain('noindex')
      }
    }
    // 至少 4 条不同 title（/login 与 /register 统一登录，允许同标题）
    expect(new Set(seen.values()).size).toBeGreaterThanOrEqual(4)
  })
})
