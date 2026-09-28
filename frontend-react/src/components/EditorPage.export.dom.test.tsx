// ============================================================================
// EditorPage.export.dom.test.tsx — ★ D-4「导出译文」钮回归（2026-09-29）
// 缺陷因果链：后端回写导出（/api/tickets/segments/export ＋ /api/editor/export/download）
//   自 2026-08 就在，但对照编辑器界面上**没有任何入口**，只有 UAT 脚本 T56 在打——
//   审批人在线改过的修订于是带不进交付件，只能人工抄回原文重排。
//   本批（D-4）补钮，这条链最容易被改坏的是**可用性判据**而不是请求形态，所以四条都锁：
//   ① 无脏段时可点，且按「当前工单 ID ＋ 当前语种」发导出、拿到 download 再走下载；
//   ② 有脏段时**必须禁用**——后端读的是库里已落盘的 edited_text，
//      此时导出＝静默丢掉最新修订（F-44「修订在交付件里凭空消失」同族失真），
//      这比按钮少一个更贵，所以它是本用例的正身；
//   ③ 后端 success:false 出后端那句文案（不静默）；
//   ④ 下载环节抛错（魔数校验/越权）也必须有提示，不能「点了没反应」。
// 运行：npx vitest run src/components/EditorPage.export.dom.test.tsx
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, cleanup, fireEvent, waitFor } from '@testing-library/react'
import { setLang, t, tpl } from '@/i18n'

const mocks = vi.hoisted(() => ({
  getSegments: vi.fn(),
  getSegmentsByKey: vi.fn(),
  saveSegments: vi.fn(),
  segmentsExport: vi.fn(),
  editorExportFetch: vi.fn(),
}))
vi.mock('@/api/tickets', () => ({
  getSegments: mocks.getSegments,
  getSegmentsByKey: mocks.getSegmentsByKey,
  saveSegments: mocks.saveSegments,
  segmentsExport: mocks.segmentsExport,
  editorExportFetch: mocks.editorExportFetch,
}))

import EditorPage from './EditorPage'
import { ToastProvider } from '@/ui/langcross/src'

const segs = () => ([
  { index: 0, source: '第一段原文', target: 'Segment one', edited_text: '', status: 'pending', note: '' },
  { index: 1, source: '第二段原文', target: 'Segment two', edited_text: '', status: 'approved', note: '' },
])
const okResp = () => ({ success: true, segments: segs(), terms: [], type: 'text', langs: ['en'], lang: 'en' })

const renderPage = () => render(<ToastProvider><EditorPage /></ToastProvider>)
const buttons = () => Array.from(document.querySelectorAll('button')) as HTMLButtonElement[]
const btnOf = (label: string) => buttons().find((b) => b.textContent?.includes(label))
const textareas = () => Array.from(document.querySelectorAll('textarea.lc-textarea')) as HTMLTextAreaElement[]
const toastTexts = () => Array.from(document.querySelectorAll('.lc-toast__title')).map((n) => n.textContent || '')

/** 挂载页面并加载工单：输入数字 ID → 点「加载」→ 等两段渲染出来
    （返回 container 供局部查询；语种下拉留默认 'en'，与导出断言里的语种同源） */
async function loadTicket() {
  const { container } = renderPage()
  const idInput = container.querySelector('input') as HTMLInputElement
  fireEvent.change(idInput, { target: { value: '42' } })
  const loadBtn = btnOf(t('tk.edLoad'))
  expect(loadBtn).toBeTruthy()
  fireEvent.click(loadBtn!)
  await waitFor(() => expect(textareas().length).toBe(2))
}

/** 把第 0 段改成「脏」：键入不进 state（非受控），blur 且值变化才计脏（B2 语义） */
function makeDirty() {
  const ta = textareas()[0]
  fireEvent.change(ta, { target: { value: '改过的译文' } })
  fireEvent.blur(ta, { target: { value: '改过的译文' } })
}

beforeEach(() => {
  cleanup()
  setLang('zh')
  mocks.getSegments.mockReset().mockResolvedValue(okResp() as never)
  mocks.getSegmentsByKey.mockReset()
  mocks.saveSegments.mockReset()
  mocks.segmentsExport.mockReset().mockResolvedValue({ success: true, download: '/api/editor/export/download?file=a.docx' } as never)
  mocks.editorExportFetch.mockReset().mockResolvedValue('a_en_edited_1.docx' as never)
})

describe('D-4 对照编辑器「导出译文」钮', () => {
  it('① 工具条有导出钮；无脏段时可点，按当前 ID＋语种发导出并接续下载', async () => {
    await loadTicket()
    const exportBtn = btnOf(t('tk.edExport'))
    expect(exportBtn, '工具条必须有「导出译文」钮（后端能力早已存在，缺的是入口）').toBeTruthy()
    expect(exportBtn!.disabled, '无脏段时不该禁用').toBe(false)
    fireEvent.click(exportBtn!)
    await waitFor(() => expect(mocks.segmentsExport).toHaveBeenCalledWith(42, 'en'))
    await waitFor(() => expect(mocks.editorExportFetch).toHaveBeenCalledWith('/api/editor/export/download?file=a.docx'))
    // 成功提示带落盘文件名（用户要确认下的是哪一份），整句等值于 tpl 产物
    await waitFor(() => expect(toastTexts()).toContain(tpl('tk.edExportStarted', { name: 'a_en_edited_1.docx' })))
  })

  it('② 有未保存修订时钮必须禁用（导出会丢最新修订，比没按钮更贵）', async () => {
    await loadTicket()
    expect(btnOf(t('tk.edExport'))!.disabled).toBe(false)
    makeDirty()
    const exportBtn = btnOf(t('tk.edExport'))!
    expect(exportBtn.disabled, '脏段存在 ⇒ 导出钮禁用，提示先保存').toBe(true)
    // 反证：强行点它也不能发出导出请求（disabled 之外不留第二条触发路径）
    fireEvent.click(exportBtn)
    await new Promise((r) => setTimeout(r, 0))
    expect(mocks.segmentsExport).not.toHaveBeenCalled()
  })

  it('③ 未加载工单（无分段）时钮禁用', () => {
    renderPage()
    expect(btnOf(t('tk.edExport'))!.disabled).toBe(true)
  })

  it('④ 后端业务失败：出后端那句文案，且不发下载请求', async () => {
    await loadTicket()
    mocks.segmentsExport.mockResolvedValueOnce({ success: false, message: '回写仅支持 docx 结果文件（当前结果非 docx）' } as never)
    fireEvent.click(btnOf(t('tk.edExport'))!)
    await waitFor(() => expect(toastTexts().join('|')).toContain('回写仅支持 docx 结果文件'))
    expect(mocks.editorExportFetch).not.toHaveBeenCalled()
  })

  it('⑤ 下载环节抛错（魔数校验/越权）也要有提示，不能「点了没反应」', async () => {
    await loadTicket()
    mocks.editorExportFetch.mockRejectedValueOnce(new Error('导出产物校验失败（不是 docx 文件）'))
    fireEvent.click(btnOf(t('tk.edExport'))!)
    await waitFor(() => expect(mocks.editorExportFetch).toHaveBeenCalled())
    await waitFor(() => expect(toastTexts().join('|')).toContain('导出产物校验失败（不是 docx 文件）'))
  })
})
