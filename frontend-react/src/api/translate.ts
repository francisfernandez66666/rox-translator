// ============================================================================
// api/translate.ts — 翻译域接口
// 职责：SSE 流式聊天翻译、文件翻译、健康检查（均基于 core 的 fetch 能力）
// ============================================================================

/**
 * api/translate.ts · 职责说明
 * 封装翻译相关的所有接口，包括：
 * - 文本翻译：SSE 流式聊天翻译，支持进度回调和中断
 * - 文件翻译：SSE 流式文件翻译，支持多语言和进度回调
 * - 健康检查：后端服务状态检测（10 秒超时）
 * - 文件校验：翻译文件格式和大小校验（白名单 + 40MB 上限）
 */

import type { ChatResponse, HealthResponse, ProgressEvent } from '@/types'
import { API_BASE, authHeaders, request, handleUnauthorized, ApiError } from './core'

/** SSE 公共解析器：从 ReadableStream 逐行解析 SSE 事件，回调进度，返回最终结果 */
async function consumeSSEStream(
  reader: ReadableStreamDefaultReader<Uint8Array>,
  onProgress?: (event: ProgressEvent) => void,
  errorMessage = '翻译出错',
  onDelta?: (lang: string, text: string) => void, // ★ D20：token 级流式增量
): Promise<ChatResponse> {
  const decoder = new TextDecoder()
  let buffer = ''
  let finalResult: ChatResponse | null = null

  while (true) {
    const { done, value } = await reader.read()
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
  if (!finalResult) throw new Error('未收到翻译结果')
  return finalResult
}

/** SSE 流式聊天接口 */
export async function chatStream(
  message: string,
  skill?: string,
  options?: Record<string, unknown>,
  onProgress?: (event: ProgressEvent) => void,
  signal?: AbortSignal,
  onDelta?: (lang: string, text: string) => void, // ★ D20
): Promise<ChatResponse> {
  const body = JSON.stringify({ message, skill: skill || '', options: options || {} })
  const response = await fetch(`${API_BASE}/api/chat/stream`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...authHeaders() },
    body,
    signal,
  })

  if (!response.ok) {
    const errorText = await response.text()
    // ★ E5：SSE 通道 401 与其他请求同源处理——清登录态并跳登录，而不是只渲染"请求失败(401)"
    if (response.status === 401) {
      handleUnauthorized('/api/chat/stream')
      throw new ApiError('登录已过期，请重新登录', 401)
    }
    throw new Error(`请求失败 (${response.status}): ${errorText}`)
  }

  const reader = response.body?.getReader()
  if (!reader) throw new Error('无法读取流式响应')

  return consumeSSEStream(reader, onProgress, '翻译出错', onDelta)
}

/** 健康检查（10 秒超时：后端挂起时快速判定离线，不无限等待） */
// ★ F7：翻译前 token 消耗预估（/api/translation/estimate，后端已具备）
export interface EstimateResp {
  success: boolean
  sentences: number
  tokens_min: number
  tokens_max: number
  balance_tokens: number
  sufficient: boolean
  hint?: string
}
// 翻译前预估 token 消耗与费用（失败静默返回 null，不打断输入）
export async function estimateTranslation(text: string, targetLangs: string[], mode = 'pro'): Promise<EstimateResp | null> {
  try {
    const j = await request<EstimateResp>('/api/translation/estimate', { method: 'POST', body: JSON.stringify({ text, target_langs: targetLangs, mode }) })
    return j?.success ? j : null
  } catch { return null } // 预估失败静默（不打断输入）
}

// 后端健康检查（10s 超时）
export async function healthCheck(): Promise<HealthResponse> {
  return request('/api/health', { timeoutMs: 10000 })
}


