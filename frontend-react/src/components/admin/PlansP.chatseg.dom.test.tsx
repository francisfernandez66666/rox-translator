// ============================================================================
// components/admin/PlansP.chatseg.dom.test.tsx — 即时翻译分段两档的运营表单
// （★ 2026-11-01 DEF-P1-05 B2：text_chat_per_paragraph / chat_segment_max_points）
//
// 为什么有这张锁：这两档改的是**所有租户每一单**即时翻译的执行形态与成本上限
// （走不走逐段流式、超多少积分自动回落整篇）。写死在代码里＝调档必须发版，
// 而"上线第一天先实测 3/10/30 段三档再收默认值"（施工方案 §B2-2 灰度那条）本身就要求能配。
//
// ★★ 这一族最容易被写错的**不是**边界，而是方向：0 在这里是真实档位
//   （0＝这一单走整篇／0＝不设积分上限），而同一张面板上紧挨着的逐行翻译那族刻意**没有** 0 档语义。
//   所以下面 ② 与 ③ 是一对**反向判据**：本族填 0 必须发出请求，越界值必须零请求。
//   只测其中一半的话，"把那颗关分段的闸做成摆设"和"把脏值放行进库"都能各绿一侧
//   （AGENTS §一·19 九-4 那条"两种脏值落点不同、必须分开点名"的同族）。
//
// 另外两条不在本文件的判据：
//   · 区间/配置键/缺省的**三方跨侧等值**（前端 ⇔ api/chat_seg_config.go ⇔ engine 常量）
//     归 src/lib/chatSegTiers.crossSide.lock.test.ts（node 环境，它读 Go 源文件）；
//   · 接口拒收边界 == 读侧钳位（行为双向）归 backend-go/internal/api/chat_seg_config_gate_test.go。
//   这里只留界面行为的判据——两处各写一份解析正则就是本仓最忌的"两份尺子"。
//
// 运行：npx vitest run src/components/admin/PlansP.chatseg.dom.test.tsx
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, cleanup, fireEvent } from '@testing-library/react'
import { PlansP } from './PlansP'
import { DialogHost } from '@/components/uiDialogs'
import { ApiError } from '@/api'
// 期望文案一律现读词典（改文案即改期望，不写死字面量）
import { zh as billingZh } from '@/i18n/panels/billing'
import { baseZh } from '@/i18n/dicts.zh'
// 区间与字段名的前端出处（界面预拦用的就是这一份，本文件不另抄数字）
import { CHAT_SEG_TIER_RANGES, CHAT_SEG_TIER_FIELDS } from '@/lib/chatSegTiers'

// S = 可控回显：csCfg 注入分段档 GET 桩的返回体；csSaves 采集保存调用（判"弹窗未决时零请求"）
const S = vi.hoisted(() => ({
  csSaves: [] as Array<Record<string, unknown>>,
  csGetOk: true,
  csGetFail: '' as '' | '403',
  csCfg: {} as Record<string, unknown>,
}))

function segCfg() {
  return {
    success: true,
    tiers: { per_paragraph: 1, max_points: 60 },
    stored: { text_chat_per_paragraph: '', chat_segment_max_points: '' },
    defaults: { per_paragraph: 1, max_points: 60 },
    ranges: { ...CHAT_SEG_TIER_RANGES },
    zero_semantics: 'zero_is_a_real_tier',
  }
}

