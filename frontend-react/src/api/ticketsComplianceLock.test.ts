// ============================================================================
// src/api/ticketsComplianceLock.test.ts — 工单「内容合规跳过」出栈**三面等值锁**（★ 2026-11-03 合规闸批）
//
// 形态照抄 ticketsUpstreamLock.test.ts（三面逐字相同，摘掉任一面即红）：
//   ① Go 出栈：`backend-go/internal/api/tickets.go` 的 ticketQualityView 必须带两个
//      omitempty tag：compliance_skipped（map[string]int）/ compliance_items（[]orchestrator.ComplianceItem）；
//   ② TS 类型：`api/tickets.ts` 的 TicketQuality 必须声明同名字段（Record<string, number> /
//      TicketComplianceItem[]），且 TicketComplianceItem 的五个键与 Go 侧
//      `orchestrator.ComplianceItem` 的 json tag 逐字等值；
//   ③ 渲染判据：`components/TicketsPage.tsx` 必须读 `q?.<键>` 两件、以「两半任一非空」为渲染条件、
//      并把 `<ComplianceNoticeBlock q={detail?.quality} …/>` 接进详情面。
//
// 空值口径由 ③ 的读法保证：两个键**读不到＝这一单没有合规跳过**（Go 侧 omitempty，没跳过时
// 键整个不出现），整块不渲染——不许出现"0 处跳过"的空态话。
//
// ★ 第四条腿（C5）是**措辞单一事实源**：处置说明的真值住在 `engine.SensitiveSkippedLead`
//   （服务端 .md 尾节与前端详情区两个渲染面共用一句）。前端只许渲染接口下发的 `lead`，
//   **一个字都不许抄**——抄一份＝第二个事实源，下一批改措辞只会改到其中一处（那正是
//   AGENTS §一·5「页脚尾注是两处面的同一句话」那条裁决的手法）。
//   负向锁必须配正向对照（`{it.lead}` 真被渲染），否则它会是一条"句子里本来就没有"的空转锁。
//
// 反证 R1–R6：分别摘掉三面各自的腿与两处措辞纪律后 judge 必须点名；每条先断言变异体与原文不同
// （空替换会把 no-op 伪装成"锁抓住了破坏"，本仓 2026-10-14 真踩）。
// ============================================================================
import { describe, it, expect } from 'vitest'
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import path from 'node:path'

const HERE = path.dirname(fileURLToPath(import.meta.url))
const REPO = path.resolve(HERE, '..', '..', '..')
const GO_VIEW = path.join(REPO, 'backend-go', 'internal', 'api', 'tickets.go')
const GO_ITEM = path.join(REPO, 'backend-go', 'internal', 'orchestrator', 'workflow.go')
const GO_SENSITIVE = path.join(REPO, 'backend-go', 'internal', 'engine', 'sensitive.go')
const TS_API = path.join(HERE, 'tickets.ts')
const PAGE = path.join(HERE, '..', 'components', 'TicketsPage.tsx')
const SRC_DIR = path.join(REPO, 'frontend-react', 'src')

// 剥注释再判：说明性注释里会写键名与措辞，不剥会把"注释冒充实现"判成通过。
function stripComments(src: string): string {
  return src.replace(/\/\*[\s\S]*?\*\//g, '').replace(/^\s*\/\/.*$/gm, '')
}

/** 出栈两键（名单只在这里出现一次，三面判据由它派生） */
const KEYS = ['compliance_skipped', 'compliance_items'] as const

/** 逐条记录的五键：Go json tag ↔ TS 字段名（等值由 C2 判） */
const ITEM_KEYS = ['lang', 'line', 'terms', 'side', 'lead'] as const

/** 从 Go 源码里取某个 const 的字符串值（跨「+ 拼接」两行；取不到回 ""）。 */
function goConstValue(src: string, name: string): string {
  const re = new RegExp(`const\\s+${name}\\s*=\\s*((?:"(?:[^"\\\\]|\\\\.)*"\\s*\\+?\\s*)+)`)
  const m = re.exec(src)
  if (!m) return ''
  const parts = m[1].match(/"(?:[^"\\]|\\.)*"/g) || []
  return parts.map((p) => JSON.parse(p) as string).join('')
}

