// ============================================================================
// selfserviceOverflow.lock.test.ts — ★ 2026-10-19 G15-D9「/my 桌面档不得横滚」源码级锁
// ----------------------------------------------------------------------------
// 缺陷形态（现网桌面 1280 实测）：自助页（/my、/billing 内的 .ss-grid 卡片）横向溢出，
//   根因是两条无法收缩的腿——它们不在任何窄屏规则里，只在桌面档生效：
//     ① 网格项默认 min-width:auto，卡片内容最小宽度顶宽列（mobile.css 的 .ss-grid>* 只管 ≤900）；
//     ② App.tsx 的 `.ss-row>span{flex:none}` 会命中值侧 <Badge>（渲染成 <span>），长 URL/邮箱
//        既不收缩也不换行 ⇒ 卡片被撑破、整页横滚。
// 修复落点＝styles/frontshell.css 里三条**无条件**覆盖，且 .ss-row 那条特异度压过基础规则。
//
// 为什么是源码级锁而不是 DOM 量宽：jsdom 不做布局（scrollWidth 恒 0），量不出真假；
//   真浏览器量宽由 pixel_uat 承担（不在本批可跑范围内）。这里锁的是「覆盖形态 + 特异度关系」，
//   与 readability.test.ts 同口径（读源文件，不读渲染 DOM）。
// 判据分两半（等值锁）：
//   A) 基础规则在位（App.tsx 里 `.ss-row>span{flex:none}` 必须还在，否则本锁失去靶子＝空转）；
//   B) frontshell.css 三条覆盖齐全 + .ss-row 覆盖选择器特异度 **严格大于** 基础规则。
// 反证（三枚，逐条摘一个腿必须判红，证明 checkOverflow 有牙齿、不是常量恒真）：
//   R1 摘掉 .ss-grid>* / R2 摘掉 .ssc-card / R3 摘掉 .ss-row>* 覆盖。
// ============================================================================
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'

// styles/ → src/ → frontend-react 包根
const ROOT = resolve(fileURLToPath(new URL('.', import.meta.url)), '..', '..')
const read = (p: string) => readFileSync(resolve(ROOT, p), 'utf8')

/** 剥掉 CSS 注释：负向/正锁都只在「代码」上判，注释里写说明不算数。 */
function stripComments(s: string): string {
  return s.replace(/\/\*[\s\S]*?\*\//g, '')
}

/** 选择器特异度三元组 (a,b,c)：a=id、b=类+伪类、c=元素+伪元素。够用即可（本仓选择器形态简单）。 */
function specOf(sel: string): [number, number, number] {
  const s = sel.trim()
  const ids = (s.match(/#[\w-]+/g) || []).length
  const b = (s.match(/\.[\w-]+/g) || []).length + (s.match(/:(?!:)[\w-]+/g) || []).length
  const c = s
    .split(/[\s>+~]+/)
    .filter((t) => t && !/^[.:[]/.test(t) && t !== '*')
    .length
  return [ids, b, c]
}
function cmp(a: [number, number, number], b: [number, number, number]): number {
  return a[0] - b[0] || a[1] - b[1] || a[2] - b[2]
}

/** A 段靶子：App.tsx 基础规则 `.ss-row>span{...flex:none...}` 的选择器文本（找不到返回 null）。 */
export function baseRowSpanSelector(app: string): string | null {
  const m = stripComments(app).match(/(\.ss-row\s*>\s*span)\s*\{[^}]*\}/)
  return m ? m[1].replace(/\s+/g, '') : null
}

/** B 段：三条覆盖是否齐全 + .ss-row 覆盖的特异度是否压过基础规则。返回缺失项。 */
export function checkOverflow(css: string, app: string): string[] {
  const c = stripComments(css)
  const miss: string[] = []
  if (!/\.ss-grid\s*>\s*\*\s*\{[^}]*min-width:\s*0/.test(c)) miss.push('.ss-grid > * { min-width:0 }')
  if (!/\.ssc-card\s*\{[^}]*min-width:\s*0/.test(c)) miss.push('.ssc-card { min-width:0 }')
  const rowOv = c.match(/(\.ss-row\s*>\s*:last-child)\s*\{([^}]*)\}/)
  if (!rowOv || !/min-width:\s*0/.test(rowOv[2]) || !/overflow-wrap:\s*anywhere/.test(rowOv[2])) {
    miss.push('.ss-row > :last-child { min-width:0; overflow-wrap:anywhere }')
  }
  const base = baseRowSpanSelector(app)
  if (!base) {
    miss.push('App.tsx 基础规则 .ss-row>span{flex:none}（靶子缺失＝本锁空转）')
  } else if (rowOv) {
    const ov = rowOv[1].replace(/\s+/g, '')
    if (cmp(specOf(ov), specOf(base)) <= 0) miss.push(`特异度不足：${ov} 未压过 ${base}`)
  }
  return miss
}

describe('G15-D9 · 自助页桌面档收缩覆盖（源码级等值锁）', () => {
  const css = read('src/styles/frontshell.css')
  const app = read('src/App.tsx')

  it('A) 靶子在位：App.tsx 基础规则 `.ss-row>span` 仍是 flex:none', () => {
    expect(baseRowSpanSelector(app)).toBe('.ss-row>span')
    expect(stripComments(app)).toMatch(/\.ss-row\s*>\s*span\s*\{[^}]*flex:\s*none/)
  })

  it('B) frontshell.css 三条覆盖齐全、且 .ss-row 覆盖特异度严格大于基础规则', () => {
    expect(checkOverflow(css, app)).toEqual([])
    // 单独把「特异度」这半条也点名一次，防它被上面那条一起兜住而失去可见性
    const ov = stripComments(css).match(/(\.ss-row\s*>\s*:last-child)\s*\{/)![1].replace(/\s+/g, '')
    expect(cmp(specOf(ov), specOf('.ss-row>span'))).toBeGreaterThan(0)
  })

  // 反证：逐条摘掉一个腿，checkOverflow 必须报缺（证明它不是在判一条恒真的东西）
  // ⚠ 变异一律加 g 旗并按「代码行」删：本文件注释里也写有 `.ss-grid>*{min-width:0}` 这类举例，
  //   不加 g 会先删中注释那一处、真规则原样留着，反证当场被自己的注释喂饱（本批真踩）。
  it('反证 R1：摘掉 .ss-grid > * 覆盖 ⇒ 判缺', () => {
    const broken = css.replace(/\.ss-grid\s*>\s*\*\s*\{[^}]*\}/g, '')
    expect(checkOverflow(broken, app)).toContain('.ss-grid > * { min-width:0 }')
  })
  it('反证 R2：摘掉 .ssc-card 覆盖 ⇒ 判缺', () => {
    const broken = css.replace(/\.ssc-card\s*\{[^}]*\}/g, '')
    expect(checkOverflow(broken, app)).toContain('.ssc-card { min-width:0 }')
  })
  it('反证 R3：摘掉 .ss-row > :last-child 覆盖 ⇒ 判缺', () => {
    const broken = css.replace(/\.ss-row\s*>\s*:last-child\s*\{[^}]*\}/g, '')
    expect(checkOverflow(broken, app)).toContain('.ss-row > :last-child { min-width:0; overflow-wrap:anywhere }')
  })
  it('反证 R4：把 .ss-row 覆盖压回基础规则形态（flex:none）⇒ 判缺', () => {
    const broken = css.replace(
      /\.ss-row\s*>\s*:last-child\s*\{[^}]*\}/g,
      '.ss-row > :last-child { flex: none; }',
    )
    expect(checkOverflow(broken, app).length).toBeGreaterThan(0)
  })
})
