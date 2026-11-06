// @vitest-environment jsdom
// ============================================================================
// ProductShelf.exhausted.dom.test.tsx — 额度耗尽态「两副面孔」纪律（施工_一期 §8.5／§9.2）
//
// X1 访客面：出现「销售忙／留言」，且不出现技术原因（额度／token）；
// X2 卖家面：出现真实原因与 resets_at，且不出现「销售忙」；
// X3 两面各只此一处出口：组件与映射源码的执行代码里不许自造「销售忙」文案（第三处）；
// X4 available 分支绝不吐「销售忙」，并配**源码变异反证**（把兜底改成 BUSY_KEY 必须红）；
// X5 resets_at 缺失时落「暂不可用」那一档，不退化成「销售忙」（读数不许为失败背书）。
//
// 与 noManualPromise.lock.test.ts 的分工：那边管「这句文案过不过禁词表」，这边管
// 「这副面孔在哪个 reason 出栈」——两把尺子各治一病，不许互相代替。
// 运行：npx vitest run src/ProductShelf.exhausted.dom.test.tsx
// ============================================================================
import { cleanup, render, screen } from '@testing-library/react'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { afterEach, describe, expect, it } from 'vitest'

import { ExhaustNotice } from './components/ProductShelf'
import { BUSY_COPY_KEY, copyFor, copyText } from './lib/productExhaustCopy'

/** 读源码（X3／X4 的静态判据要按实现行说话，注释里提到「销售忙」不算自造文案） */
function readSrc(rel: string): string {
  return readFileSync(fileURLToPath(new URL(rel, import.meta.url)), 'utf-8')
}

/** 剥注释：块注释 + 整行行注释；只留执行代码供字面量判据使用 */
function stripComments(src: string): string {
  return src.replace(/\/\*[\s\S]*?\*\//g, '').replace(/^\s*\/\/.*$/gm, '')
}

/**
 * 映射表源码判据：copyFor 的 BUSY_KEY 出口必须恰为 1 处，且兜底（available）不许落 BUSY_KEY。
 * 抽成纯函数是为了让 X4 的「变异体」与真实源码喂**同一把尺子**（AGENTS §三 反证纪律）。
 */
function copyMapFailures(src: string): string[] {
  const bad: string[] = []
  const code = stripComments(src)
  const m = code.match(/export function copyFor\([\s\S]*?\n\}/)
  if (!m) return ['读不到 copyFor 函数体（锚点漂了，判据失明）']
  const returns = [...m[0].matchAll(/return\s+\{\s*key:\s*(\w+)/g)].map((x) => x[1])
  if (returns.length === 0) return ['copyFor 里没有映射 return（判据空转）']
  const last = returns[returns.length - 1]
  if (last === 'BUSY_KEY') bad.push('copyFor 兜底分支返回 BUSY_KEY：available 也会吐「销售忙」')
  const busyCount = returns.filter((k) => k === 'BUSY_KEY').length
  if (busyCount !== 1) bad.push(`BUSY_KEY 出口应恰为 1 处（实测 ${busyCount}）：「销售忙」只许 exhausted_today/visitor 那一支`)
  return bad
}

const COPY_SRC = readSrc('./lib/productExhaustCopy.ts')

afterEach(() => cleanup())

describe('额度耗尽态两副面孔（§8.5 X1–X5）', () => {
  it('X1 访客面：出现「销售忙／留言」，不出现「额度／token」', () => {
    render(<ExhaustNotice reason="exhausted_today" audience="visitor" />)
    const text = screen.getByTestId('exhaust-copy').textContent || ''
    expect(text).toContain('销售忙')
    expect(text).toContain('留言')
    expect(text).not.toContain('额度')
    expect(text).not.toContain('token')
  })

  it('X2 卖家面：出现真实原因与 resets_at，不出现「销售忙」', () => {
    render(<ExhaustNotice reason="exhausted_today" audience="owner" resetsAt="18:00" />)
    const text = screen.getByTestId('exhaust-copy').textContent || ''
    expect(text).toContain('额度')
    expect(text).toContain('18:00')
    expect(text).not.toContain('销售忙')
  })

  it('X3 两面各只此一处出口：组件/映射源码不许自造「销售忙」文案（配正向对照）', () => {
    for (const rel of ['./components/ProductShelf.tsx', './components/ProductPublicPage.tsx', './lib/productExhaustCopy.ts']) {
      expect(stripComments(readSrc(rel)), `${rel} 里自造了「销售忙」文案（第三处出口）`).not.toContain('销售忙')
    }
    // 正向对照：词典里确实有这句，否则上面那条「不许出现」就是空转的负向锁
    expect(readSrc('./i18n/dicts.zh.ts')).toContain('销售忙')
    expect(copyMapFailures(COPY_SRC)).toEqual([])
  })

  it('X4 available 分支绝不吐「销售忙」＋源码变异反证（改成无条件 BUSY_KEY 必须红）', () => {
    const c = copyFor('available', 'visitor')
    expect(c.busy).toBe(false)
    expect(c.key).not.toBe(BUSY_COPY_KEY)
    expect(copyText('available', 'visitor')).not.toContain('销售忙')

    // 变异体：把 available 的兜底分支也改成吐「销售忙」⇒ 判据必须当场红
    const mutated = COPY_SRC.replace(
      'return { key: READY_KEY, busy: false, vars: {} }',
      'return { key: BUSY_KEY, busy: true, vars: {} }')
    expect(mutated, '反证失效：变异没落地（兜底分支字符串没命中）').not.toBe(COPY_SRC)
    expect(copyMapFailures(mutated).join('\n'), 'available 分支吐「销售忙」却没被判红').toContain('BUSY_KEY')
    expect(copyMapFailures(COPY_SRC), '对照组：真实映射必须干净').toEqual([])
  })

  it('X5 resets_at 缺失时落「暂不可用」档，不退化成「销售忙」', () => {
    const c = copyFor('exhausted_today', 'owner')
    expect(c.busy).toBe(false)
    expect(c.key).not.toBe(BUSY_COPY_KEY)
    const text = copyText('exhausted_today', 'owner')
    expect(text).toContain('暂不可用')
    expect(text).not.toContain('销售忙')
  })

  it('disabled 与 available 都是非「销售忙」档（三态不同形，§5.2）', () => {
    expect(copyText('disabled', 'visitor')).not.toContain('销售忙')
    expect(copyText('available', 'owner')).not.toContain('销售忙')
    expect(copyText('disabled', 'visitor')).not.toBe(copyText('available', 'visitor'))
  })
})
