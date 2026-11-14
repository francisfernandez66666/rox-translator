// ============================================================================
// lib/productPublicExhaust.ts — /p/ 托管页耗尽态「两副面孔」的薄桥接（四期 W2 §4.2.2-4）
//
// 两副面孔的**唯一文案源**是 productExhaustCopy.copyFor/copyText（一期 §8.5 X3：
// 任何组件不得自造第三处「销售忙」文案）：
//   · 访客面（visitor）：「销售忙…建议留言」；
//   · 卖家面（owner）：真实原因 ＋ resets_at（额度耗尽），且不许退化成「销售忙」。
// 本文件只做「按 audience 选哪副面孔」的转发，**一个文案字符串都不写**，
// 也不 import ProductShelf.tsx（那是 FrontShell 登录态懒加载件，公开面快车道不许把它拉进包）。
//
// 现状缺口：ProductPublicPage.tsx:76 的 busyCopy 把 audience 写死成 'visitor'，
// 卖家面那一支永远走不到。挂点（把那一行改调 publicExhaustText）不在本文件，
// 见 /tmp/w2_wiring.md。判据 X1–X5 由既有的 ProductShelf.exhausted.dom.test.tsx 承担，
// 本文件只补「/p/ 页面接进同一张 copyFor 表」这一环，不新写第二份文案。
// ============================================================================
import { copyFor, copyText, type CopyAudience, type ExhaustReason } from '@/lib/productExhaustCopy'

/** /p/ 页面耗尽态入参：reason=耗尽原因码；audience=访客/卖家；resetsAt=卖家面恢复时刻（可选） */
export interface PublicExhaustArgs {
  reason: ExhaustReason
  audience: CopyAudience
  resetsAt?: string
}

/**
 * publicExhaustText —— /p/ 页面耗尽态唯一出栈口：按 audience 取两副面孔之一，
 * 文案一律经 copyText → copyFor（唯一映射表），本函数不产生任何文案字面量。
 * 未耗尽（available/disabled）返回空串，由调用方决定是否渲染耗尽态容器。
 * 参数：args=耗尽态入参；返回客户可见文案。
 */
export function publicExhaustText(args: PublicExhaustArgs): string {
  if (args.reason !== 'exhausted_today') return ''
  return copyText(args.reason, args.audience, args.resetsAt)
}

/**
 * publicExhaustCopy —— 与 publicExhaustText 同源的决策对象（供需要 busy 位/插值变量的调用方）。
 * 参数：args=耗尽态入参；返回 copyFor 的决策结果（不新写文案）。
 */
export function publicExhaustCopy(args: PublicExhaustArgs) {
  return copyFor(args.reason, args.audience, args.resetsAt)
}
