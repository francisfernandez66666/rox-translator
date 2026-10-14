// ============================================================================
// api/translate.ts — 翻译域接口
// 职责：SSE 流式聊天翻译、健康检查（均基于 core 的 fetch 能力）、翻译文件校验工具
//       （★ #36 2026-09-21：SSE 流式文件翻译函数已随即时翻译下线文件入口而移除，
//        文件翻译走工单 /api/tickets/create-file；本文件的校验工具仍被工单页使用）
// ============================================================================

/**
 * api/translate.ts · 职责说明
 * 封装翻译相关的接口与工具，包括：
 * - 文本翻译：SSE 流式聊天翻译，支持进度回调、逐字增量和中断
 * - SSE 解析：consumeSSEStream 公共解析器（单测直接喂假 reader 覆盖）
 * - 健康检查：后端服务状态检测（10 秒超时）
 * - 文件校验：翻译文件格式和大小校验（白名单 + 40MB 上限，供工单翻译页使用）
 */

import type { ChatResponse, FileSegmentEvent, HealthResponse, ProgressEvent } from '@/types'
import { API_BASE, authHeaders, request, handleUnauthorized, readErrEnvelope, ApiError, apiMsg } from './core'

/** SSE 空闲超时：后端每 20s 发一帧 `: ping` 注释（不匹配 data: 但计入字节、重置计时）。
 *  连续 SSE_IDLE_MS 无任何字节 = 判定代理静默断连，主动中断避免 UI 永卡 loading。 */
const SSE_IDLE_MS = 60_000
/** 带空闲超时的 reader.read() 包装：每次拿到字节即重置计时；
 *  空闲超限触发 onIdle（外部据此 abort 请求）并以 AbortError 拒绝本 Promise。 */
function readWithIdle<T extends { done: boolean; value?: Uint8Array }>(
  reader: ReadableStreamDefaultReader<Uint8Array>,
  onIdle: () => void,
): Promise<T> {
  let timer: ReturnType<typeof setTimeout> | undefined
  const idle = new Promise<never>((_, rej) => {
    timer = setTimeout(() => { onIdle(); rej(new ApiError(apiMsg('tr.streamIdle', '连接空闲超时，请重试'), undefined, 'stream_idle')) }, SSE_IDLE_MS)
  })
  return Promise.race([reader.read() as Promise<T>, idle]).finally(() => { if (timer) clearTimeout(timer) }) as Promise<T>
}

/** SSE 公共解析器：从 ReadableStream 逐行解析 SSE 事件，回调进度，返回最终结果
 *  事件分流：progress→onProgress；delta→onDelta(lang,text)（D20 逐字流式，不参与最终结果）；
 *  ★B3 segment_done/segment_final/segments_sealed→onSegment（文件逐段上屏，不参与最终结果）；
 *  done→取 event.result 作为返回值；error→抛 ApiError（携带 error_code 稳定码）。
 *  （导出仅供单测喂假 reader；业务侧一律走 chatStream。） */
