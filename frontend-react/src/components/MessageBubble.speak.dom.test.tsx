// ============================================================================
// components/MessageBubble.speak.dom.test.tsx — 译文朗读的行为锁（★ 2026-10-21 UAT 收口 M8）
//
// 由来：UAT 报「播放语音译文报错」。只读探针已排除两类"能力缺失"（两站 tts_ready=true、
// 十个语种全在覆盖清单里），剩下三类发生在**点击之后的那几秒**里，而这一段过去没有任何锁：
//   ① 浏览器自动播放策略 —— 合成要几秒，await 之后 play() 已跨出手势窗口（Safari 尤甚），
//      旧写法 `new Audio(url)` 每次都新建元素、每次都丢授权 ⇒ NotAllowedError 被当成"服务器出错"；
//   ② 没有任何超时 —— fetch 默认不超时，上游一卡按钮就永远 loading，用户只能刷新；
//   ③ 加载态文案串档 —— 点"朗读"却显示「正在识别录音…」。
// 本文件锁的就是这三条 + 解码失败要出声（旧形态静默回 idle，与"没合成"在屏幕上同形）。
//
// ★ 判据取向：S1 判的是**调用顺序**（解锁必须在 speakAloud 的 promise 落地之前同步发生），
//   不是"play() 被调过"——后者在纯异步写法下同样为真，等于把这条锁摘成空转。
// ★ 每条腿都配一枚"故意写坏必红"的反证说明（见各 it 的注释）；反证方式＝把对应那一行
//   改成旧形态后重跑，红必须落在这一条腿、其余腿仍绿。
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, cleanup, fireEvent, act } from '@testing-library/react'
import { setLang, t } from '@/i18n'
import type { ChatMessage } from '@/types'

vi.mock('@/api', () => ({ API_BASE: '', getAuthToken: () => '', handleUnauthorized: vi.fn() }))

// 朗读请求由本文件控制：默认挂起，由用例决定何时兑现/拒绝（要判"落地之前"的顺序）。
const voice = {
  resolve: null as null | ((b: Blob) => void),
  reject: null as null | ((e: unknown) => void),
  calls: 0,
}
vi.mock('@/api/chatVoice', async (importOriginal) => {
  // ★ VoiceError 必须用**真实类**：voiceFailText 的判档是 `err instanceof VoiceError`，
  //   整体替换成假模块会让那把尺子在测试里恒不命中（判据假红/假绿的经典形态）。
  const actual = await importOriginal<typeof import('@/api/chatVoice')>()
  return {
    ...actual,
    // 每次调用新建一个挂起的 promise，把两口子交给用例
    speakAloud: vi.fn(() => {
      voice.calls++
      return new Promise<Blob>((res, rej) => { voice.resolve = res; voice.reject = rej })
    }),
  }
})

// 能力门放行（tts=true、语种清单未知）：本文件测的是"点击之后"，不是"渲染门"（那是 SpeakButton.gate）。
vi.mock('@/api/voiceCapability', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api/voiceCapability')>()
  const cap = { tts: true as boolean | null, langs: null as string[] | null, asr: null as boolean | null, ocr: null as boolean | null }
  return {
    ...actual,
    useVoiceCapability: () => cap,
    probeVoiceCapability: () => Promise.resolve(),
    voiceCapability: () => cap,
    subscribeVoiceCapability: () => () => {},
  }
})

// toast 只收文案：把两条出口都换成 spy（本文件不测渲染通道，只测"说了哪一句"）。
const toasts = { warn: vi.fn(), error: vi.fn() }
vi.mock('@/lib/toastBus', () => ({
  toast: vi.fn(),
  toastWarn: (arg: unknown) => toasts.warn(arg),
  toastError: (arg: unknown) => toasts.error(arg),
}))

import { VoiceError } from '@/api/chatVoice'
import MessageBubble from './MessageBubble'

const mkFull = (): ChatMessage => ({
  id: 'm1', role: 'assistant', timestamp: Date.now(),
  content: 'Hello world',
  points_used: 12,
  data: { translations: { en: 'Hello world' }, lang_names: { en: '英语' }, mode: '模型翻译（无知识库）' } as any,
})