/** SSE 流式文件翻译 */
export async function translateFileStream(
  file: File,
  targetLangs?: string[],
  useOnline: boolean = true,
  onProgress?: (event: ProgressEvent) => void,
  signal?: AbortSignal,
  userMessage: string = "",
  mode?: string,
  maxLength?: number,
): Promise<ChatResponse> {
  const formData = new FormData()
  formData.append('file', file)
  if (targetLangs && targetLangs.length > 0) {
    formData.append('target_langs', targetLangs.join(','))
  }
  formData.append('use_online', String(useOnline))
  if (userMessage) formData.append('message', userMessage)
  // ★ 双模式：fast 快速（无KB）/ pro 专业校对；随表单透传后端
  if (mode) formData.append('mode', mode)
  // ★ 缩翻（任务7）：最长字符限制随表单透传后端（>0 启用缩翻）
  if (maxLength && maxLength > 0) formData.append('max_length', String(maxLength))

  // 文件上传用登录令牌认证头（不带租户头），与后端文件翻译接口对齐
  const response = await fetch(`${API_BASE}/api/translate/stream`, {
    method: 'POST',
    headers: authHeaders(),
    body: formData,
    signal,
  })

  if (!response.ok) {
    const errorText = await response.text()
    // ★ E5：文件流式翻译 401 同上
    if (response.status === 401) {
      handleUnauthorized('/api/translate/stream')
      throw new ApiError('登录已过期，请重新登录', 401)
    }
    throw new Error(`文件翻译失败 (${response.status}): ${errorText}`)
  }

  const reader = response.body?.getReader()
  if (!reader) throw new Error('无法读取流式响应')

  return consumeSSEStream(reader, onProgress, '文件翻译出错')
}

// ============ 翻译文件格式/大小校验（即时翻译与工单翻译共用，保证两端一致） ============
// 与后端 translateExtWhitelist 保持一致：docx/xlsx/pptx/pdf/txt/csv/srt/vtt/md/json/yaml/yml
export const TRANSLATE_FILE_EXTS = [
  '.docx', '.xlsx', '.pptx', '.pdf', '.txt', '.csv', '.srt', '.vtt', '.md', '.json', '.yaml', '.yml',
] as const

// ★ 工单双模式（2026-09-13）：仅「纯文案模式」准入的格式（anydoc 本地提取转 Markdown，不还原版式）
export const TEXT_DELIVERY_ONLY_EXTS = [
  '.doc', '.docm', '.ppt', '.pps', '.pot', '.pptm', '.ppsx', '.ppsm',
  '.xls', '.xlsm', '.xlsb', '.odt', '.ods', '.odp', '.rtf', '.epub',
] as const

// 文件选择框 accept 属性（即时翻译与工单翻译共用，避免两端格式不一致）
export const TRANSLATE_FILE_ACCEPT = TRANSLATE_FILE_EXTS.join(',')

// 纯文案模式的 accept（还原文件模式既有 12 种 + anydoc 独占老格式/ODF/RTF/EPUB）
export const TEXT_DELIVERY_ACCEPT = [...TRANSLATE_FILE_EXTS, ...TEXT_DELIVERY_ONLY_EXTS].join(',')

// 单文件大小上限：与后端 translateUploadMax 对齐（40MB）
export const TRANSLATE_FILE_MAX_BYTES = 40 * 1024 * 1024

/**
 * 校验待翻译文件：返回错误原因字符串（含「为什么不能翻译」）或 null（通过）。
 * - 格式不在白名单：提示支持的格式
 * - 体积超过上限：提示具体大小与上限
 * @param deliveryText 工单「纯文案模式」放宽格式准入（即时翻译不适用，保持默认 false）
 */
export function validateTranslateFile(file: File, deliveryText = false): string | null {
  const ext = '.' + (file.name.split('.').pop() || '').toLowerCase()
  const allowed: readonly string[] = deliveryText ? [...TRANSLATE_FILE_EXTS, ...TEXT_DELIVERY_ONLY_EXTS] : TRANSLATE_FILE_EXTS
  if (!allowed.includes(ext)) {
    if (deliveryText) {
      return `不支持的文件格式：${file.name}（纯文案模式支持 ${[...TRANSLATE_FILE_EXTS, ...TEXT_DELIVERY_ONLY_EXTS].join(' / ')}）`
    }
    if (TEXT_DELIVERY_ONLY_EXTS.includes(ext as (typeof TEXT_DELIVERY_ONLY_EXTS)[number])) {
      return `不支持的文件格式：${file.name}（${ext} 老格式仅在工单「纯文案模式」下支持；或请先转换为对应新版格式）`
    }
    return `不支持的文件格式：${file.name}（仅支持 ${TRANSLATE_FILE_EXTS.join(' / ')}）`
  }
  if (file.size > TRANSLATE_FILE_MAX_BYTES) {
    const mb = (file.size / 1024 / 1024).toFixed(1)
    return `文件过大（${mb}MB），超出翻译上限 ${TRANSLATE_FILE_MAX_BYTES / 1024 / 1024}MB，请拆分或压缩后重试`
  }
  return null
}