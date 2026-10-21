// ============================================================================
// ChatWindow.micStates.dom.test.tsx — 麦克风「四态」与出结果出声（★ 2026-10-21 UAT 收口 M7）
//
// 由来：UAT 07M3 实测读到两件事——
//   ① 真开麦失败（权限被拒/设备被占）时按钮已经先变成「停止录音」：旧写法在 start() 之后
//      **乐观** setMicOn(true)，而引擎随后报 'failed' 只弹一句 toast，屏幕上的形态仍是"在录"；
//   ② 识别成功只是把文字塞进输入框，界面零反馈；空结果走 resolve('') 那条**静默**分支，一句话都没有。
// 本文件锁组件侧四件事（引擎侧识别本身由服务端 asr 用例与 micFill/micNoSpeech 覆盖）：
//   S1 引擎不回报 ⇒ 不进入录音态（乐观置真已删，负向对照）；
//   S2 引擎报 'recording' ⇒ 停止方块 + 语义红状态 class + 计时读数 + 带秒数 title；
//   S3 停止后识别中 ⇒ 按钮禁用 + 转圈（这时再点没有语义）；
//   S4/S5 回填与空结果各自出声（success / warn 两档，文案取自词典）；
//   S6 引擎报 'denied' ⇒ 回到地面态 + 权限被拒那一句（不是"识别失败"）。
// 反证思路：S1 就是"把乐观置真写回去"的变异体——它一旦回归，S1 立刻红而 S2 仍绿（射程分开）。
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, cleanup, waitFor, fireEvent } from '@testing-library/react'
import { setLang, t, tpl } from '@/i18n'
import { ToastProvider } from '@/ui/langcross/src'

