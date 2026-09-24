// ============================================================================
// utils/utm.ts — S4 归因捕获（2026-09-14）
// 落地页把 URL 中的 utm_* 存 localStorage('utm')；注册提交时取出随表单上报。
// 一次性消费：注册成功后清除，避免同浏览器反复注册串行归因。
// ============================================================================

/** URL 归因五参数快照（utm_source/medium/campaign/term/content）：落地页捕获→注册时随表单上报→成功即清除 */
export interface UtmSnapshot { utm_source?: string; utm_medium?: string; utm_campaign?: string; utm_term?: string; utm_content?: string }

/** 读取并清洗 localStorage 中的 utm_* 快照（仅保留 5 个约定键、字符串且 ≤120 字符）。 */
export function readUtm(): UtmSnapshot {
  try {
    const raw = localStorage.getItem('utm')
    if (!raw) return {}
    const o = JSON.parse(raw) as UtmSnapshot
    const out: UtmSnapshot = {}
    ;(['utm_source', 'utm_medium', 'utm_campaign', 'utm_term', 'utm_content'] as const).forEach((k) => {
      const v = o[k]
      if (typeof v === 'string' && v.length <= 120) out[k] = v
    })
    return out
  } catch { return {} }
}

/** 注册提交后一次性消费：清除已存的 utm，防同浏览器重复归因。 */
export function clearUtm(): void {
  try { localStorage.removeItem('utm') } catch { /* ignore */ }
}
