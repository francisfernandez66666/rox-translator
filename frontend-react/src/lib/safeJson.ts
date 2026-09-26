// ============================================================================
// lib/safeJson.ts — 脏 JSON 文本的「必须是对象」安全解析（★ 2026-09-26 〇-U 批 I-2）
// ----------------------------------------------------------------------------
// 为什么要有这个文件：`try { JSON.parse(x) } catch { return {} }` 看起来安全，其实漏了
// 最难发现的一类脏值——字符串 "null" 是**合法 JSON**，parse 不抛错而返回 null，
// try/catch 完全拦不住；调用方紧接着 `Object.entries(null)` 才抛 TypeError，
// 在 React 里表现为「点进这条记录，整块详情白屏」（线上反馈详情的真实事故 F-45）。
// 同族脏值还有 "true"/"123"/"[1,2]"：都能 parse 成功，都不是我们要的键值映射。
// 规则：解析失败、或非「普通对象」（数组/null/标量）→ 一律回退调用方给的默认值。
// ============================================================================

/**
 * parseStringMap — 把「语种→译文」一类字符串映射 JSON 安全解析成对象。
 * @param raw   后端回带的 JSON 文本；允许空串 / null / "null" / 数组 / 标量等脏值。
 * @param fallback 解析不成普通对象时的返回值；缺省给一个**新的**空对象
 *                 （不共享常量实例，避免调用方往返回值里塞键值污染全局兜底）。
 * @returns 普通对象时原样返回（键值按字符串处理），否则返回 fallback。
 */
export function parseStringMap(raw: unknown, fallback?: Record<string, string>): Record<string, string> {
  const empty = (): Record<string, string> => fallback ?? {}
  if (typeof raw !== 'string' || raw.trim() === '') return empty()
  let parsed: unknown
  try {
    parsed = JSON.parse(raw)
  } catch {
    return empty()
  }
  // 只有「非 null 的对象且不是数组」才算可用映射：null/true/1/'abc'/[] 全部降级
  if (parsed === null || typeof parsed !== 'object' || Array.isArray(parsed)) return empty()
  return parsed as Record<string, string>
}
