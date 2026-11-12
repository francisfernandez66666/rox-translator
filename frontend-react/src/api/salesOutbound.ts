// ============================================================================
// api/salesOutbound.ts — 三期 W5 外联合规接口层（维护页开关 + 频限读数 + 名单登记）
//
// 后端契约（backend-go/internal/api/sales_outbound_api.go 实读）：
//   GET  /api/admin/sales/outbound/config  开关与频限读数（键过 outboundConfigKeys 白名单）
//   POST /api/admin/sales/outbound/config  写开关/频限（≥3）
//   GET  /api/admin/sales/outbound/sends   发送记录分页（排障面）
//   POST /api/admin/sales/outbound/consent 登记订阅状态
//   POST /api/admin/sales/outbound/suppression 加入抑制名单
//
// ★ 频限键 freq_cap_per_day 是**数值型**（1-1000），与维护页布尔开关（0/1）分开走本面，
//   两把白名单不混（后端 sales_role_api.go 与 sales_outbound_api.go 各管各的键集）。
// 本文件零界面中文（文案一律 t() 键）。
// ============================================================================
import { bizResp, request, authHeaders, type AdminResp } from './core'

/** 外联配置读响应：config 为键 → 后端真实值（未配置键为空串） */
export interface OutboundConfigResp extends AdminResp {
  config?: Record<string, string>
}

/** 外联配置键全集（与后端 outboundConfigKeys 同源；前端只读这几把） */
export const OUTBOUND_CONFIG_KEYS = [
  'outbound_enabled', 'freq_cap_per_day', 'outbound_batch_size',
] as const

/** 读外联配置（租户管理员及以上） */
export async function outboundConfigGet(): Promise<OutboundConfigResp> {
  return bizResp(() => request('/api/admin/sales/outbound/config', { headers: authHeaders() }))
}

/** 写外联配置（布尔键只许 0/1，数值键 1-1000；后端再验一遍） */
export async function outboundConfigSave(patch: Record<string, string>): Promise<AdminResp> {
  return bizResp(() => request('/api/admin/sales/outbound/config', {
    method: 'POST',
    headers: { ...authHeaders(), 'Content-Type': 'application/json' },
    body: JSON.stringify(patch),
  }))
}

/** 发送记录行 */
export interface OutboundSendRow {
  id: number
  customer_email: string
  node_key: string
  status: string
  reason: string
  claimed_at: string
  sent_at: string
}

/** 发送记录分页（最新在前） */
export interface OutboundSendsResp extends AdminResp {
  rows?: OutboundSendRow[]
}

export async function outboundSendsList(limit = 100): Promise<OutboundSendsResp> {
  return bizResp(() => request(`/api/admin/sales/outbound/sends?limit=${limit}`, { headers: authHeaders() }))
}

/** 登记订阅状态（opted_in / opted_out） */
export async function outboundConsentSet(email: string, status: string): Promise<AdminResp> {
  return bizResp(() => request('/api/admin/sales/outbound/consent', {
    method: 'POST',
    headers: { ...authHeaders(), 'Content-Type': 'application/json' },
    body: JSON.stringify({ email, status, source: 'admin_panel' }),
  }))
}

/** 加入抑制名单（kind: email | domain） */
export async function outboundSuppressionAdd(value: string, kind: string): Promise<AdminResp> {
  return bizResp(() => request('/api/admin/sales/outbound/suppression', {
    method: 'POST',
    headers: { ...authHeaders(), 'Content-Type': 'application/json' },
    body: JSON.stringify({ value, kind, reason: 'admin_panel' }),
  }))
}
