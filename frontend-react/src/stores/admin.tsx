// ============================================================================
// stores/admin.tsx — 后台上下文（对应 Vue 版 components/admin/store.ts）
// 职责：后台用户视图、租户列表与切换器（X-Tenant-ID）、面板路由（无 vue-router，
//       沿用 pathname 手搓路由语义）、gotoFeedbackPanel 跨组件跳转。
// ============================================================================

/**
 * stores/admin.tsx · 职责说明
 * 后台管理全局状态 Context，提供以下功能：
 * - 角色权限：根据用户角色计算权限等级、判断管理员身份
 * - 租户管理：租户列表加载、租户切换（X-Tenant-ID）
 * - 组织架构：组织树加载、组织 ID 到组织信息的映射
 * - 面板路由：后台面板切换、路径同步
 * - 跨组件跳转：消息中心点击通知跳转到对应面板
 * - 邀请开关：当前租户的邀请好友功能开关
 */

// 依赖引入：React 基础 Hooks、API（租户/组织/邀请开关）、认证与 i18n 模块
import { useEffect, useMemo } from 'react'
import { create } from 'zustand'
import type { NavigateFunction } from 'react-router-dom'
import { useNavigate } from 'react-router-dom'
import type { ReactNode } from 'react'
import { tenantList as apiTenantList, tenantInviteEnabledGet, setActiveTenantId, getActiveTenantId } from '@/api'
import type { TenantInfo } from '@/api'
import { orgList, type OrgInfo } from '@/api/org'
import { useAuthStore, roleLevel } from './auth'
import { t } from '@/i18n'

/** 后台管理可用面板键名集合（用于面板路由与侧边栏导航） */
export type PanelKey =
  | 'overview' | 'tenants' | 'plans' | 'referral' | 'org' | 'invites' | 'usage' | 'kb'
  | 'models' | 'workflow' | 'apikeys' | 'webhooks' | 'tickets' | 'audit' | 'alerts' | 'users' | 'agreements' | 'brand' | 'mailTpl' | 'footer' | 'system' | 'dataSources'
  | 'external' | 'personal' | 'ops' | 'reconcile' // ★ F9 对账
  | 'billing' | 'opsHub' // ★ Tab 精简（2026-09-15）：计费 Hub / 系统与运维 Hub

/** 根据角色 key 返回本地化展示名称（后台侧边栏展示，i18n）；未知角色返回普通用户
 * @param r - 角色标识字符串（如 super_admin / tenant_admin 等）
 */
export function roleName(r?: string): string {
  if (r === 'super_admin' || r === 'admin') return t('users.role.super_admin')
  if (r === 'tenant_admin' || r === 'approver') return t('users.role.tenant_admin')
  if (r === 'dept_admin') return t('users.role.dept_admin')
  return t('users.role.user')
}

// AdminCtx 对外契约（★ H8：实现换成 zustand，签名保持不变）
interface AdminCtx {
  myLevel: number
  isAdmin: boolean
  isDeptAdmin: boolean
  isTenantAdmin: boolean
  isSuper: boolean
  roleOptions: string[]
  tenants: TenantInfo[]
  inviteEnabled: boolean
  isPersonal: boolean
  tenantName: string
  loadInviteEnabled: () => Promise<void>
  activeTenantId: number
  switchTenant: (tid: number) => void
  loadTenants: () => Promise<void>
  orgs: OrgInfo[]
  orgMap: Map<number, OrgInfo>
  loadOrgs: () => Promise<void>
  clearAuth: () => void
  panel: PanelKey
  gotoPanel: (p: PanelKey) => void
  pendingFeedbackId: number
  openFeedback: (fid: number) => void
  consumeFeedback: () => number
}

// ★ H8：后台域状态唯一来源（Zustand）。导航函数由 AdminProvider 挂载时注入
// （store 内 gotoPanel/openFeedback 需要 navigate，而 zustand action 在 React 外）。
interface AdminState {
  userRole: string
  tenants: TenantInfo[]
  activeTenantId: number
  orgs: OrgInfo[]
  orgMap: Map<number, OrgInfo>
  panel: PanelKey
  pendingFeedbackId: number
  inviteEnabled: boolean
  isPersonal: boolean
  tenantName: string
  navigate: NavigateFunction | null
  setUserRole: (r: string) => void
  bindNavigate: (n: NavigateFunction | null) => void
  loadTenants: () => Promise<void>
  switchTenant: (tid: number) => void
  loadInviteEnabled: () => Promise<void>
  loadOrgs: () => Promise<void>
  clearAuth: () => void
  gotoPanel: (p: PanelKey) => void
  openFeedback: (fid: number) => void
  consumeFeedback: () => number
}

