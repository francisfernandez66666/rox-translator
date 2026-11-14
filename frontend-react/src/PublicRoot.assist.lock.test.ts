// ============================================================================
// PublicRoot.assist.lock.test.ts — R1（施工图 §5 / §4.2.3）：/p/ 分支必须挂 <AiAssist />，
// 且三支营销页（/pricing、/compare、/）仍在。
//
// 反证由变异体 M0277 提供：删掉 /p/ 分支那一行 <AiAssist /> ⇒ 本锁判红。
// 内置反证腿：把真源码派生的变异喂进同一把判据，必须报「/p/ 分支没挂」，
// 证明本锁读的正是那一行、而不是「全文任意一处有 AiAssist 就算过」。
//
// 静态源码锁（读源文件、不起组件）⇒ 按仓内口径放 node 环境。
// ============================================================================
import { readFileSync } from 'node:fs'
import * as path from 'node:path'
import { describe, expect, it } from 'vitest'

const SRC = readFileSync(path.resolve(__dirname, 'PublicRoot.tsx'), 'utf8')

/** 剥注释：块注释 + JSX 注释 { /* … * / } + 整行 // 注释（只留执行代码形状） */
function stripComments(src: string): string {
  return src.replace(/\/\*[\s\S]*?\*\//g, '').replace(/^\s*\/\/.*$/gm, '')
}

/**
 * 四个 MarketingShell 分支各判一次：每段区间里必须出现 <AiAssist />。
 * 抽成纯函数是为了让「真源码」与「变异源码」喂**同一把尺子**（AGENTS §三 反证纪律）。
 * 区间按分支判据字面量切片，绝不写成「全文 count >= 1」那种弱判据。
 */
function assistBranchFailures(src: string): string[] {
  const bad: string[] = []
  const code = stripComments(src)

  const pIdx = code.indexOf("path.startsWith('/p/')")
  const loginIdx = code.indexOf('return <LoginScreen', pIdx < 0 ? 0 : pIdx)
  const idxPricing = code.indexOf("path === '/pricing'")
  const idxCompare = code.indexOf("path === '/compare'")
  const idxHome = code.indexOf("path === '/'")

  const blocks: Array<{ name: string; a: number; b: number }> = [
    { name: '/pricing', a: idxPricing, b: idxCompare },
    { name: '/compare', a: idxCompare, b: idxHome },
    { name: '/', a: idxHome, b: pIdx },
    { name: '/p/', a: pIdx, b: loginIdx },
  ]
  for (const { name, a, b } of blocks) {
    if (a < 0 || b < 0 || b <= a) {
      bad.push(`${name} 分支区间解析失败（锚点漂了，判据失明）`)
      continue
    }
    if (!code.slice(a, b).includes('<AiAssist')) {
      bad.push(`${name} 分支没挂 <AiAssist />`)
    }
  }
  // 射程自证：全文 AiAssist 标签数必须 ≥ 4，否则是解析失明而非「都过了」。
  const total = (code.match(/<AiAssist\s*\/>/g) || []).length
  if (total < 4) bad.push(`全文 <AiAssist /> 仅 ${total} 处，低于 4（解析失明或漏挂）`)
  return bad
}

describe('R1 /p/ 托管页与三支营销页各挂 AI 接待挂件（源码锁）', () => {
  it('R1 四个分支各挂 <AiAssist />（/p/ 必挂、三支营销页仍在）', () => {
    const bad = assistBranchFailures(SRC)
    expect(bad, 'R1 /p/ 分支漏挂 AiAssist 或三支营销页有缺失：' + bad.join('；')).toEqual([])
  })

  it('R1 反证：删掉 /p/ 那行 AiAssist，同一把判据必须判红（不是空转锁）', () => {
    // 变异从真源码派生，锚在 /p/ 分支独有的 ProductPublicPage 行之后。
    const mutated = SRC.replace(
      /(<ProductPublicPage tenantCode=\{tenantCode\} pageCode=\{pageCode\} \/>[\s\S]*?)<AiAssist \/>/,
      '$1',
    )
    expect(mutated, '反证失效：变异没改动源码（/p/ 分支锚点漂了）').not.toBe(SRC)
    const bad = assistBranchFailures(mutated)
    expect(bad.join('；'), '删掉 /p/ 那行却没被判红 ⇒ 本锁没在测那一行').toContain('/p/ 分支没挂 <AiAssist />')
    // 反向：三支营销页不受这次变异影响，仍须在（证明判据按分支各判，不是一损俱损）。
    expect(bad.join('；'), '误伤：/p/ 的变异不该让三支营销页也报缺').not.toContain('/pricing 分支没挂')
  })

  it('R1 射程自证：判据真解析到四段分支，且 /p/ 分支确在产品页之后', () => {
    const code = stripComments(SRC)
    expect(code.indexOf("path.startsWith('/p/')"), '解析不到 /p/ 分支').toBeGreaterThan(0)
    expect(code.indexOf('return <LoginScreen'), '解析不到兜底登录分支').toBeGreaterThan(0)
    expect((code.match(/<AiAssist\s*\/>/g) || []).length, 'AiAssist 挂载数不足 4').toBeGreaterThanOrEqual(4)
  })
})
