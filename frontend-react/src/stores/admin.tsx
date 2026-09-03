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
import { createContext, useCallback, useContext, useEffect, useMemo, useState } from 'react'
import type { ReactNode } from 'react'
import { tenantList as apiTenantList, tenantInviteEnabledGet, setActiveTenantId, getActiveTenantId } from '@/api'
import type { TenantInfo } from '@/api'
import { orgList, type OrgInfo } from '@/api/org'
import { useAuth, roleLevel } from './auth'
import { t } from '@/i18n'

/** 后台管理可用面板键名集合（用于面板路由与侧边栏导航） */
export type PanelKey =
  | 'overview' | 'tenants' | 'plans' | 'referral' | 'org' | 'invites' | 'usage' | 'kb'
  | 'models' | 'workflow' | 'apikeys' | 'webhooks' | 'tickets' | 'audit' | 'alerts' | 'users' | 'agreements' | 'brand' | 'mailTpl' | 'footer' | 'system' | 'dataSources'

/** 根据角色 key 返回本地化展示名称（后台侧边栏展示，i18n）；未知角色返回普通用户
 * @param r - 角色标识字符串（如 super_admin / tenant_admin 等）
 */
export function roleName(r?: string): string {
  if (r === 'super_admin' || r === 'admin') return t('users.role.super_admin')
  if (r === 'tenant_admin' || r === 'approver') return t('users.role.tenant_admin')
  if (r === 'dept_admin') return t('users.role.dept_admin')
  return t('users.role.user')
}

// AdminContext 对外暴露的状态与方法类型
interface AdminCtx {
  // 当前用户角色等级
  myLevel: number
  // 是否为管理员（等级≥2）
  isAdmin: boolean
  // 是否为部门管理员（等级≥2）
  isDeptAdmin: boolean
  // 是否为租户管理员（等级≥3）
  isTenantAdmin: boolean
  // 是否为超级管理员（等级≥4）
  isSuper: boolean
  // 当前用户可分配的角色选项
  roleOptions: string[]
  // 租户列表（仅超管需要）
  tenants: TenantInfo[]
  // 当前生效租户是否开通「邀请好友」功能（超管按租户开关；默认 true）
  inviteEnabled: boolean
  // 当前生效租户是否为个人用户租户（个人用户不显示「企业管理」类后台；企业用户不显示「邀请好友」除非开通开关）
  isPersonal: boolean
  // 重新拉取当前生效租户的「邀请好友」开关
  loadInviteEnabled: () => Promise<void>
  // 当前选中的租户 ID（0 表示平台根组织）
  activeTenantId: number
  // 切换当前租户上下文
  switchTenant: (tid: number) => void
  // 加载租户列表
  loadTenants: () => Promise<void>
  // 组织树（登录后静默加载；供部门包映射为部门名等使用）
  orgs: OrgInfo[]
  // 组织 id → 组织信息（含名称），供部门包按 id 反查部门名
  orgMap: Map<number, OrgInfo>
  // 静默加载当前租户组织树
  loadOrgs: () => Promise<void>
  // 清除本地认证与租户信息
  clearAuth: () => void
  // 当前展示的后台面板
  panel: PanelKey
  // 切换后台面板
  gotoPanel: (p: PanelKey) => void
  // 跨组件跳转：消息中心点击 feedback 类通知 → 跳转问题反馈面板并打开对应详情
  pendingFeedbackId: number
  // 打开指定反馈详情并切到工单面板
  openFeedback: (fid: number) => void
  // 消费待处理反馈 ID（读取后清空）
  consumeFeedback: () => number
}

