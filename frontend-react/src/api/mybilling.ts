// ============================================================================
// api/mybilling.ts — 自服务账单端点客户端（★ F8；2026-09-19 全积分口径）
// 对应用户侧账单中心：概览（趋势）、订单、台账、奖励、发票。
// 后端出参即积分值（token 仅内部记账，不外发、不下发汇率），前端零换算。
//
// ★ F-64②／批 #42 读写分档（★ D-3 批 2026-09-29 复核修正）：本文件**全部是纯读取接口**，
//   一律**保持抛出、不包 bizResp**——bizResp 会把后端结构化 4xx 还原成 {success:false} 壳，
//   读取失败于是被伪装成「空数据」（用户看到「本月零消耗」而不是「取数失败」，批 #42 的静默形态）。
//   本文件 5 个接口的调用点（components/MyBilling.tsx）全部走 runGuarded 的**异常通道**
//   兜后端原文，所以抛出才是正确的失败语义；常设锁见 backend-go/internal/api/payhonesty_gate_test.go
//   的 mustNotBizResp 表（点名到函数级），静态锁见 api/bizRespGate.test.ts 的 READ_THROW_EXEMPT 表。
//   ⚠️ 新增接口若返回 {success,...} 信封且调用点靠 success 分支处理，才按 AGENTS §一·5 走收敛；
//   纯读取接口别走，走了闸门判红。
// ============================================================================
import { request } from './core'

/** OverviewResp 我的账户总览（双桶余额/≈句数/强计费态/日用量曲线，均积分） */
export interface OverviewResp {
  success: boolean
  points_grants: number
  points_available: number
  approx_sentences: number
  billing_enforced: boolean
  daily: { date: string; cost_points: number; count: number }[]
}
/** PageResp 服务端分页通用出参 */
export interface PageResp { success: boolean; total: number; page: number; size: number }
/** MyOrder 我的订单（积分包/句包/人工核销态；amount_points=充值积分数） */
export interface MyOrder {
  id: number; order_no: string; amount_points: number; amount_money: number
  status: string; channel: string; pay_method: string; manual_confirm: number
  created_at: string; paid_at: string
}
/** MyLedgerRow 计费流水行（供应商/模型/数量/费用积分/业务口径；内部单价不外发） */
export interface MyLedgerRow {
  id: number; task_type: string; provider: string; model: string
  quantity: number; cost_points: number
  biz_kind: string; biz_mode: string; created_at: string
}
/** MyReward 邀请奖励记录（被邀人/类型/积分或天数/到账态） */
export interface MyReward {
  invitee_uid: number; invitee_name: string; invitee_email: string
  type: string; reward_points: number; days: number; paid: boolean; created_at: string
}
/** MyInvoice 我的发票（人工开票轨） */
export interface MyInvoice {
  id: number; order_id: number; invoice_no: string; amount_money: number
  title: string; tax_no: string; status: string; created_at: string
}

// 分页查询串拼接
const page = (p: number, size = 10) => `page=${p}&size=${size}`

// 个人计费概览（余额/本月消费/待支付订单）
// ★ 读取保持抛出：调用点 runGuarded 靠异常兜后端文案，不包 bizResp
export async function myOverview(): Promise<OverviewResp> {
  return request<OverviewResp>('/api/billing/my/overview')
}
// 我的订单列表（分页，可按状态过滤）
// ★ 读取保持抛出：调用点 runGuarded 靠异常兜后端文案，不包 bizResp
export async function myOrders(p: number, status = ''): Promise<PageResp & { orders: MyOrder[] | null }> {
  return request(`/api/billing/my/orders?${page(p)}${status ? `&status=${encodeURIComponent(status)}` : ''}`)
}
// 我的消费流水（分页，可按业务类型过滤）
// ★ 读取保持抛出：调用点 runGuarded 靠异常兜后端文案，不包 bizResp
export async function myLedger(p: number, bizKind = ''): Promise<PageResp & { rows: MyLedgerRow[] | null }> {
  return request(`/api/billing/my/ledger?${page(p)}${bizKind ? `&biz_kind=${encodeURIComponent(bizKind)}` : ''}`)
}
// 我的奖励记录（邀请返佣等）
// ★ 读取保持抛出：调用点 runGuarded 靠异常兜后端文案，不包 bizResp
export async function myRewards(p: number): Promise<PageResp & { rewards: MyReward[] | null }> {
  return request(`/api/billing/my/rewards?${page(p)}`)
}
// 我的发票申请记录
// ★ 读取保持抛出：调用点 runGuarded 靠异常兜后端文案，不包 bizResp
export async function myInvoices(p: number): Promise<PageResp & { invoices: MyInvoice[] | null }> {
  return request(`/api/billing/my/invoices?${page(p)}`)
}
