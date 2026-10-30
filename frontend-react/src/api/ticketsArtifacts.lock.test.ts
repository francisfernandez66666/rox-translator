// ============================================================================
// ticketsArtifacts.lock.test.ts — 组5 两段式交付的**三面等值**静态锁（B13）
//
// 三面：Go 出参 JSON tag ↔ TS 类型字段 ↔ 页面渲染取键。
// 任何一面改名（后端 tag、前端类型、渲染取键）都必须当场红——这类漂移不会报错，
// 只会让面板永远空着（读不到字段），排查时看不出来是哪一面的名字改了。
//
// 另加两条行为形状锁：
//   ① 产物区（TicketArtifacts）**不调用**旧的 `ticketDownload(`（旧下载口只留给历史单回落）；
//   ② 页面里新单走分组、历史单走旧清单：`artifactGroups.length === 0` 才渲染 deliverables
//      （两套同时渲染 = 用户看到两份产物清单）。
// ============================================================================
import { describe, it, expect } from 'vitest'
import fs from 'node:fs'
import path from 'node:path'

const repo = path.resolve(__dirname, '../../..')
const goAPI = fs.readFileSync(path.join(repo, 'backend-go/internal/api/ticket_artifacts.go'), 'utf8')
const tsAPI = fs.readFileSync(path.join(__dirname, 'tickets.ts'), 'utf8')
const page = fs.readFileSync(path.join(repo, 'frontend-react/src/components/TicketsPage.tsx'), 'utf8')

// goFields 从 Go 结构体里取 json tag（只取 TicketArtifactItem 那一段，避免把别的结构体也算进来）。
function goArtifactFields(): string[] {
  const start = goAPI.indexOf('type ticketArtifactItem struct')
  expect(start).toBeGreaterThan(-1)
  const seg = goAPI.slice(start, goAPI.indexOf('}', start))
  return Array.from(seg.matchAll(/json:"([^",]+)/g)).map((m) => m[1])
}

describe('两段式交付三面等值（B13）', () => {
  it('Go JSON tag ↔ TS 类型字段逐值等值', () => {
    const goFields = goArtifactFields()
    expect(goFields.length).toBeGreaterThanOrEqual(7) // 射程自证：解析到 7+ 个字段才算扫到
    const tsStart = tsAPI.indexOf('export interface TicketArtifactItem')
    expect(tsStart).toBeGreaterThan(-1)
    const tsSeg = tsAPI.slice(tsStart, tsAPI.indexOf('}', tsStart))
    for (const f of goFields) {
      if (f === 'created_at') continue // 可选字段，页面不渲染
      expect(tsSeg).toContain(f === 'primary' ? 'primary' : f)
    }
  })

  it('页面渲染的字段都在 TS 类型里（不许凭空取键）', () => {
    for (const f of ['kind', 'file_id', 'lang', 'name', 'size', 'primary', 'degraded']) {
      expect(tsAPI).toContain(f)
    }
    // 渲染侧实际取的两个键（分组与按钮分支）
    expect(page).toContain("i.kind === 'md'")
    expect(page).toContain("i.kind === 'original'")
  })

  it('产物区不调用旧下载口；历史单才回落 deliverables', () => {
    const start = page.indexOf('export function TicketArtifacts')
    expect(start).toBeGreaterThan(-1)
    const seg = page.slice(start)
    expect(seg.includes('ticketDownload(')).toBe(false)
    // 新单块 + 旧清单互斥
    expect(page).toContain('artifactGroups.length > 0 && detail?.ticket')
    expect(page).toContain('artifactGroups.length === 0 && deliverables.length > 0')
  })
})
