// @vitest-environment jsdom
// ============================================================================
// lib/productPublicUrl.dom.test.ts — 对外页两段短码 URL 契约的「前端那一面」（施工_一期 §2.1 H1–H5／§5.7／§9.2）
//
// 前端职责（H1–H5 的前端面，后端那一面的 P7–P10 在 store/product_public_gate_test.go）：
//   · 两段不可枚举短码 `/p/{tenantCode}/{pageCode}` 必须**动态透传**，前端不得写死单一租户；
//   · 两个入口树（无 token 的 PublicRoot.tsx、已登录的 App.tsx）解析口径必须逐字相同，
//     否则会出现「同一 URL 一棵树进了产品页、另一棵退回登录墙」的分裂（AGENTS 那条
//     「两棵树必须逐支等值」）；
//   · 改名不改 URL（H1）：URL 由 codes 拼成，前端不拿产品名去拼路径——这条由后端
//     `seo_indexable`/codes 不可变保证，前端只负责「拿到 codes 就原样拼」。
//
// 运行态（跨租户拼接 not-found、改名不改 URL）需要真后端＋真短码，用 env 守卫跳过：
//   PRODUCT_TENANT_CODE / PRODUCT_PAGE_CODE / PRODUCT_CROSS_TENANT_CODE / PRODUCT_CROSS_PAGE_CODE
// 源码级契约（两棵树解析一致、不写死租户）无需后端，常态绿。
// 运行：npx vitest run src/lib/productPublicUrl.dom.test.ts
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

/** 抽出某文件里解析 /p/ 两段短码的那段代码：必须同时出现 segments[2] 与 segments[3] */
function publicUrlParse(src: string): { raw: string; hasTwoSegments: boolean; passesBoth: boolean } {
  const code = stripComments(src)
  const m = code.match(/path\.startsWith\('\/p\/'\)\)?\s*\{[\s\S]*?\n\s*\}/)
  if (!m) return { raw: '', hasTwoSegments: false, passesBoth: false }
  const raw = m[0]
  const hasTwoSegments = /segments\[2\]/.test(raw) && /segments\[3\]/.test(raw)
  // 两个 codes 都必须传给 ProductPublicPage（tenantCode={tenantCode} 与 pageCode={pageCode}）
  const passesBoth = /tenantCode=\{tenantCode\}/.test(raw) && /pageCode=\{pageCode\}/.test(raw)
  return { raw, hasTwoSegments, passesBoth }
}

const PUBLIC_ROOT = readSrc('../PublicRoot.tsx')
const APP = readSrc('../App.tsx')

afterEach(() => {})

describe('对外页两段短码 URL 契约（§2.1 H1–H5 前端面／§9.2）', () => {
  it('H1 前端面：两个入口树都从 /p/ 解析出 tenantCode=segments[2]、pageCode=segments[3]', () => {
    const a = publicUrlParse(PUBLIC_ROOT)
    const b = publicUrlParse(APP)
    expect(a.hasTwoSegments, 'PublicRoot.tsx 的 /p/ 解析没同时取 segments[2] 与 segments[3]').toBe(true)
    expect(b.hasTwoSegments, 'App.tsx 的 /p/ 解析没同时取 segments[2] 与 segments[3]').toBe(true)
  })

  it('H1/H5 前端面：两棵树都把两个 codes 原样透传给 ProductPublicPage（不写死单一租户）', () => {
    const a = publicUrlParse(PUBLIC_ROOT)
    const b = publicUrlParse(APP)
    expect(a.passesBoth, 'PublicRoot.tsx 没把 tenantCode 与 pageCode 两个都传给 ProductPublicPage').toBe(true)
    expect(b.passesBoth, 'App.tsx 没把 tenantCode 与 pageCode 两个都传给 ProductPublicPage').toBe(true)
    // 前端不得在公开 URL 构造里写死某个租户 code（跨租户拼接由后端判 not-found，前端只透传）
    const both = stripComments(PUBLIC_ROOT) + '\n' + stripComments(APP)
    expect(both, '前端公开 URL 构造里出现写死的租户字面量（应是动态透传）').not.toMatch(/tenantCode=\{['"][a-zA-Z0-9]+['"]\}/)
  })

  it('两段短码解析口径逐支等值（两棵树不得分裂）', () => {
    // 两棵树外壳不同（PublicRoot 用 MarketingShell，App 用 PublicShell+RouteBoundary+Suspense），
    // 但「解析」核心（segments 拆分 + 取 segments[2]/[3]）必须一致——这才是「不分裂」的真判据。
    const extractParse = (raw: string): string => {
      const lines = raw
        .split('\n')
        .map((l) => l.trim())
        .filter((l) =>
          /const segments = path\.split\('\/'\)/.test(l) ||
          /const tenantCode = segments\[2\]/.test(l) ||
          /const pageCode = segments\[3\]/.test(l))
      return lines.join(' ').replace(/\s+/g, ' ').trim()
    }
    const ca = extractParse(publicUrlParse(PUBLIC_ROOT).raw)
    const cb = extractParse(publicUrlParse(APP).raw)
    expect(ca, 'PublicRoot 解析片段读不到').not.toBe('')
    expect(cb, 'App 解析片段读不到').not.toBe('')
    expect(ca, '两棵树 /p/ 解析口径不一致（会分裂：一棵进产品页、另一棵退回登录墙）').toBe(cb)
  })
})

// ---- 运行态（需真后端＋真短码，本地默认跳过） ----
const TENANT = process.env.PRODUCT_TENANT_CODE || ''
const PAGE = process.env.PRODUCT_PAGE_CODE || ''
const CROSS_TENANT = process.env.PRODUCT_CROSS_TENANT_CODE || ''
const CROSS_PAGE = process.env.PRODUCT_CROSS_PAGE_CODE || ''
const HAVE_RUNTIME = Boolean(TENANT && PAGE && CROSS_TENANT && CROSS_PAGE)

describe.skipIf(!HAVE_RUNTIME)('对外页两段短码运行态（需真后端，本地跳过）', () => {
  test('H4/H5 跨租户拼接：A 域名 + B 短码 ⇒ not_found（不 403、不 500）', async () => {
    const res = await fetch(`/p/${CROSS_TENANT}/${CROSS_PAGE}`, { headers: { Accept: 'application/json' } })
    expect(res.status, '跨租户拼接应回落 not_found 语义（不为 403/500）').not.toBe(403)
    expect(res.status, '跨租户拼接应回落 not_found 语义（不为 403/500）').not.toBe(500)
    const body = (await res.json().catch(() => ({}))) as { not_found?: boolean }
    expect(body.not_found, '跨租户拼接必须判 not_found（B 的内容或 A 的品牌不得出栈）').toBe(true)
  })
})
