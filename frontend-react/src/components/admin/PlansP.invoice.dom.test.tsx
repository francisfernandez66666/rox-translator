// ============================================================================
// components/admin/PlansP.invoice.dom.test.tsx — 开票成功回调必须重取发票列表
// （★ F-57①，2026-09-26 立为口径、本轮 〇-U 补锁；配额锁见 PlansP.quota.dom.test.tsx）
//
// 缺陷因果链：
//   「开票」弹窗提交走 billingInvoiceCreate，旧的成功分支只 `setInvDlg(null)` 关弹窗，
//   下方「发票申请」表格停在申请前的行集 ⇒ 客户以为没提交成功、又点一次（重复申请）。
//   同文件的「作废」分支一直是 `if (toastResp(r, …)) void loadInvoices()` 的对称口径，
//   现源码（PlansP.tsx 开票 Dialog 的 onConfirm）已补齐成功分支 `void loadInvoices()`。
//
// 锁法（为什么这组断言能复现它）：
//   ① billingInvoices 就是 GET /api/billing/invoices（发票列表接口）在接口层的函数身份，
//      在 jsdom 里 mock 到函数层即可，不需要真 fetch——「成功后调用次数 +1、
//      且新增那次调的就是 billingInvoices」与「第二次 URL 是发票列表」严格等价；
//   ② 更狠的一层：第二次回包里塞一条**新发票号**，断言它出现在表格里——
//      防止「调了但没接回列表 state」的半修复把调用次数锁变绿而界面依旧陈旧；
//   ③ 反向对照：提交失败（success:false）时**不得**重取（列表本来没变，重取是噪声），
//      且弹窗必须留在原地可重试（Dialog 的「确认失败不关框」产品口径）。
//
// 运行：npx vitest run src/components/admin/PlansP.invoice.dom.test.tsx
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, cleanup, fireEvent } from '@testing-library/react'
import { PlansP } from './PlansP'
// 期望文案一律现读词典（改文案即改期望）：开票链接/弹窗标题/确认钮都取 zh 字面值
import { zh as billingZh } from '@/i18n/panels/billing'
import { baseZh } from '@/i18n/dicts.zh'

// Q = 用例可控状态：发票列表按「第几次调用」回不同数据；creates 采集提交体；
// createResult 由每条用例就地翻转成功/失败。
const Q = vi.hoisted(() => ({
  invCalls: 0,
  createResult: { success: true } as Record<string, unknown>,
  creates: [] as Array<Record<string, unknown>>,
}))

// @/api 整模块桩（沿用 PlansP.quota.dom.test.tsx 已验证可行的桩集合，
// 另补 F-64② 之后新增的 adminGrowthFunnel——isSuper 挂载即拉漏斗，缺它会走 guardRead
// 的 catch 分支弹红字噪声；ApiError/handleUnauthorized 给真模块语义即可，读取全成功时不触达）
vi.mock('@/api', () => ({
  billingQuota: vi.fn(async () => ({ success: true, qps: 10, concurrent: 3, max_daily_chars: 0, max_daily_points: 0, tenant_selected: true, unlimited: {} })),
  billingQuotaSave: vi.fn(async () => ({ success: true })),
  billingOrders: vi.fn(async () => ({
    success: true,
    orders: [{ id: 77, order_no: 'ORD-2026-0077', status: 'paid', amount_points: 1000, amount_money: 99, channel: 'mock', created_at: '2026-09-27T02:00:00Z' }],
  })),
  // 发票列表：第 1 次（挂载首拉）回空表，第 2 次（成功回调重取）回一条新发票——
  // 「新行出现」才是「重取真正落到界面」的行为证据，调用次数只是前半截。
  billingInvoices: vi.fn(async () => {
    Q.invCalls += 1
    return Q.invCalls === 1
      ? { success: true, invoices: [] }
      : { success: true, invoices: [{ id: 9, invoice_no: 'FP-2026-0927-NEW', title: '某某科技有限公司', amount_money: 99, status: 'applied' }] }
  }),
  billingInvoiceCreate: vi.fn(async (data: Record<string, unknown>) => { Q.creates.push(data); return Q.createResult }),
  billingInvoiceVoid: vi.fn(async () => ({ success: true })),
  adminOrderRefund: vi.fn(async () => ({ success: true })),
  adminOrderPay: vi.fn(async () => ({ success: true })),
  payCreate: vi.fn(async () => ({ success: true })),
  payStatus: vi.fn(async () => ({ success: true })),
  paySimulate: vi.fn(async () => ({ success: true })),
  payManualConfirm: vi.fn(async () => ({ success: true })),
  manualConfirmOrders: vi.fn(async () => ({ success: true, orders: [] })),
  plans: vi.fn(async () => ({ success: true, plans: [] })),
  myPackage: vi.fn(async () => ({ success: true })),
  packageSubscribe: vi.fn(async () => ({ success: true })),
  packageUpgrade: vi.fn(async () => ({ success: true })),
  autoRenewSet: vi.fn(async () => ({ success: true })),
  couponPreview: vi.fn(async () => ({ success: true })),
  adminPackages: vi.fn(async () => ({ success: true, packages: [] })),
  adminPackageCreate: vi.fn(async () => ({ success: true })),
  adminPackageUpdate: vi.fn(async () => ({ success: true })),
  adminPackageDelete: vi.fn(async () => ({ success: true })),
  adminPackageSettings: vi.fn(async () => ({ success: true, free_trial_points: 1000, usdt_enabled: '0' })),
  adminPackageSettingsSave: vi.fn(async () => ({ success: true })),
  adminQRUpload: vi.fn(async () => ({ success: true })),
  adminPayChannels: vi.fn(async () => ({ success: true, fields: {}, env_overridden: {} })),
  adminPayChannelsSave: vi.fn(async () => ({ success: true })),
  PAY_CH_FIELDS: [],
  adminQuoteCurrency: vi.fn(async () => ({ success: true, currency: 'CNY', rates: { CNY: 1 }, supported_currencies: ['CNY'], env_overridden: {} })),
  adminQuoteCurrencySave: vi.fn(async () => ({ success: true })),
  adminGrowthFunnel: vi.fn(async () => ({ success: true, rows: [] })),
  request: vi.fn(async () => ({ success: true })),
  authHeaders: vi.fn(() => ({})),
  handleUnauthorized: vi.fn(),
  API_BASE: '',
}))

