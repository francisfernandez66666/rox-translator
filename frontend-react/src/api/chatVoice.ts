// ============================================================================
// api/chatVoice.ts — ★ P3 即时翻译的语音两件套（麦克风录音 / 译文朗读）
//
// 设计边界（与后端 chat_audio.go 同一口径，改一侧要念一遍另一侧）：
//   · **不做翻译**。`asrTranscribe` 只把录音变成文字（前端填进输入框，用户改完照常点翻译）；
//     `speakAloud` 只把已经译好的文字变成声音。翻译链路、KB、计费一条都不碰。
//   · **能力不可用 = 没有语音，不是故障**。两个函数的失败都只在 UI 上消掉那颗按钮，
//     绝不影响用户拿到译文——这是用户 2026-10-14 立的「译文第一」。
//   · 两个端点与 /api/chat 同口径要求登录：免登录试用只有文本一条腿（用户令：试用态不开放语音）。
//
// ★ 为什么单独成文件：与 fileproc 侧一样的可插拔原则——这一模块的**全部家当**在此，
//   不想用语音能力时删掉本文件与 ChatWindow 里那几行引用即可，不留下任何半截状态。
// ============================================================================
import { API_BASE, authHeaders } from './core'

// micSupported 判断当前浏览器是否具备录音能力（MediaRecorder + getUserMedia）。
// ★ 必须显式判：不支持的浏览器上点录音会直接抛 TypeError，
//   而这一段既不是网络错误也不是权限错误，通用错误兜底会把它显示成"请求失败"，用户完全无法理解。
export function micSupported(): boolean {
  return typeof window !== 'undefined'
    && typeof navigator !== 'undefined'
    && !!navigator.mediaDevices
    && typeof navigator.mediaDevices.getUserMedia === 'function'
    && typeof window.MediaRecorder !== 'undefined'
}

// startMicRecording 开始录音，返回停止句柄。
// ★ mimeType 优先让浏览器选它支持的容器（audio/webm;codecs=opus 在 Chrome/Firefox 通用，
//   Safari 要 mp4/aac）。后端不经由扩展名判断，一律交给 ffmpeg 归一到 16k 单声道 wav，
//   所以这里**不需要**为了兼容去探测一堆格式——能录下来就行。
//
// ★ 2026-10-30 免费档批（D-3）三件：
//   ① **码率固定 24 kbps**：语音识别对码率不敏感，而免费档把成本顶压在这一层——
//      浏览器默认码率通常 64~128 kbps，同样的 30 秒要多传 3~5 倍字节（服务端还要再解码一遍）。
//      Safari 对 `audioBitsPerSecond` 的取值偶有拒绝，故构造失败时回落默认档（录音本身优先）。
//   ② **按档自动停**：`maxSec` 由调用方按档传入（免费 30 / 付费 60，见 lib/instantTier.ts），
//      到点自动收尾并回调 `onAutoStop`——「30 秒」这条卖点必须在**前端**就成立，
//      端侧 WASM 识别时服务端根本没被调用（N8 的读数就是这条）。
//   ③ **计时器必须有退出腿**：stop/cancel/卸载三条路径都要清掉 interval，
//      否则一次录音在后台继续跑，第二次点录音会叠出第二个计时器（秒数越跳越快）。
export type MicRecordingOptions = {
  /** 录音时长上限（秒）；≤0/缺省＝不限（老调用点行为不变） */
  maxSec?: number
  /** 每秒回调一次：(已录秒数, 剩余秒数)。剩余 ≤3 时调用方可给倒计时提示 */
  onTick?: (elapsed: number, remaining: number) => void
  /** 到点自动收尾后回调最终 blob（与用户点停止拿到的是同一份） */
  onAutoStop?: (blob: Blob | null) => void
}

