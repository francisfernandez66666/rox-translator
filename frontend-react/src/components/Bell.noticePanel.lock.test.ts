// ============================================================================
// components/Bell.noticePanel.lock.test.ts — 站内通知铃铛浮层的「自适应形态」静态锁
//   （★ 2026-10-19 G20：与 AccountMenu.noticePanel 同族的第二种宿主）
//
// 缺陷本体的机制：后台顶栏的铃铛浮层 past 形态是 `position:absolute; width:340px` 静态留在
//   `.bell-wrap`（顶栏内）——顶栏/左栏任一有 overflow 时浮层就被裁切；窄屏下 340px 也会越过视口。
//   修法与 AccountMenu 同一口径：portal 到 document.body + position:fixed + 落点现算 + 外点放行。
//
// 为什么是静态锁：这里锁的是**形态**（portal／fixed／宽度不写死／现算夹边／外点放行腿），
//   真几何由 e2e/pixel_uat.spec.ts 在真浏览器里量（jsdom 不做布局，写在这里等于自欺）。
//   ⚠ 本文件留在 node 环境（vitest 缺省）读源文件：jsdom 下 import.meta.url 是 http: URL，
//   fileURLToPath 当场抛「The URL must be of scheme file」。
//
// 运行：npx vitest run src/components/Bell.noticePanel.lock.test.ts
// ============================================================================
import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

const PKG = fileURLToPath(new URL('../../', import.meta.url))
const FILE = 'src/components/Bell.tsx'
const SRC = readFileSync(PKG + FILE, 'utf-8')

/** 去注释：负向/正向锁都不许被说明注释喂饱（本仓老坑，见 AGENTS §三） */
function stripComments(src: string): string {
  let out = ''
  let i = 0
  let inBlock = false
  let inLine = false
  let quote = ''
  while (i < src.length) {
    const c = src[i]
    const n = src[i + 1]
    if (inLine) {
      if (c === '\n') { inLine = false; out += c }
      i++
      continue
    }
    if (inBlock) {
      if (c === '*' && n === '/') { inBlock = false; i += 2; continue }
      i++
      continue
    }
    if (quote) {
      out += c
      if (c === '\\') { out += n ?? ''; i += 2; continue }
      if (c === quote) quote = ''
      i++
      continue
    }
    if (c === '/' && n === '*') { inBlock = true; i += 2; continue }
    if (c === '/' && n === '/') { inLine = true; i += 2; continue }
    if (c === "'" || c === '"' || c === '`') { quote = c; out += c; i++; continue }
    out += c
    i++
  }
  return out
}

/** 判据函数化（正证喂真源文件、反证喂变异文本，同一个函数——AGENTS §三「判据级代替不了守卫级」）。 */
function bellPanelViolations(src: string): string[] {
  const out: string[] = []
  const code = stripComments(src)

  // ① 浮层必须 portal 到 body：脱离顶栏/左栏这些滚动容器的排版流
  if (!/\bcreatePortal\(/.test(code)) out.push('缺少 createPortal：浮层又回到顶栏容器内')
  if (!/document\.body/.test(code)) out.push('portal 目标不是 document.body')

  // ② 固定定位（不许回退成容器内 absolute）
  const popRule = code.match(/\.bell-pop\{[^}]*\}/)?.[0] ?? ''
  if (!/position\s*:\s*fixed/.test(popRule)) out.push('.bell-pop 不是 position:fixed')
  if (/position\s*:\s*absolute/.test(popRule)) out.push('.bell-pop 仍是容器内 absolute（会被顶栏裁切）')

  // ③ 面板宽度不许写死（旧形态 width:340px 在窄屏越出视口）
  const popPanelRule = code.match(/\.bell-pop \.bell-panel\{[^}]*\}/)?.[0] ?? ''
  if (!/width\s*:\s*100%/.test(popPanelRule)) out.push('.bell-pop .bell-panel 宽度不是 100%')
  if (/width\s*:\s*\d+px/.test(popPanelRule)) out.push('.bell-pop .bell-panel 宽度被写死 px')
  if (!/box-sizing\s*:\s*border-box/.test(popPanelRule)) out.push('.bell-pop .bell-panel 缺 box-sizing:border-box')

  // ④ 落点必须现算夹边（触发器矩形为锚 + 宽度自适应 + 左右夹 + 高度随视口收 + 越界翻上）
  const fn = code.match(/function placeBellPanel\([\s\S]*?\n\}/)?.[0] ?? ''
  if (!fn) out.push('placeBellPanel() 不见了：落点改成了写死坐标')
  else {
    if (!/getBoundingClientRect\(\)/.test(fn)) out.push('placeBellPanel 不以触发器矩形为锚')
    if (!/Math\.min\(340,\s*vw - 24\)/.test(fn)) out.push('placeBellPanel 缺宽度自适应（窄屏会顶破视口）')
    if (!/Math\.min\(r\.right - width,\s*vw - width - 8\)/.test(fn)) out.push('placeBellPanel 缺左右夹边')
    if (!/r\.top - maxHeight - 8/.test(fn)) out.push('placeBellPanel 缺向上翻（矮屏下沿越界不兜底）')
  }

  // ⑤ portal 之后，外点关闭必须单独放行浮层内部点击（否则点面板里任何地方都会把面板关掉）
  if (!/panelRef\.current\?\.contains/.test(code)) out.push('外点判定没有放行 panelRef 内的点击')

  return out
}

