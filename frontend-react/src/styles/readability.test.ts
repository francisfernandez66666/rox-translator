// ============================================================================
// readability.test.ts — ★ #35 可读性闸门（2026-09-21 用户反馈：登录后前后台字体偏小、
// 次级文字对比度偏低 → 全站上调一档，并用本文件钉死，防止后续改动把字号/对比度改回去）
// ----------------------------------------------------------------------------
// 三条断言：
//  1) 组件库（src/ui/langcross/css/components.css，冻结区不可直接改）里每一处 ≤13.5px
//     的小字号，theme.css §十都必须有一条 `html <原选择器>` 的页面级覆写，且正好抬 1px；
//  2) 次级文字令牌（--lc-text-2/3/4）对页面底 #000 与卡面 #0E1014 的对比度都要 ≥6:1
//     （AA 底线 4.5:1 留一倍余量），且必须比 tokens.css 原值更亮（只准变好不准回退）；
//  3) 登录后的 tsx 里禁止再写死那四个灰色字面值，必须走语义令牌，
//     否则未来调对比度又要改几十个文件。
// 说明：本测试读源文件而非渲染后的 DOM——jsdom 不加载外链 CSS，源码级断言才是稳定闸门。
// ============================================================================
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'

const ROOT = fileURLToPath(new URL('../../', import.meta.url))
const KIT_CSS = readFileSync(ROOT + 'src/ui/langcross/css/components.css', 'utf-8')
const KIT_TOKENS = readFileSync(ROOT + 'src/ui/langcross/css/tokens.css', 'utf-8')
const THEME_CSS = readFileSync(ROOT + 'src/styles/theme.css', 'utf-8')

// 去掉 /* */ 注释后按 { } 拆规则，返回「选择器 → font-size 列表」
function rules(css: string) {
  const body = css.replace(/\/\*[\s\S]*?\*\//g, '')
  const out: { sel: string; sizes: number[] }[] = []
  const re = /([^{}]+)\{([^{}]*)\}/g
  let m: RegExpExecArray | null
  while ((m = re.exec(body))) {
    const sel = m[1].trim().replace(/\s+/g, ' ')
    if (sel.startsWith('@') || sel.startsWith('from') || sel.startsWith('to')) continue
    const sizes = [...m[2].matchAll(/font-size:\s*([0-9.]+)px/g)].map((x) => Number(x[1]))
    if (sizes.length) out.push({ sel, sizes })
  }
  return out
}

const themeMap = new Map<string, number>()
for (const r of rules(THEME_CSS)) {
  if (!r.sel.startsWith('html ')) continue
  for (const s of r.sel.split(',')) themeMap.set(s.trim().replace(/^html\s+/, ''), r.sizes[0])
}
// .lc-root 基础字号是唯一的「非小字号」覆写（14→15），单独断言
const kitSmall = rules(KIT_CSS)
  .filter((r) => r.sizes.every((v) => v >= 10 && v <= 13.5))
  .flatMap((r) => r.sel.split(',').map((s) => ({ sel: s.trim().replace(/\s+/g, ' '), size: r.sizes[0] })))

describe('#35 字号上调闸门', () => {
  it('组件库每处 ≤13.5px 小字号都有 +1px 的页面级覆写', () => {
    const missing: string[] = []
    for (const { sel, size } of kitSmall) {
      const override = themeMap.get(sel)
      if (override === undefined || override < size + 1) missing.push(`${sel}: 库内 ${size}px → 覆写 ${override ?? '无'}px`)
    }
    expect(missing, '缺覆写或抬升不足 1px：\n' + missing.join('\n')).toEqual([])
    // 断言非空集：解析一旦失效（选择器改名/正则退化）也要立刻红灯，而不是「零条通过」
    expect(kitSmall.length).toBeGreaterThan(40)
  })
  it('基础正文 .lc-root 由 14px 抬到 15px', () => {
    expect(themeMap.get('.lc-root')).toBe(15)
  })
})

// WCAG 相对亮度与对比度（与《部署指南》无关，纯前端可读性校验）
function contrast(fg: string, bg: string) {
  const lum = (hex: string) => {
    const c = [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16) / 255)
      .map((v) => (v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4))
    return 0.2126 * c[0] + 0.7152 * c[1] + 0.0722 * c[2]
  }
  const a = lum(fg), b = lum(bg)
  return (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05)
}
const token = (css: string, name: string) => {
  const m = css.match(new RegExp(`${name}:\\s*(#[0-9a-fA-F]{6})`))
  return m ? m[1] : ''
}

describe('#35 次级文字对比度闸门', () => {
  for (const name of ['--lc-text-2', '--lc-text-3', '--lc-text-4']) {
    const now = token(THEME_CSS, name)
    const before = token(KIT_TOKENS, name)
    it(`${name} 在页面底与卡面上都 ≥6:1 且不得比原值更暗`, () => {
      expect(now.length, 'theme.css §十 必须覆写 ' + name).toBe(7)
      expect(contrast(now, '#000000')).toBeGreaterThanOrEqual(6)
      expect(contrast(now, '#0E1014')).toBeGreaterThanOrEqual(6)
      expect(contrast(now, '#000000'), '不得回退到 tokens.css 的更暗值')
        .toBeGreaterThanOrEqual(contrast(before, '#000000'))
    })
  }
})

describe('#35 灰色字面值禁再写死', () => {
  // 未登录的营销页保持原设计口径，不在本闸门范围内
  const EXEMPT = /langcross|\.test\.|\/(Landing|PricingPage|LeadForm|SiteFooter|HeroDemo|Login)\.tsx|styles\/(theme|mobile)\.css/
  const offenders: string[] = []
  for (const f of walkTs('src')) {
    if (EXEMPT.test(f)) continue
    readFileSync(ROOT + f, 'utf-8').split('\n').forEach((line, i) => {
      if (/color:\s*['"]?#(9AA0AA|878D95|7A828E|8A9099)/i.test(line)) offenders.push(`${f}:${i + 1}`)
    })
  }
  it('登录后界面不得再出现写死的次级灰（改走 var(--lc-text-*)）', () => {
    expect(offenders, '请改为语义令牌：\n' + offenders.join('\n')).toEqual([])
  })
})

// 枚举 src 下的 ts/tsx（测试里不跑 shell，也不进组件库冻结区）
function walkTs(dir: string): string[] {
  const acc: string[] = []
  for (const e of readdirSync(ROOT + dir)) {
    const rel = `${dir}/${e}`
    if (statSync(ROOT + rel).isDirectory()) { if (!/langcross/.test(rel)) acc.push(...walkTs(rel)); continue }
    if (/\.(tsx|ts)$/.test(rel)) acc.push(rel)
  }
  return acc
}