export async function consumeSSEStream(
  reader: ReadableStreamDefaultReader<Uint8Array>,
  onProgress?: (event: ProgressEvent) => void,
  errorMessage = apiMsg('tr.translateErr', '翻译出错'),
  onDelta?: (lang: string, text: string) => void, // ★ D20：token 级流式增量
  onSegment?: (event: FileSegmentEvent) => void,  // ★ B3：文件翻译逐段事件
  onWarning?: (secondsLeft: number) => void,      // ★ 决策⑪②：慢预警帧（deadline 前 ~15s）
): Promise<ChatResponse> {
  const decoder = new TextDecoder()
  let buffer = ''
  let finalResult: ChatResponse | null = null

  try {
    while (true) {
      // ★ 2026-09-16 P2：空闲超时护栏（旧版 reader.read() 在代理静默断连时永挂）
      const { done, value } = await readWithIdle(reader, () => { void reader.cancel() })
      if (done) break
      buffer += decoder.decode(value, { stream: true })
      const lines = buffer.split('\n')
      buffer = lines.pop() || ''
      for (const line of lines) {
        const trimmed = line.trim()
        if (!trimmed.startsWith('data: ')) continue
        const jsonStr = trimmed.slice(6)
        if (jsonStr === '[DONE]') continue
        try {
          const event: ProgressEvent = JSON.parse(jsonStr)
          if (event.type === 'progress' && onProgress) {
            onProgress(event)
          } else if (event.type === 'delta') {
            if (onDelta) onDelta(event.lang || '', event.text || '')
          } else if (event.type === 'segment_done' || event.type === 'segment_final') {
            // ★ B3：done→draft、final→target 拉平成统一 text 字段，消费端不再判别协议键
            if (onSegment) onSegment({
              kind: event.type,
              lang: event.lang || '',
              index: event.index,
              source: event.source,
              sourceHash: event.source_hash,
              text: event.type === 'segment_final' ? event.target : event.draft,
              stage: event.stage,
              placeholder: event.placeholder === true,
            })
          } else if (event.type === 'segments_sealed') {
            if (onSegment) onSegment({ kind: 'segments_sealed', lang: event.lang || '' })
          } else if (event.type === 'warning') {
            // ★ 决策⑪②：慢预警帧——后端在 deadline 前 ~15s 发出；不参与最终结果，
            //   只回调给调用方（useChat 据此在气泡上挂「继续等待/转工单」）。
            if (onWarning) onWarning(event.seconds_left ?? 0)
          } else if (event.type === 'done') {
            finalResult = event.result || null
          } else if (event.type === 'error') {
            // ★ E11：SSE error 事件透传稳定错误码（余额不足等可在 UI 差异化处理）
            throw new ApiError(event.error || errorMessage, undefined, event.error_code)
          }
        } catch (e) {
          if (e instanceof Error && !e.message.includes('JSON')) throw e
        }
      }
    }
  } catch (e) {
    // 空闲超时/网络错误统一转 ApiError（保留既有 ApiError 原样上抛）
    if (e instanceof ApiError) throw e
    if (e instanceof Error && e.name === 'AbortError') throw e
    throw new ApiError(e instanceof Error ? e.message : String(e), undefined, 'stream_error')
  }
  if (!finalResult) throw new Error(apiMsg('tr.noResult', '未收到翻译结果'))
  return finalResult
}

/** SSE 流式聊天接口
 *  @param onDelta 末位可选：单目标语言时把 token 级增量回灌给调用方（D20），
 *                 不传则只走 progress/done，行为与旧版一致 */
export async function chatStream(
  message: string,
  skill?: string,
  options?: Record<string, unknown>,
  onProgress?: (event: ProgressEvent) => void,
  signal?: AbortSignal,
  onDelta?: (lang: string, text: string) => void, // ★ D20
  onWarning?: (secondsLeft: number) => void,      // ★ 决策⑪②：慢预警帧透传
): Promise<ChatResponse> {
  const body = JSON.stringify({ message, skill: skill || '', options: options || {} })
  // ★ §4.2-2 正当豁免：SSE 流式通道必须裸用 fetch——request() 封装会把整份响应 response.json()
  //   一次性解析后返回，拿不到可读 stream，无法逐帧回调 progress/delta/done。故这里直连 fetch，
  //   并复用 handleUnauthorized 手工处理 401（见下方），与统一 client 的鉴权语义保持一致。
  const response = await fetch(`${API_BASE}/api/chat/stream`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...authHeaders() },
    body,
    signal,
  })

  if (!response.ok) {
    // ★ F-52（批 I-8 2026-09-26）：失败体改走 core 的统一解析器并抛 **ApiError（带稳定码）**。
    //   旧写法 `throw new Error(请求失败 (状态): ${整段响应体})` 有两个落点：
    //   ① 后端 4xx 现在回结构化 JSON（{success,code,message,details,trace_id}），整段 JSON
    //      被当正文送进聊天气泡，用户看到一屏大括号（useChat 只挡 HTML 网关页，对 JSON 无判）；
    //   ② 裸 Error 不带 code，chat_text_too_long / 限额一类「按码分支」的前端逻辑全部失效，
    //      等于 SSE 通道自成一套错误契约。
    //   现在：message 取后端那句、code 透传、body 原样留对象（trace_id 只在 body 里供复制排查，
    //   不进正文），display 对 JSON/HTML 恒为空——不会再有整段体进气泡这条路。
    const env = await readErrEnvelope(response)
    // ★ E5：SSE 通道 401 与其他请求同源处理——清登录态并跳登录，而不是只渲染"请求失败(401)"
    if (response.status === 401) {
      handleUnauthorized('/api/chat/stream')
      throw new ApiError(env.message || apiMsg('tr.sessionExpired', '登录已过期，请重新登录'), 401, env.code, env.body)
    }
    throw new ApiError(
      env.message || apiMsg('common.reqFailDetail', `请求失败 (${response.status})${env.display ? `: ${env.display}` : ''}`,
        { status: response.status, text: env.display }),
      response.status, env.code, env.body,
    )
  }

  const reader = response.body?.getReader()
  if (!reader) throw new Error(apiMsg('tr.streamReadFail', '无法读取流式响应'))

  return consumeSSEStream(reader, onProgress, apiMsg('tr.translateErr', '翻译出错'), onDelta, undefined, onWarning)
}

