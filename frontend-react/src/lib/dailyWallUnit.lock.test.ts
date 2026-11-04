// ============================================================================
// src/lib/dailyWallUnit.lock.test.ts — 日限额「生效口径」三档的**跨侧等值锁**（★ 2026-11-03 复核批 D-1）
//
// 这一批修的是一条**静默失效**：日计数器累加的是成本，而运营配的上限可能是字符量纲，
// 两者之间隔着计价系数与 rate_card 单价×倍率 ⇒ 只配 `max_daily_chars` 的租户那一档
// **根本不拦**（取向见 backend-go/internal/api/daily_budget.go 文件头）。修法里有一半是
// 「把真相说出来」：后端出 `daily_wall_unit` 三档词，管理台配额面照实显示。
//
// 于是这里要判的是**两份事实不许漂**：
//   L1 三档字面量：TS 的 DAILY_WALL_UNITS ↔ Go 的三个常量（从源码现读，不抄）；
//   L2 码→词典键：三档各一个键、彼此不同、且 12 语种词典里**都有非空值**；
//   L3 未知/缺失 → 空串（＝整行不渲染）：不猜档位（猜成 unlimited 会让老后端"看起来已确认不限"）；
//   L4 接线：PlansP 必须**消费** dailyWallUnitLabelKey 且不许在组件里自己比字符串（第二把尺子）。
//
// 反证 R1–R4：分别改坏四处后 judge 必须点名；每条先断言变异体与原文不同
// （空替换会把 no-op 伪装成"锁抓住了破坏"，本仓 2026-10-14 真踩）。
// ============================================================================
// @vitest-environment node
import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import path from 'node:path'
import {
  DAILY_WALL_UNITS,
  DAILY_WALL_UNIT_LABEL_KEYS,
  dailyWallUnitLabelKey,
  isDailyWallUnit,
} from './dailyWallUnit'
import { dict as ru } from '../i18n/locales/ru'
import { dict as fr } from '../i18n/locales/fr'
import { dict as ar } from '../i18n/locales/ar'
import { dict as es } from '../i18n/locales/es'
import { dict as pt } from '../i18n/locales/pt'
import { dict as de } from '../i18n/locales/de'
import { dict as ja } from '../i18n/locales/ja'
import { dict as ko } from '../i18n/locales/ko'
import { dict as th } from '../i18n/locales/th'
import { dict as zhHant } from '../i18n/locales/zh-hant'
import { zh as pBillingZh, en as pBillingEn } from '../i18n/panels/billing'

const HERE = path.dirname(fileURLToPath(import.meta.url))
const REPO = path.resolve(HERE, '..', '..', '..')
const GO_BUDGET = path.join(REPO, 'backend-go', 'internal', 'api', 'daily_budget.go')
const PAGE = path.join(HERE, '..', 'components', 'admin', 'PlansP.tsx')

/** 全部 12 语种的词典（zh/en 取面板那两份，其余十份是 ALL_KEYS 全量） */
const ALL_DICTS: Array<[string, Record<string, string>]> = [
  ['zh', pBillingZh], ['en', pBillingEn],
  ['ru', ru], ['fr', fr], ['ar', ar], ['es', es], ['pt', pt],
  ['de', de], ['ja', ja], ['ko', ko], ['th', th], ['zh_hant', zhHant],
]