// startMicRecording 开始录像：码率/时长上限由 opts 决定（免费 30s、付费 60s），到点自动停并清理计时器。
export function startMicRecording(onError: (msg: string) => void, opts: MicRecordingOptions = {}): {
  stop: () => Promise<Blob | null>
  cancel: () => void
} | null {
  if (!micSupported()) return null
  let stream: MediaStream | null = null
  let rec: MediaRecorder | null = null
  const chunks: BlobPart[] = []
  let stopped = false
  // 停止承诺只建一次：用户点停、到点自动停、上层兜底停可能同时发生，
  // 三条路径必须拿到**同一个** blob，且只触发一次 onstop。
  let stopPromise: Promise<Blob | null> | null = null
  let finishStop: ((b: Blob | null) => void) | null = null
  let tickTimer: ReturnType<typeof setInterval> | null = null
  let elapsed = 0

  const clearTick = () => {
    if (tickTimer !== null) {
      clearInterval(tickTimer)
      tickTimer = null
    }
  }
  const release = () => stream?.getTracks().forEach((t) => t.stop())
  const collect = (): Blob | null => (chunks.length ? new Blob(chunks, { type: rec?.mimeType || 'audio/webm' }) : null)

  const setup = async (): Promise<boolean> => {
    try {
      stream = await navigator.mediaDevices.getUserMedia({ audio: true })
    } catch {
      onError('mic-denied')
      return false
    }
    // ★ 2026-10-30：授权对话框还开着的时候用户已经取消（或组件卸载）⇒ 拿到流也必须立刻放掉，
    //   否则「取消」之后麦克风指示灯仍亮着（旧形态没有这条，cancel 只能关掉还没建出来的 recorder）。
    if (stopped) {
      stream.getTracks().forEach((t) => t.stop())
      return false
    }
    try {
      // ★ 24 kbps：语音识别够用，免费档的成本顶靠它（见函数头①）。构造参数被拒时回落默认档。
      rec = new MediaRecorder(stream, { audioBitsPerSecond: 24000 })
    } catch {
      try { rec = new MediaRecorder(stream) } catch { onError('mic-failed'); return false }
    }
    rec.ondataavailable = (e) => { if (e.data && e.data.size > 0) chunks.push(e.data) }
    rec.onerror = () => onError('mic-failed')
    rec.start()
    // 到点自动停：只在 maxSec 是正数时起表（不限档连 interval 都不建）
    const maxSec = Math.floor(Number(opts.maxSec) || 0)
    if (maxSec > 0) {
      tickTimer = setInterval(() => {
        if (stopped) { clearTick(); return }
        elapsed += 1
        const remaining = Math.max(0, maxSec - elapsed)
        opts.onTick?.(elapsed, remaining)
        if (remaining <= 0) {
          clearTick()
          void finish().then((b) => opts.onAutoStop?.(b))
        }
      }, 1000)
    }
    return true
  }
  // finish 收尾：清计时器 → stop 录音 → 等 onstop → 产出 blob（幂等，见 stopPromise 注释）
  function finish(): Promise<Blob | null> {
    if (stopPromise) return stopPromise
    stopPromise = new Promise<Blob | null>((resolve) => { finishStop = resolve })
    clearTick()
    if (!rec || rec.state === 'inactive') {
      stopped = true
      release()
      finishStop?.(collect())
      return stopPromise
    }
    rec.onstop = () => {
      stopped = true
      release()
      finishStop?.(collect())
    }
    try { rec.stop() } catch { stopped = true; release(); finishStop?.(collect()) }
    return stopPromise
  }
  void setup().then((ok) => { if (!ok) { stopped = true; clearTick() } })

  return {
    stop: () => finish(),
    cancel: () => {
      stopped = true
      clearTick()
      try { if (rec && rec.state !== 'inactive') rec.stop() } catch { /* 忽略 */ }
      chunks.length = 0
      release()
      // 取消不是错误也不是产物：让等待 stop() 的调用方拿到 null（否则会悬着一个 promise）
      finishStop?.(null)
      stopPromise = stopPromise || Promise.resolve(null)
    },
  }
}

