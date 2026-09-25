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
import { render, screen, cleanup } from '@testing-library/react'
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
