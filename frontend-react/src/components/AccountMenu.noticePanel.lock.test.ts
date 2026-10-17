// ============================================================================
// components/AccountMenu.noticePanel.lock.test.ts — 通知中心浮层的「自适应形态」静态锁
//   （★ 2026-10-17 用户报障：登录后打开通知中心不自适应、**强制左边菜单栏拖动**）
//
// 缺陷本体的机制：左栏 .lc-sidebar 宽仅 237（桌面）／295（移动抽屉）且自带 overflow-y:auto，
//   按 CSS 的 overflow 归一规则另一轴因此也算 auto；而旧形态把通知浮层 position:absolute +
//   写死 width:300px 静态留在栏内 ⇒ 当场把左栏撑出横向滚动条（实测溢出 73/15px）。
//   ⚠ 「页面级无横向溢出」那类判据（pixel_uat P7 问 document.documentElement）看不到这一层——
//   真正的滚动容器是左栏自己，所以本条的运行时判据必须遍历浮层的滚动祖先（见 P8）。
//
// 为什么是静态锁：这里锁的是**形态**（portal 到 body + 固定定位 + 现算夹边 + 外点放行腿），
//   真几何由 e2e/pixel_uat.spec.ts P8 在真浏览器里量（jsdom 不做布局，写在这里等于自欺）。
//   ⚠ 本文件必须留在 node 环境（vitest 缺省）读源文件：jsdom 下 import.meta.url 是 http: URL，
//   fileURLToPath 当场抛「The URL must be of scheme file」（同 lib/chunkRecovery.wiring.test.ts 的注释）。
//
// 运行：npx vitest run src/components/AccountMenu.noticePanel.lock.test.ts
// ============================================================================
import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

const PKG = fileURLToPath(new URL('../../', import.meta.url))
const FILE = 'src/components/AccountMenu.tsx'
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

/**
 * 判据函数化（正证喂真源文件、反证喂变异文本，同一个函数——AGENTS §三「判据级代替不了守卫级」）。
 * 返回违规清单，空数组＝合格。
 */
function noticePanelViolations(src: string): string[] {
  const out: string[] = []
  const code = stripComments(src)

  // ① 浮层必须 portal 到 body：脱离左栏（.lc-sidebar）这个滚动容器的排版流。
  if (!/\bcreatePortal\(/.test(code)) out.push('缺少 createPortal：浮层又回到左栏滚动容器内')
  if (!/document\.body/.test(code)) out.push('portal 目标不是 document.body')

  // ② 固定定位（不许回退成栏内 absolute）
  const amNoticeRule = code.match(/\.am-notice\{[^}]*\}/)?.[0] ?? ''
  if (!/position\s*:\s*fixed/.test(amNoticeRule)) out.push('.am-notice 不是 position:fixed')
  if (/position\s*:\s*absolute/.test(amNoticeRule)) out.push('.am-notice 仍是栏内 absolute（会撑开左栏）')

  // ③ 面板宽度不许写死（旧形态 width:300px 正是撑开 237/295px 左栏的根因）
  const amPanelRule = code.match(/\.am-notice \.bell-panel\{[^}]*\}/)?.[0] ?? ''
  if (!/width\s*:\s*100%/.test(amPanelRule)) out.push('.am-notice .bell-panel 宽度不是 100%')
  if (/width\s*:\s*\d+px/.test(amPanelRule)) out.push('.am-notice .bell-panel 宽度被写死 px')
  // box-sizing 两半之一下半：content-box 下 width:100% 会被面板自带的 8px 内垫 + 1.2px 描边再顶破约 18px
  if (!/box-sizing\s*:\s*border-box/.test(amPanelRule)) out.push('.am-notice .bell-panel 缺 box-sizing:border-box')

  // ④ 落点必须现算夹边（宽度上限 + 左右夹 + 高度上限），不许写死坐标
  const fn = code.match(/function placeNotice\(\)\s*\{[\s\S]*?\n  \}/)?.[0] ?? ''
  if (!fn) out.push('placeNotice() 不见了：落点改成了写死坐标')
  else {
    if (!/getBoundingClientRect\(\)/.test(fn)) out.push('placeNotice 不以触发器矩形为锚')
    if (!/Math\.min\(300,\s*window\.innerWidth - 24\)/.test(fn)) out.push('placeNotice 缺宽度自适应（窄屏会顶破视口）')
    if (!/Math\.min\(r\.left,\s*window\.innerWidth - width - 8\)/.test(fn)) out.push('placeNotice 缺左右夹边')
    if (!/Math\.min\(420,\s*r\.top - 16\)/.test(fn)) out.push('placeNotice 缺高度夹边（矮屏会越过视口上缘）')
  }

  // ⑤ portal 之后，外点关闭必须单独放行浮层内部点击（否则点面板里任何地方都会把面板关掉）
  if (!/noticeRef\.current\?\.contains/.test(code)) out.push('外点判定没有放行 noticeRef 内的点击')

  return out
}

