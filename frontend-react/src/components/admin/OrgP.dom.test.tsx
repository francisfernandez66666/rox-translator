// ============================================================================
// OrgP.dom.test.tsx — 组织架构面板「新建用户」提交载荷类型锁（★ F-22 批G）
// 缺陷背景：归属组织下拉的 onChange 把 e.target.value（字符串）直接存进 nuOrgId state，
//   建号提交的 JSON 里 org_id 变成 "7" 字符串形态，后端严格类型校验直接拒单。
// 本用例锁两向：
//   ① 正向：选组织后提交，adminUserCreate 载荷的 org_id 必须是 number 且精确等于 7；
//   ② 反向：序列化后的载荷里不得出现字符串形态的 org_id 值（"org_id":" 前串）。
//   反向锁的判据串用拼接构造，避免测试文件自身源码里出现可被误匹配的完整字面量。
// 运行：npx vitest run src/components/admin/OrgP.dom.test.tsx
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, fireEvent, cleanup } from '@testing-library/react'
import { OrgP } from './OrgP'

const mocks = vi.hoisted(() => ({
  orgList: vi.fn(),
  orgCreate: vi.fn(),
  orgUsers: vi.fn(),
  orgBudgetSummary: vi.fn(),
  adminUserCreate: vi.fn(),
}))

// ---- '@/api' mock：只提供 OrgP 具名导入的运行时最小集（type 导入编译期擦除） ----
vi.mock('@/api', () => ({
  orgList: mocks.orgList,
  orgCreate: mocks.orgCreate,
  orgRename: vi.fn(),
  orgMove: vi.fn(),
  orgDelete: vi.fn(),
  orgUsers: mocks.orgUsers,
  orgBudgetSummary: mocks.orgBudgetSummary,
  orgTokenLimit: vi.fn(),
  adminUserCreate: mocks.adminUserCreate,
  adminUserDelete: vi.fn(),
  adminUserResetPassword: vi.fn(),
  userBulkImport: vi.fn(),
  downloadUserImportTemplate: vi.fn(),
  inviteCodes: vi.fn(),
  inviteCodeCreate: vi.fn(),
  tenantSetStatus: vi.fn(),
  request: vi.fn(),
  authHeaders: () => ({}),
}))

// ---- 子 tab 面板（邀请码/成员账户）打桩：本用例只跑「组织架构」tab，不拉起它们的依赖 ----
vi.mock('./panels_a', () => ({
  InvitesP: () => null,
  UsersP: () => null,
}))

// ---- 登录态：租户管理员（myLevel=3，非超管），单租户上下文 ----
vi.mock('@/stores/admin', () => ({
  useAdmin: () => ({
    myLevel: 3, isSuper: false, tenants: [], activeTenantId: 1,
    roleOptions: ['user', 'dept_admin', 'tenant_admin'], loadTenants: () => {},
  }),
}))

beforeEach(() => {
  cleanup()
  vi.clearAllMocks()
  // 一棵「研发部」部门树（id=7），成员列表为空
  mocks.orgList.mockResolvedValue({ success: true, orgs: [{ id: 7, tenant_id: 1, parent_id: 0, name: '研发部', type: 'dept' }] })
  mocks.orgUsers.mockResolvedValue({ success: true, users: [] })
  mocks.orgBudgetSummary.mockResolvedValue({ success: true, summary: { depts: [] } })
  mocks.orgCreate.mockResolvedValue({ success: true })
  mocks.adminUserCreate.mockResolvedValue({ success: true })
})

describe('组织架构 · 新建用户 org_id 类型（★ F-22）', () => {
  it('归属组织下拉选中部门后提交，adminUserCreate 载荷 org_id 为 number 且无字符串形态', async () => {
    const { container } = render(<OrgP />)
    // 等组织树异步落定（标题/下拉选项都依赖 orgList 回包）
    await vi.waitFor(() => { expect(container.textContent).toContain('研发部') })

    const inputs = Array.from(container.querySelectorAll('input')) as HTMLInputElement[]
    const userInput = inputs.find((i) => i.placeholder === '用户名')!
    const pwdInput = inputs.find((i) => i.placeholder === '初始密码（≥6位）')!
    fireEvent.change(userInput, { target: { value: 'qa_newbie' } })
    fireEvent.change(pwdInput, { target: { value: 'passw0rd' } })

    // 定位「归属部门」下拉：其首个 option 是 org.rootOption「在租户根下新建组织」，
    // 与组织创建父级下拉（首项走 rootOptionTpl 带租户根名）天然区分。
    const selects = Array.from(container.querySelectorAll('select')) as HTMLSelectElement[]
    const nuOrgSel = selects.find((s) => s.options[0]?.textContent?.trim() === '在租户根下新建组织')
    expect(nuOrgSel).toBeTruthy()

    // <select> 事件值天然是字符串 '7'——修复点即在这里被归一成 number
    fireEvent.change(nuOrgSel!, { target: { value: '7' } })
    // 正向控制：级联标题回显「归属：研发部」，证明确实选中、state 已更新
    await vi.waitFor(() => { expect(container.textContent).toContain('开通用户（归属：研发部）') })

    const addBtn = Array.from(container.querySelectorAll('button')).find((b) => b.textContent === '开通用户')!
    fireEvent.click(addBtn)
    await vi.waitFor(() => { expect(mocks.adminUserCreate).toHaveBeenCalledTimes(1) })

    const payload = mocks.adminUserCreate.mock.calls[0][0]
    // 等值锁①：org_id 是 number 且精确为 7
    expect(typeof payload.org_id).toBe('number')
    expect(payload.org_id).toBe(7)
    // 反向锁②：序列化载荷不得出现字符串形态 org_id。判据串拼接构造
    //（'"org_id":' + '"' → "org_id":"），避免本文件源码里出现完整字面量自伤匹配。
    const strOrgIdProbe = '"org_id":' + '"'
    const json = JSON.stringify(payload)
    expect(json).not.toContain(strOrgIdProbe)
    // 同串正向确认：number 形态的 "org_id":7 必须在（防空载荷/字段丢失的假绿）
    expect(json).toContain('"org_id":7')
  })
})