/** 健康检查（10 秒超时：后端挂起时快速判定离线，不无限等待） */
// ★ F7：翻译前积分消耗预估（/api/translation/estimate；2026-09-19 起全积分口径）
export interface EstimateResp {
  success: boolean
  sentences: number
  points_min: number
  points_max: number
  cost_sentences_approx: number
  points_balance: number
  balance_sentences_approx: number
  sufficient: boolean
  activated?: boolean
  hint?: string
  // ★ 决策⑪①（2026-10-10）：LLM 排队深度代理值（饱和代理，口径见后端 stream.go
  //   llmQueueDepth 注释）。参与前端 slowVerdict 三档预检；字段缺失时前端按保守档兜底。
  llm_queue_depth?: number
}
// 翻译前预估积分消耗与余额（失败静默返回 null，不打断输入）
// ★ 2026-10-13 用户令⑦（#31）：多带一个 ui_lang——后端预估的固定指令开销是按**真实提示词**
//
//	现算的（见 engine/costmodel.go），而中/英文界面走的是两套提示词，长度不同。
//	不送这个字段就只能按中文那一套量，英文界面访客会看到偏高的区间。
export async function estimateTranslation(text: string, targetLangs: string[], mode = 'pro', uiLang = ''): Promise<EstimateResp | null> {
  try {
    const j = await request<EstimateResp>('/api/translation/estimate', { method: 'POST', body: JSON.stringify({ text, target_langs: targetLangs, mode, ui_lang: uiLang }) })
    return j?.success ? j : null
  } catch { return null } // 预估失败静默（不打断输入）
}

// 后端健康检查（10s 超时）
export async function healthCheck(): Promise<HealthResponse> {
  return request('/api/health', { timeoutMs: 10000 })
}


// ★ #36（2026-09-21）：原 `translateFileStream`（POST /api/translate/stream 的 SSE 文件翻译）
// 已随「即时翻译不再支持文件翻译」下线——前端唯一调用方是即时翻译工作台的上传按钮。
// 后端路由仍保留（老客户端/桌面端兼容），文件翻译的产品入口统一为「文档翻译」工单
// （TicketsPage → /api/tickets/create-file，走工单审批/交付/保留期链路）。
// 下面的格式/大小校验函数仍被工单页使用，务必保留。

// ============ 翻译文件格式/大小校验（工单翻译入口使用，与后端白名单一致） ============
// ★ 这一份清单**不是**事实源，只是事实源的**镜像**：唯一事实源是
//   `backend-go/internal/fileproc/formats.go` 的 `TranslateFormatOrder`，
//   等值由 `src/api/translateFormats.lock.test.ts`（#43）逐项钉住——后端加一项而这里没跟上，
//   表现是"客户在文件选择框里根本选不中那个格式"（本批首跑就是这一形态被抓红：
//   后端 P1a 加了 8 个图片格式走 OCR，前端 accept 还停在 12 项）。
//   改这里必须同时确认后端那一行；只改一侧当场红。
export const TRANSLATE_FILE_EXTS = [
  '.docx', '.xlsx', '.pptx', '.pdf', '.txt', '.csv', '.srt', '.vtt', '.md', '.json', '.yaml', '.yml',
  // 图片格式：后端经 OCR 投喂翻译引擎（fileproc/formats.go 里标 P1a 那一行）
  '.png', '.jpg', '.jpeg', '.bmp', '.tif', '.tiff', '.webp', '.heic',
  // 音频格式：后端经 ASR 转写投喂翻译引擎，产物为译文字幕 .srt + 译文 .md（fileproc/formats.go 里标 P2 那一行）
  '.mp3', '.m4a', '.wav', '.ogg', '.flac', '.aac', '.opus',
] as const

// ★ 工单双模式（2026-09-13）：仅「纯文案模式」准入的格式（anydoc 本地提取转 Markdown，不还原版式）
export const TEXT_DELIVERY_ONLY_EXTS = [
  '.doc', '.docm', '.ppt', '.pps', '.pot', '.pptm', '.ppsx', '.ppsm',
  '.xls', '.xlsm', '.xlsb', '.odt', '.ods', '.odp', '.rtf', '.epub',
] as const

