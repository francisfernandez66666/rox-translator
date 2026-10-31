// ============================================================================
// asrEngines.engineLog.test.ts — N8 的读数锁（★ 2026-10-31 补齐）
//
// 为什么必须有这一条：端侧 WASM 识别**不经过服务端**，服务端日志里天然看不到这一轮；
// 若界面/日志没有任何"走了哪条腿"的读数，排障只能靠猜（把"服务端没拦到"误读成缺陷）。
// 口径：选择点只记一行 `[asr-engine] engine=ondevice|server`，且随引擎缓存只出现一次。
// 三条腿：显式 server / 端侧可用选 ondevice / 端侧不可用回落 server。
// ============================================================================
// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('./chatVoice', () => ({
  asrTranscribe: vi.fn(),
  micSupported: vi.fn(() => true),
  startMicRecording: vi.fn(),
  VoiceError: class VoiceError extends Error {},
}))

import { getAsrEngine, resetAsrEngineCache, WasmAsrEngine } from './asrEngines'

describe('ASR 引擎选择读数（N8）', () => {
  let info: ReturnType<typeof vi.spyOn>
  afterEach(() => vi.restoreAllMocks())
  beforeEach(() => {
    resetAsrEngineCache()
    localStorage.clear()
    info = vi.spyOn(console, 'info').mockImplementation(() => {})
  })

  it('pref=server ⇒ 记 engine=server', async () => {
    localStorage.setItem('asr_engine_pref', 'server')
    await getAsrEngine()
    expect(info).toHaveBeenCalledWith('[asr-engine] engine=server')
  })

  it('端侧可用（auto）⇒ 选 WASM 并记 engine=ondevice', async () => {
    vi.spyOn(WasmAsrEngine.prototype, 'isAvailable').mockResolvedValue(true)
    const eng = await getAsrEngine()
    expect(eng.kind).toBe('wasm')
    expect(info).toHaveBeenCalledWith('[asr-engine] engine=ondevice')
  })

  it('端侧不可用 ⇒ 回落服务端，记 engine=server；且缓存后只记一次', async () => {
    vi.spyOn(WasmAsrEngine.prototype, 'isAvailable').mockResolvedValue(false)
    const eng = await getAsrEngine()
    expect(eng.kind).toBe('server')
    await getAsrEngine() // 命中模块级缓存
    expect(info.mock.calls.filter((c: unknown[]) => String(c[0]).startsWith('[asr-engine]'))).toHaveLength(1)
    expect(info).toHaveBeenCalledWith('[asr-engine] engine=server')
  })
})
