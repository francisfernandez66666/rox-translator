// ============================================================================
// ReconcileP.tsx — 超管对账视图（★ F9）：orders ↔ payments 三表勾稽报告
// 职责：按最近 N 天跑一次对账，列出跨表不一致的订单/流水，供财务与超管定位差异。
// 数据源：GET /api/admin/reconcile?days=N（后端逐规则扫描，前端只做规则筛选展示）。
// 2026-09-18（UI 融合）：整面板由 TDesign 迁到 ui/langcross —— Select 改原生 select
//   （lc-select）、Tag 改 Badge/StatusPill、Table 改 DataTable；loading 用 disabled 表达。
// ============================================================================
import { useCallback, useEffect, useState } from 'react'
import { Badge, Button, DataTable, StatusPill } from '@/ui/langcross/src'
import { t } from '@/i18n'
import { adminReconcile, type ReconIssue } from '@/api'
import { Panel } from './parts'
import { fmtTime } from '@/lib/ui'

/** Any 对账出参宽松别名 */
type Any = Record<string, unknown>

// 对账规则清单（缺失支付/状态不符/金额不符/退款无流水/孤儿记录…）
const RULES = ['missing_payment', 'status_mismatch', 'fen_mismatch', 'refund_no_flow', 'orphan_payment']

// ReconcileP 后台对账页：按最近 N 天运行余额对账并展示差异
export function ReconcileP() {
  const [days, setDays] = useState('30')
  const [rule, setRule] = useState('')
  const [rows, setRows] = useState<ReconIssue[]>([])
  const [summary, setSummary] = useState({ orders: 0, pays: 0 })
  const [loading, setLoading] = useState(false)

  /** 跑一次对账：days 非法（空/非数字）时兜底按 30 天，避免后端收到 NaN */
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

  // 规则筛选走前端：后端一次返回全量 issues（不支持按 rule 过滤），
  // 切换筛选不再发请求，只对已有结果做视图过滤。
  const view = rows.filter((x) => !rule || x.rule === rule)
  return (
    <Panel title={t('rc.title')} extra={
      <div style={{ display: 'flex', gap: 8 }}>
        <select className="lc-select" value={days} onChange={(e) => setDays(e.target.value)} style={{ width: 120 }}>
          <option value="7">{t('rc.d7')}</option>
          <option value="30">{t('rc.d30')}</option>
          <option value="90">{t('rc.d90')}</option>
          <option value="365">{t('rc.d365')}</option>
        </select>
        <select className="lc-select" value={rule} onChange={(e) => setRule(e.target.value)} style={{ width: 170 }}>
          <option value="">{t('rc.allRules')}</option>
          {RULES.map((k) => <option key={k} value={k}>{t(`rc.rule.${k}`)}</option>)}
        </select>
        <Button size="sm" variant="secondary" disabled={loading} onClick={() => void load()}>{t('rc.refresh')}</Button>
      </div>
    }>
      <p style={{ fontSize: 12, color: 'var(--adm-faint)', margin: '0 0 8px' }}>{t('rc.hint')}</p>
      <div style={{ marginBottom: 8 }}>
        <Badge>{t('rc.summary').replace('{o}', String(summary.orders)).replace('{p}', String(summary.pays)).replace('{i}', String(rows.length))}</Badge>
      </div>
      <DataTable<ReconIssue> rowKey={(row) => String(row.order_no)} rows={view}
        columns={[
          { key: 'order_no', title: t('rc.colOrder'), width: 190, mono: true },
          { key: 'tenant_id', title: t('rc.colTenant'), width: 70, render: (row) => `#${row.tenant_id}` },
          // 差异严重度分档：漏流水/金额不符属资金实损 → danger；其余（状态不同步/退款无流水/孤儿）→ warn
          { key: 'rule', title: t('rc.colRule'), width: 150, render: (row) => (
            <StatusPill tone={row.rule === 'missing_payment' || row.rule === 'fen_mismatch' ? 'danger' : 'warn'}>{t(`rc.rule.${row.rule}`)}</StatusPill>
          ) },
          { key: 'detail', title: t('rc.colDetail'), dim: true, render: (row) => String(row.detail ?? '—') },
          { key: 'created_at', title: t('rc.colTime'), width: 160, render: (row) => fmtTime(row.created_at) },
          ]} />
      {!view.length && !loading && <div style={{ textAlign: 'center', color: 'var(--adm-faint)', padding: 12 }}>{t('rc.empty')}</div>}
    </Panel>
  )
}
