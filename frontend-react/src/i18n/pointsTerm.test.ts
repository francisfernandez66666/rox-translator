// ============ i18n/pointsTerm.test.ts · 职责说明 ============
// 计费单位「积分」的跨语种写法交叉锁（★ 082x 增补，2026-09-29，现网复问抓到）。
//
// 抓的是什么：
//   换件后拿英文访客复问价格，补翻把答案里的「积分」写成了 "integral"
//   （"7.5 integral fee" / "400 integral per 1,000 characters"）。官网没有任何一种界面语言
//   出现过这个词——客户一边在界面上看到 credits，一边听助手说 integral，
//   对不上账的一句报价就是对外错报（F-12「三口径打架」同一族形态，只是这次发生在**语言之间**）。
//
// 为什么是一条交叉锁而不是一句提示词：
//   模型侧的词表住在 backend-go/internal/assist/engine/reply_lang.go 的 pointsTermByLang，
//   界面侧的词面住在本目录的 app.pkgLineFmt。两边各写一份迟早分叉（品牌名那次就是这么翻车的，
//   见同目录 brandName.test.ts 的教训记录）。本锁**直接读那份 Go 源码**逐语种核对，
//   只改一边必红；真正的判据（怎么算不一致）抽成纯函数 termViolation()，
//   由反证用例直接喂坏数据自证它抓得住——只做磁盘扫描的锁一旦判据写错会长期恒绿。
//
// 口径：界面侧取值以 app.pkgLineFmt 里 `{points}` 与 `≈` 之间那个词为准
//   （全站 11 份词典都是 "{points} <单位> ≈ {approx} …" 这一形态，是唯一一处把计费单位
//    当作独立名词写出来的界面文案，故取它而不是另挑一个词）。
//   简体中文不在表内：素材本来就是中文，后端 pointsTermLine 对 zh 返回空串（不追加口径）。
// =============================================
import { describe, expect, it } from 'vitest'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

const SRC_ROOT = fileURLToPath(new URL('../', import.meta.url)).replace(/\/$/, '')
const GO_TABLE = fileURLToPath(
  new URL('../../../backend-go/internal/assist/engine/reply_lang.go', import.meta.url),
)

// 界面里出现计费单位的词典：10 份非中英 locale ＋ dicts.en（zh 不列入，见文件头口径）
const DICT_FILES: Record<string, string> = {
  en: 'i18n/dicts.en.ts',
  zh_hant: 'i18n/locales/zh-hant.ts',
  ja: 'i18n/locales/ja.ts',
  ko: 'i18n/locales/ko.ts',
  de: 'i18n/locales/de.ts',
  fr: 'i18n/locales/fr.ts',
  ru: 'i18n/locales/ru.ts',
  es: 'i18n/locales/es.ts',
  pt: 'i18n/locales/pt.ts',
  ar: 'i18n/locales/ar.ts',
  th: 'i18n/locales/th.ts',
}

/**
 * 一个语种的两侧写法一致吗？返回违规说明（一致返回 null）。
 * 三档都是**正向点名**，不用"没扫到就算过"：
 *   - 界面侧取不到值＝锁失明（词典缺键或句式改了），不是这里干净了；
 *   - Go 侧没有这个语种＝模型对该语种完全不会提术语口径，属表漏档；
 *   - 两侧词面不等（忽略首尾空白）＝对外两个叫法。
 */
function termViolation(code: string, goTerm: string | undefined, diskTerm: string): string | null {
  const disk = diskTerm.trim()
  if (disk === '') return `${code}: 界面词典取不到计费单位（缺 app.pkgLineFmt 或句式变了，属锁失明而非合规）`
  if (goTerm === undefined) return `${code}: 后端 pointsTermByLang 没有这一档（模型对该语种不会提术语口径）`
  const want = goTerm.trim()
  if (want === '') return `${code}: 后端表里这一档是空串（等于没配）`
  if (want !== disk) return `${code}: 界面写「${disk}」而后端教模型写「${want}」⇒ 同一语种两个叫法`
  return null
}

