// ============================================================================
// PlansP.dom.test.tsx — 计费 Hub（收银台）面板组件级测试
// 2026-09-16 测试盲区补全（核实与修复_测试盲区补全_20260916.md 三.5）：
//   此前 admin 20+ 面板零渲染测试，USDT 收款配置/订单表/商业包管理纯靠 E2E 截图。
//   本用例 jsdom 环境渲染 PlansP，锁定三个高价值分支：
//     ① 订单表渲染后端数据（order_no/状态）；
//     ② USDT 收款开启时展示收款地址输入（三链按 usdt_chains 展开）；
//     ③ 超管视角商业包管理表渲染。
// 运行：npx vitest run（文件级 @vitest-environment jsdom，其余用例保持 node 环境）
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import { PlansP } from './PlansP'

// ---- '@/api' 全量 mock：只提供 PlansP 具名导入的最小实现 ----
vi.mock('@/api', () => {
  const ok = (extra: Record<string, unknown> = {}) => ({ success: true, ...extra })
  return {
    billingQuota: vi.fn(async () => ok({ qps: 10, concurrent: 3, max_daily_chars: 0 })),
    billingQuotaSave: vi.fn(async () => ok()),
    billingOrders: vi.fn(async () => ok({
      orders: [{
        id: 1, order_no: 'T1-ROTEST01', status: 'paid', amount_points: 300,
        amount_money: 99, channel: 'mock', created_at: '2026-09-16T00:00:00Z',
      }],
    })),
    billingInvoices: vi.fn(async () => ok({ invoices: [] })),
    billingInvoiceCreate: vi.fn(async () => ok()),
    billingInvoiceVoid: vi.fn(async () => ok()),
    adminOrderRefund: vi.fn(async () => ok()),
    payCreate: vi.fn(async () => ok()),
    payStatus: vi.fn(async () => ok()),
    paySimulate: vi.fn(async () => ok()),
    payManualConfirm: vi.fn(async () => ok()),
    manualConfirmOrders: vi.fn(async () => ok({ orders: [] })),
    plans: vi.fn(async () => ok({ plans: [] })),
    myPackage: vi.fn(async () => ok({ points_balance: 1000, points_grants_left: 2, points_permanent_balance: 300, points_used_month: 45, package_code: '', balance_sentences_approx: 600 })),
    packageSubscribe: vi.fn(async () => ok()),
    packageUpgrade: vi.fn(async () => ok()),
    adminPackages: vi.fn(async () => ok({
      packages: [{
        id: 1, code: 'uat_pkg_a', name: '商业包A', ptype: 'paid', points: 1000,
        price_money: 99, sentences: 0, duration_days: 30, enabled: 1,
      }],
    })),
    adminPackageCreate: vi.fn(async () => ok()),
    adminPackageUpdate: vi.fn(async () => ok()),
    adminPackageDelete: vi.fn(async () => ok()),
    // S1 积分制 + USDT 收款配置（开关开启 → 应渲染 TRC20 地址输入）
    adminPackageSettings: vi.fn(async () => ok({
      free_trial_points: 1000, sensitive_gate_enabled: '1',
      usdt_enabled: '1', usdt_auto_settle: '0', usdt_tail_enabled: '1',
      usdt_chains: ['trc20'], usdt_rate_fen_per_usdt: 720,
      usdt_addr_trc20: 'T4BJRYfnu29GPWdksz7EMUbiqx5CKSZgov',
      usdt_confirmations_trc20: 19,
    })),
    adminPackageSettingsSave: vi.fn(async () => ok()),
    adminQRUpload: vi.fn(async () => ok()),
    // ★ 2026-09-22 支付渠道凭据（微信/支付宝商户参数）：微信 APIv3 密钥以掩码回显、
    // 商户号由环境变量 PAY_WECHAT_MCH_ID 接管（该栏应置灰）
    adminPayChannels: vi.fn(async () => ok({
      fields: {
        paych_wechat_app_id: 'wx1234567890', paych_wechat_mch_id: 'db-mch',
        paych_wechat_apiv3_key: '********', paych_wechat_private_key: '********',
        paych_alipay_app_id: '2021000123456789', paych_alipay_enabled: '',
      },
      env_overridden: { paych_wechat_mch_id: 'PAY_WECHAT_MCH_ID' },
    })),
    adminPayChannelsSave: vi.fn(async () => ok()),
    PAY_CH_FIELDS: [
      'paych_notify_base',
      'paych_wechat_enabled', 'paych_wechat_app_id', 'paych_wechat_mch_id', 'paych_wechat_serial_no',
      'paych_wechat_apiv3_key', 'paych_wechat_private_key', 'paych_wechat_platform_cert', 'paych_wechat_notify_url',
      'paych_alipay_enabled', 'paych_alipay_app_id', 'paych_alipay_private_key', 'paych_alipay_public_key',
      'paych_alipay_seller_id', 'paych_alipay_gateway', 'paych_alipay_notify_url',
    ],
    // ★ #75：超管视角 loadPkgs 会拉报价配置（多币种），缺这两个导出会让整页测试踩未 mock 的 getter
    adminQuoteCurrency: vi.fn(async () => ok({
      currency: 'CNY', rates: { CNY: 1 },
      supported_currencies: ['CNY', 'USD', 'EUR', 'JPY', 'GBP', 'HKD', 'KRW', 'SGD', 'AUD', 'CAD', 'CHF', 'THB'],
      env_overridden: {},
    })),
    adminQuoteCurrencySave: vi.fn(async () => ok()),
    request: vi.fn(async () => ok({ funnel: {} })),
    authHeaders: vi.fn(() => ({})),
    API_BASE: '',
  }
})

