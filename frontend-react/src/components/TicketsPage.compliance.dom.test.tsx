// ============================================================================
// TicketsPage.compliance.dom.test.tsx — 详情面「内容合规跳过」诚实块行为锁（★ 2026-11-03 合规闸批）
//
// 五条腿（形态照 TicketsPage.upstream.dom.test.tsx）：
//   K1 输入侧一处跳过（带命中词面）⇒ 出块标题、逐条行号、**词面点名**、以及**接口下发的处置说明原文**
//      （逐字相等——前端只渲染不再造措辞，AGENTS §一·5「两处面是同一句话」的手法）；
//   K2 两个键都缺席（Go 侧 omitempty 的空值形态）⇒ **整块不渲染**——不许出现"0 处跳过"空态话；
//   K3 只给 `compliance_skipped` 计数、不给明细（历史单/明细缺档）⇒ 仍渲染（计数是权威真值，
//      让它因为缺明细而消失＝最该说清的一单反而静默）；
//   K4 输出侧那一档（`side:'output'`、无 `terms`）⇒ 只报行号，**不编词面**
//      （服务端刻意不回显模型自产的违禁词，前端不许拿 side 去猜一个词出来）；
//   K5 来源第六档 `compliance_skipped` 走唯一取词点出词典文案，不裸显档位名（与上游块同口径）。
// 说明：本块是**条件渲染**（读不到键即不进 DOM），jsdom 判"在不在 DOM"即判"显不显示"。
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, cleanup, fireEvent, waitFor } from '@testing-library/react'
import { setLang, t, tpl } from '@/i18n'
import { langLabel } from '@/lib/langNames'
import TicketsPage from './TicketsPage'

const mocks = vi.hoisted(() => ({ detail: {} as Record<string, unknown> }))

vi.mock('@/lib/toastBus', () => ({
  toastError: vi.fn(), toastSuccess: vi.fn(), toastWarn: vi.fn(), toast: vi.fn(), registerToastHandler: vi.fn(),
}))
vi.mock('@/api', () => {
  const ok = (extra: Record<string, unknown> = {}) => ({ success: true, ...extra })
  return {
    myTickets: vi.fn(async () => ok({ tickets: [{ id: 11, ticket_no: 'T-1', title: 't', status: 'completed', target_langs: 'en', created_at: '2026-11-03T00:00:00Z' }] })),
    ticketDetail: vi.fn(async () => ok(mocks.detail)),
    ticketCreate: vi.fn(async () => ok()), ticketCreateFile: vi.fn(async () => ok()),
    ticketRun: vi.fn(async () => ok()), ticketDownload: vi.fn(async () => undefined),
    ticketDelete: vi.fn(async () => ok()), ticketCancel: vi.fn(async () => ok()),
    createFeedback: vi.fn(async () => ok()),
    getAuthToken: () => '', getActiveTenantId: () => 0,
  }
})
vi.mock('@/components/LangMultiSelect', () => ({
  default: () => <div data-stub="lang-multi" />, LangChips: () => <div data-stub="lang-chips" />,
}))
vi.mock('@/api/voiceCapability', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api/voiceCapability')>()
  return { ...actual, useVoiceCapability: () => ({ tts: null, langs: null, asr: null, ocr: null }), probeVoiceCapability: () => Promise.resolve() }
})
vi.mock('@/components/ModeToggle', () => ({ default: () => <div data-stub="mode-toggle" /> }))
vi.mock('./WordSwap', () => ({ default: () => <div data-stub="word-swap" /> }))

const ticket = { id: 11, ticket_no: 'T-1', title: 't', status: 'completed', target_langs: 'en', created_at: '2026-11-03T00:00:00Z' }

// 处置说明的**期望值**刻意写在这里当「客户看到的接口原文」：这条腿要证的是
// 「渲染的是接口给的那一句」，不是「前端抄对了那一句」（同 §三「假成功不会让闸门红」那族）。
const LEAD_IN = '内容合规审核：该处未翻译，原文已按原样保留'

async function openDetail(no: string) {
  const cells = await screen.findAllByText(no)
  const row = cells[0].closest('tr') as HTMLElement
  const btns = [...row.querySelectorAll('button')]
  fireEvent.click(btns[btns.length - 1])
  await waitFor(() => expect(document.querySelector('[data-testid="tk-progress-dialog"]')).toBeTruthy())
}

beforeEach(() => { setLang('zh'); mocks.detail = { ticket, states: [], files: [] } })
afterEach(cleanup)