export const useAdminStore = create<AdminState>()((set, get) => ({
  userRole: '',
  tenants: [],
  activeTenantId: getActiveTenantId(),
  orgs: [],
  orgMap: new Map(),
  panel: 'overview',
  pendingFeedbackId: 0,
  inviteEnabled: true,
  isPersonal: false,
  tenantName: '',
  navigate: null,
  setUserRole: (r) => set({ userRole: r }),
  bindNavigate: (n) => set({ navigate: n }),
  loadTenants: async () => {
    try {
      const r = await apiTenantList()
      if (r.success) {
        const list = (r as unknown as { tenants?: TenantInfo[] }).tenants || []
        const stored = getActiveTenantId()
        if (stored > 0 && !list.some((t) => t.id === stored) && list.length) {
          setActiveTenantId(0)
        }
        set({ tenants: list, activeTenantId: getActiveTenantId() ?? 0 })
      }
    } catch { /* 忽略 */ }
  },
  switchTenant: (tid) => {
    setActiveTenantId(tid) // 写入 core（此后所有请求自动带 X-Tenant-ID）
    set({ activeTenantId: tid })
  },
  loadInviteEnabled: async () => {
    try {
      const r = await tenantInviteEnabledGet()
      if (r.success) set({
        inviteEnabled: r.invite_enabled !== false,
        isPersonal: r.is_personal === true,
        tenantName: r.tenant_name || '',
      })
    } catch { /* 忽略 */ }
  },
  loadOrgs: async () => {
    try {
      const r: any = await orgList()
      if (r && r.success) {
        const list: OrgInfo[] = r.orgs || []
        const m = new Map<number, OrgInfo>()
        list.forEach((o) => m.set(o.id, o))
        set({ orgs: list, orgMap: m })
      }
    } catch { /* 忽略 */ }
  },
  clearAuth: () => {
    setActiveTenantId(0)
    try {
      sessionStorage.removeItem('auth_token')
      localStorage.removeItem('auth_token') // 兼容清理旧 localStorage 残留
      localStorage.removeItem('active_tenant_id')
    } catch { /* 忽略 */ }
  },
  gotoPanel: (p) => {
    set({ panel: p })
    if (window.location.pathname !== '/admin') get().navigate?.('/admin')
  },
  openFeedback: (fid) => {
    set({ pendingFeedbackId: fid, panel: 'tickets' })
    if (window.location.pathname !== '/admin') get().navigate?.('/admin')
  },
  consumeFeedback: () => {
    const v = get().pendingFeedbackId
    set({ pendingFeedbackId: 0 })
    return v
  },
}))

/** 后台管理 Provider：★ H8 仅保留副作用编排（自动加载/路由同步/导航注入），
 *  状态本体在 useAdminStore；对外 useAdmin() 契约不变。
 * @param children - 需要访问后台上下文的子组件树
 */
export function AdminProvider({ children }: { children: ReactNode }) {
  const navigate = useNavigate()
  const user = useAuthStore((st) => st.user)
  useEffect(() => { useAdminStore.getState().bindNavigate(navigate) }, [navigate])
  const myLevel = roleLevel(user?.role)
  // 角色进入 store（供 store 内动作回读，也触发派生视图刷新）
  useEffect(() => { useAdminStore.getState().setUserRole(user?.role ?? '') }, [user?.role])
  const activeTenantId = useAdminStore((s) => s.activeTenantId)

  // 仅超管需要租户列表（切换器）；进入后台时拉取一次
  useEffect(() => {
    if (myLevel >= 4) void useAdminStore.getState().loadTenants()
  }, [myLevel])

  // 登录后：【非超管】切到本人租户上下文，并静默加载组织树
  useEffect(() => {
    if (!user) return
    const st = useAdminStore.getState()
    if (roleLevel(user.role) < 4 && user.tenant_id && st.activeTenantId !== user.tenant_id) {
      st.switchTenant(user.tenant_id)
    }
    void st.loadOrgs()
  }, [user, activeTenantId])

  // 租管及以上：登录/切租户后刷新邀请开关（其余角色跳过免 403）
  useEffect(() => {
    if (user && roleLevel(user.role) >= 3) void useAdminStore.getState().loadInviteEnabled()
  }, [user, activeTenantId])

  // 前进/后退与 /admin 路径保持一致
  useEffect(() => {
    const onPop = () => {
      if (window.location.pathname.startsWith('/admin')) useAdminStore.setState({ panel: 'overview' })
    }
    window.addEventListener('popstate', onPop)
    return () => window.removeEventListener('popstate', onPop)
  }, [])

  return <>{children}</>
}

/** 读取后台管理状态（★ H8：zustand 选择器聚合，返回引用稳定） */
export function useAdmin(): AdminCtx {
  const s = useAdminStore()
  const user = useAuthStore((st) => st.user)
  return useMemo<AdminCtx>(() => {
    const myLevel = roleLevel(user?.role)
    const isSuper = myLevel >= 4
    const isTenantAdmin = myLevel >= 3
    const isDeptAdmin = myLevel >= 2
    const roleOptions = isSuper ? ['user', 'dept_admin', 'tenant_admin', 'admin']
      : isTenantAdmin ? ['user', 'dept_admin', 'tenant_admin']
        : isDeptAdmin ? ['user'] : []
    return {
      myLevel, isAdmin: myLevel >= 2, isDeptAdmin, isTenantAdmin, isSuper, roleOptions,
      tenants: s.tenants, activeTenantId: s.activeTenantId,
      switchTenant: s.switchTenant, loadTenants: s.loadTenants, clearAuth: s.clearAuth,
      orgs: s.orgs, orgMap: s.orgMap, loadOrgs: s.loadOrgs,
      panel: s.panel, gotoPanel: s.gotoPanel,
      pendingFeedbackId: s.pendingFeedbackId, openFeedback: s.openFeedback, consumeFeedback: s.consumeFeedback,
      inviteEnabled: s.inviteEnabled, isPersonal: s.isPersonal,
      loadInviteEnabled: s.loadInviteEnabled, tenantName: s.tenantName,
    }
  }, [s, user])
}
