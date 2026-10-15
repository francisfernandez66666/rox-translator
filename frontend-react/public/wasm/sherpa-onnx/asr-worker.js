// ============================================================================
// asr-worker.js — 真实 sherpa-onnx 浏览器流式 ASR Worker（2026-10-16）
//
// 资产（官方 k2-fsa 预编译包，随构建落盘，禁运行时联网）：
//   - sherpa-onnx-asr.js            官方 API glue（createOnlineRecognizer/OnlineStream）
//   - sherpa-onnx-wasm-main-asr.js  emscripten main（加载 .wasm + 预载 .data 模型）
//   - sherpa-onnx-wasm-main-asr.wasm  浏览器版 wasm 运行时（13MB，sha256 见 manifest）
//   - sherpa-onnx-wasm-main-asr.data  官方打包的流式模型（zipformer 中英双语，199MB，
//                                     超 GitHub 单文件 100MB 限故 gitignore，
//                                     部署期由 scripts/fetch_sherpa_assets.sh 落盘）
//   （index.html / app-asr.js 为官方 demo 原件，留作对照与手工自测）
//
// 协议（与 src/api/asrEngines.ts 严格对齐）：
//   收： { cmd: 'start' } / { cmd: 'stop' }
//   发： { type: 'state', state: 'ready'|'recording'|'denied'|'failed' }
//       { type: 'partial', text }  实时中间结果
//       { type: 'final', text }    一句结束的最终结果
//
// 接线与官方 demo（wasm/asr/index.html）一致：glue → Module 配置 → emscripten main。
// ============================================================================

// 本品所在目录即 wasm/.data 的根（emscripten 按此解析运行时与预载模型包）。
const WASM_BASE = self.location.href.substring(0, self.location.href.lastIndexOf('/'))

let recognizer = null
let recognizerReady = false
let pendingStart = false

let mediaStream = null
let audioCtx = null
let sourceNode = null
let scriptNode = null
let recStream = null
let running = false
let tailPaddings = null // paraformer 句末补零（zipformer 不用）

// ---------------------------------------------------------------------------
// 1) 装配 emscripten Module → 依次载入官方 glue 与 wasm main（自动实例化+预载 .data）
// ---------------------------------------------------------------------------
self.Module = {
  locateFile: (path) => WASM_BASE + '/' + path,
  onRuntimeInitialized() {
    try {
      const cfg = self.__sherpaConfig // undefined = 官方默认（与 .data 打包约定一致）
      recognizer = createOnlineRecognizer(self.Module, cfg)
      recognizerReady = true
      self.postMessage({ type: 'state', state: 'ready' })
      if (pendingStart) {
        pendingStart = false
        beginRecording()
      }
    } catch (e) {
      self.postMessage({ type: 'state', state: 'failed' })
    }
  },
}

importScripts('./sherpa-onnx-asr.js')
importScripts('./sherpa-onnx-wasm-main-asr.js')

// ---------------------------------------------------------------------------
// 2) 读 manifest.json 构造 CONFIG（模型路径与官方 .data 打包约定一致）
// ---------------------------------------------------------------------------
async function loadConfig() {
  const r = await fetch(WASM_BASE + '/manifest.json')
  if (!r.ok) throw new Error('manifest.json 缺失')
  const m = await r.json()
  const name = m.defaultModel
  const model = m.models && m.models[name]
  if (!model) throw new Error('manifest 未声明 defaultModel: ' + name)
  const isParaformer = (model.modelType || '') === 'paraformer'
  const modelConfig = {
    transducer: { encoder: '', decoder: '', joiner: '' },
    paraformer: { encoder: '', decoder: '' },
    zipformer2Ctc: { model: '' },
    nemoCtc: { model: '' },
    toneCtc: { model: '' },
    tokens: model.tokens || './tokens.txt',
    numThreads: model.numThreads || 1,
    provider: 'cpu',
    debug: 0,
    modelType: model.modelType || '',
    modelingUnit: model.modelingUnit || 'cjkchar',
    bpeVocab: '',
  }
  if (isParaformer) {
    modelConfig.paraformer = {
      encoder: model.encoder || './encoder.onnx',
      decoder: model.decoder || './decoder.onnx',
    }
    const sr = model.sampleRate || 16000
    tailPaddings = new Float32Array(Math.round(sr * 0.2))
  } else {
    // transducer（zipformer）：文件名与官方 .data 打包约定一致
    modelConfig.transducer = {
      encoder: model.encoder || './encoder.onnx',
      decoder: model.decoder || './decoder.onnx',
      joiner: model.joiner || './joiner.onnx',
    }
  }
  self.__sherpaConfig = {
    featConfig: { sampleRate: model.sampleRate || 16000, featureDim: model.featureDim || 80 },
    modelConfig,
    decodingMethod: model.decodingMethod || 'greedy_search',
    maxActivePaths: model.maxActivePaths || 4,
    enableEndpoint: 1,
    rule1MinTrailingSilence: model.rule1 != null ? model.rule1 : 2.4,
    rule2MinTrailingSilence: model.rule2 != null ? model.rule2 : 1.2,
    rule3MinUtteranceLength: model.rule3 != null ? model.rule3 : 20,
  }
}

