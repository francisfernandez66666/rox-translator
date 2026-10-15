// ============================================================================
// api/asrEngines.ts — ★ P4 端侧 WASM 实时识别的「可插拔」接缝（2026-10-16）
//
// 背景：P3 已落地「麦克风录音 → 上传服务端 /api/chat/asr → 识别文本填输入框」。
//   本文件把「识别这件事」抽象成 AsrEngine，使端侧 WASM（sherpa-onnx-web）能与
//   服务端并存、且**自动优先端侧、缺模型时无缝回落服务端**（改造方案 v2 §4.1：
//   「弱网/模型缺失时走服务端」）。
//
// ★ 资产门（决定 WASM 引擎何时真正可用，绝不谎报「已激活」）：
//   本目录已随构建落盘官方预编译运行时三件套（glue sherpa-onnx-asr.js + emscripten main
//   sherpa-onnx-wasm-main-asr.js + wasm 二进制 13MB，sha256 记录在 manifest.json）+ 协议
//   对齐 worker（asr-worker.js）。官方流式模型（zipformer 中英双语）烘焙在
//   sherpa-onnx-wasm-main-asr.data（199MB）内——该文件超 GitHub 单文件 100MB 硬限，
//   gitignore，由部署机跑 scripts/fetch_sherpa_assets.sh 落盘（禁运行时联网）。
//   isAvailable() 因此对 manifest、wasm、.data 三者各发 HEAD，缺一即 false，
//   上层自动回落服务端（与 P3 行为零回归）。
//
// 契约（两个引擎必须同构）：
//   start() 开麦 → 返回 AsrSession；session.final 在 stop() 后 resolve 出识别文本
//   （出错 reject，UI 按 reason 取词典，遵循 chatVoice.ts 的「译文第一」原则）。
//   onPartial 仅 WASM 实时字幕用；服务端引擎不流（final 一次性给）。
// ============================================================================
import {
  asrTranscribe,
  micSupported,
  startMicRecording,
  VoiceError,
} from './chatVoice'

export type AsrKind = 'server' | 'wasm'

export interface AsrHandlers {
  // 'recording' | 'denied' | 'failed' | 'ready'
  onState?: (s: 'recording' | 'denied' | 'failed' | 'ready') => void
  // 实时部分识别（WASM 流式；服务端不触发）
  onPartial?: (text: string) => void
}

export interface AsrSession {
  readonly final: Promise<string>
  stop(): void
  cancel(): void
}

export interface AsrEngine {
  readonly kind: AsrKind
  isAvailable(): Promise<boolean>
  start(handlers: AsrHandlers): AsrSession
}

// ---------------------------------------------------------------------------
// 服务端引擎：复用 chatVoice 既有「录完上传」链路，行为等同 P3。
// ---------------------------------------------------------------------------
export class ServerAsrEngine implements AsrEngine {
  readonly kind = 'server' as const

  async isAvailable(): Promise<boolean> {
    return micSupported()
  }

  start(h: AsrHandlers): AsrSession {
    const ctrl = startMicRecording((why) =>
      h.onState?.(why === 'mic-denied' ? 'denied' : 'failed'),
    )
    if (!ctrl) {
      h.onState?.('failed')
      return { final: Promise.resolve(''), stop() {}, cancel() {} }
    }
    h.onState?.('recording')
    let resolveFinal: (v: string) => void = () => {}
    let rejectFinal: (e: unknown) => void = () => {}
    const final = new Promise<string>((res, rej) => {
      resolveFinal = res
      rejectFinal = rej
    })
    return {
      final,
      stop() {
        void (async () => {
          const blob = await ctrl.stop()
          if (!blob || blob.size === 0) {
            resolveFinal('') // 没录到声音：安静放回，不报错
            return
          }
          try {
            resolveFinal(await asrTranscribe(blob))
          } catch (e) {
            // 失败不阻塞译文：reject 让 UI 走「重试/改用键盘」那句词典
            rejectFinal(e instanceof VoiceError ? e : new VoiceError('asr failed', 'failed'))
          }
        })()
      },
      cancel() {
        ctrl.cancel()
      },
    }
  }
}

