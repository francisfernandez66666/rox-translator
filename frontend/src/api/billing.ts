// ============================================================================
// api/billing.ts — 计费/充值/配额/发票域接口
// 职责：余额、用量、充值订单、计费开关、租户配额、发票
// ============================================================================

import { request, authHeaders, type AdminResp } from './core'

// 租户余额查询
export async function billingBalance(): Promise<AdminResp> {
  return request('/api/billing/balance', { headers: authHeaders() })
}

// 租户用量查询
export async function billingUsage(): Promise<AdminResp> {
  return request('/api/billing/usage', { headers: authHeaders() })
}

// 充值订单列表
export async function billingOrders(): Promise<AdminResp> {
  return request('/api/billing/orders', { headers: authHeaders() })
}

// ==================== 计费配置（super_admin） ====================

// 读取计费配置（是否强制计费）
export async function billingConfig(): Promise<AdminResp> {
  return request('/api/billing/config', { headers: authHeaders() })
}

// 保存计费配置
export async function billingConfigSave(data: { billing_enforced: boolean }): Promise<AdminResp> {
  return request('/api/billing/config/save', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) })
}

// ==================== 租户配额（QPS/并发/每日上限） ====================

// 读取当前租户配额
export async function billingQuota(): Promise<AdminResp> {
  return request('/api/billing/quota', { headers: authHeaders() })
}

// 保存当前租户配额
export async function billingQuotaSave(data: { qps: number; concurrent: number; max_daily_chars: number }): Promise<AdminResp> {
  return request('/api/billing/quota/save', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) })
}

// ==================== 发票 ====================

// 发票列表
export async function billingInvoices(): Promise<AdminResp> {
  return request('/api/billing/invoices', { headers: authHeaders() })
}

// 创建发票（关联已支付订单）
export async function billingInvoiceCreate(data: { order_id: number; title: string; tax_no: string }): Promise<AdminResp> {
  return request('/api/billing/invoices/create', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) })
}