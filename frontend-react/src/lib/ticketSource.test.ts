// ============================================================================
// src/lib/ticketSource.test.ts — 工单「来源档」取词静态锁（★ 2026-10-26 追加裁决九-2）
//
// 三件事：
//   L1 **名单等值**：TICKET_SOURCE_VALUES 与 Go 侧 backend-go/internal/orchestrator/workflow.go
//      的 Source* 常量值逐字相同（名单从 Go 现读派生，不是把五个值再抄第二份）；
//   L2 **每个值都有 12 语种文案**：五个值映射的词典键在 zh/en 底典与十份 locales 全量词典里
//      逐键非空（漏一份语种即红——locales.core.test.ts 管 ALL_KEYS 全集，这里管这五键点名）；
//   L3 **名单外值不吞**：第六档出现时原样回显档位名（不渲染空白、不判错）；
//   L4 **不许长出第二份表**：全仓非测试源码里，`model_with_source_fallback` 这个档位字面量
//      **只许出现在 lib/ticketSource.ts 一处**（映射表唯一性）；其余任何文件内联这份名单
//      ＝第二把尺子，当场判红。正/负两侧同判（射程自证）。
// ============================================================================
import { describe, it, expect } from 'vitest'
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import path from 'node:path'
import { TICKET_SOURCE_VALUES, ticketSourceLabel } from './ticketSource'
import { baseZh } from '@/i18n/dicts.zh'
import { baseEn } from '@/i18n/dicts.en'
import { setLang } from '@/i18n'
// 十份语种词典静态 import（测试件不受入口包体积纪律约束，口径同 locales.core.test.ts）
import { dict as dictAr } from '@/i18n/locales/ar'
import { dict as dictDe } from '@/i18n/locales/de'
import { dict as dictEs } from '@/i18n/locales/es'
import { dict as dictFr } from '@/i18n/locales/fr'
import { dict as dictJa } from '@/i18n/locales/ja'
import { dict as dictKo } from '@/i18n/locales/ko'
import { dict as dictPt } from '@/i18n/locales/pt'
import { dict as dictRu } from '@/i18n/locales/ru'
import { dict as dictTh } from '@/i18n/locales/th'
import { dict as dictZhHant } from '@/i18n/locales/zh-hant'

const REPO_LOCALIZED: Array<[string, Record<string, string>]> = [
  ['ar', dictAr], ['de', dictDe], ['es', dictEs], ['fr', dictFr], ['ja', dictJa],
  ['ko', dictKo], ['pt', dictPt], ['ru', dictRu], ['th', dictTh], ['zh-hant', dictZhHant],
]

const HERE = path.dirname(fileURLToPath(import.meta.url))
const REPO = path.resolve(HERE, '..', '..', '..') // 仓库根（src/lib 上两级＝frontend-react，再上一级＝仓库）
const GO_WORKFLOW = path.join(REPO, 'backend-go', 'internal', 'orchestrator', 'workflow.go')
const SRC_DIR = path.join(REPO, 'frontend-react', 'src')

function stripComments(src: string): string {
  return src.replace(/\/\*[\s\S]*?\*\//g, '').replace(/^\s*\/\/.*$/gm, '').replace(/(^|[^:])\/\/[^\n]*/g, '$1')
}

describe('工单来源档取词锁（九-2）', () => {
  it('L1 名单与 Go 侧 orchestrator Source* 常量逐字等值', () => {
    const go = readFileSync(GO_WORKFLOW, 'utf8')
    const found: string[] = []
    const re = /(?:^|\n)\s*Source[A-Za-z]+\s*=\s*"([a-z_]+)"/g
    let m: RegExpExecArray | null
    while ((m = re.exec(go)) !== null) found.push(m[1])
    expect(found.sort()).toEqual([...TICKET_SOURCE_VALUES].sort())
    expect(TICKET_SOURCE_VALUES.length).toBeGreaterThanOrEqual(5) // 下限：名单缩水＝锁失明
  })

  it('L2 五个值各映射到五个互不相同的 tk.src* 键（取词点唯一、一档一键）', () => {
    setLang('en')
    const labels = TICKET_SOURCE_VALUES.map((v) => ticketSourceLabel(v))
    for (const [i, v] of TICKET_SOURCE_VALUES.entries()) {
      // 取到文案＝不是回显档位名本身（回显形态由 L3 单独钉）
      expect(labels[i], `档位 ${v} 取不到英文文案`).not.toBe(v)
      // 该文案必须能在 en 底典的 tk.src* 五键里找到（映射表指向的就是这五键，不许指向别处）
      expect(Object.entries(baseEn).some(([k, val]) => k.startsWith('tk.src') && val === labels[i]),
        `档位 ${v} 的文案不在 tk.src* 键集里`).toBe(true)
    }
    expect(new Set(labels).size).toBe(TICKET_SOURCE_VALUES.length) // 五档五案，不许两档共用一句
    setLang('zh')
  })

  it('L2b 五个键在 12 份词典里全部存在且非空（键名字面量点名）', () => {
    const keys = ['tk.srcKb', 'tk.srcModel', 'tk.srcModelFallback', 'tk.srcUpstreamFailed', 'tk.srcApproved']
    for (const k of keys) {
      expect(baseZh[k], `zh 缺键 ${k}`).toBeTruthy()
      expect(baseEn[k], `en 缺键 ${k}`).toBeTruthy()
      for (const [code, dict] of REPO_LOCALIZED) {
        expect(dict[k], `${code} 缺键 ${k}`).toBeTruthy()
        // 占位符形态：这五键都不许有 {token}
        expect(dict[k], `${code} 的 ${k} 混入了占位符`).not.toMatch(/\{[A-Za-z0-9_]+\}/)
      }
    }
  })

  it('L3 名单外的值原样回显档位名（新档出现时不吞、不空白）', () => {
    setLang('zh')
    expect(ticketSourceLabel('some_future_tier')).toBe('some_future_tier')
  })

  it('L4 全仓非测试源码里档位字面量只有唯一映射表一处（禁第二份表）', () => {
    const files: string[] = []
    const walk = (dir: string) => {
      for (const name of readdirSync(dir)) {
        const p = path.join(dir, name)
        const st = statSync(p)
        if (st.isDirectory()) { walk(p); continue }
        if (!/\.(ts|tsx)$/.test(name)) continue
        if (/\.(test|spec)\.(ts|tsx)$/.test(name) || name.includes('.dom.test.')) continue
        files.push(p)
      }
    }
    walk(SRC_DIR)
    const hits = files.filter((p) => stripComments(readFileSync(p, 'utf8')).includes("'model_with_source_fallback'")
      || stripComments(readFileSync(p, 'utf8')).includes('"model_with_source_fallback"')
      || stripComments(readFileSync(p, 'utf8')).includes('`model_with_source_fallback`'))
    const rel = hits.map((p) => path.relative(REPO, p))
    // 唯一实现文件必须命中（射程正锁：表被摘空＝这条腿失明）
    expect(rel).toContain(path.join('frontend-react', 'src', 'lib', 'ticketSource.ts'))
    // 除它以外一个都不许有（负锁：谁再内联一份名单/映射＝第二把尺子）
    expect(rel.filter((r) => !r.endsWith(path.join('lib', 'ticketSource.ts'))), `第二份来源档表: ${JSON.stringify(rel)}`).toEqual([])
  })
})
