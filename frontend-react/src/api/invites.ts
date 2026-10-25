// ============================================================================
// api/invites.ts — 邀请域接口
// 职责：注册邀请码的查询与生成 + 企业专属邀请链接的创建/列表/公开校验/撤销/二维码取图
// ============================================================================

/**
 * api/invites.ts · 职责说明
 * 封装邀请凭据的管理接口，包括：
 * - 邀请码列表：获取当前租户的邀请码列表
 * - 邀请码生成：创建新的邀请码，可绑定租户或留空新建租户
 * - ★ 企业专属邀请链接（多身份方案 B3/B4）：创建、列表、匿名公开校验、撤销、按 id 出二维码
 */

// ★ F-64②（2026-09-26 批 I-10）：本文件所有接口统一经 core.ts 的 bizResp 接线——
//   HTTP 200 但业务体 success:false 会被如实降级为异常口径，调用方不再拿到「假成功」；
//   新增接口一律写 bizResp(() => request(...))，禁止直返裸 request。
//   ⚠️ 唯一例外是文件末尾那道二维码取图（fetch → Blob）：它不是 {success,...} 信封，
//   套 bizResp 反而会把 PNG 字节当错误体解析；失败口径由它自己抛 ApiError 承担。
import { bizResp, request, authHeaders, API_BASE, handleUnauthorized, ApiError, type AdminResp } from './core'

/** 获取自助注册邀请码列表 */
export async function inviteCodes(): Promise<AdminResp> {
  return bizResp(() => request('/api/admin/invite-codes', { headers: authHeaders() }))
}

/** 生成邀请码（绑定租户或留空新建租户） */
export async function inviteCodeCreate(data: { code: string; tenant_id: number; org_id?: number }): Promise<AdminResp> {
  return bizResp(() => request('/api/admin/invite-codes/create', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) }))
}

// ============================================================================
// 企业专属邀请链接（★ 多身份方案 B3/B4，2026-10-24；R9 拍板「建号不收初始口令」）
// 与上面「管理员手输的注册邀请码」是两套东西、共用同一张 invite_codes 表与同一台状态机：
//   手输码＝管理员写一个码、发给谁不知道；专属链接＝系统生成 inv_<32hex>、
//   预绑用户名/部门/角色（可预绑邮箱）、一次性、默认 7 天、用一次即失效。
// 前端因此要分三区展示（§6.4 命名隔离）：/invites 页仍是「邀请好友」（裂变 ref），
// 管理台这里叫「企业邀请链接」，两种文案在任何一面都不许同文。
// ============================================================================

/** InviteLinkRow 一枚凭据（链接或手输码）的出栈读数；status 为后端派生的四档展示态。 */
export interface InviteLinkRow {
  id: number
  code: string
  used: number
  tenant_id: number
  used_by: string
  created_at: string
  used_at: string
  org_id: number
  revoked: number
  revoked_at: string
  /** active/used/revoked/expired（★ 四档视图的分组键，只读派生，不回库） */
  status: string
  /** code=手输码 / link=专属链接 */
  kind: string
  bound_username: string
  bound_email: string
  role: string
  expires_at: string
  /**
   * 服务端算好的完整落地链接（★ B4-c，2026-10-24）。
   * 列表必须由后端出这一格、前端不许自己拼 `https://${location.host}/?invite=<code>`：
   * host 的取法是「`invite_link_host` 配置 ＞ 该租户品牌域 ＞ 主站 ＞ 请求 Host」
   * （`inviteLinkHostFor`），超管在平台后台给品牌域租户生成的那一档，前端拿到的
   * `location.host` 是平台域名 ⇒ 发出去的链接会把受邀人送到错误的门面。
   * 创建回执（`InviteLinkCreateResult.url`）与列表读数同源同尺。
   */
  url: string
}

/**
 * InviteLinkCreateResult 创建一枚链接的回执（★ 两个调用面共用同一形态：
 * 「邀请链接面板」直接调 inviteLinkCreate，「管理台建号」调 /api/admin/users/create 拿 invite 字段——
 * 后端两处出栈的结构体是同一个 inviteLinkCreateResp，所以这里也只定义一份；
 * 两处各写一遍字段，前端早晚会漂成「一个面读 url、另一个面读 link」）。
 */
