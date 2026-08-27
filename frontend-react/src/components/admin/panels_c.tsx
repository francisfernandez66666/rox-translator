// ============================================================================
// components/admin/panels_c.tsx — PlansPanel(套餐中心) / Referral / Webhooks / ApiKeys
// 行为对齐 Vue 版：PlansPanel.vue / Referral.vue / Webhooks.vue / ApiKeys.vue
// ============================================================================
import { useCallback, useEffect, useRef, useState } from 'react'
import {
  Button, Table, Dialog, Input, Select, Switch, Tag, Space, Popconfirm, Textarea,
} from 'tdesign-react'
import {
  billingQuota, billingQuotaSave,
  billingOrders, billingInvoices, billingInvoiceCreate,
  payCreate, payStatus, paySimulate, payManualConfirm, manualConfirmOrders, adminOrderPay,
  plans as apiPlans, myPackage, packageSubscribe,
  adminPackages, adminPackageCreate, adminPackageUpdate, adminPackageDelete,
  adminPackageSettings, adminPackageSettingsSave,
  referralMy, fetchReferralQrBlob, referralConfigGet, referralConfigSave,
  webhooks as apiWebhooks, webhookSave, webhookDelete, webhookTest,
  apiKeys as apiApiKeys, apiKeyCreate, apiKeyStatus, apiKeyRotate, apiKeyDelete,
  apiKeyLimit, getOpenAPIDocs, saveOpenAPIDocs, previewOpenAPIDocs,
} from '@/api'
import { Panel, Field, toastResp, num } from './parts'
import { fmtNum, fmtTime, maskKey } from '@/lib/ui'
import { useAdmin } from '@/stores/admin'
import { useT } from '@/i18n'

type Any = Record<string, any>

