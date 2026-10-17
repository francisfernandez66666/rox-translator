// ============================================================================
// lib/chunkRecovery.wiring.test.ts — ★ 链路批④（#29）的**接线层**静态锁（E/F 段）
//
// 为什么单独一个文件：行为锁（A~D）要 window/location/history，走 jsdom（见同目录
// chunkRecovery.dom.test.tsx）；而本文件读的是**源文件文本**，jsdom 环境下
// `import.meta.url` 是 http: URL，fileURLToPath 当场抛「The URL must be of scheme file」
// ⇒ 整个 suite 起不来（首跑真踩，报错点在这一行而不是判据）。node 环境（vitest 缺省）
// 才有 file: URL，所以这两半必须分文件，别图省事合成一个。
//
// 锁的仍是那条老教训（AGENTS §三「判据级断言代替不了守卫级」）：
//   恢复腿的判据函数（isChunkLoadError / reloadForChunkFailure）全绿，
//   不代表**有人调用它**——把 main.tsx 那一行 install 摘掉，A~D 四条照样一条不红。
//   所以这里钉三处接线：main.tsx 在挂载之前装、App.tsx/PublicRoot.tsx 每处页面级懒加载都在路由级边界里、
//   边界（components/RouteBoundary.tsx，★ 批 B 从 App.tsx 搬出并改走 lib/nav）把 resetKey 交给当前路径。
//   判据写成可喂变异文本的函数，各配反证。
//
// 运行：npx vitest run src/lib/chunkRecovery.wiring.test.ts
// ============================================================================
import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

// 包根：本文件在 src/lib 下，往上**两级**才是包根（写错一级会读不到文件、闸门静默失明）
const PKG = fileURLToPath(new URL('../../', import.meta.url))


// ============ E/F 静态接线与「不碰存储」腿 ============
// 判据函数化：正证喂真源文件，反证喂摘掉一行的变异文本，同一个函数（AGENTS §三「判据级代替不了守卫级」）。

