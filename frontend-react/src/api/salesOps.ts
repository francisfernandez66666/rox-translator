// ============ salesOps.ts · 职责说明 ============
// 三期 W3 维护页（SalesOpsP）的接口层 + **开关三态真值**（A12 的被测对象）。
//
// ★ 半套语义：一项开关由「主闸 + 可能的配对闸」共同决定。只开一把 ⇒ half（显示 halfHint，
//   绝不呈现为已生效）；两把都开 ⇒ on；都没开 ⇒ off。W4/W5 才落的新键后端读不到（空串）
//   ⇒ 视为未接线，一律 half 且禁用；批次未到（W3 施工期）即使两把都开也不许冒充 on。
// ★ 两批靶显式登记（A12）：W5 落库后 4 项允许 on，W6 建 handoff_sessions 后 3 项才改写，
//   当前 CURRENT_OPS_BATCH='W3' 不许一次性放全绿。
// 所有配置键与 backend-go/internal/api/sales_role_api.go 的 salesOpsConfigKeys 同源。
// 本文件不含任何界面中文字符串（文案一律走 t() 键）。
// ================================================
import { bizResp, request, authHeaders, type AdminResp } from './core'

/** 后端真实配置键全集（★ W5 收口校准：与后端 salesOpsConfigKeys 逐字同源——布尔 5 键；
 *  freq_cap_per_day 数值型走 salesOutbound.ts 的专用面，不进本表） */
export const OPS_CONFIG_KEYS = [
  'sales_engine_enabled', 'billing_enforced',
  'sales_facts_injection_enabled', 'sales_claim_guard_enabled',
  'outbound_enabled',
] as const

/** 允许翻 on 的批次档（A12 分批改写） */
export type OpsBatch = 'W3' | 'W5' | 'W6'

/** 当前施工批次：W5/W6 已全部落库（W2 巡检槽、W4 守卫、W5 外联、W6 CRM 均已接线） */
export const CURRENT_OPS_BATCH: OpsBatch = 'W6'

/** 开关定义：每项独立绑定一个后端真实读口；pairKey 为需同批开启的第二把闸（A15 半套判据） */
export interface OpsSwitchDef {
  key: string
  pairKey?: string
  labelKey: string
  hintKey: string
  groupKey: string
  batch: OpsBatch
}

/** 维护页开关全集（★ 键名与后端真实功能键逐字一致；双向比对见 A12 与后端白名单） */
export const OPS_SWITCH_DEFS: OpsSwitchDef[] = [
  { key: 'sales_engine_enabled', pairKey: 'billing_enforced', labelKey: 'sales.ops.swSweep', hintKey: 'sales.ops.swSweepHint', groupKey: 'sales.ops.groupRuntime', batch: 'W5' },
  { key: 'sales_facts_injection_enabled', pairKey: 'sales_claim_guard_enabled', labelKey: 'sales.ops.swFactsInjection', hintKey: 'sales.ops.swFactsInjectionHint', groupKey: 'sales.ops.groupFacts', batch: 'W6' },
  { key: 'sales_claim_guard_enabled', pairKey: 'sales_facts_injection_enabled', labelKey: 'sales.ops.swClaimGuard', hintKey: 'sales.ops.swClaimGuardHint', groupKey: 'sales.ops.groupStrategy', batch: 'W6' },
  { key: 'outbound_enabled', labelKey: 'sales.ops.swOutbound', hintKey: 'sales.ops.swOutboundHint', groupKey: 'sales.ops.groupCompliance', batch: 'W5' },
]

/** 各批次已到（W0–W6 全部落地）：两项都允许呈现 on */
export const W5_ON_KEYS = ['sales_engine_enabled', 'billing_enforced', 'outbound_enabled']
export const W6_ON_KEYS = ['sales_facts_injection_enabled', 'sales_claim_guard_enabled']

/** 开关三态 */
export type OpsState = 'on' | 'off' | 'half'

/** 批次门控：只有到了该批，这项才允许呈现 on（W3 期间 W5/W6 项一律 false） */
export function opsAllowOn(batch: OpsBatch, current: OpsBatch): boolean {
  if (batch === 'W3') return true
  return batch === current
}

/**
 * 开关三态推导（★ A12 唯一真值，两枚反证的靶）：
 *  - 未接线（W4/W5 才落的新键读不到）⇒ half / off，绝不 on；
 *  - 批次未到 ⇒ half / off，不冒充已生效；
 *  - 只开一把闸 ⇒ half（不得 on）；两把都开 ⇒ on；都没开 ⇒ off。
 * pair 为 null 表示单闸开关（自己与自己配对）。
 */
export function opsSwitchState(primary: boolean, pair: boolean | null, wired: boolean, allowOn: boolean): OpsState {
  const pairOn = pair === null ? primary : pair
  if (!wired) return primary || pairOn ? 'half' : 'off'
  if (!allowOn) return primary || pairOn ? 'half' : 'off'
  if (primary && pairOn) return 'on'
  if (primary || pairOn) return 'half'
  return 'off'
}

/** 维护页开关读响应：values 为配置键 → 后端真实值（新键取不到为空串） */
export interface SalesOpsResp extends AdminResp {
  keys?: string[]
  values?: Record<string, string>
}

/** 能力面三态读数（两把既有闸的真实值，供总开关交叉核对） */
export interface SalesCapabilityResp extends AdminResp {
  status?: string
  engine_enabled?: boolean
  billing_enforced?: boolean
}

/** 读维护页全部开关的后端真实配置值（租户管理员及以上） */
export async function salesOpsGet(): Promise<SalesOpsResp> {
  return bizResp(() => request('/api/admin/sales/ops', { headers: authHeaders() }))
}

/** 保存单个开关（只认白名单键与 0/1 值，后端留审计） */
export async function salesOpsSave(key: string, value: string): Promise<AdminResp> {
  return bizResp(() => request('/api/admin/sales/ops/save', {
    method: 'POST',
    headers: { ...authHeaders(), 'Content-Type': 'application/json' },
    body: JSON.stringify({ key, value }),
  }))
}

/** 读既有能力面三态（两把闸真实值，半套判定的交叉核对源） */
export async function salesCapabilityGet(): Promise<SalesCapabilityResp> {
  return bizResp(() => request('/api/sales/capability', { headers: authHeaders() }))
}
