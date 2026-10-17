// 面板词典分档（门面档 vs 懒加载档）的机械守卫 —— ★ 2026-10-17 首屏轻量化批 A
//
// 背景（一句话）：34 份后台/前台面板词典此前全部静态 import 进 i18n/index.ts，
// 于是**只看官网首页的未登录访客**也要把整个后台的词条背进入口包（源 ≈197 KB）。
// 改造把 30 份非门面板挪进 `./appPanels`（由 App 外壳 `ensureAppPanels()` 按需 import），
// 门面四份（chat／landing／cost／auth）留原位。本文件守四件事：
//
//   P0 射程自检：面板目录与两份清单都读得到（任一处锚漂了＝本闸失明，比红灯更坏，见 AGENTS §三）。
//   P1 全集等值：**磁盘上的面板 ≡ 门面档 ∪ 懒加载档**，且 `ensureAppPanels()` 之后
//     所有面板键在运行期真取得到（不是只对了两张静态清单——那正是"判据级代替不了守卫级"）。
//   P2 **本批的核心口径**：公开面（官网/定价/比价/登录注册/挂件）用到的每一个键，
//     必须在 `ensureAppPanels()` **之前**就取得到 —— 否则未登录访客的首屏会露键名/回退文案。
//     文件集由**入口文件的 import 图现算**（新增公开页会被自动扫到，不靠人往名单里补）。
//   P3 源码级负向：index.ts 不许静态 import 懒加载面板；allKeys.ts 必须收全（漏一个＝
//     locales 全量闸门的基准缺键，十语种那一侧会开始报警而没人知道源头）。
//   另配三枚反证（P4）：把懒面板键当公开面键喂进去／把静态 import 写回 index.ts／
//     从 allKeys.ts 摘掉一个面板 —— 三者必须各自判红。
//
// ⚠️ **运行期的腿一律取"新实例"**（`vi.resetModules()` + `await import('./index')`）：
//    vitest.setup.ts 为了百来条组件用例统一下手了 `ensureAppPanels()`（等价于"词条已到位"），
//    本文件若用静态实例，P2 的射程会被那一次合并抹掉（判据恒绿且看不出为什么）。
//    静态 top-level import 拿到的是**原始实例**，resetModules 只影响其后的动态 import ⇒
//    本文件里"读源码"的腿不受影响。
import { describe, expect, it, vi } from 'vitest'
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { ALL_KEYS } from './allKeys'
import { zh as lazyZh } from './appPanels'

const I18N = fileURLToPath(new URL('.', import.meta.url))
const SRC = fileURLToPath(new URL('..', import.meta.url))
const MISS = '\u0000__missing__'

/** 剥掉注释（判据一律按**实现行**说话：注释里出现 `from './panels/x'` 属于记录，不算接线）。
 *  ⚠️ 必须真剥（逐字符状态机），不许用 `/*…*\/` 正则：本仓注释里写着 `panels/*.ts` 这类字样，
 *  正则版会把从那个 `/*` 起到下一个 `*\/` 之间的**全部代码**一起吃掉，判据当场变成 0 命中空转
 *  （本文件首跑真踩：剥完只剩 ensureLocale 之后的部分）。 */
function stripComments(s: string): string {
  let out = ''
  let state: 'code' | 'line' | 'block' | 'str' = 'code'
  let quote = ''
  for (let i = 0; i < s.length; i++) {
    const c = s[i]
    const n = s[i + 1]
    if (state === 'code') {
      if (c === '/' && n === '/') { state = 'line'; i++; continue }
      if (c === '/' && n === '*') { state = 'block'; i++; continue }
      if (c === '"' || c === "'" || c === '`') { state = 'str'; quote = c; out += c; continue }
      out += c; continue
    }
    if (state === 'line') { if (c === '\n') { state = 'code'; out += c } continue }
    if (state === 'block') { if (c === '*' && n === '/') { state = 'code'; i++ } continue }
    if (c === '\\') { out += c + (n ?? ''); i++; continue }
    if (c === quote) { state = 'code' }
    out += c
  }
  return out
}
const readSrc = (p: string) => stripComments(readFileSync(p, 'utf-8'))

