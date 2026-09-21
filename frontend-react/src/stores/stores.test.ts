// ============================================================================
// stores/stores.test.ts — 全局状态 store 行为回归（★ §4.2-11 盲区补齐，node 环境）
// 覆盖 useAuthStore（登录态/角色等级）与 useAdminStore（租户上下文/组织派生/反馈跳转）
// 里「改动最容易炸、且没有断言兜底」的字段：登录 token 复位、X-Tenant-ID 生效租户、
//   历史活跃租户越权回落、orgMap 派生、consumeFeedback 一次性消费。
// 刻意走订阅/派生断言而非快照——快照一改文案就假红，也测不出「订阅没触发/派生算错」。
// 运行：npx vitest run src/stores/stores.test.ts
// ============================================================================
import { beforeEach, describe, expect, it, vi } from 'vitest'

// stores/admin / stores/auth 经 '@/api' 桶引到 core（顶层读 sessionStorage，setup 已备）；
// 用 importOriginal 保留 core 原语（setAuthToken/authHeaders/setActiveTenantId…），
// 只把网络函数换成 spy——整层抽空会让 store 初始化即崩。
vi.mock('@/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api')>()
  return { ...actual, tenantList: vi.fn(), orgList: vi.fn(), tenantInviteEnabledGet: vi.fn() }
})
vi.mock('@/api/org', () => ({ orgList: vi.fn() }))

import { getAuthToken, setAuthToken, setActiveTenantId, getActiveTenantId, authHeaders } from '@/api'
import { orgList } from '@/api/org'
import { useAuthStore, roleLevel } from '@/stores/auth'
import { useAdminStore, roleName } from '@/stores/admin'
import { tenantList as apiTenantList } from '@/api'
import type { AuthUser } from '@/api'

const sampleUser = { id: 9, role: 'tenant_admin', tenant_id: 3 } as AuthUser

beforeEach(() => {
  // 复位两个 store 到干净初值，避免用例串状态
  useAuthStore.setState({ user: null, restoring: false })
  useAdminStore.setState({ tenants: [], orgs: [], orgMap: new Map(), activeTenantId: 0, pendingFeedbackId: 0 })
  setActiveTenantId(0)
  setAuthToken('')
})

describe('stores/auth · 登录态与角色等级', () => {
  it('roleLevel 四级映射：super/admin=4 · tenant_admin/approver=3 · dept_admin=2 · 其余=1', () => {
    // 改坏后果：门控比较（>=3 才见计费）错档，越权菜单泄露或合法管理员被挡在门外
    expect(roleLevel('super_admin')).toBe(4)
    expect(roleLevel('admin')).toBe(4)
    expect(roleLevel('tenant_admin')).toBe(3)
    expect(roleLevel('approver')).toBe(3)
    expect(roleLevel('dept_admin')).toBe(2)
    expect(roleLevel('user')).toBe(1)
    expect(roleLevel(undefined)).toBe(1)
    expect(roleLevel('随便什么')).toBe(1)
  })

  it('roleName 命中本地化标签而非原始键串（未知角色回落普通用户）', () => {
    expect(roleName('super_admin')).not.toBe('users.role.super_admin')
    expect(roleName('nope')).toBe(roleName('user'))
  })

  it('onLogin 写入 user 并复位 restoring；logout 同时清内存 token 与登录态', () => {
    setAuthToken('tk-keep')
    useAuthStore.setState({ restoring: true })
    useAuthStore.getState().onLogin(sampleUser)
    expect(useAuthStore.getState().user).toMatchObject({ id: 9 })
    expect(useAuthStore.getState().restoring, '登录成功即退出恢复中态').toBe(false)

    useAuthStore.getState().logout()
    expect(useAuthStore.getState().user).toBeNull()
    expect(getAuthToken(), 'logout 必须清 core token，否则下次请求仍带旧凭证').toBe('')
  })

  it('订阅回归：logout 触发一次订阅、且新值可读到 user=null', () => {
    setAuthToken('tk-sub')
    useAuthStore.getState().onLogin(sampleUser)
    const seen: Array<AuthUser | null> = []
    const unsub = useAuthStore.subscribe((s) => seen.push(s.user))
    useAuthStore.getState().logout()
    unsub()
    expect(seen, '状态字段变更后订阅必须收到新切片').toEqual([null])
  })
})

describe('stores/admin · 租户上下文与派生', () => {
  it('switchTenant 写入 core（后续请求自动带 X-Tenant-ID）并同步 store.activeTenantId', () => {
    useAdminStore.getState().switchTenant(42)
    expect(getActiveTenantId()).toBe(42)
    expect(useAdminStore.getState().activeTenantId).toBe(42)
    expect(authHeaders()['X-Tenant-ID'], '切换租户后认证头必须立刻带上，否则打错租户库').toBe('42')
  })

  it('loadTenants：历史活跃租户不在返回列表时归零（防误挂已失效租户 → 前台命中错库）', async () => {
    setActiveTenantId(999) // 模拟 sessionStorage 残留的历史租户
    useAdminStore.setState({ activeTenantId: 999 })
    vi.mocked(apiTenantList).mockResolvedValue({ success: true, tenants: [{ id: 1 }, { id: 2 }] } as never)
    await useAdminStore.getState().loadTenants()
    expect(getActiveTenantId(), '失效租户必须被清 0，而非继续带在请求头').toBe(0)
    expect(useAdminStore.getState().activeTenantId).toBe(0)
    expect(useAdminStore.getState().tenants).toHaveLength(2)
  })

  it('loadTenants：活跃租户仍在列表内则保留（正常切换不被误清）', async () => {
    setActiveTenantId(2)
    vi.mocked(apiTenantList).mockResolvedValue({ success: true, tenants: [{ id: 1 }, { id: 2 }] } as never)
    await useAdminStore.getState().loadTenants()
    expect(getActiveTenantId()).toBe(2)
  })

  it('loadOrgs：orgMap 按 id 派生（列表→映射，供组织归属回填）', async () => {
    const orgs = [{ id: 5, name: '研发' }, { id: 7, name: '市场' }]
    vi.mocked(orgList).mockResolvedValue({ success: true, orgs } as never)
    await useAdminStore.getState().loadOrgs()
    const st = useAdminStore.getState()
    expect(st.orgs).toHaveLength(2)
    expect(st.orgMap.get(5)).toMatchObject({ name: '研发' })
    expect(st.orgMap.get(7)).toMatchObject({ name: '市场' })
  })

  it('consumeFeedback：一次性消费——返回当前 pending 后复位为 0，二次消费拿不到旧值', () => {
    useAdminStore.setState({ pendingFeedbackId: 0 })
    useAdminStore.getState().consumeFeedback() // 复位无副作用
    useAdminStore.setState({ pendingFeedbackId: 77 })
    expect(useAdminStore.getState().consumeFeedback()).toBe(77)
    expect(useAdminStore.getState().pendingFeedbackId).toBe(0)
    expect(useAdminStore.getState().consumeFeedback(), '通知跳转只消费一次，避免重复弹面板').toBe(0)
  })
})
