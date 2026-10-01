// ============================================================================
// api/chat.ts — 聊天对话持久化域接口
// 职责：工作台 SSE 翻译通道的对话历史查询（列出最近会话 / 获取单条会话消息）
// ============================================================================

/**
 * api/chat.ts · 职责说明
 * 封装工作台翻译对话记录的所有接口，包括：
 * - 获取用户最近的对话列表（按 updated_at 降序）
 * - 获取单条对话的完整消息列表
 */

import { request, authHeaders, bizResp } from './core'

/** 对话列表项结构（剥离内部字段，只暴露 id/title/updated_at） */
export interface ChatConv {
  id: string           // 全局唯一 UUID v4
  title: string        // 自动生成的标题（第一条用户消息前 40 字）
  updated_at: string   // 最后活跃时间（ISO 8601）
}

/** 消息列表项结构（剥离自增 id、conversation_id 等内部字段） */
export interface ChatMsg {
  role: string         // "user" | "assistant"
  content: string      // 消息正文
  model: string        // 生成该回复使用的模型名（可空）
  created_at: string   // 发送时间戳
}

/** 对话接口统一响应结构 */
export interface ChatResp {
  success: boolean
  data?: ChatConv[] | ChatMsg[]
  message?: string
}

/** getChatHistory 获取当前用户最近的对话列表（默认 20 条，上限 100）。*/
export async function getChatHistory(limit = 20): Promise<ChatResp> {
  return bizResp(() => request(`/api/chat/list?limit=${limit}`, { headers: authHeaders() }))
}

/** getChatMessages 获取指定会话的消息列表（默认 100 条，上限 500）。*/
export async function getChatMessages(convId: string, limit = 100): Promise<ChatResp> {
  const params = new URLSearchParams({ id: convId })
  if (limit > 1 && limit <= 500) params.set('limit', String(limit))
  return bizResp(() => request(`/api/chat/messages?${params.toString()}`, { headers: authHeaders() }))
}
