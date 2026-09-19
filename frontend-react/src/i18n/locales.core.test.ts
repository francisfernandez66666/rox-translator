// ============================================================================
// i18n/locales.core.test.ts — ★ #23 多语言部分词典覆盖闸门（2026-09-19）
// 十份 locales/*.ts 各自必须：① 键全部落在 CORE_KEYS 核心集内（防手写错键静默失效）；
// ② 核心集 100% 覆盖（新键进了核心前缀而某语种没跟上即红灯）；③ 值非空、
// {placeholder} 逐键与英文源一致；④ 非 CJK 语种（俄/法/阿/西/葡/德/泰）值内
// 禁止残留汉字（漏译扫描）。回退链行为另测：未翻键 lang→en→zh，绝不露裸 key。
// 运行：npx vitest run src/i18n/locales.core.test.ts
// ============================================================================
// @vitest-environment node
import { describe, expect, it, afterEach, vi } from 'vitest'
import { CORE_KEYS, setLang, getLang, t, type Lang } from './index'
import { dict as ru } from './locales/ru'
import { dict as fr } from './locales/fr'
import { dict as ar } from './locales/ar'
import { dict as es } from './locales/es'
import { dict as pt } from './locales/pt'
import { dict as de } from './locales/de'
import { dict as ja } from './locales/ja'
import { dict as ko } from './locales/ko'
import { dict as th } from './locales/th'
import { dict as zhHant } from './locales/zh-hant'

const CORE = new Set(CORE_KEYS)
// 英文源值（占位符一致性基准）：index 未导出合并词典，切到 en 逐键取词
setLang('en')
const EN: Record<string, string> = Object.fromEntries(CORE_KEYS.map((k) => [k, t(k)]))

// 非 CJK 语种：值中若还有汉字即判漏译（ja/ko/zh_hant 本身含 CJK/谚文，天然豁免）
const CJK_BAN: Array<[Lang, Record<string, string>]> = [
  ['ru', ru], ['fr', fr], ['ar', ar], ['es', es], ['pt', pt], ['de', de], ['th', th],
]
const ALL: Array<[Lang, Record<string, string>]> = [...CJK_BAN, ['ja', ja], ['ko', ko], ['zh_hant', zhHant]]

const toks = (s: string) => (s.match(/\{[a-zA-Z]+\}/g) || []).sort().join(',')

afterEach(() => setLang('zh'))

describe('locales 部分词典覆盖（★ #23）', () => {
  for (const [lang, dict] of ALL) {
    it(`${lang}：核心集 100% 覆盖、无越界键、占位符逐键一致、无空值`, () => {
      expect(Object.keys(dict).length).toBe(CORE_KEYS.length)
      for (const k of Object.keys(dict)) expect(CORE.has(k), `越界键 ${k}`).toBe(true)
      for (const k of CORE_KEYS) {
        expect(dict[k], `缺键 ${k}`).toBeTruthy()
        expect(toks(dict[k] ?? ''), `占位符漂移 ${k}`).toBe(toks(EN[k] ?? ''))
      }
    })
  }
  for (const [lang, dict] of CJK_BAN) {
    it(`${lang}：值内零汉字残留（漏译扫描）`, () => {
      const bad = Object.entries(dict).filter(([, v]) => /[\u4e00-\u9fff]/.test(v)).map(([k]) => k)
      expect(bad, `含汉字: ${bad.slice(0, 5)}`).toEqual([])
    })
  }
  it('回退链：未翻键按 lang→en→zh 兜底，全链落空才回显 key', () => {
    const probe = 'tenants.permissionsHint' // 管理后台长尾键，刻意不在核心集
    expect(CORE.has(probe)).toBe(false)
    setLang('ru')
    const ruVal = t(probe) // 俄词典无此键 → 应等于英文值
    setLang('en')
    expect(ruVal).toBe(t(probe))
    setLang('ru')
    expect(t('no.such.key.at.all')).toBe('no.such.key.at.all')
  })
  it('核心集已翻键：切语种即生效（抽查每语种首/中/末三键）', () => {
    for (const [lang, dict] of ALL) {
      setLang(lang)
      const picks = [CORE_KEYS[0], CORE_KEYS[Math.floor(CORE_KEYS.length / 2)], CORE_KEYS[CORE_KEYS.length - 1]]
      for (const k of picks) expect(t(k), `${lang} 取词 ${k}`).toBe(dict[k])
    }
  })
  it('持久化与未知值回落：setLang 写 app_lang；初始读到未知代码回落 zh', async () => {
    // ① setLang 必须同步落盘（外国用户切语种后刷新不丢）
    setLang('ar')
    expect(getLang()).toBe('ar')
    expect(localStorage.getItem('app_lang')).toBe('ar')
    // ② 旧版本/手工改出的野值不得让全站取词踩空：重导入模块模拟冷启动
    setLang('zh')
    const zhVal = t(CORE_KEYS[0])
    localStorage.setItem('app_lang', 'klingon')
    vi.resetModules()
    const fresh = await import('./index')
    expect(fresh.getLang()).toBe('zh')
    expect(fresh.t(CORE_KEYS[0])).toBe(zhVal)
    localStorage.setItem('app_lang', 'zh')
  })
})
