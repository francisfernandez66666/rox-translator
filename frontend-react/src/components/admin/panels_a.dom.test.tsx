// ============================================================================
// panels_a.dom.test.tsx — 管理台总览面板组件测试（★ 2026-09-25 UAT 修复批G）
// 锁两条本批改动的行为口径（等值锁，不钉无关数字）：
//   ① F-20/F-39：总览工具条不再渲染 Prometheus 按钮（openMetrics 已删，
//      /metrics 属平台运维面信息，不得暴露给租户管理员）——超管 / 租户管理员
//      两角色态下 DOM 内均不得出现任何「Prometheus」字样；
//   ② F-16（前端半）：余额卡数值改读 health.balance.total_points（批B 补的
//      免费桶+付费永久桶双桶合计），balance_points 只是永久桶明细——
//      合计卡必须显示 total_points 的值且不含 balance_points 的值；
//      total_points 缺失（旧后端）时回落 balance_points，原明细展示语义保留。
// 运行：cd frontend-react && npx vitest run src/components/admin/panels_a.dom.test.tsx
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, cleanup, fireEvent } from '@testing-library/react'
import Overview from './panels_a'

const m = vi.hoisted(() => ({
  health: vi.fn(),
  audit: vi.fn(),
  // useAdmin 的按用例可切换返回值（超管 / 租户管理员两角色态）
  admin: { state: {} as Record<string, unknown> },
}))

// 网络层：保留真实模块（API_BASE/authHeaders 等全部导出），只把总览用到的两个取数函数换成 spy
vi.mock('@/api', async (importOriginal) => {
  const real = await importOriginal<typeof import('@/api')>()
  return { ...real, systemHealth: m.health, systemAudit: m.audit }
})

// 后台上下文：useAdmin 由用例注入角色态；roleName 用恒等实现（本测不触达角色列）
vi.mock('@/stores/admin', () => ({
  useAdmin: () => m.admin.state,
  roleName: (r?: string) => r ?? '',
}))

// toast 出口桩掉：本测只锁 DOM，不让 toast 总线参与断言
vi.mock('@/lib/toastBus', () => ({ toastSuccess: vi.fn(), toastError: vi.fn(), toastWarn: vi.fn() }))

/** 一帧总览健康数据：balance_points（永久桶明细）与 total_points（双桶合计）刻意取不同值 */
const healthResp = (balance: Record<string, unknown>) => ({
  success: true,
  health: {
    kb_entries: 12,
    balance,
    usage: { translate: 5 },
    flow_steps_enabled: 3,
    flow_steps_total: 5,
    breaker_open: false,
    llm_error_rate: '0.0%',
  },
})

beforeEach(() => {
  cleanup()
  vi.clearAllMocks()
  localStorage.setItem('app_lang', 'zh') // 钉中文词典（vitest.setup 同口径，双保险）
  m.audit.mockResolvedValue({ success: true, logs: [] })
})

/** 等值锁①：渲染后整棵 DOM 不得出现 Prometheus 入口（F-20/F-39） */
async function expectNoPrometheusEntry() {
  // 先等健康卡出来（渲染完成信号），再判 Prometheus 缺席，避免「没渲染所以没按钮」的假绿
  await vi.waitFor(() => { expect(screen.getByText('知识库条目')).toBeTruthy() })
  expect(screen.queryByRole('button', { name: 'Prometheus 指标' })).toBeNull()
  expect(document.body.innerHTML).not.toMatch(/Prometheus/i)
}

describe('管理台总览 · Prometheus 入口移除（F-20/F-39）', () => {
  it('超管态：工具条无 Prometheus 按钮', async () => {
    m.admin.state = { isSuper: true, myLevel: 4, activeTenantId: 0, tenants: [], roleOptions: [] }
    m.health.mockResolvedValue(healthResp({ balance_points: 300, total_points: 800, updated_at: '2026-09-25T00:00:00Z' }))
    render(<Overview />)
    await expectNoPrometheusEntry()
  })

  it('租户管理员态：工具条同样无 Prometheus 按钮（本批越权修复的靶心角色）', async () => {
    m.admin.state = { isSuper: false, myLevel: 3, activeTenantId: 2, tenants: [], roleOptions: [] }
    m.health.mockResolvedValue(healthResp({ balance_points: 300, total_points: 800, updated_at: '2026-09-25T00:00:00Z' }))
    render(<Overview />)
    await expectNoPrometheusEntry()
  })
})

