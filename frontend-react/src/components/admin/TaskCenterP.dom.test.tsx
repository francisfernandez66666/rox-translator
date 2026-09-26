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

// ============================================================================
// ★ F-60（2026-09-26 〇-U 批 I-8）：周期两列收敛为单口径后，界面必须逐枚不变。
//   后端把 task_type 订正成与 period 恒等（周任务由 'daily' 变 'weekly'）之后，
//   展示层若还按 task_type 判色，这一枚胶囊会凭空从 idle 灰翻成 warn 黄——
//   纯数据订正带出的无色差变更是交付稿没授权的（AGENTS §一·5 等值锁口径）。
//   三条锁：
//   ① 对照锁（等值）：脏配行（daily+weekly）与订正行（weekly+weekly）渲染出的
//      类型标签与状态点颜色**逐字相同**，且订正行仍精确回「每周」、「每日任务」旧错标清零；
//   ② 反向自检：once 行的状态点颜色与 weekly 行**必须不同**——
//      否则 ① 就是「所有点同色」造成的假绿；
//   ③ 表单读写同源：编辑订正后的 auto 周任务时，旧的「每日任务/一次性任务」开关必须禁用
//      （该列对 auto 行已无后端语义），计数周期选择器接管；保存回写的 payload 两列同值。
// ============================================================================

/** 订正后的周任务（后端保证 task_type ≡ period） */
const weeklyAutoFixed = { ...weeklyAuto, task_type: 'weekly' }

/** 取某枚类型胶囊的状态点颜色（inline style 上的 var(--lc-*) 档位，不写死十六进制） */
function pillDotColor(label: string): string {
  const pill = screen.getByText(label)
  const dot = pill.querySelector('.lc-pill__dot') as HTMLElement | null
  expect(dot, `标签为「${label}」的胶囊应带状态点`).toBeTruthy()
  return String(dot?.style.background)
}

describe('任务中心 · 周期两列订正不带色差（★ F-60）', () => {
  it('脏配行与订正行的标签/状态点逐字相同；once 行点位颜色不同（反向自检）', async () => {
    // 第一遍：历史脏配（task_type='daily' + period='weekly'）
    mocks.myTasks.mockResolvedValue({ success: true, tasks: [loginDaily, manualOnce, weeklyAuto] })
    render(<TaskCenterP />)
    await vi.waitFor(() => { expect(screen.getAllByText('每周')).toHaveLength(1) })
    const legacyWeekly = pillDotColor('每周')
    const legacyDaily = pillDotColor('每日')
    cleanup()
    // 第二遍：订正后（task_type='weekly' ≡ period）
    mocks.myTasks.mockResolvedValue({ success: true, tasks: [loginDaily, manualOnce, weeklyAutoFixed] })
    render(<TaskCenterP />)
    await vi.waitFor(() => { expect(screen.getAllByText('每周')).toHaveLength(1) })
    // 等值锁：标签计数与配色逐枚相同（订正只收口口径，不带来任何视觉变更）
    expect(screen.getAllByText('每日')).toHaveLength(1)
    expect(screen.getAllByText('终身一次')).toHaveLength(1)
    expect(screen.queryByText('每日任务')).toBeNull()
    expect(pillDotColor('每周')).toBe(legacyWeekly)
    expect(pillDotColor('每日')).toBe(legacyDaily)
    // 反向自检：weekly 档与 once 档必须不同色，否则上面两条等值锁是「全同色」假绿
    expect(pillDotColor('每周')).not.toBe(pillDotColor('终身一次'))
  })

  it('编辑订正后的 auto 周任务：旧类型开关禁用、周期选择器接管，保存 payload 两列同值', async () => {
    mocks.isSuper = true
    mocks.myTasks.mockResolvedValue({ success: true, tasks: [weeklyAutoFixed] })
    mocks.adminTasks.mockResolvedValue({ success: true, tasks: [weeklyAutoFixed] })
    mocks.adminTaskSave.mockResolvedValue({ success: true, id: 3 })
    render(<TaskCenterP />)
    // 等超管表格真正渲染出来（adminTasks 被调用 ≠ 行已上屏）
    const editBtn = await vi.waitFor(() => {
      const el = screen.getByText('编辑任务')
      expect(el).toBeTruthy()
      return el
    })
    fireEvent.click(editBtn)
    await vi.waitFor(() => { expect(screen.getByText('任务类型')).toBeTruthy() })
    const dailyBtn = screen.getByText('每日任务').closest('button') as HTMLButtonElement
    const onceBtn = screen.getByText('一次性任务').closest('button') as HTMLButtonElement
    // 等值锁：auto 行两枚旧开关一律 disabled（点了没后端语义的钮不得留给超管按）
    expect(dailyBtn.disabled).toBe(true)
    expect(onceBtn.disabled).toBe(true)
    // 保存：task_type 原样回传，后端归一后与 period 恒等（这里锁前端不把别名窄化回 daily）
    fireEvent.click(screen.getByText('保存任务'))
    await vi.waitFor(() => { expect(mocks.adminTaskSave).toHaveBeenCalledTimes(1) })
    const payload = mocks.adminTaskSave.mock.calls[0][0] as Record<string, unknown>
    expect(payload.task_type).toBe('weekly')
    expect(payload.period).toBe('weekly')
    expect(payload.grant_mode).toBe('auto')
  })

  it('手工行仍保留可用的类型开关（防「auto 禁用」被整排套用，把手工任务的唯一判据钮做没）', async () => {
    mocks.isSuper = true
    mocks.myTasks.mockResolvedValue({ success: true, tasks: [manualOnce] })
    mocks.adminTasks.mockResolvedValue({ success: true, tasks: [manualOnce] })
    render(<TaskCenterP />)
    // 等超管表格真正渲染出来（adminTasks 被调用 ≠ 行已上屏）
    const editBtn = await vi.waitFor(() => {
      const el = screen.getByText('编辑任务')
      expect(el).toBeTruthy()
      return el
    })
    fireEvent.click(editBtn)
    await vi.waitFor(() => { expect(screen.getByText('任务类型')).toBeTruthy() })
    const dailyBtn = screen.getByText('每日任务').closest('button') as HTMLButtonElement
    const onceBtn = screen.getByText('一次性任务').closest('button') as HTMLButtonElement
    expect(dailyBtn.disabled).toBe(false)
    expect(onceBtn.disabled).toBe(false)
    // 点「每日任务」即改判据（手工行的真值就是这一列），点完仍是可用态
    fireEvent.click(dailyBtn)
    expect((screen.getByText('每日任务').closest('button') as HTMLButtonElement).disabled).toBe(false)
  })
})
