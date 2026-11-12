// ============================================================================
// api/salesQuote.ts — 三期 W2 报价窄管理面接口层
//
// 后端契约（backend-go/internal/api/sales_quote_api.go 实读）：
//   GET  /api/admin/sales/quote?quote_id=   读单条（不存在/跨租户 → 404 不泄露存在性）
//   POST /api/admin/sales/quote/create      建报价（draft 起步）
//   POST /api/admin/sales/quote/accept      转正（后端唯一转正写口 AcceptQuote）
//
// ★ 金额量纲只有「分」（total_cents，与 payments.amount_fen 同量纲）——
// 本层不做分↔元换算；展示侧换算一律 import lib/priceCalc.ts 的既有口径，
// 禁止在这里再抄一份（§一·5「报价只有一个事实源」）。
// 纪律：三个调用都返回 {success,...} 信封，一律 bizResp 包裹，不进任何豁免档。
// ============================================================================
import { bizResp, request, authHeaders, type AdminResp } from './core'

/** 报价行（出参键为后端 Quote 结构的 json tag，逐字对应） */
export interface SalesQuoteRow {
  id: string
  tenant_id: number
  deal_id: string
  /** 金额，单位「分」；换算/展示一律走 lib/priceCalc.ts */
  total_cents: number
  currency: string
  status: string
  created_at: string
  updated_at: string
}

export interface SalesQuoteResp extends AdminResp {
  quote?: SalesQuoteRow
}

/** 读单条报价（读 ≥2 部门管理员；404=不存在或跨租户） */
export async function salesQuoteGet(quoteID: string): Promise<SalesQuoteResp> {
  return bizResp(() => request(`/api/admin/sales/quote?quote_id=${encodeURIComponent(quoteID)}`, { headers: authHeaders() }))
}

/** 建一张报价（金额单位分，负数后端拒收；撞在途唯一索引 → 409） */
export async function salesQuoteCreate(dealID: string, totalCents: number, currency: string): Promise<SalesQuoteResp> {
  return bizResp(() => request('/api/admin/sales/quote/create', {
    method: 'POST',
    headers: { ...authHeaders(), 'Content-Type': 'application/json' },
    body: JSON.stringify({ deal_id: dealID, total_cents: totalCents, currency }),
  }))
}

/** 把在途报价流转为 accepted（转正动作；后端留审计） */
export async function salesQuoteAccept(quoteID: string): Promise<SalesQuoteResp> {
  return bizResp(() => request('/api/admin/sales/quote/accept', {
    method: 'POST',
    headers: { ...authHeaders(), 'Content-Type': 'application/json' },
    body: JSON.stringify({ quote_id: quoteID }),
  }))
}
