// ============================================================================
// EditorPage.b5.dom.test.tsx — ★ B5 编辑器虚拟化回归（2026-09-19，方案 C1）
// 锁定的行为契约：
//   ① 大表（>50 段）进 virtuoso 窗口列表：DOM 挂载行严格少于总段数（旧版 578 段
//      全量挂 578 个 textarea 的形态不再出现）；
//   ② 小表（≤50 段）保持直渲染全行在场（B2 编辑/测试语义不变、零虚拟化开销）；
//   ③ virtuoso 容器在场（data-virtuoso 根节点）。
// jsdom 无 ResizeObserver（react-virtuoso 挂载即 new），本文件先补最小桩。
// 运行：npx vitest run src/components/EditorPage.b5.dom.test.tsx
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, beforeEach, vi } from 'vitest'

// ResizeObserver 最小桩：jsdom 缺失；observe/unobserve/disconnect 空实现即可让
// virtuoso 完成挂载（零高度环境下只按估算视口出少量行，正好用于「窗口化生效」断言）
class ROStub {
  observe() {} unobserve() {} disconnect() {}
}
;(globalThis as unknown as Record<string, unknown>).ResizeObserver = ROStub

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

// mkSegs 造 n 段最小载荷
const mkSegs = (n: number) => Array.from({ length: n }, (_, i) => ({
  index: i, source: `源文段落 ${i}`, target: `Translation ${i}`, edited_text: '', status: 'pending', note: '',
}))
const resp = (segs: ReturnType<typeof mkSegs>) => ({
  success: true, segments: segs, terms: [], type: 'text', langs: ['en'], lang: 'en',
})

const renderPage = () => render(<ToastProvider><EditorPage /></ToastProvider>)
const taCount = () => document.querySelectorAll('textarea.lc-textarea').length

async function loadTicket(container: HTMLElement, n: number) {
  mocks.getSegments.mockResolvedValue(resp(mkSegs(n)) as never)
  const idInput = container.querySelector('input') as HTMLInputElement
  fireEvent.change(idInput, { target: { value: '42' } })
  const loadBtn = [...container.querySelectorAll('button')].find((b) => b.textContent?.includes(t('tk.edLoad')))
  fireEvent.click(loadBtn!)
  await waitFor(() => expect(mocks.getSegments).toHaveBeenCalled())
}

beforeEach(() => { cleanup(); setLang('zh'); mocks.getSegments.mockReset(); mocks.getSegmentsByKey.mockReset(); mocks.saveSegments.mockReset() })

describe('B5 编辑器虚拟化', () => {
  it('①③ 120 段大表：virtuoso 在场且挂载行收敛（远小于总段数）', async () => {
    const { container } = renderPage()
    await loadTicket(container, 120)
    // 注意：v4 标记属性是 data-virtuoso-scroller（不是 data-virtuoso），item 容器带 data-testid=virtuoso-item-list
    await waitFor(() => expect(container.querySelector('[data-virtuoso-scroller]')).toBeTruthy())
    await waitFor(() => expect(taCount()).toBeGreaterThan(0))
    expect(taCount()).toBeLessThan(120) // 窗口化生效：不再全量挂 textarea
    expect(container.querySelector('[data-testid="virtuoso-item-list"]')).toBeTruthy()
  })

  it('② 40 段小表：直渲染全行在场，不出现 virtuoso 容器', async () => {
    const { container } = renderPage()
    await loadTicket(container, 40)
    await waitFor(() => expect(taCount()).toBe(40))
    expect(container.querySelector('[data-virtuoso-scroller]')).toBeNull()
  })
})