/** 现算：磁盘上 panels/ 目录里的全部面板名（不含 .test. 与 index 之类） */
function panelsOnDisk(): string[] {
  return readdirSync(I18N + 'panels')
    .filter((n) => n.endsWith('.ts') && !n.includes('.test.'))
    .map((n) => n.replace(/\.ts$/, ''))
    .sort()
}

/** 现算：一段源码里静态 import 的面板名集合（必须显式带 `panels/` 段，避免把 `from 'react'` 也算进来） */
function panelsInSource(src: string): string[] {
  const disk = panelsOnDisk()
  const out = new Set<string>()
  for (const m of src.matchAll(/from\s+'(?:\.\/|\.\.\/|@\/)?panels\/([a-z0-9-]+)'/g)) {
    if (disk.includes(m[1])) out.add(m[1])
  }
  return [...out].sort()
}
const panelsImportedBy = (file: string) => panelsInSource(readSrc(file))

/**
 * P2 用的「公开面文件集」：从入口文件沿 import 图**现算**（本次改造的入口在批 B 里会收敛成
 * PublicRoot.tsx，届时只需把下面这行换成 ['PublicRoot.tsx']，走出新页面的键自动进射程）。
 */
function publicEntryFiles(): string[] {
  const candidates = ['PublicRoot.tsx', 'components/Landing.tsx', 'components/Login.tsx',
    'components/PricingPage.tsx', 'components/PriceComparePage.tsx', 'components/AiAssist.tsx',
    'components/publicshell/PublicShell.tsx']
  return candidates.map((f) => SRC + f).filter((p) => { try { return statSync(p).isFile() } catch { return false } })
}

/**
 * 从入口文件沿 import 图广度优先收文件（只跟 `./` 与 `@/` 的相对路径，node_modules 与样式不进）。
 * 返回 src 下真存在的 .ts/.tsx 文件集合 —— 它决定"公开面到底会执行到哪些取词点"。
 */
function publicGraphFiles(roots: string[]): string[] {
  const seen = new Set<string>()
  const queue = [...roots]
  while (queue.length) {
    const f = queue.shift()!
    if (seen.has(f)) continue
    let src: string
    try { if (!statSync(f).isFile()) continue; src = readSrc(f) } catch { continue }
    seen.add(f)
    for (const m of src.matchAll(/from\s+'([^']+)'/g)) {
      const spec = m[1]
      if (!spec.startsWith('.') && !spec.startsWith('@/')) continue
      const base = spec.startsWith('@/') ? SRC + spec.slice(2) : fileURLToPath(new URL(spec, 'file://' + f))
      for (const cand of [base + '.ts', base + '.tsx', base + '/index.ts', base + '/index.tsx', base]) {
        try { if (statSync(cand).isFile() && !seen.has(cand)) { queue.push(cand); break } } catch { /* 不是文件，试下一个后缀 */ }
      }
    }
  }
  return [...seen]
}

