// ============================================================================
// src/logoConsistency.test.ts — 全站 logo 一致性静态锁（★ 2026-09-24 #8）
// 背景：用户下令「全站 logo 统一用首页的 logo（有独立品牌页面的租户除外），网站标签 icon 也用这个 logo」。
// 首页标识 = 白色 30×30 圆角块 + 两笔一实一虚黑笔画（Landing.tsx 内联 SVG），现以此锁死五处同源：
//   ① Landing.tsx 顶栏+页脚内联 SVG（图形源头）
//   ② ui/langcross Icon n="brand"（BrandDotIcon：App 顶栏/后台侧栏/定价页默认标记共用）
//   ③ public/logo.svg（favicon 静态文件）
//   ④ index.html 的 <link rel="icon"> 指向 /logo.svg
//   ⑤ 消费点口径：App.tsx 与 AdminDashboard 无 brandLogo 时落到 Icon n="brand"；
//      PricingPage 旧「圆环+圆点」BrandMark 不得复活；branding.tsx 有 favicon 随品牌切换的逻辑。
// 判据用两条笔画 path 的 d 属性逐字比对（图形即这两串坐标，改动任一处即红灯）。
// node 环境即可（只读文件文本），无需 jsdom。
// ============================================================================
import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

const ROOT = resolve(__dirname, '..')
const read = (p: string) => readFileSync(resolve(ROOT, p), 'utf8')

// 首页 logo 的两笔：实笔在前、虚笔（opacity .45）在后——坐标逐字取 Landing.tsx:926-927
const STROKE_SOLID = 'M9.5 10.5v5.2a4.3 4.3 0 0 0 8.6 0V12'
const STROKE_GHOST = 'M20.5 19.5v-5.2a4.3 4.3 0 0 0-8.6 0V18'
const bothStrokes = (src: string) => src.includes(STROKE_SOLID) && src.includes(STROKE_GHOST)

describe('全站 logo 统一（#8）· 同源静态锁', () => {
  it('Landing.tsx 首页标识（图形源头）含两条笔画与白色圆角块', () => {
    const src = read('src/components/Landing.tsx')
    expect(bothStrokes(src)).toBe(true)
    expect(src).toContain('rx="8"')
  })

  it('Icon n="brand"（BrandDotIcon）与首页逐字同源', () => {
    expect(bothStrokes(read('src/ui/langcross/src/icons.tsx'))).toBe(true)
  })

  it('public/logo.svg（favicon 文件）与首页逐字同源', () => {
    expect(bothStrokes(read('public/logo.svg'))).toBe(true)
  })

  it('index.html 声明 favicon 指向 /logo.svg', () => {
    const html = read('index.html')
    expect(html).toMatch(/<link\s[^>]*rel="icon"[^>]*href="\/logo\.svg"/)
  })

  it('消费点口径：App 顶栏与后台侧栏默认用 Icon n="brand"，旧圆点 BrandMark 已退役', () => {
    expect(read('src/App.tsx')).toContain('<Icon n="brand"')
    const admin = read('src/components/admin/AdminDashboard.tsx')
    expect(admin).toContain('<Icon n="brand"')
    expect(admin).toContain('branding.brandLogo ?') // 有独立品牌 Logo 的租户仍走自家 Logo（除外条款）
    const pricing = read('src/components/PricingPage.tsx')
    expect(pricing).toContain('<Icon n="brand"')
    expect(pricing).not.toContain('<circle') // 旧「圆环+圆点」图形不得复活
  })

  it('branding.tsx：favicon 随品牌切换（brandLogo 优先，否则 /logo.svg）', () => {
    const src = read('src/branding.tsx')
    expect(src).toContain("link[rel~='icon']")
    expect(src).toContain("b.brandLogo || '/logo.svg'")
  })
})
