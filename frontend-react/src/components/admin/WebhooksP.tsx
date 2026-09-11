// ============================================================================
// components/admin/WebhooksP.tsx — Webhook 回调通知面板
// 职责：Webhook 列表、新增/编辑/启停/测试/删除 + 投递历史/重试
// 从 panels_c.tsx 拆分
// ============================================================================
import { useCallback, useEffect, useState } from 'react'
import {
  Button, Table, Input, Space, Tag, Popconfirm, Drawer, MessagePlugin,
} from 'tdesign-react'
import {
  webhooks as apiWebhooks, webhookSave, webhookDelete, webhookTest, webhookDeliveries, webhookRetry,
} from '@/api'
import { Panel, toastResp } from './parts'
import { useT } from '@/i18n'

type Any = Record<string, any>

/** Webhook 回调通知面板 */
export function WebhooksP() {
  const [, t] = useT()
  const [rows, setRows] = useState<Any[]>([])
  const [dlg, setDlg] = useState<null | Any>(null)
  const [deliveryOpen, setDeliveryOpen] = useState(false)
  const [deliveryWebhookId, setDeliveryWebhookId] = useState(0)
  const [deliveries, setDeliveries] = useState<Any[]>([])
  const [deliveryStats, setDeliveryStats] = useState<Any>({})

  const load = useCallback(async () => {
    const r: Any = await apiWebhooks()
    if (r.success) setRows((r.webhooks as Any[]) || [])
  }, [])
  useEffect(() => { void load() }, [load])

  async function toggleWebhook(w: Any) {
    const r: Any = await webhookSave({ id: Number(w.id), url: String(w.url || ''), secret: w.secret ? String(w.secret) : undefined, events: String(w.events || 'translation.completed'), enabled: w.enabled ? 0 : 1, max_retries: Number(w.max_retries) || 3, retry_interval: Number(w.retry_interval) || 60 })
    if (toastResp(r)) void load()
  }

  async function openDeliveries(webhookId: number) {
    setDeliveryWebhookId(webhookId)
    setDeliveryOpen(true)
    const r: Any = await webhookDeliveries(webhookId)
    if (r.success) {
      setDeliveries(r.deliveries || [])
      setDeliveryStats(r.stats || {})
    }
  }

  async function handleRetry(deliveryId: number) {
    const r: Any = await webhookRetry(deliveryId)
    if (r.success) {
      void MessagePlugin.success(t('webhooks.retrySent'))
      const dr: Any = await webhookDeliveries(deliveryWebhookId)
      if (dr.success) {
        setDeliveries(dr.deliveries || [])
        setDeliveryStats(dr.stats || {})
      }
    } else {
      void MessagePlugin.error((r.message as string) || t('webhooks.retryFailed'))
    }
  }

  function deliveryStatusTheme(status: string) {
    if (status === 'success') return 'success'
    if (status === 'failed') return 'warning'
    if (status === 'dead') return 'danger'
    return 'default'
  }

  return (
    <Panel title={t('webhooks.title')} extra={<Button theme="primary" onClick={() => setDlg({ url: '', secret: '', events: 'translation.completed', max_retries: 3, retry_interval: 60 })}>＋ {t('webhooks.saveConfig')}</Button>}>
      <p style={{ fontSize: 13, color: '#667', margin: '0 0 10px' }}>{t('webhooks.hint')}</p>
      <Space size={8} align="center" style={{ marginBottom: 10, flexWrap: 'wrap' }}>
        <Input value={String(dlg?.url || '')} onChange={(v) => setDlg((d) => (d ? { ...d, url: v } : d))} placeholder={t('webhooks.urlPlaceholder')} style={{ flex: 1, minWidth: 240 }} />
        <Input value={String(dlg?.secret || '')} onChange={(v) => setDlg((d) => (d ? { ...d, secret: v } : d))} placeholder={t('webhooks.secretPlaceholder')} style={{ width: 200 }} />
        <Input value={String(dlg?.events || '')} onChange={(v) => setDlg((d) => (d ? { ...d, events: v } : d))} placeholder={t('webhooks.eventsPlaceholder')} style={{ flex: 1, minWidth: 200 }} />
        <Input value={String(dlg?.max_retries || '3')} onChange={(v) => setDlg((d) => (d ? { ...d, max_retries: Number(v) || 3 } : d))} placeholder={t('webhooks.maxRetries')} style={{ width: 100 }} />
        <Input value={String(dlg?.retry_interval || '60')} onChange={(v) => setDlg((d) => (d ? { ...d, retry_interval: Number(v) || 60 } : d))} placeholder={t('webhooks.retryInterval')} style={{ width: 100 }} />
        {!!dlg && <Button theme="primary" onClick={async () => { if (!dlg?.url) { void MessagePlugin.warning(t('webhooks.urlRequired')); return } const r: Any = await webhookSave({ id: dlg.id ? Number(dlg.id) : undefined, url: String(dlg.url || ''), secret: dlg.secret ? String(dlg.secret) : undefined, events: String(dlg.events || 'translation.completed'), max_retries: Number(dlg.max_retries) || 3, retry_interval: Number(dlg.retry_interval) || 60 }); if (toastResp(r)) { setDlg(null); void load() } }}>{t('webhooks.saveConfig')}</Button>}
      </Space>

      <Table rowKey="id" size="small" data={rows}
             columns={[
               { colKey: 'id', title: t('webhooks.colId'), width: 70 },
               { colKey: 'url', title: t('webhooks.colUrl'), ellipsis: true, cell: ({ row }: any) => <span style={{ wordBreak: 'break-all' }}>{row.url}</span> },
               { colKey: 'events', title: t('webhooks.colEvents'), width: 200 },
               { colKey: 'failure_count', title: t('webhooks.failureCount'), width: 80, cell: ({ row }: any) => <span style={{ color: (row.failure_count || 0) > 0 ? '#e34d59' : undefined }}>{row.failure_count || 0}</span> },
               { colKey: 'enabled', title: t('webhooks.colStatus'), width: 90, cell: ({ row }: any) => <Tag theme={row.enabled ? 'success' : 'default'}>{row.enabled ? t('webhooks.enable') : t('webhooks.disable')}</Tag> },
               { colKey: 'op', title: t('webhooks.colActions'), width: 320, cell: ({ row }: any) => (
                 <Space size={4}>
                    <Button size="small" variant="text" onClick={() => openDeliveries(Number(row.id))}>{t('webhooks.deliveries')}</Button>
                    <Button size="small" variant="text" onClick={async () => { const r: Any = await webhookTest(Number(row.id)); if (r.success) void MessagePlugin.success(t('webhooks.testSent')); else void MessagePlugin.error((r.message as string) || t('webhooks.testFailed')) }}>{t('webhooks.test')}</Button>
                   <Button size="small" variant="text" onClick={() => toggleWebhook(row)}>{row.enabled ? t('webhooks.disable') : t('webhooks.enable')}</Button>
                   <Button size="small" variant="text" onClick={() => setDlg({ ...row })}>{t('webhooks.saveConfig')}</Button>
                   <Popconfirm content={t('webhooks.confirmDelete')} onConfirm={async () => { await webhookDelete(Number(row.id)); void load() }}>
                     <Button size="small" variant="text" theme="danger">{t('webhooks.delete')}</Button>
                   </Popconfirm>
                 </Space>
               ) },
             ]} />

      <Drawer header={t('webhooks.deliveries')} visible={deliveryOpen} onClose={() => setDeliveryOpen(false)} size="large">
        <div style={{ display: 'flex', gap: 12, marginBottom: 16 }}>
          <Tag theme="primary" variant="light">{t('webhooks.statsTotal')}: {deliveryStats.total || 0}</Tag>
          <Tag theme="success" variant="light">{t('webhooks.statsSuccess')}: {deliveryStats.success || 0}</Tag>
          <Tag theme="warning" variant="light">{t('webhooks.statsFailed')}: {deliveryStats.failed || 0}</Tag>
          <Tag theme="danger" variant="light">{t('webhooks.statsDead')}: {deliveryStats.dead || 0}</Tag>
        </div>
        {deliveries.length === 0 ? (
          <p style={{ color: '#999', textAlign: 'center', padding: 40 }}>{t('webhooks.noDeliveries')}</p>
        ) : (
          <Table rowKey="id" size="small" data={deliveries}
                 columns={[
                   { colKey: 'id', title: t('webhooks.colDeliveryId'), width: 60 },
                   { colKey: 'event', title: t('webhooks.colDeliveryEvent'), width: 150 },
                   { colKey: 'status', title: t('webhooks.colDeliveryStatus'), width: 90, cell: ({ row }: any) => <Tag theme={deliveryStatusTheme(row.status)}>{t(`webhooks.status${row.status.charAt(0).toUpperCase() + row.status.slice(1)}`) || row.status}</Tag> },
                   { colKey: 'status_code', title: t('webhooks.colDeliveryCode'), width: 70 },
                   { colKey: 'attempts', title: t('webhooks.colDeliveryAttempts'), width: 70, cell: ({ row }: any) => `${row.attempts}/${row.max_retries}` },
                   { colKey: 'error', title: t('webhooks.colDeliveryError'), ellipsis: true },
                   { colKey: 'created_at', title: t('webhooks.colDeliveryTime'), width: 160, cell: ({ row }: any) => row.created_at?.slice(0, 19)?.replace('T', ' ') || '—' },
                   { colKey: 'op', title: t('webhooks.colDeliveryActions'), width: 80, cell: ({ row }: any) => (
                     row.status !== 'success' ? <Button size="small" variant="text" onClick={() => handleRetry(Number(row.id))}>{t('webhooks.retry')}</Button> : null
                   ) },
                 ]} />
        )}
      </Drawer>
    </Panel>
  )
}
