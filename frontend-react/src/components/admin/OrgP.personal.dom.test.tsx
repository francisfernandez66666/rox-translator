// ============================================================================
// OrgP.personal.dom.test.tsx — 2026-10-23 F4 前端锁：平台树默认收起个人租户
//
// 缺陷背景：ListPlatformOrgs 把每个个人租户的根组织也挂在平台根下，个人注册持续增长
// ⇒ 平台组织树被撑成无限长。后端已按 include_personal 收敛（Go 锁＝orgs_f4_test.go），
// 本文件锁前端两半：
//   ① 默认档不渲染个人租户节点（后端已过滤时不得被前端二次塞回来）；
//   ② 勾选「显示个人租户（N）」后必须真的带 include_personal=1 重拉，并渲染出来。
// 反向对照：企业租户节点在默认档必须在（否则"个人不在"是空读假绿）。
// 运行：npx vitest run src/components/admin/OrgP.personal.dom.test.tsx
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, fireEvent, cleanup } from '@testing-library/react'
import { OrgP } from './OrgP'

/** 可控 mock：orgList 按 includePersonal 返回不同集合（默认档/展开档） */
const M = vi.hoisted(() => ({
  orgList: vi.fn(),
}))

vi.mock('@/api', () => ({
  orgList: M.orgList,
  orgCreate: vi.fn(), orgRename: vi.fn(), orgMove: vi.fn(), orgDelete: vi.fn(),
  orgUsers: vi.fn(async () => ({ success: true, users: [] })),
  orgBudgetSummary: vi.fn(async () => ({ success: true, summary: { depts: [] } })),
  orgTokenLimit: vi.fn(), adminUserCreate: vi.fn(), adminUserDelete: vi.fn(),
  adminUserResetPassword: vi.fn(), adminUserUpdate: vi.fn(),
  userBulkImport: vi.fn(), downloadUserImportTemplate: vi.fn(),
  inviteCodes: vi.fn(), inviteCodeCreate: vi.fn(), tenantSetStatus: vi.fn(),
  request: vi.fn(), authHeaders: () => ({}),
}))

vi.mock('./panels_a', () => ({ InvitesP: () => null, UsersP: () => null }))

vi.mock('@/stores/admin', () => ({
  useAdmin: () => ({
    myLevel: 4, isSuper: true, isPersonal: false,
    tenants: [], activeTenantId: 0, myTenantId: 0,
    roleOptions: ['user', 'dept_admin', 'tenant_admin', 'admin'], loadTenants: () => {},
  }),
}))

/** 平台根组织（视图锚点） */
const PLATFORM_ROOT = { id: 9, tenant_id: 0, parent_id: 0, name: '平台根', type: 'root' }
/** 企业租户根（默认档必须在，作为「个人不在」的正对照） */
const ENT = { id: 20, tenant_id: 2, parent_id: 9, name: 'F4 企业', type: 'root', tenant_is_personal: false }
/** 个人租户根（默认档必须被收起；展开档才出现） */
const PER = { id: 30, tenant_id: 3, parent_id: 9, name: 'F4 个人', type: 'root', tenant_is_personal: true }

beforeEach(() => {
  cleanup()
  vi.clearAllMocks()
  // 后端默认档：个人租户不在返回里；展开档：带上个人租户
  M.orgList.mockImplementation(async (includePersonal?: boolean) => ({
    success: true,
    platform: true,
    tenant_id: 0,
    root: PLATFORM_ROOT,
    personal_count: 1,
    orgs: includePersonal ? [PLATFORM_ROOT, ENT, PER] : [PLATFORM_ROOT, ENT],
  }))
})

describe('OrgP · 平台树个人租户收敛（F4）', () => {
  it('默认档不渲染个人租户、企业租户在；勾选后带 include_personal 重拉并渲染', async () => {
    const { container } = render(<OrgP />)
    await vi.waitFor(() => expect(container.textContent).toContain('F4 企业'))
    // 正对照 + 主判据：企业可见、个人不可见
    expect(container.textContent).toContain('F4 企业')
    expect(container.textContent).not.toContain('F4 个人')
    // 默认调用不带参数（不能一上来就展开）
    expect(M.orgList.mock.calls[0][0]).toBeFalsy()

    const boxes = Array.from(container.querySelectorAll('input[type="checkbox"]')) as HTMLInputElement[]
    const toggle = boxes.find((b) => b.parentElement?.textContent?.includes('显示个人租户'))
    expect(toggle, '未找到「显示个人租户」开关').toBeTruthy()
    expect(toggle!.parentElement?.textContent).toContain('（1）') // personal_count 如实展示

    fireEvent.click(toggle!)
    await vi.waitFor(() => expect(container.textContent).toContain('F4 个人'))
    expect(M.orgList.mock.calls.some((c) => c[0] === true)).toBe(true)
  })
})
