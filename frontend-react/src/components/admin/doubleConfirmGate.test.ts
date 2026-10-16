// ============================================================================
// admin/doubleConfirmGate.test.ts — 「一个删除动作弹两次确认框」家族级静态锁
// （★ 2026-10-16 用户当场报障「api key后台删不掉 / 弹窗两次」）
//
// 缺陷形态（不是某一处的笔误，是一种**可以继续长出来**的写法）：
//   操作列的 Link 自己先 `if (!(await confirmDialog(...))) return;` 一次，
//   然后调用一个**内部也带 confirmDialog** 的函数 ⇒ 用户要点两次「确定」才算数，
//   而且点第一次「确定」时请求还没发出去（第二次确认还挡在前面）——
//   用户看到的就是「点了没反应 / 删不掉」。本仓已在 TenantsP / OrgP / KbP 三处修掉同一形态。
//
// 判据（按**形状**判，不看文件里出现过什么字）：
//   A. 找出「外层 confirm + 紧跟调用某个函数」的点位；
//   B. 把那个被调函数在同一文件里展开：它体内**还有** confirmDialog ⇒ 双确认 ⇒ 判红；
//      （被调函数体内没有确认框 ⇒ 那是正常的"确认后干活"，放行：IndustriesP / PersonasP 即此形态）
//   C. 射程自证：扫描文件数有下限，且必须**至少看到一个**带 confirmDialog 的 danger 删除点位
//      ——「一条都没扫到」和「一处都没有」长得一样，前者更坏（闸门失明）。
// 反证（R1/R2）在本文件内直接喂历史形态的源码文本给判据函数：改判据方向时必须能被抓住，
//   否则这条锁就只是"当前这棵树恰好干净"。
// 运行：npx vitest run src/components/admin/doubleConfirmGate.test.ts
// ============================================================================
import { describe, it, expect } from 'vitest'
import { readFileSync, readdirSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

const HERE = dirname(fileURLToPath(import.meta.url))
const ROOTS = [HERE, join(HERE, '..')] // admin 面板 + 顶层组件（工单/聊天等）

// 剥行注释与块注释：说明性注释里出现这个形状（比如解释"旧写法是…"）不算违规，
//   否则下一次给这段写注脚就会把自己判红（同 §三「负向锁会被自己新写的注释再犯」）。
function stripComments(src: string): string {
  return src.replace(/\/\*[\s\S]*?\*\//g, '').replace(/^[ \t]*\/\/.*$/gm, '')
}

/** 找出「外层 confirm + 同一行紧跟调用某函数」的点位：[文件, 行号, 被调函数名] */
export function outerConfirmCalls(src: string): Array<{ line: number, callee: string }> {
  const out: Array<{ line: number, callee: string }> = []
  stripComments(src).split('\n').forEach((l, i) => {
    // 形态： if (!(await confirmDialog({ … }))) return; <ident>(…)
    const m = l.match(/if\s*\(\s*!\s*\(\s*await\s+confirmDialog\([^]*$/ )
    if (!m) return
    const tail = l.match(/\)\s*\)\s*\)\s*\)?\s*return;\s*([A-Za-z_$][\w$]*)\s*\(/)
    if (tail) out.push({ line: i + 1, callee: tail[1] })
  })
  return out
}

/** 被调函数在**同一个文件**里的函数体。
 *  口径＝花括号配平（不用"下一个顶层声明"切：面板里的函数都嵌在组件里，
 *  按顶层切会把整个组件吞进来，于是别的函数的确认框被算到这个函数头上 —— 那是判据自伤）。 */
export function bodyOf(src: string, fn: string): string | null {
  const clean = stripComments(src)
  const re = new RegExp(String.raw`(?:async\s+)?function\s+${fn}\s*\(`)
  const m = re.exec(clean)
  if (!m) return null
  const from = clean.indexOf('{', m.index)
  if (from < 0) return null
  let depth = 0
  for (let i = from; i < clean.length; i++) {
    const c = clean[i]
    if (c === '/' && clean[i + 1] === '*') { const e = clean.indexOf('*/', i + 2); i = e < 0 ? clean.length : e + 1; continue }
    if (c === '/' && clean[i + 1] === '/') { const e = clean.indexOf('\n', i); i = e < 0 ? clean.length : e; continue }
    if (c === '"' || c === "'" || c === '`') {
      // 跳过字符串字面量（含转义），免得里面的花括号把配平带偏
      for (let j = i + 1; j < clean.length; j++) {
        if (clean[j] === '\\') { j++; continue }
        if (clean[j] === c) { i = j; break }
      }
      continue
    }
    if (c === '{') depth++
    else if (c === '}') { depth--; if (depth === 0) return clean.slice(from, i + 1) }
  }
  return clean.slice(from)
}

/** 判据主体：返回违规点位（外层确认 + 被调函数内部也确认） */
export function findDoubleConfirms(files: Array<{ path: string, src: string }>) {
  const bad: Array<{ path: string, line: number, callee: string }> = []
  for (const f of files) {
    for (const hit of outerConfirmCalls(f.src)) {
      const body = bodyOf(f.src, hit.callee)
      if (body && body.includes('confirmDialog')) bad.push({ path: f.path, ...hit })
    }
  }
  return bad
}

function loadFiles(): Array<{ path: string, src: string }> {
  const out: Array<{ path: string, src: string }> = []
  for (const root of ROOTS) {
    for (const name of readdirSync(root)) {
      if (!name.endsWith('.tsx') || name.includes('.test.')) continue
      const p = join(root, name)
      out.push({ path: p.slice(p.indexOf('/src/') + 1), src: readFileSync(p, 'utf8') })
    }
  }
  return out
}

describe('家族级：一个删除动作只许弹一次确认框', () => {
  it('D1 全量扫描：不得存在「外层 confirm + 被调函数内部又 confirm」的双确认点位', () => {
    const files = loadFiles()
    // 射程自证①：扫到的文件数有下限（目录/后缀变了会让判据静默空转）
    expect(files.length, '扫描面过小 ⇒ 这条闸门已经失明，先修判据再谈结论').toBeGreaterThanOrEqual(25)
    // 射程自证②：必须真的看到带确认框的删除点位（否则"没有违规"可能只是"没扫到任何确认框"）
    const withConfirm = files.filter((f) => f.src.includes('confirmDialog')).length
    expect(withConfirm, '一处 confirmDialog 都没扫到 ⇒ 判据靶子漂了').toBeGreaterThanOrEqual(5)

    expect(findDoubleConfirms(files), '同一动作弹两次确认框：用户要点两次「确定」才生效（现网报「删不掉」）').toEqual([])
  })

  it('D2 反证 R1：喂历史形态（外层确认 + 内层确认）必须被判红', () => {
    const src = [
      'async function removeEntry(row) {',
      '  if (!(await confirmDialog({ body: "确定删除？" }))) return',
      '  await apiDelete(row.id)',
      '}',
      'export function P() {',
      '  return <Link onClick={async () => { if (!(await confirmDialog({ body: "确定删除？" }))) return; removeEntry(row) }}>删</Link>',
      '}',
    ].join('\n')
    // 注意：外层调用点在文件里排在函数体之后——判据按"同文件解析被调函数体"定位，不受顺序影响
    const bad = findDoubleConfirms([{ path: 'fake.tsx', src }])
    expect(bad.length, '历史形态必须被判红（否则这条锁只是"当前树恰好干净"）').toBe(1)
    expect(bad[0].callee).toBe('removeEntry')
  })

  it('D3 反证 R2：外层确认 + 被调函数**不带**确认（正常形态）必须放行', () => {
    const src = [
      'async function doDelete(row) {',
      '  const r = await runGuarded(() => apiDelete(row.id))',
      '  if (!r) return',
      '}',
      'export function P() {',
      '  return <Link onClick={async () => { if (!(await confirmDialog({ body: "确定删除？" }))) return; doDelete(row) }}>删</Link>',
      '}',
    ].join('\n')
    expect(findDoubleConfirms([{ path: 'fake2.tsx', src }]), '单确认形态被误判 ⇒ 闸门会被人摘掉').toEqual([])
  })

  it('D4 注释里的历史写法不算违规（解释"旧形态"不该把闸门自己判红）', () => {
    const src = [
      '// 旧形态：onClick={async () => { if (!(await confirmDialog({ body: "删除？" }))) return; removeEntry(row) }}',
      'async function removeEntry(row) {',
      '  if (!(await confirmDialog({ body: "删除？", danger: true }))) return',
      '  await apiDelete(row.id)',
      '}',
    ].join('\n')
    expect(findDoubleConfirms([{ path: 'fake3.tsx', src }])).toEqual([])
  })
})
