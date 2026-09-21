// ============================================================================
// api/billing.ts — 计费/充值/配额/发票域接口
// 职责：余额、用量、充值订单、计费开关、租户配额、发票
// ============================================================================

/**
 * api/billing.ts · 职责说明
 * 封装计费域的所有接口，包括：
 * - 余额与用量查询：当前租户余额、个人用量、组织用量、模型成本核算
 * - 充值订单：订单列表、在线支付、模拟支付、人工确认
 * - 计费配置：强制计费开关、全局设置
 * - 租户配额：QPS、并发数、每日上限
 * - 发票管理：发票列表、创建发票
 * - 商业包管理：套餐列表、订阅、创建、更新、删除
 */

import { request, authHeaders, API_BASE, handleUnauthorized, type AdminResp } from './core'

/** 查询当前租户余额 */

/** 查询当前租户用量 */

/** 个人用量看板（普通用户个人级）：from/to=YYYY-MM-DD 日期区间（均空=累计+当日口径） */
/** 个人用量曲线（时间窗可选） */
export async function usageMe(from?: string, to?: string): Promise<AdminResp> {
  const params = new URLSearchParams()
  if (from) params.set('from', from)
  if (to) params.set('to', to)
  const q = params.toString() ? `?${params.toString()}` : ''
  return request(`/api/billing/usage/me${q}`, { headers: authHeaders() })
}

/** 组织用量看板（租户管理员：组织→子组织→用户下钻）：from/to=YYYY-MM-DD 日期区间 */
/** 组织维度用量（管理员） */
export async function usageOrg(orgId?: number, from?: string, to?: string): Promise<AdminResp> {
  const params = new URLSearchParams()
  if (orgId) params.set('org_id', String(orgId))
  if (from) params.set('from', from)
  if (to) params.set('to', to)
  const q = params.toString() ? `?${params.toString()}` : ''
  return request(`/api/billing/usage/org${q}`, { headers: authHeaders() })
}

/** 全平台模型成本核算（超级管理员） */
/** 成本视图：订单→模型单价折算 */
export async function usageCost(): Promise<AdminResp> {
  return request('/api/billing/usage/cost', { headers: authHeaders() })
}

/** 获取充值订单列表 */
/** 我的订单列表（含支付/退款状态） */
export async function billingOrders(): Promise<AdminResp> {
  return request('/api/billing/orders', { headers: authHeaders() })
}

// ==================== 计费配置（super_admin） ====================

/** 读取计费配置（是否强制计费） */

/** 保存计费配置（是否强制计费） */
/** 保存租户计量/强制计费开关（管理员） */
export async function billingConfigSave(data: { billing_enforced: boolean }): Promise<AdminResp> {
  return request('/api/billing/config/save', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) })
}

// ==================== 租户配额（QPS/并发/每日上限） ====================

/** 读取当前租户配额（QPS/并发/每日上限） */
export async function billingQuota(): Promise<AdminResp> {
  return request('/api/billing/quota', { headers: authHeaders() })
}

/** 保存当前租户配额（QPS/并发/每日字符与积分上限） */
/** 保存租户配额（qps/并发，管理员） */
export async function billingQuotaSave(data: { qps: number; concurrent: number; max_daily_chars: number; max_daily_points?: number }): Promise<AdminResp> {
  return request('/api/billing/quota/save', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) })
}

// ==================== 发票 ====================

/** 获取发票列表 */
export async function billingInvoices(): Promise<AdminResp> {
  return request('/api/billing/invoices', { headers: authHeaders() })
}

/** 创建发票（关联已支付订单） */
/** 提交开票申请（订单维度） */
export async function billingInvoiceCreate(data: { order_id: number; title: string; tax_no: string }): Promise<AdminResp> {
  return request('/api/billing/invoices/create', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) })
}

/** 发票冲红/作废（C16：租户管理员及以上，作废后同单可重开） */
export async function billingInvoiceVoid(id: number): Promise<AdminResp> {
  return request('/api/billing/invoices/void', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id }) })
}

/** 订单退款（★ 2026-09-16 补口：后端 /api/admin/orders/refund 一直健在，前端此前零封装）
 *  仅超管可调；tenant_id 省略时后端按当前生效租户（X-Tenant-ID）核销。 */
export async function adminOrderRefund(data: { id: number; tenant_id?: number; reason?: string }): Promise<AdminResp> {
  return request('/api/admin/orders/refund', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) })
}

// ==================== 在线支付 ====================

/** 发起在线支付下单：为当前租户创建充值订单并返回收款二维码（points=充值积分数；usdt 渠道可带 usdt_chain；coupon=券码，选填） */
export async function payCreate(data: { points: number; channel: string; usdt_chain?: string; coupon?: string }): Promise<AdminResp> {
  return request('/api/pay/create', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) })
}

/** 查询订单支付状态（收银台轮询） */
/** 轮询订单支付状态 */
export async function payStatus(orderId: number): Promise<AdminResp> {
  return request(`/api/pay/status?order_id=${orderId}`, { headers: authHeaders() })
}

/** 模拟支付到账（仅 mock 模式测试用） */
/** 模拟支付（pay_mode=mock 联调用） */
export async function paySimulate(orderId: number): Promise<AdminResp> {
  return request('/api/pay/simulate', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ order_id: orderId }) })
}

