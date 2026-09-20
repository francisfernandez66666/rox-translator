// ============================================================================
// i18n/locales.core.test.ts — ★ 多语言部分词典覆盖闸门（#23 建立 → 2026-09-20 全站十语种升级）
// 十份 locales/*.ts 各自必须：① 键与 ALL_KEYS 全量词典逐键一致（多一份、少一份都红灯——
// 新面板键而某语种没跟上即失败；历史 CORE_KEYS 核心集口径已被全量口径吸收）；
// ② 值非空、{placeholder} 逐键与英文源一致；③ 非 CJK 语种（俄/法/阿/西/葡/德/泰）值内
// 禁止残留汉字（漏译扫描）。取词回退链行为另测：全链落空才回显 key。
// 运行：npx vitest run src/i18n/locales.core.test.ts
// ============================================================================
// @vitest-environment node
import { describe, expect, it, afterEach, vi } from 'vitest'
import { ALL_KEYS, setLang, getLang, t, type Lang } from './index'
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

const ALLK = new Set(ALL_KEYS)
// 英文源值（占位符一致性基准）：index 未导出合并词典，切到 en 逐键取词
setLang('en')
const EN: Record<string, string> = Object.fromEntries(ALL_KEYS.map((k) => [k, t(k)]))

// 非 CJK 语种：值中若还有汉字即判漏译（ja/ko/zh_hant 本身含 CJK/谚文，天然豁免）
const CJK_BAN: Array<[Lang, Record<string, string>]> = [
  ['ru', ru], ['fr', fr], ['ar', ar], ['es', es], ['pt', pt], ['de', de], ['th', th],
]
const ALL: Array<[Lang, Record<string, string>]> = [...CJK_BAN, ['ja', ja], ['ko', ko], ['zh_hant', zhHant]]

const toks = (s: string) => (s.match(/\{[A-Za-z0-9_]+\}/g) || []).sort().join(',')

afterEach(() => setLang('zh'))

describe('locales 全量词典覆盖（★ 2026-09-20 全站十语种）', () => {
  for (const [lang, dict] of ALL) {
    it(`${lang}：全量 ${ALL_KEYS.length} 键逐键覆盖、无越界键、占位符逐键一致、无空值`, () => {
      expect(Object.keys(dict).length).toBe(ALL_KEYS.length)
      for (const k of Object.keys(dict)) expect(ALLK.has(k), `越界键 ${k}`).toBe(true)
      for (const k of ALL_KEYS) {
        expect(dict[k], `缺键 ${k}`).toBeTruthy()
        expect(toks(dict[k] ?? ''), `占位符漂移 ${k}`).toBe(toks(EN[k] ?? ''))
      }
    })
  }
  for (const [lang, dict] of CJK_BAN) {
    it(`${lang}：值内零汉字残留（漏译扫描；源文自带汉字如品牌名「极石」豁免）`, () => {
      const bad = Object.entries(dict)
        .filter(([k, v]) => [...v].some((c) => /[一-鿿㐀-䶿]/.test(c) && !EN[k].includes(c)))
        .map(([k]) => k)
      expect(bad, `含汉字: ${bad.slice(0, 5)}`).toEqual([])
    })
  }
  it('zh_hant：值内零简体残留（高频「仅简体出现」字表扫描）', () => {
    // 字表经 OpenCC s2t 逐字验真：只收「繁体必有另一字形」的简体专属字（关→關、录→錄…），
    // 繁简同形字（服/注/部/容…）刻意不进表，避免误伤（2026-09-20 两连败教训）
    const SIMPLE_ONLY = '关开与专业设录销务网络项隐标题内页选择认审质检术语记忆译运营门户积额价账单发馈处记录历统计报标签关联规则条逻辑错误确驳预范围设备竞争对赋线转档为闭'
    const bad = Object.entries(zhHant).filter(([, v]) => [...v].some((c) => SIMPLE_ONLY.includes(c))).map(([k]) => k)
    expect(bad, `疑似简体残留: ${bad.slice(0, 5)}`).toEqual([])
  })
  it('回退链：全链落空才回显 key（★ 全站十语种后不再有「未翻键落英文」的日常场景，兜底行为仍在）', () => {
    setLang('ru')
    expect(t('no.such.key.at.all')).toBe('no.such.key.at.all')
    // lang→en→zh 链在词典缺键时仍生效：临时语种视角下越界 key 一律走 en
    setLang('th')
    expect(t('chat.send')).toBeTruthy() // 全量覆盖后任何键都取得到值
  })
  it('全量词典已翻键：切语种即生效（抽查每语种首/中/末三键）', () => {
    for (const [lang, dict] of ALL) {
      setLang(lang)
      const picks = [ALL_KEYS[0], ALL_KEYS[Math.floor(ALL_KEYS.length / 2)], ALL_KEYS[ALL_KEYS.length - 1]]
      for (const k of picks) expect(t(k), `${lang} 取词 ${k}`).toBe(dict[k])
    }
  })
  it('持久化与未知值回落：setLang 写 app_lang；初始读到未知代码走浏览器检测', async () => {
    // ① setLang 必须同步落盘（外国用户切语种后刷新不丢）
    setLang('ar')
    expect(getLang()).toBe('ar')
    expect(localStorage.getItem('app_lang')).toBe('ar')
    // ② 旧版本/手工改出的野值不得让全站取词踩空：重导入模块模拟冷启动，
    //    ★ 2026-09-20 起回落口径从「一律 zh」改为「按浏览器语言检测」（此处 navigator=vi → en）
    setLang('zh')
    localStorage.setItem('app_lang', 'klingon')
    vi.stubGlobal('navigator', { language: 'vi-VN', languages: ['vi-VN'] })
    vi.resetModules()
    const fresh = await import('./index')
    expect(fresh.getLang()).toBe('en') // 语种表外语言回落英文（land.*/长尾键英文完整）
    localStorage.setItem('app_lang', 'zh')
    vi.unstubAllGlobals()
  })
  it('★ 浏览器语言自动检测（反馈④外国人可读）：语种表内直达、中文分简繁、表外回落 en', async () => {
    // 每组：mock navigator → 冷启动期望语种（app_lang 留空 = 首次访问场景）
    const cases: Array<[string[], Lang]> = [
      [['fr-FR', 'en-US'], 'fr'], // 取第一优先语言，哪怕次选是英语
      [['zh-TW'], 'zh_hant'], // 台湾/港澳/繁体标记 → 繁中
      [['zh-HK', 'zh-CN'], 'zh_hant'],
      [['zh-CN', 'en'], 'zh'], // 简中浏览器仍是简中
      [['ja'], 'ja'],
      [['vi-VN'], 'en'], // 语种表外 → 英文回退
      [[''], 'en'], // 空语言标 → 英文
    ]
    for (const [langs, want] of cases) {
      localStorage.removeItem('app_lang')
      vi.stubGlobal('navigator', { language: langs[0] || '', languages: langs })
      vi.resetModules()
      const fresh = await import('./index')
      expect(fresh.getLang(), `navigator.languages=${langs.join(',')} → ${want}`).toBe(want)
    }
    vi.unstubAllGlobals()
    localStorage.setItem('app_lang', 'zh') // 复位测试默认语种，后续用例不受影响
  })
})