/** 去注释（负向锁误伤注释是本仓长期红灯来源）：行注释 + 块注释 */
function stripComments(src: string): string {
  return src.replace(/\/\*[\s\S]*?\*\//g, '').replace(/^\s*\/\/.*$/gm, '').replace(/(^|[^:'"`])\/\/[^\n]*$/gm, '$1')
}

/** main.tsx 接线判据：必须在 createRoot **之前**调用 installChunkRecovery() */
function mainWiringViolations(src: string): string[] {
  const out: string[] = []
  const code = stripComments(src)
  if (!code.includes('installChunkRecovery()')) out.push('main.tsx 没有调用 installChunkRecovery()')
  if (!/from\s+['"]\.\/lib\/chunkRecovery['"]/.test(code)) out.push('main.tsx 没有 import chunkRecovery')
  const call = code.indexOf('installChunkRecovery()')
  const mount = code.indexOf('ReactDOM.createRoot')
  if (call < 0 || mount < 0) out.push(`锚点缺失（调用 ${call} / 挂载 ${mount}），本锁已失明`)
  else if (call > mount) out.push('installChunkRecovery() 必须排在 createRoot 之前')
  return out
}

/** 路由级边界文件的判据（★ 2026-10-17 批 B：定义从 App.tsx 搬到 components/RouteBoundary.tsx）。
 *  批 B 把公开面拆成不带 react-router 的 PublicRoot，而公开面页面同样是懒加载的、也要这层边界，
 *  所以边界改读 `lib/nav` 的最小接口（带 router 时是真 location、不带时是 window.location）。
 *  ⚠️ 这里**额外**钉住"它不许引 react-router"：一引就等于把 router 拉回公开面（批 B 的全部收益）。 */
function boundaryViolations(src: string): string[] {
  const out: string[] = []
  const code = stripComments(src)
  if (!/export default function RouteBoundary\(\{ children \}: \{ children: ReactNode \}\)/.test(code)) {
    out.push('components/RouteBoundary.tsx 缺 RouteBoundary 定义（或签名被改动，请同步本锁）')
  }
  if (!/resetKey=\{pathname\}/.test(code)) out.push('RouteBoundary 没把 resetKey 交给当前路径')
  if (!/\buseNav\(\)/.test(code)) out.push('RouteBoundary 没走 lib/nav 的最小接口（公开面没有 router）')
  if (/react-router/.test(code)) out.push('RouteBoundary 引了 react-router（会把 367 KB 拉回公开面）')
  return out
}

/** 入口文件接线判据：页面级懒加载必须包在 RouteBoundary 里（App.tsx 与 PublicRoot.tsx 各判一次）
 *  ⚠️ fallback 组件两种形态都在射程内：App 用 `<PageLoading />`，公开面用 `<BootLoading />`
 *  （后者与首屏骨架同貌，且**必须非空**——见 BootLoading.tsx 文件头）。 */
function appWiringViolations(src: string, name: string): string[] {
  const out: string[] = []
  const code = stripComments(src)
  // 派生式判据：逐个页面级 Suspense 都要被 RouteBoundary 包着（不写死「有几处」）
  const wrapped = code.match(/<RouteBoundary>\s*<Suspense fallback=\{<[A-Za-z]+ \/>\}>/g) || []
  const nakedAll = code.match(/<Suspense fallback=\{<[A-Za-z]+ \/>\}>/g) || []
  if (nakedAll.length === 0) out.push(`${name} 页面级 Suspense 锚点数 0，本锁已失明`)
  if (wrapped.length === 0) out.push(`${name} 没有任何页面级 Suspense 被 RouteBoundary 包住`)
  if (wrapped.length < nakedAll.length) {
    out.push(`${name} 页面级 Suspense ${nakedAll.length} 处，只有 ${wrapped.length} 处被包住`)
  }
  return out
}

describe('E 接线在位（守卫级）', () => {
  const main = readFileSync(PKG + 'src/main.tsx', 'utf-8')
  const app = readFileSync(PKG + 'src/App.tsx', 'utf-8')
  const pub = readFileSync(PKG + 'src/PublicRoot.tsx', 'utf-8')
  const boundary = readFileSync(PKG + 'src/components/RouteBoundary.tsx', 'utf-8')

  it('main.tsx：挂载之前装恢复腿', () => {
    expect(mainWiringViolations(main), 'main.tsx 接线：\n').toEqual([])
  })
  it('App.tsx：每一处页面级懒加载都在路由级边界内', () => {
    expect(appWiringViolations(app, 'App.tsx'), 'App.tsx 接线：\n').toEqual([])
  })
  it('PublicRoot.tsx（★ 批 B 新入口）：每一处页面级懒加载都在路由级边界内', () => {
    expect(appWiringViolations(pub, 'PublicRoot.tsx'), 'PublicRoot.tsx 接线：\n').toEqual([])
  })
  it('RouteBoundary：定义在位、走 nav 最小接口、不引 react-router', () => {
    expect(boundaryViolations(boundary), 'RouteBoundary 接线：\n').toEqual([])
  })
  it('反证：摘掉调用／摘掉包裹／边界改用 router —— 各自必须判红', () => {
    expect(mainWiringViolations(main.replace('installChunkRecovery()', '')).length, '摘掉调用必须报违规').toBeGreaterThan(0)
    expect(appWiringViolations(app.replace(/<RouteBoundary>/g, ''), 'App.tsx').length, '摘掉包裹必须报违规').toBeGreaterThan(0)
    expect(appWiringViolations(pub.replace(/<RouteBoundary>/g, ''), 'PublicRoot.tsx').length, '公开面摘掉包裹必须报违规').toBeGreaterThan(0)
    expect(boundaryViolations(boundary.replace('resetKey={pathname}', '')).length, '摘掉 resetKey 必须报违规').toBeGreaterThan(0)
    expect(boundaryViolations(boundary + "\nimport { useLocation } from 'react-router-dom'\n").length,
      '边界引 react-router 必须报违规').toBeGreaterThan(0)
    // 顺序反了也要红（顺序锁两侧各一枚：正证在上面的用例，反证这里把调用挪到挂载之后）
    const moved = main.replace('\ninstallChunkRecovery()', '') + '\ninstallChunkRecovery()\n'
    expect(moved).not.toContain('installChunkRecovery()\n\nReactDOM.createRoot')
    expect(mainWiringViolations(moved), '调用挪到挂载之后必须报违规').toContain('installChunkRecovery() 必须排在 createRoot 之前')
  })
})

describe('F 恢复腿不碰浏览器存储（AGENTS §一·5 批⑤ 同族防线）', () => {
  const src = readFileSync(PKG + 'src/lib/chunkRecovery.ts', 'utf-8')
  it('去注释后源码里零 localStorage／sessionStorage 标识（护栏只走 URL hash）', () => {
    const code = stripComments(src)
    expect(code).not.toMatch(/localStorage|sessionStorage/)
  })
  it('自证：文件确实在讲护栏时提到过这两个词（否则上一条负向锁是空转）', () => {
    expect(src).toMatch(/localStorage\/sessionStorage/)
  })
})
