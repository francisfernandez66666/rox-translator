// ============================================================================
// src/seo/routeMeta.ts — 客户端路由 meta（DEF-06，2026-10-24）
//
// 与服务端注入消费同一份 route_meta.json（12 语种单一事实源）：
//   - 服务端负责首屏 HTML 的 title/description/canonical/og/robots/hreflang；
//   - 客户端在路由/语言变化后同步更新（SPA 不整页刷新，首屏注入不会自动变）。
// `syncRouteMeta` 刻意与 router 无关：App（有 react-router）与 PublicRoot（无 router）都要用。
// canonical 与服务端同口径：origin + pathname + "?lang=<lang>"（语言版本的稳定地址）。
// og:image 一族（2026-10-25 补腿）同样两侧同口径：平台品牌域发绝对 URL 的分享卡，
// 白标（租户品牌域）整族摘掉——图是平台的，客户转发自己域名不该替平台做广告。
// ============================================================================
import routeMetaJSON from './route_meta.json'
import type { Lang } from '@/i18n'

// RouteMeta 单条路由的 robots/title/description（与服务端 seo_meta.json 同源）。
type RouteMeta = {
  robots?: string
  title?: Record<string, string>
  description?: Record<string, string>
}
// RouteMetaDoc route_meta.json 的整体结构（12 语种 + 路由表 + app 兜底）。
type RouteMetaDoc = {
  languages: string[]
  x_default_lang: string
  routes: Record<string, RouteMeta>
  app: { robots: string; title_suffix: string }
}

// DOC 编译期 JSON 的强类型视图；运行时不修改。
const DOC = routeMetaJSON as unknown as RouteMetaDoc

// pick 按语种取文案，缺该语种回落 en→zh（12 语种由 JSON 全量保证，回落只防漏键）。
function pick(m: Record<string, string> | undefined, lang: string): string {
  if (!m) return ''
  return m[lang] || m.en || m.zh || ''
}

// setMetaByName 写入/更新 <meta name=...>。
function setMetaByName(name: string, content: string) {
  let el = document.querySelector<HTMLMetaElement>(`meta[name="${name}"]`)
  if (!el) {
    el = document.createElement('meta')
    el.setAttribute('name', name)
    document.head.appendChild(el)
  }
  el.setAttribute('content', content)
}

// setMetaByProperty 写入/更新 <meta property=...>（og:*）。
function setMetaByProperty(property: string, content: string) {
  let el = document.querySelector<HTMLMetaElement>(`meta[property="${property}"]`)
  if (!el) {
    el = document.createElement('meta')
    el.setAttribute('property', property)
    document.head.appendChild(el)
  }
  el.setAttribute('content', content)
}

// setCanonical 写入/更新 canonical（语言版本稳定地址）。
function setCanonical(href: string) {
  let el = document.querySelector<HTMLLinkElement>('link[rel="canonical"]')
  if (!el) {
    el = document.createElement('link')
    el.rel = 'canonical'
    document.head.appendChild(el)
  }
  el.href = href
}

// clearHreflang 清掉上一轮 hreflang，避免路由/语言切换后重复。
function clearHreflang() {
  document.querySelectorAll('link[rel="alternate"][hreflang]').forEach((el) => el.remove())
}

// 分享卡图（og:image）——与服务端 injectRouteMeta 同一条口径的客户端那一半（DEF-06 补腿，2026-10-25）：
//   · 图本身是**平台品牌资产**（卡面写着「能言 LangCross」），所以只有平台品牌域发它；
//   · 租户品牌域（白标）必须**整族摘掉**——客户转发自己域名的链接却在给平台做广告，
//     与「标题/description 里的品牌名要换成租户名」是同一件事的两半；
//   · 必须是**绝对 URL**（相对路径社交爬虫一律不解，等于没有卡图），域名取当前 origin 不写死站点域。
// 路径与尺寸和服务端 seoOGImagePath / scripts/growth/build_og_image.py 的 W/H 三处同值。
const OG_IMAGE_PATH = '/og-image.png'
const OG_IMAGE_W = '1200'
const OG_IMAGE_H = '630'
// PLATFORM_BRAND 平台品牌展示名；与服务端 seoPlatformBrand 逐字同值（两把尺子迟早打架）。
const PLATFORM_BRAND = '能言 LangCross'

