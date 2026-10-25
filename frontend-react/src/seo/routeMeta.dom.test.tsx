// @vitest-environment jsdom
// ============ 本文件职责中文说明 ============
// DEF-06 客户端 route meta 单测：五条公开路由 title/canonical/robots 与 12 语种 hreflang。
// 服务端注入有 Go 侧锁；本文件锁客户端在 SPA 路由/语言变化后的同步行为。
// ============
import { beforeEach, describe, expect, it } from 'vitest'
import { syncRouteMeta } from './routeMeta'

describe('syncRouteMeta', () => {
  beforeEach(() => {
    document.head.innerHTML = '<title>old</title>'
    document.documentElement.removeAttribute('data-route-meta')
  })

  it('/pricing en 写入独立 title/canonical/robots/hreflang', () => {
    expect(syncRouteMeta('/pricing', 'en', 'LangCross')).toBe(true)
    expect(document.title).toContain('Pricing')
    expect(document.querySelector('link[rel="canonical"]')?.getAttribute('href')).toContain('/pricing?lang=en')
    expect(document.querySelector('meta[name="robots"]')?.getAttribute('content')).toBe('index,follow')
    expect(document.querySelectorAll('link[rel="alternate"][hreflang]').length).toBe(13)
    expect(document.documentElement.dataset.routeMeta).toBe('1')
  })

  it('五条公开路由 title 各不相同', () => {
    const titles = ['/', '/pricing', '/compare', '/login', '/register'].map((p) => {
      syncRouteMeta(p, 'zh', '能言 LangCross')
      return document.title
    })
    expect(new Set(titles).size).toBeGreaterThanOrEqual(4) // /login 与 /register 同为统一登录页
    expect(titles[0]).toContain('专业 AI 翻译')
    expect(titles[1]).toContain('定价')
  })

  // ★ 2026-10-25 对账补腿（方案 §六「前端：五条路由 title/canonical/og:url」）：
  //   此前只锁了 title/canonical/robots/hreflang，og:url 与 og:title 在实现里写了却没人问——
  //   "社交/IM 分享卡指向哪一页"全靠这两条，漏锁时表现是分享出去永远指向首页（无任何报错）。
  it('五条公开路由 og:url＝canonical 且 og:title＝title', () => {
    for (const p of ['/', '/pricing', '/compare', '/login', '/register']) {
      document.head.innerHTML = '<title>old</title>'
      expect(syncRouteMeta(p, 'zh', '能言 LangCross')).toBe(true)
      const canonical = document.querySelector('link[rel="canonical"]')?.getAttribute('href') || ''
      expect(canonical, `${p} 必须有 canonical`).toBeTruthy()
      expect(document.querySelector('meta[property="og:url"]')?.getAttribute('content'), `${p} og:url 必须等于 canonical`).toBe(canonical)
      expect(document.querySelector('meta[property="og:title"]')?.getAttribute('content'), `${p} og:title 必须等于 title`).toBe(document.title)
    }
  })

  it('app 路由 noindex 且不输出 hreflang', () => {
    syncRouteMeta('/tickets', 'zh', 'LangCross')
    expect(document.querySelector('meta[name="robots"]')?.getAttribute('content')).toBe('noindex,nofollow')
    expect(document.querySelectorAll('link[rel="alternate"][hreflang]').length).toBe(0)
  })

  // ★ 分享卡图（og:image）客户端那一半，与 Go 侧 TestInjectRouteMetaOGImage 同一把尺子：
  // 平台品牌域给**绝对 URL**（相对路径社交爬虫不解＝等于没有卡图），白标整族摘掉（图是平台的）。
  describe('og:image 分享卡族', () => {
    // 基线 HTML 带着「别处域名」的卡图：真实 index.html 里写死的是主站绝对 URL，
    // 演示站/其他 host 上必须被换成当前 origin，而不是并存两份（抓取方只认第一条）。
    const baseTags =
      '<meta property="og:image" content="https://other.example/og-image.png">' +
      '<meta property="og:image:width" content="1200"><meta property="og:image:height" content="630">' +
      '<meta name="twitter:image" content="https://other.example/og-image.png">' +
      '<meta name="twitter:card" content="summary">'

    const count = (sel: string) => document.head.querySelectorAll(sel).length

    it('平台品牌：绝对 URL 指向当前 origin，旧域名不残留、各条只一枚', () => {
      document.head.innerHTML = '<title>old</title>' + baseTags
      syncRouteMeta('/pricing', 'en', '能言 LangCross')
      const origin = window.location.origin
      expect(document.querySelector('meta[property="og:image"]')?.getAttribute('content')).toBe(`${origin}/og-image.png`)
      expect(document.querySelector('meta[name="twitter:image"]')?.getAttribute('content')).toBe(`${origin}/og-image.png`)
      expect(document.querySelector('meta[name="twitter:card"]')?.getAttribute('content')).toBe('summary_large_image')
      expect(document.head.innerHTML).not.toContain('other.example')
      // 重复的 og:image 抓取方只认第一条，所以「恰好一枚」才是判据
      expect(count('meta[property="og:image"]')).toBe(1)
      expect(count('meta[property="og:image:width"]')).toBe(1)
      expect(count('meta[name="twitter:image"]')).toBe(1)
      expect(count('meta[name="twitter:card"]')).toBe(1)
    })

    it('平台品牌且基线完全没有卡图族：补齐（不是只替换）', () => {
      document.head.innerHTML = '<title>old</title>'
      syncRouteMeta('/compare', 'zh', '')
      // brand 传空＝未配品牌的平台站（RouteMetaSync 传的就是 `brandName || tenantName || ''`）
      expect(count('meta[property="og:image"]')).toBe(1)
      expect(document.querySelector('meta[property="og:image"]')?.getAttribute('content')).toContain('/og-image.png')
    })

    it('白标（租户品牌域）：整族摘掉，卡档回落小图', () => {
      document.head.innerHTML = '<title>old</title>' + baseTags
      syncRouteMeta('/pricing', 'en', 'Acme 译通')
      expect(document.head.innerHTML).not.toContain('og-image.png')
      expect(count('meta[property="og:image"]')).toBe(0)
      expect(count('meta[property="og:image:width"]')).toBe(0)
      expect(count('meta[property="og:image:height"]')).toBe(0)
      expect(count('meta[name="twitter:image"]')).toBe(0)
      expect(document.querySelector('meta[name="twitter:card"]')?.getAttribute('content')).toBe('summary')
    })

    it('反复调用不累积（路由/语言每次变化都重跑同一条腿）', () => {
      document.head.innerHTML = '<title>old</title>'
      syncRouteMeta('/pricing', 'en', '能言 LangCross')
      syncRouteMeta('/compare', 'zh', '能言 LangCross')
      syncRouteMeta('/', 'th', '能言 LangCross')
      expect(count('meta[property="og:image"]')).toBe(1)
      expect(count('meta[name="twitter:image"]')).toBe(1)
      expect(count('meta[name="twitter:card"]')).toBe(1)
    })
  })
})