vi.mock('@/api', () => ({
  billingQuota: vi.fn(async () => ({ success: true, qps: 10, concurrent: 3, max_daily_chars: 0, max_daily_points: 0, tenant_selected: true, unlimited: {} })),
  billingQuotaSave: vi.fn(async () => ({ success: true })),
  billingOrders: vi.fn(async () => ({ success: true, orders: [] })),
  billingInvoices: vi.fn(async () => ({ success: true, invoices: [] })),
  billingInvoiceCreate: vi.fn(async () => ({ success: true })),
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
  adminPackageSettings: vi.fn(async () => ({ success: true, free_trial_points: 1000, usdt_enabled: '0', pay_mode: 'static_qr' })),
  adminPackageSettingsSave: vi.fn(async () => ({ success: true })),
  adminQRUpload: vi.fn(async () => ({ success: true })),
  adminPayChannels: vi.fn(async () => ({ success: true, fields: {}, env_overridden: {} })),
  adminPayChannelsSave: vi.fn(async () => ({ success: true })),
  PAY_CH_FIELDS: [],
  adminQuoteCurrency: vi.fn(async () => ({ success: true, currency: 'CNY', rates: { CNY: 1 }, supported_currencies: ['CNY'], env_overridden: {} })),
  adminQuoteCurrencySave: vi.fn(async () => ({ success: true })),
  EST_TOKEN_FIELDS: ['k_pro', 'k_fast', 'fixed_pro', 'fixed_fast'],
  adminEstTokens: vi.fn(async () => ({ success: true, coefficients: {}, stored: {}, defaults: {} })),
  adminEstTokensSave: vi.fn(async () => ({ success: true })),
  // ---- 同面板的逐行翻译卡：本文件不测它，但必须给一份合法回显，
  //      否则那张卡进入"回显失败⇒禁用"态而把整个面板的渲染路径改掉（夹具噪声）
  adminTextLineTiers: vi.fn(async () => ({
    success: true,
    tiers: { retry_max: 3, upstream_dead_streak: 3 },
    stored: { text_line_retry_max: '', text_line_upstream_dead_streak: '' },
    defaults: { retry_max: 3, upstream_dead_streak: 3 },
    ranges: { retry_max: [1, 5], upstream_dead_streak: [2, 10] },
  })),
  adminTextLineTiersSave: vi.fn(async () => ({ success: true })),
  // ---- 本文件主测对象：分段两档的读与写 ----
  adminChatSegTiers: vi.fn(async () => {
    if (S.csGetFail === '403') {
      // 后端读面对非平台超管回 403（bizResp 会把 403 照抛以触发重登录），这里造同一形态
      throw new ApiError('无权限访问该配置', 403)
    }
    return S.csGetOk ? S.csCfg : { success: false, message: '读取失败' }
  }),
  adminChatSegTiersSave: vi.fn(async (data: Record<string, unknown>) => {
    S.csSaves.push(data)
    return { success: true, ...(S.csCfg as Record<string, unknown>) }
  }),
  adminGrowthFunnel: vi.fn(async () => ({ success: true, rows: [] })),
  request: vi.fn(async () => ({ success: true })),
  authHeaders: vi.fn(() => ({})),
  handleUnauthorized: vi.fn(),
  // 与真实 core.ts 同构造签名（message, status）——guardRead 按 status 分派 401/其余
  ApiError: class extends Error {
    status?: number
    constructor(message: string, status?: number) {
      super(message)
      this.name = 'ApiError'
      this.status = status
    }
  },
  API_BASE: '',
}))

// 提示总线收成可断言的清单：「拦下了」必须既能证明没发请求（负向），
// 也能证明给出了该给的那句话（正向对照）——只数请求次数的话，一句都不弹也算"拦下"。
const T = vi.hoisted(() => ({ warn: [] as string[], error: [] as string[], ok: [] as string[] }))
vi.mock('@/lib/toastBus', () => ({
  toastSuccess: vi.fn((m: string) => { T.ok.push(m) }),
  toastWarn: vi.fn((m: string) => { T.warn.push(m) }),
  toastError: vi.fn((m: string) => { T.error.push(m) }),
}))

vi.mock('@/stores/admin', () => ({
  useAdmin: () => ({ isSuper: true, activeTenantId: 3 }),
  useAdminStore: { getState: () => ({ gotoPanel: () => {} }), setState: () => {} },
}))

