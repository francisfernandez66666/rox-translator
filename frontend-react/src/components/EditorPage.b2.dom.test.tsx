// ============================================================================
// EditorPage.b2.dom.test.tsx — B2 编辑器计算收敛回归（2026-09-19）
// 锁定四条行为：
//   ① 术语高亮一次编译：元字符（C++ / .NET）转义正确、≤1 字词丢弃；
//   ② 非受控提交语义：键入不进 state（无「待保存」徽标），blur 且值有变化才计脏；
//   ③ 状态 select 受控即时计脏；
//   ④ 重新加载（loadSeq 换 key 重挂载）后，textarea 复位到服务端最新值。
// 运行：npx vitest run src/components/EditorPage.b2.dom.test.tsx
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, cleanup, fireEvent, waitFor } from '@testing-library/react'
import { setLang, t } from '@/i18n'

const mocks = vi.hoisted(() => ({
  getSegments: vi.fn(),
  getSegmentsByKey: vi.fn(),
  saveSegments: vi.fn(),
}))
vi.mock('@/api/tickets', () => ({
  getSegments: mocks.getSegments,
  getSegmentsByKey: mocks.getSegmentsByKey,
  saveSegments: mocks.saveSegments,
}))

import EditorPage from './EditorPage'
import { ToastProvider } from '@/ui/langcross/src'

// segs 最小分段载荷（与后端 EditorSegment 传输契约对齐）
const segs = () => ([
  { index: 0, source: '基于 C++ 与 .NET 的平台', target: 'A C++ and .NET based platform', edited_text: '', status: 'pending', note: '' },
  { index: 1, source: '第二段', target: 'Segment two', edited_text: '', status: 'pending', note: '' },
])
const okResp = (over: Record<string, unknown> = {}) => ({
  success: true, segments: segs(), terms: ['C++', '.NET', 'x'], type: 'text', langs: ['en'], lang: 'en', ...over,
})

const renderPage = () => render(<ToastProvider><EditorPage /></ToastProvider>)
const textareas = () => Array.from(document.querySelectorAll('textarea.lc-textarea')) as HTMLTextAreaElement[]

beforeEach(() => {
  cleanup()
  setLang('zh')
  mocks.getSegments.mockReset().mockResolvedValue(okResp() as never)
  mocks.getSegmentsByKey.mockReset()
  mocks.saveSegments.mockReset()
})

/** 用数字工单 ID 触发一次加载 */
async function loadTicket(container: HTMLElement) {
  const idInput = container.querySelector('input') as HTMLInputElement
  fireEvent.change(idInput, { target: { value: '42' } })
  // 按钮文案走 t() 查词条（tk.edLoad 词条即便调整也不影响本断言）
  const loadBtn = [...container.querySelectorAll('button')].find((b) => b.textContent?.includes(t('tk.edLoad')))
  expect(loadBtn).toBeTruthy()
  fireEvent.click(loadBtn!)
  await waitFor(() => expect(textareas().length).toBe(2))
}

describe('B2 编辑器计算收敛', () => {
  it('① 术语高亮：元字符转义命中、≤1字词不参与', async () => {
    const { container } = renderPage()
    await loadTicket(container)
    const marks = Array.from(container.querySelectorAll('mark')).map((m) => m.textContent)
    expect(marks).toContain('C++')
    expect(marks).toContain('.NET')
    expect(marks).not.toContain('x') // 单字词噪声，构建期即丢弃
  })

  it('② 键入不计脏、blur 且值变化才计脏；blur 无变化不计脏', async () => {
    const { container } = renderPage()
    await loadTicket(container)
    const ta = textareas()[0]
    fireEvent.change(ta, { target: { value: 'draft editing' } })
    expect(container.textContent).not.toContain('待保存') // 键入不进 state（旧版每键一次全表重渲染+计脏）
    fireEvent.blur(ta, { target: { value: 'draft editing' } })
    expect(container.textContent).toContain('待保存 1 段')
    // 改回原值再 blur：与初值等值即撤脏
    const ta2 = textareas()[0]
    fireEvent.change(ta2, { target: { value: 'A C++ and .NET based platform' } })
    fireEvent.blur(ta2, { target: { value: 'A C++ and .NET based platform' } })
    expect(container.textContent).not.toContain('待保存')
  })

  it('③ 状态 select 受控即时计脏', async () => {
    const { container } = renderPage()
    await loadTicket(container)
    const sel = container.querySelectorAll('select.lc-select')[0] as HTMLSelectElement
    fireEvent.change(sel, { target: { value: 'approved' } })
    expect(container.textContent).toContain('待保存 1 段')
  })

  it('④ 重新加载后非受控框复位到服务端值（loadSeq 重挂载）', async () => {
    const { container } = renderPage()
    await loadTicket(container)
    const ta = textareas()[0]
    fireEvent.change(ta, { target: { value: '手改未保存' } })
    fireEvent.blur(ta, { target: { value: '手改未保存' } })
    expect((textareas()[0] as HTMLTextAreaElement).value).toBe('手改未保存')
    // 再次加载（服务端未变）→ 本地未保存改动应被复位
    await loadTicket(container)
    await waitFor(() => expect((textareas()[0] as HTMLTextAreaElement).value).toBe('A C++ and .NET based platform'))
  })
})
