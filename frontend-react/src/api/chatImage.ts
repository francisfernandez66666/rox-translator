// ============================================================================
// api/chatImage.ts — ★ 2026-10-17 即时翻译的**图片翻译**（用户令推翻 #36 旧决策）
//
// 与后端 chat_image.go 同一口径（改一侧要念一遍另一侧）：
//   · 本端点**做翻译**（不是 asr/tts 那种"只做格式转换"的上游适配器），因此它会走
//     配额闸门与计费——与 /api/chat 同权同责；调用方在界面上要按"一次翻译"对待。
//   · 产出两件：① 各目标语译文文本 ② 在**原图上按原坐标回填译文**的译后图（data URL）。
//     译后图是**择优产出**：后端回填失败时 image 为空串、image_error 说明原因，译文文本照常返回。
//   · 能力不可用（后端 OCR 缺失）时后端回 HTTP 200 + {error, reason}，这里统一转成
//     ImageError（带 reason），让界面按 12 语种词典说话，并把入口交给 voiceCapability 的 ocr 门。
//
// ★ 为什么单列一份而不是塞进 chatVoice.ts：语音与图片是两组独立能力（各有各的 /api/health 门），
//   混在一个文件里将来拆任何一组都要动另一组。
// ============================================================================
import { API_BASE, authHeaders } from './core'

// ChatImageResult 后端成功出参（字段名与 chat_image.go 的 chatImageResp 逐字对应）。
export type ChatImageResult = {
  text: string
  translations: Record<string, string>
  image_lang: string
  image: string // data URL（PNG）；降级时为空串
  image_error: string // 降级原因（空＝正常产出）
  line_count: number
  points_used: number
  mode: string
  target_langs: string[]
}

// ImageError 图片翻译失败：除人话句子外还带**档位名**（reason）。
// ★ 与 VoiceError 同族：后端那句中文是给运维日志的，界面一律按 reason 取 12 语种词典，
//   不把中文句子投给非中文界面。
export class ImageError extends Error {
  readonly reason: string
  constructor(message: string, reason: string) {
    super(message)
    this.name = 'ImageError'
    this.reason = reason || 'failed'
  }
}

// chatImageTranslate 上传一张图片，返回译文文本与译后图。
// 参数：file＝图片文件；targetLangs＝目标语种列表（首个用于译后图）；mode＝fast/pro；lang＝界面语言。
// 失败一律 throw ImageError（reason 供界面取词典；message 只进日志）。
export async function chatImageTranslate(
  file: File,
  targetLangs: string[],
  mode: string,
  lang: string,
): Promise<ChatImageResult> {
  const fd = new FormData()
  fd.append('file', file, file.name || 'image.png')
  fd.append('target_langs', targetLangs.join(','))
  if (mode) fd.append('mode', mode)
  if (lang) fd.append('lang', lang)
  // ★ Content-Type 不手设：multipart 的 boundary 由浏览器随 FormData 自动带上（同 chatVoice.asrTranscribe）。
  const r = await fetch(`${API_BASE}/api/chat/image`, { method: 'POST', headers: authHeaders(), body: fd })
  const text = await r.text()
  let j: Partial<ChatImageResult> & { error?: string; reason?: string; message?: string; code?: string } = {}
  try { j = JSON.parse(text) } catch { /* 空体时保持空对象 */ }
  if (!r.ok) throw new ImageError(j.message || j.error || 'image-http-' + r.status, j.reason || 'failed')
  // 后端把"这一次没有产出"做成 200 + {error, reason}（能力缺失 / 图里没字 / 翻译失败），统一转 throw。
  if (j.error) throw new ImageError(j.error, j.reason || 'failed')
  return {
    text: j.text || '',
    translations: j.translations || {},
    image_lang: j.image_lang || '',
    image: j.image || '',
    image_error: j.image_error || '',
    line_count: j.line_count || 0,
    points_used: j.points_used || 0,
    mode: j.mode || '',
    target_langs: j.target_langs || [],
  }
}