// box 取分段区块根节点：标题 span → 按钮行 → 区块容器（与 textline/esttokens 同一取法）
function box(): HTMLElement {
  const title = (Array.from(document.querySelectorAll('span')) as HTMLElement[])
    .find((s) => (s.textContent ?? '').trim() === billingZh['billing.csSection'])
  if (!title) throw new Error('未渲染即时翻译分段档位区块（PlansP 结构变了？锁需随迁）')
  return title.parentElement!.parentElement!
}
// 两个输入框按 CHAT_SEG_TIER_FIELDS 声明序渲染，故下标即键序
function input(i: number): HTMLInputElement {
  const ins = Array.from(box().querySelectorAll('input')) as HTMLInputElement[]
  if (ins.length !== CHAT_SEG_TIER_FIELDS.length) {
    throw new Error(`分段档位区块应有 ${CHAT_SEG_TIER_FIELDS.length} 个输入框，实得 ${ins.length}`)
  }
  return ins[i]
}
function btn(label: string): HTMLButtonElement {
  const b = (Array.from(box().querySelectorAll('button')) as HTMLButtonElement[])
    .find((x) => (x.textContent ?? '').trim() === label)
  if (!b) throw new Error(`分段档位区块未找到按钮「${label}」`)
  return b
}
function dialogBtn(key: 'common.ok' | 'common.cancel'): HTMLButtonElement | undefined {
  return ((Array.from(document.querySelectorAll('button')) as HTMLButtonElement[])
    .filter((b) => (b.textContent ?? '').trim() === baseZh[key])).pop()
}

async function mount() {
  render(<><PlansP /><DialogHost /></>)
  await vi.waitFor(() => expect(input(0).value).toBe('1'))
}

beforeEach(() => {
  cleanup()
  S.csSaves.length = 0
  S.csGetOk = true
  S.csGetFail = ''
  T.warn.length = 0
  T.error.length = 0
  T.ok.length = 0
  S.csCfg = segCfg()
})