describe('AccountMenu · 通知中心浮层形态（★ 2026-10-17 自适应修复）', () => {
  it('正证：真源文件齐全——portal 到 body + fixed + 宽度 100% + 现算夹边 + 外点放行', () => {
    expect(noticePanelViolations(SRC)).toEqual([])
  })

  it('自证：三条关键腿都在（射程不是被"处处命中"撑出来的）', () => {
    // 射程自证：三个锚点必须在文中真出现，否则上面的判据可能是恒空
    expect(SRC).toContain('createPortal(')
    expect(SRC).toContain('noticeRef.current?.contains')
    expect(SRC).toContain('function placeNotice()')
  })

  it('反证①：摘掉 portal（回到栏内渲染）必须判红', () => {
    const mut = SRC.replace('createPortal(', 'plainRender(').replace('document.body,', '{},')
    expect(noticePanelViolations(mut).length).toBeGreaterThan(0)
  })

  it('反证②：把 position:fixed 改回栏内 absolute 必须判红', () => {
    const mut = SRC.replace('.am-notice{position:fixed;z-index:70}', '.am-notice{position:absolute;z-index:70}')
    expect(mut).not.toBe(SRC)
    expect(noticePanelViolations(mut)).toContain('.am-notice 仍是栏内 absolute（会撑开左栏）')
  })

  it('反证③：把面板宽度写回 300px 必须判红（原缺陷形态）', () => {
    const mut = SRC.replace('.bell-panel{position:static;width:100%;box-sizing:border-box;max-height:inherit}',
      '.bell-panel{position:static;width:300px;max-height:min(420px,60vh)}')
    expect(mut).not.toBe(SRC)
    const v = noticePanelViolations(mut)
    expect(v).toContain('.am-notice .bell-panel 宽度不是 100%')
    expect(v).toContain('.am-notice .bell-panel 宽度被写死 px')
  })

  it('反证④：摘掉外点放行腿必须判红（portal 的下半条口径）', () => {
    const mut = SRC.replace('if (noticeRef.current?.contains(t)) return', 'void 0')
    expect(mut).not.toBe(SRC)
    expect(noticePanelViolations(mut)).toContain('外点判定没有放行 noticeRef 内的点击')
  })

  it('反证⑤：落点改回写死坐标必须判红', () => {
    const mut = SRC.replace(/function placeNotice\(\)\s*\{[\s\S]*?\n  \}/,
      'function placeNotice() {\n    setNotifyPos({ left: 0, bottom: 60, width: 300, maxH: 420 })\n  }')
    expect(mut).not.toBe(SRC)
    const v = noticePanelViolations(mut)
    expect(v).toContain('placeNotice 不以触发器矩形为锚')
    expect(v).toContain('placeNotice 缺宽度自适应（窄屏会顶破视口）')
  })
})
