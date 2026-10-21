// ============================================================================
// ForcePasswordGate.wiring.test.ts — 强制改密闸门的「接线」静态锁
// （★ 2026-10-21 IAM 重做 P0-8）
//
// 为什么行为测试之外还要一条接线锁：428 → 改密框这条链跨三个文件，任何一处被"顺手"
// 改掉都不会有编译错误，而且症状只在**特定账号**（must_change_pwd=1）身上出现：
//   · core.ts 派发 lc:force-password            （判据在 request() 的 428 分支）
//   · ForcePasswordGate 监听同一个事件名        （两处共用同一导出常量，不许各写一份字符串）
//   · main.tsx 把闸门挂在 ToastProvider 内      （不在 App/PublicRoot 的分诊分支里）
// 第三条最容易被"整理代码"删掉：组件还在、测试也还绿，只是再也没被渲染过。
// 另一条与体积有关：本组件在**入口包**里，只许从域文件直取接口——
// 一旦写成 `import { … } from '@/api'`，那个桶会把全部 API 域一起拉进入口 bundle，
// T8（入口 raw ≤420,000 B）当场被顶破（tsc/vitest 都看不见这一层）。
//
// 反证在末尾（每条负向判据都配"往那一侧塞个假目标必须判红"），
// 判据全部按源码现读，不钉行号。
// ============================================================================
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'

// 包根用 URL 解析（同 src/bootStorageGate.test.ts 口径）：cwd 可能不是包根。
// 本文件在 src/components/ 下，回退两级才是包根（写成一级会读到 src/src/main.tsx）。
const ROOT = fileURLToPath(new URL('../../', import.meta.url))
const read = (rel: string) => readFileSync(ROOT + rel, 'utf-8')

/** 剥掉 JS 注释（行注释 + 块注释，字符串/模板串内的斜杠不算注释）。
 *  这几份文件的说明注释里本来就会写到 `from '@/api'`、事件名与挂载标签，
 *  不剥注释的负向判据会被自己的注释喂饱——那是本仓反复踩过的「负向锁误伤注释」。
 *  ⚠️ 不能只做一条 `/\*[\s\S]*?\*\//` 正则：本仓注释里出现过 `api/*` 这种**行注释内的
 *  未闭合块注释记号**，正则会把从那里到下一处 `*\/` 之间的真实代码整段吃掉，
 *  于是判据读到的是一份"被注释掉的源码"（首跑真踩）。这里按字符状态机走。
 *  正则字面量未单独处理——这三份文件里没有含 `//` 的正则；将来若加，需补一个 regex 态。 */
function stripJsComments(src: string): string {
  let out = ''
  let mode: 'code' | 'line' | 'block' | 'single' | 'double' | 'tpl' = 'code'
  for (let i = 0; i < src.length; i++) {
    const c = src[i]
    const n = src[i + 1]
    if (mode === 'code') {
      if (c === '/' && n === '/') { mode = 'line'; i++; continue }
      if (c === '/' && n === '*') { mode = 'block'; i++; continue }
      if (c === "'") mode = 'single'
      else if (c === '"') mode = 'double'
      else if (c === '`') mode = 'tpl'
      out += c
      continue
    }
    if (mode === 'line') {
      if (c === '\n') { mode = 'code'; out += c }
      continue
    }
    if (mode === 'block') {
      if (c === '*' && n === '/') { mode = 'code'; i++; continue }
      if (c === '\n') out += c // 保留换行，行号对齐时可读
      continue
    }
    // 字符串/模板串：原样保留，跳过转义；遇到同种引号即退出
    out += c
    if (c === '\\') { out += src[i + 1] ?? ''; i++; continue }
    if ((mode === 'single' && c === "'") || (mode === 'double' && c === '"') || (mode === 'tpl' && c === '`')) mode = 'code'
  }
  return out
}

/** main.tsx 的挂载判据（返回失败项；空数组＝通过）。
 *  一处且仅一处 ＋ 在 ToastProvider 之内 ＋ 在入口 Suspense 之外（与 pickEntry 的选路无关）。 */
function mountFailures(src: string): string[] {
  const code = stripJsComments(src)
  const fails: string[] = []
  const tag = '<ForcePasswordGate />'
  const count = code.split(tag).length - 1
  if (count !== 1) fails.push(`挂载次数应为 1，实得 ${count}`)
  const at = code.indexOf(tag)
  const open = code.indexOf('<ToastProvider>')
  const close = code.indexOf('</ToastProvider>')
  if (at < 0 || open < 0 || close < 0) {
    fails.push('挂载点或 ToastProvider 边界读不到')
  } else if (!(open < at && at < close)) {
    fails.push('闸门不在 ToastProvider 之内（用不到 useToast / 组件可能根本没被渲染）')
  }
  const suspense = code.indexOf('<React.Suspense')
  if (at >= 0 && suspense >= 0 && at > suspense) {
    fails.push('闸门落进了入口分诊/Suspense 之后（未登录门面与工作台只有一支会渲染它）')
  }
  return fails
}