describe('PlansP · 即时翻译分段两条档位（DEF-P1-05 B2）', () => {
  it('① 回显生效值；库里没表态时标注「走代码缺省」，提示与文案都在场', async () => {
    await mount()
    expect([input(0).value, input(1).value]).toEqual(['1', '60'])
    const txt = box().textContent ?? ''
    expect(txt).toContain(billingZh['billing.estDefault'].replace('{v}', '1'))
    expect(txt).toContain(billingZh['billing.csHint'])
    // 区间/键名/缺省一律现读纯模块，界面文案里不许出现第二份硬编码区间之外的数字
    expect(txt).toContain(billingZh['billing.csSwitch'])
    expect(txt).toContain(billingZh['billing.csMaxPoints'])
  })

  it('② ★ 本族 0 是真实档位：填 0 必须**发出请求**（那颗"关分段"的闸要按得下去）', async () => {
    await mount()
    fireEvent.change(input(0), { target: { value: '0' } })
    fireEvent.click(btn(baseZh['common.save']))
    await vi.waitFor(() => expect(S.csSaves.length).toBe(1))
    expect(S.csSaves[0]).toEqual({ per_paragraph: 0, max_points: 60 })
    // 上限那一档的 0（＝不设上限）同样必须发出去
    S.csSaves.length = 0
    await mount()
    fireEvent.change(input(1), { target: { value: '0' } })
    fireEvent.click(btn(baseZh['common.save']))
    await vi.waitFor(() => expect(S.csSaves.length).toBe(1))
    expect(S.csSaves[0]).toEqual({ per_paragraph: 1, max_points: 0 })
  })

  it('③ 越界与非整数当场拦下且零请求，并且**出声**（假拦截判红）', async () => {
    await mount()
    // 开关只认 0/1 ⇒ 2 越界；上限上界 100000 ⇒ 100001 越界；负数两档都越界
    const bads: Array<[number, string]> = [[0, '2'], [1, '100001'], [1, '-1']]
    for (const [i, v] of bads) {
      fireEvent.change(input(i), { target: { value: v } })
      fireEvent.click(btn(baseZh['common.save']))
      await new Promise((r) => setTimeout(r, 30))
      expect(S.csSaves.length, `填 ${v} 到第 ${i} 档却发出了请求`).toBe(0)
    }
    // 正向对照：拦下必须出声（只数请求次数的话，"什么都不做"也算拦下，
    // 而运营看到的是按钮没反应 —— #42 批定性过的静默失败形态）
    expect(T.warn, '越界输入被拦下却没给任何提示').toContain(billingZh['billing.csNeedAll'])
    // 非整数（3.5）：这两档一个是开关、一个是"次数"型积分，小数没有意义
    S.csSaves.length = 0
    fireEvent.change(input(1), { target: { value: '3.5' } })
    fireEvent.click(btn(baseZh['common.save']))
    await new Promise((r) => setTimeout(r, 30))
    expect(S.csSaves.length).toBe(0)
  })

  it('④ 区间两端各能存进去，且载荷两键一次交齐（端点现算，本文件不抄第二份区间数字）', async () => {
    const [loS, hiS] = CHAT_SEG_TIER_RANGES.per_paragraph
    const [loM, hiM] = CHAT_SEG_TIER_RANGES.max_points
    for (const [a, b] of [[loS, loM], [hiS, hiM]] as Array<[number, number]>) {
      S.csSaves.length = 0
      await mount()
      fireEvent.change(input(0), { target: { value: String(a) } })
      fireEvent.change(input(1), { target: { value: String(b) } })
      fireEvent.click(btn(baseZh['common.save']))
      await vi.waitFor(() => expect(S.csSaves.length).toBe(1))
      expect(S.csSaves[0]).toEqual({ per_paragraph: a, max_points: b })
      cleanup()
    }
  })

  it('⑤ 库里是脏值：输入框填生效值、把脏值点名出来，保存发出的是生效值而非脏串', async () => {
    // 库里躺着 "abc"（读侧按缺省解释 ⇒ 生效值 60），界面必须填 60 而不是 abc；
    // 把 "abc" 塞进输入框＝管理员原样点保存，把一个系统从没在用过的值钉成正式配置。
    S.csCfg = {
      success: true,
      tiers: { per_paragraph: 1, max_points: 60 },
      stored: { text_chat_per_paragraph: 'abc', chat_segment_max_points: '' },
      defaults: { per_paragraph: 1, max_points: 60 },
      ranges: { ...CHAT_SEG_TIER_RANGES },
    }
    render(<><PlansP /><DialogHost /></>)
    await vi.waitFor(() => expect(input(0).value).toBe('1'))
    expect(box().textContent ?? '').toContain('abc')
    fireEvent.click(btn(baseZh['common.save']))
    await vi.waitFor(() => expect(S.csSaves.length).toBe(1))
    expect(S.csSaves[0]).toEqual({ per_paragraph: 1, max_points: 60 })
  })

  it('⑤b 库里存的是合法的 0 ⇒ 生效值 0 与库内 0 相等，**不许**标成脏值', async () => {
    // 这一条是"0 合法"的另一半证据：上面 ⑤ 用 abc 证"脏要标出来"，
    // 这里必须证"合法的那个 0 不能被顺手标出来"——否则运营每次按下关分段的闸都会看到一句误告警。
    S.csCfg = {
      success: true,
      tiers: { per_paragraph: 0, max_points: 0 },
      stored: { text_chat_per_paragraph: '0', chat_segment_max_points: '0' },
      defaults: { per_paragraph: 1, max_points: 60 },
      ranges: { ...CHAT_SEG_TIER_RANGES },
    }
    render(<><PlansP /><DialogHost /></>)
    await vi.waitFor(() => expect(input(0).value).toBe('0'))
    expect(input(1).value).toBe('0')
    const txt = box().textContent ?? ''
    // 生效值==库内值 ⇒ 既不显示"走代码缺省"，也不显示脏值提示。
    // ⚠️ 两条模板都要把**全部占位符**代入后再比：只替 {v} 会拿一条"界面上根本不可能出现的串"去判缺失
    //    （结构性恒真＝空转锁，本仓那条老坑）。
    const dirty = (v: string, d: string) => billingZh['billing.estDirty'].replace('{v}', v).replace('{d}', d)
    const dflt = (v: string) => billingZh['billing.estDefault'].replace('{v}', v)
    for (const s of [dirty('0', '1'), dirty('0', '60'), dflt('1'), dflt('60')]) {
      expect(txt.includes(s), `合法的 0 被界面说成「${s}」`).toBe(false)
    }
    // 射程自证：同一份判据喂**真脏值**那一态必须能命中（否则上面四条"不许出现"是闸门失明）
    expect(dirty('abc', '1')).not.toBe(dirty('0', '1'))
    S.csCfg = {
      success: true,
      tiers: { per_paragraph: 1, max_points: 60 },
      stored: { text_chat_per_paragraph: 'abc', chat_segment_max_points: '' },
      defaults: { per_paragraph: 1, max_points: 60 },
      ranges: { ...CHAT_SEG_TIER_RANGES },
    }
    cleanup()
    render(<><PlansP /><DialogHost /></>)
    await vi.waitFor(() => expect(input(0).value).toBe('1'))
    expect(box().textContent ?? '').toContain(dirty('abc', '1'))
  })

  it('⑥ GET 失败：输入框禁用且点保存零调用（防拿空表单覆盖线上两档）', async () => {
    S.csGetOk = false
    render(<><PlansP /><DialogHost /></>)
    await new Promise((r) => setTimeout(r, 80))
    expect(input(0).disabled).toBe(true)
    expect(input(1).disabled).toBe(true)
    fireEvent.click(btn(baseZh['common.save']))
    await new Promise((r) => setTimeout(r, 30))
    expect(S.csSaves.length).toBe(0)
  })

  it('⑦ 恢复缺省：先二次确认；取消零调用，确认只发 {reset:true}', async () => {
    await mount()
    fireEvent.click(btn(billingZh['billing.estReset']))
    await vi.waitFor(() => expect(dialogBtn('common.ok')).toBeTruthy())
    fireEvent.click(dialogBtn('common.cancel')!)
    await new Promise((r) => setTimeout(r, 30))
    expect(S.csSaves.length, '取消确认却发出了请求').toBe(0)
    fireEvent.click(btn(billingZh['billing.estReset']))
    await vi.waitFor(() => expect(dialogBtn('common.ok')).toBeTruthy())
    fireEvent.click(dialogBtn('common.ok')!)
    await vi.waitFor(() => expect(S.csSaves.length).toBe(1))
    expect(S.csSaves[0]).toEqual({ reset: true })
  })

  it('⑧ 403（非平台超管读到越权）：走 toast、界面不白屏、两档禁用且零写入', async () => {
    S.csGetFail = '403'
    render(<><PlansP /><DialogHost /></>)
    await new Promise((r) => setTimeout(r, 80))
    expect(box()).toBeTruthy() // 取不到节点会直接抛 ⇒ 这一句本身就是"没白屏"的判据
    expect(input(0).disabled).toBe(true)
    expect(input(1).disabled).toBe(true)
    expect(T.error.length, '403 却没弹任何提示').toBeGreaterThan(0)
    expect(T.error.join('|')).toContain('无权限访问该配置')
    fireEvent.click(btn(baseZh['common.save']))
    await new Promise((r) => setTimeout(r, 30))
    expect(S.csSaves.length, '回显都失败还发出了写请求').toBe(0)
  })

  it('⑨ 承诺禁词：分段档位面不许出现"人工/复核/审核/稍后重试"字样（本仓无人工环节）', () => {
    const banned = ['人工', '复核', '审核', '稍后重试', '我们会跟进']
    for (const k of ['billing.csSection', 'billing.csHint', 'billing.csSwitch', 'billing.csMaxPoints', 'billing.csRange', 'billing.csNeedAll', 'billing.csConfirmReset']) {
      const v = billingZh[k]
      for (const w of banned) {
        expect(v.includes(w), `${k} 含禁词「${w}」（本仓无人工环节，不许承诺）`).toBe(false)
      }
    }
  })

  it('⑩ 回落话术：csHint 必须把"超上限回落整篇"写成成本保护而不是故障（§B2-2 那条对外表达口径）', () => {
    // 这条抓的是**语义**而不是字面量：施工方案明写"回落是成本保护，不是缺陷；
    // 把它写成失败会把客户吓跑"。界面提示若翻成"失败/出错/不支持"，下一位就会把回落当缺陷去修。
    const hint = billingZh['billing.csHint']
    expect(/回落/.test(hint), 'csHint 必须写明超上限会回落整篇（这是客户会看到的形态）').toBe(true)
    for (const bad of ['失败', '出错', '报错', '不支持']) {
      expect(hint.includes(bad), `csHint 把回落写成「${bad}」⇒ 把成本保护说成了故障`).toBe(false)
    }
    expect(hint.includes('成本保护'), 'csHint 必须点名回落的性质是成本保护').toBe(true)
  })
})