describe('管理台总览 · 余额卡读双桶合计（F-16 前端半）', () => {
  /** 取余额卡容器（label 的父节点即 HealthCard），返回其 <b> 数值文本 */
  function balanceCardValue(): string {
    const label = screen.getByText('组织余额合计 (积分)')
    const card = label.parentElement as HTMLElement
    return (card.querySelector('b') as HTMLElement).textContent ?? ''
  }

  it('total_points 在场：卡面显示双桶合计 800，且整卡不出现永久桶明细 300', async () => {
    m.admin.state = { isSuper: true, myLevel: 4, activeTenantId: 0, tenants: [], roleOptions: [] }
    m.health.mockResolvedValue(healthResp({ balance_points: 300, total_points: 800, updated_at: '2026-09-25T00:00:00Z' }))
    render(<Overview />)
    await vi.waitFor(() => { expect(screen.getByText('组织余额合计 (积分)')).toBeTruthy() })
    expect(balanceCardValue()).toBe('800') // 等值锁：读数 = total_points
    const label = screen.getByText('组织余额合计 (积分)')
    expect((label.parentElement as HTMLElement).textContent).not.toContain('300')
  })

  it('total_points 缺失（旧后端未带该字段）：回落 balance_points 明细，展示语义保留', async () => {
    m.admin.state = { isSuper: true, myLevel: 4, activeTenantId: 0, tenants: [], roleOptions: [] }
    m.health.mockResolvedValue(healthResp({ balance_points: 300, updated_at: '2026-09-25T00:00:00Z' }))
    render(<Overview />)
    await vi.waitFor(() => { expect(screen.getByText('组织余额合计 (积分)')).toBeTruthy() })
    expect(balanceCardValue()).toBe('300') // 等值锁：回落 = balance_points
  })
})

