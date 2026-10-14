// ============================================================================
// noticeRouting.lock.test.ts — #46 站内信落点表的跨语言等值锁＋守卫级接线锁（2026-10-14）
//
// 这一条锁问的是三件事，缺一件都会让"一把尺子"退化成两份各自演化的清单：
//   A. 名单等值：后端 store.NotifyRefTypes 的**取值集合**必须与前端 NOTICE_LANDINGS 的键集合逐字相同
//      （后端加一类而前端没落点＝那条通知点了没反应；前端多一类＝永远命中不到的死映射）。
//   B. 落点合法：表里每个 panel 必须是 stores/admin.tsx 的 PanelKey 字面量，
//      每个 minLevel 必须与 AdminDashboard 菜单里同一 key 的档位一致
//      （菜单按 minLevel 过滤、renderPanel 不按等级过滤 ⇒ 门槛写错就是给用户一条"点进去全线 403 的死路"）。
//   C. 守卫级接线：Bell.tsx 必须真的经 noticeLandingFor 取落点，且**不许**再留第二条
//      按 ref_type 硬编码的分支（只测映射表本身是判据级，代替不了"消费方确实走这张表"）。
//
// 判据一律读**剥注释后**的源码：这类"某串字面在不在"的锁会被说明注释冒充（AGENTS §一·9 本批真踩）。
// 运行：npx vitest run src/lib/noticeRouting.lock.test.ts
// ============================================================================
// @vitest-environment node
import { describe, expect, it } from 'vitest'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { NOTICE_LANDINGS, NOTICE_FALLBACK_PANEL, noticeLandingFor, noticeLandingKeys } from './noticeRouting'

// ROOT = frontend-react 包根，所以后端路径一律带 `../`（少一个就是 ENOENT，且是整文件一条不跑的那种红）
const ROOT = fileURLToPath(new URL('../../', import.meta.url))
const read = (p: string) => readFileSync(resolve(ROOT, p), 'utf-8')

