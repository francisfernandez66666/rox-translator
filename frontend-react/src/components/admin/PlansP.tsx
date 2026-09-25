// ============================================================================
// components/admin/PlansP.tsx — 套餐中心面板
// 职责：计费配置、套餐订阅、充值、订单/发票、配额与超管商业包管理
// 从 panels_c.tsx 拆分
// 2026-09-18（UI 融合）：金额/余额等强调数字的品牌蓝兜底色改为暗色主题正文色
//   （统一落到 var(--lc-text-1)，暗底上不再出现旧版深蓝）；收款台/静态码预览边框同步转暗；
//   订单标题、按钮文案的 emoji 前缀清理。计费、轮询、退款与权限判断逻辑均未动。
// 2026-09-18（组件迁移）：TDesign 组件整体迁移至项目自带 langcross 纯黑组件库
//   （Button / DataTable / Dialog / Switch / StatusPill / Badge / Link），业务逻辑不变。
// 2026-09-21（★ #41 商业洞三）：充值/订阅下单链路接入优惠券——券码选填、试算走
//   /api/coupon/preview（与下单同一算法，前端不自算金额），建单成功即清空券码防止重复核销；
//   自动续费开关（#44）随套餐卡渲染，仅付费包可见。折让只减钱不减积分，口径见 store/coupons.go。
// 2026-09-22（支付渠道凭据管理台可配）：「运营配置」面板内新增微信/支付宝商户参数区块
//   （/api/admin/pay/channels[/save]）。敏感项后端加密落库、只以掩码回显，表单原样回提
//   不会冲掉真密钥；被环境变量接管的字段置灰并标出变量名（取值优先级 env > 库配置）。
// 2026-09-23（★ #74 订阅续费宽限期）：当前套餐区新增宽限期提示条——订阅已到期但开启
//   自动续费时（/api/me/package 的 in_grace/grace_expires），显示「已到期 · 宽限期至
//   {本地日期}」并说明身份/额度保留与逾期移除规则。仅消费后端新出参，计费逻辑未动。
// ============================================================================
import { useCallback, useEffect, useRef, useState } from 'react'
import { fmtPoints } from '@/utils/points' // ★ S1 积分展示
import type { ChangeEvent } from 'react'
import { Button, DataTable, Dialog, Switch, StatusPill, Badge, Link, type StatusTone } from '@/ui/langcross/src'
import { confirmDialog } from '@/components/uiDialogs'
import { toastSuccess, toastError, toastWarn } from '@/lib/toastBus'
import {
  billingQuota, billingQuotaSave,
  billingOrders, billingInvoices, billingInvoiceCreate, billingInvoiceVoid, adminOrderRefund,
  payCreate, payStatus, paySimulate, payManualConfirm, manualConfirmOrders, adminOrderPay,
  plans as apiPlans, myPackage, packageSubscribe, packageUpgrade, autoRenewSet,
  couponPreview,
  adminPackages, adminPackageCreate, adminPackageUpdate, adminPackageDelete,
  adminPackageSettings, adminPackageSettingsSave, adminQRUpload,
  adminPayChannels, adminPayChannelsSave, PAY_CH_FIELDS,
  type PayChField,
  adminQuoteCurrency, adminQuoteCurrencySave,
  request,
  authHeaders,
  API_BASE,
  handleUnauthorized, // ★ §4.2-2：二维码 blob 通道自管 401（裸 fetch 不经 request()）
  type Any,
} from '@/api'
import { Panel, Field, toastResp, num } from './parts'
import { fmtQuoteMoney, cnyToQuote } from '@/components/quoteFmt' // ★ #75 报价展示口径集中在 quoteFmt
import { fmtTime } from '@/lib/ui'
import { useAdmin } from '@/stores/admin'
import { useT } from '@/i18n'

