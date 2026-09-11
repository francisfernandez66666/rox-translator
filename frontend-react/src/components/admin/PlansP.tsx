// ============================================================================
// components/admin/PlansP.tsx — 套餐中心面板
// 职责：计费配置、套餐订阅、充值、订单/发票、配额与超管商业包管理
// 从 panels_c.tsx 拆分
// ============================================================================
import { useCallback, useEffect, useRef, useState } from 'react'
import type { ChangeEvent } from 'react'
import {
  Button, Table, Dialog, Input, Select, Switch, Tag, Space, Popconfirm, MessagePlugin,
} from 'tdesign-react'
import { confirmDialog } from '@/components/uiDialogs'
import {
  billingQuota, billingQuotaSave,
  billingOrders, billingInvoices, billingInvoiceCreate,
  payCreate, payStatus, paySimulate, payManualConfirm, manualConfirmOrders, adminOrderPay,
  plans as apiPlans, myPackage, packageSubscribe, packageUpgrade,
  adminPackages, adminPackageCreate, adminPackageUpdate, adminPackageDelete,
  adminPackageSettings, adminPackageSettingsSave, adminQRUpload,
  authHeaders,
} from '@/api'
import { Panel, Field, toastResp, num } from './parts'
import { fmtNum, fmtTime } from '@/lib/ui'
import { useAdmin } from '@/stores/admin'
import { useT } from '@/i18n'

type Any = Record<string, any>