/** 入口包体积判据：本组件不许经 @/api 桶取接口（只认域文件直取）。 */
function barrelImportFailures(src: string): string[] {
  const code = stripJsComments(src)
  const fails: string[] = []
  if (/from\s+['"]@\/api['"]/.test(code)) fails.push("不许 `from '@/api'`（桶会把全部 API 域拉进入口包）")
  if (!/from\s+['"]@\/api\/auth['"]/.test(code)) fails.push("changePassword 必须从 '@/api/auth' 直取")
  if (!/from\s+['"]@\/api\/core['"]/.test(code)) fails.push("事件常量与 setAuthToken 必须从 '@/api/core' 直取")
  return fails
}

/** 监听判据：事件名必须用共用常量（不是手写字符串），且 add/remove 成对。 */
function listenFailures(src: string): string[] {
  const code = stripJsComments(src)
  const fails: string[] = []
  if (!/window\.addEventListener\(\s*FORCE_PASSWORD_EVENT\s*,\s*onForce\s*\)/.test(code)) {
    fails.push('没有监听 FORCE_PASSWORD_EVENT')
  }
  if (!/window\.removeEventListener\(\s*FORCE_PASSWORD_EVENT\s*,\s*onForce\s*\)/.test(code)) {
    fails.push('effect 清理时没摘监听（重复开框 / 泄漏）')
  }
  if (/addEventListener\(\s*['"]lc:force-password['"]/.test(code)) {
    fails.push('事件名被写成了字面量（两处各自维护，改一处就断）')
  }
  return fails
}

describe('ForcePasswordGate 接线锁（★ IAM P0-8）', () => {
  it('main.tsx：闸门恰好挂一次，且在 ToastProvider 之内、入口分诊之外', () => {
    const main = read('src/main.tsx')
    expect(mountFailures(main), '挂载接线不完整 ⇒ 428 会再次变成"没有任何地方能改密码"').toEqual([])
    // 正向对照：入口确实还在（否则上面那条会因为"文件没读到"而空转绿）
    expect(main).toContain('{React.createElement(pickEntry())}')
  })

  it('ForcePasswordGate.tsx：监听共用常量、成对摘除；只从域文件直取接口', () => {
    const gate = read('src/components/ForcePasswordGate.tsx')
    expect(listenFailures(gate)).toEqual([])
    expect(barrelImportFailures(gate)).toEqual([])
    // 正向对照：契约常量本身确实是从 core 导入的（否则监听的是别的东西）
    expect(gate).toMatch(/import\s*\{[^}]*FORCE_PASSWORD_EVENT[^}]*\}\s*from\s*['"]@\/api\/core['"]/)
  })

  it('api/core.ts：428 只在非认证前缀上派发，口令文案与事件名是对外契约', () => {
    const core = stripJsComments(read('src/api/core.ts'))
    // 事件名逐字（后端/组件两侧都认这一个字符串）
    expect(core).toMatch(/FORCE_PASSWORD_EVENT\s*=\s*'lc:force-password'/)
    // 派发形态：CustomEvent 包一层，浏览器外静默（不 throw）
    expect(core).toMatch(/new CustomEvent\(FORCE_PASSWORD_EVENT\)/)
    // 触发判据：状态码 428 且不是 /api/auth/*（认证域自己不该触发自己的闸）
    expect(core).toMatch(/response\.status\s*===\s*428\s*&&\s*!url\.startsWith\('\/api\/auth\/'\)/)
  })

  // 反证：每条负向判据都要证明"改坏真的会被抓"，否则零命中与"扫描器失明"同形。
  it('反证：摘掉挂载 / 挪出 ToastProvider / 写成桶导入，都必须判红', () => {
    const main = read('src/main.tsx')
    expect(mountFailures(main)).toEqual([]) // 基线绿

    const removed = main.replace('        <ForcePasswordGate />\n', '')
    expect(removed, '反证失效：变异没落地').not.toBe(main)
    expect(mountFailures(removed).length, '删掉挂载没被判红').toBeGreaterThan(0)

    const outside = main.replace('        <ForcePasswordGate />\n', '').replace('</ToastProvider>', '</ToastProvider>\n        <ForcePasswordGate />')
    expect(outside, '反证失效：变异没落地').not.toBe(main)
    expect(mountFailures(outside).length, '挪出 ToastProvider 没被判红').toBeGreaterThan(0)

    const gate = read('src/components/ForcePasswordGate.tsx')
    expect(barrelImportFailures(gate)).toEqual([]) // 基线绿
    const barrel = gate.replace("import { changePassword } from '@/api/auth'", "import { changePassword } from '@/api'")
    expect(barrel, '反证失效：变异没落地').not.toBe(gate)
    expect(barrelImportFailures(barrel).length, '写成桶导入没被判红').toBeGreaterThan(0)

    const noCleanup = gate.replace('return () => window.removeEventListener(FORCE_PASSWORD_EVENT, onForce)', 'return undefined')
    expect(noCleanup, '反证失效：变异没落地').not.toBe(gate)
    expect(listenFailures(noCleanup).length, '摘掉 effect 清理没被判红').toBeGreaterThan(0)
  })
})
