// ============================================================================
// ChatWindow.micCapability.dom.test.tsx — 麦克风「服务端 ASR 能力门」（★ 2026-10-16 能力门批）
//
// 由来：麦克风这条链的上游是**服务端** ASR（部署机没装 sherpa-onnx/ffmpeg 时
//   /api/health.asr_ready=false，识别接口恒回 capability_missing）。组件原先只问浏览器
//   （micSupported()＝MediaRecorder 有没有），于是新机器上摆着一颗点了必然失败的按钮——
//   与「浏览器不支持就不渲染这颗按钮」是同一条口径，只是判据在服务端。
//
// 落点四档（与朗读钮 gate、工单图片/音频门同一把尺子，见 api/voiceCapability.ts）：
//   · asr=false（服务端明确没有能力）⇒ 整颗不渲染，同排其余件（输入框/发送键）一件不少；
//   · asr=null（探不到，fail-open）⇒ 照常渲染（探针坏了不等于能力没了）；
//   · asr=true ⇒ 渲染；
//   · 浏览器门没被顶掉：micSupported()=false 时即使 asr=true 也必须不渲染。
// 探针本身（TTL／失败不落状态／字段形态）由 api/voiceCapability.test.ts 覆盖，两条射程互补。
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, cleanup, waitFor } from '@testing-library/react'
import { setLang } from '@/i18n'
import { ToastProvider } from '@/ui/langcross/src'

// 浏览器门开关：本文件要单独测服务端那一档，所以把「浏览器支持吗」钉成可控
const mocks = vi.hoisted(() => ({
  micSupported: true,
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
// 浏览器门：MediaRecorder 在 jsdom 里根本不存在，不桩化则 canMic 恒假 ⇒ 服务端那一档测不到
vi.mock('@/api/chatVoice', () => ({ micSupported: () => mocks.micSupported }))
vi.mock('@/api/asrEngines', () => ({ getAsrEngine: vi.fn(), ServerAsrEngine: class {} }))
// 服务端能力快照：只替换「取快照/拨探针」，其余用真实实现（不拨网络）
const cap = { tts: null as boolean | null, langs: null as string[] | null, asr: null as boolean | null, ocr: null as boolean | null }
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

describe('麦克风渲染门（服务端 ASR 能力 × 浏览器支持）', () => {
  beforeEach(() => {
    cleanup()
    setLang('zh')
    mocks.micSupported = true
    cap.asr = null
    localStorage.removeItem('translate_mode')
    ;(Element.prototype as any).scrollTo = (Element.prototype as any).scrollTo || vi.fn()
  })

  it('M1 asr=false：麦克风整颗不渲染，同排发送键与输入框一件不少', async () => {
    cap.asr = false
    const { queryByTestId, getByTestId } = renderWindow()
    await waitFor(() => expect(getByTestId('translate-input')).toBeTruthy())
    expect(queryByTestId('mic-btn'), 'asr_ready=false 时不许渲染点了必然失败的麦克风').toBeNull()
    // 正向对照：摘掉的是语音这一件，不是整条工具条
    expect(getByTestId('translate-input')).toBeTruthy()
  })

  it('M2 asr=null（探不到）：fail-open 保留麦克风', async () => {
    cap.asr = null
    const { getByTestId } = renderWindow()
    await waitFor(() => expect(getByTestId('mic-btn'), '探不到能力时不许摘掉入口').toBeTruthy())
  })

  it('M3 asr=true：麦克风在场', async () => {
    cap.asr = true
    const { getByTestId } = renderWindow()
    await waitFor(() => expect(getByTestId('mic-btn')).toBeTruthy())
  })

  it('M4 浏览器门仍在：micSupported()=false 时即使 asr=true 也不渲染（新门不许顶掉旧门）', async () => {
    cap.asr = true
    mocks.micSupported = false
    const { queryByTestId, getByTestId } = renderWindow()
    await waitFor(() => expect(getByTestId('translate-input')).toBeTruthy())
    expect(queryByTestId('mic-btn'), '浏览器不支持录音时同样不许渲染').toBeNull()
  })
})
