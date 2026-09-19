// ============================================================================
// lib/draftClean.ts — 流式初译草稿的 <t> 契约清洗纯函数（★ B1 流式双态）
// 与后端 internal/engine/postprocess.go 的 extractContractTranslation
// （contractTagRe = (?is)<t\b[^>]*>(.*?)</t\s*>）保持同一口径：
// 命中完整 <t>…</t> 时只取标签内译文、多块以换行拼接；未命中契约时原样返回。
// 与后端的差异只在「流式中途」：token 尚未走完时标签可能只出现一半，
// 这里把尾部的半截标签剥掉避免尖括号闪现在界面上，已完成部分照常展示。
// ============================================================================

// 输出契约完整对：与 Go 侧 contractTagRe 等价（容忍属性与大小写、跨行、容忍 </t 后空格）
const CONTRACT_PAIR_RE = /<t\b[^>]*>([\s\S]*?)<\/t\s*>/gi

/**
 * cleanDraft 清洗一段（可能未流完的）模型原始输出，返回可展示的初译文本。
 * 规则（与后端契约提取同口径，逐条对齐 postprocess.go）：
 * 1. 文本不含 "<t"（小写比较）→ 原样返回（模型违约/无契约场景，走黑名单链兜底的是后端，前端不重复清洗）；
 * 2. 命中一个或多个完整 <t>…</t> → 取标签内容 trim 后以换行拼接（多段分块保留）；
 * 3. 只有半截标签（契约尚未走完）→ 剥掉尾部未闭合的标签碎片，保留已生成的译文本体。
 */
export function cleanDraft(raw: string): string {
  if (!raw) return ''
  if (!raw.toLowerCase().includes('<t')) return raw

  // 规则 2：白名单提取完整契约对
  const parts: string[] = []
  CONTRACT_PAIR_RE.lastIndex = 0
  let m: RegExpExecArray | null
  while ((m = CONTRACT_PAIR_RE.exec(raw)) !== null) {
    const s = m[1].trim()
    if (s) parts.push(s)
  }
  if (parts.length > 0) return parts.join('\n')

  // 规则 3：契约未闭合的流式中途——依次剥掉
  //   ① 已闭合的开标签 <t ...>（其后的内容正是进行中的译文，保留）
  //   ② 尾部落了一半的开标签碎片（<t / <t attr 等，未见 > 为止）
  //   ③ 尾部落了一半的闭标签碎片（</t 等）
  return raw
    .replace(/<t\b[^>]*>/gi, '')
    .replace(/<\/?t\b[^>]*$/i, '')
    .replace(/<\/?t$/i, '')
    .trimEnd()
}
