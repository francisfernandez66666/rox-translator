// ============================================================================
// api/auth.ts — 认证域接口
// 职责：账号密码登录、会话恢复（me）、自助注册（可带邀请码/租户信息）
// ============================================================================

/**
 * api/auth.ts · 职责说明
 * 封装用户认证相关的所有接口，包括：
 * - 登录认证：账号密码登录、会话恢复（me 接口）
 * - 自助注册：支持邀请码、租户信息、行业、邮箱验证码、人机验证
 * - 密码管理：忘记密码、重置密码、自助修改密码
 * - 邮箱管理：发送验证码、绑定/修改邮箱
 * - 账号注销：自助注销账号
 */

// ★ F-64②（2026-09-26 批 I-10）：本文件所有接口统一经 core.ts 的 bizResp 接线——
//   HTTP 200 但业务体 success:false 会被如实降级为异常口径，调用方不再拿到「假成功」；
//   新增接口一律写 bizResp(() => request(...))，禁止直返裸 request。
import { bizResp, request, authHeaders, API_BASE, currentUiLang, type AdminResp } from './core'
// ★ F-81（2026-09-28）：注册请求随附浏览器设备号（与免登录试用同一份标识，见 lib/trialDevice.ts 文件头）
import { trialDeviceID } from '@/lib/trialDevice'

/** 登录用户信息结构：含 id/用户名/显示名/角色/所属租户 */
export interface AuthUser {
  id: number
  username: string
  display_name: string
  role: string
  tenant_id: number
  email?: string
  must_change_pwd?: number
  /** 首登资料提醒（可跳过；与 must_change_pwd 的 428 强制闸分离） */
  profile_setup_required?: number
  [key: string]: unknown
}

// LoginResp 登录接口响应结构：token 为 JWT 凭证、user 为当前用户信息。
export interface LoginResp {
  success: boolean
  message?: string
  token?: string
  /** 品牌专属域名：当用户所属租户配置了独立子域且本次登录不在该子域时返回，前端据此跳转过去 */
  brand_host?: string
  /** ★ P0-3（2026-09-14）：跨子域跳转用一次性兑换码（60s、单次消费，落地后经 /api/auth/sso/exchange 换 JWT），JWT 不再进 URL */
  sso_code?: string
  user?: AuthUser
}

/** 账号密码登录，成功返回 JWT token 与用户信息 */
/** 登录：用户名+密码（兼容无邮箱存量/UAT），返回会话 token 与用户/组织上下文 */
export async function login(username: string, password: string): Promise<LoginResp> {
  return request('/api/auth/login', { method: 'POST', body: JSON.stringify({ username, password }) })
}

/**
 * 邮箱验证码登录（主流程）。未注册邮箱在主站会自动创建空白个人账号，
 * 因此自动建号分支必须带 agreed/device_id/captcha（与注册同一套防薅口径）。
 */
export async function loginWithEmail(email: string, code: string, opts?: { agreed?: boolean; captchaToken?: string }): Promise<LoginResp> {
  return bizResp(() => request('/api/auth/login', {
    method: 'POST',
    body: JSON.stringify({
      email, code,
      agreed: !!opts?.agreed,
      device_id: trialDeviceID(),
      captcha_token: opts?.captchaToken,
    }),
  }))
}

/** 首登资料初始化：skip=true 只关本次弹窗（服务端仍保留提醒，下次登录再来）。 */
export async function profileSetup(payload: { username?: string; password?: string; job_role?: string; skip?: boolean }): Promise<LoginResp> {
  return bizResp(() => request('/api/auth/profile-setup', { method: 'POST', headers: authHeaders(), body: JSON.stringify(payload) }))
}

/** SSOProvider 已接入的身份源（IdP）：name 回传后端，display_name 用于按钮文案 */
export interface SSOProvider { name: string; display_name: string }

/**
 * ssoProviders 查询已启用的 IdP（★ #38 前端接线）。
 * 服务端未配置 SSO 时回 {enabled:false, providers:[]}，调用方据此整块不渲染按钮，
 * 不能在登录页留一排点了只会报错的死按钮。
 */
export async function ssoProviders(): Promise<{ success?: boolean; enabled: boolean; providers: SSOProvider[] }> {
  return request('/api/sso/providers')
}

/**
 * ssoLoginUrl SSO 发起地址（/api/sso/login?provider=）。
 * 必须以浏览器整页跳转消费：该端点回 302 到 IdP 授权页并下发 state cookie，
 * fetch 会把它当跨域 XHR 解析而丢掉整个重定向链。
 */
export function ssoLoginUrl(provider: string): string {
  return `${API_BASE}/api/sso/login?provider=${encodeURIComponent(provider)}`
}

/** 登录后自助修改密码（校验原密码）。首登强制改密（must_change_pwd=1）时用于设置新密码 */
/** 修改密码（校验旧密码） */
export async function changePassword(old_password: string, new_password: string, email_code?: string): Promise<AdminResp> {
  return bizResp(() => request('/api/auth/change-password', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ old_password, new_password, email_code }) }))
}

/** 校验当前 token 对应的用户信息（用于会话恢复；出参积分口径，前端零换算） */
export async function authMe(): Promise<LoginResp> {
  return request('/api/auth/me', { headers: authHeaders() })
}

/**
 * 自助注册：可带邀请码/租户信息/行业/邮箱验证码/人机验证 token。
 * @param data 注册字段（username/password 必填，其余可选；ref 为邀请裂变个人码）
 */
