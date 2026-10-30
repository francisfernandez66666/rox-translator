// ============================================================================
// components/chatInstantGuide.dom.test.tsx — 即时翻译免费档的「墙」与徽标（★ 2026-10-30 组 4 D 段）
//
// 锁什么（对应方案 G17–G19 / G22 / G24）：
//   ① 首屏零付费字样（W-2 / G22）：明确免费的账号、单语种、未撞墙时，
//      **聊天区 DOM 不出现「积分/充值/付费/升级/开通」**；正向对照「免费」徽标必须在位
//      （没有这条对照，负向腿就是永真空转——AGENTS 反复强调的那条）。
//   ② 四档原因码各出一张引导卡（W-1 / G17）：too_large_paid / too_large / too_long / unreadable
//      各一句、每句点名下一档；too_large_paid 额外给「按原图翻译」按钮。
//   ③ 工单按钮真的接线（G19）：点击后写一次性交接槽（sessionStorage）并 navigate('/tickets')。
//   ④ 升级卡三分钟节流（W-5 / G24）：同一会话第二次撞墙不再弹；dismissed 不落库。
//   ⑤ 多语种同出是付费权益（G12）：选 ≥2 个目标语种时就地给「按积分计费」口径。
//
// 与 ChatWindow.image.dom.test.tsx 的分工：那份锁**图片结果卡**与能力门；
// 本文件只锁「墙的出路」与新文案口径，桩集合因此单独一份（同族做法见 micCapability）。
// 运行：npx vitest run src/components/chatInstantGuide.dom.test.tsx
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, cleanup, fireEvent, waitFor } from '@testing-library/react'
import ChatWindow from './ChatWindow'
import { PkgRefreshCtx } from '@/hooks/useChat'
import { ToastProvider } from '@/ui/langcross/src'
import { setLang } from '@/i18n'
import { resetInstantUpgradeThrottle } from '@/lib/instantUpsell'
import { INSTANT_TICKET_HANDOFF_KEY } from '@/lib/instantTicketHandoff'

const mocks = vi.hoisted(() => ({
  cap: { tts: null as boolean | null, langs: null as string[] | null, asr: null as boolean | null, ocr: true as boolean | null },
  translate: vi.fn(),
  navigate: vi.fn(),
  chat: {
    messages: [] as any[],
    isLoading: false,
    isBackendOnline: true,
    isBackendLoading: false,
    isBackendChecking: false,
    errorMessage: '',
    selectedLangs: ['en'] as string[],
    setSelectedLangs: vi.fn(),
    sendMessage: vi.fn(),
    stopGeneration: vi.fn(),
    clearMessages: vi.fn(),
    retryHealth: vi.fn(),
  },
}))

vi.mock('@/hooks/useChat', async () => {
  const { createContext } = await import('react')
  return { useChat: () => mocks.chat, PkgRefreshCtx: createContext<unknown>(null) }
})
vi.mock('@/api/voiceCapability', () => ({
  useVoiceCapability: () => mocks.cap,
  ttsLangCovered: () => true,
  probeVoiceCapability: vi.fn(async () => {}),
}))
// 录音口不参与本文件：整颗麦克风不渲染（避免两条链路互相干扰）
vi.mock('@/api/chatVoice', () => ({ micSupported: () => false }))
// 图片客户端只换请求函数，ImageError 用真实现（调用方按 reason 分派）
vi.mock('@/api/chatImage', async () => {
  const actual = await vi.importActual<typeof import('@/api/chatImage')>('@/api/chatImage')
  return { ...actual, chatImageTranslate: mocks.translate }
})
const apiMocks = vi.hoisted(() => ({ myPackage: vi.fn(), meContext: vi.fn() }))
vi.mock('@/api', () => ({
  myPackage: apiMocks.myPackage, meContext: apiMocks.meContext,
  getAuthToken: () => 'tok-under-test', getActiveTenantId: () => 1,
}))
vi.mock('@/api/translate', () => ({ estimateTranslation: vi.fn(async () => null) }))
vi.mock('./MessageBubble', () => ({ default: () => <div className="bubble-row" /> }))
vi.mock('./modals', () => ({ FeedbackModalFromMessage: () => null }))
vi.mock('@/components/LangMultiSelect', () => ({
  default: () => <div data-stub="lang-select" />,
  LangChips: () => <div data-stub="lang-chips" />,
}))
vi.mock('@/components/ModeToggle', () => ({ default: () => <div data-stub="mode-toggle" /> }))
// 路由跳转是判据的一部分：断言"真的调用 navigate('/tickets')"，不依赖 jsdom 的未实现导航
vi.mock('@/lib/nav', () => ({
  useNav: () => ({ pathname: '/', search: '', navigate: mocks.navigate }),
}))

