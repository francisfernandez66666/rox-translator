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
import { applyInstantCaps, resetInstantCaps } from '@/lib/instantTier'

// 能力快照与翻译结果的桩（hoisted：vi.mock 工厂在模块求值前就要能引用）
const mocks = vi.hoisted(() => ({
  // 压缩腿的桩：本文件判的是**档位数字怎么传进压缩与硬顶判定**，不是压缩算法
  // （算法由 lib/imageCompress.test.ts 六档锁负责）。jsdom 没有 canvas/图像解码，
  // 走真实现会一直等一个永不落地的 onload ⇒ 用例超时（本批真踩）。
  compress: vi.fn(async (_file: File, _opts: { maxBytes: number; maxSide: number }) => ({
    compressed: true, blob: new Blob(['x'], { type: 'image/png' }), width: 1600, height: 1200,
  })),

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
vi.mock('@/lib/imageCompress', async () => {
  const actual = await vi.importActual<typeof import('@/lib/imageCompress')>('@/lib/imageCompress')
  return { ...actual, compressImage: mocks.compress }
})
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
  resetInstantCaps()
  mocks.compress.mockClear()
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


  it('⑥ 4 MiB 图片（超出厂硬顶 3 MiB）⇒ 一个请求都不发，直接出工单引导卡（G10 第三档）', async () => {
    mocks.cap.ocr = true
    const { container } = renderWindow()
    const input = container.querySelector('[data-testid="chat-image-input"]') as HTMLInputElement
    const big = new File([new Uint8Array(4 * 1024 * 1024)], 'big.png', { type: 'image/png' })
    fireEvent.change(input, { target: { files: [big] } })
    await waitFor(() => {
      const g = container.querySelector('[data-testid="chat-instant-guide"]') as HTMLElement
      expect(g, '超硬顶必须出引导卡（点名下一档），不是一句 toast').toBeTruthy()
      expect(g.getAttribute('data-reason')).toBe('too_large')
    })
    // 「不发请求」是这一档的全部意义：跨境链路上一张 4 MiB 的图发出去＝524 白屏（现网教训见 api 侧注释）
    expect(mocks.translate, '超硬顶的图不许发翻译请求').not.toHaveBeenCalled()
    const text = (container.querySelector('.cw-dialog') as HTMLElement).textContent || ''
    expect(text, '硬顶必须念**当前生效**的数字（出厂 3 MiB）').toContain('3 MB')
  })

  it('⑦ 运营放宽硬顶到 5 MiB ⇒ 同一张 4 MiB 图能发出去，且压缩目标跟的是**现读免费档**（G10 跨面同尺）', async () => {
    // 与 ⑥ 是**同一张图**的两种配置形态：写死 3 MiB 的实现在这一枪必红；
    // 而把压缩目标写死成 1 MiB 的实现在下面那条 maxBytes 判据里必红——
    // "运营改了档、界面还按旧档拦人/按旧档压图"正是 S2 立约要消灭的形态。
    mocks.cap.ocr = true
    applyInstantCaps({ image_max_mb: 5, free_image_max_mb: 2, audio_max_sec: 60, free_audio_max_sec: 30 })
    mocks.translate.mockResolvedValue({
      text: '你好', translations: { en: 'Hello' }, image_lang: 'en', image: '',
      image_error: '', line_count: 1, points_used: 0, mode: 'pro', target_langs: ['en'],
    })
    const { container } = renderWindow()
    const input = container.querySelector('[data-testid="chat-image-input"]') as HTMLInputElement
    const big = new File([new Uint8Array(4 * 1024 * 1024)], 'big.png', { type: 'image/png' })
    fireEvent.change(input, { target: { files: [big] } })
    await waitFor(() => expect(mocks.translate, '现值 5 MiB 之内的图必须能发出去').toHaveBeenCalled(), { timeout: 10000 })
    expect(container.querySelector('[data-testid="chat-instant-guide"]'), '这一档不该再出墙卡').toBeNull()
    // 压缩目标＝现读免费档 2 MiB（出厂默认是 1 MiB；这里读到 2 才叫"与后端同一把尺子"）
    await waitFor(() => expect(mocks.compress).toHaveBeenCalled())
    const opts = mocks.compress.mock.calls[0][1]
    expect(opts.maxBytes, '压缩目标必须是 caps 现值（2 MiB），不是出厂 1 MiB').toBe(2 * 1024 * 1024)
    expect(opts.maxSide, '长边目标取档位常量（压缩策略，不在 caps 里）').toBeGreaterThan(0)
  }, 20000)

  it('⑧ 页面 DOM 零暗额度（G18 的 DOM 面）：三个隐形数字一个都不许出现，而宽度正向对照在位', async () => {
    // 暗额度**物理上不会**经 /api/instant/caps 到手（服务端那一层就不出，见 api/instant_caps_face_test.go），
    // 这一条守的是"别把整个策略对象塞进前端"这条回归路：一旦有人改成透传，这里先红。
    // 正向对照用 ⑥ 那一档：卡片必须点名当前硬顶，否则"零命中"只是因为整块没渲染。
    mocks.cap.ocr = true
    const { container } = renderWindow()
    const input = container.querySelector('[data-testid="chat-image-input"]') as HTMLInputElement
    fireEvent.change(input, { target: { files: [new File([new Uint8Array(4 * 1024 * 1024)], 'big.png', { type: 'image/png' })] } })
    await waitFor(() => expect(container.querySelector('[data-testid="chat-instant-guide"]')).toBeTruthy())
    const text = (container.querySelector('.cw-dialog') as HTMLElement).textContent || ''
    for (const n of ['3141', '2718', '1618']) {
      expect(text, `页面 DOM 里出现了暗额度数字 ${n}`).not.toContain(n)
    }
    // 正向对照用**卡片里那句宽度**（撞墙之后「免费」徽标按设计收起，见 chatInstantGuide ① 与 G22，
    // 拿它当正向对照会把一条正确的实现判成红）。
    expect(text, '正向对照：宽度没念出来时，上面三条负向是空转').toMatch(/\d+ MB/)
  })
})
