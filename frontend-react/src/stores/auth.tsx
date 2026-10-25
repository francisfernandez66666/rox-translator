// ============================================================================
// stores/auth.tsx — 全局登录态（★ H8 Zustand 版）
// 职责：authUser 状态、restoreSession（token→/api/auth/me）、roleLevel 四级判定、
//       登录/登出收敛点。状态源收敛到 zustand useAuthStore（可在组件树外读写），
//       AuthProvider 仅保留首屏会话恢复副作用；useAuth() 公开签名保持不变。
// ============================================================================

/**
 * stores/auth.tsx · 职责说明
 * 全局认证状态（Zustand）：
 * - 用户状态管理：当前登录用户信息、会话恢复状态
 * - 会话恢复：启动时根据本地 token 调用 /api/auth/me 恢复会话
 * - 角色等级判定：四级角色体系（super_admin=4, tenant_admin=3, dept_admin=2, user=1）
 * - 登录/登出：统一的登录成功和登出处理
 * - ★ 多身份（B4）：本账户全部身份清单 identities、loadIdentities 补拉与 switchTo 切换
 *   （切换器与账号菜单共用这一份，不在组件里各拉一次）
 */

import { useEffect, useMemo } from 'react'
import type { ReactNode } from 'react'
import { create } from 'zustand'
import { authMe, setAuthToken, setActiveTenantId, getAuthToken, setUnauthorizedHandler, getActiveTenantId, logout as authLogout, identities as fetchIdentities, switchIdentity } from '@/api'
import type { AuthUser, Identity } from '@/api'

/** roleLevel 角色等级：super_admin/admin=4 · tenant_admin/approver=3 · dept_admin=2 · 其他=1
 *  （单一来源，与后端 auth.IsSuperAdmin/IsTenantAdmin 口径一致） */
/** 角色等级序（owner>admin>member 便于门控比较） */
export function roleLevel(r?: string): number {
  if (r === 'super_admin' || r === 'admin') return 4
  if (r === 'tenant_admin' || r === 'approver') return 3
  if (r === 'dept_admin') return 2
  return 1
}

/**
 * ★ O-9（2026-09-26 批 I-10）：「平台计费上下文」判定——**平台身份且当前没有切入任何租户**。
 *
 * 为什么需要它：`/api/me/package` 对 `tid<=0` 的语义是「平台账号不参与计费，余额返回 0」，
 * 这个出参本身是对的（改后端只会把语义搞浑）。但界面两处余额位（App 顶栏积分行、
 * ChatWindow 余额条）都照 `points_balance` 直渲染，于是超管登录后永久看到「余额 0 积分」，
 * 并且 `points_balance<=0` 会点亮 E11 的「余额不足」顶部横幅——一个根本不会扣点的身份
 * 天天被告知没钱，属纯噪声加误导。
 *
 * 判据为什么与后端同源：后端 `effTenant(r,u)` 对超管读 `X-Tenant-ID` 头，
 * 而 `authHeaders()`（api/core.ts）**只在 activeTenantId>0 时才下发该头**，
 * 故「等级≥4 且本地无生效租户」严格等价于「服务端这次看到 tid<=0」。
 * 两处余额位统一调本函数，判据只写一份；不要在任何一处另拼条件。
 */
export function isPlatformBillingContext(role?: string): boolean {
  return roleLevel(role) >= 4 && getActiveTenantId() <= 0
}

// AuthCtx 认证上下文对外暴露的状态与方法类型定义（公开契约保持不变）
interface AuthCtx {
  user: AuthUser | null
  restoring: boolean
  /** 登录成功 / 会话恢复后调用；ids 为登录/注册/切换三条出口都带的身份数组（不传则保持原值） */
  onLogin: (u: AuthUser, ids?: Identity[]) => void
  logout: () => void
  // ★ 多身份方案 B4（2026-10-24）：当前账户的全部可用身份与两个动作。
  //   放在这里而不是各组件自己拉一次：切换器（左栏徽标）与账号菜单都要读同一份清单，
  //   两处各打一次 /api/auth/identities 就是「同一件事写两遍」的前端版本（AGENTS §一·5 同源教训）。
  /** 本账户全部可用身份（家行在前）；空数组＝还没取到或只有一行 */
  identities: Identity[]
  /** 重新拉取身份清单（登录/切换/成员被移除后调用）；失败保留上一份，不清空 */
  loadIdentities: () => Promise<Identity[]>
  /** 切到本账户名下另一个身份；成功即换 token 与 user，失败回一句可行动文案 */
  switchTo: (tid: number) => Promise<{ ok: boolean; message?: string }>
}

