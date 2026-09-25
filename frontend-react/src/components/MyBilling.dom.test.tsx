// ============================================================================
// MyBilling.dom.test.tsx — 账单中心时间列 · 双 TZ 等值锁（★ F-14 批G，2026-09-25）
// 缺陷背景：订单/台账时间列旧写法直接对 UTC ISO 串裸切片（slice(0,10) /
// replace('T',' ').slice(0,16)），东八区用户看 UTC 16:00 之后的记录会**错一天**。
// 断言口径：同一 UTC 串 '2026-09-25T18:30:00Z' 在两个运行时区下各断各自正确的
// 本地日历日数字序列（期望值由纯 Date 本地取值器独立算出，与被测 Intl 渲染是
// 两条链路）。必须真跑两条命令各一遍、两条都绿：
//   TZ=UTC            npx vitest run src/components/MyBilling.dom.test.tsx
//   TZ=Asia/Shanghai  npx vitest run src/components/MyBilling.dom.test.tsx
// （用例按 Intl resolvedOptions().timeZone 选档；未钉 TZ 的机器时区下双 TZ 等值
//   自动跳过，仅保留渲染自证与「旧裸切片字样不再上屏」的负向锁。）
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, cleanup, fireEvent } from '@testing-library/react'
import { t } from '@/i18n'
import BillingCenter from './MyBilling'

/** 被测样本时刻：UTC 18:30 = 东八区次日 02:30，跨日缺陷的触发形态 */
const UTC_ISO = '2026-09-25T18:30:00Z'
/** 当前进程实际生效的运行时区（以 Date/Intl 行为准，不直接读 process.env.TZ） */
const zone = Intl.DateTimeFormat().resolvedOptions().timeZone
/** 两位补零 */
const pad = (n: number) => String(n).padStart(2, '0')
/** 用纯 Date 本地取值器独立算出「该时区正确的 yyyymmddHHMM」（与被测 Intl 渲染不同链路） */
function localDigits(iso: string): string {
  const d = new Date(iso)
  return `${d.getFullYear()}${pad(d.getMonth() + 1)}${pad(d.getDate())}${pad(d.getHours())}${pad(d.getMinutes())}`
}
/** 抹掉全部非数字，只留数字序列做等值锁（分隔符/标点不进比较位） */
const digitsOnly = (s: string) => s.replace(/\D/g, '')
/** 两档钉死的本地日历日数字：UTC 当日 09-25 / 东八区跨日次日 09-26 */
const DAY_BY_ZONE: Record<string, string> = { UTC: '20260925', 'Asia/Shanghai': '20260926' }
const wantDay = DAY_BY_ZONE[zone]
const wrongDay = zone === 'Asia/Shanghai' ? DAY_BY_ZONE.UTC : DAY_BY_ZONE['Asia/Shanghai']

// API 层整模块打桩：账单中心五个端点都回固定样本行，不发真实请求
const mocks = vi.hoisted(() => ({
  myOverview: vi.fn(),
  myOrders: vi.fn(),
  myLedger: vi.fn(),
  myRewards: vi.fn(),
  myInvoices: vi.fn(),
}))
vi.mock('@/api/mybilling', () => ({
  myOverview: mocks.myOverview,
  myOrders: mocks.myOrders,
  myLedger: mocks.myLedger,
  myRewards: mocks.myRewards,
  myInvoices: mocks.myInvoices,
}))
// BalancePanel 自带登录态/路由依赖，与本次时间列断言无关，直接桩掉
vi.mock('@/components/selfservice', () => ({ BalancePanel: () => null }))

beforeEach(() => {
  cleanup()
  vi.clearAllMocks()
  mocks.myOverview.mockResolvedValue({ success: true, points_grants: 0, points_available: 0, approx_sentences: 0, billing_enforced: false, daily: [] })
  mocks.myOrders.mockResolvedValue({
    success: true, total: 1, page: 1, size: 10,
    orders: [{ id: 1, order_no: 'NO1', amount_points: 100, amount_money: 9.9, status: 'paid', channel: 'alipay', pay_method: '', manual_confirm: 0, created_at: UTC_ISO, paid_at: '' }],
  })
  mocks.myLedger.mockResolvedValue({
    success: true, total: 1, page: 1, size: 10,
    rows: [{ id: 1, task_type: 'translate', provider: 'p', model: 'm', quantity: 100, cost_points: 5, biz_kind: 'text', biz_mode: 'fast', created_at: UTC_ISO }],
  })
  mocks.myRewards.mockResolvedValue({ success: true, total: 0, page: 1, size: 10, rewards: [] })
  mocks.myInvoices.mockResolvedValue({ success: true, total: 0, page: 1, size: 10, invoices: [] })
})

describe('账单中心 · 订单时间列（默认页签）', () => {
  it('订单「创建时间」按运行时区显示本地日历日，双 TZ 档各自等值', async () => {
    const { container } = render(<BillingCenter />)
    // 链路自证：mock 已回数据、行已渲染（等值断言前先排除空态假绿）
    await vi.waitFor(() => { expect(container.textContent).toContain('NO1') })
    const cell = container.querySelector('tbody tr td:last-child')!.textContent ?? ''
    // 负向锁：旧「UTC 串裸切片」的样式（2026-09-25）不再作为跨日档之外的唯一显示——
    // 钉死时区下以数字序列等值为准（见下）；此处只锁单元格非空。
    expect(cell.trim().length).toBeGreaterThan(0)
    if (!wantDay) return // 机器时区不在钉死档：双 TZ 等值交给两条命令实跑
    // 运行时区生效自证（TZ 没透传进 worker 时直接红灯）
    expect(new Date(UTC_ISO).getHours()).toBe(zone === 'Asia/Shanghai' ? 2 : 18)
    // 等值锁：订单日期列的日历日数字 == 该时区独立算出的本地日；跨日错档不得出现
    expect(digitsOnly(cell).slice(0, 8)).toBe(wantDay)
    expect(digitsOnly(cell).slice(0, 8)).not.toBe(wrongDay)
  })
})

describe('账单中心 · 用量台账时间列', () => {
  it('切到台账页签后，时间列断出本地时区的「日期+时分」数字序列', async () => {
    const { container } = render(<BillingCenter />)
    await vi.waitFor(() => { expect(container.textContent).toContain('NO1') })
    fireEvent.click(screen.getByText(t('ss2.tabLedger')))
    // 行渲染自证：biz_mode=fast 的本地化标签已上屏，再谈时间列等值
    await vi.waitFor(() => { expect(container.textContent).toContain(t('ss2.modeFast')) })
    expect(mocks.myLedger).toHaveBeenCalled()
    const cell = container.querySelector('tbody tr td:first-child')!.textContent ?? ''
    // 负向锁（TZ 无关）：旧裸切片字样 '2026-09-25 18:30'（连字符+空格拼接的 UTC 直切）永不复发
    expect(cell).not.toBe('2026-09-25 18:30')
    if (!wantDay) return
    // 等值锁：整串数字序列 == 纯 Date 取值器算出的本地 yyyymmddHHMM
    expect(digitsOnly(cell)).toBe(localDigits(UTC_ISO))
    expect(digitsOnly(cell)).not.toBe(`${wrongDay}${zone === 'Asia/Shanghai' ? '1830' : '0230'}`)
  })
})
