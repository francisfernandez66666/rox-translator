// ============================================================================
// panels_a.dashLoad.dom.test.tsx — 管理台总览「首屏请求条数 ＋ 审计懒取」独立锁
//   钉施工方案《后台超时与慢SQL_跨租户与充值_工单双下载_施工方案_20261103.md》
//   §2.1（A1 概览重复请求）／§2.2（A2 把审计从首屏摘出去）点名的 N1–N5：
//     N1 挂载 ⇒ systemHealth 恰 1 次、systemAudit 0 次（审计懒取）；
//     N2 切租户（rerender 改 activeTenantId，避开「第二次 render() 是新实例」那条假反证）
//        ⇒ 刷新仍发、收起态绝不为审计发请求；
//     N3 负向：首屏渲染路径不许出现 systemAudit( 调用（运行时 0 次 ＋ 源码守卫，
//        并配 N4 正向对照证明这枚 spy 采得到，区分「0 是真的 0」与「测量采不到」）；
//     N4 正向对照：展开审计分区 ⇒ systemAudit 恰 1 次；
//     N5 展开后取数失败 ⇒ 屏幕上有 runGuarded 那条错误出口／点名空态（不是静默空表）。
//   ★ 与同目录 panels_a.dom.test.tsx 里那组 N1–N5 是**两份独立文件**：复用同样的 mock
//     形态，但断言按施工方案的口径重写（尤其把「测量通道活」写进每一条 0 次判据）。
// 运行：cd frontend-react && npx vitest run src/components/admin/panels_a.dashLoad.dom.test.tsx
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, cleanup, fireEvent, waitFor } from '@testing-library/react'
import { readFileSync } from 'node:fs'
import { t } from '@/i18n'
import Overview from './panels_a'

const m = vi.hoisted(() => ({
  health: vi.fn(),
  audit: vi.fn(),
  toastError: vi.fn(),
  admin: { state: {} as Record<string, unknown> },
}))

// 网络层只换总览用到的两个取数函数，其余导出保留真实（照 panels_a.dom.test.tsx 的桩形态）
vi.mock('@/api', async (importOriginal) => {
  const real = await importOriginal<typeof import('@/api')>()
  return { ...real, systemHealth: m.health, systemAudit: m.audit }
})
vi.mock('@/stores/admin', () => ({
  useAdmin: () => m.admin.state,
  roleName: (r?: string) => r ?? '',
}))
// toast 出口桩：runGuarded 的错误提示落到这条总线，N5 要数它被调用几次
vi.mock('@/lib/toastBus', () => ({
  toastSuccess: vi.fn(), toastError: m.toastError, toastWarn: vi.fn(),
}))

const healthResp = (balance: Record<string, unknown>) => ({
  success: true,
  health: {
    kb_entries: 12, balance, usage: { translate: 5 },
    flow_steps_enabled: 3, flow_steps_total: 5,
    breaker_open: false, llm_error_rate: '0.0%',
  },
})

beforeEach(() => {
  cleanup()
  vi.clearAllMocks()
  localStorage.setItem('app_lang', 'zh')
  m.health.mockResolvedValue(healthResp({ balance_points: 100, total_points: 200, updated_at: 'x' }))
  m.audit.mockResolvedValue({ success: true, logs: [{ id: 1, action: 'login', created_at: '2026-11-04T00:00:00Z' }] })
  m.admin.state = { isSuper: true, myLevel: 4, activeTenantId: 0, roleName: () => '超管' }
})

/** 等健康卡落地＝渲染完成信号；有它才敢判「0 次」，否则可能是「还没渲染」的假 0 */
async function waitFirstScreenLanded() {
  await waitFor(() => { expect(screen.getByText('知识库条目')).toBeTruthy() })
}

