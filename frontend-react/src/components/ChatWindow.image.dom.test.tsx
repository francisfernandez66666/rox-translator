// ============================================================================
// ChatWindow.image.dom.test.tsx — 即时翻译**图片上传**入口的组件锁（★ 2026-10-17 用户令新增）
//
// 锁三条口径（与麦克风那条「服务端能力门」同源，见 ChatWindow 里 canMic/canImage 注释）：
//   ① 能力三档分派：ocr===false ⇒ **整颗图片按钮不渲染**（不给点了必然报错的入口）；
//      ocr===null（探不到）⇒ fail-open 照常渲染；ocr===true ⇒ 渲染。
//   ② 选中图片 → 调 chatImageTranslate（首个目标语种用于译后图）→ 出结果卡：
//      译后图（data URL）＋各目标语种译文文本，都可点开下载。
//   ③ 降级方向：后端回 capability_missing 时不出结果卡（图片是择优产出，主翻译不受影响）。
//
// 为什么单开一份而不并进 ChatWindow.dom.test.tsx：那个文件的桩集合是"无网络"形态，
// 本文件需要按能力读数分档驱动，桩的口径不同（同族做法见 useChat.pkgRefresh.dom.test.tsx）。
// 运行：npx vitest run src/components/ChatWindow.image.dom.test.tsx
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, cleanup, fireEvent, waitFor } from '@testing-library/react'
import ChatWindow from './ChatWindow'
import { PkgRefreshCtx } from '@/hooks/useChat'
import { ToastProvider } from '@/ui/langcross/src'
import { setLang } from '@/i18n'