const mocks = vi.hoisted(() => ({
  micSupported: true,
  // 引擎回报档：recording＝正常开麦；silent＝什么都不报（乐观置真的反证靶子）；
  // denied/failed＝开麦失败两档。
  emit: 'recording' as 'recording' | 'silent' | 'denied' | 'failed',
  // 识别结果（S4 用真实文本 / S5 用空串）
  asrText: '你好世界',
  // 停止后是否挂住 final（S3 要观察"识别中"那一跳）
  holdStop: false,
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
// 引擎桩：按 mocks.emit 在 start() 内同步回报一档（与真实引擎"开麦即报"的时序一致），
// final 由 __resolveMic 手动放行，便于 S3 停在"识别中"。
vi.mock('@/api/asrEngines', () => ({
  getAsrEngine: async () => ({
    kind: 'server',
    isAvailable: async () => true,
    start: (h: { onState?: (s: string) => void }) => {
      if (mocks.emit !== 'silent') h.onState?.(mocks.emit)
      return {
        final: new Promise<string>((res) => {
          ;(globalThis as any).__resolveMic = () => res(mocks.asrText)
        }),
        stop() {
          if (!mocks.holdStop) (globalThis as any).__resolveMic?.()
        },
        cancel() {},
      }
    },
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

/** 读当前第一枚 toast 的标题（出结果出声那一腿）。 */
async function firstToast(): Promise<string> {
  const el = await waitFor(() => {
    const n = document.querySelector('.lc-toast__title')
    expect(n).toBeTruthy()
    return n as HTMLElement
  })
  return el.textContent || ''
}

describe('麦克风四态 · 录音/识别中/回填/空结果（M7）', () => {
  beforeEach(() => {
    cleanup()
    setLang('zh')
    mocks.micSupported = true
    mocks.emit = 'recording'
    mocks.asrText = '你好世界'
    mocks.holdStop = false
    mocks.chat.sendMessage.mockClear()
    delete (globalThis as any).__resolveMic
    ;(Element.prototype as any).scrollTo = (Element.prototype as any).scrollTo || vi.fn()
  })

  it('S1 引擎不回报 recording ⇒ 不进入录音态（乐观置真已删）', async () => {
    mocks.emit = 'silent'
    const { getByTestId } = renderWindow()
    await waitFor(() => expect(getByTestId('mic-btn')).toBeTruthy())
    fireEvent.click(getByTestId('mic-btn'))
    // 给引擎回报留一拍（若实现退回归到乐观置真，这一拍后就会翻成"停止录音"）
    await new Promise((r) => setTimeout(r, 0))
    const btn = getByTestId('mic-btn')
    expect(btn.getAttribute('aria-label'), '引擎没报 recording 就不许显示停止态').toBe(t('chat.mic'))
    expect(btn.className).not.toContain('cw-mic-rec')
    expect(document.querySelector('[data-testid="mic-elapsed"]'), '没在录就不该有计时').toBeNull()
  })

  it('S2 引擎报 recording ⇒ 停止方块 + 状态 class + 计时 + 带秒数 title', async () => {
    const { getByTestId } = renderWindow()
    await waitFor(() => expect(getByTestId('mic-btn')).toBeTruthy())
    fireEvent.click(getByTestId('mic-btn'))
    await waitFor(() => expect(getByTestId('mic-btn').getAttribute('aria-label')).toBe(t('chat.micStop')))
    const btn = getByTestId('mic-btn')
    expect(btn.className, '录音态必须有语义红状态 class').toContain('cw-mic-rec')
    expect(btn.getAttribute('title')).toBe(tpl('chat.micRecording', { sec: 0 }))
    expect(getByTestId('mic-elapsed').textContent, '计时读数从 0s 起').toBe('0s')
  })

  it('S3 停止后识别中 ⇒ 按钮禁用 + 转圈（不可再点）', async () => {
    mocks.holdStop = true
    const { getByTestId } = renderWindow()
    await waitFor(() => expect(getByTestId('mic-btn')).toBeTruthy())
    fireEvent.click(getByTestId('mic-btn'))
    await waitFor(() => expect(getByTestId('mic-btn').getAttribute('aria-label')).toBe(t('chat.micStop')))
    fireEvent.click(getByTestId('mic-btn'))
    await waitFor(() => expect((getByTestId('mic-btn') as HTMLButtonElement).disabled).toBe(true))
    expect(getByTestId('mic-btn').querySelector('.cw-spin'), '识别中要给转圈而不是空白').toBeTruthy()
    // 放行识别，收尾避免悬挂 promise
    ;(globalThis as any).__resolveMic?.()
  })

  it('S4 识别成功 ⇒ 回填输入框并出声（且不自动发送）', async () => {
    const { getByTestId } = renderWindow()
    await waitFor(() => expect(getByTestId('mic-btn')).toBeTruthy())
    fireEvent.click(getByTestId('mic-btn'))
    await waitFor(() => expect(getByTestId('mic-btn').getAttribute('aria-label')).toBe(t('chat.micStop')))
    fireEvent.click(getByTestId('mic-btn'))
    await waitFor(() => {
      expect((getByTestId('translate-input') as HTMLTextAreaElement).value).toBe('你好世界')
    })
    expect(await firstToast(), '回填后必须给一句"已填入"的出声提示').toBe(t('chat.micFilled'))
    expect(mocks.chat.sendMessage, '回填不是终点，不许自动发送').not.toHaveBeenCalled()
  })

  it('S5 识别为空 ⇒ 按「没听到声音」出声，不回填不发送', async () => {
    mocks.asrText = ''
    const { getByTestId } = renderWindow()
    await waitFor(() => expect(getByTestId('mic-btn')).toBeTruthy())
    fireEvent.click(getByTestId('mic-btn'))
    await waitFor(() => expect(getByTestId('mic-btn').getAttribute('aria-label')).toBe(t('chat.micStop')))
    fireEvent.click(getByTestId('mic-btn'))
    expect(await firstToast()).toBe(t('chat.micNoSpeech'))
    expect((getByTestId('translate-input') as HTMLTextAreaElement).value).toBe('')
    expect(mocks.chat.sendMessage).not.toHaveBeenCalled()
  })

  it('S6 开麦被拒 ⇒ 回到地面态并提示权限（不是识别失败）', async () => {
    mocks.emit = 'denied'
    const { getByTestId } = renderWindow()
    await waitFor(() => expect(getByTestId('mic-btn')).toBeTruthy())
    fireEvent.click(getByTestId('mic-btn'))
    expect(await firstToast()).toBe(t('chat.micDenied'))
    const btn = getByTestId('mic-btn')
    expect(btn.getAttribute('aria-label')).toBe(t('chat.mic'))
    expect(btn.className).not.toContain('cw-mic-rec')
  })
})
