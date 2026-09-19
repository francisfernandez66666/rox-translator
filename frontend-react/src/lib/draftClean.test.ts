// ============================================================================
// lib/draftClean.test.ts — 初译草稿 <t> 契约清洗单测（★ B1 流式双态）
// 口径锚点：backend-go/internal/engine/postprocess.go 的 contractTagRe /
// extractContractTranslation。此处锁三类行为：
//   ① 无契约字符 → 原样透传；② 完整契约对 → 白名单提取、多块换行拼接；
//   ③ 流式中途的半截标签 → 剥碎片、保正文（保证界面上永远看不到 <t 闪现）。
// =============================================
import { describe, it, expect } from 'vitest'
import { cleanDraft } from './draftClean'

describe('cleanDraft（与后端 <t> 契约同口径）', () => {
  it('无契约字符：原样返回', () => {
    expect(cleanDraft('Hello world')).toBe('Hello world')
    expect(cleanDraft('')).toBe('')
  })

  it('单个完整契约对：只取标签内译文，标签外注释整体丢弃', () => {
    expect(cleanDraft('注：以下为译文\n<t>Hallo</t>\n术语对照：x→y')).toBe('Hallo')
  })

  it('多个契约块：trim 后按换行拼接（与 Go 侧 parts join 一致）', () => {
    expect(cleanDraft('<t>A1</t>\n说明\n<t>A2</t>')).toBe('A1\nA2')
  })

  it('容忍属性与大小写（对齐 (?is)<t\\b[^>]*>…</t\\s*>）', () => {
    expect(cleanDraft('<T note="x">译文一</T >')).toBe('译文一')
  })

  it('流式中途：已闭合开标签、译文在走 → 剥开标签留正文', () => {
    expect(cleanDraft('<t>Hallo w')).toBe('Hallo w')
  })

  it('流式中途：半截开标签碎片 → 整段尾部剥除', () => {
    expect(cleanDraft('Hallo\n<t')).toBe('Hallo')
    expect(cleanDraft('Hallo\n<t note="x')).toBe('Hallo')
  })

  it('流式中途：半截闭标签碎片 → 剥碎片留译文', () => {
    expect(cleanDraft('<t>Hallo</t')).toBe('Hallo')
    expect(cleanDraft('<t>Hallo</t ')).toBe('Hallo')
  })

  it('以 <t 开头的普通标签（如 <table>）不误伤：\\b 边界不匹配，原样透传', () => {
    expect(cleanDraft('<table>text')).toBe('<table>text')
  })
})
