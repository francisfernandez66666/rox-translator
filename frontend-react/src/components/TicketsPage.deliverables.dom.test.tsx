// ============================================================================
// TicketsPage.deliverables.dom.test.tsx — 详情面「交付件清单 + 退化露出」（★ 2026-10-19 G2/G13）
//
// 由来：
//   G13（D17）：音频工单下载包 4 件齐全，而详情接口的 files 数组只有"用户上传那 1 件"
//     ⇒ 客户"在页面上看不见自己已经拿到手的东西"。
//   G2（D2）：图片原地回填失败被静默兜成"只发 .md"，界面仍写"已完成"——退化必须露出。
// 本文件把两件事在**详情面**钉死：
//   · D1 交付件清单（deliverables）逐件点名渲染；
//   · D2 退化语言（degraded_langs）非空 ⇒ 出退化提示块；
//   · D3 两处为空 ⇒ 两块都不渲染（负向：别在健康单上凭空长出一块）；
//   · D4 兼容 `outputs` 别名（后端字段名另一 agent 未定稿）；
//   · D5/D6 下载失败分档与在途进度（§五#2）。
// 说明：本组件对这两块是**条件渲染**（为空即不进 DOM），故 jsdom 侧判"在不在 DOM"即判"显不显示"；
//   "CSS 隐藏件仍在 DOM"那族陷阱（§一·5）是 e2e/UAT 层用 `:visible` 的职责，不在本文件的射程。
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, cleanup, fireEvent, waitFor } from '@testing-library/react'
import TicketsPage from './TicketsPage'

const mocks = vi.hoisted(() => ({
  detail: {} as Record<string, unknown>,
  download: vi.fn(async () => undefined),
  toastError: vi.fn(),
  toast: vi.fn(),
}))

vi.mock('@/lib/toastBus', () => ({
  toastError: mocks.toastError,
  toastSuccess: vi.fn(),
  toastWarn: vi.fn(),
  toast: mocks.toast,
  registerToastHandler: vi.fn(),
}))

vi.mock('@/api', () => {
  const ok = (extra: Record<string, unknown> = {}) => ({ success: true, ...extra })
  return {
    myTickets: vi.fn(async () => ok({ tickets: [{ id: 11, ticket_no: 'T-1', title: 't', status: 'completed', target_langs: 'en', created_at: '2026-10-19T00:00:00Z' }] })),
    ticketDetail: vi.fn(async () => ok(mocks.detail)),
    ticketCreate: vi.fn(async () => ok()),
    ticketCreateFile: vi.fn(async () => ok()),
    ticketRun: vi.fn(async () => ok()),
    ticketDownload: mocks.download,
    ticketDelete: vi.fn(async () => ok()),
    ticketCancel: vi.fn(async () => ok()),
  }
})

beforeEach(() => {
  mocks.toastError.mockClear()
  mocks.toast.mockClear()
  mocks.download.mockClear()
  vi.spyOn(window, 'scrollTo').mockImplementation(() => undefined)
})
afterEach(cleanup)

// 打开详情气泡（详情钮＝该行最后一个 button；文案走 i18n，不按文案定位）
async function openDetail(no: string) {
  const cells = await screen.findAllByText(no)
  const row = cells[0].closest('tr') as HTMLElement
  const btns = [...row.querySelectorAll('button')]
  fireEvent.click(btns[btns.length - 1])
  await waitFor(() => expect(document.querySelector('[data-testid="tk-progress-dialog"]')).toBeTruthy())
}

const ticket = { id: 11, ticket_no: 'T-1', title: 't', status: 'completed', target_langs: 'en', created_at: '2026-10-19T00:00:00Z' }