function renderWindow() {
  return render(
    <ToastProvider>
      <PkgRefreshCtx.Provider value={null}>
        <ChatWindow />
      </PkgRefreshCtx.Provider>
    </ToastProvider>,
  )
}

/** pickImage 选中一张小图（小图不触发压缩，直接走请求桩） */
function pickImage(container: HTMLElement, name = 'shot.png') {
  const input = container.querySelector('[data-testid="chat-image-input"]') as HTMLInputElement
  fireEvent.change(input, { target: { files: [new File([new Uint8Array([1, 2, 3])], name, { type: 'image/png' })] } })
}

/** rejectWith 让图片请求按指定 reason 失败（ImageError 为真实类） */
async function rejectWith(reason: string) {
  const { ImageError } = await vi.importActual<typeof import('@/api/chatImage')>('@/api/chatImage')
  mocks.translate.mockRejectedValue(new ImageError('upstream', reason, { suggest: 'ticket' }))
}

beforeEach(() => {
  cleanup()
  vi.clearAllMocks()
  setLang('zh')
  resetInstantUpgradeThrottle()
  sessionStorage.clear()
  mocks.cap = { tts: null, langs: null, asr: null, ocr: true }
  mocks.chat.messages = []
  mocks.chat.isLoading = false
  mocks.chat.errorMessage = ''
  mocks.chat.selectedLangs = ['en']
  // 「明确免费」账号：包编码 trial + 0 积分（首屏隐藏余额条的前提，见 lib/instantTier.isKnownFreeAccount）
  apiMocks.myPackage.mockResolvedValue({ success: true, package_code: 'trial', points_balance: 0, balance_sentences_approx: 0 })
  apiMocks.meContext.mockResolvedValue({ success: true, instant_tier: 'free' })
  localStorage.removeItem('translate_mode')
  ;(Element.prototype as any).scrollTo = (Element.prototype as any).scrollTo || vi.fn()
})

