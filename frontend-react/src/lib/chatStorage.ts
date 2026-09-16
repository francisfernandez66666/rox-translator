// ============================================================================
// lib/chatStorage.ts — 聊天记录持久化纯函数（★ F11；自 useChat 抽提，语义不变）
// 键规则（E2）：chat_msgs_v1:<uid>，按账号隔离；匿名（:anon）不落聊天记录。
// 落盘规则（E1）：进度字段剥离、上限 MAX_MESSAGES（取尾部）、>2MB 裁最近 50 条。
// ============================================================================
import type { ChatMessage } from '@/types'

/** 消息条数硬上限（超出取最近 N 条） */
export const MAX_MESSAGES = 200
/** 落盘体积红线：超过则裁剪到 KEEP_ON_BLOAT 条 */
export const BLOAT_BYTES = 2 * 1024 * 1024
/** KEEP_ON_BLOAT 超体积裁剪后保留的最近会话条数 */
export const KEEP_ON_BLOAT = 50

/** msgsKeyFor 账号隔离键（E2）：uid 缺省/0 → ':anon'（不落盘仅内存） */
export function msgsKeyFor(uid?: number | null): string {
  return `chat_msgs_v1:${uid && uid > 0 ? uid : 'anon'}`
}

/** loadMsgs 解析恢复串：损坏/非数组返回空；条数收敛到尾部 MAX_MESSAGES */
export function loadMsgs(raw: string | null): ChatMessage[] {
  try {
    if (raw) {
      const arr = JSON.parse(raw) as ChatMessage[]
      if (Array.isArray(arr)) return arr.slice(-MAX_MESSAGES)
    }
  } catch { /* 损坏忽略 */ }
  return []
}

/**
 * serializeForPersist 落盘序列化（E1 回填持久化 + 进度剥离 + 2MB 裁剪）。
 * 返回 null 表示不落盘（匿名会话）。
 */
export function serializeForPersist(key: string, msgs: ChatMessage[]): string | null {
  if (key.endsWith(':anon')) return null
  let arr = msgs
  if (arr.length > KEEP_ON_BLOAT && JSON.stringify(arr).length > BLOAT_BYTES) arr = arr.slice(-KEEP_ON_BLOAT)
  return JSON.stringify(arr.map((m) => ({ ...m, progress: undefined })))
}
