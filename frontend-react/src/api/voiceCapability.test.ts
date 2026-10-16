// ============================================================================
// api/voiceCapability.test.ts — 服务端语音能力探针的尺子（★ 2026-10-16）
//
// 锁的是「探针自己」的行为，与 SpeakButton.gate.dom.test.tsx 的「接线」互补：
//   P1 tts_ready=true/false 如实落到快照；
//   P2 字段缺失／形态不对 ⇒ 保持「未知」（把"读不到"写成"不可用"会无端摘掉入口）；
//   P3 网络失败／非 2xx ⇒ 保持「未知」，且**不写状态**（下一次调用必须重拨——一次抖动
//      不该把「这个站永远没有语音」钉死，同 ensureLocale 那条「失败不写状态」）；
//   P4 TTL 内不重复拨网络；force 能强制重拨；
//   P5 值没变不发通知（emit 只对变化发，否则每个气泡空转重渲染）；
//   ★ 2026-10-16 能力门批补：探针从「只有 TTS」扩成 tts/asr/ocr 三档同一把尺子——
//   P9 asr_ready/ocr_ready 如实落快照；P10 形态不对／缺失按**未知**（不误摘入口）；
//   P11 只改 asr 也必须发通知（emit 漏比一个字段＝界面上那颗按钮该出现时不出现）；
//   P12 三档全不可读（响应形态不是认识的那一份）⇒ 整次探测按失败处理（TTL 时间戳都不刷）。
// 运行：npx vitest run src/api/voiceCapability.test.ts
// ============================================================================
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { probeVoiceCapability, voiceCapability, subscribeVoiceCapability, resetVoiceCapability, ttsLangCovered, VoiceCap } from './voiceCapability'

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

  // ★ 2026-10-16 补（按语种收门）：语音是**按语种**的能力，只有一个布尔时
  //   日/俄/泰译文的同一排会摆出一颗点下去必然 unsupported_lang 的朗读钮。
  it('P6 tts_langs 如实落快照并归一化；形态不对一律按未知（不误摘入口）', async () => {
    impl = () => healthReply({ tts_ready: true, tts_langs: ['zh', 'zh-Hant', 'en'] })
    await probeVoiceCapability(true)
    expect(voiceCapability().langs, '地域段要归一化到主码（zh-Hant → zh），否则 zh-hant 译文会被误判越界')
      .toEqual(['zh', 'zh', 'en'])

    // 五档"读不到"：字段缺失 / null / 空数组 / 非数组 / 数组里混了非字符串
    const badBodies = [
      { tts_ready: true },
      { tts_ready: true, tts_langs: null },
      { tts_ready: true, tts_langs: [] },
      { tts_ready: true, tts_langs: 'zh,en' },
      { tts_ready: true, tts_langs: ['zh', 7] },
    ]
    for (const body of badBodies) {
      resetVoiceCapability()
      impl = () => healthReply(body)
      await probeVoiceCapability(true)
      expect(voiceCapability().langs, `形态不对必须按"未知"（放行），不许推断成"一个语种都不支持"：${JSON.stringify(body)}`)
        .toBeNull()
    }
  })

  // capOf 组装一份能力快照：ttsLangCovered 只吃 tts/langs，另两档（asr/ocr）显式写"未知"，
  // 免得这段测试因为新增字段而变成"顺手把别的档位也钉死"（那不是这条用例的射程）。
  const capOf = (tts: boolean | null, langs: string[] | null): VoiceCap => ({ tts, langs, asr: null, ocr: null })

  it('P9 asr_ready/ocr_ready 如实落到快照（三档同一把尺子）', async () => {
    impl = () => healthReply({ tts_ready: true, asr_ready: false, ocr_ready: true })
    await probeVoiceCapability(true)
    const c = voiceCapability()
    expect(c.asr, 'asr_ready=false 必须落到快照（麦克风那颗按钮的渲染门）').toBe(false)
    expect(c.ocr, 'ocr_ready=true 必须落到快照（工单图片后缀的准入）').toBe(true)
  })

  it('P10 asr/ocr 形态不对或缺失 ⇒ 未知（不能把"读不到"写成"不可用"）', async () => {
    impl = () => healthReply({ tts_ready: true, asr_ready: 'false', ocr_ready: null })
    await probeVoiceCapability(true)
    const c = voiceCapability()
    expect(c.asr, '字符串 "false" 不是布尔 ⇒ 未知').toBeNull()
    expect(c.ocr, 'null ⇒ 未知').toBeNull()
  })

  it('P11 只有 asr 变化也必须发通知（emit 漏比字段＝按钮该出现时不出现）', async () => {
    const seen: Array<string> = []
    subscribeVoiceCapability(() => seen.push(`tts=${voiceCapability().tts},asr=${voiceCapability().asr}`))
    impl = () => healthReply({ tts_ready: true, asr_ready: true, ocr_ready: true })
    await probeVoiceCapability(true)
    impl = () => healthReply({ tts_ready: true, asr_ready: false, ocr_ready: true })
    await probeVoiceCapability(true)
    expect(seen, 'tts 没变但 asr 翻了，也必须重渲染一次').toEqual(['tts=true,asr=true', 'tts=true,asr=false'])
  })

  it('P12 三档全不可读 ⇒ 整次探测按失败处理（TTL 时间戳不刷，下一次调用照旧重拨）', async () => {
    // 先拿到一份正常读数（把 fetchedAt 打上）
    impl = () => healthReply({ tts_ready: true, asr_ready: true, ocr_ready: true })
    await probeVoiceCapability(true)
    expect(calls).toBe(1)
    // 接口形态变成"另一种东西"：三档都读不出来 ⇒ 快照一个字都不许动
    impl = () => healthReply({ capabilities: { tts: true } })
    await probeVoiceCapability(true)
    const c = voiceCapability()
    expect([c.tts, c.asr, c.ocr], '读不到任何一档时不许把快照改写').toEqual([true, true, true])
  })

  it('P7 ttsLangCovered 三档：未知放行 / 空码放行 / 命中放行 / 越界收门', () => {
    // 未知（探不到）⇒ 放行：探针坏了不等于能力没了
    expect(ttsLangCovered(capOf(null, null), 'ja')).toBe(true)
    // 已知覆盖清单：命中（含地域段与大小写）放行
    const cap = capOf(true, ['zh', 'en'])
    expect(ttsLangCovered(cap, 'zh')).toBe(true)
    expect(ttsLangCovered(cap, 'en')).toBe(true)
    expect(ttsLangCovered(cap, 'ZH-hant'), '地域段/大小写都要归一化后再判').toBe(true)
    // 空 lang（调用方没指定）⇒ 放行
    expect(ttsLangCovered(cap, '')).toBe(true)
    // 越界 ⇒ 收门（这一语种的朗读钮不渲染）
    expect(ttsLangCovered(cap, 'ja')).toBe(false)
    expect(ttsLangCovered(cap, 'th')).toBe(false)
  })

  it('P8 通知按两个字段任一变化发（只比 tts 会让覆盖语种变化不重渲染）', async () => {
    const seen: Array<string | null> = []
    subscribeVoiceCapability(() => seen.push((voiceCapability().langs || []).join(',')))
    impl = () => healthReply({ tts_ready: true, tts_langs: ['zh', 'en'] })
    await probeVoiceCapability(true)
    impl = () => healthReply({ tts_ready: true, tts_langs: ['zh', 'en', 'ja'] })
    await probeVoiceCapability(true)
    expect(seen, 'tts 没变但覆盖语种扩了，也必须重渲染一次').toEqual(['zh,en', 'zh,en,ja'])
  })
})