// 能力快照与翻译结果的桩（hoisted：vi.mock 工厂在模块求值前就要能引用）
const mocks = vi.hoisted(() => ({
  cap: { tts: null as boolean | null, langs: null as string[] | null, asr: null as boolean | null, ocr: null as boolean | null },
  translate: vi.fn(),
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
// 能力读数由本文件的 mocks.cap 驱动（真实现要去 fetch /api/health，测试期不打网络）
vi.mock('@/api/voiceCapability', () => ({
  useVoiceCapability: () => mocks.cap,
  ttsLangCovered: () => true,
  probeVoiceCapability: vi.fn(async () => {}),
}))
// 图片翻译客户端整体桩掉：本文件验的是"入口与渲染"，客户端自身的报文形态在 api/chatImage.test.ts 锁
vi.mock('@/api/chatImage', async () => {
  const actual = await vi.importActual<typeof import('@/api/chatImage')>('@/api/chatImage')
  return { ...actual, chatImageTranslate: mocks.translate }
})
vi.mock('@/api', () => ({
  myPackage: vi.fn(async () => ({ success: false })),
  meContext: vi.fn(async () => ({ success: false })),
  getAuthToken: () => '', getActiveTenantId: () => 0,
}))
vi.mock('@/api/translate', () => ({ estimateTranslation: vi.fn(async () => null) }))
vi.mock('./MessageBubble', () => ({ default: () => <div className="bubble-row" /> }))
vi.mock('./modals', () => ({ FeedbackModalFromMessage: () => null }))
vi.mock('@/components/LangMultiSelect', () => ({
  default: () => <div data-stub="lang-select" />,
  LangChips: () => <div data-stub="lang-chips" />,
}))
vi.mock('@/components/ModeToggle', () => ({ default: () => <div data-stub="mode-toggle" /> }))

// renderWindow 在 ToastProvider（组件内会用 toast 提示）与空的余额刷新枢纽下渲染工作台
function renderWindow() {
  return render(
    <ToastProvider>
      <PkgRefreshCtx.Provider value={null}>
        <ChatWindow />
      </PkgRefreshCtx.Provider>
    </ToastProvider>,
  )
}

beforeEach(() => {
  cleanup()
  vi.clearAllMocks()
  setLang('zh')
  mocks.cap = { tts: null, langs: null, asr: null, ocr: null }
  mocks.chat.messages = []
  mocks.chat.isLoading = false
  mocks.chat.errorMessage = ''
  mocks.chat.selectedLangs = ['en']
  localStorage.removeItem('translate_mode')
  ;(Element.prototype as any).scrollTo = (Element.prototype as any).scrollTo || vi.fn()
})

describe('即时翻译图片上传（2026-10-17 用户令）', () => {
  it('① 服务端无 OCR（ocr=false）⇒ 图片按钮整颗不渲染', () => {
    mocks.cap.ocr = false
    const { container } = renderWindow()
    expect(container.querySelector('[data-testid="chat-image-btn"]')).toBeNull()
  })

  it('② ocr=null（探不到）与 ocr=true 都渲染图片按钮（fail-open）', () => {
    const a = renderWindow()
    expect(a.container.querySelector('[data-testid="chat-image-btn"]')).toBeTruthy()
    cleanup()
    mocks.cap.ocr = true
    const b = renderWindow()
    expect(b.container.querySelector('[data-testid="chat-image-btn"]')).toBeTruthy()
  })

  it('③ 选中图片 → 调 chatImageTranslate → 出结果卡（译后图 data URL + 译文）', async () => {
    mocks.cap.ocr = true
    mocks.translate.mockResolvedValue({
      text: '你好', translations: { en: 'Hello' }, image_lang: 'en',
      image: 'data:image/png;base64,AAAA', image_error: '', line_count: 1,
      points_used: 1, mode: 'pro', target_langs: ['en'],
    })
    const { container } = renderWindow()
    const input = container.querySelector('[data-testid="chat-image-input"]') as HTMLInputElement
    const file = new File([new Uint8Array([1, 2, 3])], 'shot.png', { type: 'image/png' })
    fireEvent.change(input, { target: { files: [file] } })
    await waitFor(() => expect(container.querySelector('[data-testid="chat-image-result"]')).toBeTruthy())
    // 客户端收到的首个目标语种＝当前已选（用于译后图那张）
    expect(mocks.translate).toHaveBeenCalledWith(file, ['en'], expect.any(String), 'zh')
    expect(container.querySelector('img.cw-img-out')).toBeTruthy()
    expect(screen.getByText('Hello')).toBeTruthy()
  })

  it('④ 能力缺失（reason=capability_missing）⇒ 不出结果卡（图片是择优产出）', async () => {
    mocks.cap.ocr = true
    const { ImageError } = await vi.importActual<typeof import('@/api/chatImage')>('@/api/chatImage')
    mocks.translate.mockRejectedValue(new ImageError('OCR 不可用', 'capability_missing'))
    const { container } = renderWindow()
    const input = container.querySelector('[data-testid="chat-image-input"]') as HTMLInputElement
    fireEvent.change(input, { target: { files: [new File([new Uint8Array([1])], 'a.png', { type: 'image/png' })] } })
    await waitFor(() => expect(mocks.translate).toHaveBeenCalled())
    expect(container.querySelector('[data-testid="chat-image-result"]')).toBeNull()
  })
  it('⑤ 上传后立即出"正在识别并翻译"状态卡（长链路不许无反馈）', async () => {
    mocks.cap.ocr = true
    let resolveFn: (v: any) => void = () => {}
    mocks.translate.mockReturnValue(new Promise((resolve) => { resolveFn = resolve }))
    const { container } = renderWindow()
    const input = container.querySelector('[data-testid="chat-image-input"]') as HTMLInputElement
    fireEvent.change(input, { target: { files: [new File([new Uint8Array([1])], 'shot.png', { type: 'image/png' })] } })
    await waitFor(() => expect(container.querySelector('[data-testid="chat-image-working"]')).toBeTruthy())
    const btn = container.querySelector('[data-testid="chat-image-btn"]') as HTMLButtonElement
    expect(btn.getAttribute('aria-busy')).toBe('true')
    expect(btn.querySelector('.cw-spin')).toBeTruthy()
    // 收尾：让 promise 落地，避免卸载后 act 警告。
    resolveFn({ text: '你好', translations: { en: 'Hello' }, image_lang: 'en', image: '', image_error: '', line_count: 1, points_used: 1, mode: 'pro', target_langs: ['en'] })
    await waitFor(() => expect(container.querySelector('[data-testid="chat-image-result"]')).toBeTruthy())
  })

})
