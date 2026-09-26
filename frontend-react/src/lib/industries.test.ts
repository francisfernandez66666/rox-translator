// ============================================================================
// lib/industries.test.ts — 行业展示名的语种等值锁（★ 2026-09-26 〇-U 批 I-8 · 缺陷 F-61）
// ----------------------------------------------------------------------------
// 复现的真实问题：客户在繁体中文界面看到整排简体行业 chip（「汽车/房产/跨境独立站…」）。
// 根因是判据 `lang.startsWith('zh') ? m.zh : m.en` 把 zh_hant 一并当成简体，
// 而 IndustryMeta 里根本没有繁体词条。
// 本文件按 AGENTS §一·5「等值锁 + 负向清零」的口径钉住三件事：
//   ① 简体/繁体/英文三种界面各自**等于**交付词条（不是「更繁一点」这类单向锁）；
//   ② 繁体输出里不得再出现任何简体专用字形（负向清零＝抓「判据被改回 startsWith」的回归）；
//   ③ 下拉的 **value 仍必须是简体名**——后端按 code/简体名存储，label 翻繁体系不动存储契约，
//      谁把 value 一起翻成繁体，这里立刻红（那是数据面事故，不是显示问题）。
// ============================================================================
import { describe, expect, it } from 'vitest'

import { INDUSTRY_META, industryCodeOf, industryName, industryOptions } from './industries'
import type { Lang } from '@/i18n'

/** 简体专用字形（繁体的对应字形不同）：繁体输出中出现任意一个即为回归。
 *  ⚠️ 表内只放「繁简字形不同」的字：媒/立/教/育 这类两形同字的字不能进来，
 *  否则负向锁会恒红（首版误列「媒」，自媒體 本身就是繁体写法）。 */
const SIMPLIFIED_ONLY = ['业', '车', '产', '务', '学', '独', '庆', '电', '内', '创', '体']

const CODES = Object.keys(INDUSTRY_META)

describe('industryName — 按界面语种精确取词（F-61）', () => {
  it('简体界面取简体词条（等值锁，防「为了繁体把中文档整体翻掉」）', () => {
    expect(industryName('auto', 'zh')).toBe('汽车')
    expect(industryName('general', 'zh')).toBe('通用行业')
  })

  it('繁体界面取繁体词条，逐个人工词条等值', () => {
    expect(industryName('auto', 'zh_hant')).toBe('汽車')
    expect(industryName('general', 'zh_hant')).toBe('通用行業')
    expect(industryName('realestate', 'zh_hant')).toBe('房產/裝修')
    expect(industryName('education', 'zh_hant')).toBe('教育/留學')
    expect(industryName('media', 'zh_hant')).toBe('自媒體/內容創作')
  })

  it('其余十语种仍落英文（#23 的既定取舍：行业表没有 12 份词条）', () => {
    for (const l of ['ja', 'ko', 'ru', 'fr', 'de', 'es', 'pt', 'ar', 'th'] as Lang[]) {
      expect(industryName('ecommerce', l)).toBe('Cross-border E-commerce')
    }
  })

  it('未知 code 透传、空串回空（表单/表格兜底不变）', () => {
    expect(industryName('', 'zh_hant')).toBe('')
    expect(industryName('fintech', 'zh')).toBe('fintech')
  })

  it('★ 负向清零：繁体输出不得含任何简体字形，且必须真的不同于简体', () => {
    for (const code of CODES) {
      const hant = industryName(code, 'zh_hant')
      const hans = INDUSTRY_META[code].zh
      for (const ch of SIMPLIFIED_ONLY) {
        expect(hant, `行业 ${code} 的繁体词条含简体字 ${ch}`).not.toContain(ch)
      }
      // 词条相同＝这一行根本没做繁体（F-61 的原始形态），要红在这里而不是红在人眼
      expect(hant, `行业 ${code} 的 zhHant 与简体同名，繁体界面等于没改`).not.toBe(hans)
    }
  })
})

describe('industryOptions — 展示翻繁、存储契约不动（F-61 的另一半）', () => {
  it('value 恒为简体名（后端存储按 code/简体名，industryCodeOf 按此反查）', () => {
    for (const o of industryOptions('zh_hant')) {
      expect(o.value).toBe(INDUSTRY_META[o.code].zh)
      expect(o.label).toBe(INDUSTRY_META[o.code].zhHant)
    }
  })

  it('label 随界面语种、value 不随语种漂移（同一 code 在三种语言下 value 相同）', () => {
    for (const code of CODES) {
      const vals = (['zh', 'zh_hant', 'en'] as Lang[]).map((l) => industryOptions(l).find((o) => o.code === code)?.value)
      expect(new Set(vals).size).toBe(1)
      expect(vals[0]).toBe(INDUSTRY_META[code].zh)
    }
  })

  it('简体/繁体下拉的可见文案互不相同（等值于各自的词条档）', () => {
    expect(industryOptions('zh').map((o) => o.label)).toContain('汽车')
    expect(industryOptions('zh_hant').map((o) => o.label)).toContain('汽車')
    expect(industryOptions('zh_hant').map((o) => o.label)).not.toContain('汽车')
  })

  it('反查仍按简体名命中 code；繁体串透传（label 从不作提交值）', () => {
    expect(industryCodeOf('汽车')).toBe('auto')
    expect(industryCodeOf('')).toBe('')
    expect(industryCodeOf('auto')).toBe('auto')
  })
})
