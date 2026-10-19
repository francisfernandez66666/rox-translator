// ============================================================================
// api/asrEngines.assetGate.test.ts — 端侧 WASM 资产门「真件 vs SPA 兜底页」（★ 2026-10-17）
//
// 由来（用户报障：即时翻译语音按钮点了没反应、也不弹麦克风授权）：
//   端侧资产 .data（199MB，模型烘焙其中）没落盘时，旧判据 `res.ok` 会被
//   spa.go 的**兜底页**骗过——它对不存在的路径回整页 index.html 且状态码仍是 200
//   （AGENTS §一·6 那族「文件在仓库里 ≠ 线上点得开」）。于是 isAvailable() 谎报就绪
//   ⇒ 选中端侧 WASM ⇒ worker 拿 HTML 当模型包、识别器建不起来 ⇒ 点录音既不弹授权也不出声。
//
// 本文件把「回的是真件还是兜底页」这一跳钉死：
//   · 判据＝2xx ＋ content-type 非 text/html ＋ content-length 达下限（两把尺子）；
//   · W1/W3/W4 三档缺失形态必须判不可用（旧判据在这三档全假绿）；
//   · W7 是**只问 status 的实现**会踩的那一格：200 + 体积不足（真件下限）仍须判死。
//   · S1/S2 守住「不可用即无缝回落服务端」这条与 P3 零回归的契约。
//
// ★ 2026-10-19 G10 补腿（X1~X6）：HEAD **不带 content-length** 的 CDN/反代形态。
//   旧实现 `Number('' || '0')=0 < 下限` ⇒ 真件被误杀、端侧引擎恒不可用（用户报障
//   「语音按钮无法录入」的另一形态）。现在缺长度时补一次 Range 探测读 content-range 总长：
//     · X1 总长达标 ⇒ 可用（旧实现在此**误杀**，这是本条的本体）；
//     · X2 总长不足 ⇒ 判死（Range 问到了，证明文件真小）；
//     · X3 Range 回兜底 HTML ⇒ 判死（不许把兜底页当资产）；
//     · X4 Range 抛错 ⇒ 'unknown' ⇒ **仍可用**（fail-open：探不到不钉死，同 §一·17②）；
//     · X5 .data 缺长度 + Range 达标 ⇒ 可用（199MB 档这条腿）；
//     · X6 反证：把 Range 探测摘掉（只信 HEAD 的长度）⇒ X1 型形态必须判不可用。
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'

// 浏览器门单独可控（jsdom 里 MediaRecorder 不存在，不桩化则恒 false）
const mocks = vi.hoisted(() => ({ micSupported: true }))
vi.mock('./chatVoice', () => ({
  micSupported: () => mocks.micSupported,
  startMicRecording: vi.fn(),
  asrTranscribe: vi.fn(),
  VoiceError: class extends Error {},
}))

import {
  WasmAsrEngine,
  ServerAsrEngine,
  getAsrEngine,
  resetAsrEngineCache,
} from './asrEngines'

const WASM = '/wasm/sherpa-onnx'

// 造一个 fetch 响应壳（只实现判据真正读的那几面：ok/status/headers.get/json）
function resp(opts: {
  status?: number
  contentType?: string | null
  contentLength?: number
  contentRange?: string
  json?: unknown
}) {
  const status = opts.status ?? 200
  const headers = new Map<string, string>()
  if (opts.contentType !== null && opts.contentType !== undefined) {
    headers.set('content-type', opts.contentType)
  }
  if (opts.contentLength !== undefined) {
    headers.set('content-length', String(opts.contentLength))
  }
  if (opts.contentRange !== undefined) {
    headers.set('content-range', opts.contentRange)
  }
  return {
    ok: status >= 200 && status < 300,
    status,
    headers: { get: (k: string) => headers.get(k.toLowerCase()) ?? null },
    json: async () => {
      if (opts.json === undefined) throw new Error('not json')
      return opts.json
    },
  } as unknown as Response
}

// SPA 兜底页形态：200 + text/html + 小体积（缺件时后端回整页 index.html）
const FALLBACK = resp({ contentType: 'text/html; charset=utf-8', contentLength: 2591 })
// 真件形态
const REAL_MANIFEST = resp({ contentType: 'application/json', contentLength: 1703, json: { defaultModel: 'x', models: {} } })
const REAL_WASM = resp({ contentType: 'application/wasm', contentLength: 13150239 })
const REAL_DATA = resp({ contentType: 'application/octet-stream', contentLength: 199_000_000 })

