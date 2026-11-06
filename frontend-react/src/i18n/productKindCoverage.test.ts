// ============================================================================
// i18n/productKindCoverage.test.ts — facts.kind 九值名单覆盖棘轮（施工_一期 §8.3／§9.2）
//
// 补的缝：auditActionCoverage.test.ts 只跨包扫 `LogAudit` 的审计动作名，
// `facts.kind` 九值名单（§2.2 对外契约，12 语种要翻译）**不在它射程内**。
// 判据：九值 × 「panels/product.ts 的 zh/en 两腿 + 10 份 locales」差集只减不增
//（基线现值 0，改小要同步改本锁）；并把「名单本身」与 product 面板里的
// `facts.kind.*` 键集合做等值，防有人只往一边加第十个值。
// 运行：npx vitest run src/i18n/productKindCoverage.test.ts
// ============================================================================
import { describe, expect, it } from 'vitest'
import { zh as productZh, en as productEn } from './panels/product'
import { dict as de } from './locales/de'
import { dict as fr } from './locales/fr'
import { dict as ar } from './locales/ar'
import { dict as es } from './locales/es'
import { dict as pt } from './locales/pt'
import { dict as ru } from './locales/ru'
import { dict as ja } from './locales/ja'
import { dict as ko } from './locales/ko'
import { dict as th } from './locales/th'
import { dict as zhHant } from './locales/zh-hant'

/** §2.2 的 facts.kind 九值名单（对外契约；新增值必须走同族棘轮同步改这里 + 12 份词典） */
const KINDS = ['spec', 'material', 'price', 'moq', 'cert', 'capacity', 'warranty', 'usecase', 'other']

/** 差集基线：现值 0（只减不增；改小要同步改本锁） */
const DIFF_BASELINE = 0

/** 十二份事实源：product 面板的 zh/en 两腿 + 十份全量 locale */
const SOURCES: Array<[string, Record<string, string>]> = [
  ['panels/product.ts zh', productZh],
  ['panels/product.ts en', productEn],
  ['de', de], ['fr', fr], ['ar', ar], ['es', es], ['pt', pt], ['ru', ru],
  ['ja', ja], ['ko', ko], ['th', th], ['zh_hant', zhHant],
]

/** 判据函数：一份词典相对九值名单缺了哪些键（可喂变异后的词典对象做反证） */
function missingKinds(dict: Record<string, string>): string[] {
  return KINDS.filter((k) => !String(dict[`facts.kind.${k}`] ?? '').trim())
}

/** 判据函数：整批事实源的差集条目（每条点名「哪份缺哪个」） */
function coverageFailures(sources: Array<[string, Record<string, string>]>): string[] {
  const bad: string[] = []
  for (const [name, dict] of sources) {
    for (const k of missingKinds(dict)) bad.push(`${name} 缺 facts.kind.${k}`)
  }
  return bad
}

/** 名单等值判据：product 面板里出现的 facts.kind.* 键必须恰好是那九值（不多不少） */
function listFailures(dict: Record<string, string>): string[] {
  const onDisk = Object.keys(dict).filter((k) => k.startsWith('facts.kind.')).sort()
  const want = KINDS.map((k) => `facts.kind.${k}`).sort()
  if (onDisk.length !== want.length || onDisk.some((k, i) => k !== want[i])) {
    return [`panels/product.ts 的 facts.kind.* 集合与九值名单不等值：实测=[${onDisk}]`]
  }
  return []
}

describe('facts.kind 九值覆盖棘轮（§8.3／§9.2）', () => {
  it('十二份事实源逐份覆盖九值，差集只减不增（基线 0）', () => {
    const bad = coverageFailures(SOURCES)
    expect(bad, 'facts.kind 名单漏译：\n' + bad.join('\n')).toEqual([])
    expect(bad.length, '差集不得超过基线（只减不增；改小要同步改 DIFF_BASELINE）').toBeLessThanOrEqual(DIFF_BASELINE)
    expect(SOURCES.length, '十二份事实源没读全（判据空转）').toBe(12)
  })

  it('名单等值：product 面板里不许冒出第十个 kind，也不许少一个', () => {
    expect(listFailures(productZh)).toEqual([])
    expect(listFailures(productEn)).toEqual([])
  })

  it('反证：从一份 locale 摘掉 facts.kind.cert 必须判红（锁有射程，不是空转）', () => {
    const mutated: Record<string, string> = { ...de }
    delete mutated['facts.kind.cert']
    expect(missingKinds(mutated), '反证失效：删了键却没被判红').toContain('cert')
    expect(coverageFailures([['de', mutated]]).join('\n')).toContain('facts.kind.cert')
    expect(coverageFailures(SOURCES), '对照组：真实事实源必须干净').toEqual([])
  })

  it('反证：往 product 面板塞第十个 kind 必须被名单等值判据判红', () => {
    const mutated: Record<string, string> = { ...productZh, 'facts.kind.tenth': '第十项' }
    expect(listFailures(mutated).length, '第十个值没被判红').toBeGreaterThan(0)
    expect(listFailures(productZh), '对照组：真实名单必须干净').toEqual([])
  })
})
