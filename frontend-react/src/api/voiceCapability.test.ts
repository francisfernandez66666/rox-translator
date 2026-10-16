// ============================================================================
// api/voiceCapability.test.ts — 服务端语音能力探针的尺子（★ 2026-10-16）
//
// 锁的是「探针自己」的行为，与 SpeakButton.gate.dom.test.tsx 的「接线」互补：
//   P1 tts_ready=true/false 如实落到快照；
//   P2 字段缺失／形态不对 ⇒ 保持「未知」（把"读不到"写成"不可用"会无端摘掉入口）；
//   P3 网络失败／非 2xx ⇒ 保持「未知」，且**不写状态**（下一次调用必须重拨——一次抖动
//      不该把「这个站永远没有语音」钉死，同 ensureLocale 那条「失败不写状态」）；
//   P4 TTL 内不重复拨网络；force 能强制重拨；
//   P5 值没变不发通知（emit 只对变化发，否则每个气泡空转重渲染）。
// 运行：npx vitest run src/api/voiceCapability.test.ts
// ============================================================================
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import {
  probeVoiceCapability, voiceCapability, subscribeVoiceCapability, resetVoiceCapability,
} from './voiceCapability'

// healthReply 造一个 /api/health 响应
const healthReply = (body: unknown, ok = true) => ({
  ok,
  status: ok ? 200 : 503,
  json: async () => body,
}) as unknown as Response

describe('语音能力探针（/api/health.tts_ready）', () => {
  let calls = 0
  let impl: () => Response

  beforeEach(() => {
    resetVoiceCapability()
    calls = 0
    impl = () => healthReply({ tts_ready: true })
    vi.stubGlobal('fetch', vi.fn(async () => { calls++; return impl() }))
  })
  afterEach(() => { vi.unstubAllGlobals(); resetVoiceCapability() })

  it('P1 true／false 如实落到快照', async () => {
    await probeVoiceCapability(true)
    expect(voiceCapability().tts).toBe(true)

    resetVoiceCapability()
    impl = () => healthReply({ tts_ready: false })
    await probeVoiceCapability(true)
    expect(voiceCapability().tts).toBe(false)
  })

  it('P2 字段缺失或非布尔 ⇒ 保持未知（宁可露出按钮，也不误摘入口）', async () => {
    impl = () => healthReply({})
    await probeVoiceCapability(true)
    expect(voiceCapability().tts, '缺 tts_ready 时不许推断成不可用').toBeNull()

    impl = () => healthReply({ tts_ready: 'false' })
    await probeVoiceCapability(true)
    expect(voiceCapability().tts, '字符串 "false" 不是布尔，必须按未知处理').toBeNull()
  })

  it('P3 网络失败／非 2xx ⇒ 保持未知且不写状态（下一次照旧重拨）', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => { calls++; throw new Error('offline') }))
    await probeVoiceCapability(true)
    expect(voiceCapability().tts).toBeNull()

    // 「失败不写状态」的判据：紧接着再拨一次必须真的又打了一次网络（而不是被判进 TTL 缓存）
    calls = 0
    impl = () => healthReply({ tts_ready: true })
    vi.stubGlobal('fetch', vi.fn(async () => { calls++; return impl() }))
    await probeVoiceCapability()
    expect(calls, '探针失败后不许把"未知"当成已探过的档位缓存住').toBe(1)
    expect(voiceCapability().tts).toBe(true)

    // 非 2xx 同口径
    resetVoiceCapability()
    vi.stubGlobal('fetch', vi.fn(async () => { calls++; return healthReply({ tts_ready: true }, false) }))
    await probeVoiceCapability(true)
    expect(voiceCapability().tts).toBeNull()
  })

  it('P4 TTL 内不重复拨网络；force 能强制重拨', async () => {
    await probeVoiceCapability(true)
    expect(calls).toBe(1)
    calls = 0
    await probeVoiceCapability()
    expect(calls, 'TTL 内第二次调用不该再打网络').toBe(0)
    await probeVoiceCapability(true)
    expect(calls, 'force=true 必须绕过 TTL').toBe(1)
  })

  it('P5 值没变不发通知；变了才发一次', async () => {
    const seen: Array<boolean | null> = []
    subscribeVoiceCapability(() => seen.push(voiceCapability().tts))
    await probeVoiceCapability(true)
    await probeVoiceCapability(true)
    expect(seen, '同一档位连拨两次只该通知一次（否则每个气泡空转重渲染）').toEqual([true])

    impl = () => healthReply({ tts_ready: false })
    await probeVoiceCapability(true)
    expect(seen).toEqual([true, false])
  })
})