/** judge 返回各面缺了哪些腿（空数组＝三面等值）。参数都是**已剥注释**的源码正文。 */
export function judge(goSrc: string, tsSrc: string, pageSrc: string): string[] {
  const issues: string[] = []
  for (const key of KEYS) {
    // ① Go：ticketQualityView 的 struct tag
    const goField = key === 'compliance_skipped' ? 'ComplianceSkipped' : 'ComplianceItems'
    const goType = key === 'compliance_skipped' ? 'map\\[string\\]int' : '\\[\\]orchestrator\\.ComplianceItem'
    const goRe = new RegExp(`${goField}\\s+${goType}\\s+\`json:"${key},omitempty"\``)
    if (!goRe.test(goSrc)) issues.push(`Go 出栈缺少 json:"${key},omitempty" 字段`)
    // ② TS：TicketQuality 的可选字段
    const tsType = key === 'compliance_skipped' ? 'Record<string, number>' : 'TicketComplianceItem\\[\\]'
    const tsRe = new RegExp(`${key}\\?:\\s*${tsType}`)
    if (!tsRe.test(tsSrc)) issues.push(`TS 类型缺少 ${key}?: ${tsType.replace(/\\/g, '')}`)
    // ③ 页面：渲染判据读同一个键
    if (!new RegExp(`q\\?\\.${key}\\b`).test(pageSrc)) issues.push(`TicketsPage 未读 q?.${key}`)
  }
  // ③b 渲染条件与接线（缺任一条＝块读得到数据也不会出现在屏幕上）
  if (!/if\s*\(!\(langs\.length\s*>\s*0\)\s*&&\s*!\(items\.length\s*>\s*0\)\)\s*return null/.test(pageSrc)) {
    issues.push('TicketsPage 合规块缺少「两半任一非空才渲染」的判据')
  }
  if (!/<ComplianceNoticeBlock q=\{detail\?\.quality\}/.test(pageSrc)) {
    issues.push('TicketsPage 详情面未接 <ComplianceNoticeBlock q={detail?.quality}')
  }
  // ③c 处置说明只许渲染接口下发的值（正向对照：缺这一条整块就没有那句话）
  if (!/\{it\.lead\}/.test(pageSrc)) issues.push('TicketsPage 未渲染接口下发的 item.lead')
  return issues
}

const read = (p: string) => stripComments(readFileSync(p, 'utf8'))

