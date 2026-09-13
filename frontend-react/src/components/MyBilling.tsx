// ============================================================================
// MyBilling.tsx — 自服务「账单中心」增强（★ F8）
// 组合：BalancePanel（余额+不足横幅+充值引导，复用） + 用量趋势图（SVG 柱状，
// 近 30 天） + 订单/台账/奖励/发票四个可分页可筛选明细表。
// 后端端点：/api/billing/my/{overview,orders,ledger,rewards,invoices}。
// ============================================================================
import { useEffect, useState } from 'react'
import { Card, Button, Tabs, Select } from 'tdesign-react'
import { t } from '@/i18n'
import {
  myOverview, myOrders, myLedger, myRewards, myInvoices,
  type OverviewResp, type MyOrder, type MyLedgerRow, type MyReward, type MyInvoice,
} from '@/api/mybilling'
import { BalancePanel } from './selfservice'

// 千分位数字格式化
function fmtNum(n: number): string {
  return (n ?? 0).toLocaleString('en-US')
}

// ---------- 用量趋势图（近 30 天，UTC 日；SVG 柱状无第三方依赖） ----------
function TrendCard() {
  const [ov, setOv] = useState<OverviewResp | null>(null)
  useEffect(() => { void myOverview().then((r) => { if (r.success) setOv(r) }).catch(() => { /* 静默 */ }) }, [])
  const days = 30
  const byDate = new Map((ov?.daily ?? []).map((d) => [d.date, d]))
  const bars: { date: string; cost: number; count: number }[] = []
  const today = new Date()
  for (let i = days - 1; i >= 0; i--) {
    const dt = new Date(today.getTime() - i * 86400000).toISOString().slice(0, 10)
    const hit = byDate.get(dt)
    bars.push({ date: dt, cost: hit?.cost ?? 0, count: hit?.count ?? 0 })
  }
  const max = Math.max(1, ...bars.map((b) => b.cost))
  const W = 600; const H = 120; const bw = W / days
  return (
    <Card style={{ marginBottom: 12 }}>
      <h3 style={{ margin: '4px 0 10px' }}>{t('ss2.trendTitle')}</h3>
      {ov ? (
        <svg viewBox={`0 0 ${W} ${H + 18}`} style={{ width: '100%', height: 'auto', display: 'block' }} role="img" aria-label={t('ss2.trendTitle')}>
          {bars.map((b, i) => {
            const h = Math.round((b.cost / max) * (H - 10))
            return (
              <rect key={b.date} x={i * bw + 1} y={H - h} width={bw - 2} height={Math.max(h, b.cost > 0 ? 2 : 0)}
                    rx={2} fill="var(--td-brand-color, #2f47f5)" opacity={b.cost > 0 ? 0.85 : 0.15}>
                <title>{`${b.date} · ${fmtNum(b.cost)} token · ${b.count}${t('ss2.unitCnt')}`}</title>
              </rect>
            )
          })}
          <text x={0} y={H + 14} fontSize={10} fill="#999">{bars[0].date}</text>
          <text x={W} y={H + 14} fontSize={10} fill="#999" textAnchor="end">{bars[days - 1].date}</text>
        </svg>
      ) : <div style={{ fontSize: 12, color: '#999' }}>{t('ss2.loading')}</div>}
      <div style={{ fontSize: 12, color: '#889', marginTop: 4 }}>{t('ss2.trendMax')}：{fmtNum(max)} token</div>
    </Card>
  )
}

// ---------- 通用分页条 ----------
function Pager(props: { page: number; total: number; size: number; onPage: (p: number) => void }) {
  const pages = Math.max(1, Math.ceil(props.total / props.size))
  return (
    <div style={{ display: 'flex', gap: 8, alignItems: 'center', marginTop: 8, fontSize: 12 }}>
      <Button size="small" variant="outline" disabled={props.page <= 1} onClick={() => props.onPage(props.page - 1)}>{t('ss2.prev')}</Button>
      <span>{t('ss2.pageOf').replace('{p}', String(props.page)).replace('{n}', String(pages))}</span>
      <Button size="small" variant="outline" disabled={props.page >= pages} onClick={() => props.onPage(props.page + 1)}>{t('ss2.next')}</Button>
      <span style={{ color: '#999' }}>{t('ss2.totalN').replace('{n}', String(props.total))}</span>
    </div>
  )
}