function isImage(s?: string): boolean {
  if (!s) return false
  if (s.indexOf('data:image') === 0) return true
  if (s.indexOf('/api/qr-image/') === 0) return true
  const idx = s.indexOf('.')
  if (idx < 0) return false
  const tail = s.slice(idx + 1).split(/[?#]/)[0].toLowerCase()
  return ['png', 'jpg', 'jpeg', 'gif', 'webp'].indexOf(tail) >= 0 && (s.indexOf('http://') === 0 || s.indexOf('https://') === 0)
}

function orderStatusLabel(s: string, t: (k: string) => string): string {
  const m: Record<string, string> = {
    pending: t('billing.stPending'), paid: t('billing.stPaid'),
    refunded: t('billing.stRefunded'), cancelled: t('billing.stCancelled'),
  }
  return m[s] || s
}

function statusTheme(s: string): string {
  return ({ pending: 'warning', paid: 'success', refunded: 'default', cancelled: 'default' } as Record<string, string>)[s] || 'default'
}

export function PlansP() {
  const ad = useAdmin()
  const [, t, tpl] = useT()
  const isSuper = ad.isSuper

  const [pkg, setPkg] = useState<Any>({})
  const [planList, setPlanList] = useState<Any[]>([])
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
  const [quotaForm, setQuotaForm] = useState<Any>({ qps: 10, concurrent: 3, max_daily_chars: 0, max_daily_tokens: 0 })
  const [pkgs, setPkgs] = useState<Any[]>([])
  const [billingEnforced, setBillingEnforced] = useState(false)
  const [freeTrialTokens, setFreeTrialTokens] = useState(300000)
  const [freeTrialDays, setFreeTrialDays] = useState(14)
  const [markupMultiplier, setMarkupMultiplier] = useState(1.5)
  const [tokensPerSentence, setTokensPerSentence] = useState(500)
  const [staticQRImage, setStaticQRImage] = useState('')
  const [manualOrders, setManualOrders] = useState<Any[]>([])
  const [invDlg, setInvDlg] = useState<null | { order: Any; title: string; taxNo: string }>(null)

  const setOrder = (o: Any | null) => { orderRef.current = o; setCurOrder(o) }
  const pkgExpiresLabel = (() => {
    const v = pkg.package_expires as string
    if (!v) return ''
    const d = new Date(v)
    return isNaN(d.getTime()) ? '' : d.toISOString().slice(0, 10)
  })()

  function startPolling() { stopPolling(); payTimer.current = setInterval(checkStatus, 3000) }
  function stopPolling() { if (payTimer.current) { clearInterval(payTimer.current); payTimer.current = null } }
  useEffect(() => () => stopPolling(), [])

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
  const loadOrders = useCallback(async () => {
    const r: Any = await billingOrders()
    if (r.success) setOrders((r.orders as Any[]) || [])
  }, [])
  const loadInvoices = useCallback(async () => {
    const r: Any = await billingInvoices()
    if (r.success) setInvoices((r.invoices as Any[]) || [])
  }, [])
  const loadQuota = useCallback(async () => {
    const r: Any = await billingQuota()
    if (r.success) setQuotaForm({
      qps: (r.qps as number) || 10, concurrent: (r.concurrent as number) || 3,
      max_daily_chars: (r.max_daily_chars as number) || 0, max_daily_tokens: (r.max_daily_tokens as number) ?? 0,
    })
  }, [])
  const loadPkgs = useCallback(async () => {
    if (!isSuper) return
    const r: Any = await adminPackages()
    if (r.success) setPkgs((r.packages as Any[]) || [])
    const cfg: Any = await adminPackageSettings()
    if (cfg.success) {
      setBillingEnforced(cfg.billing_enforced === '1' || cfg.billing_enforced === true)
      if (cfg.free_trial_tokens) setFreeTrialTokens(Number(cfg.free_trial_tokens))
      if (cfg.free_trial_days) setFreeTrialDays(Number(cfg.free_trial_days))
      if (typeof cfg.billing_markup_multiplier === 'number') setMarkupMultiplier(cfg.billing_markup_multiplier)
      if (cfg.estimate_tokens_per_sentence) setTokensPerSentence(Number(cfg.estimate_tokens_per_sentence))
      if (cfg.pay_mode) setPayModeCfg(cfg.pay_mode as string)
      if (cfg.static_qr_image) setStaticQRImage(cfg.static_qr_image as string)
    }
    const m: Any = await manualConfirmOrders()
    if (m.success) setManualOrders((m.orders as Any[]) || [])
  }, [isSuper])

  const loadAll = useCallback(async () => {
    if (!isSuper) await loadPackage()
    await Promise.all([loadOrders(), loadInvoices(), loadQuota(), loadPkgs()])
  }, [isSuper, loadPackage, loadOrders, loadInvoices, loadQuota, loadPkgs])
  useEffect(() => { void loadAll() }, [loadAll])

  async function subscribe(pl: Any) {
    const r: Any = await packageSubscribe(String(pl.code))
    if (!r.success) return
    const o = r.order as Any
    if (o) { setOrder(o); setShowCheckout(true); if (o.channel !== 'manual') startPolling() }
    await loadPackage()
  }
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
    if (r.credit_money > 0) void MessagePlugin.success(tpl('billing.upgradeCredit', { money: r.credit_money }))
    const o = r.order as Any
    if (o) { setOrder(o); setShowCheckout(true); if (o.channel !== 'manual') startPolling() }
    await loadPackage()
  }
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
  async function resumePay(_o: Any) {
    const r: Any = await payStatus(Number(_o.id))
    const o = (r.success ? (r.order as Any) : _o)
    setOrder(o); setShowCheckout(true)
    if (o && o.channel !== 'manual') startPolling()
  }
  function closeCheckout() { setShowCheckout(false); stopPolling(); void loadOrders(); if (!isSuper) void loadPackage() }
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
  async function simulatePay() {
    const o = orderRef.current
    if (!o) return
    setChLoading(true)
    try { const r: Any = await paySimulate(Number(o.id)); if (r.success) await checkStatus() } finally { setChLoading(false) }
  }
  async function manualConfirm() {
    const o = orderRef.current
    if (!o) return
    setChLoading(true)
    try {
      const r: Any = await payManualConfirm(Number(o.id))
      if (r.success) { void MessagePlugin.success(t('billing.manualNotify')); stopPolling(); closeCheckout() }
      else void MessagePlugin.error((r.message as string) || t('billing.iPaidFailed'))
    } catch (e: any) { void MessagePlugin.error(e?.message || t('common.fail')) }
    finally { setChLoading(false) }
  }

  const [qrImg, setQrImg] = useState('')
  useEffect(() => {
    let alive = true
    setQrImg('')
    const content = curOrder?.qr_content as string | undefined
    if (content && !isImage(content)) {
      void (async () => {
        try {
          const res = await fetch(`/api/qr/render?text=${encodeURIComponent(content)}`, { headers: authHeaders() })
          if (!res.ok) return
          const blob = await res.blob()
          if (alive) setQrImg(URL.createObjectURL(blob))
        } catch { /* 渲染失败则回退文本展示 */ }
      })()
    }
    return () => { alive = false }
  }, [curOrder?.qr_content])

  async function saveQuota() {
    await billingQuotaSave({
      qps: Math.max(1, Number(quotaForm.qps) || 0),
      concurrent: Math.max(1, Number(quotaForm.concurrent) || 0),
      max_daily_chars: Math.max(0, Number(quotaForm.max_daily_chars) || 0),
      max_daily_tokens: Math.max(0, Number(quotaForm.max_daily_tokens) || 0),
    })
    await loadQuota()
  }

  const [pkgForm, setPkgForm] = useState<Any>({ code: '', name: '', ptype: 'paid', sentences: 1000, price_money: 0, duration_days: 30 })
  async function createPkg() {
    if (!pkgForm.code || !pkgForm.name) { void MessagePlugin.warning(t('apikeys.nameRequired')); return }
    const r: Any = await adminPackageCreate(pkgForm as any)
    if (toastResp(r)) { setPkgForm({ code: '', name: '', ptype: 'paid', sentences: 1000, price_money: 0, duration_days: 30 }); void loadPkgs() }
  }
  async function togglePkg(p: Any) { await adminPackageUpdate({ id: Number(p.id), enabled: p.enabled ? 0 : 1 }); void loadPkgs() }
  async function deletePkg(p: Any) {
    if (!(await confirmDialog({ body: t('webhooks.confirmDelete') }))) return
    await adminPackageDelete(Number(p.id)); void loadPkgs()
  }
  async function saveEnforce() { const r: Any = await adminPackageSettingsSave({ billing_enforced: billingEnforced ? '1' : '0' } as never); toastResp(r, t('common.save')) }
  async function saveBillingParams() {
    if (!(freeTrialTokens > 0)) { void MessagePlugin.warning(t('packages.trialTokensInvalid')); return }
    if (!(freeTrialDays > 0)) { void MessagePlugin.warning(t('packages.trialDaysInvalid')); return }
    if (!(markupMultiplier >= 1)) { void MessagePlugin.warning(t('packages.markupInvalid')); return }
    if (!(tokensPerSentence > 0)) { void MessagePlugin.warning(t('packages.rateInvalid')); return }
    const r: Any = await adminPackageSettingsSave({
      free_trial_tokens: freeTrialTokens, free_trial_days: freeTrialDays,
      billing_markup_multiplier: markupMultiplier, estimate_tokens_per_sentence: tokensPerSentence,
    } as never)
    toastResp(r, t('common.save'))
  }
  async function savePayMode() {
    const r: Any = await adminPackageSettingsSave({ pay_mode: payModeCfg } as never)
    if (toastResp(r, t('common.save'))) setPayMode(payModeCfg)
  }
  async function saveStaticQR() { const r: Any = await adminPackageSettingsSave({ static_qr_image: staticQRImage } as never); toastResp(r, t('common.save')) }
  const [qrUploading, setQrUploading] = useState(false)
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
        void MessagePlugin.error((r.message as string) || t('common.saveFail'))
      }
    } catch (err: any) {
      void MessagePlugin.error(err?.message || t('common.saveFail'))
    } finally { setQrUploading(false) }
  }
  async function confirmManual(o: Any) {
    const r: Any = await adminOrderPay(Number(o.id), Number(o.tenant_id) || 0)
    if (r.success) { void MessagePlugin.success(t('billing.manualConfirmed')); await Promise.all([loadPkgs(), loadOrders()]) }
    else void MessagePlugin.error((r.message as string) || t('billing.iPaidFailed'))
  }

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
    { label: t('billing.chMock'), value: 'mock' },
  ]

  return (
    <>
      {!isSuper && (
        <Panel title={t('plans.nav.current')}>
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit,minmax(150px,1fr))', gap: 10 }}>
            <div style={{ background: '#f7f9fc', borderRadius: 8, padding: '12px 14px', display: 'flex', flexDirection: 'column', gap: 2 }}>
              <b style={{ fontSize: 20, color: 'var(--td-brand-color-active, #1f33d6)' }}>{fmtNum(pkg.balance_tokens as number)}</b><span style={{ fontSize: 12, color: '#78909c' }}>{t('usage.currentBalance')}</span>
            </div>
            <div style={{ background: '#f7f9fc', borderRadius: 8, padding: '12px 14px', display: 'flex', flexDirection: 'column', gap: 2 }}>
              <b style={{ fontSize: 20, color: '#e65100' }}>{fmtNum(pkg.sub_grants_left as number)}</b><span style={{ fontSize: 12, color: '#78909c' }}>{t('plans.balanceGrants')}</span>
            </div>
            <div style={{ background: '#f7f9fc', borderRadius: 8, padding: '12px 14px', display: 'flex', flexDirection: 'column', gap: 2 }}>
              <b style={{ fontSize: 20, color: '#2e7d32' }}>{fmtNum(pkg.permanent_balance as number)}</b><span style={{ fontSize: 12, color: '#78909c' }}>{t('plans.balancePermanent')}</span>
            </div>
            <div style={{ background: '#f7f9fc', borderRadius: 8, padding: '12px 14px', display: 'flex', flexDirection: 'column', gap: 2 }}>
              <b style={{ fontSize: 20, color: 'var(--td-brand-color-active, #1f33d6)' }}>{fmtNum(pkg.tokens_used_month as number)}</b><span style={{ fontSize: 12, color: '#78909c' }}>{t('plans.usedMonth')}</span>
            </div>
          </div>
          <div style={{ marginTop: 10, fontSize: 13, color: '#667' }}>
            {tpl('billing.myPackageCode', { code: (pkg.package_code as string) || '—' })}
            {pkgExpiresLabel ? ` · ${t('plans.expiresAt')}: ${pkgExpiresLabel}` : ''}
            {' · '}{tpl('billing.myPackageBalance', { balance: pkg.sentence_balance ?? '—' })}
          </div>
          {(() => {
            const total = Number(pkg.balance_tokens ?? 0)
            const hasPlan = !!(pkg.package_code && pkg.package_code !== 'trial')
            if (total > 0 || hasPlan) return null
            return (
              <div style={{ marginTop: 10, padding: '10px 14px', borderRadius: 8, background: '#fff7e6', border: '1px solid #ffd591', fontSize: 13, color: '#ad6800', lineHeight: 1.7 }}>
                {t('plans.exhaustedHint')}
                <Space size={6} style={{ marginTop: 6 }}>
                  <Button size="small" theme="warning" onClick={() => { document.getElementById('plans-shop')?.scrollIntoView({ behavior: 'smooth' }) }}>{t('plans.goSubscribe')}</Button>
                  <Button size="small" variant="outline" theme="warning" onClick={() => { document.getElementById('plans-topup')?.scrollIntoView({ behavior: 'smooth' }) }}>{t('plans.goTopup')}</Button>
                </Space>
              </div>
            )
          })()}
        </Panel>
      )}

      {!isSuper && (
        <Panel id="plans-shop" title={t('plans.nav.shop')}>
          {planGroups.map((g) => (
            <div key={g.type}>
              <div style={{ fontWeight: 600, fontSize: 14, color: '#455a64', margin: '10px 0 6px' }}>{g.title}</div>
              <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill,minmax(190px,1fr))', gap: 12 }}>
                {g.items.map((pl) => (
                  <div key={pl.id} style={{ border: '1px solid #e3e6ef', borderRadius: 8, padding: 14, display: 'flex', flexDirection: 'column', gap: 6, background: '#fff' }}>
                    <div style={{ fontWeight: 600, fontSize: 14 }}>{pl.name}</div>
                    <div style={{ fontSize: 22, fontWeight: 700, color: 'var(--td-brand-color-active, #1f33d6)' }}>¥{pl.price_money}<small style={{ fontSize: 12, color: '#90a4ae', fontWeight: 400 }}>{pl.ptype === 'paid' ? ` /${pl.duration_days}d` : ''}</small></div>
                    <ul style={{ margin: '0 0 4px 16px', padding: 0, fontSize: 13, color: '#607d8b', lineHeight: 1.7 }}>
                      <li>{tpl('billing.pkgSentences', { n: pl.sentences })}</li>
                      <li>{t('packages.type.' + pl.ptype)}</li>
                    </ul>
                    <Button theme="success" onClick={() => {
                      if (isUpgradePlan(pl)) upgrade(pl)
                      else subscribe(pl)
                    }}>{isUpgradePlan(pl) ? t('plans.upgrade') : t('billing.subscribeNow')}</Button>
                  </div>
                ))}
                {!g.items.length && <div style={{ color: '#999', fontSize: 13 }}>{t('billing.noPlans')}</div>}
              </div>
            </div>
          ))}
        </Panel>
      )}

      {!isSuper && (
        <Panel id="plans-topup" title={t('plans.nav.topup')}>
          <div style={{ fontSize: 13, color: '#667', marginBottom: 8 }}>{t('billing.onlineTopUpHint')}</div>
          <Space size={8} align="center">
            <Select value={chForm.channel} onChange={(v) => setChForm({ ...chForm, channel: v as string })} style={{ width: 200 }} options={chOptions} />
            <Input type="number" value={String(chForm.tokens)} onChange={(v) => setChForm({ ...chForm, tokens: Number(v) || 0 })} placeholder={t('billing.tokenCount')} style={{ width: 180 }} />
            <Button theme="success" loading={chLoading} onClick={openCheckout}>{chLoading ? t('billing.ordering') : t('billing.goPay')}</Button>
          </Space>
          {curOrder && curOrder.status === 'pending' && (
            <p style={{ color: 'var(--td-brand-color-active, #1f33d6)', fontSize: 13, marginTop: 8 }}>{tpl('billing.currentOrder', { orderNo: curOrder.order_no, amount: curOrder.amount_tokens })}</p>
          )}
        </Panel>
      )}

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

      {isSuper && (
        <Panel title={t('plans.nav.ops')}>
          <Space size={8} align="center">
            <Switch value={billingEnforced} onChange={(v) => setBillingEnforced(v as boolean)} />
            <span style={{ color: billingEnforced ? '#2e7d32' : '#888', fontWeight: 600 }}>{billingEnforced ? t('billing.enforcedOn') : t('billing.enforcedOff')}</span>
            <Button onClick={saveEnforce}>{t('common.save')}</Button>
          </Space>
          <div style={{ marginTop: 12 }}>
            <Space size={8} align="center">
              <span style={{ fontSize: 13, color: '#556' }}>{t('packages.trialTokensLabel')}</span>
              <Input type="number" value={num(freeTrialTokens)} onChange={(v) => setFreeTrialTokens(Number(v) || 0)} style={{ width: 120 }} />
              <span style={{ fontSize: 13, color: '#556' }}>{t('packages.trialDaysLabel')}</span>
              <Input type="number" value={num(freeTrialDays)} onChange={(v) => setFreeTrialDays(Number(v) || 0)} style={{ width: 80 }} />
              <span style={{ fontSize: 13, color: '#556', marginLeft: 12 }}>{t('packages.markupLabel')}</span>
              <Input type="number" value={num(markupMultiplier)} onChange={(v) => setMarkupMultiplier(Math.max(0, Number(v) || 0))} style={{ width: 120 }} />
              <span style={{ fontSize: 13, color: '#556', marginLeft: 12 }}>{t('packages.rateLabel')}</span>
              <Input type="number" value={num(tokensPerSentence)} onChange={(v) => setTokensPerSentence(Math.max(0, Number(v) || 0))} style={{ width: 120 }} />
              <Button onClick={saveBillingParams}>{t('common.save')}</Button>
            </Space>
            <div style={{ fontSize: 12, color: '#889', marginTop: 6 }}>{t('packages.markupHint')}</div>
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
              <div style={{ fontSize: 12, color: '#889', marginBottom: 4 }}>{t('packages.staticQRHint')}</div>
              <Space size={8} align="center">
                <Input value={staticQRImage} onChange={(v) => setStaticQRImage(v)} placeholder={t('packages.staticQRPlaceholder')} style={{ width: 360 }} />
                <input type="file" accept=".png,.jpg,.jpeg,.gif,.webp" style={{ fontSize: 12 }} onChange={uploadStaticQR} disabled={qrUploading} />
                {qrUploading && <span style={{ fontSize: 12, color: '#889' }}>…</span>}
                <Button onClick={saveStaticQR}>{t('common.save')}</Button>
              </Space>
              {isImage(staticQRImage) && (
                <div style={{ marginTop: 8, display: 'inline-block', border: '1px dashed #d0d5e0', borderRadius: 8, padding: 8 }}>
                  <img src={staticQRImage} alt="qr" style={{ maxWidth: 160, maxHeight: 160, borderRadius: 6, display: 'block' }} />
                </div>
              )}
            </div>
          )}
        </Panel>
      )}

      {isSuper && (
        <Panel title={t('plans.nav.pkgMgmt')}>
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
                      : qrImg
                        ? <img src={qrImg} style={{ maxWidth: 200, borderRadius: 8, border: '1px solid #eee', margin: '8px 0', background: '#fff' }} alt="qr" />
                        : <pre style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-all', background: '#f7f9fc', borderRadius: 8, padding: 12, fontSize: 12, maxHeight: 140, overflow: 'auto' }}>{String(curOrder.qr_content)}</pre>}
                  </div>
                ) : (
                  qrImg
                    ? <img src={qrImg} style={{ maxWidth: 200, borderRadius: 8, border: '1px solid #eee', margin: '8px 0', background: '#fff' }} alt="qr" />
                    : <pre style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-all', background: '#f7f9fc', borderRadius: 8, padding: 12, fontSize: 12, maxHeight: 140, overflow: 'auto' }}>{String(curOrder.qr_content)}</pre>
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
