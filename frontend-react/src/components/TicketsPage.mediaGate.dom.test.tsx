// ============================================================================
// TicketsPage.mediaGate.dom.test.tsx — 工单「图片/音频」服务端能力门（★ 2026-10-16 能力门批）
//
// 由来：图片与音频这两类素材**不是**纯文本链路——图片先进 OCR、音频先进 ASR
//   （backend-go/internal/fileproc 的 ocr_translate_image.py / asr_transcribe.py）。
//   部署机没投放对应模型时 /api/health 的 ocr_ready/asr_ready=false，那两个接口恒回
//   capability_missing；而文件选择框的 accept 里照样列着八个图片后缀与七个音频后缀
//   ⇒ 客户选完、建单、等一会儿才吃一句失败。**入口本身在承诺一件服务端做不到的事**。
//
// 落点四档（同一把尺子见 api/voiceCapability.ts）：
//   · ocr/asr=false ⇒ 该类的后缀从 accept 摘掉，且客户绕开选择框（切「所有文件」）塞进来时当场拒收＋出话；
//   · ocr/asr=null（探不到）⇒ 放行（fail-open，探针坏了不该摘产品入口）；
//   · 只禁用一类时另一类不受影响（两条能力彼此独立，不许"一档 false 摘两类"）；
//   · 拒收是**逐文件**的：同一批里被禁用类被丢、其余照收（不许整批作废）。
// 清单内容（哪八个后缀属图片类）不在本文件射程内——那是 fileproc 唯一事实源的镜像，
// 由 src/api/translateFormats.lock.test.ts 钉住。
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, cleanup, fireEvent, waitFor } from '@testing-library/react'
import { setLang, t } from '@/i18n'

const ok = (extra: Record<string, unknown> = {}) => ({ success: true, ...extra })
// 工单页要打的接口（最小集）：只取数不建单，避免用例与真实链路耦合
vi.mock('@/api', () => ({
  myTickets: vi.fn(async () => ok({ tickets: [] })),
  ticketDetail: vi.fn(async () => ok({ states: [], files: [], progress: 0 })),
  ticketCreate: vi.fn(async () => ok()), ticketCreateFile: vi.fn(async () => ok()),
  ticketRun: vi.fn(async () => ok()), ticketDownload: vi.fn(async () => undefined),
  ticketDelete: vi.fn(async () => ok()), ticketCancel: vi.fn(async () => ok()),
  createFeedback: vi.fn(async () => ok()),
  getAuthToken: () => '', getActiveTenantId: () => 0,
}))
// 提示出口桩：本文件要判"拒收时到底说了什么"（文案键取错＝客户看到一句不相关的话）
const toast = vi.hoisted(() => ({ error: vi.fn(), success: vi.fn(), warn: vi.fn() }))
vi.mock('@/lib/toastBus', () => ({ toastError: toast.error, toastSuccess: toast.success, toastWarn: toast.warn }))
// 能力快照：只替换"取快照/拨探针"两条，其余用真实实现
const cap = { tts: null as boolean | null, langs: null as string[] | null, asr: null as boolean | null, ocr: null as boolean | null }
vi.mock('@/api/voiceCapability', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api/voiceCapability')>()
  return { ...actual, useVoiceCapability: () => cap, probeVoiceCapability: () => Promise.resolve() }
})
vi.mock('@/components/LangMultiSelect', () => ({
  default: () => <div data-stub="lang-multi" />, LangChips: () => <div data-stub="lang-chips" />,
}))
vi.mock('@/components/ModeToggle', () => ({ default: () => <div data-stub="mode-toggle" /> }))
vi.mock('./WordSwap', () => ({ default: () => <div data-stub="word-swap" /> }))

// 文件选择框在「文件翻译」模式才渲染：切过去再量 accept
async function openFileMode(container: HTMLElement) {
  const btn = Array.from(container.querySelectorAll('button')).find((b) => b.textContent === t('tk.modeFile'))
  expect(btn, '找不到「文件翻译」模式按钮').toBeTruthy()
  fireEvent.click(btn!)
  await waitFor(() => expect(container.querySelector('#tk-file-input')).toBeTruthy())
  return container.querySelector('#tk-file-input') as HTMLInputElement
}

// jsdom 的 File.size 由内容决定（一字节=1），这里把 size 定义成可控值：
// 用例只关心"哪个后缀被拒/被收"，不想因为体积那条腿（后端上限 40 MiB）产生噪声
function mkFile(name: string, size = 1024): File {
  const f = new File(['x'], name)
  Object.defineProperty(f, 'size', { value: size })
  return f
}

