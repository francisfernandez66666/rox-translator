// ============================================================================
// lib/ticketEta.lock.test.ts — P2-01「件型耗时预告」源码锁（2026-10-27）
//
// 钉四件事，缺一件就会退回"进度 30% 干等 5 分半、访客以为卡死"那个形态：
//   E1 档位表四档齐、单调（文本 ≤ 文档 ≤ 图片 ≤ 音频），且**判档派生自** api/translate 的
//      IMAGE_EXTS/AUDIO_EXTS——本仓两份图片/音频列表漂过一次（一边加了 .heic 另一边没加），
//      漂了就是"图片按文档档报耗时"，给一个错的预期比不给更坏；
//   E2 行为：.png/.heic→image、.mp3/.opus→audio、.docx→document、无扩展名→document、空→text；
//   E3 消费点在位：进度弹窗里那句必须被 progressAnimating 门住（终态不许继续演），
//      且必须走 tpl('tk.etaTypical', {n})——写死中文＝十语种访客看到英文；
//   E4 12 份词典逐份点名该键**且带 {n} 占位符**（漏一个占位符，那一语种就显示字面 "{n}"）。
// 每条腿各配一枚反证，喂同一个判据函数。
// ============================================================================
// @vitest-environment node
import { describe, it, expect } from 'vitest'
import { readFileSync } from 'fs'
import { resolve } from 'path'
import { ticketWaitMinutes, ticketWaitTier, type WaitTier } from './ticketEta'

const here = __dirname
const etaSrc = readFileSync(resolve(here, 'ticketEta.ts'), 'utf8')
const pageSrc = readFileSync(resolve(here, '../components/TicketsPage.tsx'), 'utf8')
const dicts = {
  zh: readFileSync(resolve(here, '../i18n/dicts.zh.ts'), 'utf8'),
  en: readFileSync(resolve(here, '../i18n/dicts.en.ts'), 'utf8'),
}
const LOCALES = ['zh-hant', 'ja', 'ko', 'de', 'fr', 'es', 'pt', 'ru', 'th', 'ar']

