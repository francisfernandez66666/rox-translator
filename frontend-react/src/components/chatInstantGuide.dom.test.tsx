// ============================================================================
// components/chatInstantGuide.dom.test.tsx — 即时翻译免费档的「墙」与徽标（★ 2026-10-30 组 4 D 段）
//
// 锁什么（对应方案 G17–G19 / G22 / G24 / G12 / N6）：
//   ① 首屏零付费字样（W-2 / G22）：明确免费的账号、单语种、未撞墙时，
//      **聊天区 DOM 不出现「积分/充值/付费/升级/开通」**；正向对照「免费」徽标必须在位
//      （没有这条对照，负向腿就是永真空转——AGENTS 反复强调的那条）。
//   ② 四档原因码各出一张引导卡（W-1 / G17）：too_large_paid / too_large / too_long / unreadable
//      各一句、每句点名下一档；too_large_paid 额外给「按原图翻译」按钮。
//   ③ 工单按钮真的接线（G19）：点击后写一次性交接槽（sessionStorage）并 navigate('/tickets')。
//   ④ 升级卡三分钟节流（W-5 / G24）：同一会话第二次撞墙不再弹；dismissed 不落库。
//   ⑤ 多语种同出是付费权益（G12）：选 ≥2 个目标语种时就地给「按积分计费」口径。
//   ⑨ ★ 2026-10-31 补：G12 只是"提示在位"，N6 要的是**不静默收费**——付费档的预估区间
//      必须在按发送之前就渲染出来，且请求带模式档与 ui_lang（AGENTS §一·5④ 那一把尺子）。
//   ⑩ ★ 同批补 G24 的第二腿：节流**不许跨会话**（写进 localStorage 的实现在 ⑦ 上照样绿，
//      只有这一条会红）。
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
  // 预估桩（★ N6）：默认 null＝"取不到预估就不显示"，需要判预估上屏的那条腿自己改返回值。
  // 形参必须显式写出来：`vi.fn(async () => null)` 会让 tsc 把 mock.calls 的元素推成空元组，
  // 于是"读第 2/4 个入参"这条判据在类型检查期就红（⑨ 判的正是入参形状）。
  estimate: vi.fn(async (_text: string, _langs: string[], _mode: string, _uiLang: string): Promise<any> => null),
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
vi.mock('@/api/translate', () => ({ estimateTranslation: mocks.estimate }))
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

  it('⑨ 多语种不静默收费（N6）：付费档预估在发送前就上屏，且请求带 ui_lang', async () => {
    // ★ 2026-10-31 补 G12 的**另一半**：⑧ 只证明"提示在位"，没证明"扣多少在按发送之前就给了"。
    //   N6 的原话是「≥2 语种 ⇒ 付费档**且响应/前端有预估**（ui_lang 必送）」，
    //   静默收费（扣完才知道）正是这条点名要抓的形态，所以这里三件一起判：
    //   ① 预估请求真发出去了，且吃的是**本次选中的两个语种**（分档判据读的就是语种数）；
    //   ② 带模式档与界面语言 ui_lang——中/英界面是两套提示词，不送就只能按中文那套量
    //      （AGENTS §一·5④：预估与实扣同尺，缺这一维英文界面访客看到偏高区间）；
    //   ③ 区间真的渲染进 `chat-estimate`（"发出去了但界面没显示"依然是静默收费）。
    mocks.chat.selectedLangs = ['en', 'ja']
    mocks.estimate.mockResolvedValue({
      success: true, points_min: 12, points_max: 30, cost_sentences_approx: 8, points_balance: 500, llm_queue_depth: 0,
    })
    const { container } = renderWindow()
    const input = container.querySelector('[data-testid="translate-input"]') as HTMLTextAreaElement
    expect(input, '工作台输入框未渲染').toBeTruthy()
    fireEvent.change(input, { target: { value: '请在傍晚前把设备送到客户手上。' } })
    // 预估有 600ms 防抖（打字停顿量级），所以这里等到它真被调用为止，不假设同一帧出结果
    await waitFor(() => expect(mocks.estimate).toHaveBeenCalled(), { timeout: 5000 })
    const args = mocks.estimate.mock.calls[0]
    expect(args[1], `预估必须带本次的两个目标语种（实到 ${JSON.stringify(args[1])}）`).toEqual(['en', 'ja'])
    expect(['fast', 'pro']).toContain(args[2])
    expect(args[3], '预估必须把界面语言当 ui_lang 送出去（缺它＝按中文提示词量英文界面的钱）').toBe('zh')
    const est = screen.getByTestId('chat-estimate')
    const estText = (est.textContent || '').replace(/[,\s]/g, '')
    expect(/12.{0,40}30/.test(estText), `预估区间没上屏：${est.textContent}`).toBeTruthy()
    expect(est.textContent).toContain('积分')
    // 同一时刻两处面必须在位：付费提示（⑧）与预估区间——只剩一个是"半个口径"
    expect(screen.getByTestId('chat-multi-paid'), '多语种付费提示与预估区间只出了一处').toBeTruthy()
  })

  it('⑩ 升级卡节流的会话边界（G24 第二腿）：刷新页面即按新会话计，仍会弹', async () => {
    // ⑦ 判的是"同一会话内第二次撞墙不再弹"；G24 还有第二条腿——**节流不许跨会话**。
    //   实现用模块级内存量（lib/instantUpsell.ts），整页刷新＝模块重新初始化＝归零；
    //   这里用测试专用入口 resetInstantUpgradeThrottle() 演这一次刷新，再重挂一次工作台。
    //   射程自证：把 lastShownAt 写进 localStorage 的实现能在 ⑦ 上照样绿（模块变量归零
    //   清不掉那份持久态），只有这一条腿会红——而"关掉一次就永远不再提示"正是 W-5 点名要防的另一头。
    await rejectWith('too_large')
    const { container } = renderWindow()
    pickImage(container)
    await waitFor(() => expect(screen.getByTestId('chat-instant-upgrade')).toBeTruthy())
    // 先收起这一次（组件态清干净），再演"刷新页面"
    fireEvent.click(screen.getByText('稍后'))
    await waitFor(() => expect(screen.queryByTestId('chat-instant-upgrade')).toBeNull())

    resetInstantUpgradeThrottle()
    cleanup()
    const c2 = renderWindow()
    pickImage(c2.container)
    await waitFor(() => expect(screen.getByTestId('chat-instant-guide')).toBeTruthy(), { timeout: 5000 })
    expect(screen.getByTestId('chat-instant-upgrade'), '新会话（刷新页面）后升级卡再不出现 ⇒ 节流跨会话了').toBeTruthy()
  })
})