export interface InviteLinkCreateResult {
  success?: boolean
  id?: number
  code?: string
  /** 可直接转发的落地链接（`https://<host>/?invite=<code>`） */
  url?: string
  tenant_id?: number
  tenant_name?: string
  /** 到期时刻（UTC RFC3339）；管理台展示"此链接 X 前有效" */
  expires_at?: string
  status?: string
}

/** inviteLinks 列本租户的专属邀请链接（四档状态都在，由 status 分组；超管平台上下文列全平台）。 */
export async function inviteLinks(): Promise<AdminResp & { links?: InviteLinkRow[] }> {
  return bizResp(() => request('/api/admin/invite-links', { headers: authHeaders() }))
}

/**
 * inviteLinkCreate 创建一枚专属邀请链接（R9：管理员只录用户名 + 部门/角色，不录口令、可不录邮箱）。
 * 回执里的 url 是可直接转发的落地链接（`https://<host>/?invite=<code>`），管理台据此复制/发信/出二维码。
 */
export async function inviteLinkCreate(data: {
  username: string
  org_id?: number
  role?: string
  email?: string
  tenant_id?: number
  expires_days?: number
}): Promise<AdminResp & InviteLinkCreateResult> {
  return bizResp(() => request('/api/admin/invite-links', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) }))
}

/**
 * inviteLinkPublic 公开校验一枚邀请链接（注册页预填用，匿名可达）。
 * 后端刻意对「无效/已用/已撤销/已过期」也回 HTTP 200，用 valid=false + reason 表达——
 * 注册页要展示的是那句「这个链接已经过期了，请找管理员重新生成」，不是一个报错页。
 */
export async function inviteLinkPublic(code: string): Promise<AdminResp & {
  valid?: boolean
  reason?: 'invalid' | 'used' | 'revoked' | 'expired'
  message?: string
  tenant_name?: string
  org_name?: string
  username?: string
  bound_email?: string
  email_locked?: boolean
  role?: string
  expires_at?: string
  is_personal?: boolean
  tenant_status?: string
}> {
  return bizResp(() => request(`/api/auth/invite-link/${encodeURIComponent(code)}`))
}

/** inviteCodeRevoke 撤销一枚凭据（码与链接同一状态机、同一风险面，共用这一端点）。只作废未使用的行。 */
export async function inviteCodeRevoke(id: number): Promise<AdminResp> {
  return bizResp(() => request('/api/admin/invite-codes/revoke', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id }) }))
}

/**
 * fetchInviteLinkQrBlob 取一枚专属邀请链接的二维码 PNG（★ B4-c，2026-10-24）。
 *
 * 三条形态纪律，改这一腿前必读：
 * ① **只认链接 id，不接任意文本**。后端没有 `?text=`／`?url=` 的出图口——那等于给每个
 *    登录用户一把钓鱼工具（任一跳板都能打成码贴在页面上）。所以这里传 id，域名与码由服务端算。
 * ② **走 fetch→blob 而不是 `<img src>`**。后台鉴权只有 Authorization 头（见 core.ts `authHeaders`），
 *    `<img>` 带不上头；同一形态已由 `/api/referral/qrcode` 在现网验证过（ReferralP）。
 * ③ **失败必须抛，不许静默返回 null**。死链（已使用/已撤销/已过期）后端回 400 带一句可行动文案，
 *    管理员要看见那句「请重新生成后再发二维码」，而不是看见一张永远转圈的坏图；
 *    401 走 `handleUnauthorized`（清会话），403 走 `handleForbidden` 口径由 core 侧统一处理。
 *
 * `download=true` 时后端额外带 `Content-Disposition`（面板上的「下载二维码」按钮）。
 */
export async function fetchInviteLinkQrBlob(id: number, download = false): Promise<Blob> {
  const path = `/api/admin/invite-links/qrcode?id=${encodeURIComponent(String(id))}${download ? '&download=1' : ''}`
  const resp = await fetch(`${API_BASE}${path}`, { headers: authHeaders() })
  if (resp.status === 401) handleUnauthorized(path)
  const ct = resp.headers.get('content-type') || ''
  if (!resp.ok || !ct.includes('image/png')) {
    // 错误体是 {code,message}；解不出来也照样把状态码带出去，调用方按 status 出兜底文案
    let message = ''
    try { message = String(((await resp.json()) as { message?: string }).message || '') } catch { /* 非 JSON 错误体 */ }
    throw new ApiError(message, resp.status, 'INVITE_QR_FAILED')
  }
  return resp.blob()
}
