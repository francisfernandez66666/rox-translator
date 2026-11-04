// ============================================================================
// TicketsPage.rowArtifacts.dom.test.tsx — 工单列表「行内双下载」六腿行为锁
//   钉施工方案《后台超时与慢SQL_跨租户与充值_工单双下载_施工方案_20261103.md》
//   §5.2/§5.3（行内操作列三态、灰态「有内容地缺席」）与 §5.6-B 点名的 K1–K6：
//     K1 completed ⇒ 行内 data-testid="tk-row-dl-md" 在位；
//     K2 md 与 tk-row-dl-original 都在且 md 在前（compareDocumentPosition，不写死子节点序号）；
//     K3 in_progress/queued ⇒ 零个可点下载钮 ＋ 灰态 tk-row-dl-waiting 在位；
//     K4 completed 但 artifact_summary.original 为 null ⇒ md 仍出、原格式那格出原因灰字（不许整格消失）；
//     K5 点击后请求必须打到 /api/tickets/artifact 或它实际走的那条下载口（断言请求发出，不是界面变绿）；
//     K6 completed ＋ 多语种 ⇒ 行内只出主语种一组，且该组与详情的 primary 同源。
//   行数据要带 artifact_summary（列表出参），fixture 造法照 TicketsPage.artifacts.dom.test.tsx，
//   但这里渲染的是**整页列表行**（不是进度弹窗里的 TicketArtifacts 子组件）。
// 运行：cd frontend-react && npx vitest run src/components/TicketsPage.rowArtifacts.dom.test.tsx
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, cleanup, fireEvent, waitFor } from '@testing-library/react'
import { setLang, t } from '@/i18n'
import TicketsPage from './TicketsPage'

const ROW_ID = 11
const MD_ID = 31
const ORIGINAL_ID = 32
const JA_MD_ID = 41
const JA_ORIGINAL_ID = 42

const h = vi.hoisted(() => ({
  tickets: [] as any[],
  detail: {} as Record<string, unknown>,
  ticketDownload: vi.fn(async () => undefined),
  ticketArtifactDownload: vi.fn(async () => undefined),
  ticketArtifactPreview: vi.fn(async () => ({ text: 'x', truncated: false })),
}))