describe('工单合规跳过三面等值锁（2026-11-03）', () => {
  it('C1 现存三面逐字一致（缺任一面即红）', () => {
    expect(judge(read(GO_VIEW), read(TS_API), read(PAGE))).toEqual([])
  })

  it('C2 逐条记录五键与 Go 侧 json tag 逐字等值', () => {
    const goItem = read(GO_ITEM)
    const ts = read(TS_API)
    const block = /export interface TicketComplianceItem \{([\s\S]*?)\n\}/.exec(ts)
    expect(block, 'TS 里找不到 TicketComplianceItem 接口').not.toBeNull()
    const tsBody = block![1]
    for (const key of ITEM_KEYS) {
      const goRe = new RegExp(`${key === 'lang' ? 'Lang' : key === 'line' ? 'Line' : key === 'terms' ? 'Terms' : key === 'side' ? 'Side' : 'Lead'}\\s+\\S+\\s+\`json:"${key}(,omitempty)?"\``)
      expect(goRe.test(goItem), `Go 侧 ComplianceItem 缺 json:"${key}" tag`).toBe(true)
      expect(new RegExp(`\\b${key}\\??:`).test(tsBody), `TS 侧 TicketComplianceItem 缺 ${key}`).toBe(true)
    }
    // 两个成因档字面量也是对外契约：Go 只有 input/output 两值
    expect(/ComplianceSideInput\s*=\s*"input"/.test(goItem) && /ComplianceSideOutput\s*=\s*"output"/.test(goItem)).toBe(true)
  })

  it('C5 前端一个字都不许抄处置说明（措辞唯一事实源在 Go）', () => {
    const goSensitive = readFileSync(GO_SENSITIVE, 'utf8')
    const skippedLead = goConstValue(goSensitive, 'SensitiveSkippedLead')
    const fileLead = goConstValue(goSensitive, 'SensitiveBlockedLeadForFile')
    // 射程自证：两条常量真取到了值，否则下面的负向锁是空转（"没取到＝一定不含"）
    expect(skippedLead.length).toBeGreaterThan(10)
    expect(fileLead.length).toBeGreaterThan(10)

    const files: string[] = []
    const walk = (dir: string) => {
      for (const name of readdirSync(dir)) {
        const p = path.join(dir, name)
        if (statSync(p).isDirectory()) { walk(p); continue }
        if (!/\.(ts|tsx)$/.test(name)) continue
        if (/\.(test|spec)\.(ts|tsx)$/.test(name) || name.includes('.dom.test.')) continue
        files.push(p)
      }
    }
    walk(SRC_DIR)
    // 逐文件比对「原文含那句」；两族引导语各判一次（工单通道/文件通道交付形态不同，共用一句会写错）
    const hits = files.filter((p) => {
      const src = stripComments(readFileSync(p, 'utf8'))
      return src.includes(skippedLead) || src.includes(fileLead)
    })
    expect(hits.map((p) => path.relative(REPO, p)), '前端出现了处置说明的第二份措辞').toEqual([])
    // 正向对照：那句话在唯一事实源里确实存在（否则本锁判的是一个不存在的串）
    expect(readFileSync(GO_SENSITIVE, 'utf8').includes(skippedLead)).toBe(true)
    expect(read(PAGE)).toMatch(/\{it\.lead\}/)
  })

  it('R1 摘掉 Go 的 compliance_items tag → 必须报错', () => {
    const go = read(GO_VIEW)
    const mutated = go.replace(/ComplianceItems\s+\[\]orchestrator\.ComplianceItem\s+`json:"compliance_items,omitempty"`/, 'ComplianceItems []orchestrator.ComplianceItem `json:"comp_items,omitempty"`')
    expect(mutated).not.toBe(go)
    expect(judge(mutated, read(TS_API), read(PAGE))).toContain('Go 出栈缺少 json:"compliance_items,omitempty" 字段')
  })

  it('R2 把 TS 键名改成驼峰 → 必须报错', () => {
    const ts = read(TS_API)
    const mutated = ts.replace(/compliance_skipped\?:/, 'complianceSkipped?:')
    expect(mutated).not.toBe(ts)
    expect(judge(read(GO_VIEW), mutated, read(PAGE))).toContain('TS 类型缺少 compliance_skipped?: Record<string, number>')
  })

  it('R3 摘掉页面对 compliance_items 的读取 → 必须报错', () => {
    const page = read(PAGE)
    const mutated = page.replace(/const items = q\?\.compliance_items \|\| \[\]/, 'const items: TicketComplianceItem[] = []')
    expect(mutated).not.toBe(page)
    expect(judge(read(GO_VIEW), read(TS_API), mutated)).toContain('TicketsPage 未读 q?.compliance_items')
  })

  it('R4 把渲染条件写成恒渲染 → 必须报错（"0 处跳过"的空态话就长在这里）', () => {
    const page = read(PAGE)
    const mutated = page.replace(/if\s*\(!\(langs\.length\s*>\s*0\)\s*&&\s*!\(items\.length\s*>\s*0\)\)\s*return null/, 'if (false) return null')
    expect(mutated).not.toBe(page)
    expect(judge(read(GO_VIEW), read(TS_API), mutated)).toContain('TicketsPage 合规块缺少「两半任一非空才渲染」的判据')
  })

  it('R5 摘掉详情面的接线 → 必须报错', () => {
    const page = read(PAGE)
    const mutated = page.replace(/<ComplianceNoticeBlock q=\{detail\?\.quality\} lang=\{lang\} \/>/, '')
    expect(mutated).not.toBe(page)
    expect(judge(read(GO_VIEW), read(TS_API), mutated)).toContain('TicketsPage 详情面未接 <ComplianceNoticeBlock q={detail?.quality}')
  })

  it('R6 把渲染改成前端自造措辞（不读 item.lead）→ 必须报错', () => {
    const page = read(PAGE)
    const mutated = page.replace(/\{it\.lead\}/, "{it.side === 'input' ? 'skipped' : 'dropped'}")
    expect(mutated).not.toBe(page)
    expect(judge(read(GO_VIEW), read(TS_API), mutated)).toContain('TicketsPage 未渲染接口下发的 item.lead')
  })
})
