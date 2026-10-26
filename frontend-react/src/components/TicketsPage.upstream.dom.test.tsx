// ============================================================================
// TicketsPage.upstream.dom.test.tsx — 详情面「上游那一族」诚实块行为锁（★ 2026-10-26 追加裁决九-1）
//
// 三条腿（对应任务书要求的三条，形态照 TicketsPage.deliverables.dom.test.tsx）：
//   U1 unresolved_lines:{en:2} + upstream_dead_lines:{en:2} ⇒ 出「上游未返回可用译文」那一句，
//      行数 2 在屏上；来源档 `upstream_failed` 出的是词典文案不是裸档位名；
//   U2 四个键都缺席（Go 侧 omitempty 的空值形态）⇒ **整块不渲染**——不许出现任何"0 行"空态话；
//   U3 只给 truncated_lines ⇒ 出「输出被截断」那一句，**不出**「上游未返回」那一句
//      （三档各一句、混写即红，是 AGENTS §一·19 族一那条的界面侧镜像）；
//   U4 总数减去两档的余量 ⇒ 出「补译后仍无可用输出」那一句（第三档有自己的句子，不并句）。
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
    myTickets: vi.fn(async () => ok({ tickets: [{ id: 11, ticket_no: 'T-1', title: 't', status: 'completed', target_langs: 'en', created_at: '2026-10-26T00:00:00Z' }] })),
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
// 能力快照桩：全部 null＝fail-open 形态，本文件不测能力门（那是 mediaGate 测试的射程）
vi.mock('@/api/voiceCapability', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api/voiceCapability')>()
  return { ...actual, useVoiceCapability: () => ({ tts: null, langs: null, asr: null, ocr: null }), probeVoiceCapability: () => Promise.resolve() }
})
vi.mock('@/components/ModeToggle', () => ({ default: () => <div data-stub="mode-toggle" /> }))
vi.mock('./WordSwap', () => ({ default: () => <div data-stub="word-swap" /> }))

const ticket = { id: 11, ticket_no: 'T-1', title: 't', status: 'completed', target_langs: 'en', created_at: '2026-10-26T00:00:00Z' }

// 打开详情气泡（详情钮＝该行最后一个 button；文案走 i18n，不按文案定位）
async function openDetail(no: string) {
  const cells = await screen.findAllByText(no)
  const row = cells[0].closest('tr') as HTMLElement
  const btns = [...row.querySelectorAll('button')]
  fireEvent.click(btns[btns.length - 1])
  await waitFor(() => expect(document.querySelector('[data-testid="tk-progress-dialog"]')).toBeTruthy())
}

beforeEach(() => { setLang('zh'); mocks.detail = { ticket, states: [], files: [] } })
afterEach(cleanup)

describe('工单详情面 · 上游诚实块（九-1）', () => {
  it('U1 上游断两行 ⇒ 「上游未返回可用译文」那句在屏、行数 2 在屏、来源档出词典文案', async () => {
    mocks.detail = {
      ticket, states: [], files: [],
      quality: {
        sources: { en: 'upstream_failed' },
        unresolved_lines: { en: 2 },
        upstream_dead_lines: { en: 2 },
      },
    }
    render(<TicketsPage />)
    await openDetail('T-1')
    const box = document.querySelector('[data-testid="tk-upstream"]') as HTMLElement
    expect(box, '上游诚实块没渲染').toBeTruthy()
    const line = tpl('tk.upstreamDead', { lang: langLabel('en', 'zh'), n: 2 })
    expect(box.textContent, `上游断那一句没按文案出，实得：${box.textContent}`).toContain(line)
    expect(box.textContent).toContain('2')
    // 来源档走唯一取词点：屏上是词典文案，不是裸档位名 upstream_failed
    expect(box.textContent).toContain(t('tk.srcUpstreamFailed'))
    expect(box.textContent).not.toContain('upstream_failed')
    // 只有一档有数：另两档的句子不许凭空长出来
    expect(box.textContent).not.toContain(tpl('tk.upstreamTruncated', { lang: langLabel('en', 'zh'), n: 2 }))
  })

  it('U2 四个键都缺席 ⇒ 整块不渲染（禁止出现"0 行"空态话）', async () => {
    mocks.detail = { ticket, states: [], files: [], quality: { eval_scores: { en: 90 } } }
    render(<TicketsPage />)
    await openDetail('T-1')
    expect(document.querySelector('[data-testid="tk-upstream"]')).toBeNull()
    const dialog = document.querySelector('[data-testid="tk-progress-dialog"]') as HTMLElement
    expect(dialog.textContent, '健康单上不许出现"0 行"式空态句').not.toMatch(/0 行/)
  })

  it('U3 只给截断档 ⇒ 出「输出被截断」那句、不出「上游未返回」那句（三档各一句不混写）', async () => {
    mocks.detail = {
      ticket, states: [], files: [],
      quality: {
        sources: { en: 'model' },
        unresolved_lines: { en: 1 },
        truncated_lines: { en: 1 },
      },
    }
    render(<TicketsPage />)
    await openDetail('T-1')
    const box = document.querySelector('[data-testid="tk-upstream"]') as HTMLElement
    expect(box, '截断层也在诚实块射程内').toBeTruthy()
    const lang = langLabel('en', 'zh')
    expect(box.textContent).toContain(tpl('tk.upstreamTruncated', { lang, n: 1 }))
    expect(box.textContent, '截断层不许混写成上游断那句').not.toContain(tpl('tk.upstreamDead', { lang, n: 1 }))
    // 余量法：总数 1 − 截断 1 = 0 ⇒ 第三档句子也不许凭空长出来
    expect(box.textContent).not.toContain(tpl('tk.upstreamNotUsable', { lang, n: 0 }))
  })

  it('U4 总数减两档的余量 ⇒ 第三档「补译后仍无可用输出」单独成句', async () => {
    mocks.detail = {
      ticket, states: [], files: [],
      quality: {
        sources: { en: 'model_with_source_fallback' },
        unresolved_lines: { en: 4 },
        upstream_dead_lines: { en: 1 },
        truncated_lines: { en: 1 },
      },
    }
    render(<TicketsPage />)
    await openDetail('T-1')
    const box = document.querySelector('[data-testid="tk-upstream"]') as HTMLElement
    const lang = langLabel('en', 'zh')
    expect(box.textContent).toContain(tpl('tk.upstreamDead', { lang, n: 1 }))
    expect(box.textContent).toContain(tpl('tk.upstreamTruncated', { lang, n: 1 }))
    expect(box.textContent).toContain(tpl('tk.upstreamNotUsable', { lang, n: 2 }))
    // 来源档文案同样点名（第五档名 model_with_source_fallback 走词典，不许裸显）
    expect(box.textContent).toContain(t('tk.srcModelFallback'))
    expect(box.textContent).not.toContain('model_with_source_fallback')
  })
})