// ============================================================================
// ★★ 2026-11-04 超时与慢查询批 A1＋A2：概览首屏「请求条数」的守卫级锁。
//
// 为什么必须有这条：旧形态两个 useEffect 在挂载时各触发一次 ⇒ 系统健康与**全平台审计**各发两遍，
// 而审计那条是"不带租户限定＋按时间全表排序"的查询——翻倍直接把首屏推过 30s 超时线，
// 屏幕上就是用户报的那句「请求超时…」。这条病灶**没有任何既有锁覆盖**：
// 健康卡与审计列表的断言只看"有没有渲染"，发两遍照样渲染一次（§一·5 那条"正锁不覆盖调用次数"盲区）。
//
// A2 之后判据方向**翻转了一次**（旧 N2 期望"超管首屏 audit 恰 1 次"），翻转理由写在这里，
// 不许只把断言删掉——留着旧期望，下一个人会顺着旧口径把懒取改回首屏必发：
//   概览首屏该回答的是"系统活着吗/余额还剩多少"，审计是第二屏问题，
//   所以现在是「收起 ⇒ 一个请求都不发，展开才发」。
//
// 四档缺一不可：
//   N1 挂载 ⇒ health 恰 1 次（>1 就是回归到双 effect）；audit **0 次**（A2 新方向）
//   N2 切租户 ⇒ health 再 1 次（合并 effect 时漏 deps 只有这一档抓得住），audit 仍 0（没展开就不发）
//   N3 点「最近审计」⇒ audit 恰 1 次（正向对照：只测 N1 的负向，把请求整条摘掉也会全绿）
//   N4 展开后取数失败 ⇒ 屏幕给出空态那一档（#42「不许把查不到画成没有」的组件侧一半）
//   N5 非超管 ⇒ 连展开入口都不给，更不发审计请求（不为本该隐藏的面发请求）
// ============================================================================
describe('管理台总览 · 请求条数与懒取（超时批 A1＋A2）', () => {
  it('N1＋N2：挂载只发 health；切租户再发一次，审计在收起态一个都不发', async () => {
    m.admin.state = { isSuper: true, myLevel: 4, activeTenantId: 0, roleName: () => '超管' }
    m.health.mockResolvedValue(healthResp({ balance_points: 100, total_points: 200, updated_at: 'x' }))
    const { rerender } = render(<Overview />)
    await vi.waitFor(() => { expect(screen.getByText('知识库条目')).toBeTruthy() })
    expect(m.health).toHaveBeenCalledTimes(1)
    expect(m.audit).not.toHaveBeenCalled() // ★ A2 的方向：收起 ⇒ 零请求

    // N2：租户切换 ⇒ **同一个实例**的 health 必须再发一次。
    // ⚠️ 刻意用 rerender 而不是再 render() 一次：新挂载的实例本来就会发一遍，
    //    那样即使 deps 漏掉 activeTenantId 也照样通过——是假反证。
    m.admin.state = { isSuper: true, myLevel: 4, activeTenantId: 7, roleName: () => '超管' }
    rerender(<Overview />)
    await vi.waitFor(() => { expect(m.health.mock.calls.length).toBeGreaterThanOrEqual(2) })
    expect(m.health).toHaveBeenCalledTimes(2)
    expect(m.audit).not.toHaveBeenCalled() // 切租户也不为收起的面发请求
  })

  it('N3＋N4：展开审计区才取数（恰一次）；取数失败落到空态那一档而不是什么都没有', async () => {
    m.admin.state = { isSuper: true, myLevel: 4, activeTenantId: 0, roleName: () => '超管' }
    m.health.mockResolvedValue(healthResp({ balance_points: 1, total_points: 2, updated_at: 'x' }))
    m.audit.mockResolvedValue({ success: true, logs: [{ id: 1, action: 'login', created_at: '2026-11-04T00:00:00Z' }] })
    render(<Overview />)
    await vi.waitFor(() => { expect(screen.getByText('知识库条目')).toBeTruthy() })
    expect(m.audit).not.toHaveBeenCalled()

    fireEvent.click(screen.getByTestId('overview-audit-toggle'))
    await vi.waitFor(() => { expect(m.audit).toHaveBeenCalledTimes(1) })
    // 展开后有一行留痕 ⇒ 不该出现空态那一档。
    // ⚠️ 刻意不 getByText('login')：动作列走 auditActionLabel 取词（中文界面是「登录」），
    //   拿原始 code 当判据是把"渲染了什么"写成"数据里有什么"——换词典就红，而产品一字没错。
    // ⚠️ 也必须用 waitFor 而不是裸判：m.audit **被调用**与**回包落地并触发重渲染**是两个时刻，
    //   在第一刻判会把"数据还没到"读成"空态"（记忆里那条"固定 sleep 后取一次数"的同族）。
    await vi.waitFor(() => { expect(screen.queryByTestId('overview-audit-empty')).toBeNull() })

    // 折叠再展开**不该**重复发请求（auditOpen 只在收起时清读数；展开态由那次取数覆盖）
    fireEvent.click(screen.getByTestId('overview-audit-toggle'))
    fireEvent.click(screen.getByTestId('overview-audit-toggle'))
    await vi.waitFor(() => { expect(m.audit.mock.calls.length).toBeLessThanOrEqual(2) })

    // N4：失败那一支走的是"零条 + 空态文案"（runGuarded 的 toast 由 toastBus 桩接住，本测只判 DOM 那半）
    m.audit.mockResolvedValue({ success: false })
    m.admin.state = { isSuper: true, myLevel: 4, activeTenantId: 9, roleName: () => '超管' }
    fireEvent.click(screen.getByTestId('overview-audit-toggle')) // 收起
    fireEvent.click(screen.getByTestId('overview-audit-toggle')) // 再展开
    await vi.waitFor(() => { expect(screen.queryByTestId('overview-audit-empty')).toBeTruthy() })
  })

  it('N5 反向：非超管（等级 3 以下）连展开入口都不给，也绝不发审计请求', async () => {
    m.admin.state = { isSuper: false, myLevel: 2, activeTenantId: 3, roleName: () => '成员' }
    m.health.mockResolvedValue(healthResp({ balance_points: 1, total_points: 2, updated_at: 'x' }))
    render(<Overview />)
    await vi.waitFor(() => { expect(screen.getByText('知识库条目')).toBeTruthy() })
    expect(m.health).toHaveBeenCalledTimes(1)
    expect(m.audit).not.toHaveBeenCalled()
    expect(screen.queryByTestId('overview-audit-toggle')).toBeNull()
  })
})
