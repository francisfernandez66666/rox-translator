// ============================================================================
// components/admin/PlansP.tsx — 套餐中心面板
// 职责：计费配置、套餐订阅、充值、订单/发票、配额与超管商业包管理
// 从 panels_c.tsx 拆分
// 2026-09-18（UI 融合）：金额/余额等强调数字的品牌蓝兜底色改为暗色主题正文色
//   （统一落到 var(--lc-text-1)；旧 var(--td-brand-color-active, #E7E9EA) 兜底已无必要，
//   暗底上不再出现旧版深蓝）；收款台/静态码预览边框同步转暗；
//   订单标题、按钮文案的 emoji 前缀清理。计费、轮询、退款与权限判断逻辑均未动。
// 2026-09-18（组件迁移）：TDesign 组件整体迁移至项目自带 langcross 纯黑组件库
//   （Button / DataTable / Dialog / Switch / StatusPill / Badge / Link），业务逻辑不变。
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
  plans as apiPlans, myPackage, packageSubscribe, packageUpgrade,
  adminPackages, adminPackageCreate, adminPackageUpdate, adminPackageDelete,
  adminPackageSettings, adminPackageSettingsSave, adminQRUpload,
  request,
  authHeaders,
  API_BASE,
} from '@/api'
import { Panel, Field, toastResp, num } from './parts'
import { fmtTime } from '@/lib/ui'
import { useAdmin } from '@/stores/admin'
import { useT } from '@/i18n'

