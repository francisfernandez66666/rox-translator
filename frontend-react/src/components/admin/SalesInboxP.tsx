// ============ SalesInboxP.tsx · 职责说明 ============
// 三期 W6 销售事件收件箱页（PanelKey 面板，不加 URL 路由、不动 App.tsx / spa.go）。
//
// 数据源两腿（都只读）：
//   ① 列表腿 salesInboxList（GET /api/admin/sales/inbox）——按状态过滤分页；
//   ② 单条腿 salesInboxEvent（GET /api/admin/sales/inbox/event）——按事件 ID 排障。
// 收件箱状态不许外部改写（幂等落账只发生在主服务 mq 消费者），本页零写侧。
// 所有文案一律 t('键名')，无硬编码中文；组件只用 @/ui/langcross/src 与 ./parts。
// ================================================
import { useCallback, useEffect, useState } from 'react'
import { useT } from '@/i18n'
import { Button, DataTable, EmptyState, InlineBanner, Input, PageHeader, StatusPill, type TableColumn } from '@/ui/langcross/src'
import { Panel } from './parts'
import { salesInboxList, type SalesInboxEventRow } from '@/api/salesLeads'
import { salesInboxEvent } from '@/api/salesInbox'

/** 收件箱事件列表（租户管理员及以上） */
export default function SalesInboxP() {
  const [, t] = useT()
  const [rows, setRows] = useState<SalesInboxEventRow[]>([])
  const [filter, setFilter] = useState('') // '' = 全部；'pending' | 'done'
  const [failed, setFailed] = useState(false)
  const [queryID, setQueryID] = useState('')
  const [queryResult, setQueryResult] = useState<{ found: boolean; status: string } | null>(null)

  const refresh = useCallback(() => {
    return salesInboxList(filter)
      .then((r) => {
        if (!r.success) { setFailed(true); return }
        setRows(Array.isArray(r.rows) ? r.rows : [])
        setFailed(false)
      })
      .catch(() => setFailed(true))
  }, [filter])

  useEffect(() => { void refresh() }, [refresh])

  const runQuery = useCallback(() => {
    const id = queryID.trim()
    if (!id) { setQueryResult(null); return }
    salesInboxEvent(id)
      .then((r) => setQueryResult({ found: !!r.found, status: r.status ?? '' }))
      .catch(() => setQueryResult({ found: false, status: '' }))
  }, [queryID])

  const cols: TableColumn<SalesInboxEventRow>[] = [
    { key: 'event_id', title: t('sales.inbox.colEventID'), render: (r) => r.event_id },
    { key: 'topic', title: t('sales.inbox.colTopic'), render: (r) => r.topic },
    {
      key: 'status', title: t('sales.inbox.colStatus'),
      render: (r) => r.status === 'done'
        ? <StatusPill tone="success">{t('sales.inbox.statusDone')}</StatusPill>
        : <StatusPill tone="warn">{t('sales.inbox.statusPending')}</StatusPill>,
    },
    { key: 'created_at', title: t('sales.inbox.colCreatedAt'), render: (r) => r.created_at },
    { key: 'processed_at', title: t('sales.inbox.colProcessedAt'), render: (r) => r.processed_at },
  ]

  return (
    <div data-testid="sales-inbox-page">
      <PageHeader title={t('sales.inbox.title')} />
      <InlineBanner tone="warn">{t('sales.inbox.subtitle')}</InlineBanner>
      {failed && <InlineBanner tone="error">{t('sales.inbox.loadFailed')}</InlineBanner>}

      <Panel title={t('sales.inbox.title')}>
        <div>
          <Button variant={filter === '' ? 'primary' : 'secondary'} onClick={() => setFilter('')}>
            {t('sales.inbox.filterAll')}
          </Button>
          <Button variant={filter === 'pending' ? 'primary' : 'secondary'} onClick={() => setFilter('pending')}>
            {t('sales.inbox.statusPending')}
          </Button>
          <Button variant={filter === 'done' ? 'primary' : 'secondary'} onClick={() => setFilter('done')}>
            {t('sales.inbox.statusDone')}
          </Button>
          <Button variant="secondary" onClick={() => void refresh()}>{t('sales.inbox.reload')}</Button>
        </div>
        {rows.length === 0 && !failed
          ? <EmptyState title={t('sales.inbox.empty')} />
          : <DataTable columns={cols} rows={rows} rowKey={(r) => String(r.id)} />}
      </Panel>

      <Panel title={t('sales.inbox.eventIdLabel')}>
        <div>
          <Input
            value={queryID}
            onChange={(e) => setQueryID(e.target.value)}
            placeholder={t('sales.inbox.eventIdLabel')}
          />
          <Button onClick={runQuery}>{t('sales.inbox.query')}</Button>
        </div>
        {queryResult && (
          queryResult.found
            ? <InlineBanner tone="success">{`${t('sales.inbox.colStatus')}: ${queryResult.status || t('sales.inbox.statusPending')}`}</InlineBanner>
            : <InlineBanner tone="warn">{t('sales.inbox.empty')}</InlineBanner>
        )}
      </Panel>
    </div>
  )
}