// jsdom 没有媒体播放实现：给 <audio> 装一枚可编排的 play()/pause()。
// play 的返回是 promise（产品代码 await 它），拒绝即模拟浏览器拒绝。
const media = { play: vi.fn<() => Promise<void>>(() => Promise.resolve()), pause: vi.fn<() => void>() }
let origPlay: unknown
let origPause: unknown
let origCreate: unknown
let origRevoke: unknown
let createElSpy: ReturnType<typeof vi.spyOn> | null = null

beforeEach(() => {
  setLang('zh')
  voice.resolve = null
  voice.reject = null
  voice.calls = 0
  toasts.warn.mockReset()
  toasts.error.mockReset()
  media.play.mockReset()
  media.play.mockImplementation(() => Promise.resolve())
  media.pause.mockReset()
  origPlay = HTMLMediaElement.prototype.play
  origPause = HTMLMediaElement.prototype.pause
  HTMLMediaElement.prototype.play = media.play
  HTMLMediaElement.prototype.pause = media.pause
  origCreate = URL.createObjectURL
  origRevoke = URL.revokeObjectURL
  URL.createObjectURL = vi.fn(() => 'blob:fake-audio')
  URL.revokeObjectURL = vi.fn()
  // 抓住产品创建的那只 <audio>：S4 要直接拨它的 onerror。`new Audio()` 在 jsdom 里
  // 走的就是 document.createElement('audio')，所以这里能拿到同一个实例（不改产品代码）。
  ;(globalThis as unknown as { __lastAudio?: HTMLAudioElement }).__lastAudio = undefined
  const origCreateEl = document.createElement.bind(document)
  createElSpy = vi.spyOn(document, 'createElement').mockImplementation(((tag: string, opts?: ElementCreationOptions) => {
    const el = origCreateEl(tag, opts)
    if (String(tag).toLowerCase() === 'audio') {
      ;(globalThis as unknown as { __lastAudio?: HTMLAudioElement }).__lastAudio = el as HTMLAudioElement
    }
    return el
  }) as typeof document.createElement)
})

afterEach(() => {
  cleanup()
  createElSpy?.mockRestore()
  createElSpy = null
  HTMLMediaElement.prototype.play = origPlay as () => Promise<void>
  HTMLMediaElement.prototype.pause = origPause as () => void
  URL.createObjectURL = origCreate as typeof URL.createObjectURL
  URL.revokeObjectURL = origRevoke as typeof URL.revokeObjectURL
})

// clickSpeak 点一次朗读钮并把 onClick 里那条 async 链跑到当前 await 点。
async function clickSpeak(el: HTMLElement) {
  await act(async () => { fireEvent.click(el) })
}

describe('译文朗读：加载态文案与手势解锁（★ M8）', () => {
  it('S1 加载态用朗读专用键；解锁 play() 必须发生在合成落地之前（同一拍里）', async () => {
    // 反证：把解锁那段挪到 `await speakAloud` 之后 ⇒ 本条的 calls 顺序判红（那时播放已跨出手势）。
    const { getByTestId } = render(<MessageBubble message={mkFull()} source="你好世界" />)
    const btn = getByTestId('speak-translation')

    await clickSpeak(btn)

    // 文案：loading 必须说"合成语音"，不许复用录音那条（串档是本批 UAT 的下一条读数）
    expect(btn.getAttribute('aria-label')).toBe(t('chat.speakLoading'))
    expect(btn.getAttribute('aria-label'), '朗读加载态不许复用「正在识别录音…」').not.toBe(t('chat.micWorking'))
    // 请求已发出、尚未落地
    expect(voice.calls).toBe(1)
    // ★ 顺序判据：此刻合成还没回来，play() 已经调过一次（静音样本解锁）
    expect(media.play.mock.calls.length, '解锁必须同步发生在点击处理器里，不能等合成回来').toBe(1)
  })

  it('S2 合成落地后换真实音频并播；按钮转「停止朗读」', async () => {
    const { getByTestId } = render(<MessageBubble message={mkFull()} source="你好世界" />)
    const btn = getByTestId('speak-translation')
    await clickSpeak(btn)

    await act(async () => { voice.resolve!(new Blob(['x'], { type: 'audio/wav' })) })

    expect(URL.createObjectURL).toHaveBeenCalled()
    // 第一次是静音解锁、第二次是真实音频
    expect(media.play.mock.calls.length).toBe(2)
    expect(btn.getAttribute('aria-label')).toBe(t('chat.speakStop'))
  })
})

