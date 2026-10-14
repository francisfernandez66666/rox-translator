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
export function startMicRecording(onError: (msg: string) => void): {
  stop: () => Promise<Blob | null>
  cancel: () => void
} | null {
  if (!micSupported()) return null
  let stream: MediaStream | null = null
  let rec: MediaRecorder | null = null
  const chunks: BlobPart[] = []
  let stopped = false

  const setup = async (): Promise<boolean> => {
    try {
      stream = await navigator.mediaDevices.getUserMedia({ audio: true })
    } catch {
      onError('mic-denied')
      return false
    }
    rec = new MediaRecorder(stream)
    rec.ondataavailable = (e) => { if (e.data && e.data.size > 0) chunks.push(e.data) }
    rec.onerror = () => onError('mic-failed')
    rec.start()
    return true
  }
  void setup().then((ok) => { if (!ok) stopped = true })

  return {
    stop: () => new Promise<Blob | null>((resolve) => {
      if (!rec || stopped) { resolve(null); return }
      const done = () => {
        stopped = true
        stream?.getTracks().forEach((t) => t.stop())
        resolve(chunks.length ? new Blob(chunks, { type: rec?.mimeType || 'audio/webm' }) : null)
      }
      rec.onstop = done
      if (rec.state !== 'inactive') rec.stop()
      else done()
    }),
    cancel: () => {
      stopped = true
      try { if (rec && rec.state !== 'inactive') rec.stop() } catch { /* 忽略 */ }
      chunks.length = 0
      stream?.getTracks().forEach((t) => t.stop())
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
  constructor(message: string, reason: string) {
    super(message)
    this.name = 'VoiceError'
    this.reason = reason || 'failed'
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
  let payload: { text?: string; error?: string; reason?: string; message?: string; code?: string } = {}
  try { payload = JSON.parse(text) } catch { /* 后端返回空体时保持空对象 */ }
  if (!r.ok) throw new VoiceError(payload.message || payload.error || 'asr-http-' + r.status, payload.reason || 'failed')
  if (payload.error) throw new VoiceError(payload.error, payload.reason || 'failed')
  return payload.text || ''
}

// speakAloud 请求朗读某段译文，返回可直接喂给 <audio> 的字节。
// 失败一律 throw **VoiceError**（带后端档位；后端同时给了可行动的人话句子，那条只进日志）。
// ★ 为什么用裸 fetch 而不是 api/core 的 request：request 已经 json 化出参，
//   音频是二进制流，套一层只会把 wav 字节当文本解析坏掉。
export async function speakAloud(text: string, lang: string): Promise<Blob> {
  const r = await fetch(`${API_BASE}/api/chat/tts`, {
    method: 'POST',
    headers: { ...authHeaders(), 'Content-Type': 'application/json' },
    body: JSON.stringify({ text, lang }),
  })
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
  return r.blob()
}

// releaseAudioUrl 释放 blob URL（不释放会让一段音频一直在内存里，点十次就十个副本）。
export function releaseAudioUrl(url: string | null): void {
  if (url && url.startsWith('blob:')) URL.revokeObjectURL(url)
}