// ★ H8：认证状态的唯一来源（Zustand）。组件树外（api core 401 收敛等）
// 也可安全读写；Provider 只做一次性会话恢复。
interface AuthState extends AuthCtx {
  setRestoring: (v: boolean) => void
  setIdentities: (ids: Identity[]) => void
}

/** useAuthStore 登录态全局 store（uid/token/租户上下文/身份清单，zustand） */
export const useAuthStore = create<AuthState>()((set, get) => ({
  user: null,
  restoring: !!getAuthToken(),
  identities: [],
  onLogin: (u, ids) => set({ user: u, restoring: false, ...(ids ? { identities: ids } : {}) }),
  setIdentities: (ids) => set({ identities: ids }),
  // loadIdentities 只在**成功**时覆盖状态：一次网络抖动不该把切换器上的身份清单洗成空
  // （表现是"我的企业身份不见了"，而真因是那一发请求没通）。
  loadIdentities: async () => {
    try {
      const r = await fetchIdentities()
      const ids = (r.identities || []).filter(Boolean)
      set({ identities: ids })
      return ids
    } catch {
      return get().identities
    }
  },
  // switchTo 是「换到另一个身份」这一动作的唯一前端落点（切换器之外不另开第二条链）。
  // 顺序纪律：先落 token，再落 user —— 反过来会让任何一次读 authHeaders() 的渲染
  // 拿到「新用户 + 旧会话」的错配（旧 token 的 (uid,tid) 才是服务端此刻真正认的那个人）。
  switchTo: async (tid: number) => {
    try {
      const r = await switchIdentity(tid)
      if (!r.success || !r.token || !r.user) return { ok: false, message: r.message || '' }
      setAuthToken(r.token)
      // ★ B4：连同「生效租户」一起归零。activeTenantId 是超管后台租户切换器的作用域（authHeaders
      //   在 >0 时下发 X-Tenant-ID），服务端对超管按那个头裁定当前租户；切完身份还留着上一份，
      //   会得到「新会话 + 旧租户作用域」——切进 A 企业却处处读到 B（现网极难归因）。
      setActiveTenantId(0)
      set({ user: r.user, identities: r.identities || [] })
      return { ok: true }
    } catch (e) {
      return { ok: false, message: (e as Error)?.message || '' }
    }
  },
  logout: () => {
    // ★ 2026-10-21 IAM 重做 P0-11：登出不再只是"本地抹掉 token"。
    //   JWT 无状态，抹掉本地那份不等于凭证失效——它在到期前被人抄走仍然有效
    //   （公用电脑上一个人登出、下一个人拿旧 token 照样读得到前一个人的数据）。
    //   先发起服务端撤销（按 jti 记标记），**再**清本地；顺序不能反——反了 authHeaders()
    //   就已经拿不到 token，请求变成匿名调用，服务端无从知道该撤销哪一条。
    //   这一步是 best-effort：网络失败/服务端无共享存储时只清本地（服务端会如实回 revoked:false），
    //   绝不因为"撤销请求没成功"就把用户卡在已登出不了的状态里。
    void authLogout().catch(() => { /* 登出永不因服务端不可达而失败 */ })
    setAuthToken('')
    // ★ 2026-10-23 F1-d：登出连同「生效租户」一起清零。旧形态只清 token，租管会话选过的
    //   租户号会留在 sessionStorage，被同一浏览器里的下一个登录会话（尤其超管）静默继承。
    setActiveTenantId(0)
    // ★ B4：身份清单随会话一起归零。留着上一位登出者的身份数组，
    //   下一个登录的人会在切换器里看到"不属于自己的企业"（渲染的是内存态，不是库里的真值）。
    set({ user: null, identities: [] })
  },
  setRestoring: (v) => set({ restoring: v }),
}))

// 兼容旧内部 API：供 admin store 等以模块函数方式读取
export const authStore = useAuthStore

/** 认证状态 Provider：仅负责首屏会话恢复副作用，状态本体在 zustand
 * @param children - 需要访问认证上下文的子组件树
 */