// AdminContext 默认值：所有状态/方法提供安全的空实现，避免未初始化时访问报错
const Ctx = createContext<AdminCtx>({
  myLevel: 0, isAdmin: false, isDeptAdmin: false, isTenantAdmin: false, isSuper: false,
  roleOptions: [], tenants: [], activeTenantId: 0,
  switchTenant: () => {}, loadTenants: async () => {}, clearAuth: () => {},
  orgs: [], orgMap: new Map(), loadOrgs: async () => {},
  panel: 'overview', gotoPanel: () => {}, pendingFeedbackId: 0,
  openFeedback: () => {}, consumeFeedback: () => 0,
  inviteEnabled: true, isPersonal: false, loadInviteEnabled: async () => {},
})

/** 后台管理全局状态 Provider：管理租户列表/切换、角色权限、面板路由与反馈跨组件跳转
 * @param children - 需要访问后台上下文的子组件树
 */
export function AdminProvider({ children }: { children: ReactNode }) {
  // 从认证上下文获取当前用户
  const { user } = useAuth()
  // 当前用户角色等级
  const myLevel = roleLevel(user?.role)
  // 租户列表（仅超管加载）
  const [tenants, setTenants] = useState<TenantInfo[]>([])
  // 当前选中的租户 ID
  const [activeTenantId, setActive] = useState<number>(getActiveTenantId())
  // 当前租户组织树（登录后静默加载）
  const [orgs, setOrgs] = useState<OrgInfo[]>([])
  const [orgMap, setOrgMap] = useState<Map<number, OrgInfo>>(new Map())
  // 当前展示的后台面板
  const [panel, setPanel] = useState<PanelKey>('overview')
  // 消息中心跳转而来的待处理反馈 ID
  const [pendingFeedbackId, setPendingFeedbackId] = useState<number>(0)
  // 当前生效租户是否开通「邀请好友」功能（默认开通）
  const [inviteEnabled, setInviteEnabled] = useState<boolean>(true)
  // 当前生效租户是否为个人用户租户（默认 false=企业/平台）
  const [isPersonal, setIsPersonal] = useState<boolean>(false)

  // 是否为管理员/部门管理员（等级≥2）
  const isAdmin = myLevel >= 2
  const isDeptAdmin = myLevel >= 2
  // 是否为租户管理员（等级≥3）
  const isTenantAdmin = myLevel >= 3
  // 是否为超级管理员（等级≥4）
  const isSuper = myLevel >= 4
  // 根据当前角色生成可分配角色选项
  const roleOptions = useMemo(() => {
    if (isSuper) return ['user', 'dept_admin', 'tenant_admin', 'admin']
    if (isTenantAdmin) return ['user', 'dept_admin', 'tenant_admin']
    if (isDeptAdmin) return ['user']
    return []
  }, [isSuper, isTenantAdmin, isDeptAdmin])

  // 加载租户列表；若当前存储的租户 ID 已不存在则回退到平台上下文
  const loadTenants = useCallback(async () => {
    try {
      const r = await apiTenantList()
      if (r.success) {
        const list = (r as unknown as { tenants?: TenantInfo[] }).tenants || []
        setTenants(list)
        // 超管默认平台上下文（0=能言根组织）；仅当存储了已删除的租户 id 时才回退
        const stored = getActiveTenantId()
        if (stored > 0 && !list.some((t) => t.id === stored) && list.length) {
          setActiveTenantId(0)
        }
        setActive(getActiveTenantId() ?? 0)
      }
    } catch { /* 忽略 */ }
  }, [])

  // 仅超管需要租户列表（切换器）；进入后台时拉取一次
  useEffect(() => {
    if (myLevel >= 4) void loadTenants()
  }, [myLevel, loadTenants])

  // 切换当前租户，写入 core 后所有请求自动带 X-Tenant-ID
  const switchTenant = useCallback((tid: number) => {
    setActiveTenantId(tid) // 写入 core（此后所有请求自动带 X-Tenant-ID）
    setActive(tid)
  }, [])

  // 拉取当前生效租户的「邀请好友」开关（超管随 X-Tenant-ID 切换；租户管理员取自身租户）
  const loadInviteEnabled = useCallback(async () => {
    if (!user) return
    try {
      const r = await tenantInviteEnabledGet()
      if (r.success) {
        setInviteEnabled(r.invite_enabled !== false)
        setIsPersonal(r.is_personal === true)
      }
    } catch { /* 忽略 */ }
  }, [user])

  // 清除本地认证 token 与租户上下文
  const clearAuth = useCallback(() => {
    setActiveTenantId(0)
    try {
      localStorage.removeItem('auth_token')
      localStorage.removeItem('active_tenant_id')
    } catch { /* 忽略 */ }
  }, [])

  // 静默加载当前租户组织树（供部门包映射为部门名等使用）
  const loadOrgs = useCallback(async () => {
    try {
      const r: any = await orgList()
      if (r && r.success) {
        const list: OrgInfo[] = r.orgs || []
        setOrgs(list)
        const m = new Map<number, OrgInfo>()
        list.forEach((o) => m.set(o.id, o))
        setOrgMap(m)
      }
    } catch { /* 忽略 */ }
  }, [])

  // 登录后：【非超管】把租户上下文切到本人所属租户（确保组织树/包按租户作用域）；随后静默加载组织树
  useEffect(() => {
    if (!user) return
    if (roleLevel(user.role) < 4 && user.tenant_id && activeTenantId !== user.tenant_id) {
      switchTenant(user.tenant_id)
    }
    void loadOrgs()
  }, [user, activeTenantId, switchTenant, loadOrgs])

  // 登录后/切换租户后刷新「邀请好友」开关（仅租户管理员及以上可读该接口，其余角色跳过免 403）
  useEffect(() => {
    if (user && roleLevel(user.role) >= 3) void loadInviteEnabled()
  }, [user, activeTenantId, loadInviteEnabled])

  // 跨组件跳转入口（Bell → 反馈处理面板）
  const gotoPanel = useCallback((p: PanelKey) => {
    setPanel(p)
    if (window.location.pathname !== '/admin') window.history.pushState({}, '', '/admin')
  }, [])

  // 打开指定反馈详情（消息中心跳转）
  const openFeedback = useCallback((fid: number) => {
    setPendingFeedbackId(fid)
    setPanel('tickets')
    if (window.location.pathname !== '/admin') window.history.pushState({}, '', '/admin')
  }, [])

  // 读取并清空待处理反馈 ID，供工单面板消费
  const consumeFeedback = useCallback(() => {
    const v = pendingFeedbackId
    setPendingFeedbackId(0)
    return v
  }, [pendingFeedbackId])

  // 前进/后退与 /admin 路径保持一致
  useEffect(() => {
    const onPop = () => {
      if (window.location.pathname.startsWith('/admin')) setPanel('overview')
    }
    window.addEventListener('popstate', onPop)
    return () => window.removeEventListener('popstate', onPop)
  }, [])

  // 聚合后台所有状态与方法，缓存后注入 Provider
  const value = useMemo<AdminCtx>(() => ({
    myLevel, isAdmin, isDeptAdmin, isTenantAdmin, isSuper, roleOptions,
    tenants, activeTenantId, switchTenant, loadTenants, clearAuth,
    orgs, orgMap, loadOrgs,
    panel, gotoPanel, pendingFeedbackId, openFeedback, consumeFeedback,
    inviteEnabled, isPersonal, loadInviteEnabled,
  }), [myLevel, isAdmin, isDeptAdmin, isTenantAdmin, isSuper, roleOptions,
        tenants, activeTenantId, switchTenant, loadTenants, clearAuth,
        orgs, orgMap, loadOrgs,
        panel, gotoPanel, pendingFeedbackId, openFeedback, consumeFeedback,
        inviteEnabled, isPersonal, loadInviteEnabled])

  return <Ctx.Provider value={value}>{children}</Ctx.Provider>
}

/** 在函数组件中读取后台管理上下文（必须在 <AdminProvider> 内使用） */
export function useAdmin() { return useContext(Ctx) }