describe('译文朗读：失败分档（★ M8）', () => {
  it('S3 浏览器拒绝播放（NotAllowedError）⇒ 提示"再点一次"，且缓存件在第二次点击同一拍就能响', async () => {
    // 反证：把 NotAllowedError 分支合并进默认 voiceFailText ⇒ 本条的 speakTapAgain 断言判红。
    // 反证：去掉 cacheRef 命中分支 ⇒ 第二次播放会落在 await 之后，第二次的 play 同步计数判红。
    const { getByTestId } = render(<MessageBubble message={mkFull()} source="你好世界" />)
    const btn = getByTestId('speak-translation')

    // 第一次点击：静音解锁成功；合成回来后真正播放被浏览器拒绝
    media.play.mockImplementationOnce(() => Promise.resolve()) // 解锁
    media.play.mockImplementationOnce(() => Promise.reject(Object.assign(new Error('blocked'), { name: 'NotAllowedError' })))
    await clickSpeak(btn)
    await act(async () => { voice.resolve!(new Blob(['x'], { type: 'audio/wav' })) })

    expect(toasts.warn, '手势被拒必须给一句可行动提示').toHaveBeenCalledWith(t('chat.speakTapAgain'))
    expect(btn.getAttribute('aria-label'), '被拒后回到可再点的地面态').toBe(t('chat.speak'))

    // 第二次点击：命中缓存 ⇒ 同一个元素、点击同一拍里 play()，不需要再合成
    media.play.mockReset()
    media.play.mockImplementation(() => Promise.resolve())
    await clickSpeak(btn)

    expect(voice.calls, '缓存命中不许再拨一次合成').toBe(1)
    expect(media.play.mock.calls.length, '缓存命中必须在点击这一拍里 play()').toBe(1)
    expect(btn.getAttribute('aria-label')).toBe(t('chat.speakStop'))
  })

  it('S4 音频解码失败（onerror）⇒ 出声并回到地面态（旧形态静默闪一下）', async () => {
    // 反证：把 a.onerror 那行摘掉 ⇒ 本条的红落在"没有 toast"这一条腿。
    const { getByTestId } = render(<MessageBubble message={mkFull()} source="你好世界" />)
    const btn = getByTestId('speak-translation')
    await clickSpeak(btn)
    await act(async () => { voice.resolve!(new Blob(['bad'], { type: 'audio/wav' })) })
    expect(btn.getAttribute('aria-label')).toBe(t('chat.speakStop'))

    // 直接触发那只共享 audio 元素的 onerror（模拟浏览器解码失败）
    const audio = (globalThis as unknown as { __lastAudio?: HTMLAudioElement }).__lastAudio
    expect(audio, '产品必须复用同一只 audio 元素（新建元素会丢手势授权，S3 已判）').toBeTruthy()
    await act(async () => { audio!.onerror?.(new Event('error')) })

    expect(toasts.warn).toHaveBeenCalledWith(t('chat.voiceUnavailable'))
    expect(btn.getAttribute('aria-label')).toBe(t('chat.speak'))
  })

  it('S5 合成超时（reason=timeout）走本地化档位文案，不把后端中文话术直接 toast 给访客', async () => {
    // 反证：把 catch 里那句改成 toastWarn(String(e)) ⇒ 本条判红（拿到的是内部话术而非词典句）。
    const { getByTestId } = render(<MessageBubble message={mkFull()} source="你好世界" />)
    const btn = getByTestId('speak-translation')
    await clickSpeak(btn)

    const err = new VoiceError('tts-timeout', 'timeout')
    await act(async () => { voice.reject!(err) })

    expect(toasts.warn).toHaveBeenCalledWith(t('chat.voiceTimeout'))
    expect(toasts.warn.mock.calls[0][0], '不许把后端内部话术原样交给访客').not.toContain('tts-timeout')
    expect(btn.getAttribute('aria-label')).toBe(t('chat.speak'))
  })
})
