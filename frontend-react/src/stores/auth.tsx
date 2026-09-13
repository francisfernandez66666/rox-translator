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
import { authMe, setAuthToken, getAuthToken } from '@/api'
import type { AuthUser } from '@/api'

/** roleLevel 角色等级：super_admin/admin=4 · tenant_admin/approver=3 · dept_admin=2 · 其他=1
 *  （单一来源，与后端 auth.IsSuperAdmin/IsTenantAdmin 口径一致） */
export function roleLevel(r?: string): number {
  if (r === 'super_admin' || r === 'admin') return 4
  if (r === 'tenant_admin' || r === 'approver') return 3
  if (r === 'dept_admin') return 2
  return 1
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
export function AuthProvider({ children }: { children: ReactNode }) {
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
      if (r && r.success && r.user) { if (alive) onLogin(r.user) }
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
export function useAuth(): AuthCtx {
  const user = useAuthStore((s) => s.user)
  const restoring = useAuthStore((s) => s.restoring)
  const onLogin = useAuthStore((s) => s.onLogin)
  const logout = useAuthStore((s) => s.logout)
  return useMemo(() => ({ user, restoring, onLogin, logout }), [user, restoring, onLogin, logout])
}