// 装一个按 URL 分派的 fetch 桩；返回被调用的 URL 清单（供「不发请求」那档断言）。
// ★ 2026-10-19 G10：带 `Range` 头的请求走 rangeMap（缺省回同一个 map），
//   这样「HEAD 无 content-length → Range 补探」这条链才测得到（旧桩忽略 init ⇒ Range 腿结构性空转）。
function installFetch(map: Record<string, Response>, rangeMap: Record<string, Response> = {}) {
  const calls: string[] = []
  const fn = vi.fn(async (url: string, init?: RequestInit) => {
    calls.push(url)
    const hdrs = (init?.headers || {}) as Record<string, string>
    const isRange = !!(hdrs.Range || hdrs.range)
    const hit = (isRange ? rangeMap[url] ?? map[url] : map[url])
    if (!hit) throw new Error('unexpected fetch: ' + url)
    return hit
  })
  ;(globalThis as unknown as { fetch: unknown }).fetch = fn
  return calls
}

function fullMap(over: Record<string, Response> = {}) {
  return {
    [`${WASM}/manifest.json`]: REAL_MANIFEST,
    [`${WASM}/sherpa-onnx-wasm-main-asr.wasm`]: REAL_WASM,
    [`${WASM}/sherpa-onnx-wasm-main-asr.data`]: REAL_DATA,
    ...over,
  }
}