describe('Bell · 通知浮层形态（★ G20 自适应修复）', () => {
  it('正证：真源文件齐全——portal 到 body + fixed + 宽度 100% + 现算夹边 + 外点放行', () => {
    expect(bellPanelViolations(SRC)).toEqual([])
  })

  it('自证：四条关键腿都在（射程不是被"处处命中"撑出来的）', () => {
    expect(SRC).toContain('createPortal(')
    expect(SRC).toContain('document.body')
    expect(SRC).toContain('panelRef.current?.contains')
    expect(SRC).toContain('function placeBellPanel(')
  })

  it('反证①：摘掉 portal（回到容器内渲染）必须判红', () => {
    const mut = SRC.replace('createPortal(', 'plainRender(').replace('document.body,', '{},')
    expect(mut).not.toBe(SRC)
    expect(bellPanelViolations(mut).length).toBeGreaterThan(0)
  })

  it('反证②：把 position:fixed 改回容器内 absolute 必须判红', () => {
    const mut = SRC.replace('.bell-pop{position:fixed;z-index:70}', '.bell-pop{position:absolute;z-index:70}')
    expect(mut).not.toBe(SRC)
    expect(bellPanelViolations(mut)).toContain('.bell-pop 仍是容器内 absolute（会被顶栏裁切）')
  })

  it('反证③：把面板宽度写回 340px 必须判红（原缺陷形态）', () => {
    const mut = SRC.replace('.bell-pop .bell-panel{position:static;width:100%;box-sizing:border-box;max-height:inherit}',
      '.bell-pop .bell-panel{position:static;width:340px;max-height:inherit}')
    expect(mut).not.toBe(SRC)
    const v = bellPanelViolations(mut)
    expect(v).toContain('.bell-pop .bell-panel 宽度不是 100%')
    expect(v).toContain('.bell-pop .bell-panel 宽度被写死 px')
    expect(v).toContain('.bell-pop .bell-panel 缺 box-sizing:border-box')
  })

  it('反证④：摘掉外点放行腿必须判红（portal 的下半条口径）', () => {
    const mut = SRC.replace('if (panelRef.current?.contains(t)) return', 'void 0')
    expect(mut).not.toBe(SRC)
    expect(bellPanelViolations(mut)).toContain('外点判定没有放行 panelRef 内的点击')
  })

  it('反证⑤：落点改回写死坐标必须判红', () => {
    const mut = SRC.replace(/function placeBellPanel\([\s\S]*?\n\}/,
      'function placeBellPanel() {\n  return { left: 0, top: 60, width: 340, maxHeight: 420 }\n}')
    expect(mut).not.toBe(SRC)
    const v = bellPanelViolations(mut)
    expect(v).toContain('placeBellPanel 不以触发器矩形为锚')
    expect(v).toContain('placeBellPanel 缺宽度自适应（窄屏会顶破视口）')
    expect(v).toContain('placeBellPanel 缺左右夹边')
  })
})