function stripComments(s: string): string {
  return s.replace(/\/\*[\s\S]*?\*\//g, '').replace(/^\s*\/\/.*$/gm, '')
}

/** tierTableOk：档位表存在且单调递增（text ≤ document ≤ image ≤ audio） */
function tierTableOk(src: string): boolean {
  const c = stripComments(src)
  const n = (t: WaitTier) => {
    const m = new RegExp(t + ':\\s*(\\d+)').exec(c)
    return m ? Number(m[1]) : NaN
  }
  const [text, doc, img, aud] = [n('text'), n('document'), n('image'), n('audio')]
  return [text, doc, img, aud].every((x) => Number.isFinite(x) && x > 0)
    && text <= doc && doc <= img && img <= aud
}

/** derivedFromFormatSourceOk：判档必须引用那两个集合，且本文件不许自己抄扩展名字面量 */
function derivedFromFormatSourceOk(src: string): boolean {
  const c = stripComments(src)
  // 别名 @/ 与相对路径都算（本仓运行期走 @/，写死其中一种会让判据在另一种写法下"看不见 import"）
  // 具名导入的**顺序不算**（本仓按字母序写 AUDIO_EXTS, IMAGE_EXTS；要求某一种顺序＝给下一位埋假红）
  const clause = /import\s*\{([^}]*)\}\s*from\s*'(@\/api|\.\.)\/translate'/.exec(c)
  const importsSets = !!clause && clause[1].includes('IMAGE_EXTS') && clause[1].includes('AUDIO_EXTS')
  const handCopied = /\.(png|jpe?g|heic|webp|mp3|m4a|wav|ogg|opus|flac)['"]/.test(c)
  return importsSets && !handCopied
}

/** consumedOk：进度弹窗里的消费点三件齐（ETA 门、档位调用、占位符取词） */
function consumedOk(src: string): boolean {
  const c = stripComments(src)
  // 门的判据要"贴着"目标元素：`[^<]*` 挡住"前面另有一个 progressAnimating 门、隔着别的元素也算数"
  // ——反证 E3a 首跑就是这么假绿的：把 ETA 的门换成 true && 之后，正则从**上一块** WordSwap 的
  //   `{progressAnimating && (` 一路跨过来仍然判通过。跨元素的松判据等于没判据。
  const gated = /\{\s*progressAnimating\s*&&\s*\(\s*<p[^<]*data-testid="tk-eta"/.test(c)
  return gated
    && /tpl\(\s*'tk\.etaTypical'\s*,\s*\{\s*n:\s*ticketWaitMinutes\(\s*ticketWaitTier\(/.test(c)
}

/** dictHasKeyOk：某份词典里该键在位且带 {n} 占位符 */
function dictHasKeyOk(src: string): boolean {
  // 值定界符两种写法都要认（zh/en 门面词典历史上是单引号、十份懒加载词典是双引号）
  const m = /["']tk\.etaTypical["']\s*:\s*(?:"([^"]*)"|'([^']*)')/.exec(src)
  if (!m) return false
  return (m[1] ?? m[2] ?? '').includes('{n}')
}

describe('P2-01 件型耗时预告锁', () => {
  it('E1 档位表四档齐且单调，判档派生自 IMAGE_EXTS/AUDIO_EXTS', () => {
    expect(tierTableOk(etaSrc)).toBe(true)
    expect(derivedFromFormatSourceOk(etaSrc), '档位判据不许自己抄一份扩展名清单').toBe(true)
  })

  it('E2 判档行为逐型点名', () => {
    expect(ticketWaitTier('海报-en.png')).toBe('image')
    expect(ticketWaitTier('IMG_2026.HEIC')).toBe('image')   // 大写扩展名与 .heic 都必须认
    expect(ticketWaitTier('访谈录音.opus')).toBe('audio')
    expect(ticketWaitTier('合同.docx')).toBe('document')
    expect(ticketWaitTier('没有扩展名的名字')).toBe('document')
    expect(ticketWaitTier('')).toBe('text')
    expect(ticketWaitMinutes('image')).toBeGreaterThanOrEqual(5) // 现网实测 5 分 31 秒 ⇒ 上限不得低到骗人
  })

  it('E3 消费点在进度弹窗、被 progressAnimating 门住、走 12 语种取词', () => {
    expect(consumedOk(pageSrc)).toBe(true)
  })

  it('E4 十二份词典逐份点名该键且带 {n} 占位符', () => {
    for (const [name, src] of Object.entries(dicts)) {
      expect(dictHasKeyOk(src), `${name} 门面词典缺 tk.etaTypical 或缺 {n}`).toBe(true)
    }
    for (const lang of LOCALES) {
      const src = readFileSync(resolve(here, `../i18n/locales/${lang}.ts`), 'utf8')
      expect(dictHasKeyOk(src), `${lang} 缺 tk.etaTypical 或缺 {n}`).toBe(true)
    }
  })

  // ===================== 反证 =====================
  it('反证 E1a：把音频档调到比图片还短 → 判红（档位单调是判据的一部分）', () => {
    const mutated = stripComments(etaSrc).includes('audio: 8')
      ? etaSrc.replace('audio: 8', 'audio: 2') : etaSrc
    expect(mutated).not.toBe(etaSrc)
    expect(tierTableOk(mutated)).toBe(false)
  })

  it('反证 E1b：改回手抄扩展名清单 → 判红（两份列表迟早漂）', () => {
    const mutated = etaSrc.replace(
      "if ((IMAGE_EXTS as readonly string[]).includes(ext)) return 'image'",
      "if (['.png', '.jpg', '.jpeg', '.heic'].includes(ext)) return 'image'")
    expect(mutated).not.toBe(etaSrc)
    expect(derivedFromFormatSourceOk(mutated)).toBe(false)
  })

  it('反证 E3a：把 ETA 挪出 progressAnimating 门（终态还在演耗时）→ 判红', () => {
    const mutated = pageSrc.replace('{progressAnimating && (\n          <p style={{ margin: \'0 0 8px\'', '{true && (\n          <p style={{ margin: \'0 0 8px\'')
    expect(mutated).not.toBe(pageSrc)
    expect(consumedOk(mutated)).toBe(false)
  })

  it('反证 E3b：写死中文而不走词典 → 判红', () => {
    const mutated = pageSrc.replace(/tpl\('tk\.etaTypical', \{ n: ticketWaitMinutes\(ticketWaitTier\(String\(detail\?\.ticket\?\.title \|\| ''\)\)\) \}\)/, "'这类件通常需要约 6 分钟'")
    expect(mutated).not.toBe(pageSrc)
    expect(consumedOk(mutated)).toBe(false)
  })

  it('反证 E4：某语种漏掉 {n} 占位符 → 判红（漏一个占位符那语种显示字面 "{n}"）', () => {
    const src = readFileSync(resolve(here, '../i18n/locales/th.ts'), 'utf8')
    const mutated = src.replace(/("tk\.etaTypical": )"[^"]*"/, '$1"งานลักษณะนี้มักใช้เวลาประมาณ นาที"')
    expect(mutated).not.toBe(src)
    expect(dictHasKeyOk(mutated)).toBe(false)
  })
})