/** 认证状态根：登录/登出/会话恢复（含 S1 积分汇率注入） */
export function AuthProvider({ children }: { children: ReactNode }) {
  useEffect(() => {
    // ★ P0-6：向 api/core 注入 401 复位钩子——任何请求通道收到 401 即 user→null，
    // Root 守卫在当前路径原地出登录页（登录后回到失效前页面，回跳不丢）
    setUnauthorizedHandler(() => useAuthStore.setState({ user: null, restoring: false, identities: [] }))
    return () => setUnauthorizedHandler(null)
  }, [])
  useEffect(() => {
    let alive = true
    const { setRestoring, onLogin } = useAuthStore.getState()
    // ★ 会话恢复：有 token → authMe 校验；失败清 token 回登录页
    ;(async () => {
      if (!getAuthToken()) { setRestoring(false); return }
      let r
      try {
        r = await authMe()
      } catch { r = null }
      // StrictMode 双挂载下本实例可能已被取代（alive=false）：静默忽略，
      // 仅当响应确实无效才清凭证（旧写法把 alive=false 也当失败清 token，引发 401 风暴）
      if (r && r.success && r.user) {
        if (alive) onLogin(r.user)
        // ★ B4：/api/auth/me 不带 identities（它出栈的是原始 user 行），所以恢复会话这一条腿
        //   必须补一次身份清单拉取——否则刷新页面后切换 icon 会消失，
        //   而同一个人从登录页进来时它是渲染的（登录响应里带）。
        if (alive) void useAuthStore.getState().loadIdentities()
      }
      else if (!alive) { /* 被新实例接管，由其负责状态 */ }
      else { setAuthToken(''); useAuthStore.setState({ user: null, identities: [] }) }
      if (alive) setRestoring(false)
    })()
    return () => { alive = false }
  }, [])

  return <>{children}</>
}

/** 在函数组件中读取认证状态（★ H8：直连 zustand，不再依赖 Provider 层级；
 *  保留 Context 兜底仅为独立预览等无 Provider 场景的旧行为兼容） */
/** 取认证上下文 */
export function useAuth(): AuthCtx {  const user = useAuthStore((s) => s.user)
  const restoring = useAuthStore((s) => s.restoring)
  const onLogin = useAuthStore((s) => s.onLogin)
  const logout = useAuthStore((s) => s.logout)
  const identities = useAuthStore((s) => s.identities)
  const loadIdentities = useAuthStore((s) => s.loadIdentities)
  const switchTo = useAuthStore((s) => s.switchTo)
  return useMemo(
    () => ({ user, restoring, onLogin, logout, identities, loadIdentities, switchTo }),
    [user, restoring, onLogin, logout, identities, loadIdentities, switchTo],
  )
}

/**
 * applyLogin 登录成功的统一收尾（★ 2026-10-21 IAM 重做 P1：去掉 App.tsx / PublicRoot.tsx 两份手抄）。
 *
 * 两棵树（登录态走 App，未登录公开面走 PublicRoot）对「登录成功之后做什么」必须是同一件事：
 * 落全局 user ＋ 后台档下把不够格的账号弹回首页。旧形态在两边各写了一遍三元判断——
 * 将来改其中一处（比如把门槛从 2 抬到 3）另一处不会跟，而这两棵树的切换是用户完全无感的，
 * 于是同一个人从 `/` 登进去和从 `/admin` 登进去会得到两种权限结果。
 *
 * @param u        登录接口返回的当前用户
 * @param onLogin  全局登录态 setter（useAuth().onLogin）
 * @param pathname 当前路径（判定是否处于后台档）
 * @param navigate 路由跳转（两棵树各自的实现，签名都是 (to: string) => void）
 * @param ids      ★ B4：登录/注册响应携带的身份数组（有就直接落进 store，省一次 /api/auth/identities
 *                 往返；没有则留空，由会话恢复那一腿补拉，切换器不会因此永久缺席）
 */
export function applyLogin(
  u: AuthUser,
  onLogin: (u: AuthUser, ids?: Identity[]) => void,
  pathname: string,
  navigate: (to: string) => void,
  ids?: Identity[],
): void {
  onLogin(u, ids)
  // admin 档下普通成员（<2）即便拿到 token 也不放行：Login 已拦一次，这里是第二道
  if (pathname.startsWith('/admin') && roleLevel(u.role) < 2) navigate('/')
}