// removeMeta 摘掉匹配到的 meta 标签（og:image 一族在白标面必须"没有"，不是"换成空串"）。
function removeMeta(selector: string) {
  document.querySelectorAll(selector).forEach((el) => el.remove())
}

// syncOgImage 按品牌形态重写分享卡图族（服务端注入关闭或未命中路由时，SPA 侧这一腿仍然成立）。
function syncOgImage(brand: string) {
  removeMeta('meta[property="og:image"]')
  removeMeta('meta[property="og:image:width"]')
  removeMeta('meta[property="og:image:height"]')
  removeMeta('meta[name="twitter:image"]')
  if (brand && brand !== PLATFORM_BRAND) {
    // 白标：没有卡图可发，卡档同步回落小图（大卡图却无图＝社交端渲染成破图占位）
    setMetaByName('twitter:card', 'summary')
    return
  }
  const url = window.location.origin + OG_IMAGE_PATH
  setMetaByProperty('og:image', url)
  setMetaByProperty('og:image:width', OG_IMAGE_W)
  setMetaByProperty('og:image:height', OG_IMAGE_H)
  setMetaByName('twitter:image', url)
  setMetaByName('twitter:card', 'summary_large_image')
}

// hreflangCode 内部语种码 → BCP47（hreflang 只认合法语言标签）。
function hreflangCode(code: string): string {
  if (code === 'zh') return 'zh-Hans'
  if (code === 'zh_hant') return 'zh-Hant'
  return code
}

// addHreflang 为可收录路由生成 12 条 hreflang + x-default。
function addHreflang(pathname: string) {
  const origin = window.location.origin
  const base = origin + pathname
  for (const code of DOC.languages) {
    const l = document.createElement('link')
    l.rel = 'alternate'
    l.hreflang = hreflangCode(code)
    l.href = base + '?lang=' + code
    document.head.appendChild(l)
  }
  const x = document.createElement('link')
  x.rel = 'alternate'
  x.hreflang = 'x-default'
  x.href = base
  document.head.appendChild(x)
}

/**
 * syncRouteMeta 按当前 pathname + 语言更新文档 meta。
 * 返回值 true=命中 route meta（已接管 title）；false=未命中，调用方保留品牌兜底。
 */
export function syncRouteMeta(pathname: string, lang: Lang, brand: string): boolean {
  if (typeof document === 'undefined' || typeof window === 'undefined') return false
  const publicMeta = DOC.routes[pathname]
  const isApp = !publicMeta
  const meta = publicMeta || { robots: DOC.app.robots, title: { zh: DOC.app.title_suffix, en: DOC.app.title_suffix } }
  const title = isApp
    ? (brand || DOC.app.title_suffix)
    : (pick(meta.title, lang) || brand)
  const desc = pick(meta.description, lang)
  const robots = meta.robots || (isApp ? DOC.app.robots : 'index,follow')
  const canonical = window.location.origin + pathname + '?lang=' + lang

  document.title = title
  setMetaByName('robots', robots)
  setCanonical(canonical)
  setMetaByProperty('og:url', canonical)
  setMetaByProperty('og:title', title)
  if (desc) {
    setMetaByName('description', desc)
    setMetaByProperty('og:description', desc)
  }
  // 卡图族每次路由变化都按品牌形态重算（服务端那份注入只在首屏 HTML 上生效，SPA 换路由不会重跑）
  syncOgImage(brand)
  clearHreflang()
  if (!isApp && !robots.includes('noindex')) {
    addHreflang(pathname)
  }
  document.documentElement.dataset.routeMeta = '1'
  return true
}

/** 当前 URL 的 ?lang（合法 12 码）——服务端注入与客户端同步共用同一口径。 */
export function langFromURL(): Lang | null {
  if (typeof window === 'undefined') return null
  const v = new URLSearchParams(window.location.search).get('lang')
  if (!v) return null
  const all: string[] = DOC.languages
  return all.includes(v) ? (v as Lang) : null
}