/** 静态码支付「我已付费」（人工确认，通知超管审核开通） */
/** 用户声明「我已付款」→ 生成人工核对单（usdt 渠道携带链上交易哈希线索） */
export async function payManualConfirm(orderId: number, txHash = ''): Promise<AdminResp> {
  return request('/api/pay/manual-confirm', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ order_id: orderId, tx_hash: txHash }) })
}

/** 待人工确认订单列表（超管审核开通） */
/** 管理员：待人工核对的到账单列表 */
export async function manualConfirmOrders(): Promise<AdminResp> {
  return request('/api/admin/orders/manual', { headers: authHeaders() })
}

// ==================== 商业包（套餐） ====================

/** 公开定价页：列出上架中的商业包（无需登录） */
export async function plans(): Promise<AdminResp> {
  return request('/api/plans')
}

/** 我的包信息：当前包 + 剩余句数（登录用户） */
/** 我的订阅套餐与有效期 */
export async function myPackage(): Promise<AdminResp> {
  return request('/api/me/package', { headers: authHeaders() })
}

/** 订阅/兑换商业包（创建待支付订单或直接发放免费包）；coupon=券码（选填，#41 与充值单同口径：折钱不折量） */
export async function packageSubscribe(code: string, coupon = ''): Promise<AdminResp> {
  return request('/api/package/subscribe', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ code, coupon }) })
}

/** 套餐升级（付费包→更高价付费包）：旧包剩余价值按比例抵扣新包应付，新包即时生效 */
/** 升级套餐（按剩余天数折算补差） */
export async function packageUpgrade(code: string): Promise<AdminResp> {
  return request('/api/package/upgrade', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ code }) })
}

/** 读取自动续费开关（#41）：返回 auto_renew + 当前包编码 */
export async function autoRenewGet(): Promise<AdminResp> {
  return request('/api/package/auto-renew', { headers: authHeaders() })
}

/** 设置自动续费（#41）：开启后到期前 3 天自动生成同包续费订单并站内信提醒付款（不代扣） */
export async function autoRenewSet(enabled: boolean): Promise<AdminResp> {
  return request('/api/package/auto-renew', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ enabled }) })
}

// ==================== 商业包管理（super_admin） ====================

/** 列出全部商业包（含下架） */
export async function adminPackages(): Promise<AdminResp> {
  return request('/api/admin/packages', { headers: authHeaders() })
}

/** 创建商业包（包码/名称/类型/句数/价格/有效期等） */
/** 管理员：新建套餐（九档 v4 价目） */
export async function adminPackageCreate(data: {
  code: string; name: string; ptype: string; sentences: number; price_money?: number; duration_days?: number; sort_order?: number
}): Promise<AdminResp> {
  return request('/api/admin/packages/create', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) })
}

/** 更新商业包（改名/调价/改句数/启停） */
/** 管理员：修改套餐（上下架/价格） */
export async function adminPackageUpdate(data: {
  id: number; name?: string; ptype?: string; sentences?: number; price_money?: number; duration_days?: number; enabled?: number; sort_order?: number
}): Promise<AdminResp> {
  return request('/api/admin/packages/update', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) })
}

/** 删除商业包 */
/** 管理员：删除套餐 */
export async function adminPackageDelete(id: number): Promise<AdminResp> {
  return request('/api/admin/packages/delete', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id }) })
}

/** 读取商业包全局设置（句数强制开关/试用句数/支付模式/静态码等） */
/** 管理员：读取计费设置（体验积分/强制计费/合规闸/黑名单等，出参一律积分口径） */
export async function adminPackageSettings(): Promise<AdminResp> {
  return request('/api/admin/packages/settings', { headers: authHeaders() })
}

/** 上传套餐中心静态收款码图片（super_admin；multipart 字段 file） */
/** 管理员：上传收款码图片（★ P1-16：走统一守卫，401 跳登录/非 2xx 抛错） */
export async function adminQRUpload(file: File): Promise<AdminResp & { qr_url?: string }> {
  const formData = new FormData()
  formData.append('file', file)
  const resp = await fetch(`${API_BASE}/api/admin/packages/qr-upload`, {
    method: 'POST',
    headers: authHeaders(),
    body: formData,
  })
  if (resp.status === 401) handleUnauthorized('/api/admin/packages/qr-upload')
  if (!resp.ok) return { success: false, message: `上传失败 (${resp.status})` }
  return resp.json()
}

/** 保存商业包全局设置（仅传的字段更新；对齐后端 handleAdminPackageSettingsSave） */
/** 管理员：保存计费设置（强制计费/体验积分/敏感词闸/一次性邮箱域，S1/S3/S8） */
export async function adminPackageSettingsSave(data: {
  billing_enforced?: string
  // 口径区分（★ 2026-09-19 积分口径）：体验额度用 free_trial_points（积分），公开面不再传 token 裸值；
  // billing_markup_multiplier 是无量纲系数，保留原名。
  free_trial_points?: number // 新租户体验积分数（后端按内部汇率折 token 记账）
  free_trial_days?: number
  billing_markup_multiplier?: number
  pay_mode?: string
  static_qr_image?: string
  email_verify_enabled?: string
  email_notify_enabled?: string
  captcha_provider?: string
  captcha_site_key?: string
  captcha_secret_key?: string
  wecom_webhook_url?: string
  dingtalk_webhook_url?: string
}): Promise<AdminResp> {
  return request('/api/admin/packages/settings/save', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) })
}