/** 读后端 pointsTermByLang，解析成 语种码 → 词 */
function tableFromGo(): Map<string, string> {
  const src = readFileSync(GO_TABLE, 'utf8')
  const block = src.match(/var pointsTermByLang = map\[string\]string\{([\s\S]*?)\n\}/)
  if (!block) {
    throw new Error('后端 reply_lang.go 里找不到 pointsTermByLang（计费单位词表被搬走或改名，交叉锁失效）')
  }
  const out = new Map<string, string>()
  for (const m of block[1].matchAll(/"([a-z_]+)"\s*:\s*"([^"]*)"/g)) out.set(m[1], m[2])
  // 解析出 0 条属正则没命中，不是"没有语种"——必须报错而不是让用例恒绿
  if (out.size === 0) throw new Error('pointsTermByLang 解析出 0 档（锁自伤）')
  return out
}

/** 从词典源文件里取 app.pkgLineFmt，再截出 `{points}` 与 `≈` 之间的计费单位词 */
function pointsTermFromDict(rel: string): string {
  const src = readFileSync(`${SRC_ROOT}/${rel}`, 'utf8')
  const m = src.match(/["']app\.pkgLineFmt["']\s*:\s*(["'])([\s\S]*?)\1/)
  if (!m) return ''
  const fmt = m[2]
  const at = fmt.indexOf('{points}')
  if (at < 0) return ''
  const rest = fmt.slice(at + '{points}'.length)
  const cut = rest.indexOf('≈')
  return (cut < 0 ? rest : rest.slice(0, cut)).trim()
}

describe('计费单位「积分」的跨语种写法（082x 增补）', () => {
  const goTable = tableFromGo()

  it('词典覆盖面：11 份文件逐个取到值，后端表正好 11 档（含"零命中＝锁失明"防线）', () => {
    const codes = Object.keys(DICT_FILES)
    expect(codes.length).toBe(11)
    expect(goTable.size).toBe(11)
    // 后端表里的语种必须与界面词典一一对应（多一档＝界面没有该语种；少一档＝该语种没人管）
    expect([...goTable.keys()].sort()).toEqual([...codes].sort())
    for (const [code, rel] of Object.entries(DICT_FILES)) {
      expect(pointsTermFromDict(rel), `${code}: 取不到计费单位词（句式改动/缺键）`).not.toBe('')
    }
  })

  it('11 语种逐个交叉核对：后端教模型说的词 == 界面写的词', () => {
    const bad: string[] = []
    for (const [code, rel] of Object.entries(DICT_FILES)) {
      const why = termViolation(code, goTable.get(code), pointsTermFromDict(rel))
      if (why) bad.push(why)
    }
    expect(bad, `计费单位跨语种口径分叉：\n${bad.join('\n')}`).toEqual([])
  })

  it('简体中文不追加口径（素材本来就是中文），且界面侧 zh 用的就是「积分」', () => {
    expect(goTable.has('zh')).toBe(false)
    const zh = pointsTermFromDict('i18n/dicts.zh.ts')
    expect(zh, 'zh 词典里的计费单位应当正是「积分」——它是后端所有中文素材的原始词').toBe('积分')
  })

  it('反证：两侧词面不一致必须被抓（证明判据真能红，而不是恒绿摆设）', () => {
    // ① 现网事故原样：界面 credits，模型被教成 integral
    expect(termViolation('en', 'integral', 'credits')).toContain('同一语种两个叫法')
    // ② 只改后端不改界面（下一次补译最容易走的一条）
    expect(termViolation('ja', 'クレジット', 'ポイント')).toContain('同一语种两个叫法')
    // ③ 后端漏档（新增语种只加了词典）
    expect(termViolation('vi', undefined, 'điểm')).toContain('后端 pointsTermByLang 没有这一档')
    // ④ 后端表里塞空串＝等于没配，不许被当成"该语种不提术语"而静默放过
    expect(termViolation('ko', '', '포인트')).toContain('空串')
    // ⑤ 界面取不到值＝锁失明，不是合规
    expect(termViolation('de', 'Punkte', '')).toContain('取不到计费单位')
    // 对照组：合规取值必须全绿，否则上面五条只是"恒红"
    expect(termViolation('en', 'credits', 'credits')).toBeNull()
    expect(termViolation('ar', 'نقطة', 'نقطة')).toBeNull()
    expect(termViolation('ru', 'кредитов', 'кредитов')).toBeNull()
  })
})