vi.mock('@/stores/admin', () => ({
  useAdmin: () => ({ isSuper: true, activeTenantId: 3 }),
  useAdminStore: { getState: () => ({ gotoPanel: () => {} }), setState: () => {} },
}))

const $ = (sel: string) => Array.from(document.querySelectorAll(sel)) as HTMLElement[]

// openInvoiceDialog 等首拉落定 → 点已付订单行的「开发票」链接 → 等弹窗（portal 挂 body）。
// 锚点选「开发票」本身：它由订单行渲染驱动，与发票重取逻辑无关，红只落在被测分支上。
async function openInvoiceDialog() {
  await vi.waitFor(() => expect($('a,button').some((e) => (e.textContent ?? '').trim() === billingZh['billing.invoiceIssue'])).toBe(true))
  const link = $('a,button').find((e) => (e.textContent ?? '').trim() === billingZh['billing.invoiceIssue'])!
  fireEvent.click(link)
  await vi.waitFor(() => expect(document.body.textContent).toContain(baseZh['billing.invoiceDialogTitle']))
}

beforeEach(() => {
  cleanup()
  Q.invCalls = 0
  Q.creates.length = 0
  Q.createResult = { success: true }
})

describe('PlansP · F-57① 开票成功后重取发票列表', () => {
  it('① 提交成功：弹窗关闭、billingInvoices（发票列表接口）被再次调用、新发票号上屏', async () => {
    render(<PlansP />)
    await openInvoiceDialog()
    expect(Q.invCalls, '挂载首拉应恰好 1 次（+1 判据的基准）').toBe(1)
    // 确认钮 = Dialog 默认 confirmText（t("common.ok")，zh 下为「确定」）。
    // 弹窗在 body 末尾 portal，取**最后一个**同名钮防背景面板里其他「确定」类按钮干扰。
    const confirm = $('button').filter((b) => (b.textContent ?? '').trim() === baseZh['common.ok']).pop()
    expect(confirm, '开票弹窗必须有确认钮').toBeTruthy()
    fireEvent.click(confirm!)
    // 提交体：订单 id 走 number 化，抬头/税号留空原样提交
    await vi.waitFor(() => expect(Q.creates.length).toBe(1))
    expect(Q.creates[0]).toEqual({ order_id: 77, title: '', tax_no: '' })
    // ★ 本锁核心：成功回调必须再调一次发票列表接口（旧写法停在 1 次）
    await vi.waitFor(() => expect(Q.invCalls).toBe(2))
    // 重取的数据真正接回 state：第二次回包的新发票号出现在「发票申请」表格
    await vi.waitFor(() => expect(document.body.textContent).toContain('FP-2026-0927-NEW'))
    // 弹窗已关（成功才关框；标题不再在场）
    expect($('div').some((d) => (d.className ?? '').includes('lc-dialog'))).toBe(false)
  })

  it('② 反向对照：提交失败不得重取列表，且弹窗留在原地可重试', async () => {
    Q.createResult = { success: false, message: '订单不可开票' }
    render(<PlansP />)
    await openInvoiceDialog()
    const confirm = $('button').filter((b) => (b.textContent ?? '').trim() === baseZh['common.ok']).pop()!
    fireEvent.click(confirm)
    await vi.waitFor(() => expect(Q.creates.length).toBe(1))
    // 给足异步余量：失败分支若错误地也 void loadInvoices()，这一下会把它抓出来
    await new Promise((r) => setTimeout(r, 50))
    expect(Q.invCalls, '失败时列表没变，不得白重取（防把守卫删成无条件刷）').toBe(1)
    expect(document.body.textContent).toContain(baseZh['billing.invoiceDialogTitle'])
    expect(document.body.textContent).not.toContain('FP-2026-0927-NEW')
  })
})