// 判断二维码内容是否为图片（data:image 或 http(s) 图片 URL）
function isImage(s?: string): boolean {
  if (!s) return false
  if (s.indexOf('data:image') === 0) return true
  const idx = s.indexOf('.')
  if (idx < 0) return false
  const tail = s.slice(idx + 1).split(/[?#]/)[0].toLowerCase()
  return ['png', 'jpg', 'jpeg', 'gif', 'webp'].indexOf(tail) >= 0 && (s.indexOf('http://') === 0 || s.indexOf('https://') === 0)
}
// 订单状态标签映射（与 Vue 一致：pending/paid/refunded/cancelled）
function orderStatusLabel(s: string, t: (k: string) => string): string {
  const m: Record<string, string> = {
    pending: t('billing.stPending'), paid: t('billing.stPaid'),
    refunded: t('billing.stRefunded'), cancelled: t('billing.stCancelled'),
  }
  return m[s] || s
}
// 支付/订单状态→TDesign Tag 主题映射（待支付=warning，已支付=success，其余=default）
function statusTheme(s: string): string {
  return ({ pending: 'warning', paid: 'success', refunded: 'default', cancelled: 'default' } as Record<string, string>)[s] || 'default'
}

// ---------------- 套餐中心（Vue PlansPanel.vue） ----------------
// 计费配置、套餐订阅、充值、订单/发票、配额与超管商业包管理
export function PlansP() {
  const ad = useAdmin()
  const [, t, tpl] = useT()
  const isSuper = ad.isSuper

  // ① 当前套餐信息
  const [pkg, setPkg] = useState<Any>({})
  // ② 可选购套餐列表
  const [planList, setPlanList] = useState<Any[]>([])
  // ③④ 充值 / 订单 / 发票
  const [orders, setOrders] = useState<Any[]>([])
  const [invoices, setInvoices] = useState<Any[]>([])
  const [chForm, setChForm] = useState<Any>({ channel: 'auto', tokens: 100000 })
  const [showCheckout, setShowCheckout] = useState(false)
  const [chLoading, setChLoading] = useState(false)
  const [curOrder, setCurOrder] = useState<Any | null>(null)
  const orderRef = useRef<Any | null>(null)
  const payTimer = useRef<ReturnType<typeof setInterval> | null>(null)
  const [payMode, setPayMode] = useState('mock')
  const [payModeCfg, setPayModeCfg] = useState('mock')
  const payModeLabel = ({ mock: t('billing.chMock'), sdk: t('billing.chSdk'), static_qr: t('billing.chStaticQR') } as Record<string, string>)[payMode] || payMode
  // ⑤ 配额配置
  const [quotaForm, setQuotaForm] = useState<Any>({ qps: 10, concurrent: 3, max_daily_chars: 0, max_daily_tokens: 0 })
  // ⑥⑦⑧ 超管区：商业包、计费开关、价格参数、支付模式
  const [pkgs, setPkgs] = useState<Any[]>([])
  const [billingEnforced, setBillingEnforced] = useState(false)
  const [trialSentences, setTrialSentences] = useState(100)
  const [markupMultiplier, setMarkupMultiplier] = useState(1.5)
  const [tokensPerSentence, setTokensPerSentence] = useState(500)
  const [staticQRImage, setStaticQRImage] = useState('')
  const [pkgDlg, setPkgDlg] = useState<null | 'settings'>(null)
  const [manualOrders, setManualOrders] = useState<Any[]>([])
  const [invDlg, setInvDlg] = useState<null | { order: Any; title: string; taxNo: string }>(null)

  // 同步 ref 与 state，便于轮询读取最新订单
  const setOrder = (o: Any | null) => { orderRef.current = o; setCurOrder(o) }
  // 当前套餐过期日期格式化
  const pkgExpiresLabel = (() => {
    const v = pkg.package_expires as string
    if (!v) return ''
    const d = new Date(v)
    return isNaN(d.getTime()) ? '' : d.toISOString().slice(0, 10)
  })()

  // 启动/停止支付状态轮询
  function startPolling() { stopPolling(); payTimer.current = setInterval(checkStatus, 3000) }
  function stopPolling() { if (payTimer.current) { clearInterval(payTimer.current); payTimer.current = null } }
  // 组件卸载时清理轮询
  useEffect(() => () => stopPolling(), [])

  // 加载当前套餐与可购套餐
  const loadPackage = useCallback(async () => {
    const r: Any = await myPackage()
    if (r.success) {
      setPkg(r)
      const pm = r.pay_mode as string
      if (pm) { setPayMode(pm); setPayModeCfg(pm) }
    }
    const p: Any = await apiPlans()
    if (p.success) setPlanList((p.plans as Any[]) || [])
  }, [])
  // 加载订单列表
  const loadOrders = useCallback(async () => {
    const r: Any = await billingOrders()
    if (r.success) setOrders((r.orders as Any[]) || [])
  }, [])
  // 加载发票列表
  const loadInvoices = useCallback(async () => {
    const r: Any = await billingInvoices()
    if (r.success) setInvoices((r.invoices as Any[]) || [])
  }, [])
  // 加载配额配置
  const loadQuota = useCallback(async () => {
    const r: Any = await billingQuota()
    if (r.success) setQuotaForm({
      qps: (r.qps as number) || 10, concurrent: (r.concurrent as number) || 3,
      max_daily_chars: (r.max_daily_chars as number) || 0, max_daily_tokens: (r.max_daily_tokens as number) ?? 0,
    })
  }, [])
  // 超管加载商业包列表、计费设置与待人工确认订单
  const loadPkgs = useCallback(async () => {
    if (!isSuper) return
    const r: Any = await adminPackages()
    if (r.success) setPkgs((r.packages as Any[]) || [])
    const cfg: Any = await adminPackageSettings()
    if (cfg.success) {
      setBillingEnforced(cfg.billing_enforced === '1' || cfg.billing_enforced === true)
      if (cfg.trial_sentences) setTrialSentences(Number(cfg.trial_sentences))
      if (typeof cfg.billing_markup_multiplier === 'number') setMarkupMultiplier(cfg.billing_markup_multiplier)
      if (cfg.estimate_tokens_per_sentence) setTokensPerSentence(Number(cfg.estimate_tokens_per_sentence))
      if (cfg.pay_mode) setPayModeCfg(cfg.pay_mode as string)
      if (cfg.static_qr_image) setStaticQRImage(cfg.static_qr_image as string)
    }
    const m: Any = await manualConfirmOrders()
    if (m.success) setManualOrders((m.orders as Any[]) || [])
  }, [isSuper])

  // 组合加载：非超管才加载套餐/选购；所有角色加载订单、发票、配额
  const loadAll = useCallback(async () => {
    if (!isSuper) await loadPackage()
    await Promise.all([loadOrders(), loadInvoices(), loadQuota(), loadPkgs()])
  }, [isSuper, loadPackage, loadOrders, loadInvoices, loadQuota, loadPkgs])
  useEffect(() => { void loadAll() }, [loadAll])

  // 订阅套餐：创建订单后进入收银台并轮询
  async function subscribe(pl: Any) {
    const r: Any = await packageSubscribe(String(pl.code))
    if (!r.success) return
    const o = r.order as Any
    if (o) { setOrder(o); setShowCheckout(true); if (o.channel !== 'manual') startPolling() }
    await loadPackage()
  }
  // 打开充值收银台：创建订单并进入支付流程
  async function openCheckout() {
    if (Number(chForm.tokens) <= 0) return
    setChLoading(true)
    try {
      const channel = chForm.channel === 'auto' ? '' : chForm.channel
      const r: Any = await payCreate({ tokens: Number(chForm.tokens), channel })
      if (!toastResp(r)) return
      const o = r.order as Any
      setOrder(o); setShowCheckout(true)
      if (o && o.channel !== 'manual') startPolling()
    } finally { setChLoading(false) }
  }
  // 继续支付：刷新订单状态并打开收银台
  async function resumePay(_o: Any) {
    const r: Any = await payStatus(Number(_o.id))
    const o = (r.success ? (r.order as Any) : _o)
    setOrder(o); setShowCheckout(true)
    if (o && o.channel !== 'manual') startPolling()
  }
  // 关闭收银台并刷新相关数据
  function closeCheckout() { setShowCheckout(false); stopPolling(); void loadOrders(); if (!isSuper) void loadPackage() }
  // 轮询检查订单支付状态
  async function checkStatus() {
    const o = orderRef.current
    if (!o) return
    const r: Any = await payStatus(Number(o.id))
    if (r.success) {
      const no = (r.order as Any) || o
      setOrder(no)
      if (no.status === 'paid') stopPolling()
    }
  }
  // mock 模式：模拟支付成功
  async function simulatePay() {
    const o = orderRef.current
    if (!o) return
    setChLoading(true)
    try { const r: Any = await paySimulate(Number(o.id)); if (r.success) await checkStatus() } finally { setChLoading(false) }
  }
  // 人工确认已支付（static_qr / manual 渠道）
  async function manualConfirm() {
    const o = orderRef.current
    if (!o) return
    setChLoading(true)
    try { const r: Any = await payManualConfirm(Number(o.id)); if (r.success) { stopPolling(); closeCheckout() } } finally { setChLoading(false) }
  }

  // 保存配额配置
  async function saveQuota() {
    await billingQuotaSave({
      qps: Math.max(1, Number(quotaForm.qps) || 0),
      concurrent: Math.max(1, Number(quotaForm.concurrent) || 0),
      max_daily_chars: Math.max(0, Number(quotaForm.max_daily_chars) || 0),
      max_daily_tokens: Math.max(0, Number(quotaForm.max_daily_tokens) || 0),
    })
    await loadQuota()
  }

  // 超管新建商业包表单
  const [pkgForm, setPkgForm] = useState<Any>({ code: '', name: '', ptype: 'paid', sentences: 1000, price_money: 0, duration_days: 30 })
  // 创建商业包
  async function createPkg() {
    if (!pkgForm.code || !pkgForm.name) { window.alert(t('apikeys.nameRequired')); return }
    const r: Any = await adminPackageCreate(pkgForm as any)
    if (toastResp(r)) { setPkgForm({ code: '', name: '', ptype: 'paid', sentences: 1000, price_money: 0, duration_days: 30 }); void loadPkgs() }
  }
  // 超管商业包启停
  async function togglePkg(p: Any) { await adminPackageUpdate({ id: Number(p.id), enabled: p.enabled ? 0 : 1 }); void loadPkgs() }
  // 超管删除商业包
  async function deletePkg(p: Any) { if (!window.confirm(t('webhooks.confirmDelete'))) return; await adminPackageDelete(Number(p.id)); void loadPkgs() }
  // 保存计费强制开关
  async function saveEnforce() { const r: Any = await adminPackageSettingsSave({ billing_enforced: billingEnforced ? '1' : '0' } as never); toastResp(r, t('common.save')) }
  // 保存计费参数：试用句数、加价倍率、句-token 换算率
  async function saveBillingParams() {
    if (!(markupMultiplier >= 1)) return
    if (!(tokensPerSentence > 0)) return
    const r: Any = await adminPackageSettingsSave({ billing_markup_multiplier: markupMultiplier, estimate_tokens_per_sentence: tokensPerSentence } as never)
    toastResp(r, t('common.save'))
  }
  // 保存支付模式
  async function savePayMode() {
    const r: Any = await adminPackageSettingsSave({ pay_mode: payModeCfg } as never)
    if (toastResp(r, t('common.save'))) setPayMode(payModeCfg)
  }
  // 保存静态二维码图片地址
  async function saveStaticQR() { const r: Any = await adminPackageSettingsSave({ static_qr_image: staticQRImage } as never); toastResp(r, t('common.save')) }
  // 人工确认订单入账
  async function confirmManual(o: Any) { const r: Any = await adminOrderPay(Number(o.id)); if (r.success) await Promise.all([loadPkgs(), loadOrders()]) }

  const planGroups = [
    { type: 'paid', title: t('plans.groupPaid'), items: planList.filter((p) => p.ptype === 'paid') },
    { type: 'increment', title: t('plans.groupIncrement'), items: planList.filter((p) => p.ptype === 'increment') },
  ]
  const chOptions = [
    { label: tpl('billing.payModeAuto', { mode: payModeLabel }), value: 'auto' },
    ...(payMode === 'static_qr' ? [{ label: t('billing.chStaticQR'), value: 'manual' }] : []),
    ...(payMode === 'sdk' ? [{ label: t('billing.chWechat'), value: 'wechat' }, { label: t('billing.chAlipay'), value: 'alipay' }] : []),
    { label: t('billing.chMock'), value: 'mock' },
  ]

  return (
    <>
      {/* ===== 当前套餐余额（非超管） ===== */}
      {!isSuper && (
        <Panel title={t('plans.nav.current')}>
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit,minmax(150px,1fr))', gap: 10 }}>
            <div style={{ background: '#f7f9fc', borderRadius: 10, padding: '12px 14px', display: 'flex', flexDirection: 'column', gap: 2 }}>
              <b style={{ fontSize: 20, color: '#1a237e' }}>{fmtNum(pkg.balance_tokens as number)}</b><span style={{ fontSize: 12, color: '#78909c' }}>{t('usage.currentBalance')}</span>
            </div>
            <div style={{ background: '#f7f9fc', borderRadius: 10, padding: '12px 14px', display: 'flex', flexDirection: 'column', gap: 2 }}>
              <b style={{ fontSize: 20, color: '#e65100' }}>{fmtNum(pkg.sub_grants_left as number)}</b><span style={{ fontSize: 12, color: '#78909c' }}>{t('plans.balanceGrants')}</span>
            </div>
            <div style={{ background: '#f7f9fc', borderRadius: 10, padding: '12px 14px', display: 'flex', flexDirection: 'column', gap: 2 }}>
              <b style={{ fontSize: 20, color: '#2e7d32' }}>{fmtNum(pkg.permanent_balance as number)}</b><span style={{ fontSize: 12, color: '#78909c' }}>{t('plans.balancePermanent')}</span>
            </div>
            <div style={{ background: '#f7f9fc', borderRadius: 10, padding: '12px 14px', display: 'flex', flexDirection: 'column', gap: 2 }}>
              <b style={{ fontSize: 20, color: '#1a237e' }}>{fmtNum(pkg.tokens_used_month as number)}</b><span style={{ fontSize: 12, color: '#78909c' }}>{t('plans.usedMonth')}</span>
            </div>
          </div>
          <div style={{ marginTop: 10, fontSize: 13, color: '#667' }}>
            {tpl('billing.myPackageCode', { code: (pkg.package_code as string) || '—' })}
            {pkgExpiresLabel ? ` · ${t('plans.expiresAt')}: ${pkgExpiresLabel}` : ''}
            {' · '}{tpl('billing.myPackageBalance', { balance: pkg.sentence_balance ?? '—' })}
          </div>
        </Panel>
      )}

      {/* ===== 套餐选购（非超管） ===== */}
      {!isSuper && (
        <Panel title={t('plans.nav.shop')}>
          {planGroups.map((g) => (
            <div key={g.type}>
              <div style={{ fontWeight: 600, fontSize: 13.5, color: '#455a64', margin: '10px 0 6px' }}>{g.title}</div>
              <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill,minmax(190px,1fr))', gap: 12 }}>
                {g.items.map((pl) => (
                  <div key={pl.id} style={{ border: '1.5px solid #e3eaf2', borderRadius: 12, padding: 14, display: 'flex', flexDirection: 'column', gap: 6, background: '#fff' }}>
                    <div style={{ fontWeight: 600, fontSize: 15 }}>{pl.name}</div>
                    <div style={{ fontSize: 22, fontWeight: 700, color: '#1a237e' }}>¥{pl.price_money}<small style={{ fontSize: 12, color: '#90a4ae', fontWeight: 400 }}>{pl.ptype === 'paid' ? ` /${pl.duration_days}d` : ''}</small></div>
                    <ul style={{ margin: '0 0 4px 16px', padding: 0, fontSize: 12.5, color: '#607d8b', lineHeight: 1.7 }}>
                      <li>{tpl('billing.pkgSentences', { n: pl.sentences })}</li>
                      <li>{t('packages.type.' + pl.ptype)}</li>
                    </ul>
                    <Button theme="success" onClick={() => subscribe(pl)}>{t('billing.subscribeNow')}</Button>
                  </div>
                ))}
                {!g.items.length && <div style={{ color: '#999', fontSize: 13 }}>{t('billing.noPlans')}</div>}
              </div>
            </div>
          ))}
        </Panel>
      )}

      {/* ===== 在线充值（非超管） ===== */}
      {!isSuper && (
        <Panel title={t('plans.nav.topup')}>
          <div style={{ fontSize: 13, color: '#667', marginBottom: 8 }}>{t('billing.onlineTopUpHint')}</div>
          <Space size={8} align="center">
            <Select value={chForm.channel} onChange={(v) => setChForm({ ...chForm, channel: v as string })} style={{ width: 200 }} options={chOptions} />
            <Input type="number" value={String(chForm.tokens)} onChange={(v) => setChForm({ ...chForm, tokens: Number(v) || 0 })} placeholder={t('billing.tokenCount')} style={{ width: 180 }} />
            <Button theme="success" loading={chLoading} onClick={openCheckout}>{chLoading ? t('billing.ordering') : t('billing.goPay')}</Button>
          </Space>
          {curOrder && curOrder.status === 'pending' && (
            <p style={{ color: '#1a237e', fontSize: 13, marginTop: 8 }}>{tpl('billing.currentOrder', { orderNo: curOrder.order_no, amount: curOrder.amount_tokens })}</p>
          )}
        </Panel>
      )}

      {/* ===== 订单与发票 ===== */}
      <Panel title={t('billing.ordersTitle')}>
        <Table rowKey="id" size="small" maxHeight={260} data={orders}
               columns={[
                 { colKey: 'order_no', title: t('billing.colOrderNo'), width: 150 },
                 { colKey: 'amount_tokens', title: t('billing.colTokens'), width: 110 },
                 { colKey: 'amount_money', title: t('billing.colAmount'), width: 100, cell: ({ row }: any) => tpl('billing.yuan', { amount: row.amount_money }) },
                 { colKey: 'status', title: t('billing.colStatus'), width: 110, cell: ({ row }: any) => <Tag theme={statusTheme(row.status) as any}>{orderStatusLabel(row.status, t)}</Tag> },
                 { colKey: 'op', title: '', width: 120, cell: ({ row }: any) =>
                     row.status === 'paid'
                       ? <Button size="small" variant="text" onClick={() => setInvDlg({ order: row, title: '', taxNo: '' })}>开发票</Button>
                       : (row.status === 'pending' ? <Button size="small" theme="success" variant="outline" onClick={() => resumePay(row)}>{t('plans.orderContinue')}</Button> : null) },
               ] as never} />
        {!orders.length && <div style={{ textAlign: 'center', color: '#999', padding: 8 }}>{t('plans.noOrder')}</div>}
        <h4 style={{ margin: '14px 0 6px' }}>{t('billing.invoiceMgmt')}</h4>
        <Table rowKey="id" size="small" maxHeight={220} data={invoices}
               columns={[
                 { colKey: 'invoice_no', title: t('billing.colInvoiceNo') },
                 { colKey: 'title', title: t('billing.colTitle') },
                 { colKey: 'amount_money', title: t('billing.colAmountYuan'), width: 110 },
               ] as never} />
        {!invoices.length && <div style={{ textAlign: 'center', color: '#999', padding: 8 }}>{t('billing.noInvoices')}</div>}
      </Panel>

      {/* ===== 配额配置 ===== */}
      <Panel title={t('plans.nav.quota')}>
        <div style={{ fontSize: 13, color: '#667', marginBottom: 8 }}>{t('billing.quotaHint')}</div>
        <Space size={8} align="center">
          <Input type="number" value={num(quotaForm.qps)} onChange={(v) => setQuotaForm({ ...quotaForm, qps: Number(v) || 0 })} placeholder={t('billing.quotaQps')} style={{ width: 140 }} />
          <Input type="number" value={num(quotaForm.concurrent)} onChange={(v) => setQuotaForm({ ...quotaForm, concurrent: Number(v) || 0 })} placeholder={t('billing.quotaConcurrent')} style={{ width: 140 }} />
          <Input type="number" value={num(quotaForm.max_daily_chars)} onChange={(v) => setQuotaForm({ ...quotaForm, max_daily_chars: Number(v) || 0 })} placeholder={t('billing.quotaDailyChars')} style={{ width: 160 }} />
          <Input type="number" value={num(quotaForm.max_daily_tokens)} onChange={(v) => setQuotaForm({ ...quotaForm, max_daily_tokens: Number(v) || 0 })} placeholder={t('billing.quotaDailyTokens')} style={{ width: 160 }} />
          <Button onClick={saveQuota}>{t('billing.saveQuota')}</Button>
        </Space>
      </Panel>

      {/* ===== 超管商业包管理 ===== */}
      {isSuper && (
        <Panel title={t('plans.nav.pkgMgmt')} extra={
          <Button onClick={() => setPkgDlg('settings')}>{t('plans.nav.ops')}</Button>
        }>
          <Space size={8} align="center">
            <Input value={String(pkgForm.code || '')} onChange={(v) => setPkgForm({ ...pkgForm, code: v })} placeholder={t('packages.code')} style={{ width: 140 }} />
            <Input value={String(pkgForm.name || '')} onChange={(v) => setPkgForm({ ...pkgForm, name: v })} placeholder={t('packages.name')} style={{ width: 160 }} />
            <Select value={String(pkgForm.ptype || 'paid')} onChange={(v) => setPkgForm({ ...pkgForm, ptype: v })} style={{ width: 140 }}
                    options={[{ label: t('packages.type.paid'), value: 'paid' }, { label: t('packages.type.increment'), value: 'increment' }, { label: t('packages.type.free'), value: 'free' }]} />
            <Input type="number" value={num(pkgForm.sentences)} onChange={(v) => setPkgForm({ ...pkgForm, sentences: Number(v) || 0 })} placeholder={t('packages.sentences')} style={{ width: 120 }} />
            <Input type="number" value={num(pkgForm.price_money)} onChange={(v) => setPkgForm({ ...pkgForm, price_money: Number(v) || 0 })} placeholder={t('packages.price')} style={{ width: 120 }} />
            <Input type="number" value={num(pkgForm.duration_days)} onChange={(v) => setPkgForm({ ...pkgForm, duration_days: Number(v) || 0 })} placeholder={t('packages.duration')} style={{ width: 120 }} />
            <Button onClick={createPkg}>{t('common.save')}</Button>
          </Space>
          <Table rowKey="id" size="small" data={pkgs} style={{ marginTop: 10 }}
                 columns={[
                   { colKey: 'code', title: 'code', width: 140 },
                   { colKey: 'name', title: t('packages.name') },
                   { colKey: 'ptype', title: t('packages.type'), width: 100, cell: ({ row }: any) => t('packages.type.' + row.ptype) },
                   { colKey: 'sentences', title: t('packages.sentences'), width: 90 },
                   { colKey: 'price_money', title: `¥${t('packages.price')}`, width: 90 },
                   { colKey: 'enabled', title: t('common.status'), width: 90, cell: ({ row }: any) =>
                     <Button size="small" variant={row.enabled ? 'outline' : 'text'} theme={row.enabled ? 'success' : 'default'} onClick={() => togglePkg(row)}>{row.enabled ? t('common.active') : t('common.disabled')}</Button> },
                   { colKey: 'op', title: '', width: 90, cell: ({ row }: any) =>
                     <Popconfirm content={t('packages.deletePkgConfirm')} onConfirm={() => deletePkg(row)}>
                       <Button size="small" variant="text" theme="danger">✕</Button>
                     </Popconfirm> },
                 ] as never} />
        </Panel>
      )}

      {/* ===== 超管计费运营设置弹窗 ===== */}
      <Dialog visible={pkgDlg === 'settings'} onClose={() => setPkgDlg(null)} header={t('plans.nav.ops')} width={560}>
        <Space size={8} align="center">
          <Switch value={billingEnforced} onChange={(v) => setBillingEnforced(v as boolean)} />
          <span style={{ color: billingEnforced ? '#2e7d32' : '#888', fontWeight: 600 }}>{billingEnforced ? t('billing.enforcedOn') : t('billing.enforcedOff')}</span>
          <Button onClick={saveEnforce}>{t('common.save')}</Button>
        </Space>
        <div style={{ marginTop: 12 }}>
          <Space size={8} align="center">
            <span style={{ fontSize: 13, color: '#556' }}>{t('packages.trialLabel')}</span>
            <Input type="number" value={num(trialSentences)} onChange={(v) => setTrialSentences(Number(v) || 0)} style={{ width: 120 }} />
            <span style={{ fontSize: 13, color: '#556', marginLeft: 12 }}>{t('packages.markupLabel')}</span>
            <Input type="number" value={num(markupMultiplier)} onChange={(v) => setMarkupMultiplier(Number(v) || 0)} style={{ width: 120 }} />
            <span style={{ fontSize: 13, color: '#556', marginLeft: 12 }}>{t('packages.rateLabel')}</span>
            <Input type="number" value={num(tokensPerSentence)} onChange={(v) => setTokensPerSentence(Number(v) || 0)} style={{ width: 120 }} />
            <Button onClick={saveBillingParams}>{t('common.save')}</Button>
          </Space>
        </div>
        <div style={{ marginTop: 12 }}>
          <Space size={8} align="center">
            <span style={{ fontSize: 13, color: '#556' }}>{t('packages.payModeTitle')}</span>
            <Select value={payModeCfg} onChange={(v) => setPayModeCfg(v as string)} style={{ width: 200 }}
                    options={[{ label: t('packages.payMock'), value: 'mock' }, { label: t('packages.paySdk'), value: 'sdk' }, { label: t('packages.payStaticQR'), value: 'static_qr' }]} />
            <Button onClick={savePayMode}>{t('common.save')}</Button>
          </Space>
        </div>
        {payModeCfg === 'static_qr' && (
          <div style={{ marginTop: 8 }}>
            <Space size={8} align="center">
              <Input value={staticQRImage} onChange={(v) => setStaticQRImage(v)} placeholder={t('packages.staticQRPlaceholder')} style={{ width: 360 }} />
              <Button onClick={saveStaticQR}>{t('common.save')}</Button>
            </Space>
          </div>
        )}
      </Dialog>

      {/* ===== 超管人工确认订单 ===== */}
      {isSuper && (
        <Panel title={t('plans.nav.manual')}>
          <Table rowKey="id" size="small" data={manualOrders}
                 columns={[
                   { colKey: 'order_no', title: t('billing.colOrderNo'), width: 150 },
                   { colKey: 'amount_tokens', title: t('billing.colTokens'), width: 120 },
                   { colKey: 'tenant_id', title: t('billing.colTenant'), width: 80, cell: ({ row }: any) => `#${row.tenant_id}` },
                   { colKey: 'created_at', title: t('billing.colTime'), width: 165, cell: ({ row }: any) => fmtTime(row.created_at as string) },
                   { colKey: 'op', title: '', width: 120, cell: ({ row }: any) =>
                     <Button size="small" theme="success" variant="outline" onClick={() => confirmManual(row)}>{t('billing.confirmPayment')}</Button> },
                 ] as never} />
          {!manualOrders.length && <div style={{ textAlign: 'center', color: '#999', padding: 8 }}>{t('billing.noManualOrders')}</div>}
        </Panel>
      )}

      {/* ===== 收银台弹窗 ===== */}
      <Dialog visible={showCheckout} onClose={closeCheckout} header={t('billing.checkout')} width={380}>
        {curOrder && curOrder.status === 'paid' ? (
          <div style={{ textAlign: 'center', padding: '10px 0' }}>
            <div style={{ width: 52, height: 52, lineHeight: '52px', borderRadius: '50%', background: '#e8f5e9', color: '#2e7d32', fontSize: 28, margin: '0 auto 8px' }}>✓</div>
            <p>{tpl('billing.paySuccess', { amount: curOrder.amount_tokens })}</p>
            <Button theme="success" onClick={closeCheckout}>{t('billing.done')}</Button>
          </div>
        ) : (
          <div>
            {curOrder && (
              <div style={{ textAlign: 'center' }}>
                {curOrder.channel === 'manual' ? (
                  <div>
                    <div style={{ fontSize: 13, color: '#667', marginBottom: 6 }}>{t('billing.staticQR')}</div>
                    {isImage(curOrder.qr_content as string)
                      ? <img src={curOrder.qr_content} style={{ maxWidth: 200, borderRadius: 8, border: '1px solid #eee', margin: '8px 0' }} alt="qr" />
                      : <pre style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-all', background: '#f7f9fc', borderRadius: 8, padding: 12, fontSize: 12, maxHeight: 140, overflow: 'auto' }}>{String(curOrder.qr_content)}</pre>}
                  </div>
                ) : (
                  <pre style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-all', background: '#f7f9fc', borderRadius: 8, padding: 12, fontSize: 12, maxHeight: 140, overflow: 'auto' }}>{String(curOrder.qr_content)}</pre>
                )}
                <p style={{ fontSize: 13, color: '#667' }}>{tpl('billing.orderNo', { orderNo: curOrder.order_no })}</p>
              </div>
            )}
            <div style={{ display: 'flex', gap: 10, justifyContent: 'center', flexWrap: 'wrap', marginTop: 12 }}>
              {curOrder?.channel === 'manual' && <Button theme="success" loading={chLoading} onClick={manualConfirm}>{chLoading ? t('billing.processing') : t('billing.iPaid')}</Button>}
              {curOrder?.channel === 'mock' && <Button theme="success" loading={chLoading} onClick={simulatePay}>{t('billing.mockCredit')}</Button>}
              {curOrder && <Button onClick={checkStatus}>{t('billing.refreshStatus')}</Button>}
            </div>
          </div>
        )}
      </Dialog>

      {/* ===== 发票申请弹窗 ===== */}
      <Dialog visible={!!invDlg} onClose={() => setInvDlg(null)} header={t('billing.invoiceDialogTitle')} width={440}
               onConfirm={async () => {
                if (!invDlg) return
                const r = await billingInvoiceCreate({ order_id: Number(invDlg.order.id), title: invDlg.title, tax_no: invDlg.taxNo })
                if (toastResp(r, t('billing.invoiceApplied'))) setInvDlg(null)
              }}>
        <Field label={t('billing.invoiceTitleField')}><Input value={invDlg?.title || ''} onChange={(v) => setInvDlg((d) => (d ? { ...d, title: v } : d))} /></Field>
        <Field label={t('billing.invoiceTaxField')}><Input value={invDlg?.taxNo || ''} onChange={(v) => setInvDlg((d) => (d ? { ...d, taxNo: v } : d))} /></Field>
      </Dialog>
    </>
  )
}

// ---------------- 邀请好友（Vue Referral.vue） ----------------
// 展示我的邀请码、邀请链接、二维码、奖励统计；超管可配置运营参数
export function ReferralP() {
  const ad = useAdmin()
  const [, t] = useT()
  const isSuper = ad.isSuper
  const [refCode, setRefCode] = useState('')
  const [inviteUrl, setInviteUrl] = useState('')
  const [records, setRecords] = useState<Any[]>([])
  const [qrUrl, setQrUrl] = useState('')
  const [invited, setInvited] = useState(0)
  const [trialCount, setTrialCount] = useState(0)
  const [trialTokens, setTrialTokens] = useState(0)
  const [paidTokens, setPaidTokens] = useState(0)
  const [cfg, setCfg] = useState({ enabled: true, reward_tokens: 300000, paid_tokens: 500000, reward_days: 14, paid_days: 0 })

  // 加载邀请数据、二维码 blob、超管运营配置
  useEffect(() => {
    void (async () => {
      try {
        const r: Any = await referralMy()
        if (r.success) {
          setRefCode(r.ref_code || '')
          setInviteUrl(r.invite_url || '')
          setRecords((r.records as Any[]) || [])
          setInvited((r.invited as number) || 0)
          setTrialCount((r.trial_count as number) || 0)
          setTrialTokens((r.trial_tokens as number) || 0)
          setPaidTokens((r.paid_tokens as number) || 0)
        }
      } catch { /* ignore */ }
      const blob = await fetchReferralQrBlob()
      if (blob) setQrUrl(URL.createObjectURL(blob))
      if (isSuper) {
        try {
          const c: Any = await referralConfigGet()
          if (c.success) setCfg({ enabled: !!c.enabled, reward_tokens: c.reward_tokens ?? 300000, paid_tokens: c.paid_reward_tokens ?? 500000, reward_days: c.reward_days ?? 14, paid_days: c.paid_reward_days ?? 0 })
        } catch { /* ignore */ }
      }
    })()
  }, [isSuper])

  // 下载邀请二维码
  function downloadQr() {
    if (!qrUrl) return
    const a = document.createElement('a')
    a.href = qrUrl; a.download = `invite-${refCode || 'code'}.png`
    document.body.appendChild(a); a.click(); a.remove()
  }
  // 兼容旧浏览器的复制回退方案
  function fallbackCopy(text: string) {
    const el = document.createElement('textarea')
    el.value = text
    document.body.appendChild(el)
    el.select()
    try { document.execCommand('copy') } catch { /* ignore */ }
    el.remove()
  }
  // 复制邀请链接（优先 Clipboard API，失败回退 textarea）
  function copyLink() {
    if (!inviteUrl) return
    try {
      if (navigator.clipboard && navigator.clipboard.writeText) {
        void navigator.clipboard.writeText(inviteUrl).catch(() => fallbackCopy(inviteUrl))
      } else fallbackCopy(inviteUrl)
    } catch { fallbackCopy(inviteUrl) }
  }

  return (
    <>
      <Panel title={t('referral.title')} extra={<Space size={8}><Button onClick={downloadQr}>⬇️ {t('referral.downloadQr')}</Button></Space>}>
        <div style={{ display: 'flex', gap: 20, alignItems: 'flex-start', flexWrap: 'wrap' }}>
          <div style={{ flex: 1, minWidth: 320, background: 'rgba(64,128,255,.06)', border: '1px solid rgba(64,128,255,.18)', borderRadius: 10, padding: '14px 16px' }}>
            <div style={{ fontSize: 12, color: '#556' }}>{t('referral.myCode')}</div>
            <div style={{ fontSize: 22, fontWeight: 700, letterSpacing: 2, color: '#1a237e', marginTop: 2 }}>{refCode || '—'}</div>
            <div style={{ fontSize: 12, color: '#667', marginTop: 8 }}>{t('referral.linkLabel')}</div>
            <div style={{ display: 'flex', gap: 8, marginTop: 4 }}>
              <Input readOnly value={inviteUrl} onFocus={(e: any) => e.target.select()} style={{ flex: 1 }} />
              <Button onClick={copyLink}>📋 {t('referral.copy')}</Button>
            </div>
            <div style={{ display: 'flex', gap: 18, flexWrap: 'wrap', marginTop: 12, fontSize: 13, color: '#555' }}>
              <span>👥 {t('referral.invitedCount')}：<b>{invited}</b></span>
              <span>🎁 {t('referral.trialRewards')}：<b>{fmtNum(trialTokens)}</b> token / {trialCount} {t('referral.times')}</span>
              <span>💰 {t('referral.paidRewards')}：<b>{fmtNum(paidTokens)}</b> token</span>
            </div>
          </div>
          {qrUrl && <img src={qrUrl} alt="QR" width={150} height={150} style={{ borderRadius: 10, border: '1px solid #e3e6ef', background: '#fff' }} />}
        </div>

        {/* ===== 超管运营配置 ===== */}
        {isSuper && (
          <div style={{ marginTop: 16, background: 'rgba(255,152,0,.06)', border: '1px solid rgba(255,152,0,.25)', borderRadius: 10, padding: '14px 16px', maxWidth: 560 }}>
            <div style={{ fontWeight: 700, color: '#b26a00', marginBottom: 10 }}>{t('referral.cfgTitle')}</div>
            <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginBottom: 8 }}>
              <span style={{ minWidth: 220, fontSize: 13, color: '#555' }}>{t('referral.cfgEnabled')}</span>
              <Switch value={cfg.enabled} onChange={(v) => setCfg({ ...cfg, enabled: v as boolean })} />
            </div>
            <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginBottom: 8 }}>
              <span style={{ minWidth: 220, fontSize: 13, color: '#555' }}>{t('referral.cfgRewardTokens')}</span>
              <Input type="number" value={String(cfg.reward_tokens)} onChange={(v) => setCfg({ ...cfg, reward_tokens: Number(v) || 0 })} style={{ width: 200 }} />
            </div>
            <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginBottom: 8 }}>
              <span style={{ minWidth: 220, fontSize: 13, color: '#555' }}>{t('referral.cfgPaidTokens')}</span>
              <Input type="number" value={String(cfg.paid_tokens)} onChange={(v) => setCfg({ ...cfg, paid_tokens: Number(v) || 0 })} style={{ width: 200 }} />
            </div>
            {/* 注册邀请奖励有效期（天）：控制体验叠加额度的到期时长，后台可调 */}
            <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginBottom: 8 }}>
              <span style={{ minWidth: 220, fontSize: 13, color: '#555' }}>{t('referral.cfgRewardDays')}</span>
              <Input type="number" value={String(cfg.reward_days)} onChange={(v) => setCfg({ ...cfg, reward_days: Number(v) || 0 })} style={{ width: 200 }} />
            </div>
            {/* 付费邀请奖励有效期（天）：0=永久（默认），>0=限时台账（按最早到期优先扣减） */}
            <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginBottom: 8 }}>
              <span style={{ minWidth: 220, fontSize: 13, color: '#555' }}>{t('referral.cfgPaidDays')}</span>
              <Input type="number" value={String(cfg.paid_days)} onChange={(v) => setCfg({ ...cfg, paid_days: Number(v) || 0 })} style={{ width: 200 }} />
            </div>
            <Button onClick={async () => {
              const c: Any = await referralConfigSave({ enabled: cfg.enabled, reward_tokens: Number(cfg.reward_tokens) || 0, paid_reward_tokens: Number(cfg.paid_tokens) || 0, reward_days: Number(cfg.reward_days) || 0, paid_reward_days: Number(cfg.paid_days) || 0 })
              if (!c.success) window.alert(c.message || '保存失败')
              else window.alert(t('referral.cfgSaved'))
            }}>{t('referral.cfgSave')}</Button>
          </div>
        )}
      </Panel>

      <Panel title={t('referral.title')}>
        <Table rowKey="invitee_uid" size="small" data={records as Any[]}
               columns={[
                 { colKey: 'invitee', title: t('referral.colInvitee'), cell: ({ row }: any) => `${String(row.invitee_name)} (#${String(row.invitee_uid)})` },
                 { colKey: 'invitee_email', title: t('referral.colEmail'), cell: ({ row }: any) => row.invitee_email || '—' },
                 { colKey: 'invite_status', title: t('referral.colInviteStatus'), width: 120, cell: () => <span style={{ color: '#1b8a3f', fontWeight: 600 }}>✅ {t('referral.invSuccess')}</span> },
                 { colKey: 'pay_status', title: t('referral.colPayStatus'), width: 120, cell: ({ row }: any) =>
                   row.paid
                     ? <span style={{ color: '#1b8a3f', fontWeight: 600 }}>✅ {t('referral.payYes')}</span>
                     : <span style={{ color: '#b26a00' }}>⏳ {t('referral.payNo')}</span> },
                 { colKey: 'reward', title: t('referral.colReward'), cell: ({ row }: any) =>
                   row.type === 'trial_stack'
                     ? <>+{fmtNum(row.tokens as number)} token{row.days ? ` / +${row.days} ${t('referral.daysUnit')}` : ''}</>
                     : <>+{fmtNum(row.tokens as number)} token</> },
                 { colKey: 'created_at', title: t('referral.colTime'), width: 165, cell: ({ row }: any) => fmtTime(row.created_at as string) },
               ] as never} />
        {!records.length && <div style={{ textAlign: 'center', color: '#999', padding: 8 }}>{t('referral.empty')}</div>}
      </Panel>
    </>
  )
}

