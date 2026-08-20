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
export const activeTenantId = ref<number>(getActiveTenantId() || 1)
// 租户列表（超管可见）
export const tenantList = ref<TenantInfo[]>([])

// 角色等级：普通用户(1) < 租户管理员(2) < 超级管理员(3)，兼容旧值 approver/admin
export function roleLevel(r?: string): number {
  if (r === 'super_admin' || r === 'admin') return 3
  if (r === 'tenant_admin' || r === 'approver') return 2
  return 1
}

// 角色名（后台侧边栏展示，i18n）
export function roleName(r?: string) {
  if (r === 'super_admin' || r === 'admin') return t('users.role.super_admin')
  if (r === 'tenant_admin' || r === 'approver') return t('users.role.tenant_admin')
  return t('users.role.user')
}

// 当前用户角色等级
export const myLevel = computed(() => roleLevel(user.value?.role))

// 是否具备管理权限（租户管理员及以上）
export const isAdmin = computed(() => myLevel.value >= 2)
// 是否超级管理员
export const isSuper = computed(() => myLevel.value >= 3)

// 可选角色：超管可分配全部三级；租户管理员只能分配普通用户/租户管理员（防提权）
export const roleOptions = computed(() =>
  isSuper.value ? ['user', 'tenant_admin', 'super_admin'] : ['user', 'tenant_admin']
)

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
  // 超管首次进入：默认生效第一个租户
  if (isSuper.value && activeTenantId.value === 1 && !localStorage.getItem('active_tenant_id')) {
    if (tenantList.value.length) setActiveTenantId(tenantList.value[0].id)
  }
  // 若已选租户被删除，回退到第一个可用租户
  if (!tenantList.value.some(t => t.id === activeTenantId.value) && tenantList.value.length) {
    setActiveTenantId(tenantList.value[0].id)
  }
  activeTenantId.value = getActiveTenantId() || 1
}

// 退出登录：清空 token（清 token 由壳组件触发 UI 跳转）
export function clearAuth() {
  setActiveTenantId(0)
  localStorage.removeItem('auth_token')
  localStorage.removeItem('active_tenant_id')
}