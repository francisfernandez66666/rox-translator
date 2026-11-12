// ============ SalesLeadsP.tsx · 职责说明 ============
// 三期 W6 销售线索页（PanelKey 面板，不加 URL 路由、不动 App.tsx / spa.go）。
//
// 数据源（都只读，统一 ≥3）：
//   ① 线索分页 salesLeadsList（GET /api/admin/sales/leads?stage=&pending=1）
//   ② 漏斗扩段 salesFunnelCounts（GET /api/admin/sales/funnel）
//   ③ 待人工会话 salesHandoffList + salesHandoffResolve（唯一写动作＝人工完结，只降权）
// 待人工（pending_human）只允许由 W4 守卫判定产生——本页不能造它，只能消它（A24）。
// 所有文案一律 t('键名')，无硬编码中文；组件只用 @/ui/langcross/src 与 ./parts。
// ================================================
import { useCallback, useEffect, useState } from 'react'
import { useT } from '@/i18n'
import { Button, DataTable, EmptyState, InlineBanner, PageHeader, StatusPill, type TableColumn } from '@/ui/langcross/src'
import { Panel } from './parts'
import { salesLeadsList, salesHandoffList, salesHandoffResolve, salesFunnelCounts, type SalesLeadRow, type HandoffSessionRow } from '@/api/salesLeads'

/** 阶段码 → 界面文案（静态 switch，五段一一对应，不用动态拼键绕闸） */
function stageLabel(stage: string, t: (key: string) => string): string {
  switch (stage) {
    case 'new': return t('sales.leads.stageNew')
    case 'engaged': return t('sales.leads.stageEngaged')
    case 'quoted': return t('sales.leads.stageQuoted')
    case 'won': return t('sales.leads.stageWon')
    case 'lost': return t('sales.leads.stageLost')
    default: return stage
  }
}

/** 阶段码全集（与后端 leadStageRank 同源） */
const STAGES = ['new', 'engaged', 'quoted', 'won', 'lost'] as const

/** 销售线索面板（租户管理员及以上） */
export default function SalesLeadsP() {
  const [, t] = useT()
  const [rows, setRows] = useState<SalesLeadRow[]>([])
  const [stage, setStage] = useState('')
  const [pendingOnly, setPendingOnly] = useState(false)
  const [counts, setCounts] = useState<Record<string, number>>({})
  const [handoff, setHandoff] = useState<HandoffSessionRow[]>([])
  const [failed, setFailed] = useState(false)

  const refresh = useCallback(() => {
    return Promise.all([
      salesLeadsList(stage, pendingOnly),
      salesFunnelCounts(),
      salesHandoffList(true),
    ])
      .then(([leads, funnel, ho]) => {
        if (!leads.success) { setFailed(true); return }
        setRows(Array.isArray(leads.rows) ? leads.rows : [])
        setCounts(funnel.counts ?? {})
        setHandoff(Array.isArray(ho.rows) ? ho.rows : [])
        setFailed(false)
      })
      .catch(() => setFailed(true))
  }, [stage, pendingOnly])

  useEffect(() => { void refresh() }, [refresh])

  const resolve = useCallback((sid: string) => {
    return salesHandoffResolve(sid).then(() => refresh())
  }, [refresh])

  const cols: TableColumn<SalesLeadRow>[] = [
    { key: 'email', title: t('sales.leads.colCustomer'), render: (r) => r.email },
    { key: 'stage', title: t('sales.leads.colStage'), render: (r) => stageLabel(r.stage, t) },
    { key: 'score', title: t('sales.leads.colScore'), render: (r) => String(r.score) },
    { key: 'owner', title: t('sales.leads.colOwner'), render: (r) => (r.owner_user_id > 0 ? String(r.owner_user_id) : '—') },
    {
      key: 'pending', title: t('sales.leads.handoff'),
      render: (r) => r.pending_human === 1
        ? <StatusPill tone="warn">{t('sales.leads.handoff')}</StatusPill>
        : <StatusPill tone="idle">—</StatusPill>,
    },
    { key: 'updated', title: t('sales.leads.colUpdatedAt'), render: (r) => r.updated_at },
  ]

  const hoCols: TableColumn<HandoffSessionRow>[] = [
    { key: 'session_id', title: t('sales.inbox.colEventID'), render: (r) => r.session_id },
    { key: 'created', title: t('sales.inbox.colCreatedAt'), render: (r) => r.created_at },
    {
      key: 'resolve', title: t('sales.leads.handoff'),
      render: (r) => <Button onClick={() => void resolve(r.session_id)}>{t('sales.leads.handoff')}</Button>,
    },
  ]

  return (
    <div data-testid="sales-leads-page">
      <PageHeader title={t('sales.leads.title')} />
      <InlineBanner tone="warn">{t('sales.leads.subtitle')}</InlineBanner>
      {failed && <InlineBanner tone="error">{t('sales.leads.loadFailed')}</InlineBanner>}

      <Panel title={t('sales.leads.title')}>
        <div>
          <Button variant={stage === '' ? 'primary' : 'secondary'} onClick={() => setStage('')}>
            {t('sales.inbox.filterAll')}
          </Button>
          {STAGES.map((s) => (
            <Button key={s} variant={stage === s ? 'primary' : 'secondary'} onClick={() => setStage(s)}>
              {`${stageLabel(s, t)}${counts[s] != null ? ` (${counts[s]})` : ''}`}
            </Button>
          ))}
          <Button variant={pendingOnly ? 'primary' : 'secondary'} onClick={() => setPendingOnly(!pendingOnly)}>
            {`${t('sales.leads.pendingHumanOnly')}${counts['pending_human'] ? ` (${counts['pending_human']})` : ''}`}
          </Button>
          <Button variant="secondary" onClick={() => void refresh()}>{t('sales.leads.reload')}</Button>
        </div>
        {rows.length === 0 && !failed
          ? <EmptyState title={t('sales.leads.empty')} />
          : <DataTable columns={cols} rows={rows} rowKey={(r) => String(r.id)} />}
      </Panel>

      <Panel title={t('sales.leads.pendingHumanOnly')}>
        {handoff.length === 0
          ? <EmptyState title={t('sales.leads.empty')} />
          : <DataTable columns={hoCols} rows={handoff} rowKey={(r) => String(r.id)} />}
      </Panel>
    </div>
  )
}
