// ============================================================================
// ChatWindow.micNoSpeech.dom.test.tsx — 麦克风「静音」与「识别失败」分开说（★ 2026-10-19 G11）
//
// 由来：纯静音/极低能量的录音过去交给模型会**幻觉**出一句话填进输入框（G11 的本体在
//   服务端 asr_transcribe.py 的 detect_no_speech 门）。服务端拦下后经 asrReason 出一档
//   `no_speech`，客户侧的可行动作与「识别失败」不同（前者＝靠近麦克风再说一次，后者＝
//   重试或改用键盘）——所以界面必须按档位分开说，而不是一律「识别失败」。
// 本文件锁组件侧那一腿（服务端门由 fileproc capability_shape_selftest L24 覆盖）：
//   · M1 reason='no_speech' ⇒ toast 取 no_speech 那一句（≠ 识别失败那句）；
//   · M2 reason='failed' ⇒ toast 仍取既有 chat.micFailed（负向对照：别把两档并成一档）。
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, cleanup, waitFor, fireEvent } from '@testing-library/react'
import { setLang, t, textOr } from '@/i18n'
import { ToastProvider } from '@/ui/langcross/src'

const mocks = vi.hoisted(() => ({
  micSupported: true,
  rejectWith: null as null | { reason?: string },
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
// 引擎桩：start() 立即给一个"一 stop 就按当前档位 reject"的会话（不碰真网络/真麦克风）
vi.mock('@/api/asrEngines', () => ({
  getAsrEngine: async () => ({
    kind: 'server',
    isAvailable: async () => true,
    start: () => ({
      final: new Promise<string>((_res, rej) => {
        // 在 stop() 里 reject：与真实 ServerAsrEngine 的时序一致（stop 后才上传/识别）
        ;(globalThis as any).__rejectMic = () => rej(mocks.rejectWith)
      }),
      stop() { (globalThis as any).__rejectMic?.() },
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

// 点两次麦克风钮（开→停），stop 时按 mocks.rejectWith 出档
async function tapMic() {
  const { getByTestId } = render(
    <ToastProvider>
      <PkgRefreshCtx.Provider value={null as never}><ChatWindow /></PkgRefreshCtx.Provider>
    </ToastProvider>,
  )
  await waitFor(() => expect(getByTestId('mic-btn')).toBeTruthy())
  fireEvent.click(getByTestId('mic-btn'))
  // 两次点击之间必须等 React 把 micOn 重渲染出来：同步连点两下会两个都走 startMic
  //   （闭包里 micOn 仍是 false），stopMic 一次都不跑，这条锁会变成空转。
  await waitFor(() => expect(getByTestId('mic-btn').getAttribute('aria-label')).toBe(t('chat.micStop')))
  fireEvent.click(getByTestId('mic-btn'))
  const toast = await waitFor(() => {
    const el = document.querySelector('.lc-toast__title')
    expect(el).toBeTruthy()
    return el as HTMLElement
  })
  return toast.textContent || ''
}
const NO_SPEECH_FALLBACK = '没有听到语音，请靠近麦克风、确认已授权后再说一次'

describe('麦克风失败文案 · 按档位分开（G11）', () => {
  beforeEach(() => {
    cleanup()
    setLang('zh')
    mocks.micSupported = true
    mocks.rejectWith = null
    delete (globalThis as any).__rejectMic
    ;(Element.prototype as any).scrollTo = (Element.prototype as any).scrollTo || vi.fn()
  })

  it('M1 reason=no_speech ⇒ 走「没听到语音」那一句（不是识别失败那句）', async () => {
    mocks.rejectWith = { reason: 'no_speech' }
    const title = await tapMic()
    expect(title, 'no_speech 档没走专门文案').toBe(textOr('chat.micNoSpeech', NO_SPEECH_FALLBACK))
    expect(title, 'no_speech 与识别失败必须分开说').not.toBe(t('chat.micFailed'))
  })

  it('M2 reason=failed ⇒ 仍走既有 chat.micFailed（负向对照）', async () => {
    mocks.rejectWith = { reason: 'failed' }
    const title = await tapMic()
    expect(title).toBe(t('chat.micFailed'))
  })
})
