// ============================================================================
// PlansP 预估系数「渲染字段集 == EST_TOKEN_FIELDS」源码等值锁（★ 2026-11-01 DEF-P3-08）
//
// 缺陷本体（本轮字节级复现过）：组件回显腿只按 `EST_TOKEN_FIELDS` 填 `estForm`，
// 而 JSX 曾**另抄一份**六字段硬编码清单去渲染输入框。两把尺子一致时看不出问题，
// 一旦某测试的 mock 只给 4 个键（仓内 8 个 PlansP.* 测试里有 4 键版），
// `estForm['fixed_lang_pro']` 从未被写 ⇒ 那一格 `value` 从 `''`（受控）变 `undefined`（非受控）
// ⇒ React 报「changing a controlled input to be uncontrolled」。
// 实测对照：**4 键 mock 跑一次出 1 条该告警；6 键 mock 出 0 条**——一行就证伪了
// 原报告"`Panel` 的 input 有问题"那个归因。
//
// 修法是把渲染改成**由同一个常量派生**（`EST_TOKEN_FIELDS.map` + exhaustive 的
// `Record<EstTokenField, string>` 标签映射），所以本锁判的是"两把尺子有没有又分家"：
//   S1 正向：JSX 必须 map(EST_TOKEN_FIELDS)，标签必须走 Record（TS 会在少配标签时编译不过）；
//   S2 负向：源码里不许再出现形如 `['k_pro', t('billing.estKPro')]` 的硬编码字段元组清单；
//   S3 射程自证：把 S1 的靶子改坏（还原成硬编码清单）时，本锁必须判红——
//      负向 grep 锁若不配反证，将来实现换个写法（比如换成 .map 但顺序来自另一份数组）就静默失明。
// ============================================================================
// @vitest-environment node
import { describe, expect, it } from 'vitest'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import path from 'node:path'

import { EST_TOKEN_FIELDS } from '@/api/billing'

const HERE = path.dirname(fileURLToPath(import.meta.url))
const SRC = readFileSync(path.join(HERE, 'PlansP.tsx'), 'utf-8')

// 只判**代码**：注释里出现字段名是允许的（本文件头的引用就是一例），故先粗剥行注释。
const CODE = SRC.split('\n').filter((l) => !/^\s*(\/\/|\*|\/\*)/.test(l)).join('\n')

function hardCodedTupleList(src: string): boolean {
  // 形如 ['k_pro', t('billing.estKPro')] 的"字段＋标签"硬编码对（旧形态的唯一形状）
  return /\[\s*'(?:k_pro|k_fast|fixed_pro|fixed_fast|fixed_lang_pro|fixed_lang_fast)'\s*,\s*t\(/.test(src)
}

describe('PlansP 预估系数面必须派生自 EST_TOKEN_FIELDS（DEF-P3-08）', () => {
  it('S0 射程自证：真值名单是六键，且本文件读到了 PlansP.tsx', () => {
    expect(EST_TOKEN_FIELDS.length).toBeGreaterThanOrEqual(6)
    expect(CODE.length, '读不到 PlansP.tsx ⇒ 下面三条判据全部空转').toBeGreaterThan(1000)
  })

  it('S1 正向：渲染按 EST_TOKEN_FIELDS 派生、标签走 exhaustive Record 映射', () => {
    expect(/EST_TOKEN_FIELDS\.map\(\s*\(?f\)?\s*=>/.test(CODE),
      '渲染不再是"由名单派生"⇒ 名单与输入框又要分家（DEF-P3-08 复发）').toBe(true)
    expect(/const EST_FIELD_LABEL_KEYS\s*:\s*Record<EstTokenField,\s*string>/.test(CODE),
      '标签映射必须写成 Record<EstTokenField, string>（新增键时 TS 才会逼着补标签）').toBe(true)
    expect(/t\(EST_FIELD_LABEL_KEYS\[f\]\)/.test(CODE), '取标签必须按字段名查映射，不许再内联一份').toBe(true)
  })

  it('S2 负向：源码里不许有"字段＋标签"硬编码清单', () => {
    expect(hardCodedTupleList(CODE), 'JSX 里又内联了一份字段清单 ⇒ 两把尺子分家').toBe(false)
  })

  // 反证：还原成旧形态，S1/S2 必须各自判红（否则本锁只是"看起来在守"）。
  it('S3 反证：把渲染改回硬编码六元组，两条判据都要红', () => {
    const mutated = CODE.replace(
      /EST_TOKEN_FIELDS\.map\(\s*\(?\s*f\s*\)?\s*=>/,
      "([\n              ['k_pro', t('billing.estKPro')],\n              ['k_fast', t('billing.estKFast')],\n              ['fixed_pro', t('billing.estFixedPro')],\n              ['fixed_fast', t('billing.estFixedFast')],\n              ['fixed_lang_pro', t('billing.estFixedLangPro')],\n              ['fixed_lang_fast', t('billing.estFixedLangFast')],\n            ] as Array<[EstTokenField, string]>).map(([f, label]) =>",
    )
    expect(mutated, '反证失效：变异没落地').not.toBe(CODE)
    expect(/EST_TOKEN_FIELDS\.map\(\s*\(?\s*f\s*\)?\s*=>/.test(mutated), '变异后 S1 判据仍"通过"⇒ 判据形状写松了').toBe(false)
    expect(hardCodedTupleList(mutated), '变异后 S2 没抓住硬编码清单 ⇒ 负向锁失明').toBe(true)
    // 对照组：真代码这两条都干净
    expect(hardCodedTupleList(CODE)).toBe(false)
  })
})
