// ============================================================================
// components/admin/WebhooksP.tsx — Webhook 回调通知面板
// 职责：Webhook 列表、新增/编辑/启停/测试/删除 + 投递历史/重试
// 从 panels_c.tsx 拆分
// ============================================================================
import { useCallback, useEffect, useState } from 'react'
import { Badge, Button, DataTable, Drawer, Link, StatusPill } from '@/ui/langcross/src'
import { confirmDialog } from '@/components/uiDialogs'
import {
  webhooks as apiWebhooks, webhookSave, webhookDelete, webhookTest, webhookDeliveries, webhookRetry,
} from '@/api'
import { Panel, toastResp } from './parts'
import { useT } from '@/i18n'
import { toastSuccess, toastError, toastWarn } from '@/lib/toastBus'

/** Any Webhook 出参宽松别名 */
type Any = Record<string, any>

/** Webhook 回调通知面板 */
export function WebhooksP() {
  // ===== 面板状态：Webhook 订阅列表、投递记录抽屉（按订阅懒加载） =====
  const [, t] = useT()
  const [rows, setRows] = useState<Any[]>([])
  const [dlg, setDlg] = useState<null | Any>(null)
  const [deliveryOpen, setDeliveryOpen] = useState(false)
  const [deliveryWebhookId, setDeliveryWebhookId] = useState(0)
  const [deliveries, setDeliveries] = useState<Any[]>([])
  const [deliveryStats, setDeliveryStats] = useState<Any>({})

  // load 拉取 Webhook 订阅列表
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
      toastSuccess(t('webhooks.retrySent'))
      const dr: Any = await webhookDeliveries(deliveryWebhookId)
      if (dr.success) {
        setDeliveries(dr.deliveries || [])
        setDeliveryStats(dr.stats || {})
      }
    } else {
      toastError((r.message as string) || t('webhooks.retryFailed'))
    }
  }

  function deliveryStatusTone(status: string): 'success' | 'danger' | 'warn' | 'idle' {
    if (status === 'success') return 'success'
    if (status === 'failed') return 'warn'
    if (status === 'dead') return 'danger'
    return 'idle'
  }

  return (
    <Panel title={t('webhooks.title')} extra={<Button variant="primary" onClick={() => setDlg({ url: '', secret: '', events: 'translation.completed', max_retries: 3, retry_interval: 60 })}>＋ {t('webhooks.saveConfig')}</Button>}>
      <p style={{ fontSize: 13, color: 'var(--adm-hint)', margin: '0 0 10px' }}>{t('webhooks.hint')}</p>
      <div style={{ display: 'flex', gap: 8, alignItems: 'center', marginBottom: 10, flexWrap: 'wrap' }}>
        <input className="lc-input" value={String(dlg?.url || '')} onChange={(e) => setDlg((d) => (d ? { ...d, url: e.target.value } : d))} placeholder={t('webhooks.urlPlaceholder')} style={{ flex: 1, minWidth: 240 }} />
        <input className="lc-input" value={String(dlg?.secret || '')} onChange={(e) => setDlg((d) => (d ? { ...d, secret: e.target.value } : d))} placeholder={t('webhooks.secretPlaceholder')} style={{ width: 200 }} />
        <input className="lc-input" value={String(dlg?.events || '')} onChange={(e) => setDlg((d) => (d ? { ...d, events: e.target.value } : d))} placeholder={t('webhooks.eventsPlaceholder')} style={{ flex: 1, minWidth: 200 }} />
        <input className="lc-input" value={String(dlg?.max_retries || '3')} onChange={(e) => setDlg((d) => (d ? { ...d, max_retries: Number(e.target.value) || 3 } : d))} placeholder={t('webhooks.maxRetries')} style={{ width: 100 }} />
        <input className="lc-input" value={String(dlg?.retry_interval || '60')} onChange={(e) => setDlg((d) => (d ? { ...d, retry_interval: Number(e.target.value) || 60 } : d))} placeholder={t('webhooks.retryInterval')} style={{ width: 100 }} />
        {!!dlg && <Button variant="primary" onClick={async () => { if (!dlg?.url) { void toastWarn(t('webhooks.urlRequired')); return } const r: Any = await webhookSave({ id: dlg.id ? Number(dlg.id) : undefined, url: String(dlg.url || ''), secret: dlg.secret ? String(dlg.secret) : undefined, events: String(dlg.events || 'translation.completed'), max_retries: Number(dlg.max_retries) || 3, retry_interval: Number(dlg.retry_interval) || 60 }); if (toastResp(r)) { setDlg(null); void load() } }}>{t('webhooks.saveConfig')}</Button>}
        </div>

      <DataTable<any> rowKey={(row) => String(row.id)} rows={rows}
             columns={[
               { key: 'id', title: t('webhooks.colId'), width: 70 },
               { key: 'url', title: t('webhooks.colUrl'), render: (row) => <span style={{ wordBreak: 'break-all' }}>{row.url}</span> },
               { key: 'events', title: t('webhooks.colEvents'), width: 200 },
               { key: 'failure_count', title: t('webhooks.failureCount'), width: 80, render: (row) => <span style={{ color: (row.failure_count || 0) > 0 ? '#e34d59' : undefined }}>{row.failure_count || 0}</span> },
               { key: 'enabled', title: t('webhooks.colStatus'), width: 90, render: (row) => <StatusPill tone={row.enabled ? 'success' : 'idle'}>{row.enabled ? t('webhooks.enable') : t('webhooks.disable')}</StatusPill> },
               { key: 'op', title: t('webhooks.colActions'), width: 320, render: (row) => (
                 <div style={{ display: 'flex', gap: 10, alignItems: 'center', flexWrap: 'wrap' }}>
                    <Link onClick={() => openDeliveries(Number(row.id))}>{t('webhooks.deliveries')}</Link>
                    <Link onClick={async () => { const r: Any = await webhookTest(Number(row.id)); if (r.success) toastSuccess(t('webhooks.testSent')); else toastError((r.message as string) || t('webhooks.testFailed')) }}>{t('webhooks.test')}</Link>
                   <Link onClick={() => toggleWebhook(row)}>{row.enabled ? t('webhooks.disable') : t('webhooks.enable')}</Link>
                   <Link onClick={() => setDlg({ ...row })}>{t('webhooks.saveConfig')}</Link>
                   <Link tone="danger" onClick={async () => { if (!(await confirmDialog({ body: t('webhooks.confirmDelete') }))) return; await webhookDelete(Number(row.id)); void load() }}>{t('webhooks.delete')}</Link>
        </div>
               ) },
             ]}  />

      <Drawer title={t('webhooks.deliveries')} open={deliveryOpen} onClose={() => setDeliveryOpen(false)}>
        <div style={{ display: 'flex', gap: 12, marginBottom: 16 }}>
          <Badge>{t('webhooks.statsTotal')}: {deliveryStats.total || 0}</Badge>
          <Badge>{t('webhooks.statsSuccess')}: {deliveryStats.success || 0}</Badge>
          <Badge>{t('webhooks.statsFailed')}: {deliveryStats.failed || 0}</Badge>
          <Badge>{t('webhooks.statsDead')}: {deliveryStats.dead || 0}</Badge>
        </div>
        {deliveries.length === 0 ? (
          <p style={{ color: 'var(--adm-faint)', textAlign: 'center', padding: 40 }}>{t('webhooks.noDeliveries')}</p>
        ) : (
          <DataTable<any> rowKey={(row) => String(row.id)} rows={deliveries}
                 columns={[
                   { key: 'id', title: t('webhooks.colDeliveryId'), width: 60 },
                   { key: 'event', title: t('webhooks.colDeliveryEvent'), width: 150 },
                   { key: 'status', title: t('webhooks.colDeliveryStatus'), width: 90, render: (row) => <StatusPill tone={deliveryStatusTone(row.status)}>{t(`webhooks.status${row.status.charAt(0).toUpperCase() + row.status.slice(1)}`) || row.status}</StatusPill> },
                   { key: 'status_code', title: t('webhooks.colDeliveryCode'), width: 70 },
                   { key: 'attempts', title: t('webhooks.colDeliveryAttempts'), width: 70, render: (row) => `${row.attempts}/${row.max_retries}` },
                   { key: 'error', title: t('webhooks.colDeliveryError'), dim: true, render: (row) => String(row.error ?? '—') },
                   { key: 'created_at', title: t('webhooks.colDeliveryTime'), width: 160, render: (row) => row.created_at?.slice(0, 19)?.replace('T', ' ') || '—' },
                   { key: 'op', title: t('webhooks.colDeliveryActions'), width: 80, render: (row) => (
                     row.status !== 'success' ? <Link onClick={() => handleRetry(Number(row.id))}>{t('webhooks.retry')}</Link> : null
                   ) },
                 ]}  />
        )}
      </Drawer>
    </Panel>
  )
}
