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
  // ★ S7（G11）：引擎 start 收到的 maxSec 流水（档位接线唯一可读的证据）
  seenMaxSec: [] as number[],
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
const apiMocks = vi.hoisted(() => ({
  // 返回值显式放宽成 any：S7 要按用例改回**带 instant_tier 的形态**，
  // 而推成 {success:boolean} 的窄类型会让那条入参在 tsc 期就红（判据本身没变）。
  myPackage: vi.fn(async (): Promise<any> => ({ success: false })),
  meContext: vi.fn(async (): Promise<any> => ({ success: false })),
}))
vi.mock('@/api', () => ({
  myPackage: apiMocks.myPackage, meContext: apiMocks.meContext,
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
    start: (h: { onState?: (s: string) => void; maxSec?: number }) => {
      // ★ S7：记下调用方按档传进来的时长上限（-1＝根本没传，那正是 G11 要抓的形态）
      mocks.seenMaxSec.push(Number(h.maxSec ?? -1))
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
import { applyInstantCaps, resetInstantCaps } from '@/lib/instantTier'

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
    mocks.seenMaxSec.length = 0
    apiMocks.myPackage.mockResolvedValue({ success: false })
    apiMocks.meContext.mockResolvedValue({ success: false })
    resetInstantCaps()
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

  it('S7 录音时长按档传进引擎（G11）：免费 30／付费 60／运营改档即跟（30/60 不是写死的）', async () => {
    // ① 默认（档位取不到＝保守按免费档）⇒ 引擎必须收到 30
    const a = renderWindow()
    await waitFor(() => expect(a.getByTestId('mic-btn')).toBeTruthy())
    // 地面态的 title 必须先读**点击之前**那一次：录音中它换成"录音中 Ns，点击停止"，
    // 按下去之后再问就永远拿不到档位数字（这条判据会退化成恒红）。
    expect(a.getByTestId('mic-btn').getAttribute('title'), '未录音时的 title 必须点名本档时长上限')
      .toContain('30')
    fireEvent.click(a.getByTestId('mic-btn'))
    await waitFor(() => expect(mocks.seenMaxSec.length).toBeGreaterThan(0))
    expect(mocks.seenMaxSec[0], '免费档必须把 30 秒传给引擎（0/-1＝压根没按档传）').toBe(30)
    cleanup()

    // ② 付费档 ⇒ 60 秒（这条判的是"分档"，不是"上限更大"那种单向锁）
    apiMocks.meContext.mockResolvedValue({ success: true, instant_tier: 'paid' })
    const b = renderWindow()
    await waitFor(() => expect(b.getByTestId('mic-btn')).toBeTruthy())
    fireEvent.click(b.getByTestId('mic-btn'))
    await waitFor(() => expect(mocks.seenMaxSec.length).toBeGreaterThan(1))
    expect(mocks.seenMaxSec[1], '付费档必须走 60 秒那一档').toBe(60)
    cleanup()

    // ③ 运营把硬顶改成 90 秒 ⇒ 同一个界面立刻跟到新档（S2 跨面同尺；
    //    写死 30/60 的实现在这一枪必红——那正是"运营调档、客户还被按旧档掐"的现网形态）
    applyInstantCaps({ audio_max_sec: 90, free_audio_max_sec: 45 })
    const c = renderWindow()
    await waitFor(() => expect(c.getByTestId('mic-btn')).toBeTruthy())
    fireEvent.click(c.getByTestId('mic-btn'))
    await waitFor(() => expect(mocks.seenMaxSec.length).toBeGreaterThan(2))
    expect(mocks.seenMaxSec[2], 'caps 现值必须覆盖出厂默认（付费档 90 秒）').toBe(90)
  })

  it('S8 到点前三秒的倒计时与告警色（G11 第二半：不许到点才让人发现）', async () => {
    // 把免费档压到 4 秒（经**现读覆盖层**，不改产品默认），这样第一次 1s 心跳就已进入"剩 3 秒"那一档。
    // 为什么用覆盖而不是等 27 秒：判据问的是"剩余 ≤3 时换不换脸"，与出厂数字无关；
    // 等 27 秒只会把这条腿变成 flaky（AGENTS §三「判据过时/预算」那一族的反面教材）。
    applyInstantCaps({ free_audio_max_sec: 4 })
    const { getByTestId } = renderWindow()
    await waitFor(() => expect(getByTestId('mic-btn')).toBeTruthy())
    fireEvent.click(getByTestId('mic-btn'))
    await waitFor(() => expect(getByTestId('mic-btn').getAttribute('aria-label')).toBe(t('chat.micStop')))
    expect(getByTestId('mic-elapsed').textContent, '第 0 拍还是常规读数（不该一上来就告警）').toBe('0s')
    await waitFor(() => {
      const el = getByTestId('mic-elapsed')
      expect(el.className, '剩余 ≤3 秒必须切到告警色 class（cw-mic-warn）').toContain('cw-mic-warn')
      expect(el.textContent, '剩余 ≤3 秒必须改念倒计时，不再是"已录几秒"').toBe(tpl('chat.micCountdown', { sec: 3 }))
    }, { timeout: 6000 })
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
