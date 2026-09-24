// ============================================================================
// i18n/landing-demo.test.ts — ★ 〇-Q Landing 演示卡 i18n 化回归闸门
// 守护「今日开发内容」：
//   ① 12 语种词典均含 land.demo* 全套键且非空；
//   ② demoSrcLang 决策：英语 UI 源语种为 en（演示 EN→ZH），其余语种沿用自身；
//   ③ 英语 UI 实际渲染的源句是英文（translateIn(demoSrcLang('en'),'land.demoSrc') 不含汉字，EN→ZH）；
//   ④ land.demoFinal 键恒为英文（TR_LANG='en'），全 12 语种均不含汉字；
//   ⑤ land.demoTag 方向：en =「EN → ZH」，zh_hant =「ZH → EN」，其余 =「<CODE> → EN」；
//   ⑥ 非 CJK 语种演示内容零汉字残留（漏译扫描）。
// 运行：npx vitest run src/i18n/landing-demo.test.ts
// ============================================================================
// @vitest-environment node
import { describe, expect, it } from 'vitest'
import { demoSrcLang, translateIn, type Lang } from './index'

const LANGS: Lang[] = [
  'zh', 'en', 'ru', 'fr', 'ar', 'es', 'pt', 'de', 'ja', 'ko', 'th', 'zh_hant',
]
const DEMO_KEYS = [
  'land.demoSrc', 'land.demoFinal', 'land.demoTerm1', 'land.demoTerm2', 'land.demoTerm3',
] as const

// 是否含 CJK 统一表意汉字（含中日韩 extending 区，覆盖繁中/日）
const hasHan = (s: string) => [...s].some((c) => /[一-鿿㐀-䶿]/.test(c))

describe('〇-Q Landing 演示卡 i18n（12 语种 + 英语 UI 演示 EN→ZH）', () => {
  it('12 语种词典均含 land.demo* 全套键且非空', () => {
    for (const lang of LANGS) {
      for (const k of DEMO_KEYS) {
        expect(translateIn(lang, k), `${lang} 缺 ${k}`).toBeTruthy()
      }
    }
  })

  it('demoSrcLang：英语 UI 源语种为 en（演示 EN→ZH），其余语种沿用自身', () => {
    expect(demoSrcLang('en')).toBe('en')
    for (const l of ['zh', 'ru', 'fr', 'ar', 'es', 'pt', 'de', 'ja', 'ko', 'th', 'zh_hant'] as Lang[]) {
      expect(demoSrcLang(l), `${l} 应沿用自身`).toBe(l)
    }
  })

  it('演示源句：英语 UI 实际渲染英文源（EN→ZH），母语界面取母语', () => {
    // 核心回归：en UI 经 demoSrcLang('en')='en' 取英文源句，演示方向为真实 EN→ZH（非退化的「英→英」，也非误写的 ZH→EN）
    expect(hasHan(translateIn(demoSrcLang('en'), 'land.demoSrc')), 'en UI 源句应为英文').toBe(false)
    // 中文 / 繁中界面源句亦为汉字
    expect(hasHan(translateIn('zh', 'land.demoSrc'))).toBe(true)
    expect(hasHan(translateIn('zh_hant', 'land.demoSrc'))).toBe(true)
    // 其余母语界面源句为其母语（非空即过关）
    for (const l of ['ru', 'fr', 'ar', 'es', 'pt', 'de', 'ja', 'ko', 'th'] as Lang[]) {
      expect(translateIn(l, 'land.demoSrc'), `${l} 源句应为母语`).toBeTruthy()
    }
  })

  it('演示定稿 land.demoFinal 恒为英文（TR_LANG=\'en\'，全 12 语种均不含汉字）', () => {
    for (const lang of LANGS) {
      const v = translateIn(lang, 'land.demoFinal')
      expect(v, `${lang} 定稿`).toMatch(/[A-Za-z]{4,}/)
      expect(hasHan(v), `${lang} 定稿含汉字`).toBe(false)
    }
  })

  it('land.demoTag 方向：en=「EN → ZH」、zh_hant=「ZH → EN」，其余=「<CODE> → EN」（与 demoSrcLang 决策一致）', () => {
    const want: Record<Lang, string> = {
      zh: 'ZH → EN', en: 'EN → ZH', zh_hant: 'ZH → EN',
      ru: 'RU → EN', fr: 'FR → EN', ar: 'AR → EN', es: 'ES → EN',
      pt: 'PT → EN', de: 'DE → EN', ja: 'JA → EN', ko: 'KO → EN', th: 'TH → EN',
    }
    for (const lang of LANGS) {
      expect(translateIn(lang, 'land.demoTag'), `${lang} demoTag 方向`).toContain(want[lang])
    }
  })

  it('英语 UI 演示方向 EN→ZH：源句英文、定稿取中文源句（与 demoFinal(\'en\') 实现一致）', () => {
    // demoFinal('en') 实际返回 translateIn('zh','land.demoSrc')（中文源句），与 demoSrc('en')=英文 构成 EN→ZH
    expect(hasHan(translateIn(demoSrcLang('en'), 'land.demoSrc')), 'en 源句应为英文').toBe(false)
    expect(hasHan(translateIn('zh', 'land.demoSrc')), 'en 定稿应为中文').toBe(true)
  })

  it('非 CJK 语种（ru/fr/ar/es/pt/de/th）演示内容零汉字残留（漏译扫描）', () => {
    const nonCJK: Lang[] = ['ru', 'fr', 'ar', 'es', 'pt', 'de', 'th']
    for (const lang of nonCJK) {
      const bad = DEMO_KEYS.filter((k) => hasHan(translateIn(lang, k)))
      expect(bad, `${lang} 演示含汉字: ${bad.join(',')}`).toEqual([])
    }
  })
})