describe('端侧 WASM 资产门（真件 vs SPA 兜底页）', () => {
  beforeEach(() => {
    mocks.micSupported = true
    localStorage.clear()
    resetAsrEngineCache()
  })
  afterEach(() => {
    resetAsrEngineCache()
  })

  it('W1 .data 缺失（SPA 兜底 200 text/html）⇒ 判不可用（旧判据在此假绿）', async () => {
    installFetch(
      fullMap({ [`${WASM}/sherpa-onnx-wasm-main-asr.data`]: FALLBACK }),
    )
    expect(await new WasmAsrEngine().isAvailable()).toBe(false)
  })

  it('W2 三件真件齐 ⇒ 可用', async () => {
    installFetch(fullMap())
    expect(await new WasmAsrEngine().isAvailable()).toBe(true)
  })

  it('W3 .wasm 缺失（兜底页）⇒ 判不可用', async () => {
    installFetch(
      fullMap({ [`${WASM}/sherpa-onnx-wasm-main-asr.wasm`]: FALLBACK }),
    )
    expect(await new WasmAsrEngine().isAvailable()).toBe(false)
  })

  it('W4 manifest 缺席（兜底 HTML，JSON 解析失败）⇒ 判不可用', async () => {
    installFetch(fullMap({ [`${WASM}/manifest.json`]: FALLBACK }))
    expect(await new WasmAsrEngine().isAvailable()).toBe(false)
  })

  it('W5 浏览器不支持录音 ⇒ 直接 false，且一次请求都不发', async () => {
    mocks.micSupported = false
    const calls = installFetch(fullMap())
    expect(await new WasmAsrEngine().isAvailable()).toBe(false)
    expect(calls.length, '浏览器门在前：不该去看资产').toBe(0)
  })

  it('W6 真件无 content-type 但体积达标 ⇒ 仍可用（不因头缺失误杀真件）', async () => {
    installFetch(
      fullMap({
        [`${WASM}/sherpa-onnx-wasm-main-asr.wasm`]: resp({ contentType: null, contentLength: 13150239 }),
      }),
    )
    expect(await new WasmAsrEngine().isAvailable()).toBe(true)
  })

  it('W7 200 + content-type 冒充 application/wasm 但体积不足 ⇒ 仍判死（体积是第二把尺子）', async () => {
    installFetch(
      fullMap({
        [`${WASM}/sherpa-onnx-wasm-main-asr.wasm`]: resp({
          contentType: 'application/wasm',
          contentLength: 12_000, // 远小于 1MB 真件下限
        }),
      }),
    )
    expect(await new WasmAsrEngine().isAvailable()).toBe(false)
  })

  // ===== ★ 2026-10-19 G10：HEAD 无 content-length 的 CDN/反代形态 =====
  const wasmUrl = `${WASM}/sherpa-onnx-wasm-main-asr.wasm`
  const dataUrl = `${WASM}/sherpa-onnx-wasm-main-asr.data`
  // HEAD 不带长度：真件只留 content-type（模拟 CDN 不回 content-length 的 HEAD）
  const headNoLen = (ct: string) => resp({ contentType: ct })
  // Range 206 回总长
  const rangeTotal = (total: number, ct = 'application/octet-stream') =>
    resp({ status: 206, contentType: ct, contentRange: `bytes 0-0/${total}` })

  it('X1 .wasm 的 HEAD 无 content-length、Range 总长达标 ⇒ 可用（旧实现在此误杀真件）', async () => {
    installFetch(
      fullMap({ [wasmUrl]: headNoLen('application/wasm') }),
      { [wasmUrl]: rangeTotal(13_150_239, 'application/wasm') },
    )
    expect(await new WasmAsrEngine().isAvailable()).toBe(true)
  })

  it('X2 Range 问到总长、但不足下限 ⇒ 判死（问到了就说明文件真小）', async () => {
    installFetch(
      fullMap({ [wasmUrl]: headNoLen('application/wasm') }),
      { [wasmUrl]: rangeTotal(12_000, 'application/wasm') },
    )
    expect(await new WasmAsrEngine().isAvailable()).toBe(false)
  })

  it('X3 Range 回的是 SPA 兜底页（text/html）⇒ 判死（兜底页不是资产）', async () => {
    installFetch(
      fullMap({ [wasmUrl]: headNoLen('application/wasm') }),
      { [wasmUrl]: FALLBACK },
    )
    expect(await new WasmAsrEngine().isAvailable()).toBe(false)
  })

  it('X4 Range 探测抛错 ⇒ 按未知处理、不钉死（fail-open，仍可用）', async () => {
    installFetch(
      fullMap({ [wasmUrl]: headNoLen('application/wasm') }),
      // rangeMap 缺该 URL ⇒ 桩抛 unexpected fetch ⇒ assetTotalViaRange 收成 'unknown'
    )
    expect(await new WasmAsrEngine().isAvailable()).toBe(true)
  })

  it('X5 .data 的 HEAD 无 content-length、Range 达标（199MB 档）⇒ 可用', async () => {
    installFetch(
      fullMap({ [dataUrl]: headNoLen('application/octet-stream') }),
      { [dataUrl]: rangeTotal(199_000_000) },
    )
    expect(await new WasmAsrEngine().isAvailable()).toBe(true)
  })

  it('X6 射程自证：同一条 HEAD（无长度），只改 Range 的总长读数 ⇒ 判定必须跟着翻', async () => {
    // 「无长度 ⇒ 补探」这条腿真的在决定结果，而不是恰好/一律判真：
    // 两份夹具除了 Range 的 content-range 总长以外**完全相同**。
    installFetch(
      fullMap({ [wasmUrl]: headNoLen('application/wasm') }),
      { [wasmUrl]: rangeTotal(13_150_239, 'application/wasm') },
    )
    const big = await new WasmAsrEngine().isAvailable()
    resetAsrEngineCache()
    installFetch(
      fullMap({ [wasmUrl]: headNoLen('application/wasm') }),
      { [wasmUrl]: rangeTotal(1, 'application/wasm') },
    )
    const tiny = await new WasmAsrEngine().isAvailable()
    expect([big, tiny], 'Range 读数没进入判定＝补探腿是空的').toEqual([true, false])
  })

  it('S1 auto：端侧不可用 ⇒ 无缝回落服务端（与 P3 零回归）', async () => {
    installFetch(fullMap({ [`${WASM}/sherpa-onnx-wasm-main-asr.data`]: FALLBACK }))
    const eng = await getAsrEngine()
    expect(eng).toBeInstanceOf(ServerAsrEngine)
  })

  it('S2 pref=server ⇒ 强制服务端，连端侧资产都不探', async () => {
    localStorage.setItem('asr_engine_pref', 'server')
    const calls = installFetch(fullMap())
    const eng = await getAsrEngine()
    expect(eng).toBeInstanceOf(ServerAsrEngine)
    expect(calls.length, 'pref=server 时不该发 manifest 探测').toBe(0)
  })

  it('S3 auto：端侧就绪 ⇒ 优先端侧', async () => {
    installFetch(fullMap())
    const eng = await getAsrEngine()
    expect(eng).toBeInstanceOf(WasmAsrEngine)
  })
})
