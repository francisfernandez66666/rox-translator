// ============================================================================
// api/mybilling.ts — 自服务账单端点客户端（★ F8）
// 对应用户侧账单中心：概览（趋势）、订单、台账、奖励、发票。
// ============================================================================
import { request } from './core'

export interface OverviewResp {
  success: boolean
  balance: number
  grants_left: number
  total_available: number
  approx_sentences: number
  billing_enforced: boolean
  daily: { date: string; cost: number; count: number }[]
}
export interface PageResp { success: boolean; total: number; page: number; size: number }
export interface MyOrder {
  id: number; order_no: string; amount_tokens: number; amount_money: number
  status: string; channel: string; pay_method: string; manual_confirm: number
  created_at: string; paid_at: string
}
export interface MyLedgerRow {
  id: number; task_type: string; provider: string; model: string
  quantity: number; unit_price: number; cost: number
  biz_kind: string; biz_mode: string; created_at: string
}
export interface MyReward {
  invitee_uid: number; invitee_name: string; invitee_email: string
  type: string; tokens: number; days: number; paid: boolean; created_at: string
}
export interface MyInvoice {
  id: number; order_id: number; invoice_no: string; amount_money: number
  title: string; tax_no: string; status: string; created_at: string
}

// 分页查询串拼接
const page = (p: number, size = 10) => `page=${p}&size=${size}`

// 个人计费概览（余额/本月消费/待支付订单）
export async function myOverview(): Promise<OverviewResp> {
  return request<OverviewResp>('/api/billing/my/overview')
}
// 我的订单列表（分页，可按状态过滤）
export async function myOrders(p: number, status = ''): Promise<PageResp & { orders: MyOrder[] | null }> {
  return request(`/api/billing/my/orders?${page(p)}${status ? `&status=${encodeURIComponent(status)}` : ''}`)
}
// 我的消费流水（分页，可按业务类型过滤）
export async function myLedger(p: number, bizKind = ''): Promise<PageResp & { rows: MyLedgerRow[] | null }> {
  return request(`/api/billing/my/ledger?${page(p)}${bizKind ? `&biz_kind=${encodeURIComponent(bizKind)}` : ''}`)
}
// 我的奖励记录（邀请返佣等）
export async function myRewards(p: number): Promise<PageResp & { rewards: MyReward[] | null }> {
  return request(`/api/billing/my/rewards?${page(p)}`)
}
// 我的发票申请记录
export async function myInvoices(p: number): Promise<PageResp & { invoices: MyInvoice[] | null }> {
  return request(`/api/billing/my/invoices?${page(p)}`)
}