// ============ 素材类（需识别/转写才进翻译）============
// 这两类的共同点：**交付形态不是原格式**（图片不回填合成图、音频不改音轨），
// 客户必须被提前告知，否则下载产物时会把它当成「文件坏了/只翻了一半」。
// ★ 拆两个常量是为了让 UI 能按类显示不同话术（话术本身差异很大：图片出 .md、音频出 .srt+.md）；
//   它在 translateFormats.lock.test.ts 里有一条「三类并起来恰好等于 TRANSLATE_FILE_EXTS」的划分锁，
//   一边多一项而另一边没跟，那条会红——不要照着抄成第三份独立清单。
export const IMAGE_EXTS = ['.png', '.jpg', '.jpeg', '.bmp', '.tif', '.tiff', '.webp', '.heic'] as const

// 音频类：交付形态是**译文字幕 .srt ＋译文 .md**（不改音轨，后端走 ASR 转写，fileproc/asr.go）。
// 与图片类同属「需识别/转写才进翻译」这一族，划分锁见 translateFormats.lock.test.ts 的 P6 那条。
export const AUDIO_EXTS = ['.mp3', '.m4a', '.wav', '.ogg', '.flac', '.aac', '.opus'] as const

// 文件选择框 accept 属性（★ #36 后仅工单翻译页使用，保持与后端白名单一致）
export const TRANSLATE_FILE_ACCEPT = TRANSLATE_FILE_EXTS.join(',')

// 纯文案模式的 accept（还原文件模式那一整族 + anydoc 独占的老格式/ODF/RTF/EPUB；
// 项数不写死在这里——写死就是把别人的清单抄第二份，上面 #43 那条锁红过一次就是因为它过时了）
export const TEXT_DELIVERY_ACCEPT = [...TRANSLATE_FILE_EXTS, ...TEXT_DELIVERY_ONLY_EXTS].join(',')

// 单文件大小上限：事实源是后端 `fileproc.MaxUploadBytes`（api 侧的 `translateUploadMax` 只是它的薄委托）。
// 这里不抄具体数字——档位由 `translateFormats.lock.test.ts` 的 P3 把 Go 那句 `<n> << <bits>`
// **现读现算**再与这份常量等值钉住；抬档那天前端只改位移，写死数字的话锁就不会红。
export const TRANSLATE_FILE_MAX_BYTES = 40 * 1024 * 1024

/**
 * 校验待翻译文件：返回错误原因字符串（含「为什么不能翻译」）或 null（通过）。
 * - 格式不在白名单：提示支持的格式
 * - 体积超过上限：提示具体大小与上限
 * @param deliveryText 工单「纯文案模式」放宽格式准入（默认 false=还原文件模式口径）
 */
export function validateTranslateFile(file: File, deliveryText = false): string | null {
  const ext = '.' + (file.name.split('.').pop() || '').toLowerCase()
  const allowed: readonly string[] = deliveryText ? [...TRANSLATE_FILE_EXTS, ...TEXT_DELIVERY_ONLY_EXTS] : TRANSLATE_FILE_EXTS
  if (!allowed.includes(ext)) {
    if (deliveryText) {
      return apiMsg('tr.fileFmtText', `不支持的文件格式：${file.name}（纯文案模式支持 ${[...TRANSLATE_FILE_EXTS, ...TEXT_DELIVERY_ONLY_EXTS].join(' / ')}）`, { name: file.name, fmts: [...TRANSLATE_FILE_EXTS, ...TEXT_DELIVERY_ONLY_EXTS].join(' / ') })
    }
    if (TEXT_DELIVERY_ONLY_EXTS.includes(ext as (typeof TEXT_DELIVERY_ONLY_EXTS)[number])) {
      return apiMsg('tr.fileFmtLegacy', `不支持的文件格式：${file.name}（${ext} 老格式仅在工单「纯文案模式」下支持；或请先转换为对应新版格式）`, { name: file.name, ext })
    }
    return apiMsg('tr.fileFmtOnly', `不支持的文件格式：${file.name}（仅支持 ${TRANSLATE_FILE_EXTS.join(' / ')}）`, { name: file.name, fmts: TRANSLATE_FILE_EXTS.join(' / ') })
  }
  if (file.size > TRANSLATE_FILE_MAX_BYTES) {
    const mb = (file.size / 1024 / 1024).toFixed(1)
    return apiMsg('tr.fileTooBig', `文件过大（${mb}MB），超出翻译上限 ${TRANSLATE_FILE_MAX_BYTES / 1024 / 1024}MB，请拆分或压缩后重试`, { mb, maxMB: TRANSLATE_FILE_MAX_BYTES / 1024 / 1024 })
  }
  return null
}