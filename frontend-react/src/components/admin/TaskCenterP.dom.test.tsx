// ============================================================================
// TaskCenterP.dom.test.tsx — 任务中心面板组件测试（★ #33 任务系统，2026-09-21）
// 覆盖三条易被改坏的口径：
//   ① 事件自动发放任务（grant_mode=auto）不给领取按钮，改展示本期进度（今日/本周次数）；
//      手工任务仍保留一键领取，且领取走 claimTask（不自动发放）；
//   ② 奖励一律积分口径 + 形态标注：valid_days>0 显示「临时积分 + 有效期 N 天」，
//      valid_days=0 显示「永久积分」；界面不得出现 token 裸值；
//   ③ 「重置积分消耗量」是超管专属特殊任务入口，确认后才调 reset-consumption 接口。
// 运行：npx vitest run src/components/admin/TaskCenterP.dom.test.tsx
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, cleanup, fireEvent } from '@testing-library/react'
import TaskCenterP from './TaskCenterP'
import { DialogHost } from '@/components/uiDialogs'

const mocks = vi.hoisted(() => ({
  isSuper: false,
  myTasks: vi.fn(),
  adminTasks: vi.fn(),
  claimTask: vi.fn(),
  adminTaskSave: vi.fn(),
  adminTaskDelete: vi.fn(),
  adminTaskResetConsumption: vi.fn(),
}))

// 超管视图开关：用例内直接改标志位，避免引入真实登录态
vi.mock('@/stores/admin', () => ({
  useAdmin: () => ({ isSuper: mocks.isSuper }),
}))

vi.mock('@/api/tasks', () => ({
  myTasks: mocks.myTasks,
  adminTasks: mocks.adminTasks,
  claimTask: mocks.claimTask,
  adminTaskSave: mocks.adminTaskSave,
  adminTaskDelete: mocks.adminTaskDelete,
  adminTaskResetConsumption: mocks.adminTaskResetConsumption,
}))

/** 一条自动发放的每日登录任务（+100 临时积分 / 3 天 / 日 ≤1 次） */
const loginDaily = {
  id: 1, task_type: 'daily', task_key: 'login_daily', grant_mode: 'auto', period: 'daily',
  title: '每日登录', description: '', reward_points: 100, valid_days: 3, stack_expiry: 1,
  cap_per_day: 1, cap_per_week: 0, reward_kind: 'temporary', enabled: 1, sort_order: 1,
  claimed: true, claimed_at: '2026-09-21 09:00:00', reward: { today_count: 1, week_count: 2, total_count: 9, last_expiry: '', last_granted: '' },
}

/** 一条手工领取的自定义任务（+200 永久积分） */
const manualOnce = {
  id: 2, task_type: 'once', task_key: '', grant_mode: 'manual', period: 'once',
  title: '意见反馈', description: '提交一次有效反馈', reward_points: 200, valid_days: 0, stack_expiry: 0,
  cap_per_day: 0, cap_per_week: 0, reward_kind: 'permanent', enabled: 1, sort_order: 2,
  claimed: false, claimed_at: '',
}

// ★ 观察5（批G）：task_type='daily' 但 period='weekly' 的混配行——
//   类型标签必须按 period 优先展示「每周」（tasks.periodWeekly 的 zh 值），
//   而不是此前只按 task_type 错标的「每日任务」。设成 auto 发放避免多一枚领取按钮。
const weeklyAuto = {
  id: 3, task_type: 'daily', task_key: '', grant_mode: 'auto', period: 'weekly',
  title: '每周打卡', description: '', reward_points: 150, valid_days: 0, stack_expiry: 0,
  cap_per_day: 0, cap_per_week: 2, reward_kind: 'permanent', enabled: 1, sort_order: 3,
  claimed: true, claimed_at: '2026-09-25 09:00:00', reward: { today_count: 0, week_count: 1, total_count: 1, last_expiry: '', last_granted: '' },
}

beforeEach(() => {
  cleanup()
  vi.clearAllMocks()
  mocks.isSuper = false
  mocks.myTasks.mockResolvedValue({ success: true, tasks: [loginDaily, manualOnce, weeklyAuto] })
  mocks.adminTasks.mockResolvedValue({ success: true, tasks: [loginDaily, manualOnce, weeklyAuto] })
  mocks.claimTask.mockResolvedValue({ success: true, points: 200 })
  mocks.adminTaskResetConsumption.mockResolvedValue({ success: true, tenants: 3, reset_rows: 7 })
})

