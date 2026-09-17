// ============================================================================
// TicketsPage.qa.dom.test.tsx — 工单页质检透出组件测试（★ 改造 4/5，2026-09-17）
// 覆盖：
//   ① 列表行质检徽标三分支——error 红「N 项错误」/ 仅 warning 黄「N 项提示」/ quality_flagged 橙「质检存疑」；
//   ② 无质检数据工单不渲染任何徽标（不误标）；
//   ③ 详情抽屉「质检报告」区块——汇总行 + Issues 明细（语言/规则/级别/说明）+ 各语言评估分。
// 背景：此前 qa_report / EvalScores 前端全仓零透出，付费用户仅下载 xlsx 才能看到质检结果。
// 运行：npx vitest run（文件级 @vitest-environment jsdom）
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, cleanup, fireEvent } from '@testing-library/react'
import TicketsPage from './TicketsPage'

// ---- '@/api' 最小 mock（TicketsPage 的具名导入全集） ----
vi.mock('@/api', () => {
  const ok = (extra: Record<string, unknown> = {}) => ({ success: true, ...extra })
  const tickets = [
    // ① error + warning + 评估不达标 → 三种徽标齐出
    {
      id: 1, ticket_no: 'T-QA-1', title: '有错误工单', status: 'completed', target_langs: 'en',
      qa_errors: 3, qa_warnings: 1, quality_flagged: 1, created_at: '2026-09-17T00:00:00Z',
    },
    // ② 仅 warning → 只出黄徽标
    {
      id: 2, ticket_no: 'T-QA-2', title: '仅提示工单', status: 'completed', target_langs: 'en',
      qa_errors: 0, qa_warnings: 2, quality_flagged: 0, created_at: '2026-09-17T00:00:00Z',
    },
    // ③ 全干净 → 无徽标
    {
      id: 3, ticket_no: 'T-QA-3', title: '干净工单', status: 'completed', target_langs: 'en',
      qa_errors: 0, qa_warnings: 0, quality_flagged: 0, created_at: '2026-09-17T00:00:00Z',
    },
  ]
  return {
    myTickets: vi.fn(async () => ok({ tickets })),
    ticketDetail: vi.fn(async () => ok({
      ticket: tickets[0],
      states: [],
      files: [],
      progress: 100,
      quality: {
        qa_report: {
          errors: 3, warnings: 1, pass: false,
          issues: [
            { lang: 'en', rule: 'number', level: 'error', detail: '数字 2026 在译文中丢失' },
            { lang: 'en', rule: 'placeholder', level: 'warning', detail: '占位符 {name} 数量不一致' },
          ],
        },
        eval_scores: { en: 42.5, de: 91 },
        review_eval_scores: { en: 55 },
        quality_flagged_langs: ['en'],
      },
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

// 每个用例前清理已渲染 DOM、重置全部 mock 并清空 localStorage，避免用例间状态串扰（尤其 assist_tok 注入断言）
beforeEach(() => { cleanup(); vi.clearAllMocks() })

describe('工单页质检透出（改造 4/5）', () => {
  it('列表行质检徽标三分支：error 计数 / warning 计数 / 质检存疑', async () => {
    render(<TicketsPage />)
    // ① 3 项错误 + 质检存疑（同单）；② 2 项提示
    await vi.waitFor(() => { expect(screen.getAllByText(/3 项错误/).length).toBeGreaterThan(0) })
    expect(screen.getAllByText(/2 项提示/).length).toBeGreaterThan(0)
    expect(screen.getAllByText(/质检存疑/).length).toBeGreaterThan(0)
  })

  it('有 error 的工单不再重复展示 warning 徽标（error 优先，避免双标误导）', async () => {
    render(<TicketsPage />)
    await vi.waitFor(() => { expect(screen.getAllByText(/3 项错误/).length).toBeGreaterThan(0) })
    // 工单 1 有 1 处 warning，但已用 error 徽标表达 → 不应出现「1 项提示」
    expect(screen.queryByText(/1 项提示/)).toBeNull()
  })

  it('无质检数据的工单不渲染任何质检徽标（不误标）', async () => {
    render(<TicketsPage />)
    await vi.waitFor(() => { expect(screen.getAllByText(/3 项错误/).length).toBeGreaterThan(0) })
    // 干净工单不应出现 0 计数徽标
    expect(screen.queryByText(/0 项错误/)).toBeNull()
    expect(screen.queryByText(/0 项提示/)).toBeNull()
    // 「质检存疑」只出现一次（仅工单 1 被打标）
    expect(screen.getAllByText(/质检存疑/).length).toBe(1)
  })

  it('详情抽屉渲染「质检报告」区块：汇总行 + Issues 明细 + 各语言评估分', async () => {
    render(<TicketsPage />)
    await vi.waitFor(() => { expect(screen.getAllByText(/3 项错误/).length).toBeGreaterThan(0) })
    // 点开第一条工单的「进度」按钮
    const btns = screen.getAllByText('进度')
    fireEvent.click(btns[0])
    await vi.waitFor(() => { expect(screen.getAllByText(/质检报告/).length).toBeGreaterThan(1) })
    // 汇总行：未通过 + N 处错误 / N 处警告
    expect(screen.getAllByText(/质检未通过/).length).toBeGreaterThan(0)
    expect(screen.getAllByText(/3 处错误 \/ 1 处警告/).length).toBeGreaterThan(0)
    // Issues 明细：说明文案 + 规则名（number → 数字不一致）
    expect(screen.getAllByText(/数字 2026 在译文中丢失/).length).toBeGreaterThan(0)
    expect(screen.getAllByText(/数字不一致/).length).toBeGreaterThan(0)
    expect(screen.getAllByText(/占位符不一致/).length).toBeGreaterThan(0)
    // 各语言评估分（初翻/校对）
    expect(screen.getAllByText(/机器评估分/).length).toBeGreaterThan(0)
    expect(screen.getAllByText(/初翻 42\.5/).length).toBeGreaterThan(0)
    expect(screen.getAllByText(/校对 55\.0/).length).toBeGreaterThan(0)
    expect(screen.getAllByText(/初翻 91\.0/).length).toBeGreaterThan(0)
  })
})