/** 现算：给定文件集里 `t('…')`／`tpl('…')`／`textOr('…')` 用到的键（含 gt/gtpl 别名写法） */
function keysUsedIn(files: string[]): string[] {
  const out = new Set<string>()
  const re = /\b(?:gt|t|tpl|gtpl|textOr)\('([A-Za-z0-9_.]+)'/g
  for (const f of files) {
    let src: string
    try { src = readSrc(f) } catch { continue }
    for (const m of src.matchAll(re)) out.add(m[1])
  }
  return [...out].sort()
}

/** P2 的判据函数（抽出来供反证复用）：喂进一批键与一份取词器，回「像公开面键但取不到词」的那些 */
function publicKeyFailures(keys: string[], textOr: (k: string, fb?: string) => string): string[] {
  return keys.filter((k) => textOr(k, MISS) === MISS).map((k) => `${k}（公开面用到但门面档取不到：该面板必须先加进 index.ts 的静态 import，否则未登录首屏露键名）`)
}

/** 取一份**全新**的 i18n 运行期实例（绕过 vitest.setup.ts 里那次 ensureAppPanels 合并） */
async function freshI18n(): Promise<typeof import('./index')> {
  vi.resetModules()
  return await import('./index')
}

describe('面板词典分档锁（轻量化批 A）', () => {
  it('P0 射程自检：面板目录、两份清单、公开面文件集都读得到', () => {
    const disk = panelsOnDisk()
    expect(disk.length, 'panels/ 目录读不到面板（路径锚漂了，本闸已失明）').toBeGreaterThanOrEqual(30)
    expect(panelsImportedBy(I18N + 'index.ts').length, '读不到 index.ts 的静态面板清单').toBeGreaterThan(0)
    expect(panelsImportedBy(I18N + 'appPanels.ts').length, '读不到 appPanels.ts 的懒加载清单').toBeGreaterThanOrEqual(20)
    expect(ALL_KEYS.length, 'ALL_KEYS 读不到（allKeys.ts 锚漂了）').toBeGreaterThan(1000)
    expect(publicEntryFiles().length, '一个公开面入口文件都没找到（P2 会变成空转锁）').toBeGreaterThan(0)
    expect(Object.keys(lazyZh).length, 'appPanels 的 zh 是空的').toBeGreaterThan(500)
  })

  it('P2 公开面用到的每个键，在懒加载之前就必须取得到（本批的核心口径）', async () => {
    const idx = await freshI18n()
    // 前置自证：新实例里懒加载面板**确实还没到手**（否则这条腿测的是"已加载"态，恒绿）
    const lazyOnly = Object.keys(lazyZh)[0]
    expect(idx.textOr(lazyOnly, MISS), '新实例里懒面板键已经取得到（resetModules 没生效，P2 会变成空转）').toBe(MISS)

    const files = publicGraphFiles(publicEntryFiles())
    const keys = keysUsedIn(files)
    // 射程自证：扫不到键数是"闸门空转"，不是"没问题"
    expect(keys.length, '公开面 import 图里一个取词点都没扫到（判据空转）').toBeGreaterThanOrEqual(80)
    // 先对齐宇宙：键必须都在 ALL_KEYS 里（否则扫到的是野键，判据本身就该先修）
    const unknown = keys.filter((k) => !ALL_KEYS.includes(k))
    expect(unknown, '公开面用了不在 ALL_KEYS 里的键（扫描或词典有漂移）：').toEqual([])
    // 核心判据：这些键此刻（ensureAppPanels 之前）必须都取得到
    const bad = publicKeyFailures(keys, (k, fb) => idx.textOr(k, fb))
    expect(bad, '公开面词条落在懒加载面板里了：\n').toEqual([])
    // 反证腿：只存在于懒面板的键，必须被同一个判据函数判红
    expect(publicKeyFailures([lazyOnly], (k, fb) => idx.textOr(k, fb)),
      '反证失效：懒面板独占键没被判红（这条锁是空转的）').toHaveLength(1)
  })

  it('P1 全集等值：磁盘面板 ≡ 门面档 ∪ 懒加载档；加载后全量键都取得到', async () => {
    const disk = panelsOnDisk()
    const inIndex = panelsImportedBy(I18N + 'index.ts')
    const inLazy = panelsImportedBy(I18N + 'appPanels.ts')
    const inAllKeys = panelsImportedBy(I18N + 'allKeys.ts')
    const union = [...new Set([...inIndex, ...inLazy])].sort()
    expect(disk.filter((n) => !union.includes(n)), '磁盘上有面板没被任何一档收（会静默丢词）：').toEqual([])
    expect(union.filter((n) => !disk.includes(n)), '清单里的面板在磁盘上不存在：').toEqual([])
    // 门面档与懒加载档不许重叠：重叠＝同一份词条进两次，入口包白背一份
    expect(inIndex.filter((n) => inLazy.includes(n)), '同一个面板同时进了门面档与懒加载档：').toEqual([])
    // allKeys.ts 必须收全（它是 locales 全量闸门的基准，漏一个＝十语种那侧开始报警而其源头无人知）
    expect(inAllKeys, 'allKeys.ts 与磁盘面板全集不等：').toEqual(disk)
    // 运行期腿：在**新实例**上加载一次，之后 ALL_KEYS 逐键必须真取得到
    //（对两张静态清单不算数，见 AGENTS §三「判据级代替不了守卫级」；也不拿 setup 那次合并当证据）
    const idx = await freshI18n()
    expect(idx.appPanelsLoaded(), '新实例一开始就是"已加载"（resetModules 没生效）').toBe(false)
    await idx.ensureAppPanels()
    expect(idx.appPanelsLoaded(), 'ensureAppPanels() 没有把状态翻成已加载').toBe(true)
    const missing = ALL_KEYS.filter((k) => idx.textOr(k, MISS) === MISS)
    expect(missing, 'ensureAppPanels() 之后仍有取不到的键：').toEqual([])
    // 幂等：再调一次不许重复合并／不许抛（并发合流的口径见 ensureAppPanels 注释）
    await Promise.all([idx.ensureAppPanels(), idx.ensureAppPanels()])
    expect(ALL_KEYS.filter((k) => idx.textOr(k, MISS) === MISS), '重复 ensureAppPanels 后键丢了：').toEqual([])
  })

  it('P3 源码级负向：index.ts 不许静态 import 懒加载面板；allKeys.ts 不许 re-export 回运行期', () => {
    const inLazy = panelsImportedBy(I18N + 'appPanels.ts')
    const idxSrc = readSrc(I18N + 'index.ts')
    const leaked = inLazy.filter((n) => new RegExp(`from\\s+'\\./panels/${n}'`).test(idxSrc))
    expect(leaked, 'index.ts 又静态 import 了懒加载面板（入口包体积会重回 617 KB）：').toEqual([])
    // index.ts 不许 re-export allKeys：那会让每个 import '@/i18n' 的地方顺带拉进 30 份面板
    expect(/export\s+.*from\s+'\.\/allKeys'/.test(idxSrc), 'index.ts 又 re-export allKeys（面板会顺着它回到入口包）').toBe(false)
  })

  it('P4 反证：把静态 import 写回 index.ts／从 allKeys 摘一个面板 —— 各自必须判红', () => {
    const inLazy = panelsImportedBy(I18N + 'appPanels.ts')
    const idxSrc = readSrc(I18N + 'index.ts')
    const lazyOnly = inLazy[0]
    // 反证 A：把某个懒加载面板的静态 import 写回 index.ts ⇒ P3 的判据必须抓得到
    const mutated = `import * as pX from './panels/${lazyOnly}'\n` + idxSrc
    expect(new RegExp(`from\\s+'\\./panels/${lazyOnly}'`).test(mutated), '反证失效：变异没落地').toBe(true)
    const caught = inLazy.filter((n) => new RegExp(`from\\s+'\\./panels/${n}'`).test(mutated))
    expect(caught, '写回静态 import 没被判红').toContain(lazyOnly)
    // 反证 B：从 allKeys.ts 摘掉一个面板的 import ⇒ 「allKeys ≡ 磁盘全集」必须抓得到
    const allSrc = readSrc(I18N + 'allKeys.ts')
    const victim = panelsInSource(allSrc)[0]
    const stripped = allSrc.replace(new RegExp(`import \\* as p[A-Za-z0-9]+ from '\\./panels/${victim}'\\n`), '')
    expect(stripped, '反证失效：变异没落地').not.toBe(allSrc)
    expect(panelsInSource(stripped), '摘掉一个面板后清单没变（反证本身无效）').not.toContain(victim)
    expect(panelsOnDisk().filter((n) => !panelsInSource(stripped).includes(n)),
      '摘掉面板后与磁盘全集仍判相等（全集等值这条是空转的）').not.toEqual([])
  })
})