// ---------------------------------------------------------------------------
// 3) 识别主循环（照搬官方 app-asr.js 的 acceptWaveform→decode→getResult→endpoint）
// ---------------------------------------------------------------------------
async function beginRecording() {
  if (running) return
  if (!recognizerReady || !recognizer) {
    pendingStart = true
    return
  }
  try {
    mediaStream = await navigator.mediaDevices.getUserMedia({ audio: true })
  } catch (e) {
    self.postMessage({ type: 'state', state: 'denied' })
    return
  }
  try {
    audioCtx = new (self.AudioContext || self.webkitAudioContext)({ sampleRate: 16000 })
    const expectedSampleRate = 16000
    sourceNode = audioCtx.createMediaStreamSource(mediaStream)
    scriptNode = audioCtx.createScriptProcessor(4096, 1, 1)
    recStream = recognizer.createStream()

    scriptNode.onaudioprocess = (event) => {
      if (!running || !recStream) return
      const samples = event.inputBuffer.getChannelData(0)
      recStream.acceptWaveform(expectedSampleRate, samples)
      while (recognizer.isReady(recStream)) {
        recognizer.decode(recStream)
      }
      const r = recognizer.getResult(recStream)
      if (r && r.text) {
        self.postMessage({ type: 'partial', text: r.text })
      }
      if (recognizer.isEndpoint(recStream)) {
        let finalText = r ? r.text : ''
        if (tailPaddings && tailPaddings.length) {
          // paraformer 句末补零再解一次（官方 app-asr.js 同款）
          recStream.acceptWaveform(expectedSampleRate, tailPaddings)
          while (recognizer.isReady(recStream)) recognizer.decode(recStream)
          const rf = recognizer.getResult(recStream)
          if (rf && rf.text) finalText = rf.text
        }
        if (finalText) self.postMessage({ type: 'final', text: finalText })
        recognizer.reset(recStream)
      }
    }

    sourceNode.connect(scriptNode)
    scriptNode.connect(audioCtx.destination)
    running = true
    self.postMessage({ type: 'state', state: 'recording' })
  } catch (e) {
    self.postMessage({ type: 'state', state: 'failed' })
    cleanupAudio()
  }
}

function stopRecording() {
  if (!running) {
    cleanupAudio()
    return
  }
  running = false
  try {
    if (recStream && recognizerReady) {
      const sr = (self.__sherpaConfig && self.__sherpaConfig.featConfig.sampleRate) || 16000
      if (tailPaddings && tailPaddings.length) {
        recStream.acceptWaveform(sr, tailPaddings)
        while (recognizer.isReady(recStream)) recognizer.decode(recStream)
      }
      const r = recognizer.getResult(recStream)
      if (r && r.text) self.postMessage({ type: 'final', text: r.text })
    }
  } catch (e) {
    /* 忽略收尾错误 */
  }
  cleanupAudio()
}

function cleanupAudio() {
  try {
    if (scriptNode) {
      scriptNode.disconnect()
      scriptNode.onaudioprocess = null
    }
    if (sourceNode) sourceNode.disconnect()
    if (audioCtx) audioCtx.close()
    if (mediaStream) mediaStream.getTracks().forEach((t) => t.stop())
  } catch (e) {
    /* noop */
  }
  mediaStream = null
  audioCtx = null
  sourceNode = null
  scriptNode = null
  recStream = null
}

// ---------------------------------------------------------------------------
// 4) 启动加载 + 消息分发
// ---------------------------------------------------------------------------
loadConfig().catch((e) => {
  // manifest 缺失/格式坏：wasm 仍会加载，但无法按预期建识别器；标记 failed 便于上层回落
  self.postMessage({ type: 'state', state: 'failed' })
})

self.onmessage = (e) => {
  const d = e.data || {}
  if (d.cmd === 'start') {
    if (recognizerReady) beginRecording()
    else pendingStart = true
  } else if (d.cmd === 'stop') {
    stopRecording()
  }
}
