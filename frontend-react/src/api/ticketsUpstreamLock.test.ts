// ============================================================================
// src/api/ticketsUpstreamLock.test.ts — 工单「上游那一族」出栈**三面等值锁**（★ 2026-10-26 追加裁决九-1）
//
// 形态照抄 ticketsDegradedLock.test.ts（三面逐字相同，摘掉任一面即红）：
//   ① Go 出栈：`backend-go/internal/api/tickets.go` 的 ticketQualityView 必须带四个
//      omitempty tag：sources / unresolved_lines / upstream_dead_lines / truncated_lines；
//   ② TS 类型：`api/tickets.ts` 的 TicketQuality 必须声明同名可选 Record 字段；
//   ③ 渲染判据：`components/TicketsPage.tsx` 必须读 `q?.<键>` 四件、以
//      `upstreamLangs.length > 0` 为渲染条件、并把 `<UpstreamNoticeBlock q={detail?.quality} …/>`
//      接进详情面。
// 关键空值口径由 ③ 的读法保证：四个键**读不到＝这一单没有未译出行**（Go 侧 omitempty，
// 没有回填时键整个不出现），整块不渲染——不许出现"0 行未译出"的空态话。
// 反证 R1–R5：分别摘掉三面各自的腿后 judge 必须点名；每条先断言变异体与原文不同
// （空替换会把 no-op 伪装成"锁抓住了破坏"，本仓 2026-10-14 真踩）。
// ============================================================================
import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import path from 'node:path'

const HERE = path.dirname(fileURLToPath(import.meta.url))
const GO_VIEW = path.join(HERE, '..', '..', '..', 'backend-go', 'internal', 'api', 'tickets.go')
const TS_API = path.join(HERE, 'tickets.ts')
const PAGE = path.join(HERE, '..', 'components', 'TicketsPage.tsx')

// 剥注释再判：说明性注释里会写键名，不剥会把"注释冒充实现"判成通过。
function stripComments(src: string): string {
  return src.replace(/\/\*[\s\S]*?\*\//g, '').replace(/^\s*\/\/.*$/gm, '')
}

// 三面共同的四个键（名单只在这里出现一次，三面判据由它派生）
const KEYS = ['sources', 'unresolved_lines', 'upstream_dead_lines', 'truncated_lines'] as const

/** judge 返回三面各自缺了哪些腿（空数组＝三面等值）。参数都是**已剥注释**的源码正文。 */
export function judge(goSrc: string, tsSrc: string, pageSrc: string): string[] {
  const issues: string[] = []
  for (const key of KEYS) {
    // ① Go：ticketQualityView 的 struct tag（map 值类型 sources 是 string、其余是 int）
    const goVal = key === 'sources' ? 'map\\[string\\]string' : 'map\\[string\\]int'
    const goRe = new RegExp(`${key === 'sources' ? 'Sources' : key === 'unresolved_lines' ? 'UnresolvedLines' : key === 'upstream_dead_lines' ? 'UpstreamDeadLines' : 'TruncatedLines'}\\s+${goVal}\\s+\`json:"${key},omitempty"\``)
    if (!goRe.test(goSrc)) issues.push(`Go 出栈缺少 json:"${key},omitempty" 字段`)
    // ② TS：TicketQuality 的可选 Record 字段
    const tsVal = key === 'sources' ? 'Record<string, string>' : 'Record<string, number>'
    const tsRe = new RegExp(`${key}\\?:\\s*${tsVal.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}`)
    if (!tsRe.test(tsSrc)) issues.push(`TS 类型缺少 ${key}?: ${tsVal}`)
    // ③ 页面：渲染判据读同一个键
    const pageRe = new RegExp(`q\\?\\.${key}\\b`)
    if (!pageRe.test(pageSrc)) issues.push(`TicketsPage 未读 q?.${key}`)
  }
  // ③b 渲染条件与接线（缺任一条＝块读得到数据也不会出现在屏幕上）
  if (!/upstreamLangs\.length\s*>\s*0/.test(pageSrc)) issues.push('TicketsPage 诚实块缺少 upstreamLangs.length > 0 渲染条件')
  if (!/<UpstreamNoticeBlock q=\{detail\?\.quality\}/.test(pageSrc)) issues.push('TicketsPage 详情面未接 <UpstreamNoticeBlock q={detail?.quality}')
  return issues
}

const read = (p: string) => stripComments(readFileSync(p, 'utf8'))

describe('工单上游诚实块三面等值锁（九-1）', () => {
  it('L1 现存三面逐字一致（缺任一面即红）', () => {
    expect(judge(read(GO_VIEW), read(TS_API), read(PAGE))).toEqual([])
  })

  it('R1 摘掉 Go 的 truncated_lines tag → 必须报错', () => {
    const go = read(GO_VIEW)
    const mutated = go.replace(/TruncatedLines\s+map\[string\]int\s+`json:"truncated_lines,omitempty"`/, 'TruncatedLines map[string]int `json:"trunc_lines,omitempty"`')
    expect(mutated).not.toBe(go)
    expect(judge(mutated, read(TS_API), read(PAGE))).toContain('Go 出栈缺少 json:"truncated_lines,omitempty" 字段')
  })

  it('R2 把 TS 键名改成别处 → 必须报错', () => {
    const ts = read(TS_API)
    const mutated = ts.replace(/unresolved_lines\?:/, 'unresolvedLines?:')
    expect(mutated).not.toBe(ts)
    expect(judge(read(GO_VIEW), mutated, read(PAGE))).toContain('TS 类型缺少 unresolved_lines?: Record<string, number>')
  })

  it('R3 摘掉页面对 sources 的读取 → 必须报错', () => {
    const page = read(PAGE)
    const mutated = page.replace(/const sourcesMap = q\?\.sources \|\| \{\}/, 'const sourcesMap: Record<string, string> = {}')
    expect(mutated).not.toBe(page)
    expect(judge(read(GO_VIEW), read(TS_API), mutated)).toContain('TicketsPage 未读 q?.sources')
  })

  it('R4 把渲染条件写成恒真（>= 0）→ 必须报错（空态话的源头之一就是恒渲染）', () => {
    const page = read(PAGE)
    const mutated = page.replace(/upstreamLangs\.length\s*>\s*0/, 'upstreamLangs.length >= 0')
    expect(mutated).not.toBe(page)
    expect(judge(read(GO_VIEW), read(TS_API), mutated)).toContain('TicketsPage 诚实块缺少 upstreamLangs.length > 0 渲染条件')
  })

  it('R5 摘掉详情面的接线 → 必须报错', () => {
    const page = read(PAGE)
    const mutated = page.replace(/<UpstreamNoticeBlock q=\{detail\?\.quality\} lang=\{lang\} \/>/, '')
    expect(mutated).not.toBe(page)
    expect(judge(read(GO_VIEW), read(TS_API), mutated)).toContain('TicketsPage 详情面未接 <UpstreamNoticeBlock q={detail?.quality}')
  })
})
