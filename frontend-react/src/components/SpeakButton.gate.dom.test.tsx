// ============================================================================
// components/SpeakButton.gate.dom.test.tsx — 朗读钮的「渲染门」锁（★ 2026-10-16）
//
// 由来（用户报「播放语音译文报错」）：部署机没投放 TTS 权重时 /api/health.tts_ready=false，
// 朗读接口恒回 capability_missing ⇒ 客户点一次吃一句失败 toast。界面上摆着一颗点了必然失败的
// 按钮本身就是缺陷（同 ChatWindow 麦克风那条「浏览器不支持就不渲染这颗按钮」）。
//
// 本文件只锁**渲染门**（能力→有没有这颗按钮），落点三档：
//   · tts=false（服务端明确没有能力）⇒ **整颗不渲染**，且同排其它件（复制/反馈/读数）一件不少
//     ——「译文第一」：摘掉的是语音这一件，不是半排控件；
//   · tts=null（探不到，fail-open）⇒ 照常露出（真点了仍有本地化 toast 兜底，见 lib/voiceFail.ts）；
//   · tts=true ⇒ 露出。
// ★ 2026-10-16 追加第四/五档：能力还是**按语种**的（tts_langs 出服务端有效档：
//   中英档 zh/en ＋ 多语档 ja/ko/fr/de/es/pt/ru/ar）——
//   目标语种不在覆盖清单里时同样整颗不渲染（否则客户点一次就吃一句 unsupported_lang）；
//   清单未知时放行。
// 探针本身（TTL／失败不落状态／字段缺失不误判）由 api/voiceCapability.test.ts 覆盖，
// 两条射程互补：这里是「接线」，那边是「尺子」。
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, cleanup } from '@testing-library/react'
import { setLang } from '@/i18n'
import type { ChatMessage } from '@/types'

vi.mock('@/api', () => ({ API_BASE: '', getAuthToken: () => '', handleUnauthorized: vi.fn() }))

// 能力快照由本文件控制：hook 直读这里，不拨网络（探针行为在 voiceCapability.test.ts 里测）
// ★ 只替换"取快照"与"拨探针"两条：`ttsLangCovered` 用**模块真实实现**——
//   归一化那把尺子（zh-hant → zh）是前后端共用的，在接线测试里再抄一遍就等于把它从射程里摘掉
//   （同 §三「判据级断言代替不了守卫级」：本文件要判的是"门有没有接到那把尺子上"）。
const cap = { tts: null as boolean | null, langs: null as string[] | null, asr: null as boolean | null, ocr: null as boolean | null }
vi.mock('@/api/voiceCapability', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api/voiceCapability')>()
  return {
    ...actual,
    useVoiceCapability: () => cap,
    probeVoiceCapability: () => Promise.resolve(),
    voiceCapability: () => cap,
    subscribeVoiceCapability: () => () => {},
  }
})

import MessageBubble from './MessageBubble'

const mkMsg = (over: Partial<ChatMessage> = {}): ChatMessage => ({
  id: 'm1', role: 'assistant', content: '', timestamp: Date.now(), ...over,
})

// 一条「有结构化译文」的消息：朗读钮、复制钮、反馈钮都应到场
const mkFull = () => mkMsg({
  content: 'Hello world',
  points_used: 12,
  data: { translations: { en: 'Hello world' }, lang_names: { en: '英语' }, mode: '模型翻译（无知识库）' } as any,
})

describe('朗读钮渲染门（服务端 TTS 能力）', () => {
  beforeEach(() => { setLang('zh'); cleanup() })

  it('G1 tts=false：整颗朗读钮不渲染，同排其它件一件不少（译文第一）', () => {
    cap.tts = false
    const { queryByTestId, getByTestId } = render(<MessageBubble message={mkFull()} source="你好世界" />)
    expect(queryByTestId('speak-translation'), 'tts_ready=false 时不许渲染点了必然失败的朗读钮').toBeNull()
    // 正向对照：同一排里其余件都在（否则「摘掉一件」会退化成「摘掉半排」）
    expect(getByTestId('msg-actions')).toBeTruthy()
    expect(getByTestId('copy-translation')).toBeTruthy()
    expect(getByTestId('msg-meta')).toBeTruthy()
  })

  it('G2 tts=null（探不到）：fail-open 保留朗读钮', () => {
    cap.tts = null
    const { getByTestId } = render(<MessageBubble message={mkFull()} source="你好世界" />)
    expect(getByTestId('speak-translation'), '探不到能力时不许摘掉入口（探针坏了不等于能力没了）').toBeTruthy()
  })

  it('G3 tts=true：朗读钮在场', () => {
    cap.tts = true
    const { getByTestId } = render(<MessageBubble message={mkFull()} source="你好世界" />)
    expect(getByTestId('speak-translation')).toBeTruthy()
  })

  // ★ 2026-10-16 补：语音是**按语种**的能力（现役引擎 sherpa-onnx 中英档）。
  //   只有 tts 一个布尔时，日/俄/泰译文的同一排会摆出一颗点下去必然失败（unsupported_lang）的钮。
  it('G4 tts=true 但目标语种不在覆盖清单里：该语的朗读钮不渲染，同排其它件一件不少', () => {
    cap.tts = true
    cap.langs = ['zh', 'en']
    const ja = mkMsg({
      content: '你好世界',
      points_used: 12,
      data: { translations: { ja: 'こんにちは世界' }, lang_names: { ja: '日语' }, mode: '模型翻译（无知识库）' } as any,
    })
    const { queryByTestId, getByTestId } = render(<MessageBubble message={ja} source="你好世界" />)
    expect(queryByTestId('speak-translation'), '日语越出覆盖清单时不许渲染点了必然失败的朗读钮').toBeNull()
    // 正向对照：同排其余件都在（译文第一：摘掉的是语音这一件，不是半排控件）
    expect(getByTestId('msg-actions')).toBeTruthy()
    expect(getByTestId('copy-translation')).toBeTruthy()
    expect(getByTestId('msg-meta')).toBeTruthy()
  })

  it('G5 覆盖清单命中（含地域段 zh-hant）⇒ 渲染；langs 未知 ⇒ 放行（探不到不比"已知支持"更该摘）', () => {
    cap.tts = true
    cap.langs = ['zh', 'en']
    // 繁体译文（zh-hant）归一化到 zh ⇒ 属于覆盖清单
    const hant = mkMsg({
      content: '你好世界',
      data: { translations: { 'zh-Hant': '你好世界' }, lang_names: { 'zh-Hant': '繁体中文' }, mode: 'x' } as any,
    })
    const r1 = render(<MessageBubble message={hant} source="你好世界" />)
    expect(r1.getByTestId('speak-translation'), 'zh-Hant 必须归一化后判命中').toBeTruthy()
    cleanup()

    cap.langs = null
    const r2 = render(<MessageBubble message={mkFull()} source="你好世界" />)
    expect(r2.getByTestId('speak-translation'), '覆盖清单未知（探不到）时照旧露出入口').toBeTruthy()
  })
})