// ---------- 订单 ----------
function OrdersTab() {
  const [page, setPage] = useState(1)
  const [status, setStatus] = useState('')
  const [rows, setRows] = useState<MyOrder[]>([])
  const [total, setTotal] = useState(0)
  useEffect(() => {
    void myOrders(page, status).then((r) => { setRows(r.orders ?? []); setTotal(r.total ?? 0) }).catch(() => { /* 静默 */ })
  }, [page, status])
  return (
    <div>
      <Select size="small" style={{ width: 160, marginBottom: 8 }} value={status}
              onChange={(v: any) => { setStatus(String(v)); setPage(1) }}
              options={[
                { value: '', label: t('ss2.allStatus') },
                { value: 'paid', label: t('ss2.stPaid') },
                { value: 'pending', label: t('ss2.stPending') },
                { value: 'refunded', label: t('ss2.stRefunded') },
                { value: 'cancelled', label: t('ss2.stCancelled') },
              ]} />
      <table className="ss-table"><thead><tr>
        <th>{t('ss2.colOrderNo')}</th><th>{t('ss2.colTokens')}</th><th>{t('ss2.colMoney')}</th><th>{t('ss2.colStatus')}</th><th>{t('ss2.colChannel')}</th><th>{t('ss2.colCreatedAt')}</th>
      </tr></thead><tbody>
        {rows.map((o) => (
          <tr key={o.id}>
            <td>{o.order_no}</td><td>{fmtNum(o.amount_tokens)}</td><td>{o.amount_money.toFixed(2)}</td>
            <td>{t(`ss2.st${o.status.charAt(0).toUpperCase()}${o.status.slice(1)}`)}{o.manual_confirm === 1 && o.status === 'pending' ? ` · ${t('ss2.awaitConfirm')}` : ''}</td>
            <td>{o.channel || o.pay_method || '-'}</td><td>{o.created_at.slice(0, 10)}</td>
          </tr>
        ))}
        {!rows.length && <tr><td colSpan={6} style={{ color: '#999' }}>{t('ss2.empty')}</td></tr>}
      </tbody></table>
      <Pager page={page} total={total} size={10} onPage={setPage} />
    </div>
  )
}

// ---------- 用量台账 ----------
function LedgerTab() {
  const [page, setPage] = useState(1)
  const [biz, setBiz] = useState('')
  const [rows, setRows] = useState<MyLedgerRow[]>([])
  const [total, setTotal] = useState(0)
  useEffect(() => {
    void myLedger(page, biz).then((r) => { setRows(r.rows ?? []); setTotal(r.total ?? 0) }).catch(() => { /* 静默 */ })
  }, [page, biz])
  return (
    <div>
      <Select size="small" style={{ width: 160, marginBottom: 8 }} value={biz}
              onChange={(v: any) => { setBiz(String(v)); setPage(1) }}
              options={[
                { value: '', label: t('ss2.allBiz') },
                { value: 'text', label: t('ss2.bizText') },
                { value: 'file', label: t('ss2.bizFile') },
              ]} />
      <table className="ss-table"><thead><tr>
        <th>{t('ss2.colTime')}</th><th>{t('ss2.colBiz')}</th><th>{t('ss2.colMode')}</th><th>{t('ss2.colQty')}</th><th>{t('ss2.colCost')}</th><th>{t('ss2.colModel')}</th>
      </tr></thead><tbody>
        {rows.map((l) => (
          <tr key={l.id}>
            <td>{l.created_at.replace('T', ' ').slice(0, 16)}</td>
            <td>{l.biz_kind ? t(`ss2.biz${l.biz_kind.charAt(0).toUpperCase()}${l.biz_kind.slice(1)}`) : '-'}</td>
            <td>{l.biz_mode === 'fast' ? t('ss2.modeFast') : l.biz_mode === 'pro' ? t('ss2.modePro') : '-'}</td>
            <td>{fmtNum(l.quantity)}</td><td>{fmtNum(l.cost)}</td><td>{l.model || '-'}</td>
          </tr>
        ))}
        {!rows.length && <tr><td colSpan={6} style={{ color: '#999' }}>{t('ss2.empty')}</td></tr>}
      </tbody></table>
      <Pager page={page} total={total} size={10} onPage={setPage} />
    </div>
  )
}