/** Any 计费 Hub 出参宽松别名 */
type Any = Record<string, any>

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
  const [quotaForm, setQuotaForm] = useState<Any>({ qps: 10, concurrent: 3, max_daily_chars: 0, max_daily_tokens: 0 })
  const [pkgs, setPkgs] = useState<Any[]>([])
  const [billingEnforced, setBillingEnforced] = useState(false)
  const [sensitiveGate, setSensitiveGate] = useState(true) // ★ S8 敏感词兑底闸开关
  const [freeTrialTokens, setFreeTrialTokens] = useState(300000)
  const [freeTrialDays, setFreeTrialDays] = useState(14)
  const [markupMultiplier, setMarkupMultiplier] = useState(1.5)
  const [tokensPerSentence, setTokensPerSentence] = useState(500)
  const [pointsTokensRate, setPointsTokensRate] = useState(300) // ★ S1 积分汇率（内部 token/积分，仅超管可见）
  const [staticQRImage, setStaticQRImage] = useState('')
  // ★ USDT（2026-09-15）：超管后台收款配置 + 收银台收款要素
  const [usdtCfg, setUsdtCfg] = useState<Any>({
    usdt_enabled: '0', usdt_auto_settle: '0', usdt_tail_enabled: '1',
    usdt_chains: 'trc20', usdt_rate_fen_per_usdt: 720,
    usdt_addr_trc20: '', usdt_addr_erc20: '', usdt_addr_bep20: '',
    usdt_confirmations_trc20: 19, usdt_confirmations_erc20: 12, usdt_confirmations_bep20: 15,
  })
  const usdtOn = usdtCfg.usdt_enabled === '1'
  const manualOrdersUsdt = useRef<Record<string, Any>>({})
  const [manualOrders, setManualOrders] = useState<Any[]>([])
  // ★ S4 增长漏斗（超管看板）：注册→激活→耗尽→首购→续费，按渠道聚合
  const [funnelDays, setFunnelDays] = useState(30)
  const [funnelRows, setFunnelRows] = useState<Any[]>([])
  const [invDlg, setInvDlg] = useState<null | { order: Any; title: string; taxNo: string }>(null)

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
  // loadQuota 拉取租户限流配额（QPS/并发/日字符/日 token）并回填表单
  const loadQuota = useCallback(async () => {
    const r: Any = await billingQuota()
    if (r.success) setQuotaForm({
      qps: (r.qps as number) || 10, concurrent: (r.concurrent as number) || 3,
      max_daily_chars: (r.max_daily_chars as number) || 0, max_daily_tokens: (r.max_daily_tokens as number) ?? 0,
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
      if (cfg.free_trial_tokens) setFreeTrialTokens(Number(cfg.free_trial_tokens))
      if (cfg.free_trial_days) setFreeTrialDays(Number(cfg.free_trial_days))
      if (typeof cfg.billing_markup_multiplier === 'number') setMarkupMultiplier(cfg.billing_markup_multiplier)
      if (cfg.estimate_tokens_per_sentence) setTokensPerSentence(Number(cfg.estimate_tokens_per_sentence))
      if (typeof cfg.points_tokens_rate === 'number') setPointsTokensRate(cfg.points_tokens_rate)
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
  }, [isSuper])

  // loadAll 计费面板整体刷新：租户先取套餐，再并行拉订单/发票/配额/商业包
  const loadAll = useCallback(async () => {
    if (!isSuper) await loadPackage()
    await Promise.all([loadOrders(), loadInvoices(), loadQuota(), loadPkgs()])
  }, [isSuper, loadPackage, loadOrders, loadInvoices, loadQuota, loadPkgs])
  useEffect(() => { void loadAll() }, [loadAll])

    // subscribe 订阅套餐：建单→弹收款台
async function subscribe(pl: Any) {
    const r: Any = await packageSubscribe(String(pl.code))
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
    // openCheckout 打开收款弹窗（收款码/金额/复制）
async function openCheckout() {
    if (Number(chForm.points) <= 0) return
    setChLoading(true)
    try {
      const rawCh = chForm.channel === 'auto' ? '' : String(chForm.channel)
      const channel = rawCh.startsWith('usdt:') ? 'usdt' : rawCh
      if (channel === 'usdt') chForm.usdt_chain = rawCh.slice(5)
      const r: Any = await payCreate(channel === 'usdt'
        ? { points: Number(chForm.points), channel, usdt_chain: String(chForm.usdt_chain || '') }
        : { points: Number(chForm.points), channel })
      if (!toastResp(r)) return
      const o = r.order as Any
      setOrder(o); setShowCheckout(true)
      setUsdtPay((r.usdt_pay as Any) || null); setUsdtTxInput('')
      if (o && o.channel !== 'manual') startPolling()
    } catch (e: any) {
      void toastError(e?.message || t('common.fail'))
    } finally { setChLoading(false) }
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
  useEffect(() => {
    let alive = true
    setUsdtQr('')
    const uri = usdtPay?.pay_uri as string | undefined
    if (uri) {
      void (async () => {
        try {
          const res = await fetch(`${API_BASE}/api/qr/render?text=${encodeURIComponent(uri)}`, { headers: authHeaders() })
          if (!res.ok) return
          const blob = await res.blob()
          if (alive) setUsdtQr(URL.createObjectURL(blob))
        } catch { /* 渲染失败回退文本 */ }
      })()
    }
    return () => { alive = false }
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
          if (!res.ok) return
          const blob = await res.blob()
          if (alive) setQrImg(URL.createObjectURL(blob))
        } catch { /* 渲染失败则回退文本展示 */ }
      })()
    }
    return () => { alive = false }
  }, [curOrder?.qr_content])

    // saveQuota 保存租户配额（qps/并发）