function stripComments(src: string): string {
  return src.replace(/\/\*[\s\S]*?\*\//g, '').replace(/^\s*\/\/.*$/gm, '')
}

/** 从 Go 源码取 `name = "字面量"` 的值（取不到回 ""） */
export function goConst(src: string, name: string): string {
  const re = new RegExp(`\\b${name}\\s*=\\s*"([^"]*)"`)
  const m = re.exec(src)
  return m ? m[1] : ''
}

/**
 * judge：把四类判据摊成"缺了哪些腿"的清单（空数组＝全绿）。
 * 参数都是**已剥注释**的源码正文＋待验的映射（变异体直接喂替身），
 * 这样反证与真判据走的是同一个函数（判据级断言不等于守卫级，本仓 §三 那条）。
 */
export function judge(
  goSrc: string,
  pageSrc: string,
  units: readonly string[],
  labelKeys: Record<string, string>,
  dicts: Array<[string, Record<string, string>]>,
): string[] {
  const miss: string[] = []
  // L1 三档字面量与 Go 常量逐条等值
  const goCost = goConst(goSrc, 'dailyWallUnitCost')
  const goLegacy = goConst(goSrc, 'dailyWallUnitLegacyCharsIgnored')
  const goUnlimited = goConst(goSrc, 'dailyWallUnitUnlimited')
  if (!goCost || !goLegacy || !goUnlimited) {
    miss.push('go-consts-unreadable（Go 三个常量一个都读不到 ⇒ 下面两条字面量判据全部空转）')
  } else {
    const want = [goCost, goLegacy, goUnlimited].sort()
    const got = [...units].sort()
    if (want.join('|') !== got.join('|')) miss.push(`units-mismatch（go=${want.join(',')} ts=${got.join(',')}）`)
  }
  // L2 码→词典键：三档各一个键、彼此不同、12 语种都有非空值
  const seen = new Set<string>()
  for (const u of units) {
    const k = labelKeys[u]
    if (!k) { miss.push(`label-key-missing:${u}`); continue }
    if (seen.has(k)) miss.push(`label-key-duplicated:${u}->${k}`)
    seen.add(k)
    for (const [lang, d] of dicts) {
      const v = d[k]
      if (typeof v !== 'string' || v.trim() === '') miss.push(`label-text-missing:${lang}:${k}`)
    }
  }
  // L4 接线：消费唯一映射 + 出栈形态标记；且组件里不许再自己比这三档字符串
  const page = pageSrc
  if (!page.includes('dailyWallUnitLabelKey(r.daily_wall_unit)')) {
    miss.push('page-not-consuming-mapping（PlansP 没走唯一映射 ⇒ 档位名可能被各写一份）')
  }
  if (!page.includes('data-testid="quota-wall-unit"')) miss.push('page-marker-missing（口径行没有形态标记，e2e 取不到）')
  for (const u of units) {
    // 只在**组件正文**里判：lib/dailyWallUnit.ts 里当然有这些字面量，那不是射程
    if (new RegExp(`['"]${u}['"]`).test(page)) miss.push(`page-hardcodes-unit-literal:${u}`)
  }
  return miss
}

const GO = stripComments(readFileSync(GO_BUDGET, 'utf8'))
const PAGE_SRC = stripComments(readFileSync(PAGE, 'utf8'))

describe('dailyWallUnit：日限额生效口径三档（跨侧等值）', () => {
  it('L0 判据射程自证：Go 源码与 PlansP 都真读到了（读不到时下面每条都是空转绿灯）', () => {
    expect(GO).toContain('dailyWallUnitCost')
    expect(PAGE_SRC).toContain('dailyWallUnitLabelKey')
  })

  it('L1+L2+L4 三档字面量 ↔ Go 常量、码→键唯一、12 语种齐全、组件走唯一映射', () => {
    const miss = judge(GO, PAGE_SRC, DAILY_WALL_UNITS, DAILY_WALL_UNIT_LABEL_KEYS, ALL_DICTS)
    expect(miss).toEqual([])
  })

  it('L3 未知/缺失/大小写漂移一律回空串（不猜档位 ⇒ 整行不渲染）', () => {
    for (const v of [undefined, null, '', '  cost', 'COST', 'Cost', 'legacy_chars', 'unknown', 1, {}]) {
      expect(dailyWallUnitLabelKey(v), String(v)).toBe('')
      expect(isDailyWallUnit(v)).toBe(false)
    }
    // 正向对照：三档各自真取到键（否则上面那串"全空"可能只是因为函数恒空）
    for (const u of DAILY_WALL_UNITS) {
      expect(dailyWallUnitLabelKey(u)).toBe(DAILY_WALL_UNIT_LABEL_KEYS[u])
      expect(isDailyWallUnit(u)).toBe(true)
    }
  })
})

describe('dailyWallUnit 反证（改坏任一处 ⇒ judge 必须点名）', () => {
  const base = () => [GO, PAGE_SRC, DAILY_WALL_UNITS, DAILY_WALL_UNIT_LABEL_KEYS, ALL_DICTS] as const

  it('R1 后端改档名（Go 侧字面量漂移）⇒ units-mismatch', () => {
    const mutGo = GO.replace('dailyWallUnitCost               = "cost"', 'dailyWallUnitCost               = "cost_v2"')
    expect(mutGo).not.toBe(GO)
    const [g, p, u, k, d] = base()
    expect(judge(mutGo, p, u, k, d).some((m) => m.startsWith('units-mismatch'))).toBe(true)
    expect(judge(g, p, u, k, d)).toEqual([])
  })

  it('R2 两档共用同一个词典键 ⇒ label-key-duplicated', () => {
    const mutKeys = { ...DAILY_WALL_UNIT_LABEL_KEYS, unlimited: DAILY_WALL_UNIT_LABEL_KEYS.cost }
    expect(mutKeys).not.toEqual(DAILY_WALL_UNIT_LABEL_KEYS)
    const [g, p, u, , d] = base()
    expect(judge(g, p, u, mutKeys, d).some((m) => m.startsWith('label-key-duplicated'))).toBe(true)
  })

  it('R3 某语种漏译（词典里没有这个键）⇒ label-text-missing', () => {
    const mutDicts = ALL_DICTS.map(([lang, d]) => [lang, lang === 'th' ? { ...d, [DAILY_WALL_UNIT_LABEL_KEYS.cost]: '' } : d] as [string, Record<string, string>])
    expect(mutDicts).not.toEqual(ALL_DICTS)
    const [g, p, u, k] = base()
    expect(judge(g, p, u, k, mutDicts).some((m) => m === `label-text-missing:th:${DAILY_WALL_UNIT_LABEL_KEYS.cost}`)).toBe(true)
  })

  it('R4 组件自己比档位字符串（摘掉唯一映射）⇒ page-not-consuming-mapping 与 page-hardcodes-unit-literal', () => {
    const mutPage = PAGE_SRC
      .replace('dailyWallUnitLabelKey(r.daily_wall_unit)', "(r.daily_wall_unit === 'cost' ? 'billing.quotaUnitCost' : '')")
    expect(mutPage).not.toBe(PAGE_SRC)
    const [g, , u, k, d] = base()
    const miss = judge(g, mutPage, u, k, d)
    expect(miss.some((m) => m.startsWith('page-not-consuming-mapping'))).toBe(true)
    expect(miss.some((m) => m.startsWith('page-hardcodes-unit-literal'))).toBe(true)
  })
})