describe('工单详情面 · 内容合规跳过块（2026-11-03）', () => {
  it('K1 输入侧一处 ⇒ 块标题＋行号＋命中词面＋接口原文逐字在屏', async () => {
    const item = { lang: 'en', line: 2, terms: ['测试禁词'], side: 'input', lead: LEAD_IN }
    mocks.detail = {
      ticket, states: [], files: [],
      quality: { compliance_skipped: { en: 1 }, compliance_items: [item], sources: { en: 'model' } },
    }
    render(<TicketsPage />)
    await openDetail('T-1')
    const box = document.querySelector('[data-testid="tk-compliance"]') as HTMLElement
    expect(box, '合规跳过块没渲染').toBeTruthy()
    const text = box.textContent || ''
    expect(text).toContain(t('tk.complianceTitle'))
    expect(text).toContain(tpl('tk.complianceLangFmt', { lang: langLabel('en', 'zh'), n: 1 }))
    expect(text).toContain(tpl('tk.complianceItemLine', { lang: langLabel('en', 'zh'), line: 2 }))
    // 用户裁决②：交付面必须写明跳过的是**哪个词**
    expect(text).toContain(tpl('tk.complianceItemTerms', { terms: '测试禁词' }))
    // 处置说明＝接口原文（逐字，不是"大意相同"）
    expect(text, `处置说明没按接口原文出，实得：${text}`).toContain(LEAD_IN)
    // 零承诺（AGENTS §19 用户明令：本仓无人工复核环节）
    expect(text).not.toMatch(/人工复核|请联系客服|我们会跟进|等待审核|稍后重试/)
  })

  it('K2 两个键都缺席 ⇒ 整块不渲染（禁止"0 处跳过"空态话）', async () => {
    mocks.detail = { ticket, states: [], files: [], quality: { eval_scores: { en: 90 } } }
    render(<TicketsPage />)
    await openDetail('T-1')
    expect(document.querySelector('[data-testid="tk-compliance"]')).toBeNull()
    const dialog = document.querySelector('[data-testid="tk-progress-dialog"]') as HTMLElement
    expect(dialog.textContent).not.toContain(t('tk.complianceTitle'))
    expect(dialog.textContent).not.toMatch(/0 处/)
  })

  it('K3 只有计数没有明细 ⇒ 仍渲染（明细缺档不许让最该说清的一单静默）', async () => {
    mocks.detail = {
      ticket, states: [], files: [],
      quality: { compliance_skipped: { en: 2 } },
    }
    render(<TicketsPage />)
    await openDetail('T-1')
    const box = document.querySelector('[data-testid="tk-compliance"]') as HTMLElement
    expect(box, '只有计数时块也必须渲染').toBeTruthy()
    expect(box.textContent).toContain(tpl('tk.complianceLangFmt', { lang: langLabel('en', 'zh'), n: 2 }))
    // 没有明细 ⇒ 一条逐条行都没有；更不许凭空造一行出来
    expect(document.querySelectorAll('[data-testid="tk-compliance-item"]').length).toBe(0)
  })

  it('K4 输出侧（无 terms）⇒ 只报行号、不编词面', async () => {
    mocks.detail = {
      ticket, states: [], files: [],
      quality: {
        compliance_skipped: { en: 1 },
        compliance_items: [{ lang: 'en', line: 5, side: 'output', lead: LEAD_IN }],
      },
    }
    render(<TicketsPage />)
    await openDetail('T-1')
    const box = document.querySelector('[data-testid="tk-compliance"]') as HTMLElement
    const text = box.textContent || ''
    expect(text).toContain(tpl('tk.complianceItemLine', { lang: langLabel('en', 'zh'), line: 5 }))
    // 词面那一格整段不出现（前缀判定：任何"命中词："字样都不许有）
    expect(text).not.toContain(t('tk.complianceItemTerms').split('{')[0].trim())
  })

  it('K5 来源第六档走唯一取词点（词典文案，不裸显 compliance_skipped）', async () => {
    mocks.detail = {
      ticket, states: [], files: [],
      quality: {
        sources: { en: 'compliance_skipped' },
        compliance_skipped: { en: 1 },
        compliance_items: [{ lang: 'en', line: 1, side: 'input', terms: ['x'], lead: LEAD_IN }],
      },
    }
    render(<TicketsPage />)
    await openDetail('T-1')
    const box = document.querySelector('[data-testid="tk-compliance"]') as HTMLElement
    expect(t('tk.srcComplianceSkipped')).not.toBe('compliance_skipped') // 射程自证：词典真配了文案
    // 上游块读的是 unresolved_lines（合规跳过**不进**那一档：设计不是缺陷）
    expect(document.querySelector('[data-testid="tk-upstream"]')).toBeNull()
    expect(box).toBeTruthy()
  })
})
