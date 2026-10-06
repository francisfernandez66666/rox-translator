// ============================================================================
// usdtPromise.test.ts — ⑮ 腿3（收银台文案与能力联动）的断言
// ① 判据本身：只有 auto_settle_live 为字面 true 才允许说"自动入账"；
// ② 两个键在全部 12 语种都取得到值（缺键会被回显成 key，界面直接漏英文原文给客户）；
// ③ 文案两侧同口径：降级档**不得**再出现"自动入账/automatically"字样，
//    而正常档**必须**出现（负向锁配正向对照，否则负向恒真是空转）。
// 反证口径：把 usdtCheckoutKey 的判断写成 `autoSettleLive !== false`（旧后端字段缺失时
// 也会算真）⇒ ①② 里 undefined 那一档当场红；把降级文案抄成"达到确认数后自动入账"⇒ ③ 红。
// 运行：npx vitest run src/lib/usdtPromise.test.ts
// ============================================================================
// @vitest-environment node
import { describe, expect, it, afterEach } from 'vitest'
import { ALL_KEYS, setLang, t, type Lang } from '@/i18n'
import { usdtCheckoutKey, usdtPromiseIsLive } from './usdtPromise'

const LIVE_KEY = 'billing.usdtCheckoutHint'
const MANUAL_KEY = 'billing.usdtCheckoutManual'

const LANGS: Lang[] = ['zh', 'en', 'zh_hant', 'ja', 'ko', 'ru', 'fr', 'de', 'es', 'pt', 'ar', 'th']

// 「自动入账」这层意思在各语种的正/负向特征词（只用来判"有没有在承诺自动"，不做全文等值）。
// ★ 收的是**词干**而不是完整词形（automátic 而非 automáticamente）：同一语种里形容词/副词
//   变体都会出现（conciliação automática / acreditado automáticamente），取完整词形会让
//   正常档漏判、降级档反而恒真——词干是**更严**的一侧，不是放宽。
const AUTO_MARKS: Record<string, string[]> = {
  zh: ['自动入账'],
  en: ['automatically'],
  zh_hant: ['自動入帳'],
  ja: ['自動的に計上'],
  ko: ['자동 입금'],
  ru: ['автоматически'],
  fr: ['automatiquement'],
  de: ['automatisch'],
  es: ['automátic'],
  pt: ['automátic'],
  ar: ['تلقائيًا'],
  th: ['จะเข้าระบบเอง'],
}

afterEach(() => setLang('zh'))

describe('⑮ 收银台承诺判据', () => {
  it('只有字面 true 才允许承诺自动入账（缺字段/假值/字符串一律降级）', () => {
    expect(usdtCheckoutKey(true)).toBe(LIVE_KEY)
    for (const notLive of [false, undefined, null, 'true', 1, 0, {}]) {
      expect(usdtCheckoutKey(notLive), `值 ${String(notLive)} 必须走人工核销文案`).toBe(MANUAL_KEY)
      expect(usdtPromiseIsLive(notLive)).toBe(false)
    }
    expect(usdtPromiseIsLive(true)).toBe(true)
  })

  it('两个键都在全量词典里（12 语种逐键非空）', () => {
    expect(ALL_KEYS).toContain(LIVE_KEY)
    expect(ALL_KEYS).toContain(MANUAL_KEY)
    for (const lang of LANGS) {
      setLang(lang)
      expect(t(LIVE_KEY), `${lang} 取不到正常档文案`).not.toBe(LIVE_KEY)
      expect(t(MANUAL_KEY), `${lang} 取不到降级档文案`).not.toBe(MANUAL_KEY)
      expect(t(MANUAL_KEY).length).toBeGreaterThan(10)
    }
  })

  it('降级档不再承诺自动入账，正常档仍承诺（负向锁配正向对照）', () => {
    for (const lang of LANGS) {
      setLang(lang)
      const marks = AUTO_MARKS[lang]
      expect(marks, `${lang} 未登记特征词，判据会空转`).toBeTruthy()
      const live = t(LIVE_KEY)
      const manual = t(MANUAL_KEY)
      expect(marks.some((m) => live.includes(m)), `${lang} 正常档应含"自动"承诺：${live}`).toBe(true)
      expect(marks.some((m) => manual.includes(m)), `${lang} 降级档不得再写"自动"承诺：${manual}`).toBe(false)
      // 降级档必须点名"人工核销"这条真实处置路径（只说"请稍后"等于把客户挂在半空）
      expect(manual.length).toBeGreaterThan(20)
    }
  })
})