// ---------------- 回调通知（Vue Webhooks.vue） ----------------
// Webhook 列表、新增/编辑/启停/测试/删除
export function WebhooksP() {
  const [, t] = useT()
  const [rows, setRows] = useState<Any[]>([])
  const [dlg, setDlg] = useState<null | Any>(null)
  // 加载 webhook 列表
  const load = useCallback(async () => {
    const r: Any = await apiWebhooks()
    if (r.success) setRows((r.webhooks as Any[]) || [])
  }, [])
  // 初始化加载
  useEffect(() => { void load() }, [load])

  // 切换 webhook 启用状态
  async function toggleWebhook(w: Any) {
    const r: Any = await webhookSave({ id: Number(w.id), url: String(w.url || ''), secret: w.secret ? String(w.secret) : undefined, events: String(w.events || 'translation.completed'), enabled: w.enabled ? 0 : 1 })
    if (toastResp(r)) void load()
  }

  return (
    <Panel title={t('webhooks.title')} extra={<Button theme="primary" onClick={() => setDlg({ url: '', secret: '', events: 'translation.completed' })}>＋ {t('webhooks.saveConfig')}</Button>}>
      <p style={{ fontSize: 13, color: '#667', margin: '0 0 10px' }}>{t('webhooks.hint')}</p>
      <Space size={8} align="center" style={{ marginBottom: 10 }}>
        <Input value={String(dlg?.url || '')} onChange={(v) => setDlg((d) => (d ? { ...d, url: v } : d))} placeholder={t('webhooks.urlPlaceholder')} style={{ flex: 1, minWidth: 240 }} />
        <Input value={String(dlg?.secret || '')} onChange={(v) => setDlg((d) => (d ? { ...d, secret: v } : d))} placeholder={t('webhooks.secretPlaceholder')} style={{ width: 200 }} />
        <Input value={String(dlg?.events || '')} onChange={(v) => setDlg((d) => (d ? { ...d, events: v } : d))} placeholder={t('webhooks.eventsPlaceholder')} style={{ flex: 1, minWidth: 200 }} />
        {!!dlg && <Button theme="primary" onClick={async () => { if (!dlg?.url) { window.alert(t('webhooks.urlRequired')); return } const r: Any = await webhookSave({ id: dlg.id ? Number(dlg.id) : undefined, url: String(dlg.url || ''), secret: dlg.secret ? String(dlg.secret) : undefined, events: String(dlg.events || 'translation.completed') }); if (toastResp(r)) { setDlg(null); void load() } }}>{t('webhooks.saveConfig')}</Button>}
      </Space>

      <Table rowKey="id" size="small" data={rows}
             columns={[
               { colKey: 'id', title: t('webhooks.colId'), width: 70 },
               { colKey: 'url', title: t('webhooks.colUrl'), ellipsis: true, cell: ({ row }: any) => <span style={{ wordBreak: 'break-all' }}>{row.url}</span> },
               { colKey: 'events', title: t('webhooks.colEvents'), width: 200 },
               { colKey: 'enabled', title: t('webhooks.colStatus'), width: 90, cell: ({ row }: any) => <Tag theme={row.enabled ? 'success' : 'default'}>{row.enabled ? t('webhooks.enable') : t('webhooks.disable')}</Tag> },
               { colKey: 'op', title: t('webhooks.colActions'), width: 260, cell: ({ row }: any) => (
                 <Space size={4}>
                   <Button size="small" variant="text" onClick={async () => { const r: Any = await webhookTest(Number(row.id)); window.alert((r.message as string) || (r.success ? t('webhooks.testSent') : t('webhooks.testFailed'))) }}>{t('webhooks.test')}</Button>
                   <Button size="small" variant="text" onClick={() => toggleWebhook(row)}>{row.enabled ? t('webhooks.disable') : t('webhooks.enable')}</Button>
                   <Button size="small" variant="text" onClick={() => setDlg({ ...row })}>{t('webhooks.saveConfig')}</Button>
                   <Popconfirm content={t('webhooks.confirmDelete')} onConfirm={async () => { await webhookDelete(Number(row.id)); void load() }}>
                     <Button size="small" variant="text" theme="danger">{t('webhooks.delete')}</Button>
                   </Popconfirm>
                 </Space>
               ) },
             ] as never} />
    </Panel>
  )
}