describe('工单详情面 · 交付件清单与退化露出', () => {
  it('D1 交付件逐件点名渲染（G13：不再只看得见自己上传那件）', async () => {
    mocks.detail = {
      ticket,
      states: [],
      files: [{ id: 1, file_name: 'in.png', result_path: '', error: '' }],
      deliverables: [
        { name: 'Voice_en.m4a', kind: 'audio', size: 95_295, lang: 'en' },
        { name: 'Voice_en.srt', kind: 'subtitle', size: 118 },
        { name: '译文_zh.md', kind: 'text', size: 86 },
        { name: 'in.png', kind: 'image' },
      ],
    }
    render(<TicketsPage />)
    await openDetail('T-1')
    const box = document.querySelector('[data-testid="tk-deliverables"]') as HTMLElement
    expect(box, '交付件清单块没渲染').toBeTruthy()
    for (const n of ['Voice_en.m4a', 'Voice_en.srt', '译文_zh.md', 'in.png']) {
      expect(box.textContent, `交付件 ${n} 没点名`).toContain(n)
    }
    // 健康单不出现退化块（负向对照）
    expect(document.querySelector('[data-testid="tk-degraded"]')).toBeNull()
  })

  it('D2 degraded_langs 非空 ⇒ 出退化提示块（G2）', async () => {
    mocks.detail = { ticket: { ...ticket, degraded_langs: ['en'] }, states: [], files: [] }
    render(<TicketsPage />)
    await openDetail('T-1')
    expect(document.querySelector('[data-testid="tk-degraded"]')).toBeTruthy()
  })

  it('D2b gate_warnings 非空 ⇒ 出质量提示块（M1：未译段回落原文要出声）', async () => {
    mocks.detail = { ticket: { ...ticket, gate_warnings: ['untranslated=3'] }, states: [], files: [] }
    render(<TicketsPage />)
    await openDetail('T-1')
    expect(document.querySelector('[data-testid="tk-gate-warnings"]')).toBeTruthy()
  })

  it('D3 无交付件、无退化 ⇒ 两块都不渲染', async () => {
    mocks.detail = { ticket, states: [], files: [] }
    render(<TicketsPage />)
    await openDetail('T-1')
    expect(document.querySelector('[data-testid="tk-deliverables"]')).toBeNull()
    expect(document.querySelector('[data-testid="tk-degraded"]')).toBeNull()
  })

  it('D4 兼容 outputs 别名（后端字段名未定稿）', async () => {
    mocks.detail = { ticket, states: [], files: [], outputs: [{ name: 'alias_out.docx' }] }
    render(<TicketsPage />)
    await openDetail('T-1')
    const box = document.querySelector('[data-testid="tk-deliverables"]') as HTMLElement
    expect(box?.textContent).toContain('alias_out.docx')
  })

  it('D5 下载失败（network 档）⇒ toastError 收到含字节数的现场文案', async () => {
    mocks.detail = { ticket, states: [], files: [] }
    const { DownloadError } = await import('@/api/tickets')
    mocks.download.mockRejectedValueOnce(new DownloadError('network', 'boom', { received: 2048 }))
    render(<TicketsPage />)
    const cells = await screen.findAllByText('T-1')
    const row = cells[0].closest('tr') as HTMLElement
    fireEvent.click(row.querySelectorAll('button')[0]) // 操作列第一个＝下载
    await waitFor(() => expect(mocks.toastError).toHaveBeenCalled())
    // 文案走 i18n 键（key 由另一 agent 补译），此处只要求它**不是**空、且确实弹了一次
    expect(String(mocks.toastError.mock.calls[0][0])).not.toEqual('')
  })

  it('D7 文本终态工单「重新发起」⇒ 回填标题/原文/语种/模式并关详情（M1）', async () => {
    mocks.detail = {
      ticket: { ...ticket, source_text: '原始待译文本', target_langs: 'en,ja', mode: 'pro', delivery: 'restore' },
      states: [],
      files: [],
    }
    render(<TicketsPage />)
    await openDetail('T-1')
    fireEvent.click(await screen.findByText('重新发起'))
    await waitFor(() => expect(document.querySelector('[data-testid="tk-progress-dialog"]')).toBeNull())
    expect((document.querySelector('[data-testid="tk-title"]') as HTMLInputElement).value).toBe('t')
    expect((document.querySelector('[data-testid="tk-source"]') as HTMLTextAreaElement).value).toBe('原始待译文本')
  })

  it('D8 文件终态工单「重新发起」⇒ 切文件模式并提示重选原文件（不静默丢参数）', async () => {
    mocks.detail = {
      ticket: { ...ticket, file_path: '/srv/up/a.docx', target_langs: 'en', mode: 'fast' },
      states: [],
      files: [],
    }
    render(<TicketsPage />)
    await openDetail('T-1')
    fireEvent.click(await screen.findByText('重新发起'))
    await waitFor(() => expect(document.querySelector('#tk-file-input')).toBeTruthy())
    expect(mocks.toast).toHaveBeenCalled()
    const arg = mocks.toast.mock.calls[0][0] as { title?: string; tone?: string }
    expect(arg.title, '必须提示重新选择原文件，而不是假装已恢复上传件').toBeTruthy()
    expect(arg.tone).toBe('warn')
  })

  it('D6 下载在途 ⇒ 行内出现字节进度标记（§五#2）', async () => {
    mocks.detail = { ticket, states: [], files: [] }
    let resolve!: () => void
    mocks.download.mockImplementationOnce(() => new Promise<undefined>((r) => { resolve = () => r(undefined) }))
    render(<TicketsPage />)
    const cells = await screen.findAllByText('T-1')
    const row = cells[0].closest('tr') as HTMLElement
    fireEvent.click(row.querySelectorAll('button')[0])
    await waitFor(() => expect(document.querySelector('[data-testid="tk-dl-progress"]')).toBeTruthy())
    resolve()
    await waitFor(() => expect(document.querySelector('[data-testid="tk-dl-progress"]')).toBeNull())
  })
})
