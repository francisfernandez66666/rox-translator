// ============================================================================
// lib/chatLimit.ts — 即时对话「文本过长」本地闸（★ 2026-09-26 〇-U 批 I-8 · F-52②）
// ----------------------------------------------------------------------------
// 为什么要有这个文件：后端 /api/chat/stream 一直有体积闸（stream.go 的 chatTextOverLimit，
// 运营策略键 chat_max_chars 默认 5,000 字符），超限在 SSE 头写出前回 400 +
// code=chat_text_too_long。但前端**从来没有对应的本地判据**，于是用户的实际体验是：
// 贴一篇长文 → 气泡转圈 → 收到一个失败请求 → 才知道不行（而长文本该走翻译工单）。
// 批 D 之前更糟：这条请求会一路打到引擎、被 Cloudflare ~100s 掐成 524 HTML 错误页。
//
// ★ 数值只从服务端来，这里一个默认值都不写死：
//   setChatMaxChars() 由 /api/me/package 的 chat_max_chars 喂养（后端同一函数
//   s.chatMaxChars() 的出参），所以运营调键后本地闸自动跟着变。
//   取不到（未登录、接口失败、老后端没这个字段）时保持 0 = **不设闸**，
//   绝不用「猜一个上限」去拦人——误拦合法请求比少一次前置提示严重得多，
//   真超了后端那道闸还在，用户仍会拿到那句「文本过长（N 字符，上限 M 字符）…」。
// ============================================================================

/** 生效上限；0 = 未知（不设本地闸） */
let chatMaxChars = 0

/** 由 /api/me/package 出参喂养（非正数/非数字一律当作「未知」清空，避免脏值把闸钉死） */
export function setChatMaxChars(n: unknown): void {
  chatMaxChars = typeof n === 'number' && Number.isFinite(n) && n > 0 ? Math.floor(n) : 0
}

/** 当前生效上限（0 = 未知） */
export function getChatMaxChars(): number {
  return chatMaxChars
}

/**
 * 字符数口径——与后端 chatTextOverLimit 逐字对齐：先 trim 再按**码点**计数
 * （`[...str].length`），中文一字一符、emoji 一符，和 Go 侧 `len([]rune(...))` 同义。
 * ★ 不用 str.length：UTF-16 下 emoji / 生僻字算 2，会让本地闸比后端闸更严（误拦）。
 */
export function chatCharCount(text: string): number {
  return [...(text ?? '').trim()].length
}

/** 判超限：上限未知时永远返回 false（见文件头「不设闸」口径） */
export function overChatLimit(text: string): { over: boolean; count: number; max: number } {
  const count = chatCharCount(text)
  return { over: chatMaxChars > 0 && count > chatMaxChars, count, max: chatMaxChars }
}