/** 自助注册：组织名/邮箱验证码/Turnstile/邀请码/UTM 归因一并上报。
 *  ★ F-17（2026-09-25 批E）：app_lang=注册界面语种（12 码），调用方未显式传入时自动随 currentUiLang() 带上；
 *  后端按「载荷 app_lang → X-App-Lang 头 → 中文」解析，落库 users.preferred_lang 供邮件语种跟随。 */
export async function authRegister(data: { username: string; password: string; type?: string; code?: string; name?: string; invite?: string; email?: string; email_code?: string; captcha_token?: string; industry?: string; job_role?: string; app_lang?: string; role_choice?: string; ref?: string; agreed?: boolean; brand_name?: string; brand_name_en?: string; brand_names?: string; landing_path?: string; utm_source?: string; utm_medium?: string; utm_campaign?: string; utm_term?: string; utm_content?: string; device_id?: string }): Promise<AdminResp & { token?: string; user?: AuthUser }> {
  // ★ F-81（2026-09-28）：device_id 与 app_lang 同口径在出口处兜上，调用方不传也有——
  //   后端拿它做「同一浏览器每日新建免费账号」那一档防薅账（与免登录试用同一份设备号、同一张限流表）。
  //   在这里收口而不是在每个注册表单里各写一遍：漏一个入口就等于给刷号留一个免记账的口子。
  const payload = { app_lang: currentUiLang() || undefined, device_id: trialDeviceID(), ...data }
  return request('/api/auth/register', { method: 'POST', headers: authHeaders(), body: JSON.stringify(payload) })
}

/** 发送注册邮箱验证码（noop=true 表示服务端邮件未配置，验证码打印在服务端日志） */
/** 注册邮箱验证码（开启 Turnstile 时须带人机 token）；★ F-17：app_lang 随界面语种上报，验证码邮件按语种取稿 */
export async function sendEmailCode(email: string, captchaToken?: string, purpose?: 'register' | 'login' | 'password' | 'email_change'): Promise<AdminResp & { noop?: boolean }> {
  return request('/api/auth/email-code', { method: 'POST', body: JSON.stringify({ email, purpose, captcha_token: captchaToken, app_lang: currentUiLang() || undefined }) })
}

/** 获取公开注册配置（email_verify_enabled，前端据以显隐验证码输入） */
/** 注册页公开配置（邮箱验证/人机验证开关等） */
export async function registerConfig(): Promise<AdminResp & { email_verify_enabled?: boolean }> {
  return request('/api/auth/register-config')
}

/** 获取注册行业列表（无需登录，来自超管维护的行业包） */
/** 注册可选行业清单（超管配置驱动） */
export async function registerIndustries(): Promise<AdminResp> {
  return request('/api/register/industries')
}

// ============================================================================
// 已登录用户自助修改密码（邮箱验证码通道）
// ★ 2026-10-21 IAM 重做：公开找回/重置端点已下线，这里改走登录态的 /email-code
//   （purpose=password，收件地址由服务端取当前账号，不接受客户端任意指定）与 change-password。
// ============================================================================

/** sendPwdCode 向当前账号绑定邮箱发送改密验证码；入参 email 仅作界面回显，服务端以登录态为准。 */
export async function sendPwdCode(data: { email: string }): Promise<AdminResp> {
  return sendEmailCode(data.email, undefined, 'password')
}

/** submitNewPassword 校验当前账号邮箱验证码后设置新密码。 */
export async function submitNewPassword(data: { code: string; new_password: string }): Promise<AdminResp> {
  return changePassword('', data.new_password, data.code)
}

/** meEmailCode 向新邮箱发送变更验证码（需登录） */
/** 换绑邮箱：向新邮箱发验证码 */
export async function meEmailCode(email: string): Promise<AdminResp & { noop?: boolean }> {
  return bizResp(() => request('/api/me/email-code', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ email }) }))
}

/** updateEmail 登录用户自助绑定/修改邮箱（需携带发往新邮箱的验证码） */
/** 换绑邮箱（新旧双向验证码） */
export async function updateEmail(email: string, code: string, oldCode = ''): Promise<AdminResp> {
  return bizResp(() => request('/api/me/update-email', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ email, new_code: code, old_code: oldCode }) }))
}

/** deactivateAccount 自助注销：当日宽限、次日失效；名下 API Key 立即停用；数据保留 */
/** 账号自助注销（数据按合规策略处理） */
export async function deactivateAccount(): Promise<AdminResp> {
  return request('/api/me/deactivate', { method: 'POST' })
}

/**
 * logout 服务端登出（★ 2026-10-21 IAM 重做 P0-11）。
 *
 * 为什么要请求服务端：JWT 是无状态的，前端把本地 token 抹掉**不等于**那条凭证失效——
 * 它在到期前仍可被复制出去继续用（公用电脑上"登出"完，前一个人留下的 token 还能读到后一个人的数据）。
 * 服务端按 jti 记一条撤销标记，之后任何带这条 token 的请求都会被判已撤销。
 *
 * 返回值里的 `revoked` 是对外诚实口径：服务端没有共享存储（单机内存/无 Redis）时**撤销不了**，
 * 这一档必须如实回 false，让调用方与运维看得见"这次登出只清了本地"，绝不能假装成功。
 */
export async function logout(): Promise<AdminResp & { revoked?: boolean }> {
  return bizResp(() => request('/api/auth/logout', { method: 'POST', headers: authHeaders() }))
}
