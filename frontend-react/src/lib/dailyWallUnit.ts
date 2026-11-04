// ============ src/lib/dailyWallUnit.ts · 职责说明 ============
// 日限额「生效口径」三档的**唯一映射**（★ 2026-11-03 复核批 D-1：日限额量纲修法）。
//
// 为什么前端需要这份映射而不是直接把后端的字符串印出来：
//   后端 `GET /api/billing/quota` 新增 `daily_wall_unit` 三档词（cost／legacy_chars_ignored／unlimited，
//   定义见 backend-go/internal/api/daily_budget.go），它是**运营给人看的码**，不是给客户看的话；
//   管理台有 12 语种，所以「码 → 词典键」必须收在一处，否则每个渲染面各写一份 `unit === 'cost' ? … : …`
//   ＝第二个事实源（改档名时只改到一处，界面静默留在旧口径上）。
//
// 三条硬口径（改这块前必读，锁在 src/lib/dailyWallUnit.lock.test.ts）：
//   ① **未知/缺失一律回空串**（＝不渲染这一行）。老后端不带这个键是常态，
//      把它猜成任何一档都是给运营一句"看起来像事实"的假读数——宁可不说。
//   ② 三档的**文案键**与后端三档字面量逐字对齐，跨语言等值由锁从 Go 源码现读比对（L3）。
//      后端改档名而这里没跟上 ⇒ 锁红，而不是界面上静默少一行。
//   ③ 这一档只看后端结论，**不许**在组件里按 `max_daily_chars` / `max_daily_points` 的数字形状
//      自己推断"到底拦不拦"——量纲判定（成本 vs 字符）只在后端 dailyBudgetFromPerms 一处成立，
//      前端重推一遍就是第二把尺子（AGENTS §一·11 那一族）。
// ==========================================

/** 后端 daily_wall_unit 的三个合法档位（与 daily_budget.go 的三个常量逐字相同） */
export const DAILY_WALL_UNITS = ['cost', 'legacy_chars_ignored', 'unlimited'] as const

// DailyWallUnit 日限额量纲三档（由那张表派生，不另写一份联合类型字面量——写死就和表漂移）。
export type DailyWallUnit = (typeof DAILY_WALL_UNITS)[number]

/** 档位 → 词典键（名单只有这一份；渲染面只许消费 dailyWallUnitLabelKey()） */
export const DAILY_WALL_UNIT_LABEL_KEYS: Record<DailyWallUnit, string> = {
  cost: 'billing.quotaUnitCost',
  legacy_chars_ignored: 'billing.quotaUnitLegacy',
  unlimited: 'billing.quotaUnitUnlimited',
}

/** 是否三档之一（用于把 unknown 收窄，不做任何猜测） */
export function isDailyWallUnit(v: unknown): v is DailyWallUnit {
  return typeof v === 'string' && (DAILY_WALL_UNITS as readonly string[]).includes(v)
}

/**
 * 取该档位的词典键；**未知/缺失回空串**（调用方据此整行不渲染）。
 * 刻意不设默认档：把 undefined 当 unlimited 会让"老后端"看起来像"已确认不限"。
 */
export function dailyWallUnitLabelKey(v: unknown): string {
  return isDailyWallUnit(v) ? DAILY_WALL_UNIT_LABEL_KEYS[v] : ''
}
