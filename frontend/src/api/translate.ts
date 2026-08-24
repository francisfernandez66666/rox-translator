// ============================================================================
// api/translate.ts — 翻译域接口
// 职责：SSE 流式聊天翻译、文件翻译、健康检查（均基于 core 的 fetch 能力）
// ============================================================================

import type { ChatResponse, HealthResponse, ProgressEvent } from '@/types'
import { API_BASE, authHeaders, request } from './core'

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
          throw new Error(event.error || '翻译出错')
        }
      } catch (e) {
        if (e instanceof Error && !e.message.includes('JSON')) throw e
      }
    }
  }
  if (!finalResult) throw new Error('未收到翻译结果')
  return finalResult
}

/** 健康检查（10 秒超时：后端挂起时快速判定离线，不无限等待） */
export async function healthCheck(): Promise<HealthResponse> {
  return request('/api/health', { timeoutMs: 10000 })
}

/** 翻译前报价预估（只读不扣减）：text 或 segment_count 二选一，与计量同口径 */
export async function translationEstimate(
  data: { text?: string; segment_count?: number; target_langs?: string[]; mode?: string },
): Promise<EstimateResp> {
  return request('/api/translation/estimate', {
    method: 'POST',
    headers: authHeaders(),
    body: JSON.stringify(data),
  })
}

/** 报价预估响应结构 */
export interface EstimateResp {
  success: boolean
  message?: string
  sentences: number // 源句数（文本实时计算或文件段数）
  langs: number     // 目标语言数
  cost: number      // 预计消耗句数 = sentences × langs
  balance: number   // 当前句数余额（-1=平台上下文不限）
  sentence_enforced: boolean
  activated: boolean // 租户是否已开通额度
  hint?: string      // 未开通时的提示文案
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
          throw new Error(event.error || '文件翻译出错')
        }
      } catch (e) {
        if (e instanceof Error && !e.message.includes('JSON')) throw e
      }
    }
  }
  if (!finalResult) throw new Error('未收到翻译结果')
  return finalResult
}