// isImage 判断字符串是否可作图片展示（data:image / 站内二维码 / http(s) 图片扩展名）。
function isImage(s?: string): boolean {
  if (!s) return false
  if (s.indexOf('data:image') === 0) return true
  if (s.indexOf('/api/qr-image/') === 0) return true
  const idx = s.indexOf('.')
  if (idx < 0) return false
  const tail = s.slice(idx + 1).split(/[?#]/)[0].toLowerCase()
  return ['png', 'jpg', 'jpeg', 'gif', 'webp'].indexOf(tail) >= 0 && (s.indexOf('http://') === 0 || s.indexOf('https://') === 0)
}

// orderStatusLabel 订单状态中文文案（pending/paid/refunded/cancelled；未知原样返回）。
function orderStatusLabel(s: string, t: (k: string) => string): string {
  const m: Record<string, string> = {
    pending: t('billing.stPending'), paid: t('billing.stPaid'),
    refunded: t('billing.stRefunded'), cancelled: t('billing.stCancelled'),
  }
  return m[s] || s
}

// usdtChainLabel 链名展示文案（TRC20/ERC20/BEP20）。
function usdtChainLabel(c: string): string {
  return ({ trc20: 'TRC20 (Tron)', erc20: 'ERC20 (Ethereum)', bep20: 'BEP20 (BSC)' } as Record<string, string>)[c] || c
}

// usdtAddrURL 收款地址的链上浏览器链接（钱包链接核验用）。
function usdtAddrURL(chain: string, addr: string): string {
  if (!addr) return ''
  if (chain === 'trc20') return `https://tronscan.org/#/address/${addr}`
  if (chain === 'erc20') return `https://etherscan.io/address/${addr}`
  if (chain === 'bep20') return `https://bscscan.com/address/${addr}`
  return ''
}

// toLocalDate RFC3339 → 本地 YYYY-MM-DD（★ #74 宽限期提示用）。
// 刻意与 CouponsP.tsx 的 toLocal 同一手法（getFullYear/padStart 取本地时区），
// 而不是上方 pkgExpiresLabel 的 toISOString().slice(0,10)——那是 UTC 口径，
// 时区边界附近会把「宽限期截止日」错显一天。非法/空值一律回空串，由调用侧兜 '—'。
function toLocalDate(rfc?: string): string {
  if (!rfc) return ''
  const d = new Date(rfc)
  if (Number.isNaN(d.getTime())) return ''
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
}

// 支付渠道凭据字段分组（★ 2026-09-22）：字段键与后端 internal/store/billing_payconfig.go
// 的 system_config 白名单同名（paych_ 前缀），label 为 i18n 键（12 语种全量覆盖）。
// area=true 走多行输入：商户私钥/平台证书是 PEM 全文，单行框既放不下也难核对。
const PAY_CH_GROUPS: Array<{ title: string; switchKey: PayChField; fields: Array<{ key: PayChField; label: string; area?: boolean }> }> = [
  {
    title: 'billing.chWechat',
    switchKey: 'paych_wechat_enabled',
    fields: [
      { key: 'paych_wechat_app_id', label: 'billing.paychAppId' },
      { key: 'paych_wechat_mch_id', label: 'billing.paychMchId' },
      { key: 'paych_wechat_serial_no', label: 'billing.paychSerialNo' },
      { key: 'paych_wechat_apiv3_key', label: 'billing.paychApiV3Key' },
      { key: 'paych_wechat_private_key', label: 'billing.paychPrivateKey', area: true },
      { key: 'paych_wechat_platform_cert', label: 'billing.paychPlatformCert', area: true },
      { key: 'paych_wechat_notify_url', label: 'billing.paychNotifyUrl' },
    ],
  },
  {
    title: 'billing.chAlipay',
    switchKey: 'paych_alipay_enabled',
    fields: [
      { key: 'paych_alipay_app_id', label: 'billing.paychAppId' },
      { key: 'paych_alipay_private_key', label: 'billing.paychPrivateKey', area: true },
      { key: 'paych_alipay_public_key', label: 'billing.paychPublicKey', area: true },
      { key: 'paych_alipay_seller_id', label: 'billing.paychSellerId' },
      { key: 'paych_alipay_gateway', label: 'billing.paychGateway' },
      { key: 'paych_alipay_notify_url', label: 'billing.paychNotifyUrl' },
    ],
  },
]

// statusTheme 订单状态对应的标签配色（langcross StatusPill tone）。
function statusTheme(s: string): StatusTone {
  return ({ pending: 'warn', paid: 'success', refunded: 'idle', cancelled: 'idle' } as Record<string, StatusTone>)[s] || 'idle'
}

// PlansP 套餐中心面板主组件：计费配置、套餐/订阅管理、充值订单、发票与超管商业包操作入口。
export function PlansP() {
  const ad = useAdmin()
  const [, t, tpl] = useT()
  const isSuper = ad.isSuper

  const [pkg, setPkg] = useState<Any>({})
  const [planList, setPlanList] = useState<Any[]>([])
  const [orders, setOrders] = useState<Any[]>([])
  const [invoices, setInvoices] = useState<Any[]>([])
  const [chForm, setChForm] = useState<Any>({ channel: 'auto', points: 3000 })
  const [showCheckout, setShowCheckout] = useState(false)
  const [chLoading, setChLoading] = useState(false)
  const [curOrder, setCurOrder] = useState<Any | null>(null)
  const orderRef = useRef<Any | null>(null)
  const payTimer = useRef<ReturnType<typeof setInterval> | null>(null)
  const payPollBusy = useRef(false) // ★ 轮询在途锁（2026-09-16）：慢响应下不叠加并发请求
  const [payMode, setPayMode] = useState('mock')
  const [payModeCfg, setPayModeCfg] = useState('mock')
  const payModeLabel = ({ mock: t('billing.chMock'), sdk: t('billing.chSdk'), static_qr: t('billing.chStaticQR') } as Record<string, string>)[payMode] || payMode
  const [quotaForm, setQuotaForm] = useState<Any>({ qps: 10, concurrent: 3, max_daily_chars: 0, max_daily_points: 0 })
  const [pkgs, setPkgs] = useState<Any[]>([])
  const [billingEnforced, setBillingEnforced] = useState(false)
  const [sensitiveGate, setSensitiveGate] = useState(true) // ★ S8 敏感词兑底闸开关
  const [freeTrialPoints, setFreeTrialPoints] = useState(1000)
  const [freeTrialDays, setFreeTrialDays] = useState(14)
  const [markupMultiplier, setMarkupMultiplier] = useState(1.5)
  const [staticQRImage, setStaticQRImage] = useState('')
  // ★ USDT（2026-09-15）：超管后台收款配置 + 收银台收款要素
  const [usdtCfg, setUsdtCfg] = useState<Any>({
    usdt_enabled: '0', usdt_auto_settle: '0', usdt_tail_enabled: '1',
    usdt_chains: 'trc20', usdt_rate_fen_per_usdt: 720,
    usdt_addr_trc20: '', usdt_addr_erc20: '', usdt_addr_bep20: '',
    usdt_confirmations_trc20: 19, usdt_confirmations_erc20: 12, usdt_confirmations_bep20: 15,
  })
  const usdtOn = usdtCfg.usdt_enabled === '1'
  // ★ 2026-09-22 支付渠道凭据（微信 Native v3 / 支付宝当面付）：
  //   payCfg=管理台表单值（敏感项为后端掩码 "********"，原样回提即保持不动），
  //   payCfgEnv=被环境变量接管的字段（键→变量名）；这些栏位置灰，避免「改了不生效」的坑。
  const [payCfg, setPayCfg] = useState<Record<string, string>>({})
  const [payCfgEnv, setPayCfgEnv] = useState<Record<string, string>>({})
  const [payCfgReady, setPayCfgReady] = useState(false)
  // ★ 2026-09-23 多币种报价（#75）：超管表单值 = 报价币种 + 倍率表全量（字符串承载，
  //   空串=该币种不报价；不含 CNY——结算基准恒为 1 不可配）。
  //   quoteEnv=被环境变量接管的配置项（quote_currency / fx_rates → 变量名），对应栏位置灰；
  //   quoteReady=GET 成功回显过才允许提交（表单空白直提等于把线上汇率整批清空，同 payCfgReady 的坑）。
  const [quoteCfg, setQuoteCfg] = useState<{ currency: string; rates: Record<string, string> }>({ currency: 'CNY', rates: {} })
  const [quoteSupported, setQuoteSupported] = useState<string[]>([])
  const [quoteEnv, setQuoteEnv] = useState<Record<string, string>>({})
  const [quoteReady, setQuoteReady] = useState(false)
  // ★ #75 C 端报价口径：/api/me/package 透出的 quote_currency + fx_rates_snapshot，
  //   收银台据此把人民币实收金额折算成本币展示值（仅展示，实扣仍是 CNY）。
  const [myQuote, setMyQuote] = useState<{ code: string; rates: Record<string, number> }>({ code: 'CNY', rates: {} })
  const manualOrdersUsdt = useRef<Record<string, Any>>({})
  const [manualOrders, setManualOrders] = useState<Any[]>([])
  // ★ S4 增长漏斗（超管看板）：注册→激活→耗尽→首购→续费，按渠道聚合
  const [funnelDays, setFunnelDays] = useState(30)
  const [funnelRows, setFunnelRows] = useState<Any[]>([])
  const [invDlg, setInvDlg] = useState<null | { order: Any; title: string; taxNo: string }>(null)
  // ★ 自动续费（#41）：开关请求进行中（禁用重复点击，避免与服务端状态来回覆盖）
  const [autoRenewBusy, setAutoRenewBusy] = useState(false)
  // ★ 优惠券（#41 商业洞三）：券码输入 + 服务端试算回显。
  //   金额一律由 /api/coupon/preview 按下单同一算法重算，前端不采信自己算出来的数（防改包白拿折扣）。
  const [couponCode, setCouponCode] = useState('')
  const [couponQuote, setCouponQuote] = useState<Any | null>(null)
  const [couponBusy, setCouponBusy] = useState(false)

  const setOrder = (o: Any | null) => { orderRef.current = o; setCurOrder(o) }
  const pkgExpiresLabel = (() => {
    const v = pkg.package_expires as string
    if (!v) return ''
    const d = new Date(v)
    return isNaN(d.getTime()) ? '' : d.toISOString().slice(0, 10)
  })()

    // startPolling 支付弹窗打开后每 3s 轮询订单状态（paid/cancelled/refunded 均停）
function startPolling() { stopPolling(); payPollBusy.current = false; payTimer.current = setInterval(checkStatus, 3000) }
    // stopPolling 停止支付状态轮询
function stopPolling() { if (payTimer.current) { clearInterval(payTimer.current); payTimer.current = null } }
  useEffect(() => () => stopPolling(), [])

  // loadPackage 拉取本租户套餐与支付方式，并同步可购商业包列表
  const loadPackage = useCallback(async () => {
    const r: Any = await myPackage()
    if (r.success) {
      setPkg(r)
      const pm = r.pay_mode as string
      if (pm) { setPayMode(pm); setPayModeCfg(pm) }
      // ★ USDT：租户侧收银台渠道显隐（超管配置经 /api/me/package 透出开关态，地址/汇率不下发）
      if (r.usdt_enabled !== undefined) {
        setUsdtCfg((c) => ({ ...c, usdt_enabled: r.usdt_enabled ? '1' : '0', usdt_chains: ((r.usdt_chains as string[]) || []).join(',') || c.usdt_chains }))
      }
      // ★ #75：收银台报价口径（后端 Resolve 已做「缺倍率回落 CNY」的 fail-closed，前端拿来即用）
      setMyQuote({ code: String(r.quote_currency || 'CNY'), rates: (r.fx_rates_snapshot as Record<string, number>) || {} })
    }
    const p: Any = await apiPlans()
    if (p.success) setPlanList((p.plans as Any[]) || [])
  }, [])
  // loadOrders 拉取充值订单列表（待付/已付/退款全状态）
  const loadOrders = useCallback(async () => {
    const r: Any = await billingOrders()
    if (r.success) setOrders((r.orders as Any[]) || [])
  }, [])
  // loadInvoices 拉取发票申请列表
  const loadInvoices = useCallback(async () => {
    const r: Any = await billingInvoices()
    if (r.success) setInvoices((r.invoices as Any[]) || [])
  }, [])
  // loadQuota 拉取租户限流配额（QPS/并发/日字符/日积分）并回填表单
  const loadQuota = useCallback(async () => {
    const r: Any = await billingQuota()
    if (r.success) setQuotaForm({
      qps: (r.qps as number) || 10, concurrent: (r.concurrent as number) || 3,
      max_daily_chars: (r.max_daily_chars as number) || 0, max_daily_points: (r.max_daily_points as number) ?? 0,
    })
  }, [])
  // loadPkgs 超管专属：拉取全部商业包配置（普通租户直接跳过）
  const loadPkgs = useCallback(async () => {
    if (!isSuper) return
    const r: Any = await adminPackages()
    if (r.success) setPkgs((r.packages as Any[]) || [])
    const cfg: Any = await adminPackageSettings()
    if (cfg.success) {
      setBillingEnforced(cfg.billing_enforced === '1' || cfg.billing_enforced === true)
      if (cfg.sensitive_gate_enabled !== undefined) setSensitiveGate(cfg.sensitive_gate_enabled !== '0') // ★ S8
      if (cfg.free_trial_points) setFreeTrialPoints(Number(cfg.free_trial_points))
      if (cfg.free_trial_days) setFreeTrialDays(Number(cfg.free_trial_days))
      if (typeof cfg.billing_markup_multiplier === 'number') setMarkupMultiplier(cfg.billing_markup_multiplier)
      if (cfg.pay_mode) setPayModeCfg(cfg.pay_mode as string)
      if (cfg.static_qr_image) setStaticQRImage(cfg.static_qr_image as string)
      // ★ USDT：回填收款配置（开关/链/地址/汇率/确认数）
      if (cfg.usdt_enabled !== undefined) {
        setUsdtCfg({
          usdt_enabled: String(cfg.usdt_enabled ?? '0'),
          usdt_auto_settle: String(cfg.usdt_auto_settle ?? '0'),
          usdt_tail_enabled: String(cfg.usdt_tail_enabled ?? '1'),
          usdt_chains: String(cfg.usdt_chains ?? 'trc20'),
          usdt_rate_fen_per_usdt: Number(cfg.usdt_rate_fen_per_usdt) || 720,
          usdt_addr_trc20: String(cfg.usdt_addr_trc20 ?? ''),
          usdt_addr_erc20: String(cfg.usdt_addr_erc20 ?? ''),
          usdt_addr_bep20: String(cfg.usdt_addr_bep20 ?? ''),
          usdt_confirmations_trc20: Number(cfg.usdt_confirmations_trc20) || 19,
          usdt_confirmations_erc20: Number(cfg.usdt_confirmations_erc20) || 12,
          usdt_confirmations_bep20: Number(cfg.usdt_confirmations_bep20) || 15,
        })
      }
    }
    const m: Any = await manualConfirmOrders()
    if (m.success) {
      setManualOrders((m.orders as Any[]) || [])
      manualOrdersUsdt.current = (m.usdt_info as Record<string, Any>) || {} // ★ USDT 线索映射
    }
    // ★ 2026-09-22：支付渠道凭据回显（独立接口，敏感项为掩码）
    const pc: Any = await adminPayChannels()
    if (pc.success) {
      setPayCfg((pc.fields as Record<string, string>) || {})
      setPayCfgEnv((pc.env_overridden as Record<string, string>) || {})
      setPayCfgReady(true)
    } else {
      setPayCfgReady(false) // 未取到现值就不允许提交：否则表单空值会把线上凭据整批清掉
    }
    // ★ 2026-09-23（#75）：报价配置回显（独立接口）。rates 含 CNY:1 基准项，表单不渲染它
    // （恒为 1 不可配，提交时也整表不带 CNY——后端 SetFxRates 会拒收非 1 的 CNY）。
    const qc: Any = await adminQuoteCurrency()
    if (qc.success) {
      setQuoteSupported((qc.supported_currencies as string[]) || [])
      setQuoteEnv((qc.env_overridden as Record<string, string>) || {})
      const rates: Record<string, string> = {}
      for (const [k, v] of Object.entries((qc.rates as Record<string, number>) || {})) {
        if (k !== 'CNY') rates[k] = String(v)
      }
      setQuoteCfg({ currency: String(qc.currency || 'CNY'), rates })
      setQuoteReady(true)
    } else {
      setQuoteReady(false) // 同 payCfgReady：没回显成功就禁提交，防整表清空
    }
  }, [isSuper])

  // loadAll 计费面板整体刷新：租户先取套餐，再并行拉订单/发票/配额/商业包
  const loadAll = useCallback(async () => {
    if (!isSuper) await loadPackage()
    await Promise.all([loadOrders(), loadInvoices(), loadQuota(), loadPkgs()])
  }, [isSuper, loadPackage, loadOrders, loadInvoices, loadQuota, loadPkgs])
  useEffect(() => { void loadAll() }, [loadAll])

    // subscribe 订阅套餐：建单→弹收款台（券码非空时一并提交，服务端按折后金额出码）
async function subscribe(pl: Any) {
    const r: Any = await packageSubscribe(String(pl.code), couponCode.trim().toUpperCase())
    // ★ 2026-09-16：业务失败（渠道未开放/余额校验等）不再静默吞掉，给用户可见反馈
    if (!r.success) { void toastError(String(r.message || t('billing.subscribeFailed'))); return }
    const o = r.order as Any
    if (o) { setOrder(o); setShowCheckout(true); if (o.channel !== 'manual') startPolling() }
    setUsdtPay((r.usdt_pay as Any) || null)
    await loadPackage()
  }
    // upgrade 升级套餐（补差价折算）
async function upgrade(pl: Any) {
    const cur = pkg.package_code as string
    const ok = await confirmDialog({
      header: t('plans.upgradeTitle'),
      body: tpl('billing.upgradeConfirm', { cur: cur || '', next: pl.name || pl.code }),
      confirmText: t('billing.subscribeNow'),
    })
    if (!ok) return
    const r: Any = await packageUpgrade(String(pl.code))
    if (!toastResp(r)) return
    if (r.credit_money > 0) void toastSuccess(tpl('billing.upgradeCredit', { money: r.credit_money }))
    const o = r.order as Any
    if (o) { setOrder(o); setShowCheckout(true); if (o.channel !== 'manual') startPolling() }
    await loadPackage()
  }
    // saveAutoRenew 自动续费开关（#41）：服务端落库成功后仅改本地开关态；
    // 失败（包已下架/未订阅/网络）一律重读套餐，让开关回落到服务端真值，不出现「界面开了库里没开」
  async function saveAutoRenew(on: boolean) {
    if (autoRenewBusy) return
    setAutoRenewBusy(true)
    try {
      const r: Any = await autoRenewSet(on)
      if (!r.success) {
        void toastError(String(r.message || t('plans.autoRenewFailed')))
        await loadPackage()
        return
      }
      setPkg((p: Any) => ({ ...p, auto_renew: on }))
      void toastSuccess(on ? t('plans.autoRenewOn') : t('plans.autoRenewOff'))
    } catch (e: any) {
      void toastError(e?.message || t('plans.autoRenewFailed'))
      await loadPackage()
    } finally {
      setAutoRenewBusy(false)
    }
  }
    // openCheckout 打开收款弹窗（收款码/金额/复制）
async function openCheckout() {
    if (Number(chForm.points) <= 0) return
    setChLoading(true)
    try {
      const rawCh = chForm.channel === 'auto' ? '' : String(chForm.channel)
      const channel = rawCh.startsWith('usdt:') ? 'usdt' : rawCh
      if (channel === 'usdt') chForm.usdt_chain = rawCh.slice(5)
      const coupon = couponCode.trim().toUpperCase()
      const base = { points: Number(chForm.points), channel, ...(coupon ? { coupon } : {}) }
      const r: Any = await payCreate(channel === 'usdt'
        ? { ...base, usdt_chain: String(chForm.usdt_chain || '') }
        : base)
      if (!toastResp(r)) return
      const o = r.order as Any
      setOrder(o); setShowCheckout(true)
      setUsdtPay((r.usdt_pay as Any) || null); setUsdtTxInput('')
      // ★ 券码一次性使用：建单成功即清空输入与试算，避免用户再点一次「去支付」把额度重复核销掉
      if (coupon) { setCouponCode(''); setCouponQuote(null) }
      if (o && o.channel !== 'manual') startPolling()
    } catch (e: any) {
      void toastError(e?.message || t('common.fail'))
    } finally { setChLoading(false) }
  }
  // previewCoupon 券码试算（★ #41）：按当前充值积数请服务端算折让，结果只回显不落库。
  // 试算口径与下单完全同源（同一 couponOrderAmount + PreviewCouponDiscount），不会出现「试算能减、下单报错」。
  async function previewCoupon() {
    const code = couponCode.trim().toUpperCase()
    if (!code) { setCouponQuote(null); return }
    if (couponBusy) return
    setCouponBusy(true)
    try {
      const r: Any = await couponPreview({ code, points: Number(chForm.points) || 0 })
      if (r.success) { setCouponQuote(r); return }
      setCouponQuote(null)
      void toastError(String(r.message || t('plans.couponFail')))
    } catch (e: any) {
      void toastError(e?.message || t('plans.couponFail'))
    } finally { setCouponBusy(false) }
  }
  async function resumePay(_o: Any) {
    const r: Any = await payStatus(Number(_o.id))
    const o = (r.success ? (r.order as Any) : _o)
    setOrder(o); setShowCheckout(true)
    setUsdtPay((r.usdt_pay as Any) || null); setUsdtTxInput('')
    if (o && o.channel !== 'manual') startPolling()
  }
    // closeCheckout 关闭收款弹窗并清理轮询
function closeCheckout() { setShowCheckout(false); stopPolling(); setUsdtPay(null); setUsdtTxInput(''); void loadOrders(); if (!isSuper) void loadPackage() }
    // checkStatus 单次核对订单支付状态（3s 轮询）
  // ★ 2026-09-16 整改：①in-flight 锁防慢响应叠加；②cancelled/refunded 同为终态——
  //   旧实现只认 paid，USDT 24h 窗口超时被后端置 cancelled 后收银台仍无限轮询、弹窗不收。
async function checkStatus() {
    const o = orderRef.current
    if (!o || payPollBusy.current) return
    if (document.hidden) return // ★ 标签页切到后台不轮询（回前台自动恢复）
    payPollBusy.current = true
    try {
      const r: Any = await payStatus(Number(o.id))
      if (r.success) {
        const no = (r.order as Any) || o
        setOrder(no)
        if (r.usdt_pay) setUsdtPay(r.usdt_pay as Any)
        if (no.status === 'paid') stopPolling()
        else if (no.status === 'cancelled' || no.status === 'refunded') {
          stopPolling()
          void toastWarn(t('billing.payOrderGone'))
          setShowCheckout(false); setUsdtPay(null); setUsdtTxInput('')
          void loadOrders()
        }
      }
    } finally { payPollBusy.current = false }
  }
    // simulatePay mock 模式下模拟支付成功（联调）
async function simulatePay() {
    const o = orderRef.current
    if (!o) return
    setChLoading(true)
    try { const r: Any = await paySimulate(Number(o.id)); if (r.success) await checkStatus() } finally { setChLoading(false) }
  }
    // manualConfirm 用户声明已付款→进人工核对单
async function manualConfirm() {
    const o = orderRef.current
    if (!o) return
    setChLoading(true)
    try {
      const r: Any = await payManualConfirm(Number(o.id), o.channel === 'usdt' ? usdtTxInput.trim() : '')
      if (r.success) { void toastSuccess(t('billing.manualNotify')); stopPolling(); closeCheckout() }
      else void toastError((r.message as string) || t('billing.iPaidFailed'))
    } catch (e: any) { void toastError(e?.message || t('common.fail')) }
    finally { setChLoading(false) }
  }

  // ★ 2026-09-16 补口（评审发现#4）：后端 /api/admin/orders/refund（超管）与
  //   /api/billing/invoices/void（租管+）一直健在，前端此前零封装零入口，
  //   SOP 承诺的退款只能 DBA 直连接口。此处补齐操作闭环。
async function refundOrder(row: Any) {
    const ok = await confirmDialog({
      header: t('billing.refundConfirmTitle'),
      body: tpl('billing.refundConfirmBody', { no: String(row.order_no ?? ''), money: Number(row.amount_money ?? 0).toFixed(2) }),
      confirmText: t('billing.refund'),
    })
    if (!ok) return
    const r: Any = await adminOrderRefund({ id: Number(row.id) })
    if (toastResp(r, t('billing.orderRefunded'))) void loadOrders()
  }
  // voidInvoice 发票冲红（作废后同单可重开）
async function voidInvoice(row: Any) {
    const ok = await confirmDialog({
      header: t('billing.voidConfirmTitle'),
      body: tpl('billing.voidConfirmBody', { no: String(row.invoice_no ?? '') }),
      confirmText: t('billing.void'),
    })
    if (!ok) return
    const r: Any = await billingInvoiceVoid(Number(row.id))
    if (toastResp(r, t('billing.invoiceVoided'))) void loadInvoices()
  }

  const [qrImg, setQrImg] = useState('')
  // ★ USDT：收银台收款要素（下单/轮询响应回填）、pay_uri 二维码、客户声明 txid 输入
  const [usdtPay, setUsdtPay] = useState<Any | null>(null)
  const [usdtQr, setUsdtQr] = useState('')
  const [usdtTxInput, setUsdtTxInput] = useState('')
  // ★ §4.2-1（blob 泄漏）：两张二维码各用 ref 持有其 objectURL——依赖变化重取时释放上一张、
  //   组件卸载时释放当前张；配合 alive 守卫处理「异步返回时已卸载/已被新请求取代」的竞态泄漏。
  // ★ §4.2-2 正当豁免（二维码出图走裸 fetch）：/api/qr/render 回的是 PNG 二进制，
  //   request() 的出口固定 response.json()，无法承载文件流，故这里必须裸用 fetch；
  //   但 401（登录态失效）不得静默降级成「只显示文本收款要素」——统一交给 core 的
  //   handleUnauthorized 清态回登录，口径同 api/tickets.ts 的下载通道。
  const usdtQrRef = useRef('')
  const qrImgRef = useRef('')
  useEffect(() => {
    let alive = true
    setUsdtQr('')
    const uri = usdtPay?.pay_uri as string | undefined
    if (uri) {
      void (async () => {
        try {
          const res = await fetch(`${API_BASE}/api/qr/render?text=${encodeURIComponent(uri)}`, { headers: authHeaders() })
          if (res.status === 401) { handleUnauthorized('/api/qr/render'); return } // 登录过期：清态回登录，不停在「半登录」收银台
          if (!res.ok) return
          const blob = await res.blob()
          const url = URL.createObjectURL(blob)
          if (!alive) { URL.revokeObjectURL(url); return } // 已被取代/卸载 → 就地释放新 objectURL
          if (usdtQrRef.current) URL.revokeObjectURL(usdtQrRef.current) // 释放上一张
          usdtQrRef.current = url
          setUsdtQr(url)
        } catch { /* 渲染失败回退文本 */ }
      })()
    }
    return () => {
      alive = false
      if (usdtQrRef.current) { URL.revokeObjectURL(usdtQrRef.current); usdtQrRef.current = '' }
    }
  }, [usdtPay?.pay_uri])
  useEffect(() => {
    let alive = true
    setQrImg('')
    const content = curOrder?.qr_content as string | undefined
    if (content && !isImage(content)) {
      void (async () => {
        try {
          // ★ P1-16：拼 API_BASE（裸相对路径在配置 VITE_API_BASE 跨域部署时断链）
          const res = await fetch(`${API_BASE}/api/qr/render?text=${encodeURIComponent(content)}`, { headers: authHeaders() })
          if (res.status === 401) { handleUnauthorized('/api/qr/render'); return } // 同上：401 走 core 统一清态，不静默退回文本码
          if (!res.ok) return
          const blob = await res.blob()
          const url = URL.createObjectURL(blob)
          if (!alive) { URL.revokeObjectURL(url); return } // 已被取代/卸载 → 就地释放新 objectURL
          if (qrImgRef.current) URL.revokeObjectURL(qrImgRef.current) // 释放上一张
          qrImgRef.current = url
          setQrImg(url)
        } catch { /* 渲染失败则回退文本展示 */ }
      })()
    }
    return () => {
      alive = false
      if (qrImgRef.current) { URL.revokeObjectURL(qrImgRef.current); qrImgRef.current = '' }
    }
  }, [curOrder?.qr_content])

    // saveQuota 保存租户配额（qps/并发）
async function saveQuota() {
    await billingQuotaSave({
      qps: Math.max(1, Number(quotaForm.qps) || 0),
      concurrent: Math.max(1, Number(quotaForm.concurrent) || 0),
      max_daily_chars: Math.max(0, Number(quotaForm.max_daily_chars) || 0),
      max_daily_points: Math.max(0, Number(quotaForm.max_daily_points) || 0),
    })
    await loadQuota()
  }

  const [pkgForm, setPkgForm] = useState<Any>({ code: '', name: '', ptype: 'paid', sentences: 0, points: 1000, price_money: 0, duration_days: 30 })
    // createPkg 新建套餐
async function createPkg() {
    if (!pkgForm.code || !pkgForm.name) { void toastWarn(t('packages.nameRequired')); return }
    const r: Any = await adminPackageCreate(pkgForm as any)
    if (toastResp(r)) { setPkgForm({ code: '', name: '', ptype: 'paid', sentences: 0, points: 1000, price_money: 0, duration_days: 30 }); void loadPkgs() }
  }
    // togglePkg 套餐上下架
async function togglePkg(p: Any) { await adminPackageUpdate({ id: Number(p.id), enabled: p.enabled ? 0 : 1 }); void loadPkgs() }
    // deletePkg 删除套餐
async function deletePkg(p: Any) {
    if (!(await confirmDialog({ body: t('packages.confirmDeletePkg') }))) return
    await adminPackageDelete(Number(p.id)); void loadPkgs()
  }
    // loadFunnel 拉取 S4 注册 cohort 增长漏斗
async function loadFunnel() {
    try {
      const r: Any = await request(`/api/admin/funnel?days=${funnelDays}`, { headers: authHeaders() })
      if (r?.success) setFunnelRows((r.rows || []) as Any[])
    } catch { /* 静默：看板辅助数据 */ }
  }
  useEffect(() => { if (isSuper) void loadFunnel() }, [isSuper, funnelDays])

    // saveEnforce 硬扣费开关（billing_enforced）
async function saveEnforce() { const r: Any = await adminPackageSettingsSave({ billing_enforced: billingEnforced ? '1' : '0' } as never); toastResp(r, t('common.save')) }
    // saveSensitiveGate S8 敏感词合规闸开关
async function saveSensitiveGate() { const r: Any = await adminPackageSettingsSave({ sensitive_gate_enabled: sensitiveGate ? '1' : '0' } as never); toastResp(r, t('common.save')) }
    // saveBillingParams S3 一次性邮箱黑名单 + 体验额度/加价系数保存
async function saveBillingParams() {
    if (!(freeTrialPoints > 0)) { void toastWarn(t('packages.trialPointsInvalid')); return }
    if (!(freeTrialDays > 0)) { void toastWarn(t('packages.trialDaysInvalid')); return }
    if (!(markupMultiplier >= 1)) { void toastWarn(t('packages.markupInvalid')); return }
    const r: Any = await adminPackageSettingsSave({
      free_trial_points: freeTrialPoints, free_trial_days: freeTrialDays,
      billing_markup_multiplier: markupMultiplier,
    } as never)
    toastResp(r, t('common.save'))
  }
    // savePayMode 支付模式切换（mock/静态收款码）
async function savePayMode() {
    const r: Any = await adminPackageSettingsSave({ pay_mode: payModeCfg } as never)
    if (toastResp(r, t('common.save'))) setPayMode(payModeCfg)
  }
    // saveStaticQR 保存静态收款码配置
async function saveStaticQR() { const r: Any = await adminPackageSettingsSave({ static_qr_image: staticQRImage } as never); toastResp(r, t('common.save')) }
  const [qrUploading, setQrUploading] = useState(false)
    // uploadStaticQR 上传收款码图片
async function uploadStaticQR(e: ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0]
    e.currentTarget.value = ''
    if (!file) return
    setQrUploading(true)
    try {
      const r = await adminQRUpload(file)
      if (r.success && r.qr_url) {
        setStaticQRImage(r.qr_url)
        const s = await adminPackageSettingsSave({ static_qr_image: r.qr_url } as never)
        toastResp(s, t('common.save'))
      } else {
        void toastError((r.message as string) || t('common.saveFail'))
      }
    } catch (err: any) {
      void toastError(err?.message || t('common.saveFail'))
    } finally { setQrUploading(false) }
  }
    // saveUSDT ★ USDT（2026-09-15）：保存超管收款配置（开关/链/钱包地址/汇率/确认数；后端逐项校验）
async function saveUSDT() {
    try { await saveUSDTInner() } catch (e: any) { void toastError(e?.message || t('common.saveFail')) }
  }
/** saveUSDTInner 实际保存逻辑（与 try/catch 包装分离，便于独立测试） */
async function saveUSDTInner() {
    const chains = String(usdtCfg.usdt_chains || '').split(',').map((c) => c.trim()).filter(Boolean)
    if (usdtOn && !(Number(usdtCfg.usdt_rate_fen_per_usdt) > 0)) { void toastWarn(t('billing.usdtRateRequired')); return }
    if (usdtOn && chains.length && chains.every((c) => !String(usdtCfg['usdt_addr_' + c] || '').trim())) { void toastWarn(t('billing.usdtAddrRequired')); return }
    const r: Any = await adminPackageSettingsSave({
      usdt_enabled: String(usdtCfg.usdt_enabled), usdt_auto_settle: String(usdtCfg.usdt_auto_settle),
      usdt_tail_enabled: String(usdtCfg.usdt_tail_enabled), usdt_chains: chains.join(',') || 'trc20',
      usdt_addr_trc20: String(usdtCfg.usdt_addr_trc20 || ''), usdt_addr_erc20: String(usdtCfg.usdt_addr_erc20 || ''),
      usdt_addr_bep20: String(usdtCfg.usdt_addr_bep20 || ''), usdt_rate_fen_per_usdt: Number(usdtCfg.usdt_rate_fen_per_usdt) || 0,
      usdt_confirmations_trc20: Number(usdtCfg.usdt_confirmations_trc20) || 0,
      usdt_confirmations_erc20: Number(usdtCfg.usdt_confirmations_erc20) || 0,
      usdt_confirmations_bep20: Number(usdtCfg.usdt_confirmations_bep20) || 0,
    } as never)
    toastResp(r, t('common.save'))
    await loadPkgs()
  }
    // savePayChannels ★ 2026-09-22：保存支付渠道凭据（微信/支付宝商户参数）。
    // 只提交「已回显过的字段」：GET 失败时 payCfg 为空，此时提交等于把线上凭据整批清空——
    // 故先由 Save 按钮的 disabled 兜住，这里再校验一次键集，双保险。
    // 敏感项原样回提后端掩码即可：后端识别 "****" 后不会写库（真密钥保持不动）。
async function savePayChannels() {
    const known = new Set<string>(PAY_CH_FIELDS as readonly string[])
    const fields: Partial<Record<PayChField, string>> = {}
    for (const [k, v] of Object.entries(payCfg)) {
      if (known.has(k)) fields[k as PayChField] = String(v ?? '')
    }
    if (!Object.keys(fields).length) { void toastWarn(t('common.saveFail')); return }
    try {
      const r: Any = await adminPayChannelsSave(fields)
      if (toastResp(r, t('common.save'))) await loadPkgs()
    } catch (e: any) { void toastError(e?.message || t('common.saveFail')) }
  }
  // payChInput 渲染一个支付渠道凭据输入项（area=true 用多行框承载 PEM 全文）。
  // 被环境变量接管的字段置灰但仍随保存回传：置灰只是防误改，值不能丢
  // （丢了就等于「清除该项」，与灰掉的语义相反）。
  function payChInput(key: PayChField, area: boolean) {
    const envName = payCfgEnv[key] || ''
    const value = String(payCfg[key] ?? '')
    const onCh = (e: ChangeEvent<HTMLInputElement | HTMLTextAreaElement>) => setPayCfg((c) => ({ ...c, [key]: e.target.value }))
    return (
      <>
        {area ? (
          <textarea className="lc-input" rows={3} value={value} disabled={!!envName} onChange={onCh}
                    style={{ width: '100%', maxWidth: 520, fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace', fontSize: 14 }} />
        ) : (
          <input className="lc-input" type="text" value={value} disabled={!!envName} onChange={onCh} style={{ width: 360 }} />
        )}
        {envName ? <span style={{ fontSize: 14, color: 'var(--adm-amber-tx)' }}>{envName} · {t('billing.paychEnvOn')}</span> : null}
      </>
    )
  }

    // saveQuoteCfg ★ 2026-09-23（#75）：保存报价币种与汇率倍率（整表覆盖）。
    // 客户端先做两道与服务端一致的门：倍率必须是有限正数（脏输入不进请求，
    // 免得 NaN 序列化后变 null 让后端报一句看不懂的话）；选了非 CNY 币种
    // 就必须已填该币种倍率——「配了币种没配汇率」= 客户看到外币价却回落人民币，两头不讨好。
  async function saveQuoteCfg() {
    const rates: Record<string, number> = {}
    for (const [code, raw] of Object.entries(quoteCfg.rates)) {
      const s = String(raw ?? '').trim()
      if (!s) continue // 空倍率 = 该币种不报价（覆盖保存语义）
      const n = Number(s)
      if (!Number.isFinite(n) || n <= 0) { void toastWarn(t('billing.quoteNeedRate')); return }
      rates[code] = n
    }
    const code = String(quoteCfg.currency || 'CNY')
    if (code !== 'CNY' && rates[code] === undefined) { void toastWarn(t('billing.quoteNeedRate')); return }
    try {
      const r: Any = await adminQuoteCurrencySave({ currency: code, rates })
      if (toastResp(r, t('common.save'))) await loadPkgs() // 回读生效值：清洗掉的脏键以服务端结果为准
    } catch (e: any) { void toastError(e?.message || t('common.saveFail')) }
  }

    // confirmManual 管理员确认人工到账→积分入双桶
async function confirmManual(o: Any) {
    const tx = (manualTxInputs[String(o.id)] || '').trim()
    if (o.channel === 'usdt' && !tx) { void toastWarn(t('billing.usdtTxRequired')); return }
    try {
      const r: Any = await adminOrderPay(Number(o.id), Number(o.tenant_id) || 0, tx)
      if (r.success) { void toastSuccess(t('billing.manualConfirmed')); await Promise.all([loadPkgs(), loadOrders()]) }
      else void toastError((r.message as string) || t('billing.iPaidFailed'))
    } catch (e: any) { void toastError(e?.message || t('billing.iPaidFailed')) }
  }
  const [manualTxInputs, setManualTxInputs] = useState<Record<string, string>>({})

  const planGroups = [
    { type: 'paid', title: t('plans.groupPaid'), items: planList.filter((p) => p.ptype === 'paid') },
    { type: 'increment', title: t('plans.groupIncrement'), items: planList.filter((p) => p.ptype === 'increment') },
  ]
  const curPlan = planList.find((p) => p.ptype === 'paid' && p.code === (pkg.package_code as string))
  const isUpgradePlan = (pl: Any): boolean =>
    !!curPlan && pl.ptype === 'paid' && Number(pl.price_money) > Number(curPlan.price_money)
  const chOptions = [
    { label: tpl('billing.payModeAuto', { mode: payModeLabel }), value: 'auto' },
    ...(payMode === 'static_qr' ? [{ label: t('billing.chStaticQR'), value: 'manual' }] : []),
    ...(payMode === 'sdk' ? [{ label: t('billing.chWechat'), value: 'wechat' }, { label: t('billing.chAlipay'), value: 'alipay' }] : []),
    ...(usdtOn ? String(usdtCfg.usdt_chains || '').split(',').filter(Boolean).map((c) => ({ label: `${t('billing.chUsdt')} · ${usdtChainLabel(c.trim())}`, value: `usdt:${c.trim()}` })) : []),
    // ★ F-09（批G）：mock 项只在支付模式为 mock 时展示——此前无条件渲染，
    //   static_qr/sdk 模式下租户也能选「模拟支付（测试）」，等于收银台露出测试后门；
    //   条件展开与上方 :719-720 的 static_qr/sdk 范式保持一致。
    ...(payMode === 'mock' ? [{ label: t('billing.chMock'), value: 'mock' }] : []),
  ]

  return (
    <>
      {/* 当前套餐概览（租户视角；超管在平台上下文无需看本租户余额，故 !isSuper 才渲染） */}
      {!isSuper && (
        <Panel title={t('plans.nav.current')}>
          {/* 四张余额卡：接口出口即积分口径（/api/me/package 不再透出 token 裸值）。
              可用余额/本月已用为主题色（兜底 #E7E9EA）、剩余赠送为琥珀色、永久额度为成功色。 */}
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit,minmax(150px,1fr))', gap: 10 }}>
            <div style={{ background: 'var(--adm-soft)', borderRadius: 8, padding: '12px 14px', display: 'flex', flexDirection: 'column', gap: 2 }}>
              <b style={{ fontSize: 20, color:'var(--lc-text-1)'}}>{fmtPoints(pkg.points_balance as number)}</b><span style={{ fontSize: 14, color:'var(--adm-faint)'}}>{t('usage.currentBalance')}</span>
            </div>
            <div style={{ background: 'var(--adm-soft)', borderRadius: 8, padding: '12px 14px', display: 'flex', flexDirection: 'column', gap: 2 }}>
              <b style={{ fontSize: 20, color: 'var(--adm-amber-tx)' }}>{fmtPoints(pkg.points_grants_left as number)}</b><span style={{ fontSize: 14, color: 'var(--adm-faint)' }}>{t('plans.balanceGrants')}</span>
            </div>
            <div style={{ background: 'var(--adm-soft)', borderRadius: 8, padding: '12px 14px', display: 'flex', flexDirection: 'column', gap: 2 }}>
              <b style={{ fontSize: 20, color: 'var(--adm-ok-tx)' }}>{fmtPoints(pkg.points_permanent_balance as number)}</b><span style={{ fontSize: 14, color: 'var(--adm-faint)' }}>{t('plans.balancePermanent')}</span>
            </div>
            <div style={{ background: 'var(--adm-soft)', borderRadius: 8, padding: '12px 14px', display: 'flex', flexDirection: 'column', gap: 2 }}>
              <b style={{ fontSize: 20, color:'var(--lc-text-1)'}}>{fmtPoints(pkg.points_used_month as number)}</b><span style={{ fontSize: 14, color:'var(--adm-faint)'}}>{t('plans.usedMonth')}</span>
            </div>
          </div>
          <div style={{ marginTop: 10, fontSize: 15, color: 'var(--adm-hint)' }}>
            {tpl('billing.myPackageCode', { code: (pkg.package_code as string) || '—' })}
            {pkgExpiresLabel ? ` · ${t('plans.expiresAt')}: ${pkgExpiresLabel}` : ''}
            {' · '}{tpl('billing.myPackageBalance', { balance: pkg.balance_sentences_approx ?? pkg.sentence_balance ?? '—' })}
          </div>
          {(() => {
            // ★ 自动续费（#41）：仅对「有到期日的正式订阅」开放（体验包与不限期订阅无续费概念）。
            //   开启后每日扫描在到期前 3 天生成同包续费订单并站内信提醒付款——不代扣，
            //   免密周期扣款需与渠道另签协议，故界面文案明确「不会自动扣款」。
            const code = (pkg.package_code as string) || ''
            if (!code || code === 'trial' || !pkgExpiresLabel) return null
            return (
              <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap', marginTop: 8 }}>
                <span style={{ fontSize: 16, color: 'var(--adm-hint)' }}>{t('plans.autoRenewLabel')}</span>
                <Switch checked={!!pkg.auto_renew} disabled={autoRenewBusy} onChange={(e) => { void saveAutoRenew(e.target.checked) }} />
                <span style={{ fontSize: 15, color: 'var(--adm-faint)' }}>{t('plans.autoRenewHint')}</span>
              </div>
            )
          })()}
          {(() => {
            // ★ 宽限期提示（#74，2026-09-23）：订阅已到期但开了自动续费时，后端在宽限期内
            //   保留订阅身份与剩余额度（/api/me/package 的 in_grace/grace_expires）。
            //   提示条沿用本页 exhaustedHint 的 warn 令牌口径（--adm-warn-*，不写字面色值）。
            if (!pkg.in_grace) return null
            return (
              <div style={{ marginTop: 10, padding: '10px 14px', borderRadius: 8, background: 'var(--adm-warn-bg)', border: '1.2px solid var(--adm-warn-bd)', fontSize: 15, color: 'var(--adm-warn-tx)', lineHeight: 1.7 }}>
                <b>{tpl('plans.graceTitle', { date: toLocalDate(pkg.grace_expires as string) || '—' })}</b>
                <div style={{ marginTop: 2 }}>{t('plans.graceBody')}</div>
              </div>
            )
          })()}
          {(() => {
            const total = Number(pkg.points_balance ?? 0)
            const hasPlan = !!(pkg.package_code && pkg.package_code !== 'trial')
            if (total > 0 || hasPlan) return null
            return (
              <div style={{ marginTop: 10, padding: '10px 14px', borderRadius: 8, background: 'var(--adm-warn-bg)', border: '1.2px solid var(--adm-warn-bd)', fontSize: 15, color: 'var(--adm-warn-tx)', lineHeight: 1.7 }}>
                {t('plans.exhaustedHint')}
                <div style={{ display: 'flex', gap: 6, alignItems: 'center', flexWrap: 'wrap', marginTop: 6 }}>
                  <Button size="sm" variant="secondary" onClick={() => { document.getElementById('plans-shop')?.scrollIntoView({ behavior: 'smooth' }) }}>{t('plans.goSubscribe')}</Button>
                  <Button size="sm" variant="secondary" onClick={() => { document.getElementById('plans-topup')?.scrollIntoView({ behavior: 'smooth' }) }}>{t('plans.goTopup')}</Button>
                </div>
              </div>
            )
          })()}
        </Panel>
      )}

      {!isSuper && (
        <Panel id="plans-shop" title={t('plans.nav.shop')}>
          {planGroups.map((g) => (
            <div key={g.type}>
              <div style={{ fontWeight: 600, fontSize: 16, color: 'var(--adm-hint)', margin: '10px 0 6px' }}>{g.title}</div>
              <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill,minmax(190px,1fr))', gap: 12 }}>
                {g.items.map((pl) => (
                  <div key={pl.id} style={{ border: '1.2px solid var(--adm-line)', borderRadius: 8, padding: 14, display: 'flex', flexDirection: 'column', gap: 6, background: 'var(--adm-card)' }}>
                    <div style={{ fontWeight: 600, fontSize: 16 }}>{pl.name}</div>
                    {/* ★ #75 多币种报价：非 CNY 报价时大字走后端换算好的 price_display（本币价），
                        人民币原价降级为辅助行——下单实扣仍是 ¥ 金额（amount_money），这里只改"看"的口径 */}
                    <div style={{ fontSize: 22, fontWeight: 700, color:'var(--lc-text-1)' }}>{pl.quote_currency && pl.quote_currency !== 'CNY' ? fmtQuoteMoney(Number(pl.price_display ?? pl.price_money), String(pl.quote_currency)) : `¥${pl.price_money}`}<small style={{ fontSize: 14, color:'var(--adm-faint)', fontWeight: 400 }}>{pl.ptype ==='paid'? ` /${pl.duration_days}d` :''}</small></div>
                    {pl.quote_currency && pl.quote_currency !== 'CNY' && (
                      <div style={{ fontSize: 14, color: 'var(--adm-faint)' }}>≈ ¥{pl.price_money}</div>
                    )}
                    {pl.ptype === 'paid' && Number(pl.price_money) > 0 && (
                      <div style={{ fontSize: 14, color: 'var(--lc-warn)' }}>{t('plans.halfOffBadge')}</div>
                    )}
                    <ul style={{ margin: '0 0 4px 16px', padding: 0, fontSize: 15, color: 'var(--adm-hint)', lineHeight: 1.7 }}>
                      <li>{Number(pl.points) > 0 ? tpl('billing.pkgPoints', { n: pl.points }) : tpl('billing.pkgSentences', { n: pl.sentences })}</li>
                      <li>{t('packages.type.' + pl.ptype)}</li>
                    </ul>
                    <Button variant="primary" onClick={() => {
                      if (isUpgradePlan(pl)) upgrade(pl)
                      else subscribe(pl)
                    }}>{isUpgradePlan(pl) ? t('plans.upgrade') : t('billing.subscribeNow')}</Button>
                  </div>
                ))}
                {!g.items.length && <div style={{ color: 'var(--adm-faint)', fontSize: 15 }}>{t('billing.noPlans')}</div>}
              </div>
            </div>
          ))}
        </Panel>
      )}

      {!isSuper && (
        <Panel id="plans-topup" title={t('plans.nav.topup')}>
          <div style={{ fontSize: 15, color: 'var(--adm-hint)', marginBottom: 8 }}>{t('billing.onlineTopUpHint')}</div>
          <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}>
            <select className="lc-select" value={chForm.channel} onChange={(e) => setChForm({ ...chForm, channel: e.target.value })} style={{ width: 200 }}>
              {chOptions.map((o) => <option key={o.value} value={o.value}>{o.label}</option>)}
            </select>
            <input className="lc-input" type="number" value={String(chForm.points)} onChange={(e) => setChForm({ ...chForm, points: Number(e.target.value) || 0 })} placeholder={t('billing.pointsAmount')} style={{ width: 180 }} />
            {/* ★ 优惠券（#41）：券码选填，试算走服务端同一算法；下单时随 payCreate/packageSubscribe 提交 */}
            <input className="lc-input" value={couponCode} onChange={(e) => { setCouponCode(e.target.value.toUpperCase()); setCouponQuote(null) }}
              placeholder={t('plans.couponLabel') + '（' + t('plans.couponPh') + '）'} style={{ width: 200 }} aria-label={t('plans.couponLabel')} />
            <Button variant="secondary" disabled={couponBusy || !couponCode.trim()} onClick={() => void previewCoupon()}>{t('plans.couponPreview')}</Button>
            <Button variant="primary" disabled={chLoading} onClick={openCheckout}>{chLoading ? t('billing.ordering') : t('billing.goPay')}</Button>
          </div>
          {couponQuote && (
            <p style={{ color: 'var(--adm-ok-tx, var(--lc-text-1))', fontSize: 16, marginTop: 8 }}>
              {tpl('plans.couponQuote', { discount: Number(couponQuote.discount_money ?? 0).toFixed(2), pay: Number(couponQuote.pay_money ?? 0).toFixed(2) })}
            </p>
          )}
          {curOrder && curOrder.status === 'pending' && (
            <p style={{ color: 'var(--lc-text-1)', fontSize: 15, marginTop: 8 }}>{tpl('billing.currentOrder', { orderNo: curOrder.order_no, amount: fmtPoints(curOrder.amount_points), money: Number(curOrder.amount_money ?? 0).toFixed(2) })}</p>
          )}
          {/* ★ #75：外币报价租户的收银台辅助行——把人民币应收折算成本币"约价"给客户看。
              缺该币种倍率时整行不渲染（fail-closed：宁可不显示，也不给一个错的近似值）；
              文案必须点明"实扣人民币"，避免客户按本币金额付款造成资金差错。 */}
          {curOrder && curOrder.status === 'pending' && myQuote.code !== 'CNY' && Number(myQuote.rates[myQuote.code] || 0) > 0 && Number(curOrder.amount_money ?? 0) > 0 && (
            <p style={{ color: 'var(--adm-faint)', fontSize: 15, marginTop: 2 }}>{tpl('billing.quoteApprox', { local: fmtQuoteMoney(cnyToQuote(Number(curOrder.amount_money), Number(myQuote.rates[myQuote.code])), myQuote.code) })}</p>
          )}
        </Panel>
      )}

      <Panel title={t('billing.ordersTitle')}>
        {/* 数据表格 */}
        <div style={{ maxHeight: 260, overflowY: 'auto' }}>
          <DataTable rowKey={(row) => String((row as Any).id)} rows={orders} emptyText={t('plans.noOrder')}
                 columns={[
                   { key: 'order_no', title: t('billing.colOrderNo'), width: 150 },
                   { key: 'amount_points', title: t('billing.colPoints'), width: 110, render: (row) => fmtPoints(Number((row as Any).amount_points)) },
                   { key: 'amount_money', title: t('billing.colAmount'), width: 100, render: (row) => tpl('billing.yuan', { amount: Number((row as Any).amount_money ?? 0).toFixed(2) }) },
                   { key: 'status', title: t('billing.colStatus'), width: 110, render: (row) => <StatusPill tone={statusTheme((row as Any).status)}>{orderStatusLabel((row as Any).status, t)}</StatusPill> },
                   { key: 'op', title: '', width: 170, render: (row) => {
                       const r = row as Any
                       return r.status === 'paid'
                         ? (<div style={{ display: 'flex', gap: 4, alignItems: 'center', flexWrap: 'wrap' }}>
                             <Link onClick={() => setInvDlg({ order: r, title: '', taxNo: '' })}>{t('billing.invoiceIssue')}</Link>
                             {isSuper && <Link tone="danger" onClick={() => void refundOrder(r)}>{t('billing.refund')}</Link>}
                           </div>)
                         : (r.status === 'pending' ? <Button size="sm" variant="secondary" onClick={() => resumePay(r)}>{t('plans.orderContinue')}</Button> : null)
                     } },
                 ]} />
        </div>
        <h4 style={{ margin: '14px 0 6px' }}>{t('billing.invoiceMgmt')}</h4>
        {/* 数据表格 */}
        <div style={{ maxHeight: 220, overflowY: 'auto' }}>
          <DataTable rowKey={(row) => String((row as Any).id)} rows={invoices} emptyText={t('billing.noInvoices')}
                 columns={[
                   { key: 'invoice_no', title: t('billing.colInvoiceNo') },
                   { key: 'title', title: t('billing.colTitle') },
                   { key: 'amount_money', title: t('billing.colAmountYuan'), width: 110, render: (row) => Number((row as Any).amount_money ?? 0).toFixed(2) },
                   { key: 'op', title: '', width: 90, render: (row) => ((row as Any).status === 'void' ? null
                       : <Link tone="danger" onClick={() => void voidInvoice(row as Any)}>{t('billing.void')}</Link>) },
                 ]} />
        </div>
      </Panel>

      <Panel title={t('plans.nav.quota')}>
        <div style={{ fontSize: 15, color: 'var(--adm-hint)', marginBottom: 8 }}>{t('billing.quotaHint')}</div>
        <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}>
          <input className="lc-input" type="number" value={num(quotaForm.qps)} onChange={(e) => setQuotaForm({ ...quotaForm, qps: Number(e.target.value) || 0 })} placeholder={t('billing.quotaQps')} style={{ width: 140 }} />
          <input className="lc-input" type="number" value={num(quotaForm.concurrent)} onChange={(e) => setQuotaForm({ ...quotaForm, concurrent: Number(e.target.value) || 0 })} placeholder={t('billing.quotaConcurrent')} style={{ width: 140 }} />
          <input className="lc-input" type="number" value={num(quotaForm.max_daily_chars)} onChange={(e) => setQuotaForm({ ...quotaForm, max_daily_chars: Number(e.target.value) || 0 })} placeholder={t('billing.quotaDailyChars')} style={{ width: 160 }} />
          <input className="lc-input" type="number" value={num(quotaForm.max_daily_points)} onChange={(e) => setQuotaForm({ ...quotaForm, max_daily_points: Number(e.target.value) || 0 })} placeholder={t('billing.quotaDailyPoints')} style={{ width: 160 }} />
          <Button onClick={saveQuota}>{t('billing.saveQuota')}</Button>
        </div>
      </Panel>

      {isSuper && (
        <Panel title={t('plans.funnelTitle')}>
          <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap', marginBottom: 8 }}>
            {[7, 30, 90].map((d) => (
              <Button key={d} size="sm" variant={funnelDays === d ? 'primary' : 'secondary'} onClick={() => setFunnelDays(d)}>{t('plans.funnelDays').replace('{d}', String(d))}</Button>
            ))}
            <span style={{ fontSize: 14, color: 'var(--adm-faint)' }}>{t('plans.funnelHint')}</span>
          </div>
          {/* 数据表格 */}
          <DataTable rowKey={(row) => String((row as Any).source)} rows={funnelRows} emptyText={t('plans.funnelEmpty')}
                 columns={[
                   { key: 'source', title: t('plans.funnelColSource'), width: 160 },
                   { key: 'registered', title: t('plans.funnelColReg'), width: 90 },
                   { key: 'activated', title: t('plans.funnelColAct'), width: 90 },
                   { key: 'exhausted', title: t('plans.funnelColExh'), width: 90 },
                   { key: 'first_pay', title: t('plans.funnelColPay'), width: 90 },
                   { key: 'renewed', title: t('plans.funnelColRenew'), width: 90 },
                 ]} />
        </Panel>
      )}

      {/* 商业运营参数（仅超管 L4）：计费强制开关 + 敏感词兑底闸（S8）+ 试用额度与加价系数。
          两个开关各自独立保存（saveEnforce / saveSensitiveGate），避免一次改动连带写回另一项。
          状态文字 2026-09-18 起改为「开=正文浅色加粗 / 关=灰」，不再用绿灰双色区分。 */}
      {isSuper && (
        <Panel title={t('plans.nav.ops')}>
          <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}>
            <Switch checked={billingEnforced} onChange={(e) => setBillingEnforced(e.target.checked)} />
            <span style={{ color: billingEnforced ?'var(--lc-text-1)':'var(--lc-text-3)', fontWeight: 600 }}>{billingEnforced ? t('billing.enforcedOn') : t('billing.enforcedOff')}</span>
            <Button onClick={saveEnforce}>{t('common.save')}</Button>
            <span style={{ fontSize: 15, color: 'var(--adm-hint)', marginInlineStart: 16 }}>{t('packages.sensitiveGateLabel')}</span>
            <Switch checked={sensitiveGate} onChange={(e) => setSensitiveGate(e.target.checked)} />
            <Button onClick={saveSensitiveGate}>{t('common.save')}</Button>
          </div>
          <div style={{ marginTop: 12 }}>
            <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}>
              <span style={{ fontSize: 15, color: 'var(--adm-hint)' }}>{t('packages.trialPointsLabel')}</span>
              <input className="lc-input" type="number" value={num(freeTrialPoints)} onChange={(e) => setFreeTrialPoints(Number(e.target.value) || 0)} style={{ width: 120 }} />
              <span style={{ fontSize: 15, color: 'var(--adm-hint)' }}>{t('packages.trialDaysLabel')}</span>
              <input className="lc-input" type="number" value={num(freeTrialDays)} onChange={(e) => setFreeTrialDays(Number(e.target.value) || 0)} style={{ width: 80 }} />
              <span style={{ fontSize: 15, color: 'var(--adm-hint)', marginInlineStart: 12 }}>{t('packages.markupLabel')}</span>
              <input className="lc-input" type="number" value={num(markupMultiplier)} onChange={(e) => setMarkupMultiplier(Math.max(0, Number(e.target.value) || 0))} style={{ width: 120 }} />
              <Button onClick={saveBillingParams}>{t('common.save')}</Button>
            </div>
            <div style={{ fontSize: 14, color: 'var(--adm-faint)', marginTop: 6 }}>{t('packages.markupHint')}</div>
          </div>
          <div style={{ marginTop: 12 }}>
            <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}>
              <span style={{ fontSize: 15, color: 'var(--adm-hint)' }}>{t('packages.payModeTitle')}</span>
              <select className="lc-select" value={payModeCfg} onChange={(e) => setPayModeCfg(e.target.value)} style={{ width: 200 }}>
                <option value="mock">{t('packages.payMock')}</option>
                <option value="sdk">{t('packages.paySdk')}</option>
                <option value="static_qr">{t('packages.payStaticQR')}</option>
              </select>
              <Button onClick={savePayMode}>{t('common.save')}</Button>
            </div>
          </div>
          {payModeCfg === 'static_qr' && (
            <div style={{ marginTop: 8 }}>
              <div style={{ fontSize: 14, color: 'var(--adm-faint)', marginBottom: 4 }}>{t('packages.staticQRHint')}</div>
              <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}>
                <input className="lc-input" value={staticQRImage} onChange={(e) => setStaticQRImage(e.target.value)} placeholder={t('packages.staticQRPlaceholder')} style={{ width: 360 }} />
                <input type="file" accept=".png,.jpg,.jpeg,.gif,.webp" style={{ fontSize: 14 }} onChange={uploadStaticQR} disabled={qrUploading} />
                {qrUploading && <span style={{ fontSize: 14, color: 'var(--adm-faint)' }}>…</span>}
                <Button onClick={saveStaticQR}>{t('common.save')}</Button>
              </div>
              {isImage(staticQRImage) && (
                <div style={{ marginTop: 8, display: 'inline-block', border: '1.2px dashed var(--lc-border-card)', borderRadius: 8, padding: 8 }}>
                  <img src={staticQRImage} alt="qr" style={{ maxWidth: 160, maxHeight: 160, borderRadius: 6, display: 'block' }} />
                </div>
              )}
            </div>
          )}
          {/* ★ USDT（2026-09-15）：超管后台配置 USDT 收款（开关/链/钱包地址链接/汇率/确认数） */}
          <div style={{ marginTop: 12, borderTop: '1px dashed var(--adm-line)', paddingTop: 10 }}>
            <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}>
              <span style={{ fontWeight: 600, fontSize: 15 }}>{t('billing.usdtSection')}</span>
              <Switch checked={usdtCfg.usdt_enabled === '1'} onChange={(e) => setUsdtCfg({ ...usdtCfg, usdt_enabled: e.target.checked ? '1' : '0' })} />
              <span style={{ fontSize: 14, color: 'var(--adm-hint)' }}>{usdtOn ? t('billing.usdtOn') : t('billing.usdtOff')}</span>
              <span style={{ fontSize: 14, color: 'var(--adm-hint)', marginInlineStart: 12 }}>{t('billing.usdtTail')}</span>
              <Switch checked={usdtCfg.usdt_tail_enabled === '1'} onChange={(e) => setUsdtCfg({ ...usdtCfg, usdt_tail_enabled: e.target.checked ? '1' : '0' })} />
              <span style={{ fontSize: 14, color: 'var(--adm-hint)', marginInlineStart: 12 }}>{t('billing.usdtAuto')}</span>
              <Switch checked={usdtCfg.usdt_auto_settle === '1'} onChange={(e) => setUsdtCfg({ ...usdtCfg, usdt_auto_settle: e.target.checked ? '1' : '0' })} />
              <Button onClick={saveUSDT}>{t('common.save')}</Button>
            </div>
            <div style={{ fontSize: 14, color: 'var(--adm-faint)', margin: '4px 0 8px' }}>{t('billing.usdtHint')}</div>
            <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap', marginBottom: 6 }}>
              <span style={{ fontSize: 15, color: 'var(--adm-hint)' }}>{t('billing.usdtChains')}</span>
              <select className="lc-select" multiple value={String(usdtCfg.usdt_chains || '').split(',').map((c: string) => c.trim()).filter(Boolean)}
                      onChange={(e) => setUsdtCfg({ ...usdtCfg, usdt_chains: Array.from(e.target.selectedOptions).map((o) => o.value).join(',') })} style={{ minWidth: 260 }}
                      >
                {['trc20', 'erc20', 'bep20'].map((c) => <option key={c} value={c}>{usdtChainLabel(c)}</option>)}
              </select>
              <span style={{ fontSize: 15, color: 'var(--adm-hint)', marginInlineStart: 10 }}>{t('billing.usdtRate')}</span>
              <input className="lc-input" type="number" value={num(usdtCfg.usdt_rate_fen_per_usdt)} onChange={(e) => setUsdtCfg({ ...usdtCfg, usdt_rate_fen_per_usdt: Number(e.target.value) || 0 })} style={{ width: 120 }} />
            </div>
            {['trc20', 'erc20', 'bep20'].map((c) => (
              <div key={c} style={{ display: 'flex', alignItems: 'center', gap: 8, marginTop: 4, flexWrap: 'wrap' }}>
                <span style={{ fontSize: 14, color: 'var(--adm-hint)', width: 130 }}>{usdtChainLabel(c)}</span>
                <input className="lc-input" value={String(usdtCfg['usdt_addr_' + c] || '')} onChange={(e) => setUsdtCfg({ ...usdtCfg, ['usdt_addr_' + c]: e.target.value })}
                       placeholder={t('billing.usdtAddrPh')} style={{ width: 340 }} />
                <span style={{ fontSize: 14, color: 'var(--adm-faint)' }}>{t('billing.usdtConf')}</span>
                <input className="lc-input" type="number" value={num(usdtCfg['usdt_confirmations_' + c])} onChange={(e) => setUsdtCfg({ ...usdtCfg, ['usdt_confirmations_' + c]: Number(e.target.value) || 0 })} style={{ width: 70 }} />
                {usdtAddrURL(c, String(usdtCfg['usdt_addr_' + c] || '')) ? (
                  <a href={usdtAddrURL(c, String(usdtCfg['usdt_addr_' + c] || ''))} target="_blank" rel="noreferrer" style={{ fontSize: 14 }}>{t('billing.usdtWalletLink')} ↗</a>
                ) : <span style={{ fontSize: 14, color: 'var(--adm-faint)' }}>{t('billing.usdtNoAddr')}</span>}
              </div>
            ))}
          </div>
          {/* ★ 2026-09-22 支付渠道凭据：微信 Native v3 / 支付宝当面付的商户参数改为管理台可配
              （敏感项加密落库、掩码回显）。挂在「运营配置」面板内而不是一级导航——它和
              pay_mode / 静态码 / USDT 同属「怎么收款」这一件事，拆开放反而找不到。
              环境变量接管的栏位置灰并标出变量名：优先级是 env > 库配置（应急/灰度闸门），
              不写清楚就会出现「保存成功但仍在用旧密钥」这种极难自查的坑。 */}
          <div style={{ marginTop: 12, borderTop: '1px dashed var(--adm-line)', paddingTop: 10 }}>
            <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}>
              <span style={{ fontWeight: 600, fontSize: 16 }}>{t('billing.paychSection')}</span>
              <Button onClick={savePayChannels} disabled={!payCfgReady}>{t('common.save')}</Button>
            </div>
            <div style={{ fontSize: 15, color: 'var(--adm-faint)', margin: '4px 0 8px' }}>{t('billing.paychHint')}</div>
            <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
              <span style={{ fontSize: 15, color: 'var(--adm-hint)', width: 170 }}>{t('billing.paychNotifyBase')}</span>
              {payChInput('paych_notify_base', false)}
            </div>
            {PAY_CH_GROUPS.map((g) => (
              <div key={g.switchKey} style={{ marginTop: 8 }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
                  <span style={{ fontWeight: 600, fontSize: 15 }}>{t(g.title)}</span>
                  <span style={{ fontSize: 15, color: 'var(--adm-hint)' }}>{t('billing.paychEnabled')}</span>
                  <select className="lc-select" value={String(payCfg[g.switchKey] ?? '')} style={{ width: 160 }}
                          onChange={(e) => setPayCfg((c) => ({ ...c, [g.switchKey]: e.target.value }))}
                          >
                    <option value="">{t('billing.paychFollow')}</option>
                    <option value="1">{t('common.active')}</option>
                    <option value="0">{t('common.disabled')}</option>
                  </select>
                </div>
                {g.fields.map((f) => (
                  <div key={f.key} style={{ display: 'flex', alignItems: 'flex-start', gap: 8, marginTop: 4, flexWrap: 'wrap' }}>
                    <span style={{ fontSize: 15, color: 'var(--adm-hint)', width: 170 }}>{t(f.label)}</span>
                    {payChInput(f.key, !!f.area)}
                  </div>
                ))}
              </div>
            ))}
          </div>
          {/* ★ 2026-09-23（#75）多币种报价：报价币种 + 汇率倍率（超管口）。只改客户「看到的本币价」，
              收单事实源恒为人民币（外币实扣需外币收单签约，未签约前禁止扩口径）；
              下单时币种/倍率会快照进 orders（currency/fx_rate/money_cny），所以汇率改动不影响历史单。
              整表覆盖保存：清空某币种倍率 = 该币种不再报价（前端换算拿不到倍率即回落 CNY）。
              ★ 2026-09-22 用户决策关闭封存：整块以「后端白名单含外币币种」为渲染条件——
              store 层 quoteFeatureOpen=false 时 GET 只回 ['CNY']，本区块自动隐藏；
              重开（后端翻开关）后界面零改动复原。 */}
          {quoteSupported.some((c) => c !== 'CNY') && (
          <div style={{ marginTop: 12, borderTop: '1px dashed var(--adm-line)', paddingTop: 10 }}>
            <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}>
              <span style={{ fontWeight: 600, fontSize: 16 }}>{t('billing.quoteSection')}</span>
              <Button onClick={saveQuoteCfg} disabled={!quoteReady}>{t('common.save')}</Button>
            </div>
            <div style={{ fontSize: 15, color: 'var(--adm-faint)', margin: '4px 0 8px' }}>{t('billing.quoteHint')}</div>
            <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
              <span style={{ fontSize: 15, color: 'var(--adm-hint)', width: 170 }}>{t('billing.quoteCurrencyLabel')}</span>
              <select className="lc-select" style={{ width: 160 }} value={quoteCfg.currency}
                      disabled={!!quoteEnv.quote_currency}
                      onChange={(e) => setQuoteCfg((c) => ({ ...c, currency: e.target.value }))}
              >
                {(quoteSupported.length ? quoteSupported : ['CNY']).map((c) => <option key={c} value={c}>{c}</option>)}
              </select>
              {quoteEnv.quote_currency ? <span style={{ fontSize: 14, color: 'var(--adm-amber-tx)' }}>{quoteEnv.quote_currency} · {t('billing.paychEnvOn')}</span> : null}
            </div>
            <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginTop: 8, flexWrap: 'wrap' }}>
              <span style={{ fontSize: 15, color: 'var(--adm-hint)', width: 170 }}>{t('billing.quoteRatesLabel')}</span>
              <span style={{ fontSize: 14, color: 'var(--adm-faint)' }}>{t('billing.quoteCnyNote')}</span>
            </div>
            <div style={{ display: 'flex', flexWrap: 'wrap', gap: '6px 16px', marginTop: 4, marginInlineStart: 170 }}>
              {quoteSupported.filter((c) => c !== 'CNY').map((c) => (
                <label key={c} style={{ display: 'inline-flex', alignItems: 'center', gap: 6, fontSize: 15 }}>
                  <b>{c}</b>
                  <input className="lc-input" type="number" step="0.0001" min="0" style={{ width: 110 }}
                         placeholder={t('billing.quoteRatePh')} disabled={!!quoteEnv.fx_rates}
                         value={quoteCfg.rates[c] ?? ''}
                         onChange={(e) => setQuoteCfg((cur) => ({ ...cur, rates: { ...cur.rates, [c]: e.target.value } }))} />
                </label>
              ))}
            </div>
            {quoteEnv.fx_rates ? (
              <div style={{ fontSize: 14, color: 'var(--adm-amber-tx)', marginTop: 6 }}>{quoteEnv.fx_rates} · {t('billing.paychEnvOn')}</div>
            ) : null}
          </div>
          )}
        </Panel>
      )}

      {isSuper && (
        <Panel title={t('plans.nav.pkgMgmt')}>
          <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}>
            <input className="lc-input" value={String(pkgForm.code || '')} onChange={(e) => setPkgForm({ ...pkgForm, code: e.target.value })} placeholder={t('packages.code')} style={{ width: 140 }} />
            <input className="lc-input" value={String(pkgForm.name || '')} onChange={(e) => setPkgForm({ ...pkgForm, name: e.target.value })} placeholder={t('packages.name')} style={{ width: 160 }} />
            <select className="lc-select" value={String(pkgForm.ptype || 'paid')} onChange={(e) => setPkgForm({ ...pkgForm, ptype: e.target.value })} style={{ width: 140 }}>
              <option value="paid">{t('packages.type.paid')}</option>
              <option value="increment">{t('packages.type.increment')}</option>
              <option value="free">{t('packages.type.free')}</option>
            </select>
            <input className="lc-input" type="number" value={num(pkgForm.sentences)} onChange={(e) => setPkgForm({ ...pkgForm, sentences: Number(e.target.value) || 0 })} placeholder={t('packages.sentences')} style={{ width: 120 }} />
            <input className="lc-input" type="number" value={num(pkgForm.points)} onChange={(e) => setPkgForm({ ...pkgForm, points: Number(e.target.value) || 0 })} placeholder={t('packages.points')} style={{ width: 110 }} />
            <input className="lc-input" type="number" value={num(pkgForm.price_money)} onChange={(e) => setPkgForm({ ...pkgForm, price_money: Number(e.target.value) || 0 })} placeholder={t('packages.price')} style={{ width: 120 }} />
            <input className="lc-input" type="number" value={num(pkgForm.duration_days)} onChange={(e) => setPkgForm({ ...pkgForm, duration_days: Number(e.target.value) || 0 })} placeholder={t('packages.duration')} style={{ width: 120 }} />
            <Button onClick={createPkg}>{t('common.save')}</Button>
          </div>
          {/* 数据表格 */}
          <div style={{ marginTop: 10 }}>
            <DataTable rowKey={(row) => String((row as Any).id)} rows={pkgs}
                   columns={[
                     { key: 'code', title: 'code', width: 140 },
                     { key: 'name', title: t('packages.name') },
                     { key: 'ptype', title: t('packages.type'), width: 100, render: (row) => t('packages.type.' + (row as Any).ptype) },
                     { key: 'sentences', title: t('packages.sentences'), width: 90 },
                     { key: 'points', title: t('packages.points'), width: 90 },
                     { key: 'price_money', title: `¥${t('packages.price')}`, width: 90 },
                     { key: 'enabled', title: t('common.status'), width: 90, render: (row) =>
                       (row as Any).enabled
                         ? <Button size="sm" variant="secondary" onClick={() => togglePkg(row as Any)}>{t('common.active')}</Button>
                         : <Link onClick={() => togglePkg(row as Any)}>{t('common.disabled')}</Link> },
                     { key: 'op', title: '', width: 90, render: (row) =>
                       <Link tone="danger" onClick={() => deletePkg(row as Any)}>{t('common.delete')}</Link> },
                   ]} />
          </div>
        </Panel>
      )}

      {isSuper && (
        <Panel title={t('plans.nav.manual')}>
          {/* 数据表格（★ USDT：渠道列 + 链上线索（声明哈希/精确金额/浏览器外链）+ 确认收款需回填 tx_hash） */}
          <DataTable rowKey={(row) => String((row as Any).id)} rows={manualOrders} emptyText={t('billing.noManualOrders')}
                 columns={[
                   { key: 'order_no', title: t('billing.colOrderNo'), width: 150 },
                   { key: 'channel', title: t('billing.colChannel'), width: 78, render: (row) =>
                       (row as Any).channel === 'usdt' ? <Badge>USDT</Badge> : ((row as Any).channel === 'manual' ? t('billing.chStaticQR') : String((row as Any).channel || '—')) },
                   { key: 'amount_points', title: t('billing.colPoints'), width: 110, render: (row) => fmtPoints(Number((row as Any).amount_points)) },
                   { key: 'usdt', title: t('billing.usdtCol'), width: 240, render: (row) => {
                       const r = row as Any
                       const info = manualOrdersUsdt.current[String(r.id)]
                       if (r.channel !== 'usdt' || !info) return <span style={{ color: 'var(--adm-faint)' }}>—</span>
                       return (
                         <div style={{ fontSize: 14, lineHeight: 1.6 }}>
                           <div>{String(info.amount)} USDT · {usdtChainLabel(String(info.chain))}</div>
                           {info.declared ? (
                             <a href={String(info.url || '#')} target="_blank" rel="noreferrer" style={{ wordBreak: 'break-all' }}>
                               tx:{String(info.declared).slice(0, 14)}… ↗
                             </a>
                           ) : <span style={{ color: 'var(--adm-faint)' }}>{t('billing.usdtNoTx')}</span>}
                         </div>
                       )
                     } },
                   { key: 'tx_input', title: t('billing.usdtTxCol'), width: 220, render: (row) =>
                       (row as Any).channel === 'usdt'
                         ? <input className="lc-input" value={manualTxInputs[String((row as Any).id)] || ''} onChange={(e) => setManualTxInputs({ ...manualTxInputs, [String((row as Any).id)]: e.target.value })} placeholder={t('billing.usdtTxPh')} />
                         : <span style={{ color: 'var(--adm-faint)' }}>—</span> },
                   { key: 'tenant_id', title: t('billing.colTenant'), width: 80, render: (row) => `#${(row as Any).tenant_id}` },
                   { key: 'created_at', title: t('billing.colTime'), width: 165, render: (row) => fmtTime((row as Any).created_at as string) },
                   { key: 'op', title: '', width: 120, render: (row) =>
                     <Button size="sm" variant="secondary" onClick={() => confirmManual(row as Any)}>{t('billing.confirmPayment')}</Button> },
                 ]} />
        </Panel>
      )}

      <Dialog open={showCheckout} onCancel={closeCheckout} title={t('billing.checkout')}>
        {curOrder && curOrder.status === 'paid' ? (
          <div style={{ textAlign: 'center', padding: '10px 0' }}>
 <div style={{ width: 52, height: 52, lineHeight:'52px', borderRadius:'50%', background:'var(--adm-ok-bg)', color:'var(--adm-ok-tx)', fontSize: 28, margin:'0 auto 8px'}}></div>
            <p>{tpl('billing.paySuccess', { amount: fmtPoints(curOrder.amount_points) })}</p>
            <Button variant="primary" onClick={closeCheckout}>{t('billing.done')}</Button>
          </div>
        ) : (
          <div>
            {curOrder && (
              <div style={{ textAlign: 'center' }}>
                {curOrder.channel === 'usdt' && usdtPay ? (
                  /* ★ USDT 收款台：精确金额（含尾数）+ 地址 + pay_uri 二维码 + txid 声明 */
                  <div style={{ textAlign: 'start', display: 'flex', flexDirection: 'column', gap: 8 }}>
                    <div style={{ padding: '8px 12px', borderRadius: 8, background: 'var(--adm-soft)', textAlign: 'center' }}>
                      <div style={{ fontSize: 14, color: 'var(--adm-hint)' }}>{t('billing.usdtAmountLabel')}（{usdtChainLabel(String(usdtPay.chain))}）</div>
                      <div style={{ fontSize: 24, fontWeight: 700 }}>{String(usdtPay.amount)} USDT</div>
                      {String(usdtPay.tail) !== '0' && <div style={{ fontSize: 13, color: 'var(--adm-faint)' }}>{tpl('billing.usdtTailNote', { tail: String(usdtPay.tail) })}</div>}
                    </div>
                    {usdtQr && <img src={usdtQr} alt="usdt-qr" style={{ width: 168, height: 168, alignSelf: 'center', borderRadius: 8, border: '1.2px solid var(--adm-line)', background: '#fff' }} />}
                    <div>
                      <div style={{ fontSize: 14, color: 'var(--adm-hint)' }}>{t('billing.usdtAddress')}</div>
                      <div style={{ display: 'flex', gap: 6, alignItems: 'center' }}>
                        <code style={{ flex: 1, wordBreak: 'break-all', background: 'var(--adm-soft)', borderRadius: 6, padding: '6px 8px', fontSize: 14 }}>{String(usdtPay.address)}</code>
                        <Button size="sm" variant="secondary" onClick={() => { void navigator.clipboard.writeText(String(usdtPay.address)); void toastSuccess(t('billing.usdtCopied')) }}>{t('billing.usdtCopy')}</Button>
                      </div>
                    </div>
                    <div style={{ fontSize: 14, color: 'var(--adm-hint)', display: 'flex', justifyContent: 'space-between', flexWrap: 'wrap', gap: 4 }}>
                      <span>{tpl('billing.usdtExpires', { time: fmtTime(String(usdtPay.expires_at)) })}</span>
                      <span>{tpl('billing.usdtConfNeed', { n: Number(usdtPay.confirmations) || 0 })}</span>
                    </div>
                    <input className="lc-input" value={usdtTxInput} onChange={(e) => setUsdtTxInput(e.target.value)} placeholder={t('billing.usdtTxPh')} style={{ width: '100%' }} />
                    <div style={{ fontSize: 13, color: 'var(--adm-faint)' }}>{t('billing.usdtCheckoutHint')}</div>
                  </div>
                ) : curOrder.channel === 'manual' ? (
                  <div>
                    <div style={{ fontSize: 15, color: 'var(--adm-hint)', marginBottom: 6 }}>{t('billing.staticQR')}</div>
                    {isImage(curOrder.qr_content as string)
                      ? <img src={curOrder.qr_content} style={{ maxWidth: 200, borderRadius: 8, border: '1.2px solid var(--adm-line)', margin: '8px 0' }} alt="qr" />
                      : qrImg
                        ? <img src={qrImg} style={{ maxWidth: 200, borderRadius: 8, border: '1.2px solid var(--lc-border-card)', margin: '8px 0', background: '#fff' }} alt="qr" />
                        : <pre style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-all', background: 'var(--adm-soft)', borderRadius: 8, padding: 12, fontSize: 14, maxHeight: 140, overflow: 'auto' }}>{String(curOrder.qr_content)}</pre>}
                  </div>
                ) : (
                  qrImg
                    ? <img src={qrImg} style={{ maxWidth: 200, borderRadius: 8, border: '1.2px solid var(--lc-border-card)', margin: '8px 0', background: '#fff' }} alt="qr" />
                    : <pre style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-all', background: 'var(--adm-soft)', borderRadius: 8, padding: 12, fontSize: 14, maxHeight: 140, overflow: 'auto' }}>{String(curOrder.qr_content)}</pre>
                )}
                <p style={{ fontSize: 15, color: 'var(--adm-hint)' }}>{tpl('billing.orderNo', { orderNo: curOrder.order_no })}</p>
              </div>
            )}
            <div style={{ display: 'flex', gap: 10, justifyContent: 'center', flexWrap: 'wrap', marginTop: 12 }}>
              {curOrder?.channel === 'manual' && <Button variant="primary" disabled={chLoading} onClick={manualConfirm}>{chLoading ? t('billing.processing') : t('billing.iPaid')}</Button>}
              {curOrder?.channel === 'usdt' && (
                <Button variant="primary" disabled={chLoading || !usdtTxInput.trim()} onClick={manualConfirm}>{t('billing.usdtDeclare')}</Button>
              )}
              {curOrder?.channel === 'mock' && <Button variant="primary" disabled={chLoading} onClick={simulatePay}>{t('billing.mockCredit')}</Button>}
              {curOrder && <Button onClick={checkStatus}>{t('billing.refreshStatus')}</Button>}
            </div>
          </div>
        )}
      </Dialog>

      <Dialog open={!!invDlg} onCancel={() => setInvDlg(null)} title={t('billing.invoiceDialogTitle')}
               onConfirm={async () => {
                if (!invDlg) return
                const r = await billingInvoiceCreate({ order_id: Number(invDlg.order.id), title: invDlg.title, tax_no: invDlg.taxNo })
                if (toastResp(r, t('billing.invoiceApplied'))) setInvDlg(null)
              }}>
        <Field label={t('billing.invoiceTitleField')}><input className="lc-input" value={invDlg?.title || ''} onChange={(e) => setInvDlg((d) => (d ? { ...d, title: e.target.value } : d))} /></Field>
        <Field label={t('billing.invoiceTaxField')}><input className="lc-input" value={invDlg?.taxNo || ''} onChange={(e) => setInvDlg((d) => (d ? { ...d, taxNo: e.target.value } : d))} /></Field>
      </Dialog>
    </>
  )
}