/** 剥 Go 源码注释（块注释＋行注释），只留真会被编译的那一份文本 */
function stripGoComments(src: string): string {
  return src
    .replace(/\/\*[\s\S]*?\*\//g, '')
    .split('\n')
    .map((l) => l.replace(/\/\/.*$/, ''))
    .filter((l) => !/^\s*\/\//.test(l))
    .join('\n')
}

/** 从 Go 的 const 块里取 name→字面量（只认 `Name = "value"` 这一种写法，本文件即此形态） */
function goConstLiterals(src: string, namePrefix: string): Map<string, string> {
  const out = new Map<string, string>()
  const re = new RegExp(`(^|\\n)\\s*(${namePrefix}\\w*)\\s*=\\s*"([^"]+)"`, 'g')
  let m: RegExpExecArray | null
  while ((m = re.exec(src)) !== null) out.set(m[2], m[3])
  return out
}

/** 从 `var NotifyRefTypes = []string{ ... }` 里按顺序取出成员标识符 */
function goSliceMembers(src: string, varName: string): string[] {
  const i = src.indexOf(`var ${varName} = []string{`)
  if (i < 0) return []
  const open = src.indexOf('{', i)
  let depth = 0
  let end = -1
  for (let k = open; k < src.length; k++) {
    if (src[k] === '{') depth++
    else if (src[k] === '}') {
      depth--
      if (depth === 0) {
        end = k
        break
      }
    }
  }
  if (end < 0) return []
  return src
    .slice(open + 1, end)
    .split(',')
    .map((s) => s.trim())
    .filter(Boolean)
}

// ---- A. 名单等值 ----
const refSrc = stripGoComments(read('../backend-go/internal/store/notifications_ref.go'))
const constLits = goConstLiterals(refSrc, 'NotifyRef')
const listedNames = goSliceMembers(refSrc, 'NotifyRefTypes')

describe('A 名单等值：后端 ref_type 名单 ⇄ 前端落点表', () => {
  it('A0 解析射程非空（锚漂了会让所有等值断言集体假绿）', () => {
    expect(constLits.size).toBeGreaterThanOrEqual(6)
    expect(listedNames.length).toBeGreaterThanOrEqual(6)
    // 正对照：名单里必须真的含有"后端在写、前端在跳"这两类，否则上面的"非空"可能是解析噪声
    for (const k of ['NotifyRefTicket', 'NotifyRefFeedback', 'NotifyRefKbScrape', 'NotifyRefPayManual']) {
      expect(constLits.has(k), `常量 ${k} 未被解析到`).toBe(true)
      expect(listedNames).toContain(k)
    }
  })

  it('A1 后端名单的值集合 == 前端落点表的键集合（两侧缺一即红）', () => {
    const goVals = new Set(listedNames.map((n) => constLits.get(n) ?? `?${n}`))
    const tsKeys = new Set(noticeLandingKeys())
    const missingInFrontend = [...goVals].filter((v) => !tsKeys.has(v))
    const extraInFrontend = [...tsKeys].filter((v) => !goVals.has(v))
    expect(missingInFrontend, `后端有这类 ref_type 而落点表没有 ⇒ 点了只能回落到工单：${missingInFrontend.join(',')}`).toEqual([])
    expect(extraInFrontend, `落点表有而后端名单没有 ⇒ 永远命中不到的死映射：${extraInFrontend.join(',')}`).toEqual([])
  })

  it('A2 落点表的键必须逐个被 noticeLandingFor 认得（拼错键＝静默回落）', () => {
    for (const k of noticeLandingKeys()) {
      const hit = noticeLandingFor(k, 4)
      expect(hit.kind === 'panel' ? hit.panel !== NOTICE_FALLBACK_PANEL || k === 'ticket' : true, `${k} 落到了兜底面板`).toBe(true)
    }
  })
})

// ---- B. 落点合法性（PanelKey 与菜单等级同源）----
const adminSrc = stripTsxComments(read('src/stores/admin.tsx'))
const dashSrc = stripTsxComments(read('src/components/admin/AdminDashboard.tsx'))

/** 剥 TS/TSX 注释（行注释＋块注释；JSX 里的 {/\* \*\/} 形态一并剥） */
function stripTsxComments(src: string): string {
  return src
    .replace(/\/\*[\s\S]*?\*\//g, '')
    .split('\n')
    .map((l) => l.replace(/\/\/(?!https?:).*$/, ''))
    .join('\n')
}

/** PanelKey 联合类型的字面量集合（`| 'a' | 'b'` 跨行） */
function panelKeys(): Set<string> {
  const i = adminSrc.indexOf('export type PanelKey')
  expect(i).toBeGreaterThanOrEqual(0)
  const body = adminSrc.slice(i, adminSrc.indexOf('\nexport', i + 20))
  return new Set([...body.matchAll(/'([A-Za-z]+)'/g)].map((m) => m[1]))
}

/** AdminDashboard 菜单项 key→minLevel（落点表写等级必须与这一份一致） */
function menuLevels(): Map<string, number> {
  const out = new Map<string, number>()
  for (const m of dashSrc.matchAll(/\{\s*key:\s*'([A-Za-z]+)'[^}]*?minLevel:\s*(\d+)/g)) out.set(m[1], Number(m[2]))
  return out
}

describe('B 落点合法：面板键与等级门槛都取自后台那两份真值', () => {
  it('B0 两份解析都有射程', () => {
    expect(panelKeys().size).toBeGreaterThan(20)
    expect(menuLevels().size).toBeGreaterThanOrEqual(8)
  })

  it('B1 表里每个 panel 都是合法 PanelKey', () => {
    const keys = panelKeys()
    for (const [ref, land] of Object.entries(NOTICE_LANDINGS)) {
      if (land.kind === 'panel') expect(keys.has(land.panel), `${ref} → ${land.panel} 不是 PanelKey`).toBe(true)
    }
  })

  it('B2 出现在菜单里的 panel，等级必须与菜单档位逐字一致', () => {
    const levels = menuLevels()
    for (const [ref, land] of Object.entries(NOTICE_LANDINGS)) {
      if (land.kind === 'panel' && levels.has(land.panel)) {
        expect(land.minLevel, `${ref} → ${land.panel} 的 minLevel 与 AdminDashboard 菜单不一致`).toBe(levels.get(land.panel))
      }
    }
  })

  it('B3 兜底面板 tickets 对 L2 可达（兜底把人送去死路＝这条锁的意义）', () => {
    expect(menuLevels().get('tickets')).toBeLessThanOrEqual(2)
    expect(NOTICE_FALLBACK_PANEL).toBe('tickets')
  })
})

// ---- C. 守卫级接线 ----
const bellSrc = stripTsxComments(read('src/components/Bell.tsx'))

describe('C 守卫级接线：铃铛必须走这张表，不许再留第二条硬编码分支', () => {
  it('C1 Bell.tsx  import 并调用 noticeLandingFor', () => {
    expect(bellSrc).toMatch(/from '@\/lib\/noticeRouting'/)
    expect(bellSrc).toMatch(/noticeLandingFor\(/)
  })

  it('C2 Bell.tsx 里不许再按 ref_type 字面量分支（硬编码＝映射表旁又长一把尺子）', () => {
    // 判据只问"比较"形态：ref_type === 'xxx' / !== 'xxx' / == 'xxx'
    const hardcoded = [...bellSrc.matchAll(/ref_type\s*[!=]==?\s*['"][a-z_]+['"]/g)].map((m) => m[0])
    expect(hardcoded, `Bell.tsx 残留按 ref_type 的硬编码分支：${hardcoded.join(' | ')}`).toEqual([])
    // 正对照（反证）：把旧形态喂给同一个判据必须报出来，否则这条负向是空转
    const oldShape = "if (isSuper && n.ref_type === 'feedback') openFeedback(n.ref_id as number)"
    expect(/ref_type\s*[!=]==?\s*['"][a-z_]+['"]/.test(oldShape), '判据自身失效（连旧形态都抓不到）').toBe(true)
  })

  it('C3 等级门槛真的参与决策：等级不够必须折回兜底面板', () => {
    // L2 部门管理员收到 quota（落点 org，minLevel 3）⇒ 不许被硬塞进 403 面板
    const l2 = noticeLandingFor('quota', 2)
    expect(l2.kind).toBe('panel')
    expect((l2 as { panel: string }).panel).toBe(NOTICE_FALLBACK_PANEL)
    // 正对照：同一条在 L3 上必须落到 org（否则上一条"折回"可能只是永远折回）
    const l3 = noticeLandingFor('quota', 3)
    expect((l3 as { panel: string }).panel).toBe('org')
    // 未知类型与缺省同样回落，不抛异常
    expect((noticeLandingFor('nope', 4) as { panel: string }).panel).toBe(NOTICE_FALLBACK_PANEL)
    expect((noticeLandingFor(undefined, 4) as { panel: string }).panel).toBe(NOTICE_FALLBACK_PANEL)
    // 超管面：L4 才开（采集/待核销/反馈三条各判一次，缺一条就是映射表被改窄了）
    for (const k of ['kb_scrape', 'pay_manual', 'feedback']) {
      expect((noticeLandingFor(k, 4) as { panel?: string; kind?: string }).kind).toBe(k === 'feedback' ? 'feedback' : 'panel')
      expect((noticeLandingFor(k, 3) as { panel: string }).panel).toBe(NOTICE_FALLBACK_PANEL)
    }
  })
})
