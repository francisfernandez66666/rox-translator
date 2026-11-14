// ============================================================================
// api/assist.pageHeader.lock.test.ts — R2（施工图 §5 / §4.2.3）：页面上下文头四腿判据。
//   greet 有 / chat 有 / history 无 / features 无 —— 四腿各判，不是「至少一处有」。
//
// 反证由变异体 M0278 提供：把 greet 的 pageHeader(page) 摘掉 ⇒ 本锁判红。
// 内置反证腿（两条，互不覆盖）：
//   ① 只给 chat 加、greet 漏掉 ⇒ 报「greet 缺页面头」；
//   ② 给 history 也加 pageHeader ⇒ 报「history 不该送页面头」。
//
// 静态源码锁（读源文件、不发请求）⇒ node 环境。判据按四个函数的声明区间切片，
// 先剥注释（豁免理由的注释里满是「页面头」字样，不剥会误命中）。
// ============================================================================
import { readFileSync } from 'node:fs'
import * as path from 'node:path'
import { describe, expect, it } from 'vitest'

// 本文件位于 src/api/ 下，assist.ts 与它同目录（不要多拼一层 api/）。
const SRC = readFileSync(path.resolve(__dirname, 'assist.ts'), 'utf8')

/** 剥注释：块注释 + 整行 // 注释（含 JSDoc；只留执行代码形状） */
function stripComments(src: string): string {
  return src.replace(/\/\*[\s\S]*?\*\//g, '').replace(/^\s*\/\/.*$/gm, '')
}

const FN_NAMES = ['assistGreet', 'assistChat', 'assistHistory', 'assistFeatures'] as const

/**
 * 四腿判据：按函数声明把源码切成四段区间，各查 pageHeader( 调用。
 * 抽成纯函数，让「真源码 / 内置变异 / 外部变异」喂同一把尺子（AGENTS §三）。
 */
function headerFailures(src: string): string[] {
  const bad: string[] = []
  const code = stripComments(src)
  const starts = FN_NAMES.map((n) => ({ n, i: code.indexOf(`export async function ${n}(`) }))
  for (const { n, i } of starts) {
    if (i < 0) bad.push(`${n} 函数声明解析不到（锚点漂了，判据失明）`)
  }
  if (bad.length) return bad

  const regions = starts.map((s, k) => ({
    n: s.n,
    body: code.slice(s.i, k + 1 < starts.length ? starts[k + 1].i : code.length),
  }))
  const has = (n: string) => regions.find((r) => r.n === n)!.body.includes('pageHeader(')

  if (!has('assistGreet')) bad.push('greet 缺页面头')
  if (!has('assistChat')) bad.push('chat 缺页面头')
  if (has('assistHistory')) bad.push('history 不该送页面头')
  if (has('assistFeatures')) bad.push('features 不该送页面头')
  return bad
}

describe('R2 页面上下文头四腿各判（greet/chat 有，history/features 无）', () => {
  it('R2 真源码四腿全对', () => {
    const bad = headerFailures(SRC)
    expect(bad, 'R2 页面头四腿不全：' + bad.join('；')).toEqual([])
  })

  it('R2 反证①：把 greet 的页面头摘掉，判据必须报 greet 缺头', () => {
    const mutated = SRC.replace('headers: pageHeader(page),', 'headers: {},')
    expect(mutated, '反证失效：greet 头锚点漂了（变异没落地）').not.toBe(SRC)
    expect(headerFailures(mutated).join('；'), 'greet 漏送却没被判红').toContain('greet 缺页面头')
  })

  it('R2 反证②：给 history 也补页面头，判据必须报 history 不该送', () => {
    const code = stripComments(SRC)
    const hIdx = code.indexOf('export async function assistHistory(')
    expect(hIdx, '解析不到 assistHistory 声明').toBeGreaterThan(0)
    const mutated = code.slice(0, hIdx) + code.slice(hIdx).replace('{', '{ pageHeader("x");')
    expect(mutated, '反证失效：history 注入没落地').not.toBe(code)
    expect(headerFailures(mutated).join('；'), 'history 混入头却没被判红').toContain('history 不该送页面头')
  })

  it('R2 射程自证：四个函数区间都真解析出来，且 pageHeader 定义不在任一区间内误命中', () => {
    const code = stripComments(SRC)
    for (const n of FN_NAMES) {
      expect(code.indexOf(`export async function ${n}(`), `解析不到 ${n}`).toBeGreaterThan(0)
    }
    // 正向对照：greet/chat 两处调用点确实各出现一次 pageHeader(（否则「有」那两腿是空转）。
    expect((code.match(/pageHeader\(/g) || []).length, 'pageHeader( 调用点数异常（定义行被算进去了？）').toBeGreaterThanOrEqual(2)
  })
})
