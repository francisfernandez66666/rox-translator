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
 */

import { useEffect, useMemo } from 'react'
import type { ReactNode } from 'react'
import { create } from 'zustand'
import { authMe, setAuthToken, getAuthToken, setUnauthorizedHandler, getActiveTenantId } from '@/api'
import type { AuthUser } from '@/api'

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
  /** 登录成功 / 会话恢复后调用 */
  onLogin: (u: AuthUser) => void
  logout: () => void
}

// ★ H8：认证状态的唯一来源（Zustand）。组件树外（api core 401 收敛等）
// 也可安全读写；Provider 只做一次性会话恢复。
interface AuthState extends AuthCtx {
  setRestoring: (v: boolean) => void
}

/** useAuthStore 登录态全局 store（uid/token/租户上下文，zustand） */
export const useAuthStore = create<AuthState>()((set) => ({
  user: null,
  restoring: !!getAuthToken(),
  onLogin: (u) => set({ user: u, restoring: false }),
  logout: () => {
    setAuthToken('')
    set({ user: null })
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
    setUnauthorizedHandler(() => useAuthStore.setState({ user: null, restoring: false }))
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
      }
      else if (!alive) { /* 被新实例接管，由其负责状态 */ }
      else { setAuthToken(''); useAuthStore.setState({ user: null }) }
      if (alive) setRestoring(false)
    })()
    return () => { alive = false }
  }, [])

  return <>{children}</>
}

/** 在函数组件中读取认证状态（★ H8：直连 zustand，不再依赖 Provider 层级；
 *  保留 Context 兜底仅为独立预览等无 Provider 场景的旧行为兼容） */
/** 取认证上下文 */
export function useAuth(): AuthCtx {
  const user = useAuthStore((s) => s.user)
  const restoring = useAuthStore((s) => s.restoring)
  const onLogin = useAuthStore((s) => s.onLogin)
  const logout = useAuthStore((s) => s.logout)
  return useMemo(() => ({ user, restoring, onLogin, logout }), [user, restoring, onLogin, logout])
}
