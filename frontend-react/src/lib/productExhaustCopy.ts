// ============================================================================
// lib/productExhaustCopy.ts — 额度耗尽态「两副面孔」文案的**唯一映射表**（施工_一期 §8.5）
//
// 背景（裁定 4）：「销售忙，如果不愿等待，建议留言」只在 `reason=exhausted_today`
// 这一支出栈。若卖家面也用「销售忙」，客户会把我方额度耗尽读成「需求不出现」，
// 进而做出「这产品没人要」的错误判断——那正是本仓「读数要为事实背书」那一族的反面。
//
// 三条硬口径：
//  ① 访客面（外国买家）只说商业语言，不出现技术原因（额度／token／系统故障）；
//  ② 卖家面（付费客户）只说事实语言：真实原因 ＋ `resets_at`，不许拟人化托辞；
//  ③ 映射**只此一张表**：任何组件不得自造第三处文案（§8.5 X3）。
//
// 「销售忙」文案本体住在 dicts.zh/en.ts（门面档，未登录首屏也取得到），
// 由 `noManualPromise.lock.test.ts` 的 CUSTOMER_KEYS 横扫禁词表；本文件只出**键**，
// 不复制文案字符串。断言 X1–X5 见 `src/ProductShelf.exhausted.dom.test.tsx`。
// ============================================================================
import { tplOr } from '@/i18n'

/** 耗尽态原因码（与 instant_free.go 现有序列同源：available／exhausted_today／disabled） */
export type ExhaustReason = 'available' | 'exhausted_today' | 'disabled'

/** 文案受众：visitor＝访客面（外国买家），owner＝卖家面（付费客户） */
export type CopyAudience = 'visitor' | 'owner'

/** 一次文案决策的结果：词典键 ＋ 是否属于「销售忙」那一支 ＋ 插值变量 */
export interface ExhaustCopy {
  key: string
  busy: boolean
  vars: Record<string, string>
}

// 五把键（全部登记在 dicts.zh/en.ts ＋ 十份 locales；`key` 只在这里出现一次）
const BUSY_KEY = 'product.exhaust.visitor.busy'
const OWNER_KEY = 'product.exhaust.owner.reason'
const UNAVAILABLE_KEY = 'product.exhaust.unavailable'
const READY_KEY = 'product.exhaust.available'
const DISABLED_KEY = 'product.exhaust.disabled'

/** 「销售忙」那一支的键（供 X4 反证与不渲染门使用；组件不直接比较文案字符串） */
export const BUSY_COPY_KEY = BUSY_KEY

/**
 * copyFor —— 两副面孔映射的**唯一出口**。
 * - `available` ⇒ 中性「服务可用」，**绝不**吐「销售忙」（X4 反证钉住这一支）；
 * - `exhausted_today` / visitor ⇒ 「销售忙…建议留言」；
 * - `exhausted_today` / owner ⇒ 真实原因 ＋ `resets_at`；
 * - `exhausted_today` / owner 且 `resetsAt` 缺失 ⇒ 落「暂不可用」那一档，
 *   **不许**退化成「销售忙」（X5：读数不许为失败背书）。
 */
export function copyFor(reason: ExhaustReason, audience: CopyAudience, resetsAt?: string): ExhaustCopy {
  if (reason === 'exhausted_today') {
    if (audience === 'visitor') return { key: BUSY_KEY, busy: true, vars: {} }
    if (resetsAt && resetsAt.trim()) return { key: OWNER_KEY, busy: false, vars: { resets_at: resetsAt } }
    return { key: UNAVAILABLE_KEY, busy: false, vars: {} }
  }
  if (reason === 'disabled') return { key: DISABLED_KEY, busy: false, vars: {} }
  return { key: READY_KEY, busy: false, vars: {} }
}

/** 把 copyFor 的决策解析成客户可见文案（组件唯一应调用的出栈口；先取词再插值） */
export function copyText(reason: ExhaustReason, audience: CopyAudience, resetsAt?: string): string {
  const c = copyFor(reason, audience, resetsAt)
  return tplOr(c.key, c.vars, '')
}