describe('工单图片/音频的服务端能力门', () => {
  beforeEach(() => { cleanup(); vi.clearAllMocks(); setLang('zh'); cap.asr = null; cap.ocr = null; localStorage.removeItem('ticket_delivery') })

  it('T1 ocr=false 且 asr=false：两类后缀都从 accept 摘掉，文本类一个不少', async () => {
    cap.ocr = false; cap.asr = false
    const TicketsPage = (await import('./TicketsPage')).default
    const { container } = render(<TicketsPage />)
    const input = await openFileMode(container)
    const acc = input.getAttribute('accept') || ''
    for (const ext of ['.png', '.jpg', '.heic', '.mp3', '.wav', '.opus']) {
      expect(acc.includes(ext), `服务端没有该项能力时 accept 不得再列 ${ext}（否则等于承诺做不到的事）`).toBe(false)
    }
    // 正向对照：纯文本链路那几类必须还在（摘的是能力，不是整条准入）
    for (const ext of ['.docx', '.pdf', '.txt']) expect(acc.includes(ext)).toBe(true)
  })

  it('T2 只禁用音频（asr=false）：图片后缀照旧在（两条能力彼此独立）', async () => {
    cap.ocr = true; cap.asr = false
    const TicketsPage = (await import('./TicketsPage')).default
    const { container } = render(<TicketsPage />)
    const input = await openFileMode(container)
    const acc = input.getAttribute('accept') || ''
    expect(acc.includes('.png'), 'asr=false 不该连图片一起摘掉').toBe(true)
    expect(acc.includes('.mp3'), 'asr=false 必须摘掉音频类').toBe(false)
  })

  it('T3 探不到（null）：fail-open 全量放行', async () => {
    cap.ocr = null; cap.asr = null
    const TicketsPage = (await import('./TicketsPage')).default
    const { container } = render(<TicketsPage />)
    const input = await openFileMode(container)
    const acc = input.getAttribute('accept') || ''
    expect(acc.includes('.png'), '探不到 OCR 能力时不许摘掉图片入口').toBe(true)
    expect(acc.includes('.mp3'), '探不到 ASR 能力时不许摘掉音频入口').toBe(true)
  })

  it('T4 ocr=false：绕开选择框塞进来的图片当场拒收并出对应文案，同一批的文档照收（逐文件而非整批）', async () => {
    cap.ocr = false; cap.asr = false
    const TicketsPage = (await import('./TicketsPage')).default
    const { container } = render(<TicketsPage />)
    const input = await openFileMode(container)
    fireEvent.change(input, { target: { files: [mkFile('照片.png'), mkFile('文档.docx'), mkFile('录音.mp3')] } })
    await waitFor(() => expect(toast.error).toHaveBeenCalled())
    const said = toast.error.mock.calls.map((c) => String(c[0]))
    expect(said.some((m) => m.includes('OCR')), `拒收图片要说清是图片识别能力缺失，实得：${JSON.stringify(said)}`).toBe(true)
    expect(said.some((m) => m.includes('ASR')), `拒收音频要说清是语音转写能力缺失，实得：${JSON.stringify(said)}`).toBe(true)
    // 被禁的两类不进待上传列表，文本类照进（否则这条门会把整批作废）
    await waitFor(() => expect(screen.queryByText('文档.docx')).toBeTruthy())
    expect(screen.queryByText('照片.png')).toBeNull()
    expect(screen.queryByText('录音.mp3')).toBeNull()
  })

  it('T5 能力齐备（true）：三类文件都进列表、不误拒（反向对照：门不是永远关着的）', async () => {
    cap.ocr = true; cap.asr = true
    const TicketsPage = (await import('./TicketsPage')).default
    const { container } = render(<TicketsPage />)
    const input = await openFileMode(container)
    fireEvent.change(input, { target: { files: [mkFile('照片.png'), mkFile('文档.docx'), mkFile('录音.mp3')] } })
    await waitFor(() => expect(screen.queryByText('文档.docx')).toBeTruthy())
    expect(screen.queryByText('照片.png'), '能力齐备时图片不许被拒').toBeTruthy()
    expect(screen.queryByText('录音.mp3'), '能力齐备时音频不许被拒').toBeTruthy()
  })
})
