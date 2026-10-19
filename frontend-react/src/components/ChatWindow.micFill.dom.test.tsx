// ============================================================================
// ChatWindow.micFill.dom.test.tsx — 语音识别产物「回填输入框、不自动发送」（★ 2026-10-19 用户令）
//
// 由来：2026-10-15 曾把语音改成「识别完直接发送」（元宝式一次手势）；2026-10-19 用户令推翻——
//   识别文本要先落进输入框让用户二次编辑，点「翻译」才发送（误识别/口误先校对再计费）。
// 本文件锁组件侧那一腿（服务端 ASR 本身由 fileproc/asr_p2_test.go 覆盖）：
//   · M1 识别成功 ⇒ 文本进输入框，且**没有**调用 sendMessage（没自动发出去）；
//   · M2 用户随后点「翻译」⇒ 才带着刚回填的文本调用一次 sendMessage（回填不是终点）；
//   · M3 识别为空/纯空白 ⇒ 不回填也不发送（负向对照，别把空串当一次有效输入）。
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, cleanup, waitFor, fireEvent } from '@testing-library/react'
import { setLang, t } from '@/i18n'
import { ToastProvider } from '@/ui/langcross/src'

const mocks = vi.hoisted(() => ({
  micSupported: true,
  // 识别结果：M1/M2 用真实文本，M3 用空串
  asrText: '你好世界',
  chat: {
    messages: [] as any[], isLoading: false, isBackendOnline: true, isBackendLoading: false,
    isBackendChecking: false, errorMessage: '', selectedLangs: ['en'] as string[],
    setSelectedLangs: vi.fn(), sendMessage: vi.fn(), stopGeneration: vi.fn(),
    clearMessages: vi.fn(), retryHealth: vi.fn(),
  },
}))

vi.mock('@/hooks/useChat', async () => {
  const { createContext } = await import('react')
  return { useChat: () => mocks.chat, PkgRefreshCtx: createContext<unknown>(null) }
})
vi.mock('@/api', () => ({
  myPackage: vi.fn(async () => ({ success: false })),
  meContext: vi.fn(async () => ({ success: false })),
  getAuthToken: () => '', getActiveTenantId: () => 0,
}))
vi.mock('@/api/translate', () => ({ estimateTranslation: vi.fn(async () => null) }))
vi.mock('@/api/chatVoice', () => ({ micSupported: () => mocks.micSupported }))
// 引擎桩：start() 给一个"一 stop 就 resolve 成 mocks.asrText"的会话（不碰真网络/真麦克风）
vi.mock('@/api/asrEngines', () => ({
  getAsrEngine: async () => ({
    kind: 'server',
    isAvailable: async () => true,
    start: () => ({
      final: new Promise<string>((res) => {
        ;(globalThis as any).__resolveMic = () => res(mocks.asrText)
      }),
      stop() { (globalThis as any).__resolveMic?.() },
      cancel() {},
    }),
  }),
  ServerAsrEngine: class {},
}))
const cap = { tts: null as boolean | null, langs: null as string[] | null, asr: true as boolean | null, ocr: null as boolean | null }
vi.mock('@/api/voiceCapability', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api/voiceCapability')>()
  return { ...actual, useVoiceCapability: () => cap, probeVoiceCapability: () => Promise.resolve() }
})
vi.mock('./MessageBubble', () => ({ default: () => <div className="bubble-row" /> }))
vi.mock('./modals', () => ({ FeedbackModalFromMessage: () => null }))
vi.mock('@/components/LangMultiSelect', () => ({ default: () => <div data-stub="lang-select" />, LangChips: () => <div data-stub="lang-chips" /> }))
vi.mock('@/components/ModeToggle', () => ({ default: () => <div data-stub="mode-toggle" /> }))

import ChatWindow from './ChatWindow'
import { PkgRefreshCtx } from '@/hooks/useChat'

function renderWindow() {
  return render(
    <ToastProvider>
      <PkgRefreshCtx.Provider value={null as never}><ChatWindow /></PkgRefreshCtx.Provider>
    </ToastProvider>,
  )
}

// 点两次麦克风钮（开→停），等 React 把 micOn 重渲染出来后再停，保证 stopMic 真跑到。
async function recordOnce() {
  const { getByTestId } = renderWindow()
  await waitFor(() => expect(getByTestId('mic-btn')).toBeTruthy())
  fireEvent.click(getByTestId('mic-btn'))
  await waitFor(() => expect(getByTestId('mic-btn').getAttribute('aria-label')).toBe(t('chat.micStop')))
  fireEvent.click(getByTestId('mic-btn'))
  return getByTestId
}

describe('语音识别产物 · 回填输入框不自动发送（2026-10-19）', () => {
  beforeEach(() => {
    cleanup()
    setLang('zh')
    mocks.micSupported = true
    mocks.asrText = '你好世界'
    mocks.chat.sendMessage.mockClear()
    delete (globalThis as any).__resolveMic
    ;(Element.prototype as any).scrollTo = (Element.prototype as any).scrollTo || vi.fn()
  })

  it('M1 识别成功 ⇒ 文本进输入框，且未调用 sendMessage（没自动发出去）', async () => {
    const getByTestId = await recordOnce()
    await waitFor(() => {
      expect((getByTestId('translate-input') as HTMLTextAreaElement).value).toBe('你好世界')
    })
    expect(mocks.chat.sendMessage, '识别完不许自动发送，必须先回填输入框').not.toHaveBeenCalled()
  })

  it('M2 用户点「翻译」⇒ 才带着刚回填的文本发送一次', async () => {
    const getByTestId = await recordOnce()
    await waitFor(() => {
      expect((getByTestId('translate-input') as HTMLTextAreaElement).value).toBe('你好世界')
    })
    const sendBtn = document.querySelector('.cw-send') as HTMLButtonElement
    expect(sendBtn, '发送键应在场（回填后 canSend=true）').toBeTruthy()
    fireEvent.click(sendBtn)
    await waitFor(() => expect(mocks.chat.sendMessage).toHaveBeenCalledTimes(1))
    expect(mocks.chat.sendMessage.mock.calls[0][0]).toBe('你好世界')
  })

  it('M3 识别为空/纯空白 ⇒ 不回填也不发送（负向对照）', async () => {
    mocks.asrText = '   '
    const getByTestId = await recordOnce()
    // 给 resolve 后的微任务一拍
    await new Promise((r) => setTimeout(r, 0))
    expect((getByTestId('translate-input') as HTMLTextAreaElement).value).toBe('')
    expect(mocks.chat.sendMessage).not.toHaveBeenCalled()
  })
})