// VoiceError 语音失败错误：除了人话句子，还带后端给的**档位名**（reason）。
//
// ★ 为什么要把档位单独带出来而不是只传 message（改造方案 §六 第 9 条，2026-10-15）：
// 后端那句错误话术是**中文**（它同时是运维日志，必须能指出"缺哪个依赖/资产"），
// 而它现在会被 toast 到访客屏幕上。俄语/泰语界面拿到一句中文就是语言事故，
// 反过来按字符串猜语义又会猜错（把"这语种没声音"报成"服务器坏了"）。
// 所以界面上的那句话一律由调用方按 reason 取 12 语种词典，message 只留给日志。
// 档位名单源＝后端 `fileproc.TtsReason*` 与 `api/chat_audio.go` 的 asrReason，逐字对齐（锁见 chatVoice.lock.test.ts）。
export class VoiceError extends Error {
  // reason 分档名：unsupported_lang / capability_missing / empty / too_long / failed（取不到时归 failed）
  readonly reason: string
  /** 后端的下一档建议（2026-10-30：`suggest:"ticket"` 表示这次该走翻译工单） */
  readonly suggest: string
  constructor(message: string, reason: string, suggest = '') {
    super(message)
    this.name = 'VoiceError'
    this.reason = reason || 'failed'
    this.suggest = suggest || ''
  }
}

// asrTranscribe 把录音 blob 送到后端转写，返回识别出的文本。
// ★ 出参语义：后端在 ASR 不可用时返回 HTTP 200 + {error, reason}，这里统一转成 throw，
//   让调用方只有一条失败路径（不像有的接口 success/error 两条并存，漏判一路就静默）。
//   throw 出去的是 VoiceError：**界面按 reason 取词典，原始中文句子只进日志**。
export async function asrTranscribe(blob: Blob, filename = 'mic.webm'): Promise<string> {
  const fd = new FormData()
  fd.append('file', blob, filename)
  // ★ Content-Type 一律不手动设：multipart 的 boundary 由浏览器随 FormData 自动带上，
  //   手写成 multipart/form-data 会因为缺 boundary 让后端 ParseMultipartForm 直接失败
  //   （这种失败看起来像"文件没传上去"，排查方向容易跑偏）。
  const r = await fetch('/api/chat/asr', { method: 'POST', headers: authHeaders(), body: fd })
  const text = await r.text()
  // 三种出参形态一起声明清楚，免得将来加字段又踩"按 any 取键"：
  //   text/reason ＝ 200 的正常出参与「这一次没有语音」那两档；
  //   message/code ＝ 协议/鉴权类失败走统一错误出口 s.writeError 后的字段（见 internal/errors/codes.go 的 APIError）。
  let payload: { text?: string; error?: string; reason?: string; suggest?: string; message?: string; code?: string } = {}
  try { payload = JSON.parse(text) } catch { /* 后端返回空体时保持空对象 */ }
  if (!r.ok) throw new VoiceError(payload.message || payload.error || 'asr-http-' + r.status, payload.reason || 'failed', payload.suggest || '')
  if (payload.error) throw new VoiceError(payload.error, payload.reason || 'failed', payload.suggest || '')
  return payload.text || ''
}

// speakAloud 请求朗读某段译文，返回可直接喂给 <audio> 的字节。
// 失败一律 throw **VoiceError**（带后端档位；后端同时给了可行动的人话句子，那条只进日志）。
// ★ 为什么用裸 fetch 而不是 api/core 的 request：request 已经 json 化出参，
//   音频是二进制流，套一层只会把 wav 字节当文本解析坏掉。
//
// ★ 2026-10-21 UAT 收口 M8：这条请求必须自带墙钟预算。
//   服务端单次合成预算 60s（fileproc.ttsTimeout），但浏览器 fetch **默认没有超时**——
//   旧写法一旦上游卡住（模型在 CPU 上加载权重、进程被 nice 降级、链路抖动），按钮就永远停在
//   loading 态、用户只能刷新页面。75s 留出服务端 60s 预算 + 一次往返的余量；
//   超时归到 `timeout` 档（与 failed 分开），界面按档提示"稍后重试"，不把一次慢合成说成"能力没有"。
const TTS_FETCH_TIMEOUT_MS = 75_000
// ★ 2026-10-21 M8：首字节探针。合成进程可能已经起来但上游卡在模型加载/排队，
//   迟迟不吐第一个字节；用户此时看到的仍是 loading。5s 内没有首字节即判 timeout，
//   中止请求并让按钮回到可重试态——不把整个 75s 都押在"也许马上就好"上。
const TTS_FIRST_BYTE_TIMEOUT_MS = 5_000