describe('即时翻译免费档：首屏口径与墙的出路', () => {
  it('① 首屏零付费字样（G22）：免费徽标在位，且 DOM 无「积分/充值/付费/升级/开通」', async () => {
    const { container } = renderWindow()
    expect(screen.getByTestId('chat-free-badge').textContent).toBe('免费')
    // 等两处档位来源落地（myPackage/meContext），余额条才会按"明确免费"收起
    await waitFor(() => expect(apiMocks.myPackage).toHaveBeenCalled())
    await waitFor(() => {
      // 只扫**聊天区**（.cw-dialog）的可见结构：`<style>` 里的 CSS 注释不是界面文案
      // （jsdom 的 textContent 会把 style 内容一起算进来，真实 DOM 扫描必须先剥掉它，
      //   同 AGENTS §一·5「先 stripHtmlComments 再判」那条口径）。
      const dialog = container.querySelector('.cw-dialog') as HTMLElement
      const text = dialog.textContent || ''
      expect(text, `首屏出现付费字样：${text}`).not.toMatch(/积分|充值|付费|升级|开通/)
    })
  })

  it('② too_large_paid：卡出「按原图付费」的一句 + 按原图/工单两枚按钮', async () => {
    await rejectWith('too_large_paid')
    const { container } = renderWindow()
    pickImage(container)
    await waitFor(() => expect(screen.getByTestId('chat-instant-guide').getAttribute('data-reason')).toBe('too_large_paid'))
    expect(screen.getByText(/超出免费档/)).toBeTruthy()
    expect(screen.getByText('按原图翻译')).toBeTruthy()
    expect(screen.getByText('上传到翻译工单')).toBeTruthy()
    // 点「按原图翻译」= 真以付费档重发原图（G19 的"按钮真的接线"）
    fireEvent.click(screen.getByText('按原图翻译'))
    await waitFor(() => expect(mocks.translate).toHaveBeenCalledTimes(2))
  })

  it('③ too_large：图片超硬顶 ⇒ 工单卡（点名 3 MB / 40 MB 与还原原格式）', async () => {
    await rejectWith('too_large')
    const { container } = renderWindow()
    pickImage(container)
    await waitFor(() => expect(screen.getByTestId('chat-instant-guide').getAttribute('data-reason')).toBe('too_large'))
    expect(screen.getByText(/3 MB/)).toBeTruthy()
    expect(screen.getByText(/40 MB/)).toBeTruthy()
    expect(screen.getByText('上传到翻译工单')).toBeTruthy()
  })

  it('④ too_long：录音超时长 ⇒ 工单卡（下一档是"更长上限"）', async () => {
    await rejectWith('too_long')
    const { container } = renderWindow()
    pickImage(container)
    await waitFor(() => expect(screen.getByTestId('chat-instant-guide').getAttribute('data-reason')).toBe('too_long'))
    expect(screen.getByText(/更长上限/)).toBeTruthy()
    expect(screen.getByText('上传到翻译工单')).toBeTruthy()
  })

  it('⑤ unreadable：读不出内容 ⇒ 工单卡（下一档是"上传原文件"）', async () => {
    await rejectWith('unreadable')
    const { container } = renderWindow()
    pickImage(container)
    await waitFor(() => expect(screen.getByTestId('chat-instant-guide').getAttribute('data-reason')).toBe('unreadable'))
    expect(screen.getByText(/上传原文件/)).toBeTruthy()
    expect(screen.getByText('上传到翻译工单')).toBeTruthy()
  })

  it('⑥ 点「上传到翻译工单」：写一次性交接槽并跳 /tickets（G19）', async () => {
    await rejectWith('too_large')
    const { container } = renderWindow()
    pickImage(container, 'big-shot.png')
    await waitFor(() => expect(screen.getByTestId('chat-instant-guide')).toBeTruthy())
    fireEvent.click(screen.getByText('上传到翻译工单'))
    await waitFor(() => expect(mocks.navigate).toHaveBeenCalledWith('/tickets'))
    const raw = sessionStorage.getItem(INSTANT_TICKET_HANDOFF_KEY)
    expect(raw, '交接槽必须写入（否则工单页拿不到这个文件）').toBeTruthy()
    const payload = JSON.parse(raw as string)
    expect(payload.name).toBe('big-shot.png')
    expect(payload.dataUrl, '小文件必须内联 dataUrl（工单页可直接重建 File）').toMatch(/^data:/)
  })

  it('⑦ 升级卡三分钟节流（G24）：第二次撞墙不再弹，引导卡照出', async () => {
    await rejectWith('too_large')
    const { container } = renderWindow()
    pickImage(container)
    await waitFor(() => expect(screen.getByTestId('chat-instant-upgrade')).toBeTruthy())
    fireEvent.click(screen.getByText('稍后'))
    await waitFor(() => expect(screen.queryByTestId('chat-instant-upgrade')).toBeNull())
    // 第二次撞墙：墙的出路照出（W-1），但升级卡被三分钟节流挡住
    pickImage(container)
    await waitFor(() => expect(screen.getByTestId('chat-instant-guide')).toBeTruthy())
    expect(screen.queryByTestId('chat-instant-upgrade')).toBeNull()
  })

  it('⑧ 多语种同出（G12）：选 2 个目标语种时就地给"按积分计费"口径', () => {
    mocks.chat.selectedLangs = ['en', 'ja']
    renderWindow()
    const hint = screen.getByTestId('chat-multi-paid')
    expect(hint.textContent).toContain('多语种同出为付费权益')
    expect(hint.getAttribute('title')).toContain('仅支持 1 个目标语种')
  })
})
