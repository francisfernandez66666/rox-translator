// ============================================================================
// MessageBubble.segments.dom.test.tsx — ★ B3（方案 A2）逐段上屏气泡回归（2026-09-19）
// 锁定的行为契约：
//   ① 有段落桶即渲染实时段落区，行按首次出现段序号升序、#N 从 1 起；
//   ② final/gated 行带 --final 提亮态；draft 行弱样式；
//   ③ sealed 徽章与「实时段落」互斥切换，计数走 segArrivedFmt；
//   ④ placeholder 行不展示内容、只标「敏感内容已拦截」；
//   ⑤ segmentsAborted → 顶部「非交付物」警示横幅（不做假成功）；
//   ⑥ done 拿到附件（files）后段落区整体让位下载卡；空 rows 桶不渲染；
//   ⑦ 超过 SEG_ROW_CAP(120) 只保留最近 120 行并给出截断提示。
// 运行：npx vitest run src/components/MessageBubble.segments.dom.test.tsx
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, cleanup } from '@testing-library/react'
import { setLang } from '@/i18n'
import type { ChatMessage, FileSegBucket } from '@/types'

// '@/api' 整体替换：段落区不触网（附件 blob 仅在 files 场景用到，本测试不点开下载）
vi.mock('@/api', () => ({ API_BASE: '', getAuthToken: () => '' }))

import MessageBubble from './MessageBubble'

// mkMsg 构造最小助手消息：只铺 B3 判定所需字段
const mkMsg = (over: Partial<ChatMessage> = {}): ChatMessage => ({
  id: 'm1', role: 'assistant', content: '', timestamp: Date.now(), ...over,
})

// bucket 快速造语言桶：rows 支持 (index → [text, final, placeholder]) 简写
function bucket(rows: Record<number, [string, boolean?, boolean?]>, sealed = false): FileSegBucket {
  const r: FileSegBucket['rows'] = {}
  for (const [k, v] of Object.entries(rows)) r[Number(k)] = { text: v[0], final: !!v[1], placeholder: !!v[2] }
  return { sealed, rows: r }
}

beforeEach(() => { cleanup(); setLang('zh') })

describe('B3 逐段上屏 · 段落区渲染契约', () => {
  it('①②③ 行按段序号升序 + #N 标号 + final 提亮 + 未定稿「实时段落」徽章与计数', () => {
    const { container, queryByText } = render(
      <MessageBubble message={mkMsg({
        progress: { step: '初翻|英语', percent: 45 },
        segments: { en: bucket({ 2: ['第三段译文', true], 0: ['第一段草稿'], 1: ['第二段译文', true] }) },
      })} />,
    )
    const area = container.querySelector('.file-segs')
    expect(area).toBeTruthy()
    const rows = container.querySelectorAll('.file-seg-row')
    expect(rows.length).toBe(3)
    // 乱序录入仍按段序号升序上屏
    expect(rows[0].textContent).toContain('#1')
    expect(rows[0].textContent).toContain('第一段草稿')
    expect(rows[2].textContent).toContain('#3')
    // draft 行无 --final，final 行提亮
    expect(rows[0].className).not.toContain('file-seg-row--final')
    expect(rows[1].className).toContain('file-seg-row--final')
    expect(queryByText('实时段落')).toBeTruthy()
    expect(queryByText('已定稿')).toBeNull()
    expect(queryByText('已上屏 3 段')).toBeTruthy()
    // 与三关量尺共存（量尺管总进度、段落区管翻到哪儿了）
    expect(container.querySelector('.progress-area')).toBeTruthy()
  })

  it('③ sealed 后徽章切「已定稿」', () => {
    const { queryByText } = render(
      <MessageBubble message={mkMsg({ segments: { en: bucket({ 0: ['x'] }, true) } })} />,
    )
    expect(queryByText('已定稿')).toBeTruthy()
    expect(queryByText('实时段落')).toBeNull()
  })

  it('④ placeholder 行隐藏内容、只标「敏感内容已拦截」', () => {
    const { container, queryByText } = render(
      <MessageBubble message={mkMsg({
        segments: { en: bucket({ 0: ['正常段落'], 1: ['绝密原文', true, true] }) },
      })} />,
    )
    expect(queryByText('敏感内容已拦截')).toBeTruthy()
    expect(queryByText('绝密原文')).toBeNull()
    expect(container.querySelectorAll('.file-seg-row').length).toBe(2)
  })

  it('⑤ segmentsAborted：顶部挂「非交付物」警示横幅', () => {
    const { getByTestId, queryByText } = render(
      <MessageBubble message={mkMsg({
        segments: { en: bucket({ 0: ['半成品段落'] }) },
        segmentsAborted: true,
      })} />,
    )
    expect(getByTestId('file-segs-warn')).toBeTruthy()
    expect(queryByText('翻译已中断，以上段落不是最终交付物')).toBeTruthy()
  })

  it('⑥ done 落定（有附件产物）后段落区让位下载卡；空 rows 桶不渲染', () => {
    const { container: c1 } = render(
      <MessageBubble message={mkMsg({
        segments: { en: bucket({ 0: ['x'] }, true) },
        files: ['/out/result_en.docx'],
      })} />,
    )
    expect(c1.querySelector('.file-segs')).toBeNull()
    expect(c1.querySelector('.download-card')).toBeTruthy()

    const { container: c2 } = render(
      <MessageBubble message={mkMsg({ segments: { en: bucket({}) } })} />,
    )
    expect(c2.querySelector('.file-segs')).toBeNull()
  })

  it('⑦ 超 120 行只留最近 120 段并给截断提示', () => {
    const rows: Record<number, [string]> = {}
    for (let i = 0; i < 130; i++) rows[i] = [`段落 ${i}`]
    const { container, queryByText } = render(
      <MessageBubble message={mkMsg({ segments: { en: bucket(rows) } })} />,
    )
    const shown = container.querySelectorAll('.file-seg-row')
    expect(shown.length).toBe(120)
    // 保留的是尾部：首行即 #11（下标 10）
    expect(shown[0].textContent).toContain('#11')
    expect(queryByText('段落 9')).toBeNull()
    expect(queryByText('仅显示最近 120 段')).toBeTruthy()
    // 计数仍是全量 130
    expect(queryByText('已上屏 130 段')).toBeTruthy()
  })
})