describe('任务中心 · 自动发放任务（#33）', () => {
  it('自动任务不给领取按钮，展示本期进度；手工任务保留一键领取', async () => {
    render(<TaskCenterP />)
    // 自动任务按日进度展示（今日 1/1 次），不再出现领取入口
    await vi.waitFor(() => { expect(screen.getAllByText(/今日 1\/1/).length).toBeGreaterThan(0) })
    expect(screen.getAllByText(/自动发放/).length).toBeGreaterThan(0)
    // 领取按钮仅手工任务一个：点击后走 claimTask(id=2)
    const claimBtns = screen.getAllByText('领取').filter((el) => el.tagName === 'BUTTON')
    expect(claimBtns.length).toBe(1)
    fireEvent.click(claimBtns[0])
    await vi.waitFor(() => { expect(mocks.claimTask).toHaveBeenCalledWith(2) })
  })

  it('积分口径展示：临时积分带有效期、永久积分不带，且不出现 token 裸值', async () => {
    const { container } = render(<TaskCenterP />)
    await vi.waitFor(() => { expect(screen.getAllByText(/\+100/).length).toBeGreaterThan(0) })
    expect(screen.getAllByText(/临时积分/).length).toBeGreaterThan(0)
    expect(screen.getAllByText(/有效期 3 天/).length).toBeGreaterThan(0)
    expect(screen.getAllByText(/永久积分/).length).toBeGreaterThan(0)
    expect(container.textContent).not.toMatch(/token/i)
    // 300 倍汇率的内部记账值绝不得出现在界面上
    expect(container.textContent).not.toMatch(/30000|60000/)
  })
})

describe('任务中心 · 类型标签 period 优先（★ 观察5 批G）', () => {
  it('task_type=daily 但 period=weekly 的行标签精确等于「每周」（periodWeekly 词条 zh 值），旧错标「每日任务」清零', async () => {
    render(<TaskCenterP />)
    // 等值锁①：混配行标签 = tasks.periodWeekly 的 zh 值「每周」，全页精确计数恰为 1
    await vi.waitFor(() => { expect(screen.getAllByText('每周')).toHaveLength(1) })
    // 等值锁②：纯 daily 行标签 = tasks.periodDaily 的 zh 值「每日」（精确 1，不被子串误伤）
    expect(screen.getAllByText('每日')).toHaveLength(1)
    // 等值锁③：once 行标签 = tasks.periodOnce 的 zh 值「终身一次」
    expect(screen.getAllByText('终身一次')).toHaveLength(1)
    // 反向锁：修复前按 task_type 二值映射的旧标签「每日任务」在列表页不得再出现
    expect(screen.queryByText('每日任务')).toBeNull()
  })
})

describe('任务中心 · 超管特殊任务（#33）', () => {
  it('超管可见「重置积分消耗量」，二次确认后才调接口', async () => {
    mocks.isSuper = true
    // confirmDialog 需要 <DialogHost/> 宿主（与 main.tsx 同样的挂载方式）
    render(<><TaskCenterP /><DialogHost /></>)
    await vi.waitFor(() => { expect(mocks.adminTasks).toHaveBeenCalled() })
    fireEvent.click(screen.getByText('重置积分消耗量'))
    // 确认弹窗文案（有效期不变的口径说明）；未确认前不得发请求
    await vi.waitFor(() => { expect(screen.getByText(/有效期不变/)).toBeTruthy() })
    expect(mocks.adminTaskResetConsumption).not.toHaveBeenCalled()
    fireEvent.click(screen.getByText('确定'))
    await vi.waitFor(() => { expect(mocks.adminTaskResetConsumption).toHaveBeenCalledWith(true) })
    // 重置成功后回刷用户视角任务列表
    await vi.waitFor(() => { expect(mocks.myTasks.mock.calls.length).toBeGreaterThanOrEqual(2) })
  })

  it('普通用户视图不渲染重置入口与任务管理面板', async () => {
    render(<TaskCenterP />)
    await vi.waitFor(() => { expect(screen.getAllByText('任务中心').length).toBeGreaterThan(0) })
    expect(screen.queryByText('重置积分消耗量')).toBeNull()
    expect(screen.queryByText(/任务管理/)).toBeNull()
  })
})
