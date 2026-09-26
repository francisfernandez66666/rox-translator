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

// ★ F-64②（2026-09-26 批 I-10）：本文件所有接口统一经 core.ts 的 bizResp 接线——
//   HTTP 200 但业务体 success:false 会被如实降级为异常口径，调用方不再拿到「假成功」；
//   新增接口一律写 bizResp(() => request(...))，禁止直返裸 request。
import { request, bizResp, authHeaders, API_BASE, handleUnauthorized, apiMsg, type AdminResp } from './core'

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
  return bizResp(() => request('/api/billing/invoices/create', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) }))
}

/** 发票冲红/作废（C16：租户管理员及以上，作废后同单可重开） */
export async function billingInvoiceVoid(id: number): Promise<AdminResp> {
  return bizResp(() => request('/api/billing/invoices/void', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id }) }))
}

/** 订单退款（★ 2026-09-16 补口：后端 /api/admin/orders/refund 一直健在，前端此前零封装）
 *  仅超管可调；tenant_id 省略时后端按当前生效租户（X-Tenant-ID）核销。 */
export async function adminOrderRefund(data: { id: number; tenant_id?: number; reason?: string }): Promise<AdminResp> {
  return bizResp(() => request('/api/admin/orders/refund', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) }))
}

// ==================== 在线支付 ====================
//
// ★ F-64①（批 I-7，2026-09-26）状态码诚实改造的前端配套：
//   后端充值/订阅/券域过去用「HTTP 200 承载失败」，客户与 SDK 按状态码分支时全部判成成功，
//   现已改为 400/404/409/500/503（缺陷 F-47/F-64 的第①档）。这些接口在本层一律用 bizResp 包一层，
//   把后端结构化失败体还原成历史响应形态（含 details 里随错误附带的 order_no / order 等字段），
//   于是全站 `if (!r.success)` / `toastResp(r)` 的既有调用点零改动即恢复原语义。
//   为什么不在 request() 里全局收敛：401 要走全局清登录态、403 本批未翻状态码、
//   网络层失败更不能伪装成业务失败——三条都保持抛出，详见 core.ts 的 bizResp 注释。
//
// ★ 收敛范围口径（别一刀切全包）：**动作类（POST：下单/订阅/升级/核销/退款/开票/作废/券 CRUD）
//   走 bizResp**，因为它们的调用点要按 success 分支改界面态（弹收款台、清券码、刷列表）；
//   **列表与统计读取（GET：订单列表、发票列表、待核对单、用量三口径、自助账单五表）保持抛出**，
//   由调用点的 runGuarded / try-catch 把后端原文弹出来——那些消费点只有「成功就填表，失败就提示」
//   一条路，收敛成 success:false 反而会让只剩 if (r.success) 的调用点把失败咽成静默
//   （#42 批专门消灭过的形态）。例外：payStatus 虽属 GET，但轮询要按 404 判终态停轮，一并收敛。

/** 发起在线支付下单：为当前租户创建充值订单并返回收款二维码（points=充值积分数；usdt 渠道可带 usdt_chain；coupon=券码，选填） */
export async function payCreate(data: { points: number; channel: string; usdt_chain?: string; coupon?: string }): Promise<AdminResp> {
  return bizResp(() => request('/api/pay/create', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) }))
}

/** 查询订单支付状态（收银台轮询） */
/** 轮询订单支付状态 */
export async function payStatus(orderId: number): Promise<AdminResp> {
  return bizResp(() => request(`/api/pay/status?order_id=${orderId}`, { headers: authHeaders() }))
}

/** 模拟支付到账（仅 mock 模式测试用） */
/** 模拟支付（pay_mode=mock 联调用） */
export async function paySimulate(orderId: number): Promise<AdminResp> {
  return bizResp(() => request('/api/pay/simulate', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ order_id: orderId }) }))
}

/** 静态码支付「我已付费」（人工确认，通知超管审核开通） */
/** 用户声明「我已付款」→ 生成人工核对单（usdt 渠道携带链上交易哈希线索） */
export async function payManualConfirm(orderId: number, txHash = ''): Promise<AdminResp> {
  return bizResp(() => request('/api/pay/manual-confirm', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ order_id: orderId, tx_hash: txHash }) }))
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
  return bizResp(() => request('/api/package/subscribe', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ code, coupon }) }))
}

/** 套餐升级（付费包→更高价付费包）：旧包剩余价值按比例抵扣新包应付，新包即时生效 */
/** 升级套餐（按剩余天数折算补差） */
export async function packageUpgrade(code: string): Promise<AdminResp> {
  return bizResp(() => request('/api/package/upgrade', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ code }) }))
}

/** 读取自动续费开关（#41）：返回 auto_renew + 当前包编码 */
export async function autoRenewGet(): Promise<AdminResp> {
  return request('/api/package/auto-renew', { headers: authHeaders() })
}