// readTtsBody 读取音频响应体，并给「首字节」单独一只 5s 秒表。
// 为什么不用 r.blob()：它只会在**整段收完**后才 resolve，无法区分「还在下」与「上游没开始吐」；
// 首字节探针必须直接读 stream。
async function readTtsBody(r: Response, ctrl: AbortController): Promise<Blob> {
  const type = r.headers.get('Content-Type') || 'audio/wav'
  if (!r.body) return r.blob()
  const reader = r.body.getReader()
  const first = await new Promise<ReadableStreamReadResult<Uint8Array>>((resolve, reject) => {
    let settled = false
    const timer = setTimeout(() => {
      if (settled) return
      settled = true
      ctrl.abort()
      reject(new VoiceError('tts-first-byte-timeout', 'timeout'))
    }, TTS_FIRST_BYTE_TIMEOUT_MS)
    reader.read().then(
      (v) => {
        if (settled) return
        settled = true
        clearTimeout(timer)
        resolve(v)
      },
      (e) => {
        if (settled) return
        settled = true
        clearTimeout(timer)
        reject(e)
      },
    )
  })
  const chunks: Uint8Array[] = []
  if (!first.done && first.value) chunks.push(first.value)
  for (;;) {
    const { done, value } = await reader.read()
    if (done) break
    if (value) chunks.push(value)
  }
  return new Blob(chunks, { type })
}

/** speakAloud 请求服务端 TTS 并返回音频 Blob；首字节超时按 timeout 档抛 VoiceError，供界面出本地化文案。 */
export async function speakAloud(text: string, lang: string): Promise<Blob> {
  const ctrl = new AbortController()
  const timer = setTimeout(() => ctrl.abort(), TTS_FETCH_TIMEOUT_MS)
  try {
    let r: Response
    try {
      r = await fetch(`${API_BASE}/api/chat/tts`, {
        method: 'POST',
        headers: { ...authHeaders(), 'Content-Type': 'application/json' },
        body: JSON.stringify({ text, lang }),
        signal: ctrl.signal,
      })
    } catch (e) {
      // AbortError 只可能来自上面那只总预算定时器（调用方目前不传外部 signal）：
      // 明确成 timeout 档，别让它落进默认 failed 被读成"服务器坏了"。
      if ((e as { name?: string } | null)?.name === 'AbortError') {
        throw new VoiceError('tts-timeout', 'timeout')
      }
      throw e
    }
    const ct = r.headers.get('Content-Type') || ''
    // ★ 为什么还要判 application/json：后端把「TTS 不可用」做成 HTTP 200 + error 体
    //   （口径见 chat_audio.go：朗读写不出来不是协议错误，而是一次没有语音的结果），
    //   只看 r.ok 会把错误体当成音频喂给 <audio>，播出来是解码错误、看不见人话。
    if (!r.ok || ct.includes('application/json')) {
      let msg = ''
      let reason = ''
      try {
        const j = await r.json()
        // message＝统一错误出口（s.writeError）的字段名，error＝"这一次没有语音"那两档的字段名；
        // 两条都在这里收一次，调用方不必再关心后端走的是哪一条腿。
        msg = (j && (j.error || j.message)) || ''
        reason = (j && j.reason) || ''
      } catch { /* 解析失败就退回状态码文案 */ }
      throw new VoiceError(msg || 'tts-http-' + r.status, reason)
    }
    try {
      return await readTtsBody(r, ctrl)
    } catch (e) {
      if (e instanceof VoiceError) throw e
      if ((e as { name?: string } | null)?.name === 'AbortError') {
        throw new VoiceError('tts-timeout', 'timeout')
      }
      throw e
    }
  } finally {
    clearTimeout(timer)
  }
}

// releaseAudioUrl 释放 blob URL（不释放会让一段音频一直在内存里，点十次就十个副本）。
export function releaseAudioUrl(url: string | null): void {
  if (url && url.startsWith('blob:')) URL.revokeObjectURL(url)
}