// ---------- 邀请奖励明细 ----------
function RewardsTab() {
  const [page, setPage] = useState(1)
  const [rows, setRows] = useState<MyReward[]>([])
  const [total, setTotal] = useState(0)
  useEffect(() => {
    void myRewards(page).then((r) => { setRows(r.rewards ?? []); setTotal(r.total ?? 0) }).catch(() => { /* 静默 */ })
  }, [page])
  return (
    <div>
      <table className="ss-table"><thead><tr>
        <th>{t('ss2.colInvitee')}</th><th>{t('ss2.colRwType')}</th><th>{t('ss2.colTokens')}</th><th>{t('ss2.colDays')}</th><th>{t('ss2.colRwPaid')}</th><th>{t('ss2.colTime')}</th>
      </tr></thead><tbody>
        {rows.map((rw) => (
          <tr key={`${rw.invitee_uid}-${rw.type}`}>
            <td>{rw.invitee_name}{rw.invitee_email ? ` (${rw.invitee_email})` : ''}</td>
            <td>{rw.type === 'trial_stack' ? t('ss2.rwTrial') : rw.type === 'paid_perm' ? t('ss2.rwPaidPerm') : rw.type}</td>
            <td>{fmtNum(rw.tokens)}</td><td>{rw.days > 0 ? rw.days : '-'}</td>
            <td>{rw.paid ? t('ss2.yes') : t('ss2.no')}</td><td>{rw.created_at.slice(0, 10)}</td>
          </tr>
        ))}
        {!rows.length && <tr><td colSpan={6} style={{ color: '#999' }}>{t('ss2.empty')}</td></tr>}
      </tbody></table>
      <Pager page={page} total={total} size={10} onPage={setPage} />
    </div>
  )
}

// ---------- 发票（状态：待开/已开/已冲红，配合 C16） ----------
function InvoicesTab() {
  const [page, setPage] = useState(1)
  const [rows, setRows] = useState<MyInvoice[]>([])
  const [total, setTotal] = useState(0)
  useEffect(() => {
    void myInvoices(page).then((r) => { setRows(r.invoices ?? []); setTotal(r.total ?? 0) }).catch(() => { /* 静默 */ })
  }, [page])
  return (
    <div>
      <table className="ss-table"><thead><tr>
        <th>{t('ss2.colInvNo')}</th><th>{t('ss2.colInvTitle')}</th><th>{t('ss2.colInvTax')}</th><th>{t('ss2.colMoney')}</th><th>{t('ss2.colInvStatus')}</th><th>{t('ss2.colTime')}</th>
      </tr></thead><tbody>
        {rows.map((iv) => (
          <tr key={iv.id}>
            <td>{iv.invoice_no}</td><td>{iv.title}</td><td>{iv.tax_no}</td><td>{iv.amount_money.toFixed(2)}</td>
            <td>{iv.status === 'pending' ? t('ss2.invPending') : iv.status === 'issued' ? t('ss2.invIssued') : iv.status === 'cancelled' ? t('ss2.invVoided') : iv.status}</td>
            <td>{iv.created_at.slice(0, 10)}</td>
          </tr>
        ))}
        {!rows.length && <tr><td colSpan={6} style={{ color: '#999' }}>{t('ss2.empty')}</td></tr>}
      </tbody></table>
      <Pager page={page} total={total} size={10} onPage={setPage} />
    </div>
  )
}

// BillingCenter 计费中心页：概览 / 订单 / 消费流水 / 奖励 / 发票
export default function BillingCenter() {
  return (
    <div style={{ width: '100%', maxWidth: 900, margin: '0 auto', padding: 16 }}>
      <BalancePanel />
      <TrendCard />
      <Card>
        <Tabs defaultValue="orders">
          <Tabs.TabPanel value="orders" label={t('ss2.tabOrders')}><OrdersTab /></Tabs.TabPanel>
          <Tabs.TabPanel value="ledger" label={t('ss2.tabLedger')}><LedgerTab /></Tabs.TabPanel>
          <Tabs.TabPanel value="rewards" label={t('ss2.tabRewards')}><RewardsTab /></Tabs.TabPanel>
          <Tabs.TabPanel value="invoices" label={t('ss2.tabInvoices')}><InvoicesTab /></Tabs.TabPanel>
        </Tabs>
      </Card>
    </div>
  )
}
