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
import { API_BASE, authHeaders, request } from './core'

/** SSE 公共解析器：从 ReadableStream 逐行解析 SSE 事件，回调进度，返回最终结果 */
async function consumeSSEStream(
  reader: ReadableStreamDefaultReader<Uint8Array>,
  onProgress?: (event: ProgressEvent) => void,
  errorMessage = '翻译出错',
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
        } else if (event.type === 'done') {
          finalResult = event.result || null
        } else if (event.type === 'error') {
          throw new Error(event.error || errorMessage)
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
    throw new Error(`请求失败 (${response.status}): ${errorText}`)
  }

  const reader = response.body?.getReader()
  if (!reader) throw new Error('无法读取流式响应')

  return consumeSSEStream(reader, onProgress, '翻译出错')
}

/** 健康检查（10 秒超时：后端挂起时快速判定离线，不无限等待） */
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

// 文件选择框 accept 属性（即时翻译与工单翻译共用，避免两端格式不一致）
export const TRANSLATE_FILE_ACCEPT = TRANSLATE_FILE_EXTS.join(',')

// 单文件大小上限：与后端 translateUploadMax 对齐（40MB）
export const TRANSLATE_FILE_MAX_BYTES = 40 * 1024 * 1024

/**
 * 校验待翻译文件：返回错误原因字符串（含「为什么不能翻译」）或 null（通过）。
 * - 格式不在白名单：提示支持的格式
 * - 体积超过上限：提示具体大小与上限
 */
export function validateTranslateFile(file: File): string | null {
  const ext = '.' + (file.name.split('.').pop() || '').toLowerCase()
  if (!TRANSLATE_FILE_EXTS.includes(ext as (typeof TRANSLATE_FILE_EXTS)[number])) {
    return `不支持的文件格式：${file.name}（仅支持 ${TRANSLATE_FILE_EXTS.join(' / ')}）`
  }
  if (file.size > TRANSLATE_FILE_MAX_BYTES) {
    const mb = (file.size / 1024 / 1024).toFixed(1)
    return `文件过大（${mb}MB），超出翻译上限 ${TRANSLATE_FILE_MAX_BYTES / 1024 / 1024}MB，请拆分或压缩后重试`
  }
  return null
}