async function saveQuota() {
    await billingQuotaSave({
      qps: Math.max(1, Number(quotaForm.qps) || 0),
      concurrent: Math.max(1, Number(quotaForm.concurrent) || 0),
      max_daily_chars: Math.max(0, Number(quotaForm.max_daily_chars) || 0),
      max_daily_tokens: Math.max(0, Number(quotaForm.max_daily_tokens) || 0),
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
    // saveBillingParams S1 积分汇率 + S3 一次性邮箱黑名单保存
async function saveBillingParams() {
    if (!(freeTrialTokens > 0)) { void toastWarn(t('packages.trialTokensInvalid')); return }
    if (!(freeTrialDays > 0)) { void toastWarn(t('packages.trialDaysInvalid')); return }
    if (!(markupMultiplier >= 1)) { void toastWarn(t('packages.markupInvalid')); return }
    if (!(tokensPerSentence > 0)) { void toastWarn(t('packages.rateInvalid')); return }
    if (!(pointsTokensRate > 0)) { void toastWarn(t('packages.rateInvalid')); return }
    const r: Any = await adminPackageSettingsSave({
      free_trial_tokens: freeTrialTokens, free_trial_days: freeTrialDays,
      billing_markup_multiplier: markupMultiplier, estimate_tokens_per_sentence: tokensPerSentence,
      points_tokens_rate: pointsTokensRate,
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
    { label: t('billing.chMock'), value: 'mock' },
  ]

  return (
    <>
      {/* 当前套餐概览（租户视角；超管在平台上下文无需看本租户余额，故 !isSuper 才渲染） */}
      {!isSuper && (
        <Panel title={t('plans.nav.current')}>
          {/* 四张余额卡：全部走积分口径（fmtPoints 折 token→积分，公开界面不露 token 裸值）。
              可用余额/本月已用为主题色（兜底 #E7E9EA）、剩余赠送为琥珀色、永久额度为成功色。 */}
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit,minmax(150px,1fr))', gap: 10 }}>
            <div style={{ background: 'var(--adm-soft)', borderRadius: 8, padding: '12px 14px', display: 'flex', flexDirection: 'column', gap: 2 }}>
              <b style={{ fontSize: 20, color:'var(--lc-text-1)'}}>{fmtPoints(pkg.balance_tokens as number)}</b><span style={{ fontSize: 12, color:'var(--adm-faint)'}}>{t('usage.currentBalance')}</span>
            </div>
            <div style={{ background: 'var(--adm-soft)', borderRadius: 8, padding: '12px 14px', display: 'flex', flexDirection: 'column', gap: 2 }}>
              <b style={{ fontSize: 20, color: 'var(--adm-amber-tx)' }}>{fmtPoints(pkg.sub_grants_left as number)}</b><span style={{ fontSize: 12, color: 'var(--adm-faint)' }}>{t('plans.balanceGrants')}</span>
            </div>
            <div style={{ background: 'var(--adm-soft)', borderRadius: 8, padding: '12px 14px', display: 'flex', flexDirection: 'column', gap: 2 }}>
              <b style={{ fontSize: 20, color: 'var(--adm-ok-tx)' }}>{fmtPoints(pkg.permanent_balance as number)}</b><span style={{ fontSize: 12, color: 'var(--adm-faint)' }}>{t('plans.balancePermanent')}</span>
            </div>
            <div style={{ background: 'var(--adm-soft)', borderRadius: 8, padding: '12px 14px', display: 'flex', flexDirection: 'column', gap: 2 }}>
              <b style={{ fontSize: 20, color:'var(--lc-text-1)'}}>{fmtPoints(pkg.tokens_used_month as number)}</b><span style={{ fontSize: 12, color:'var(--adm-faint)'}}>{t('plans.usedMonth')}</span>
            </div>
          </div>
          <div style={{ marginTop: 10, fontSize: 13, color: 'var(--adm-hint)' }}>
            {tpl('billing.myPackageCode', { code: (pkg.package_code as string) || '—' })}
            {pkgExpiresLabel ? ` · ${t('plans.expiresAt')}: ${pkgExpiresLabel}` : ''}
            {' · '}{tpl('billing.myPackageBalance', { balance: pkg.balance_sentences_approx ?? pkg.sentence_balance ?? '—' })}
          </div>
          {(() => {
            const total = Number(pkg.balance_tokens ?? 0)
            const hasPlan = !!(pkg.package_code && pkg.package_code !== 'trial')
            if (total > 0 || hasPlan) return null
            return (
              <div style={{ marginTop: 10, padding: '10px 14px', borderRadius: 8, background: 'var(--adm-warn-bg)', border: '1.2px solid var(--adm-warn-bd)', fontSize: 13, color: 'var(--adm-warn-tx)', lineHeight: 1.7 }}>
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
              <div style={{ fontWeight: 600, fontSize: 14, color: 'var(--adm-hint)', margin: '10px 0 6px' }}>{g.title}</div>
              <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill,minmax(190px,1fr))', gap: 12 }}>
                {g.items.map((pl) => (
                  <div key={pl.id} style={{ border: '1.2px solid var(--adm-line)', borderRadius: 8, padding: 14, display: 'flex', flexDirection: 'column', gap: 6, background: 'var(--adm-card)' }}>
                    <div style={{ fontWeight: 600, fontSize: 14 }}>{pl.name}</div>
                    <div style={{ fontSize: 22, fontWeight: 700, color:'var(--lc-text-1)'}}>¥{pl.price_money}<small style={{ fontSize: 12, color:'var(--adm-faint)', fontWeight: 400 }}>{pl.ptype ==='paid'? ` /${pl.duration_days}d` :''}</small></div>
                    {pl.ptype === 'paid' && Number(pl.price_money) > 0 && (
                      <div style={{ fontSize: 12, color: '#c66900' }}>{t('plans.halfOffBadge')}</div>
                    )}
                    <ul style={{ margin: '0 0 4px 16px', padding: 0, fontSize: 13, color: 'var(--adm-hint)', lineHeight: 1.7 }}>
                      <li>{Number(pl.points) > 0 ? tpl('billing.pkgPoints', { n: pl.points }) : tpl('billing.pkgSentences', { n: pl.sentences })}</li>
                      <li>{t('packages.type.' + pl.ptype)}</li>
                    </ul>
                    <Button variant="primary" onClick={() => {
                      if (isUpgradePlan(pl)) upgrade(pl)
                      else subscribe(pl)
                    }}>{isUpgradePlan(pl) ? t('plans.upgrade') : t('billing.subscribeNow')}</Button>
                  </div>
                ))}
                {!g.items.length && <div style={{ color: 'var(--adm-faint)', fontSize: 13 }}>{t('billing.noPlans')}</div>}
              </div>
            </div>
          ))}
        </Panel>
      )}

      {!isSuper && (
        <Panel id="plans-topup" title={t('plans.nav.topup')}>
          <div style={{ fontSize: 13, color: 'var(--adm-hint)', marginBottom: 8 }}>{t('billing.onlineTopUpHint')}</div>
          <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}>
            <select className="lc-select" value={chForm.channel} onChange={(e) => setChForm({ ...chForm, channel: e.target.value })} style={{ width: 200 }}>
              {chOptions.map((o) => <option key={o.value} value={o.value}>{o.label}</option>)}
            </select>
            <input className="lc-input" type="number" value={String(chForm.points)} onChange={(e) => setChForm({ ...chForm, points: Number(e.target.value) || 0 })} placeholder={t('billing.tokenCount')} style={{ width: 180 }} />
            <Button variant="primary" disabled={chLoading} onClick={openCheckout}>{chLoading ? t('billing.ordering') : t('billing.goPay')}</Button>
          </div>
          {curOrder && curOrder.status === 'pending' && (
            <p style={{ color: 'var(--lc-text-1)', fontSize: 13, marginTop: 8 }}>{tpl('billing.currentOrder', { orderNo: curOrder.order_no, amount: fmtPoints(curOrder.amount_tokens), money: Number(curOrder.amount_money ?? 0).toFixed(2) })}</p>
          )}
        </Panel>
      )}

      <Panel title={t('billing.ordersTitle')}>
        {/* 数据表格 */}
        <div style={{ maxHeight: 260, overflowY: 'auto' }}>
          <DataTable rowKey={(row) => String((row as Any).id)} rows={orders} emptyText={t('plans.noOrder')}
                 columns={[
                   { key: 'order_no', title: t('billing.colOrderNo'), width: 150 },
                   { key: 'amount_tokens', title: t('billing.colTokens'), width: 110, render: (row) => fmtPoints(Number((row as Any).amount_tokens)) },
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
        <div style={{ fontSize: 13, color: 'var(--adm-hint)', marginBottom: 8 }}>{t('billing.quotaHint')}</div>
        <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}>
          <input className="lc-input" type="number" value={num(quotaForm.qps)} onChange={(e) => setQuotaForm({ ...quotaForm, qps: Number(e.target.value) || 0 })} placeholder={t('billing.quotaQps')} style={{ width: 140 }} />
          <input className="lc-input" type="number" value={num(quotaForm.concurrent)} onChange={(e) => setQuotaForm({ ...quotaForm, concurrent: Number(e.target.value) || 0 })} placeholder={t('billing.quotaConcurrent')} style={{ width: 140 }} />
          <input className="lc-input" type="number" value={num(quotaForm.max_daily_chars)} onChange={(e) => setQuotaForm({ ...quotaForm, max_daily_chars: Number(e.target.value) || 0 })} placeholder={t('billing.quotaDailyChars')} style={{ width: 160 }} />
          <input className="lc-input" type="number" value={num(quotaForm.max_daily_tokens)} onChange={(e) => setQuotaForm({ ...quotaForm, max_daily_tokens: Number(e.target.value) || 0 })} placeholder={t('billing.quotaDailyTokens')} style={{ width: 160 }} />
          <Button onClick={saveQuota}>{t('billing.saveQuota')}</Button>
        </div>
      </Panel>

      {isSuper && (
        <Panel title={t('plans.funnelTitle')}>
          <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap', marginBottom: 8 }}>
            {[7, 30, 90].map((d) => (
              <Button key={d} size="sm" variant={funnelDays === d ? 'primary' : 'secondary'} onClick={() => setFunnelDays(d)}>{t('plans.funnelDays').replace('{d}', String(d))}</Button>
            ))}
            <span style={{ fontSize: 12, color: 'var(--adm-faint)' }}>{t('plans.funnelHint')}</span>
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
            <span style={{ fontSize: 13, color: 'var(--adm-hint)', marginLeft: 16 }}>{t('packages.sensitiveGateLabel')}</span>
            <Switch checked={sensitiveGate} onChange={(e) => setSensitiveGate(e.target.checked)} />
            <Button onClick={saveSensitiveGate}>{t('common.save')}</Button>
          </div>
          <div style={{ marginTop: 12 }}>
            <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}>
              <span style={{ fontSize: 13, color: 'var(--adm-hint)' }}>{t('packages.trialTokensLabel')}</span>
              <input className="lc-input" type="number" value={num(freeTrialTokens)} onChange={(e) => setFreeTrialTokens(Number(e.target.value) || 0)} style={{ width: 120 }} />
              <span style={{ fontSize: 13, color: 'var(--adm-hint)' }}>{t('packages.trialDaysLabel')}</span>
              <input className="lc-input" type="number" value={num(freeTrialDays)} onChange={(e) => setFreeTrialDays(Number(e.target.value) || 0)} style={{ width: 80 }} />
              <span style={{ fontSize: 13, color: 'var(--adm-hint)', marginLeft: 12 }}>{t('packages.markupLabel')}</span>
              <input className="lc-input" type="number" value={num(markupMultiplier)} onChange={(e) => setMarkupMultiplier(Math.max(0, Number(e.target.value) || 0))} style={{ width: 120 }} />
              <span style={{ fontSize: 13, color: 'var(--adm-hint)', marginLeft: 12 }}>{t('packages.rateLabel')}</span>
              <input className="lc-input" type="number" value={num(tokensPerSentence)} onChange={(e) => setTokensPerSentence(Math.max(0, Number(e.target.value) || 0))} style={{ width: 120 }} />
              <span style={{ fontSize: 13, color: 'var(--adm-hint)', marginLeft: 12 }}>{t('packages.pointsRateLabel')}</span>
              <input className="lc-input" type="number" value={num(pointsTokensRate)} onChange={(e) => setPointsTokensRate(Math.max(0, Number(e.target.value) || 0))} style={{ width: 110 }} />
              <Button onClick={saveBillingParams}>{t('common.save')}</Button>
            </div>
            <div style={{ fontSize: 12, color: 'var(--adm-faint)', marginTop: 6 }}>{t('packages.markupHint')}</div>
          </div>
          <div style={{ marginTop: 12 }}>
            <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}>
              <span style={{ fontSize: 13, color: 'var(--adm-hint)' }}>{t('packages.payModeTitle')}</span>
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
              <div style={{ fontSize: 12, color: 'var(--adm-faint)', marginBottom: 4 }}>{t('packages.staticQRHint')}</div>
              <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}>
                <input className="lc-input" value={staticQRImage} onChange={(e) => setStaticQRImage(e.target.value)} placeholder={t('packages.staticQRPlaceholder')} style={{ width: 360 }} />
                <input type="file" accept=".png,.jpg,.jpeg,.gif,.webp" style={{ fontSize: 12 }} onChange={uploadStaticQR} disabled={qrUploading} />
                {qrUploading && <span style={{ fontSize: 12, color: 'var(--adm-faint)' }}>…</span>}
                <Button onClick={saveStaticQR}>{t('common.save')}</Button>
              </div>
              {isImage(staticQRImage) && (
                <div style={{ marginTop: 8, display: 'inline-block', border: '1px dashed var(--lc-border-card)', borderRadius: 8, padding: 8 }}>
                  <img src={staticQRImage} alt="qr" style={{ maxWidth: 160, maxHeight: 160, borderRadius: 6, display: 'block' }} />
                </div>
              )}
            </div>
          )}
          {/* ★ USDT（2026-09-15）：超管后台配置 USDT 收款（开关/链/钱包地址链接/汇率/确认数） */}
          <div style={{ marginTop: 12, borderTop: '1px dashed var(--adm-line)', paddingTop: 10 }}>
            <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}>
              <span style={{ fontWeight: 600, fontSize: 13 }}>{t('billing.usdtSection')}</span>
              <Switch checked={usdtCfg.usdt_enabled === '1'} onChange={(e) => setUsdtCfg({ ...usdtCfg, usdt_enabled: e.target.checked ? '1' : '0' })} />
              <span style={{ fontSize: 12, color: 'var(--adm-hint)' }}>{usdtOn ? t('billing.usdtOn') : t('billing.usdtOff')}</span>
              <span style={{ fontSize: 12, color: 'var(--adm-hint)', marginLeft: 12 }}>{t('billing.usdtTail')}</span>
              <Switch checked={usdtCfg.usdt_tail_enabled === '1'} onChange={(e) => setUsdtCfg({ ...usdtCfg, usdt_tail_enabled: e.target.checked ? '1' : '0' })} />
              <span style={{ fontSize: 12, color: 'var(--adm-hint)', marginLeft: 12 }}>{t('billing.usdtAuto')}</span>
              <Switch checked={usdtCfg.usdt_auto_settle === '1'} onChange={(e) => setUsdtCfg({ ...usdtCfg, usdt_auto_settle: e.target.checked ? '1' : '0' })} />
              <Button onClick={saveUSDT}>{t('common.save')}</Button>
            </div>
            <div style={{ fontSize: 12, color: 'var(--adm-faint)', margin: '4px 0 8px' }}>{t('billing.usdtHint')}</div>
            <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap', marginBottom: 6 }}>
              <span style={{ fontSize: 13, color: 'var(--adm-hint)' }}>{t('billing.usdtChains')}</span>
              <select className="lc-select" multiple value={String(usdtCfg.usdt_chains || '').split(',').map((c: string) => c.trim()).filter(Boolean)}
                      onChange={(e) => setUsdtCfg({ ...usdtCfg, usdt_chains: Array.from(e.target.selectedOptions).map((o) => o.value).join(',') })} style={{ minWidth: 260 }}
                      >
                {['trc20', 'erc20', 'bep20'].map((c) => <option key={c} value={c}>{usdtChainLabel(c)}</option>)}
              </select>
              <span style={{ fontSize: 13, color: 'var(--adm-hint)', marginLeft: 10 }}>{t('billing.usdtRate')}</span>
              <input className="lc-input" type="number" value={num(usdtCfg.usdt_rate_fen_per_usdt)} onChange={(e) => setUsdtCfg({ ...usdtCfg, usdt_rate_fen_per_usdt: Number(e.target.value) || 0 })} style={{ width: 120 }} />
            </div>
            {['trc20', 'erc20', 'bep20'].map((c) => (
              <div key={c} style={{ display: 'flex', alignItems: 'center', gap: 8, marginTop: 4, flexWrap: 'wrap' }}>
                <span style={{ fontSize: 12, color: 'var(--adm-hint)', width: 130 }}>{usdtChainLabel(c)}</span>
                <input className="lc-input" value={String(usdtCfg['usdt_addr_' + c] || '')} onChange={(e) => setUsdtCfg({ ...usdtCfg, ['usdt_addr_' + c]: e.target.value })}
                       placeholder={t('billing.usdtAddrPh')} style={{ width: 340 }} />
                <span style={{ fontSize: 12, color: 'var(--adm-faint)' }}>{t('billing.usdtConf')}</span>
                <input className="lc-input" type="number" value={num(usdtCfg['usdt_confirmations_' + c])} onChange={(e) => setUsdtCfg({ ...usdtCfg, ['usdt_confirmations_' + c]: Number(e.target.value) || 0 })} style={{ width: 70 }} />
                {usdtAddrURL(c, String(usdtCfg['usdt_addr_' + c] || '')) ? (
                  <a href={usdtAddrURL(c, String(usdtCfg['usdt_addr_' + c] || ''))} target="_blank" rel="noreferrer" style={{ fontSize: 12 }}>{t('billing.usdtWalletLink')} ↗</a>
                ) : <span style={{ fontSize: 12, color: 'var(--adm-faint)' }}>{t('billing.usdtNoAddr')}</span>}
              </div>
            ))}
          </div>
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
                   { key: 'amount_tokens', title: t('billing.colTokens'), width: 110, render: (row) => fmtPoints(Number((row as Any).amount_tokens)) },
                   { key: 'usdt', title: t('billing.usdtCol'), width: 240, render: (row) => {
                       const r = row as Any
                       const info = manualOrdersUsdt.current[String(r.id)]
                       if (r.channel !== 'usdt' || !info) return <span style={{ color: 'var(--adm-faint)' }}>—</span>
                       return (
                         <div style={{ fontSize: 12, lineHeight: 1.6 }}>
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
            <p>{tpl('billing.paySuccess', { amount: fmtPoints(curOrder.amount_tokens) })}</p>
            <Button variant="primary" onClick={closeCheckout}>{t('billing.done')}</Button>
          </div>
        ) : (
          <div>
            {curOrder && (
              <div style={{ textAlign: 'center' }}>
                {curOrder.channel === 'usdt' && usdtPay ? (
                  /* ★ USDT 收款台：精确金额（含尾数）+ 地址 + pay_uri 二维码 + txid 声明 */
                  <div style={{ textAlign: 'left', display: 'flex', flexDirection: 'column', gap: 8 }}>
                    <div style={{ padding: '8px 12px', borderRadius: 8, background: 'var(--adm-soft)', textAlign: 'center' }}>
                      <div style={{ fontSize: 12, color: 'var(--adm-hint)' }}>{t('billing.usdtAmountLabel')}（{usdtChainLabel(String(usdtPay.chain))}）</div>
                      <div style={{ fontSize: 24, fontWeight: 700 }}>{String(usdtPay.amount)} USDT</div>
                      {String(usdtPay.tail) !== '0' && <div style={{ fontSize: 11, color: 'var(--adm-faint)' }}>{tpl('billing.usdtTailNote', { tail: String(usdtPay.tail) })}</div>}
                    </div>
                    {usdtQr && <img src={usdtQr} alt="usdt-qr" style={{ width: 168, height: 168, alignSelf: 'center', borderRadius: 8, border: '1.2px solid var(--adm-line)', background: '#fff' }} />}
                    <div>
                      <div style={{ fontSize: 12, color: 'var(--adm-hint)' }}>{t('billing.usdtAddress')}</div>
                      <div style={{ display: 'flex', gap: 6, alignItems: 'center' }}>
                        <code style={{ flex: 1, wordBreak: 'break-all', background: 'var(--adm-soft)', borderRadius: 6, padding: '6px 8px', fontSize: 12 }}>{String(usdtPay.address)}</code>
                        <Button size="sm" variant="secondary" onClick={() => { void navigator.clipboard.writeText(String(usdtPay.address)); void toastSuccess(t('billing.usdtCopied')) }}>{t('billing.usdtCopy')}</Button>
                      </div>
                    </div>
                    <div style={{ fontSize: 12, color: 'var(--adm-hint)', display: 'flex', justifyContent: 'space-between', flexWrap: 'wrap', gap: 4 }}>
                      <span>{tpl('billing.usdtExpires', { time: fmtTime(String(usdtPay.expires_at)) })}</span>
                      <span>{tpl('billing.usdtConfNeed', { n: Number(usdtPay.confirmations) || 0 })}</span>
                    </div>
                    <input className="lc-input" value={usdtTxInput} onChange={(e) => setUsdtTxInput(e.target.value)} placeholder={t('billing.usdtTxPh')} style={{ width: '100%' }} />
                    <div style={{ fontSize: 11, color: 'var(--adm-faint)' }}>{t('billing.usdtCheckoutHint')}</div>
                  </div>
                ) : curOrder.channel === 'manual' ? (
                  <div>
                    <div style={{ fontSize: 13, color: 'var(--adm-hint)', marginBottom: 6 }}>{t('billing.staticQR')}</div>
                    {isImage(curOrder.qr_content as string)
                      ? <img src={curOrder.qr_content} style={{ maxWidth: 200, borderRadius: 8, border: '1.2px solid var(--adm-line)', margin: '8px 0' }} alt="qr" />
                      : qrImg
                        ? <img src={qrImg} style={{ maxWidth: 200, borderRadius: 8, border: '1.2px solid var(--lc-border-card)', margin: '8px 0', background: '#fff' }} alt="qr" />
                        : <pre style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-all', background: 'var(--adm-soft)', borderRadius: 8, padding: 12, fontSize: 12, maxHeight: 140, overflow: 'auto' }}>{String(curOrder.qr_content)}</pre>}
                  </div>
                ) : (
                  qrImg
                    ? <img src={qrImg} style={{ maxWidth: 200, borderRadius: 8, border: '1.2px solid var(--lc-border-card)', margin: '8px 0', background: '#fff' }} alt="qr" />
                    : <pre style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-all', background: 'var(--adm-soft)', borderRadius: 8, padding: 12, fontSize: 12, maxHeight: 140, overflow: 'auto' }}>{String(curOrder.qr_content)}</pre>
                )}
                <p style={{ fontSize: 13, color: 'var(--adm-hint)' }}>{tpl('billing.orderNo', { orderNo: curOrder.order_no })}</p>
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
