// ============================================================================
// components/FrontShell.layout.lock.test.ts — 前台外壳布局静态锁（★ 2026-09-24 〇-S/#7）
// 背景：用户拍板「左侧汉堡退役；套餐/余额/账号并入右上角账号下拉；页脚回页脚位置（参考元宝）」。
// 本锁钉死改造后的结构口径，防止旧形态回潮：
//   ① App.tsx 不得再出现顶栏汉堡钮（Icon n="menu"）与导航 Drawer（含 menuOpen 状态）；
//   ② SiteFooter 挂在外壳列尾常驻（.app-main 之后），不再藏在任何抽屉里；
//   ③ 外壳改 height:100dvh + .app-main overflowY:auto（页脚常驻的前提，见 App.tsx 注释）；
//   ④ ChatWindow 不再写死 calc(100vh - 39px)——页脚占位后该算法必然把页脚顶出首屏。
// 纯字符串静态断言（node 环境读源码），不渲染组件；行为面由 AccountMenu.selfnav.dom.test.tsx 覆盖。
// ============================================================================
import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

const ROOT = resolve(__dirname, '../..')
const read = (p: string) => readFileSync(resolve(ROOT, p), 'utf8')

// 负向锁必须先剥注释：本批在 App.tsx/ChatWindow.tsx 里留了「旧写法已退役」的说明注释
// （含 menuOpen、calc(100vh - 39px) 等字样），不剥就会命中自己的历史记录（〇-M 注释批同款坑）。
// 剥法：/* */ 整段 + 行首或空白后的 // 到行尾；`https://` 这类冒号后紧跟的 // 不在射程（不误伤 URL）。
function stripComments(src: string): string {
  return src.replace(/\/\*[\s\S]*?\*\//g, '').replace(/(^|\s)\/\/.*$/gm, '$1')
}

describe('前台外壳布局锁（#7 汉堡退役）', () => {
  const app = stripComments(read('src/App.tsx'))

  it('顶栏无汉堡钮、无导航 Drawer / menuOpen', () => {
    expect(app).not.toMatch(/<Icon n="menu"/)
    expect(app).not.toMatch(/<Drawer\b/)
    expect(app).not.toMatch(/menuOpen/)
  })

  it('SiteFooter 常驻外壳（且出现在 .app-main 之后，不在抽屉里）', () => {
    expect(app).toContain('<SiteFooter />')
    expect(app.indexOf('className="app-main"')).toBeLessThan(app.indexOf('<SiteFooter />'))
  })

  it('外壳锁 100dvh、.app-main 接管滚动', () => {
    expect(app).toContain("height: '100dvh'")
    expect(app).toContain("overflowY: 'auto'")
  })

  it('ChatWindow 不再按「视口-顶栏」自算高度', () => {
    const cw = stripComments(read('src/components/ChatWindow.tsx'))
    expect(cw).not.toContain('calc(100vh - 39px)')
    expect(cw).toContain('flex: 1')
  })
})