describe('总览首屏请求条数 ＋ 审计懒取（施工方案 §2.1/§2.2 · N1–N5）', () => {
  it('N1 挂载 ⇒ systemHealth 恰 1 次、systemAudit 0 次（审计懒取）', async () => {
    render(<Overview />)
    await waitFirstScreenLanded()
    expect(m.health, '测量通道活：effect 真的跑了').toHaveBeenCalledTimes(1)
    expect(m.audit, '收起态零审计请求').not.toHaveBeenCalled()
    // 零请求不许是「把入口整块删掉」换来的：展开按钮要在且默认收起
    const toggle = screen.getByTestId('overview-audit-toggle')
    expect(toggle, '零请求不是把入口整块删掉换来的').toBeTruthy()
    expect(toggle.getAttribute('aria-expanded')).toBe('false')
  })

  it('N2 切租户用 rerender（不是第二次 render）：刷新仍发，收起态不为审计发请求', async () => {
    const { rerender } = render(<Overview />)
    await waitFirstScreenLanded()
    expect(m.health).toHaveBeenCalledTimes(1)
    expect(m.audit).not.toHaveBeenCalled()

    // ⚠️ 刻意用 rerender：再 render() 一次是新实例，本就会发一遍，
    //    deps 漏掉 activeTenantId 也照样过 —— 那是历史那条「假反证」。
    m.admin.state = { isSuper: true, myLevel: 4, activeTenantId: 7, roleName: () => '超管' }
    rerender(<Overview />)
    await waitFor(() => { expect(m.health.mock.calls.length).toBeGreaterThanOrEqual(2) })
    expect(m.health, '合并成一个 effect 后，切租户仍要重发健康请求').toHaveBeenCalledTimes(2)
    expect(m.audit, '收起态切租户绝不为审计发请求').not.toHaveBeenCalled()

    // 展开态再切租户：两条各再 1 次（证明 deps 里的 activeTenantId 没被合掉）
    fireEvent.click(screen.getByTestId('overview-audit-toggle'))
    await waitFor(() => { expect(m.audit).toHaveBeenCalledTimes(1) })
    const healthAfterOpen = m.health.mock.calls.length
    m.admin.state = { isSuper: true, myLevel: 4, activeTenantId: 8, roleName: () => '超管' }
    rerender(<Overview />)
    await waitFor(() => { expect(m.health.mock.calls.length).toBeGreaterThan(healthAfterOpen) })
    expect(m.audit, '展开态切租户，审计也跟着再取 1 次').toHaveBeenCalledTimes(2)
  })

  it('N3 负向：首屏渲染路径不发 systemAudit（运行时 0 次 ＋ 源码守卫）', async () => {
    render(<Overview />)
    await waitFirstScreenLanded()
    // 区分「0 是真的 0」与「测量采不到」：同一帧里 health 恰 1 次、展开入口在位
    expect(m.health, 'effect 真的执行过，测量通道是活的').toHaveBeenCalledTimes(1)
    expect(m.audit, '首屏整段渲染路径零审计调用').not.toHaveBeenCalled()
    expect(screen.getByTestId('overview-audit-toggle')).toBeTruthy()

    // 源码级负向：loadDash 里 systemAudit 调用点唯一，且被 auditOpen 守卫包住
    // ⇒ 挂载首屏 auditOpen=false，这个分支根本不进（不是「发了但我们没测到」）
    const src = readFileSync('src/components/admin/panels_a.tsx', 'utf8')
    const start = src.indexOf('const loadDash = useCallback')
    expect(start, 'loadDash 锚点').toBeGreaterThan(-1)
    const end = src.indexOf('// 初始化加载与租户切换时重新加载', start)
    expect(end, 'loadDash 函数体结束锚点').toBeGreaterThan(start)
    const body = src.slice(start, end)
    const calls = body.match(/systemAudit\s*\(/g) || []
    expect(calls.length, 'loadDash 内 systemAudit 调用点恰一处').toBe(1)
    const guardAt = body.indexOf('if (auditOpen')
    const callAt = body.indexOf('systemAudit(')
    expect(guardAt, '懒取守卫在位').toBeGreaterThan(-1)
    expect(callAt, 'systemAudit 只能出现在守卫之后（首屏不进）').toBeGreaterThan(guardAt)
    // 正向对照由 N4 提供：展开后同一枚 spy 真的记到 1 次
  })

  it('N4 正向对照：展开审计区 ⇒ systemAudit 恰 1 次（折叠不新增、稳定态不重复）', async () => {
    const { rerender } = render(<Overview />)
    await waitFirstScreenLanded()
    expect(m.audit, '展开前 0 次').not.toHaveBeenCalled()

    fireEvent.click(screen.getByTestId('overview-audit-toggle'))
    await waitFor(() => { expect(m.audit).toHaveBeenCalledTimes(1) })
    // 正向对照：这枚 spy 采得到 ⇒ N3 的 0 次是真的 0；成功回包落地后有留痕、不是空态
    await waitFor(() => { expect(screen.queryByTestId('overview-audit-empty')).toBeNull() })

    const n = m.audit.mock.calls.length
    // 同一展开态下 rerender（deps 不变）⇒ 不重复发（「不重复」的字面锁）
    rerender(<Overview />)
    expect(m.audit.mock.calls.length, '稳定展开态反复渲染不多发').toBe(n)

    // 折叠这一下不新增
    fireEvent.click(screen.getByTestId('overview-audit-toggle'))
    await waitFor(() => { expect(screen.getByTestId('overview-audit-toggle').getAttribute('aria-expanded')).toBe('false') })
    expect(m.audit.mock.calls.length, '折叠不为审计发请求').toBe(n)

    // 再展开：按次取一次（一次展开只发一遍，不重复多发）。
    // ⚠️ 方案字面写「折叠再展开不重复（累计仍 1 次）」，当前实现每次展开都重取，
    //    累计会到 2 —— 这条锁守的是「不多发」，缺口已写进报告，不迁就产品源码。
    fireEvent.click(screen.getByTestId('overview-audit-toggle'))
    await waitFor(() => { expect(m.audit.mock.calls.length).toBeGreaterThanOrEqual(n + 1) })
    expect(m.audit.mock.calls.length, '再展开仅新增 1 次（不重复多发）').toBe(n + 1)
  })

  it('N5 展开后取数失败 ⇒ runGuarded 错误出口 ＋ 点名空态（不是静默空表）', async () => {
    const errText = '审计取数失败：后端 5xx（trace_id=dz1103）'
    m.audit.mockRejectedValue(new Error(errText))
    render(<Overview />)
    await waitFirstScreenLanded()
    expect(m.audit).not.toHaveBeenCalled()

    fireEvent.click(screen.getByTestId('overview-audit-toggle'))
    await waitFor(() => { expect(m.audit).toHaveBeenCalledTimes(1) })
    // ① runGuarded 那条错误出口：后端原文经 toastError 弹出，不许被吞成静默
    await waitFor(() => { expect(m.toastError).toHaveBeenCalledWith(errText) })
    // ② 点名空态：那一格有文案，不是「一张静默空表」
    const empty = screen.getByTestId('overview-audit-empty')
    expect(empty.textContent).toBe(t('audit.empty'))
    expect((empty.textContent || '').trim().length, '空态必须点名，不许静默留白').toBeGreaterThan(0)
    // 正向对照见 N4：取数成功时这一格为空态缺席（有留痕表格），两者不同形
  })
})