// ---------------- 开放 API Key + 在线文档（Vue ApiKeys.vue + DocsEdit） ----------------
// API Key 创建/启停/轮换/限额/删除；超管可维护多语言 OpenAPI 在线文档
export function ApiKeysP() {
  const ad = useAdmin()
  const [, t, tpl] = useT()
  const isSuper = ad.isSuper
  const [keys, setKeys] = useState<Any[]>([])
  const [newKey, setNewKey] = useState('')
  const [copied, setCopied] = useState(false)
  const [kForm, setKForm] = useState<Any>({ name: '', perms: 'translate', daily_call_limit: '' })
  // 文档在线维护（仅超管）
  const [docsCardOpen, setDocsCardOpen] = useState(false)
  const [docsLang, setDocsLang] = useState<'zh' | 'en'>('zh')
  const [docsMDZh, setDocsMDZh] = useState('')
  const [docsMDEn, setDocsMDEn] = useState('')
  const [docsSaving, setDocsSaving] = useState(false)
  const [docsDefaultBadge, setDocsDefaultBadge] = useState(false)
  const [docsLoaded, setDocsLoaded] = useState(false)
  const docsMD = docsLang === 'en' ? docsMDEn : docsMDZh
  const setDocsMD = (v: string) => { if (docsLang === 'en') setDocsMDEn(v); else setDocsMDZh(v) }

  // 加载 API Key 列表
  const loadKeys = useCallback(async () => {
    const r: Any = await apiApiKeys()
    if (r.success) setKeys((r.keys as Any[]) || (r.api_keys as Any[]) || [])
  }, [])
  // 初始加载 API Keys 与 OpenAPI 文档元数据
  useEffect(() => {
    void loadKeys()
    void (async () => { const d: Any = await getOpenAPIDocs(); if (d.success) { setDocsMDZh(d.md_zh || ''); setDocsMDEn(d.md_en || ''); setDocsDefaultBadge(!!d.default_zh && !!d.default_en) } })()
  }, [loadKeys])

  // 复制新创建的 Key（仅展示一次）
  async function copyNewKey() {
    try { await navigator.clipboard.writeText(newKey); setCopied(true); setTimeout(() => setCopied(false), 2000) }
    catch { window.alert(t('apikeys.copyFail')) }
  }
  // 创建 API Key
  async function createKey() {
    if (!kForm.name) { window.alert(t('apikeys.nameRequired')); return }
    const r: Any = await apiKeyCreate({ name: String(kForm.name || ''), perms: String(kForm.perms || 'translate'), daily_call_limit: kForm.daily_call_limit === '' ? undefined : Number(kForm.daily_call_limit) })
    if (!r.success) { window.alert(r.message || ''); return }
    setNewKey(r.api_key || '')
    setKForm({ name: '', perms: 'translate', daily_call_limit: '' })
    await loadKeys()
  }
  // 启停 API Key
  async function toggleKey(k: Any) { await apiKeyStatus(Number(k.id), k.status === 'active' ? 'disabled' : 'active'); await loadKeys() }
  // 删除 API Key
  async function deleteKey(k: Any) { if (!window.confirm(t('apikeys.confirmDelete'))) return; await apiKeyDelete(Number(k.id)); await loadKeys() }
  // 轮换 API Key（生成新 key，旧 key 失效）
  async function rotateKey(k: Any) {
    if (!window.confirm(tpl('apikeys.confirmRotate', { name: k.name }))) return
    const r: Any = await apiKeyRotate(Number(k.id))
    if (!r.success) { window.alert(r.message || ''); return }
    setNewKey(r.api_key || '')
    await loadKeys()
  }
  // 设置单日调用上限
  async function setLimit(k: Any) {
    const input = window.prompt(tpl('apikeys.limitPrompt', { name: k.name, cur: k.daily_call_limit || 0 }))
    if (input === null) return
    const n = Number(input)
    if (!Number.isFinite(n) || n < 0) { window.alert(t('apikeys.limitInvalid')); return }
    const r: Any = await apiKeyLimit(Number(k.id), Math.floor(n))
    if (!r.success) { window.alert(r.message || ''); return }
    await loadKeys()
  }

  // ---- OpenAPI 在线文档维护（仅超管） ----
  // 加载多语言 Markdown 文档与是否使用默认文档标记
  async function loadDocs() {
    if (docsLoaded) return
    try {
      const r: Any = await getOpenAPIDocs()
      if (r.success) { setDocsMDZh(r.md_zh || ''); setDocsMDEn(r.md_en || ''); setDocsDefaultBadge(!!r.default_zh && !!r.default_en); setDocsLoaded(true) }
    } catch { /* 非超管或网络失败：静默 */ }
  }
  // 展开文档编辑区时加载文档
  useEffect(() => { if (isSuper && docsCardOpen) void loadDocs() }, [isSuper, docsCardOpen])
  async function refreshDocsState() { setDocsLoaded(false); await loadDocs() }
  // 保存当前语言文档
  async function saveDocs() {
    if (!window.confirm(t('docsEdit.confirmSave'))) return
    setDocsSaving(true)
    try {
      const r: Any = await saveOpenAPIDocs({ lang: docsLang, md: docsMD })
      if (!r.success) { window.alert(r.message || ''); return }
      window.alert(t('docsEdit.saved'))
      await refreshDocsState()
    } finally { setDocsSaving(false) }
  }
  // 预览渲染后的 HTML
  async function previewDocs() {
    try {
      const r: Any = await previewOpenAPIDocs({ lang: docsLang, md: docsMD })
      if (!r.success) { window.alert(r.message || ''); return }
      const w = window.open('', '_blank')
      if (w) { w.document.open(); w.document.write(r.html as string); w.document.close() }
    } catch (e) { window.alert(String(e)) }
  }
  // 导入本地 Markdown 文件
  function importDocs(e: any) {
    const file = e.target.files ? e.target.files[0] : null
    if (!file) return
    const reader = new FileReader()
    reader.onload = () => setDocsMD(String(reader.result || ''))
    reader.readAsText(file)
    e.target.value = ''
  }
  // 导出当前语言文档为 Markdown 文件
  function exportDocs() {
    const blob = new Blob([docsMD], { type: 'text/markdown;charset=utf-8' })
    const a = document.createElement('a')
    a.href = URL.createObjectURL(blob); a.download = 'openapi-docs.md'
    a.click(); URL.revokeObjectURL(a.href)
  }
  // 重置为默认文档（传空 md 让后端回退）
  async function resetDocs() {
    if (!window.confirm(t('docsEdit.confirmReset'))) return
    const r: Any = await saveOpenAPIDocs({ lang: docsLang, md: '' })
    if (!r.success) { window.alert(r.message || ''); return }
    setDocsMD('')
    await refreshDocsState()
    window.alert(t('docsEdit.resetDone'))
  }
  // 在新标签打开在线文档页面
  function openDocs() { window.open('/openapi/docs', '_blank') }

  // 判断 key 今日调用是否已达上限
  function isKeyOverQuota(k: Any): boolean {
    if (!k.daily_call_limit || Number(k.daily_call_limit) <= 0) return false
    const today = new Date().toISOString().slice(0, 10)
    const used = k.calls_today_date === today ? Number(k.calls_today) : 0
    return used >= Number(k.daily_call_limit)
  }
  function fmtToday(k: Any): string {
    const limit = k.daily_call_limit && Number(k.daily_call_limit) > 0 ? k.daily_call_limit : '∞'
    const today = new Date().toISOString().slice(0, 10)
    const used = k.calls_today_date === today ? Number(k.calls_today) : 0
    return `${used}/${limit}`
  }

  return (
    <>
      <Panel title={t('apikeys.title')} extra={
        <Space size={8}>
          <Button variant="outline" onClick={openDocs}>📄 {t('apikeys.docs')}</Button>
          {isSuper && <Button onClick={() => setDocsCardOpen((v) => !v)}>{docsCardOpen ? '▲' : '▼'} {t('docsEdit.title')}</Button>}
        </Space>
      }>
        {!!newKey && (
          <div style={{ background: '#fff8e1', border: '1px solid #ffe082', borderRadius: 8, padding: 10, marginBottom: 10 }}>
            ⚠️ {t('apikeys.newKeyOnce')}：<b style={{ userSelect: 'all' }}>{newKey}</b>
            <Button size="small" style={{ marginLeft: 8 }} onClick={copyNewKey}>📋 {t('apikeys.copy')}</Button>
            {copied && <span style={{ fontSize: 12, color: '#667' }}> {t('apikeys.copied')}</span>}
          </div>
        )}
        <Space size={8} align="center">
          <Input value={String(kForm.name || '')} onChange={(v) => setKForm({ ...kForm, name: v })} placeholder={t('apikeys.keyName')} style={{ width: 180 }} />
          <Select value={String(kForm.perms || 'translate')} onChange={(v) => setKForm({ ...kForm, perms: v })} style={{ width: 140 }}
                  options={['translate', 'kb', 'all'].map((x) => ({ label: x, value: x }))} />
          <Input type="number" value={String(kForm.daily_call_limit || '')} onChange={(v) => setKForm({ ...kForm, daily_call_limit: v })} placeholder={t('apikeys.limitPlaceholder')} style={{ width: 130 }} />
          <Button onClick={createKey}>{t('apikeys.create')}</Button>
        </Space>

        <Table rowKey="id" size="small" data={keys} style={{ marginTop: 10 }}
               columns={[
                 { colKey: 'id', title: t('apikeys.colId'), width: 70 },
                 { colKey: 'key_prefix', title: t('apikeys.colPrefix'), width: 160, cell: ({ row }: any) => maskKey(`${row.key_prefix || ''}…`) },
                 { colKey: 'name', title: t('apikeys.colName') },
                 { colKey: 'perms', title: t('apikeys.colPerms'), width: 100 },
                 { colKey: 'status', title: t('apikeys.colStatus'), width: 90, cell: ({ row }: any) => <Tag theme={row.status === 'active' ? 'success' : 'default'}>{row.status}</Tag> },
                 { colKey: 'calls', title: t('apikeys.colCalls'), width: 110, cell: ({ row }: any) => <span style={{ color: isKeyOverQuota(row) ? '#c62828' : '' }}>{fmtToday(row)}</span> },
                 { colKey: 'op', title: t('apikeys.colActions'), width: 300, cell: ({ row }: any) => (
                   <Space size={4}>
                     <Button size="small" variant="text" onClick={() => toggleKey(row)}>{row.status === 'active' ? t('apikeys.disable') : t('apikeys.enable')}</Button>
                     <Button size="small" variant="text" onClick={() => rotateKey(row)}>{t('apikeys.rotate')}</Button>
                     <Button size="small" variant="text" onClick={() => setLimit(row)}>📐 {t('apikeys.setLimit')}</Button>
                     <Popconfirm content={t('apikeys.confirmDelete')} onConfirm={() => deleteKey(row)}>
                       <Button size="small" variant="text" theme="danger">{t('apikeys.delete')}</Button>
                     </Popconfirm>
                   </Space>
                 ) },
               ] as never} />
      </Panel>

      {/* ===== 超管文档编辑区 ===== */}
      {isSuper && docsCardOpen && (
        <Panel title={t('docsEdit.title')}>
          <div style={{ fontSize: 13, color: '#667', marginBottom: 8 }}>{t('docsEdit.hint')}</div>
          <Space size={6} style={{ marginBottom: 8 }}>
            <Button size="small" theme={docsLang === 'zh' ? 'primary' : 'default'} onClick={() => setDocsLang('zh')}>{t('docsEdit.langZh')}</Button>
            <Button size="small" theme={docsLang === 'en' ? 'primary' : 'default'} onClick={() => setDocsLang('en')}>{t('docsEdit.langEn')}</Button>
          </Space>
          <Textarea autosize={{ minRows: 16 }} value={docsMD} onChange={(v) => setDocsMD(v as string)} placeholder={t('docsEdit.placeholder')}
                    style={{ width: '100%', fontFamily: 'SFMono-Regular, Consolas, monospace', fontSize: 12.5, lineHeight: 1.55, resize: 'vertical' }} />
          <Space size={8} style={{ marginTop: 8 }}>
            <Button theme="success" disabled={docsSaving || !docsMD.trim()} onClick={saveDocs}>💾 {docsSaving ? t('docsEdit.saving') : t('common.save')}</Button>
            <Button disabled={!docsMD.trim()} onClick={previewDocs}>👁 {t('docsEdit.preview')}</Button>
            <label style={{ cursor: 'pointer' }}>
              📂 {t('docsEdit.import')}
              <input type="file" accept=".md,.markdown,.txt" hidden onChange={importDocs} />
            </label>
            <Button onClick={exportDocs}>⬇️ {t('docsEdit.export')}</Button>
            <Button theme="danger" onClick={resetDocs}>↺ {t('docsEdit.reset')}</Button>
            {docsDefaultBadge && <span style={{ fontSize: 12, color: '#667' }}>{t('docsEdit.isDefault')}</span>}
          </Space>
        </Panel>
      )}
    </>
  )
}
