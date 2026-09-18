// ============================================================================
// TicketsPage.mode_override.dom.test.tsx — 工单「模式旁路」审计轨迹透出测试
// （★ 批次3 P0-5，2026-09-18）
// 断言：orchestrator 落库的 mode_override 轨迹行在进度抽屉里渲染为本地化标签
// 「模式旁路」而非裸 key——复现此前的缺陷（STEP_KEYS 无映射时 stepName 回退原始
// key，中文界面出现英文内部标识，用户无法理解该步含义）。
// 运行：npx vitest run src/components/TicketsPage.mode_override.dom.test.tsx
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, cleanup, fireEvent } from '@testing-library/react'

// 带 mode_override 轨迹的工单详情 mock（与 qa.dom 测试同款最小接口集）
vi.mock('@/api', async () => {
  const ok = (extra: Record<string, unknown> = {}) => ({ success: true, ...extra })
  const ticket = {
    id: 9, ticket_no: 'T-MODE-9', title: '精简模式工单', status: 'completed',
    target_langs: 'en', qa_errors: 0, qa_warnings: 0, quality_flagged: 0,
    created_at: '2026-09-18T00:00:00Z',
  }
  return {
    myTickets: vi.fn(async () => ok({ tickets: [ticket] })),
    ticketDetail: vi.fn(async () => ok({
      ticket,
      states: [
        { id: 1, step: 'file_extract', status: 'success', note: '' },
        // 后端 SetTicketState(id,"mode_override","success",{bypass:"fast+api_task"}) 的落库行
        { id: 2, step: 'mode_override', status: 'success', note: '{"bypass":"fast+api_task"}' },
      ],
      files: [],
      progress: 100,
    })),
    ticketCreate: vi.fn(async () => ok()),
    ticketCreateFile: vi.fn(async () => ok()),
    ticketRun: vi.fn(async () => ok()),
    ticketDownload: vi.fn(async () => undefined),
    ticketDelete: vi.fn(async () => ok()),
    ticketCancel: vi.fn(async () => ok()),
    createFeedback: vi.fn(async () => ok()),
  }
})

beforeEach(() => { cleanup() })

describe('工单模式旁路审计轨迹（P0-5）', () => {
  it('进度抽屉把 mode_override 步骤渲染为中文标签「模式旁路」而非裸 key', async () => {
    const TicketsPage = (await import('./TicketsPage')).default
    render(<TicketsPage />)
    await vi.waitFor(() => { expect(screen.getAllByText('进度').length).toBeGreaterThan(0) })
    fireEvent.click(screen.getAllByText('进度')[0])
    // 已映射步骤正常本地化
    await vi.waitFor(() => { expect(screen.getAllByText(/解析提取/).length).toBeGreaterThan(0) })
    // ★ 核心断言：新轨迹行有标签（若 STEP_KEYS 缺映射会显示 "mode_override" 裸 key）
    expect(screen.getAllByText('模式旁路').length).toBe(1)
    expect(screen.queryByText('mode_override')).toBeNull()
  })
})
