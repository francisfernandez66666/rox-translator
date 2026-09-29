// ============ i18n/brandName.test.ts · 职责说明 ============
// 品牌名写法静态锁（★ 082x，2026-09-29，用户指令「品牌英文名叫 LangCross，不叫 nengyan」）。
//
// 为什么需要一条独立锁，而不是改完就完：
//   ① 这次的坑是**局部正确、整体打架**——12 份 locale 里 9 份早就是 LangCross，
//      只有 de/es/ar 和英文面板那 4 处把中文品牌名「能言」音译成了 Nengyan，
//      同一个文件里 "app.title": "LangCross" 与 "chat.assistTitle": "Nengyan AI-Assistent" 并存。
//      没有等值锁的话，下一次补译/换词很容易再冒出一个音译名，而且没人会红。
//   ② 品牌名有两条独立链路：界面文案（本目录）与模型自称（后端 assist 的【回复语言】段）。
//      后者由 backend-go/internal/assist/engine/reply_lang.go 的 brandKanjiLocales 决定，
//      本锁**直接读那份 Go 源码**把两边的语种分档钉成同一张表——
//      只改一边就会出现「挂件标题写能言、AI 正文自称 LangCross」（日文档正是最容易踩的一档）。
//
// 语种分档口径（与后端同一张表）：简繁中文＋日文用汉字名「能言」（ja.ts 的 app.title 本就是能言，
// 这是全站既有约定，不是本批新设）；其余语种一律 LangCross；任何语种都不许出现拼音写法。
//
// ★ 判据住在纯函数 brandViolation() 里，磁盘扫描与「反证」共用它：
//   反证（TestBrandGateSelfProof）直接喂三份坏字典进去，证明这条锁**抓得住**——
//   只做磁盘扫描的锁一旦判据写错（比如把「取不到值」当成合规），会长期恒绿而没人知道。
// =============================================
import { describe, expect, it } from 'vitest'
import { readFileSync, readdirSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

const LOCALES_DIR = fileURLToPath(new URL('./locales', import.meta.url))
const SRC_ROOT = fileURLToPath(new URL('../', import.meta.url)).replace(/\/$/, '')
const KANJI_FROM_GO = fileURLToPath(
  new URL('../../../backend-go/internal/assist/engine/reply_lang.go', import.meta.url),
)

// 品牌名会出现的两把键：全站标题 + 挂件标题（后者就是这次翻车的那一处）
const BRAND_KEYS = ['app.title', 'chat.assistTitle'] as const

// 汉字语种（后端 brandKanjiLocales 的镜像；下面那条交叉锁会真去读后端源码核对，
// 这里写死一份是为了让"本目录自洽"这条判据不依赖后端也能跑）
const KANJI_LOCALES = new Set(['zh', 'zh_hant', 'ja'])

// 拼音写法（Nengyan / NengYan / neng yan）在任何语种都算违规
const PINYIN_RE = /neng\s?yan/i

/**
 * 一条品牌名取值合规吗？返回违规说明（合规则返回 null）。
 * 三档判据都是**正向点名**，不用"没扫到就算过"：
 *   - 空值算违规（扫不到值＝锁没覆盖到这一处，不是这里干净了）；
 *   - 拼音写法无条件违规；
 *   - 语种分档：汉字语种必须含「能言」，其余必须含 LangCross 且不得混用汉字名。
 */
function brandViolation(code: string, key: string, value: string): string | null {
  if (value.trim() === '') return `${code}/${key}: 取不到值（词典缺键或写法变了导致扫描落空，属锁失明而非合规）`
  if (PINYIN_RE.test(value)) return `${code}/${key}: 出现拼音品牌名 → ${value}`
  if (KANJI_LOCALES.has(code)) {
    if (!value.includes('能言')) return `${code}/${key}: 该语种界面用汉字品牌名「能言」，实际 → ${value}`
    return null
  }
  if (!value.includes('LangCross')) return `${code}/${key}: 必须写 LangCross → ${value}`
  if (value.includes('能言')) return `${code}/${key}: 非汉字语种不许混用汉字品牌名 → ${value}`
  return null
}

/** 从词典源文件里取某个键的字面值（只取第一条命中；locale 是扁平 "key": "value" 结构） */
function pickValue(src: string, key: string): string {
  const m = src.match(new RegExp(`["']${key.replace('.', '\\.')}["']\\s*:\\s*(["'])([\\s\\S]*?)\\1`))
  return m ? m[2] : ''
}

/** locale 文件名 → 语种码（zh-hant.ts → zh_hant，与 Lang 联合类型同口径） */
function fileToCode(file: string): string {
  return file.replace(/\.ts$/, '').replace(/-/g, '_')
}

/** 读后端 brandKanjiLocales 那一段，解析出语种码集合 */
function kanjiLocalesFromGo(): Set<string> {
  const go = readFileSync(KANJI_FROM_GO, 'utf8')
  const block = go.match(/var brandKanjiLocales = map\[string\]bool\{([\s\S]*?)\n\}/)
  if (!block) {
    throw new Error('后端 reply_lang.go 里找不到 brandKanjiLocales（品牌语种分档被搬走或改名，交叉锁失效）')
  }
  const codes = [...block[1].matchAll(/"([a-z_]+)"\s*:\s*true/g)].map((m) => m[1])
  if (codes.length === 0) throw new Error('brandKanjiLocales 解析出 0 个语种（正则没命中≠没有语种，属锁自伤）')
  return new Set(codes)
}

describe('品牌名写法（082x）', () => {
  const files = readdirSync(LOCALES_DIR).filter((f) => f.endsWith('.ts')).sort()

  it('locale 目录真的扫到了 10 份词典（防"0 文件＝0 违规"的恒空假绿）', () => {
    expect(files.length).toBe(10)
  })

  it('12 语种 × 两把键逐个扫，零违规', () => {
    const scanned: string[] = []
    const bad: string[] = []
    // 磁盘上的 10 份非中英词典（zh/en 在 panels/ 与 dicts 里，下面单独扫）
    for (const f of files) {
      const code = fileToCode(f)
      const src = readFileSync(`${LOCALES_DIR}/${f}`, 'utf8')
      for (const key of BRAND_KEYS) {
        const v = pickValue(src, key)
        scanned.push(`${code}.${key}`)
        const why = brandViolation(code, key, v)
        if (why) bad.push(why)
      }
      // 整份文件里不许在任何键上冒出拼音名（补译时很容易顺手音译别的品牌键）
      if (PINYIN_RE.test(src)) bad.push(`${f}: 整份词典含拼音品牌名`)
    }
    // 中文面板（zh+en 两档）、挂件组件、挂件接口层也点名扫一遍
    const panel = readFileSync(`${SRC_ROOT}/i18n/panels/chat.ts`, 'utf8')
    const zhTitle = pickValue(panel, 'chat.assistTitle')
    const enBlock = panel.slice(panel.indexOf('export const en'))
    const enTitle = pickValue(enBlock, 'chat.assistTitle')
    scanned.push('zh.chat.assistTitle', 'en.chat.assistTitle')
    const zhWhy = brandViolation('zh', 'chat.assistTitle', zhTitle)
    if (zhWhy) bad.push(zhWhy)
    const enWhy = brandViolation('en', 'chat.assistTitle', enTitle)
    if (enWhy) bad.push(enWhy)
    for (const rel of ['components/AiAssist.tsx', 'api/assist.ts']) {
      const src = readFileSync(`${SRC_ROOT}/${rel}`, 'utf8')
      scanned.push(rel)
      if (PINYIN_RE.test(src)) bad.push(`${rel}: 含拼音品牌名（标题应取 i18n 键，模型自称口径在后端【回复语言】段）`)
    }
    // 覆盖面数字：10 份 × 2 键 + 中英面板 + 2 个组件/接口文件 = 24 个扫描点
    expect(scanned.length).toBe(24)
    expect(bad, `品牌名违规：\n${bad.join('\n')}`).toEqual([])
  })

  it('后端品牌语种分档与本目录实际用的语种逐一对齐', () => {
    const fromGo = kanjiLocalesFromGo()
    // 后端这张表必须正好是 {zh, zh_hant, ja}
    expect([...fromGo].sort()).toEqual(['ja', 'zh', 'zh_hant'])
    // 本目录里 app.title 真取汉字名的语种（zh 在 panels/dicts 里，不在 locales 目录）
    const kanjiOnDisk = files
      .filter((f) => pickValue(readFileSync(`${LOCALES_DIR}/${f}`, 'utf8'), 'app.title').includes('能言'))
      .map(fileToCode)
      .sort()
    expect(kanjiOnDisk, '磁盘上以汉字名出题的语种必须与后端分档一致（差一个就会出现标题与正文两个品牌名）')
      .toEqual(['ja', 'zh_hant'])
    // 后端分档里除 zh 外的每一个码，磁盘上都得有对应词典（防"表里有、词典里没有"的空档）
    for (const code of fromGo) {
      if (code === 'zh') continue
      expect(files.map(fileToCode), `后端把 ${code} 当汉字语种，但 locales 里没有这份词典`).toContain(code)
    }
  })

  it('反证：坏字典必须被抓（证明判据真能红，而不是恒绿摆设）', () => {
    // ① 拼音名复活（本次事故原样）
    expect(brandViolation('de', 'chat.assistTitle', 'Nengyan AI-Assistent'))
      .toContain('出现拼音品牌名')
    // ② 大小写/分词变体也得住
    expect(brandViolation('es', 'app.title', 'NengYan')).toContain('出现拼音品牌名')
    // ③ 汉字语种被换成英文品牌名（日文最容易踩的一档）
    expect(brandViolation('ja', 'chat.assistTitle', 'LangCross AIアシスタント'))
      .toContain('该语种界面用汉字品牌名')
    // ④ 非汉字语种混用汉字名（两个名字并排出现——正是「模型自称与标题各叫各的」的界面形态）
    expect(brandViolation('ko', 'app.title', 'LangCross 能言')).toContain('不许混用汉字品牌名')
    // ④b 只写汉字名、连 LangCross 都没有：也算违规，但报的是"必须写 LangCross"
    expect(brandViolation('ko', 'app.title', '能言')).toContain('必须写 LangCross')
    // ⑤ 取不到值＝锁失明，不许当合规
    expect(brandViolation('fr', 'app.title', '')).toContain('取不到值')
    // 对照组：合规取值必须全绿，否则上面五条只是"恒红"
    expect(brandViolation('de', 'chat.assistTitle', 'LangCross AI-Assistent')).toBeNull()
    expect(brandViolation('ja', 'app.title', '能言')).toBeNull()
    expect(brandViolation('zh_hant', 'chat.assistTitle', '能言 AI 助理')).toBeNull()
    expect(brandViolation('en', 'app.title', 'LangCross')).toBeNull()
  })
})
