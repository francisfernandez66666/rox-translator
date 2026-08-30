// ============================================================================
// stores/auth.tsx — 全局登录态（React Context 版，对应 Vue 版 App.vue 会话逻辑）
// 职责：authUser 状态、restoreSession（token→/api/auth/me）、roleLevel 四级判定、
//       登录/登出收敛点（修复 Vue 版「无全局 401 收敛」的已知问题）。
// ============================================================================
// 依赖引入：React 基础 Hooks、API（会话校验/Token 读写）与 AuthUser 类型
import { createContext, useCallback, useContext, useEffect, useMemo, useState } from 'react'
import type { ReactNode } from 'react'
import { authMe, setAuthToken, getAuthToken } from '@/api'
import type { AuthUser } from '@/api'

// ============ 本文件职责中文说明 ============
// 全局登录态：认证上下文、会话恢复与角色等级判定。
// ========================================

/** roleLevel 角色等级：super_admin/admin=4 · tenant_admin/approver=3 · dept_admin=2 · 其他=1
 *  （单一来源，与后端 auth.IsSuperAdmin/IsTenantAdmin 口径一致） */
export function roleLevel(r?: string): number {
  if (r === 'super_admin' || r === 'admin') return 4
  if (r === 'tenant_admin' || r === 'approver') return 3
  if (r === 'dept_admin') return 2
  return 1
}

// AuthCtx 认证上下文对外暴露的状态与方法类型定义
interface AuthCtx {
  user: AuthUser | null
  restoring: boolean
  /** 登录成功 / 会话恢复后调用 */
  onLogin: (u: AuthUser) => void
  logout: () => void
}

// AuthContext 默认值：初始处于「恢复中」状态、未登录，方法为空实现
const Ctx = createContext<AuthCtx>({ user: null, restoring: true, onLogin: () => {}, logout: () => {} })

/** 认证状态 Provider：管理登录态、会话恢复，以及登录/登出的统一收敛点
 * @param children - 需要访问认证上下文的子组件树
 */
export function AuthProvider({ children }: { children: ReactNode }) {
  // 当前登录用户信息，null 表示未登录
  const [user, setUser] = useState<AuthUser | null>(null)
  // 是否正在根据本地 token 恢复会话；无 token 时直接结束
  const [restoring, setRestoring] = useState<boolean>(!!getAuthToken())

  useEffect(() => {
    let alive = true
    // ★ 会话恢复：有 token → authMe 校验；失败清 token 回登录页
    ;(async () => {
      if (!getAuthToken()) { setRestoring(false); return }
      try {
        const r = await authMe()
        if (alive && r.success && r.user) setUser(r.user)
        else setAuthToken('')
      } catch {
        setAuthToken('')
      } finally {
        if (alive) setRestoring(false)
      }
    })()
    return () => { alive = false }
  }, [])

  // 登录成功或会话恢复后写入用户信息
  const onLogin = useCallback((u: AuthUser) => setUser(u), [])
  // 清除 token 与本地用户状态，完成登出
  const logout = useCallback(() => {
    setAuthToken('')
    setUser(null)
  }, [])

  // 缓存上下文值，避免 Provider 子树不必要重渲染
  const value = useMemo(() => ({ user, restoring, onLogin, logout }), [user, restoring, onLogin, logout])
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>
}

/** 在函数组件中读取认证上下文（必须在 <AuthProvider> 内使用） */
export function useAuth() { return useContext(Ctx) }