// ---------------------------------------------------------------------------
// 端侧 WASM 引擎：sherpa-onnx-web。资产门就绪才可用，否则 isAvailable=false。
// 运行时只 new Worker 一个随构建落盘的脚本；脚本内部负责 getUserMedia +
// AudioWorklet 喂 16k PCM + 模型推理，并 postMessage 回 partial/final/state。
// 本文件不内置任何 WASM/模型字节——那部分按禁联网铁律必须随包落盘。
// ---------------------------------------------------------------------------
const WASM_BASE = '/wasm/sherpa-onnx'

export class WasmAsrEngine implements AsrEngine {
  readonly kind = 'wasm' as const

  async isAvailable(): Promise<boolean> {
    if (!micSupported()) return false
    try {
      // 1) manifest 必须存在（且能解析）
      const mres = await fetch(`${WASM_BASE}/manifest.json`)
      if (!mres.ok) return false
      const m = await mres.json().catch(() => null)
      if (!m || !m.defaultModel || !m.models) return false
      // 2) 真实 wasm 运行时必须落盘（否则 worker new 出来也跑不起推理）
      const wasmOk = await fetch(`${WASM_BASE}/sherpa-onnx-wasm-main-asr.wasm`, {
        method: 'HEAD',
      })
      if (!wasmOk.ok) return false
      // 3) 官方预载模型包 .data 必须落盘（模型烘焙其中，缺它识别器无法建立）
      const dataOk = await fetch(`${WASM_BASE}/sherpa-onnx-wasm-main-asr.data`, {
        method: 'HEAD',
      })
      return dataOk.ok
    } catch {
      return false
    }
  }

  start(h: AsrHandlers): AsrSession {
    let worker: Worker
    try {
      worker = new Worker(`${WASM_BASE}/asr-worker.js`)
    } catch {
      h.onState?.('failed')
      return { final: Promise.resolve(''), stop() {}, cancel() {} }
    }
    let resolveFinal: (v: string) => void = () => {}
    const final = new Promise<string>((res) => {
      resolveFinal = res
    })
    worker.onmessage = (e: MessageEvent) => {
      const d = e.data || {}
      if (d.type === 'partial') h.onPartial?.(d.text || '')
      else if (d.type === 'final') {
        h.onPartial?.(d.text || '')
        resolveFinal(d.text || '')
      } else if (d.type === 'state') h.onState?.(d.state)
    }
    worker.onerror = () => h.onState?.('failed')
    worker.postMessage({ cmd: 'start' })
    h.onState?.('recording')
    let stopped = false
    return {
      final,
      stop() {
        if (stopped) return
        stopped = true
        worker.postMessage({ cmd: 'stop' })
        // 兜底：worker 异常无回包时 4s 后放空，绝不吊死 UI
        setTimeout(() => resolveFinal(''), 4000)
      },
      cancel() {
        stopped = true
        worker.terminate()
      },
    }
  }
}

// ---------------------------------------------------------------------------
// 选择：auto 下优先 WASM（资产就绪才选），否则服务端。pref 由 localStorage 覆盖。
// 结果缓存，避免每次点录音都发一次 manifest HEAD。
// ---------------------------------------------------------------------------
let cached: Promise<AsrEngine> | null = null

export function getAsrEngine(): Promise<AsrEngine> {
  if (cached) return cached
  cached = (async () => {
    const pref =
      typeof localStorage !== 'undefined' ? localStorage.getItem('asr_engine_pref') : 'auto'
    if (pref !== 'server') {
      try {
        const wasm = new WasmAsrEngine()
        if (await wasm.isAvailable()) return wasm
      } catch {
        /* 落到服务端 */
      }
    }
    return new ServerAsrEngine()
  })()
  return cached
}

export function resetAsrEngineCache(): void {
  cached = null
}