// 整页渲染：桩掉 @/api 域接口 + 三个重子组件/能力 hook（形态照 TicketsPage.compliance.dom.test.tsx）
vi.mock('@/lib/toastBus', () => ({
  toastError: vi.fn(), toastSuccess: vi.fn(), toastWarn: vi.fn(), toast: vi.fn(), registerToastHandler: vi.fn(),
}))
vi.mock('@/api', () => {
  const ok = (extra: Record<string, unknown> = {}) => ({ success: true, ...extra })
  return {
    myTickets: vi.fn(async () => ok({ tickets: h.tickets })),
    ticketDetail: vi.fn(async () => ok(h.detail)),
    ticketCreate: vi.fn(async () => ok()), ticketCreateFile: vi.fn(async () => ok()),
    ticketRun: vi.fn(async () => ok()),
    ticketDownload: h.ticketDownload,                 // md 实际走的下载口：/api/tickets/download?fmt=text
    ticketArtifactDownload: h.ticketArtifactDownload, // 原格式走：/api/tickets/artifact?id=&aid=
    ticketArtifactPreview: h.ticketArtifactPreview,
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
  return {
    ...actual,
    useVoiceCapability: () => ({ tts: null, langs: null, asr: null, ocr: null }),
    probeVoiceCapability: () => Promise.resolve(),
  }
})
vi.mock('@/components/ModeToggle', () => ({ default: () => <div data-stub="mode-toggle" /> }))
vi.mock('./WordSwap', () => ({ default: () => <div data-stub="word-swap" /> }))

/** 一行 completed 工单；artifact_summary 是列表出参（B2），行数据必须自带 */
function row(over: Record<string, unknown> = {}) {
  return {
    id: ROW_ID, ticket_no: 'T-1103', title: '合同翻译', status: 'completed',
    target_langs: 'en', created_at: '2026-11-03T00:00:00Z',
    artifact_summary: {
      md: { id: MD_ID, name: '合同_en.md', lang: 'en', size: 120 },
      original: { id: ORIGINAL_ID, name: '合同_en.pdf', lang: 'en', size: 900 },
    },
    ...over,
  }
}

beforeEach(() => {
  cleanup()
  setLang('zh')
  vi.clearAllMocks()
  h.tickets = [row()]
  h.detail = { ticket: row(), states: [], files: [] }
  h.ticketDownload.mockResolvedValue(undefined)
  h.ticketArtifactDownload.mockResolvedValue(undefined)
  h.ticketArtifactPreview.mockResolvedValue({ text: 'x', truncated: false })
})
afterEach(cleanup)

/** 等列表行落地（工单号列出现），有它才敢判「0 个」不是「还没渲染」 */
async function waitRow() {
  await waitFor(() => { expect(screen.getAllByText('T-1103').length).toBeGreaterThan(0) })
}

/** 打开详情弹窗：详情 Link 是该行操作列最后一个 button（文案走 i18n，不按文案定位） */
async function openDetail() {
  const cells = await screen.findAllByText('T-1103')
  const tr = cells[0].closest('tr') as HTMLElement
  const btns = [...tr.querySelectorAll('button')]
  fireEvent.click(btns[btns.length - 1])
  await waitFor(() => { expect(document.querySelector('[data-testid="tk-progress-dialog"]')).toBeTruthy() })
}

describe('工单列表行内双下载（施工方案 §5.2/§5.3/§5.6-B · K1–K6）', () => {
  it('K1 completed ⇒ 行内 tk-row-dl-md 在位', async () => {
    render(<TicketsPage />)
    await waitRow()
    const md = screen.getByTestId('tk-row-dl-md')
    expect(md).toBeTruthy()
    expect(md.textContent).toBe(t('tk.downloadMdRow'))
    // 不是「整行没渲染所以找不到」：工单号列在位
    expect(screen.getByText('T-1103')).toBeTruthy()
  })

  it('K2 md 与 tk-row-dl-original 都在且 md 在前（compareDocumentPosition）', async () => {
    render(<TicketsPage />)
    await waitRow()
    const md = screen.getByTestId('tk-row-dl-md')
    const orig = screen.getByTestId('tk-row-dl-original')
    expect(orig).toBeTruthy()
    // 顺序判据：orig 必须「跟随」md，不许写死第几个子节点
    const rel = md.compareDocumentPosition(orig)
    expect(rel & Node.DOCUMENT_POSITION_FOLLOWING, 'md 在 original 之前').toBeTruthy()
    expect(rel & Node.DOCUMENT_POSITION_PRECEDING).toBeFalsy()
    // 两颗确实同属一行的操作列，不是文档里散落的两个同名锚点
    expect(md.closest('tr')).toBe(orig.closest('tr'))
  })

  it('K3 in_progress / queued ⇒ 零个可点下载钮 ＋ 灰态 tk-row-dl-waiting 在位', async () => {
    for (const status of ['in_progress', 'queued']) {
      cleanup()
      h.tickets = [row({ status })]
      render(<TicketsPage />)
      await waitRow()
      expect(screen.queryByTestId('tk-row-dl-md'), `${status}: 无 md 钮`).toBeNull()
      expect(screen.queryByTestId('tk-row-dl-original'), `${status}: 无原格式钮`).toBeNull()
      expect(screen.queryByTestId('tk-row-original-missing'), `${status}: 不进 completed 的缺席分支`).toBeNull()
      const waiting = screen.getByTestId('tk-row-dl-waiting')
      expect(waiting.textContent).toBe(t('tk.mdAfterDone'))
      // 「零个可点下载钮」用 testid 全集判：这一格只允许有灰态，不许混进任何 dl-* 可点形状
      const opCell = waiting.closest('td') as HTMLElement
      const dlIds = [...opCell.querySelectorAll('[data-testid]')]
        .map((e) => e.getAttribute('data-testid')!)
        .filter((id) => id.startsWith('tk-row-dl-'))
      expect(dlIds, `${status}: 操作列里没有可点下载钮`).toEqual(['tk-row-dl-waiting'])
      expect(opCell.textContent || '').not.toContain(t('tk.downloadMdRow'))
      expect(opCell.textContent || '').not.toContain(t('tk.downloadOriginalRow'))
    }
  })

  it('K4 completed 但 artifact_summary.original 为 null ⇒ md 仍出、原格式那格出原因灰字', async () => {
    h.tickets = [row({
      artifact_summary: {
        md: { id: MD_ID, name: '合同_en.md', lang: 'en', size: 120 },
        original: null,
      },
    })]
    render(<TicketsPage />)
    await waitRow()
    expect(screen.getByTestId('tk-row-dl-md'), 'md 仍出').toBeTruthy()
    expect(screen.queryByTestId('tk-row-dl-original'), '没有可下的原格式件 ⇒ 不给可点钮').toBeNull()
    const missing = screen.getByTestId('tk-row-original-missing')
    expect(missing.textContent, '缺席必须点名原因，不许整格消失').toBe(t('tk.originalRowMissing'))
    expect((missing.textContent || '').trim().length, '不是静默留白').toBeGreaterThan(0)
    expect(missing.tagName.toLowerCase(), '原因灰字那一「格」仍在 DOM').toBe('span')
  })

  it('K5 点击 md / 原格式 ⇒ 请求真的发到对应下载口（断言请求发出，不是界面变绿）', async () => {
    // ① md：它实际走 ticketDownload(fmt=text) ⇒ /api/tickets/download
    render(<TicketsPage />)
    await waitRow()
    expect(h.ticketDownload, '点击前 0 次（测量通道起点）').not.toHaveBeenCalled()
    fireEvent.click(screen.getByTestId('tk-row-dl-md'))
    await waitFor(() => { expect(h.ticketDownload).toHaveBeenCalledTimes(1) })
    // ★ 2026-11-04 收口：原写法直接索引 mock.calls[0][0]/[1]，而桩声明成无参
    //   `vi.fn(async () => undefined)` ⇒ TS 把元组推成 `[]`，tsc --noEmit 当场
    //   TS2493（vitest 跑得过去但发布闸门里的类型闸过不了）。改成 toHaveBeenCalledWith
    //   ＋ objectContaining：判据一字不松（仍钉 id 与 fmt=text），且不再依赖桩的参数推断。
    expect(h.ticketDownload).toHaveBeenCalledWith(ROW_ID, expect.objectContaining({ fmt: 'text' }))

    // ② 原格式：走 ticketArtifactDownload ⇒ /api/tickets/artifact（单件产物口）
    // 分两次独立 render，避开 downloadText/rowArtifactDl 共用的 downloadingId 单飞锁
    cleanup()
    render(<TicketsPage />)
    await waitRow()
    expect(h.ticketArtifactDownload, '点击前 0 次').not.toHaveBeenCalled()
    fireEvent.click(screen.getByTestId('tk-row-dl-original'))
    await waitFor(() => { expect(h.ticketArtifactDownload).toHaveBeenCalledTimes(1) })
    expect(h.ticketArtifactDownload).toHaveBeenCalledWith(ROW_ID, ORIGINAL_ID)
  })

  it('K6 completed ＋ 多语种 ⇒ 行内只出主语种一组，且该组与详情的 primary 同源', async () => {
    const artifacts = [
      { id: MD_ID, file_id: 3, lang: 'en', kind: 'md', name: '合同_en.md', size: 120, primary: false, degraded: false },
      { id: ORIGINAL_ID, file_id: 3, lang: 'en', kind: 'original', name: '合同_en.pdf', size: 900, primary: true, degraded: false },
      { id: JA_MD_ID, file_id: 3, lang: 'ja', kind: 'md', name: '合同_ja.md', size: 130, primary: false, degraded: false },
      { id: JA_ORIGINAL_ID, file_id: 3, lang: 'ja', kind: 'original', name: '合同_ja.pdf', size: 910, primary: false, degraded: false },
    ]
    h.tickets = [row({
      target_langs: 'en,ja',
      artifact_summary: {
        md: { id: MD_ID, name: '合同_en.md', lang: 'en', size: 120 },
        original: { id: ORIGINAL_ID, name: '合同_en.pdf', lang: 'en', size: 900 },
      },
    })]
    h.detail = {
      ticket: row({ target_langs: 'en,ja' }),
      states: [], files: [{ id: 3, file_name: '合同.pdf' }],
      artifacts,
    }
    render(<TicketsPage />)
    await waitRow()

    // ① 行内只出主语种一组：md / original 各恰好 1 个（不是按语种铺 2 组）
    expect(screen.getAllByTestId('tk-row-dl-md')).toHaveLength(1)
    expect(screen.getAllByTestId('tk-row-dl-original')).toHaveLength(1)

    // ② 详情弹窗里仍是多语种全集：按 (file_id, lang) 分成 2 组
    await openDetail()
    expect(document.querySelectorAll('[data-testid="tk-artifact-group"]').length).toBe(2)

    // ③ 同源锁：行内 original 用的 id 必须逐值等于详情里 primary 那件的 id，
    //    且不是另一语种（ja）组的 id —— 选错组就会把 JA_ORIGINAL_ID 传下去，当场判红
    const primaryOriginal = artifacts.find((a) => a.primary && a.kind === 'original')!
    expect(primaryOriginal.id).toBe(ORIGINAL_ID)
    expect(h.tickets[0].artifact_summary.original.id, '行内摘要与详情 primary 同源').toBe(primaryOriginal.id)
    fireEvent.click(screen.getByTestId('tk-row-dl-original'))
    await waitFor(() => { expect(h.ticketArtifactDownload).toHaveBeenCalledTimes(1) })
    expect(h.ticketArtifactDownload).toHaveBeenCalledWith(ROW_ID, primaryOriginal.id)
    expect(h.ticketArtifactDownload).not.toHaveBeenCalledWith(ROW_ID, JA_ORIGINAL_ID)
  })
})
