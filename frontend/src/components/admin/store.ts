// ============================================================================
// components/admin/store.ts — 后台共享状态（组合式函数）
// 职责：跨面板共享的响应式状态——当前用户、生效租户、租户列表、角色等级，
//       以及租户切换/角色计算等通用逻辑。各面板组件从这里取共享状态。
// ============================================================================

import { ref, computed, type Ref } from 'vue'
import { getActiveTenantId, setActiveTenantId, tenantList as apiTenantList, type AuthUser, type TenantInfo } from '@/api'
import { t } from '@/i18n'

// ---- 共享响应式状态 ----
// 当前登录用户（由壳组件注入）
export const user: Ref<AuthUser | null> = ref(null)
// 生效租户 ID（超管切换，租户管理员固定在自身租户）
export const activeTenantId = ref<number>(getActiveTenantId() ?? 0)
// 租户列表（超管可见）
export const tenantList = ref<TenantInfo[]>([])

// 角色等级：普通用户(1) < 部门管理员(2) < 租户管理员(3) < 超级管理员(4)，兼容旧值 approver/admin
export function roleLevel(r?: string): number {
  if (r === 'super_admin' || r === 'admin') return 4
  if (r === 'tenant_admin' || r === 'approver') return 3
  if (r === 'dept_admin') return 2
  return 1
}

// 角色名（后台侧边栏展示，i18n）
export function roleName(r?: string) {
  if (r === 'super_admin' || r === 'admin') return t('users.role.super_admin')
  if (r === 'tenant_admin' || r === 'approver') return t('users.role.tenant_admin')
  if (r === 'dept_admin') return t('users.role.dept_admin')
  return t('users.role.user')
}

// 当前用户角色等级
export const myLevel = computed(() => roleLevel(user.value?.role))

// 是否具备管理权限（部门管理员及以上）
export const isAdmin = computed(() => myLevel.value >= 2)
// 是否超级管理员
export const isSuper = computed(() => myLevel.value >= 4)
// 是否租户管理员（含超管）
export const isTenantAdmin = computed(() => myLevel.value >= 3)
// 是否部门管理员（含以上）
export const isDeptAdmin = computed(() => myLevel.value >= 2)

// 可选角色：超管可分配全部四级；租户管理员可分配 user/dept_admin/tenant_admin；部门管理员仅可分配 user
// （超管角色统一命名 admin，兼容历史 super_admin 值）
export const roleOptions = computed(() => {
  if (isSuper.value) return ['user', 'dept_admin', 'tenant_admin', 'admin']
  if (isTenantAdmin.value) return ['user', 'dept_admin', 'tenant_admin']
  if (isDeptAdmin.value) return ['user']
  return []
})

// ---- 动作 ----
// 切换生效租户：更新全局 X-Tenant-ID 并持久化
export function switchTenant(tid: number) {
  setActiveTenantId(tid)
  activeTenantId.value = tid
}

// 刷新租户列表，并处理超管默认/回退生效租户
export async function loadTenants() {
  const r = await apiTenantList()
  if (r.success) tenantList.value = r.tenants || []
  // 超管默认平台上下文（0=翻译助手根组织）；仅当存储了已删除的租户 id 时才回退
  const stored = getActiveTenantId()
  if (stored > 0 && !tenantList.value.some(t => t.id === stored) && tenantList.value.length) {
    setActiveTenantId(0)
  }
  activeTenantId.value = getActiveTenantId() ?? 0
}

// 退出登录：清空 token（清 token 由壳组件触发 UI 跳转）
export function clearAuth() {
  setActiveTenantId(0)
  localStorage.removeItem('auth_token')
  localStorage.removeItem('active_tenant_id')
}

// ★ 跨面板导航信号：消息中心点击 feedback 类通知 → 跳转问题反馈面板并打开对应详情
export const pendingPanel = ref<string>('')
export const pendingFeedbackId = ref<number>(0)
export function gotoFeedbackPanel(fid: number) {
  pendingPanel.value = 'tickets'
  pendingFeedbackId.value = fid
}
