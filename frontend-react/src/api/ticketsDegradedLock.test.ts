// ============================================================================
// src/api/ticketsDegradedLock.test.ts — 降级提示**三面等值锁**（★ 2026-10-21 UAT 收口 M2）
//
// 缺陷形态（UAT 07M4／07M5 实测）：前端 TicketsPage 早就在读 `detail.ticket.degraded_langs`
// 并渲染「降级交付」横幅，而 Go 侧**从没出过这个键**（工单表没有列、出参没有键）——
// 两头各写各的，客户屏幕上永远看不到"这次交付降级了"。前端当时还留了句
// 「字段名假设：Go 侧另一 agent 尚未定稿」，把结构缺陷写成了临时状态。
//
// 现在两侧都已定稿，本锁钉住**三面逐字相同**，任一面漂移即红：
//   ① Go 出栈：`internal/api/points_view.go` 的 ticketJSON 必须写 `m["degraded_langs"]`
//      与 `m["gate_warnings"]`，且值走 store.DecodeTicketNotes（空值归一成 []）；
//   ② TS 类型：`api/tickets.ts` 必须声明同名可选数组字段；
//   ③ 渲染判据：`components/TicketsPage.tsx` 必须读 `detail?.ticket?.degraded_langs`
//      并以 `.length > 0` 作为横幅条件。
//
// 反证（R1–R3）：分别摘掉任一面后 judge 必须报出问题；且先断言变异体与原文不同，
// 避免"空替换＝no-op"把反证伪装成"锁抓住了破坏"（本仓 2026-10-14 真踩）。
// ============================================================================
import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import path from 'node:path'

const HERE = path.dirname(fileURLToPath(import.meta.url))
const GO_VIEW = path.join(HERE, '..', '..', '..', 'backend-go', 'internal', 'api', 'points_view.go')
const TS_API = path.join(HERE, 'tickets.ts')
const PAGE = path.join(HERE, '..', 'components', 'TicketsPage.tsx')

// 剥注释再判：说明性注释里会写键名（解释为什么要这条锁），不剥会把"注释冒充实现"判成通过。
function stripComments(src: string): string {
  return src.replace(/\/\*[\s\S]*?\*\//g, '').replace(/^\s*\/\/.*$/gm, '')
}

/**
 * judge 返回三面各自缺了哪些腿（空数组＝三面等值）。
 * 参数：goSrc/tsSrc/pageSrc 都是**已剥注释**的源码正文。
 * 返回值：可读问题清单；调用方按"必须为空"断言。
 */
export function judge(goSrc: string, tsSrc: string, pageSrc: string): string[] {
  const issues: string[] = []
  // ① Go 出栈：两个键都要写，且必须经统一解码口（空值 [] 不出 null）
  if (!/m\["degraded_langs"\]\s*=\s*store\.DecodeTicketNotes\(t\.DegradedLangsJSON\)/.test(goSrc)) {
    issues.push('Go 出参缺少 m["degraded_langs"] = store.DecodeTicketNotes(...)')
  }
  if (!/m\["gate_warnings"\]\s*=\s*store\.DecodeTicketNotes\(t\.GateWarningsJSON\)/.test(goSrc)) {
    issues.push('Go 出参缺少 m["gate_warnings"] = store.DecodeTicketNotes(...)')
  }
  // ② TS 类型：同名可选数组
  if (!/degraded_langs\?:\s*string\[\]/.test(tsSrc)) issues.push('TS 类型缺少 degraded_langs?: string[]')
  if (!/gate_warnings\?:\s*string\[\]/.test(tsSrc)) issues.push('TS 类型缺少 gate_warnings?: string[]')
  // ③ 渲染判据：读的是同一个键，且横幅以"有降级语言"为条件
  if (!/detail\?\.ticket\?\.degraded_langs/.test(pageSrc)) {
    issues.push('TicketsPage 未读 detail?.ticket?.degraded_langs')
  }
  if (!/degradedLangs\.length\s*>\s*0/.test(pageSrc)) {
    issues.push('TicketsPage 横幅缺少 degradedLangs.length > 0 条件')
  }
  return issues
}

describe('降级提示三面等值锁（M2）', () => {
  it('L1 现存三面逐字一致（缺任一面即红）', () => {
    const issues = judge(
      stripComments(readFileSync(GO_VIEW, 'utf8')),
      stripComments(readFileSync(TS_API, 'utf8')),
      stripComments(readFileSync(PAGE, 'utf8')),
    )
    expect(issues).toEqual([])
  })

  it('R1 摘掉 Go 出栈 → 必须报错', () => {
    const go = stripComments(readFileSync(GO_VIEW, 'utf8'))
    const mutated = go.replace(/m\["degraded_langs"\]\s*=\s*store\.DecodeTicketNotes\(t\.DegradedLangsJSON\)/, '')
    expect(mutated).not.toBe(go) // 反证先证"真改了"，否则空替换会把 no-op 读成锁生效
    expect(judge(mutated, stripComments(readFileSync(TS_API, 'utf8')), stripComments(readFileSync(PAGE, 'utf8')))).toContain(
      'Go 出参缺少 m["degraded_langs"] = store.DecodeTicketNotes(...)',
    )
  })

  it('R2 把 TS 键名改成别处 → 必须报错', () => {
    const ts = stripComments(readFileSync(TS_API, 'utf8'))
    const mutated = ts.replace(/degraded_langs\?:/, 'degradedLangs?:')
    expect(mutated).not.toBe(ts)
    expect(judge(stripComments(readFileSync(GO_VIEW, 'utf8')), mutated, stripComments(readFileSync(PAGE, 'utf8')))).toContain(
      'TS 类型缺少 degraded_langs?: string[]',
    )
  })

  it('R3 摘掉详情面读取 → 必须报错', () => {
    const page = stripComments(readFileSync(PAGE, 'utf8'))
    const mutated = page.replace(/detail\?\.ticket\?\.degraded_langs/g, 'detail?.ticket?.(undefined as any)')
    expect(mutated).not.toBe(page)
    const issues = judge(stripComments(readFileSync(GO_VIEW, 'utf8')), stripComments(readFileSync(TS_API, 'utf8')), mutated)
    expect(issues).toContain('TicketsPage 未读 detail?.ticket?.degraded_langs')
  })
})
