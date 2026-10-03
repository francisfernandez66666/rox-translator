// ============================================================================
// components/admin/stageRegistryGate.test.ts — ★ 阶段档名跨语言同源锁（R-1 修法 D，2026-10-04）
//
// 钉住的历史缺陷：后端 handleStageModels 的读面白名单与前端 ModelsP 的 stageCards
//   各自抄了一份档名清单，谁也不知道对方抄了什么。实际结果是 kb_match 这一档
//   「引擎在用、运营看不见」，而旧的覆盖式保存面每点一次保存就把库里那一档抹掉一次
//   （R-1 期间 Pro 翻译 401 的直接成因之一，见《发布前E2E_UAT_20261003/修改文档》§一·修法 D）。
//
// 判据是**派生式**的，不写死清单：
//   ① 从 backend-go/internal/config/config.go 解析 Stage* 常量与 AllStages() 返回的标识符
//      ⇒ 得到后端档名真值集合（含值，不只数量）；
//   ② 从 ModelsP.tsx 解析 stageCards 的 key 字面量 ⇒ 得到前端卡片集合；
//   ③ 两集合除去**明确豁免的 legacy 档**（evals：旧键，仍被读侧当历史回落，不给卡片）后必须相等，
//      两个方向都判红：后端加了档前端没出卡＝运营看不见（kb_match 当年的形态）；
//      前端出了卡后端没登记＝保存时被 400 拒收，面板"保存成功"是假的。
//   ④ 每张卡的标题/提示键必须在 12 份词典里都存在且非空（新增档位漏补译＝其它语种拿到空串或原始键名）。
//
// 反证（本轮实跑，见批次记录）：
//   · 从 AllStages() 删掉 kb_match ⇒ ③红（前端多出卡片）；
//   · 从 stageCards 删掉 kb_match ⇒ ③红（后端有档前端没卡）＋④红；
//   · 前端塞一张 key='ai_initila' 的拼错卡 ⇒ ③红。
//
// 运行：npx vitest run src/components/admin/stageRegistryGate.test.ts
// ============================================================================
// @vitest-environment node
import { describe, expect, it } from 'vitest'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

/** goRead 读后端 config 源（相对本文件：src/components/admin 在包根下三层，仓库根 = ../../../../） */
function goConfigSource(): string {
  const p = fileURLToPath(new URL('../../../../backend-go/internal/config/config.go', import.meta.url))
  return readFileSync(p, 'utf8')
}

/** tsxSource 读前端面板源 */
function modelsPanelSource(): string {
  const p = fileURLToPath(new URL('./ModelsP.tsx', import.meta.url))
  return readFileSync(p, 'utf8')
}

/**
 * backendStageKeys 解析后端档名真值：
 * 先取 `StageXxx = "yyy"` 常量表，再取 AllStages() 里 return 的标识符序列，映射回字面值。
 * 解析失败一律当红灯处理（空清单永远不该通过等值判定）。
 */
function backendStageKeys(): { keys: string[]; constCount: number } {
  const src = goConfigSource()
  const consts = new Map<string, string>()
  for (const m of src.matchAll(/^\t(Stage[A-Za-z0-9]+)\s*=\s*"([a-z0-9_]+)"/gm)) {
    consts.set(m[1], m[2])
  }
  const fn = src.match(/func AllStages\(\) \[\]string \{([\s\S]*?)\n\}/)
  expect(fn, 'config.go 里没找到 AllStages() 函数体 ⇒ 阶段名单单一事实源被改名或删除，本锁射程失效').toBeTruthy()
  const keys: string[] = []
  for (const id of (fn as RegExpMatchArray)[1].matchAll(/\b(Stage[A-Za-z0-9]+)\b/g)) {
    const v = consts.get(id[1])
    expect(v, `AllStages() 里引用了 ${id[1]}，但常量表解析不到它的字面值 ⇒ 解析器失配，请修解析器而不是放宽判据`).toBeTruthy()
    keys.push(v as string)
  }
  return { keys, constCount: consts.size }
}

/** frontendStageKeys 解析 stageCards 数组里的 key 字面量 */
function frontendStageKeys(): string[] {
  const src = modelsPanelSource()
  const block = src.match(/const stageCards = \[([\s\S]*?)\n\s*\]/)
  expect(block, 'ModelsP.tsx 里没找到 stageCards 数组 ⇒ 卡片清单被改造，本锁需要同步换判据').toBeTruthy()
  const keys: string[] = []
  for (const m of (block as RegExpMatchArray)[1].matchAll(/key:\s*'([a-z0-9_]+)'/g)) keys.push(m[1])
  return keys
}