/** 设置自动续费（#41）：开启后到期前 3 天自动生成同包续费订单并站内信提醒付款（不代扣） */
export async function autoRenewSet(enabled: boolean): Promise<AdminResp> {
  return bizResp(() => request('/api/package/auto-renew', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ enabled }) }))
}

// ==================== 商业包管理（super_admin） ====================

/** 列出全部商业包（含下架） */
export async function adminPackages(): Promise<AdminResp> {
  return bizResp(() => request('/api/admin/packages', { headers: authHeaders() }))
}

/** 创建商业包（包码/名称/类型/句数/价格/有效期等） */
/** 管理员：新建套餐（九档 v4 价目） */
export async function adminPackageCreate(data: {
  code: string; name: string; ptype: string; sentences: number; price_money?: number; duration_days?: number; sort_order?: number
}): Promise<AdminResp> {
  return bizResp(() => request('/api/admin/packages/create', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) }))
}

/** 更新商业包（改名/调价/改句数/启停） */
/** 管理员：修改套餐（上下架/价格） */
export async function adminPackageUpdate(data: {
  id: number; name?: string; ptype?: string; sentences?: number; price_money?: number; duration_days?: number; enabled?: number; sort_order?: number
}): Promise<AdminResp> {
  return bizResp(() => request('/api/admin/packages/update', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) }))
}

/** 删除商业包 */
/** 管理员：删除套餐 */
export async function adminPackageDelete(id: number): Promise<AdminResp> {
  return bizResp(() => request('/api/admin/packages/delete', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id }) }))
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
  if (!resp.ok) return { success: false, message: apiMsg('common.uploadFail', `上传失败 (${resp.status})`, { status: resp.status }) }
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

// ============================================================================
// ★ 2026-09-22 支付渠道凭据管理台可配（微信 Native v3 / 支付宝当面付）
// 字段键名与后端 internal/store/billing_payconfig.go 的白名单一一对应（paych_ 前缀）。
// 敏感项（APIv3 密钥、商户/应用私钥）回显恒为掩码 "********"：表单原样回提即可保留库内真值，
// 后端识别掩码后不会写库（见 SetPayConfigField）——所以前端不需要「留空即不改」的特殊约定。
// ============================================================================

/** 支付渠道配置字段名（与 system_config 键同名，集中声明防手写串键） */
export const PAY_CH_FIELDS = [
  'paych_notify_base',
  'paych_wechat_enabled', 'paych_wechat_app_id', 'paych_wechat_mch_id', 'paych_wechat_serial_no',
  'paych_wechat_apiv3_key', 'paych_wechat_private_key', 'paych_wechat_platform_cert', 'paych_wechat_notify_url',
  'paych_alipay_enabled', 'paych_alipay_app_id', 'paych_alipay_private_key', 'paych_alipay_public_key',
  'paych_alipay_seller_id', 'paych_alipay_gateway', 'paych_alipay_notify_url',
] as const
// PayChField：支付渠道凭据配置键的联合类型（#74），与 PAY_CH_FIELDS 逐键同源，防手写漂移
export type PayChField = (typeof PAY_CH_FIELDS)[number]

/** 管理台：读取支付渠道凭据（敏感项掩码回显；env_overridden=配置键→接管该字段的环境变量名） */
export async function adminPayChannels(): Promise<AdminResp & { fields?: Record<string, string>; env_overridden?: Record<string, string> }> {
  return request('/api/admin/pay/channels', { headers: authHeaders() })
}

/** 管理台：保存支付渠道凭据（值留空=清除该项；掩码值=保持不动） */
export async function adminPayChannelsSave(fields: Partial<Record<PayChField, string>>): Promise<AdminResp> {
  return request('/api/admin/pay/channels/save', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ fields }) })
}

// ============================================================================
// ★ 2026-09-23 多币种报价（#75）：报价币种/汇率倍率的超管配置口
// （/api/admin/config/quote-currency）。口径红线：只做「展示报价」——
// 微信/支付宝收单恒为人民币，外币价只是客户在定价页/收银台看到的本币口径，
// 下单时后端把币种+倍率快照进 orders.currency/fx_rate/money_cny 留审计，不参与资金判定。
// rates 为整体覆盖保存（与后端 SetFxRates 一致：空倍率=该币种不报价）。
// ============================================================================

/** 管理台：回显生效报价配置（币种、倍率表、白名单币种、env 接管标注） */
export async function adminQuoteCurrency(): Promise<AdminResp & {
  currency?: string
  rates?: Record<string, number>
  supported_currencies?: string[]
  env_overridden?: Record<string, string>
}> {
  return request('/api/admin/config/quote-currency', { headers: authHeaders() })
}

/** 管理台：保存报价币种与汇率倍率（currency 空串=不表态；rates 不含 CNY——基准恒为 1） */
export async function adminQuoteCurrencySave(data: { currency: string; rates: Record<string, number> }): Promise<AdminResp & { currency?: string; rates?: Record<string, number> }> {
  return request('/api/admin/config/quote-currency', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) })
}