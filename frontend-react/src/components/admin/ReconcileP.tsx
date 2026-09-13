// ============================================================================
// ReconcileP.tsx — 超管对账视图（★ F9）：orders ↔ payments 三表勾稽报告
// ============================================================================
import { useCallback, useEffect, useState } from 'react'
import { Button, Select, Table, Tag } from 'tdesign-react'
import { t } from '@/i18n'
import { adminReconcile, type ReconIssue } from '@/api'
import { Panel } from './parts'
import { fmtTime } from '@/lib/ui'

type Any = Record<string, unknown>

const RULES = ['missing_payment', 'status_mismatch', 'fen_mismatch', 'refund_no_flow', 'orphan_payment']

// ReconcileP 后台对账页：按最近 N 天运行余额对账并展示差异
export function ReconcileP() {
  const [days, setDays] = useState('30')
  const [rule, setRule] = useState('')
  const [rows, setRows] = useState<ReconIssue[]>([])
  const [summary, setSummary] = useState({ orders: 0, pays: 0 })
  const [loading, setLoading] = useState(false)

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const r = await adminReconcile(Number(days) || 30)
      if (r.success) {
        setRows(((r as Any).issues as ReconIssue[]) || [])
        setSummary({ orders: Number((r as Any).orders_count) || 0, pays: Number((r as Any).payments_count) || 0 })
      }
    } finally {
      setLoading(false)
    }
  }, [days])

  useEffect(() => { void load() }, [load])

  const view = rows.filter((x) => !rule || x.rule === rule)
  return (
    <Panel title={t('rc.title')} extra={
      <div style={{ display: 'flex', gap: 8 }}>
        <Select size="small" value={days} onChange={(v: any) => setDays(String(v))} style={{ width: 120 }}
          options={[
            { value: '7', label: t('rc.d7') },
            { value: '30', label: t('rc.d30') },
            { value: '90', label: t('rc.d90') },
            { value: '365', label: t('rc.d365') },
          ]} />
        <Select size="small" value={rule} onChange={(v: any) => setRule(String(v))} style={{ width: 170 }}
          options={[{ value: '', label: t('rc.allRules') }, ...RULES.map((k) => ({ value: k, label: t(`rc.rule.${k}`) }))]} />
        <Button size="small" variant="outline" loading={loading} onClick={() => void load()}>{t('rc.refresh')}</Button>
      </div>
    }>
      <p style={{ fontSize: 12, color: '#889', margin: '0 0 8px' }}>{t('rc.hint')}</p>
      <div style={{ marginBottom: 8 }}>
        <Tag variant="light">{t('rc.summary').replace('{o}', String(summary.orders)).replace('{p}', String(summary.pays)).replace('{i}', String(rows.length))}</Tag>
      </div>
      <Table rowKey="order_no" size="small" data={view as unknown as Any[]}
        columns={[
          { colKey: 'order_no', title: t('rc.colOrder'), width: 190 },
          { colKey: 'tenant_id', title: t('rc.colTenant'), width: 70, cell: ({ row }: any) => `#${row.tenant_id}` },
          { colKey: 'rule', title: t('rc.colRule'), width: 150, cell: ({ row }: any) => <Tag theme={row.rule === 'missing_payment' || row.rule === 'fen_mismatch' ? 'danger' : 'warning'}>{t(`rc.rule.${row.rule}`)}</Tag> },
          { colKey: 'detail', title: t('rc.colDetail'), ellipsis: true },
          { colKey: 'created_at', title: t('rc.colTime'), width: 160, cell: ({ row }: any) => fmtTime(row.created_at) },
        ] as never} />
      {!view.length && !loading && <div style={{ textAlign: 'center', color: '#999', padding: 12 }}>{t('rc.empty')}</div>}
    </Panel>
  )
}
