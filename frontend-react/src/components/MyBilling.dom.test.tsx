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
//
// ★ O-02（2026-10-24 修复批，2026-10-25 补腿）：另锁「用量趋势卡的 UTC 日界说明在位」——
//   那一批的决策是**保留 UTC 日界、只补一句文案**，所以这句文案就是该决策唯一可读的面；
//   摘掉它界面零异常，因此必须有一条在位断言（见最后一个 describe）。
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

// ★ O-02（2026-10-24 云端 UAT 修复批）：账单时间口径**保留 UTC**、只补一句说明。
// 决策落点是文案而非日界，所以"这句说明还在不在屏幕上"就是这条决策唯一的可观测面：
// 摘掉 <p>{t('ss2.trendHint')}</p> 那一行，界面照常、数据照常、零报错，
// 而用户又会把「按 UTC 日统计」当成「按本地日统计」来对账（当初报障的本体）。
// 判据因此按**卡片归属**问，不按整页 textContent 问——说明被挪到别的卡片/页脚也照样"在页面上"，
// 那就退回成一句无关的文案，抓不到"趋势图自己没解释自己的横轴"这一族缺陷。
describe('账单中心 · 用量趋势图的 UTC 日界说明（O-02）', () => {
  it('趋势卡片内必须同时出现标题与 UTC 日界说明，且两者在同一张卡', async () => {
    const { container } = render(<BillingCenter />)
    // 链路自证：趋势图取数已回、卡片已渲染（先排除"整张卡没挂上"造成的假绿）
    await vi.waitFor(() => { expect(mocks.myOverview).toHaveBeenCalled() })
    const cards = Array.from(container.querySelectorAll('.mb-card'))
    const trend = cards.find((c) => (c.textContent ?? '').includes(t('ss2.trendTitle')))
    expect(trend, '趋势卡片未渲染（trendTitle 不在任何 .mb-card 里），后续断言无对象').toBeTruthy()
    // 等值锁：说明文案逐字上屏，且与标题同卡（12 语种词典由 locales.core.test.ts 全量键锁保证，
    // 这里只保证**这一个消费点**存在——缺键会显裸 key、缺消费点会什么都不显，两种都要红）
    expect(trend!.textContent).toContain(t('ss2.trendHint'))
    // 说明必须排在标题之后：它是"读这张图之前先看的一句话"，跑到卡片外面就不是这个语义了
    const body = trend!.textContent ?? ''
    expect(body.indexOf(t('ss2.trendTitle'))).toBeLessThan(body.indexOf(t('ss2.trendHint')))
    // 负向锁：不许把说明降级成 title 属性这类鼠标悬停才看得见的面（按属性逐个查，
    // 不用 [title*=…] 选择器——文案里有括号/分号/空格，拼进选择器就是在给判据埋转义坑）
    const hint = t('ss2.trendHint')
    const hiddenOnly = Array.from(trend!.querySelectorAll('[title]')).some((el) => (el.getAttribute('title') ?? '').includes(hint))
    expect(hiddenOnly, 'UTC 日界说明被降级成悬停 title：访客看不见就等于没写').toBe(false)
  })
})