// ---- '@/stores/admin'：仅 PlansP 消费 isSuper（超管视角渲染全部管理区块） ----
vi.mock('@/stores/admin', () => ({
  useAdmin: () => ({ isSuper: true }),
  useAdminStore: { getState: () => ({ gotoPanel: () => {} }), setState: () => {} },
}))

beforeEach(() => {
  cleanup()
  vi.clearAllMocks()
})

describe('admin 计费 Hub（PlansP）', () => {
  it('挂载不抛错，订单表渲染后端订单数据（order_no 可见）', async () => {
    const { container } = render(<PlansP />)
    // 等待 useEffect 异步加载落定
    await vi.waitFor(() => {
      expect(screen.getByText(/T1-ROTEST01/)).toBeTruthy()
    })
    expect(container.textContent).toBeTruthy()
  })

  it('USDT 收款开启时：TRC20 收款地址输入框渲染且值正确（三链按 usdt_chains 展开）', async () => {
    render(<PlansP />)
    await vi.waitFor(() => {
      const inputs = document.querySelectorAll('input')
      expect(Array.from(inputs).some((i) => (i as HTMLInputElement).value === 'T4BJRYfnu29GPWdksz7EMUbiqx5CKSZgov')).toBe(true)
    })
  })

  it('超管视角：商业包管理表渲染包名与 code', async () => {
    render(<PlansP />)
    await vi.waitFor(() => {
      expect(screen.getByText(/商业包A/)).toBeTruthy()
      expect(screen.getByText(/uat_pkg_a/)).toBeTruthy()
    })
  })

  // ★ 2026-09-22 支付渠道凭据区块（管理台可配）：锁定两条「看不到就会出事故」的呈现口径——
  //   ① 敏感项只以掩码出现在表单里（真密钥永不下发到浏览器）；
  //   ② 环境变量接管的字段置灰并标出变量名（否则管理员改完以为生效了）。
  it('支付渠道凭据：敏感项掩码回显，环境变量接管字段置灰并标注变量名', async () => {
    render(<PlansP />)
    const fieldList = () => Array.from(document.querySelectorAll('input, textarea')) as Array<HTMLInputElement | HTMLTextAreaElement>
    // 整个区块首屏就在（数据未落定前字段为空），故断言一律等异步回显落定
    await vi.waitFor(() => {
      expect(fieldList().some((f) => f.value === '********')).toBe(true)
      expect(document.body.textContent).toContain('PAY_WECHAT_MCH_ID')
    })
    const envField = fieldList().find((f) => f.value === 'db-mch')
    expect(envField?.disabled).toBe(true)
    const editable = fieldList().find((f) => f.value === 'wx1234567890')
    expect(editable?.disabled).toBe(false)
  })
})