/** i18nTitleKeys 取每张卡用到的词典键（title 与 hint） */
function cardLabelKeys(): { key: string; title: string; hint: string }[] {
  const src = modelsPanelSource()
  const block = src.match(/const stageCards = \[([\s\S]*?)\n\s*\]/)
  expect(block).toBeTruthy()
  const rows: { key: string; title: string; hint: string }[] = []
  for (const m of (block as RegExpMatchArray)[1].matchAll(
    /key:\s*'([a-z0-9_]+)',\s*title:\s*t\('([^']+)'\),\s*hint:\s*t\('([^']+)'\)/g,
  )) {
    rows.push({ key: m[1], title: m[2], hint: m[3] })
  }
  expect(rows.length, '每张卡都必须同时带 title 与 hint 两个 t() 键').toBe((block as RegExpMatchArray)[1].split('{ key:').length - 1)
  return rows
}

// LEGACY 档：后端名单里保留（读侧兼容、写侧不抹），但面板刻意不给卡片。
// 豁免必须写成显式集合，并在下面用例里正向证明它真的出现在后端名单中——
// 否则"某天把 kb_match 挪进这里"就等于把红灯改成绿灯。
const LEGACY_WITHOUT_CARD = ['evals']

describe('阶段档名：后端 AllStages() ⇔ 前端 stageCards 同源', () => {
  it('后端档名清单解析成功且包含引擎在用的每一档', () => {
    const { keys, constCount } = backendStageKeys()
    expect(constCount, '常量表解析为空 ⇒ 正则失配，不允许把"0 档"当成通过').toBeGreaterThan(5)
    expect(new Set(keys).size, `后端名单有重复档名：${keys.join(',')}`).toBe(keys.length)
    // 决定性正向（历史黑洞就是这两档）
    expect(keys).toContain('kb_match')
    expect(keys).toContain('kb_screen')
    expect(keys).toContain('ai_initial')
  })

  it('前端卡片集合 ＝ 后端名单 − legacy 豁免（双向等值）', () => {
    const backend = [...backendStageKeys().keys].sort()
    const front = [...frontendStageKeys()].sort()
    const expected = backend.filter((k) => !LEGACY_WITHOUT_CARD.includes(k)).sort()
    const missing = expected.filter((k) => !front.includes(k))
    const extra = front.filter((k) => !expected.includes(k))
    expect(missing, `后端有档、面板没卡 ⇒ 运营看不见它，而引擎拿它取模（kb_match 当年形态）：[${missing.join(',')}]`).toEqual([])
    expect(extra, `面板有卡、后端名单没登记 ⇒ 保存会被 400 拒收，"保存成功"是假的：[${extra.join(',')}]`).toEqual([])
    // 空清单正锁：前端一张卡都没有属于解析/实现双双失效，不许被上面的双向等值当成绿灯
    expect(front.length, 'stageCards 解析出 0 张卡 ⇒ 判据空转').toBeGreaterThan(0)
  })

  it('legacy 豁免只豁免"确实还在后端名单里"的旧键', () => {
    const backend = backendStageKeys().keys
    for (const k of LEGACY_WITHOUT_CARD) {
      expect(backend, `豁免名单里的 ${k} 已不在后端档名名单中 ⇒ 豁免本身腐烂，请删掉它`).toContain(k)
    }
    // 反向：不许把"该出卡"的档塞进豁免来绕过红灯（kb_match 是引擎真在读的兜底腿）
    expect(LEGACY_WITHOUT_CARD, 'kb_match 必须出卡，不许进豁免名单').not.toContain('kb_match')
  })

  it('每张卡的 title/hint 键在 12 份词典里都存在且非空', () => {
    const dictFiles = ['zh', 'en'].map((l) => `../../i18n/dicts.${l}.ts`)
    const locales = ['zh-hant', 'ja', 'ko', 'de', 'es', 'fr', 'pt', 'ru', 'ar', 'th'].map((l) => `../../i18n/locales/${l}.ts`)
    const sources: { name: string; text: string }[] = []
    for (const rel of [...dictFiles, ...locales]) {
      sources.push({ name: rel, text: readFileSync(fileURLToPath(new URL(rel, import.meta.url)), 'utf8') })
    }
    const rows = cardLabelKeys()
    expect(rows.length).toBe(frontendStageKeys().length)
    for (const { key, title, hint } of rows) {
      for (const dict of sources) {
        for (const k of [title, hint]) {
          const hit = dict.text.match(new RegExp(`["']${k.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}["']\\s*:\\s*(["'])((?:\\\\.|(?!\\1)[^\\\\])*?)\\1`, ''))
          expect(hit, `${dict.name} 缺键 ${k}（卡片 ${key}）⇒ 新增档位必须 12 语种同步补译`).toBeTruthy()
          expect((hit as RegExpMatchArray)[2].length, `${dict.name} 的 ${k} 值为空串`).toBeGreaterThan(0)
        }
      }
    }
  })
})
