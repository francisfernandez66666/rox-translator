// @vitest-environment jsdom
// ============================================================================
// lib/productSeo.dom.test.ts — 对外页 SEO 面「前端那一面」（施工_一期 §5.8／§9.2 S1–S4）
//
// 后端是 SEO 元数据单一真值（route_meta.json 静态 12 语种 ＋ 后端从 products 生成
// sitemap.xml / canonical / hreflang）。前端那一面的硬纪律：
//   · S2 前端面：受控字段（visibility='gated'）的值字面量**永不进** meta / JSON-LD / 首屏
//     ——ProductPublicPage 只渲染 publicOnly 过滤后的事实，不把 gated 行写进 title/meta；
//   · S3/S4 前端面：索引面与可见性面是两把尺子；前端**不**在客户端改写 seo_indexable，
//     「关了不被重新打开」是后端 UPDATE 纪律（只许显式设置接口写），前端只尊重服务端返回值。
//
// 运行态（sitemap 含/不含 URL、改名不重开）需真后端＋已构建 dist，用 env 守卫跳过：
//   PRODUCT_TENANT_CODE / PRODUCT_PAGE_CODE / PRODUCT_SEO_OFF_TENANT / PRODUCT_SEO_OFF_PAGE
// 源码级契约（不进 meta、不客户端改写索引）无需后端，常态绿。
// 运行：npx vitest run src/lib/productSeo.dom.test.ts
// ============================================================================
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { afterEach, describe, expect, it, test } from 'vitest'

function readSrc(rel: string): string {
  return readFileSync(fileURLToPath(new URL(rel, import.meta.url)), 'utf-8')
}
function stripComments(src: string): string {
  return src.replace(/\/\*[\s\S]*?\*\//g, '').replace(/^\s*\/\/.*$/gm, '')
}

const PAGE_SRC = readSrc('../components/ProductPublicPage.tsx')

afterEach(() => {})

describe('对外页 SEO 面（§5.8 S1–S4 前端面／§9.2）', () => {
  it('S2 前端面：publicOnly 必须滤掉 visibility=gated 的行（受控值不进 DOM）', () => {
    const code = stripComments(PAGE_SRC)
    expect(code, 'ProductPublicPage 缺 publicOnly 过滤（受控值会漏进 DOM）').toMatch(/function publicOnly/)
    expect(code, 'publicOnly 没按 visibility!==.gated. 过滤').toMatch(/f\.visibility\s*!==\s*['"]gated['"]/)
    // 渲染只用 visible（publicOnly 结果），不渲染原始 facts 的 gated 行
    expect(code, '渲染侧没用 publicOnly 结果（visible）而用了原始 facts').toMatch(/const visible = publicOnly\(facts\)/)
  })

  it('S2 前端面：组件不得把任何字段值写进 document.title / meta / JSON-LD（只由后端 SSR 提供）', () => {
    const code = stripComments(PAGE_SRC)
    expect(code, '前端不得 client 端改写 document.title（SEO meta 应来自后端 SSR）').not.toMatch(/document\.title\s*=/)
    expect(code, '前端不得 client 端注入 <meta>（受控值会借 meta 泄漏）').not.toMatch(/createElement\(['"]meta['"]\)|document\.head/)
    expect(code, '前端不得 client 端写 JSON-LD（受控值会借 script 泄漏）').not.toMatch(/application\/ld\+json|JSON-LD|json-ld/)
  })

  it('S3/S4 前端面：组件不客户端改写 seo_indexable（索引开关只许后端显式设置接口写）', () => {
    const code = stripComments(PAGE_SRC)
    expect(code, '前端不得在客户端改写 seo_indexable（会绕过「关了不被重新打开」纪律）').not.toMatch(/seo_indexable/)
  })

  it('S1 正向对照（源码级）：组件渲染公开事实，确有内容出口（负向锁不空转）', () => {
    const code = stripComments(PAGE_SRC)
    // 至少有一个可见事实的渲染出口，否则「不进 meta」的负向锁是空转
    expect(code, '缺可见事实渲染出口（负向锁会空转）').toMatch(/visible\.map\(/)
  })
})

// ---- 运行态（需真后端＋已构建 dist，本地默认跳过） ----
const TENANT = process.env.PRODUCT_TENANT_CODE || ''
const PAGE = process.env.PRODUCT_PAGE_CODE || ''
const OFF_TENANT = process.env.PRODUCT_SEO_OFF_TENANT || ''
const OFF_PAGE = process.env.PRODUCT_SEO_OFF_PAGE || ''
const HAVE_RUNTIME = Boolean(TENANT && PAGE && OFF_TENANT && OFF_PAGE)

describe.skipIf(!HAVE_RUNTIME)('对外页 SEO 运行态（需真后端，本地跳过）', () => {
  test('S1：默认开 —— sitemap.xml 含该 URL', async () => {
    const res = await fetch('/sitemap.xml')
    const xml = await res.text()
    expect(xml, '默认开的产品页必须进 sitemap.xml').toContain(`/p/${TENANT}/${PAGE}`)
  })

  test('S1：关索引后 —— sitemap.xml 不含该 URL', async () => {
    const res = await fetch('/sitemap.xml')
    const xml = await res.text()
    expect(xml, '关掉 seo_indexable 后该 URL 必须从 sitemap 消失').not.toContain(`/p/${OFF_TENANT}/${OFF_PAGE}`)
  })
})
