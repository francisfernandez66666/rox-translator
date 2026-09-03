// ============================================================================
// components/admin/TaskCenterP.tsx — 任务中心面板（功能③：个人中心 → 任务中心）
// 职责：用户视角展示启用任务并一键领取（永久 token 奖励）；超管可增删改任务定义。
// 依赖后端：/api/admin/tasks*（超管）与 /api/me/tasks*（登录用户，见 api/tasks.ts）
// ============================================================================

/**
 * TaskCenterP.tsx · 职责说明
 * 任务中心面板：
 * - 用户视图：列出启用任务（每日/一次性），显示本人领取状态，未领取可一键领取
 * - 超管视图：新增/编辑/启停/删除任务，配置任务类型（daily/once）、标题、说明与永久 token 奖励
 */

import { useCallback, useEffect, useState } from 'react'
import { Button, Dialog, Input, MessagePlugin, Switch, Table, Tag, Textarea } from 'tdesign-react'
import { useT } from '@/i18n'
import { useAdmin } from '@/stores/admin'
import { Panel, Field, toastResp } from './parts'
import { confirmDialog } from '@/components/uiDialogs'
import { adminTasks, adminTaskSave, adminTaskDelete, myTasks, claimTask, type UserTask, type UserTaskView } from '@/api/tasks'

type Any = Record<string, any>

/** 任务中心面板组件：超管管理 + 用户领取一体 */
export default function TaskCenterP() {
  const [, t, tpl] = useT()
  const { isSuper } = useAdmin()
  // 用户视图任务列表（启用任务 + 领取状态）
  const [myRows, setMyRows] = useState<UserTaskView[]>([])
  // 超管视图任务定义列表（含停用项）
  const [adminRows, setAdminRows] = useState<UserTask[]>([])
  // 超管新增/编辑弹窗表单
  const [dlg, setDlg] = useState<null | Partial<UserTask>>(null)
  const [saving, setSaving] = useState(false)

  /** 加载用户视角任务（个人中心 → 任务中心 tab 展示） */
  const loadMy = useCallback(async () => {
    try {
      const r = await myTasks()
      if (r.success) setMyRows(r.tasks || [])
    } catch { /* ignore */ }
  }, [])
  /** 加载超管任务定义列表 */
  const loadAdmin = useCallback(async () => {
    try {
      const r = await adminTasks()
      if (r.success) setAdminRows(r.tasks || [])
    } catch { /* ignore */ }
  }, [])
  // 初始化加载（超管同时加载管理列表）
  useEffect(() => { void loadMy(); if (isSuper) void loadAdmin() }, [isSuper, loadMy, loadAdmin])

  /** 一键领取任务奖励 */
  async function doClaim(row: UserTaskView) {
    const r = await claimTask(row.id)
    if (!r.success) {
      void MessagePlugin.error(tpl('tasks.claimFail', { msg: r.message || '' }))
      return
    }
    void MessagePlugin.success(tpl('tasks.claimedOk', { tokens: r.tokens ?? 0 }))
    await loadMy()
  }

  /** 保存任务定义（新增/更新） */
  async function saveTask() {
    if (!dlg) return
    if (!String(dlg.title || '').trim()) { void MessagePlugin.warning(t('tasks.titleLabel') + '不能为空'); return }
    setSaving(true)
    try {
      const r = await adminTaskSave({
        id: dlg.id || 0,
        task_type: (dlg.task_type as 'daily' | 'once') || 'daily',
        title: String(dlg.title || '').trim(),
        description: String(dlg.description || '').trim(),
        reward_tokens: Number(dlg.reward_tokens) || 0,
        enabled: dlg.enabled === undefined ? 1 : Number(dlg.enabled),
        sort_order: Number(dlg.sort_order) || 0,
      })
      if (!toastResp(r, t('tasks.saved'))) return
      setDlg(null)
      await loadAdmin(); await loadMy()
    } finally { setSaving(false) }
  }

  /** 删除任务定义 */
  async function deleteTask(row: UserTask) {
    if (!(await confirmDialog({ body: t('tasks.deleteConfirm') }))) return
    const r = await adminTaskDelete(row.id)
    if (!toastResp(r, t('tasks.deleted'))) return
    await loadAdmin(); await loadMy()
  }

  // 用户视图表格列
  const myCols = [
    { colKey: 'task_type', title: t('tasks.colType'), width: 100, cell: ({ row }: Any) => (
      <Tag theme={row.task_type === 'daily' ? 'primary' : 'warning'} variant="light">{row.task_type === 'daily' ? t('tasks.daily') : t('tasks.once')}</Tag>
    ) },
    { colKey: 'title', title: t('tasks.colTitle') },
    { colKey: 'description', title: t('tasks.colDesc'), cell: ({ row }: Any) => row.description || '—' },
    { colKey: 'reward_tokens', title: t('tasks.colReward'), width: 110, cell: ({ row }: Any) => <Tag theme="success" variant="light">+{Number(row.reward_tokens).toLocaleString()}</Tag> },
    { colKey: 'status', title: '状态', width: 100, cell: ({ row }: Any) => row.claimed
      ? <Tag theme="default" variant="light">✅ {t('tasks.claimed')}</Tag>
      : <Tag theme="warning" variant="light">{t('tasks.claim')}</Tag> },
    { colKey: 'op', title: '操作', width: 96, cell: ({ row }: Any) => (
      <Button size="small" theme={row.claimed ? 'default' : 'primary'} disabled={!!row.claimed} onClick={() => void doClaim(row)}>
        {row.claimed ? t('tasks.claimed') : t('tasks.claim')}
      </Button>
    ) },
  ] as never

  // 超管管理表格列
  const adminCols = [
    { colKey: 'id', title: 'ID', width: 70 },
    { colKey: 'task_type', title: t('tasks.colType'), width: 100, cell: ({ row }: Any) => row.task_type === 'daily' ? t('tasks.daily') : t('tasks.once') },
    { colKey: 'title', title: t('tasks.colTitle') },
    { colKey: 'reward_tokens', title: t('tasks.colReward'), width: 110 },
    { colKey: 'sort_order', title: t('tasks.colSort'), width: 70 },
    { colKey: 'enabled', title: t('tasks.colEnabled'), width: 80, cell: ({ row }: Any) => <Tag theme={row.enabled === 1 ? 'success' : 'default'} variant="light">{row.enabled === 1 ? '✓' : '—'}</Tag> },
    { colKey: 'op', title: t('tasks.colOp'), width: 160, cell: ({ row }: Any) => (
      <div style={{ display: 'flex', gap: 4 }}>
        <Button size="small" variant="outline" onClick={() => setDlg({ ...row })}>{t('tasks.edit')}</Button>
        <Button size="small" variant="text" theme="danger" onClick={() => void deleteTask(row)}>{t('common.delete')}</Button>
      </div>
    ) },
  ] as never

  return (
    <div>
      <Panel title={t('tasks.title')}>
        <p style={{ fontSize: 13, color: '#667', margin: '0 0 12px' }}>{t('tasks.hint')}</p>
        <Table rowKey="id" size="small" data={myRows as Any[]} columns={myCols} />
        {!myRows.length && <div style={{ textAlign: 'center', color: '#999', padding: 16 }}>{t('tasks.empty')}</div>}
      </Panel>

      {/* 超管任务管理 */}
      {isSuper && (
        <Panel title={t('tasks.adminTitle')} extra={<Button theme="primary" onClick={() => setDlg({ id: 0, task_type: 'daily', title: '', description: '', reward_tokens: 10000, enabled: 1, sort_order: 0 })}>＋ {t('tasks.add')}</Button>}>
          <p style={{ fontSize: 13, color: '#667', margin: '0 0 12px' }}>{t('tasks.adminHint')}</p>
          <Table rowKey="id" size="small" data={adminRows as Any[]} columns={adminCols} />
        </Panel>
      )}

      {/* 新增/编辑任务弹窗 */}
      <Dialog visible={!!dlg} onClose={() => setDlg(null)} header={dlg?.id ? t('tasks.edit') : t('tasks.add')} width={540}
        footer={
          <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 8 }}>
            <Button variant="outline" onClick={() => setDlg(null)}>{t('common.cancel')}</Button>
            <Button theme="primary" loading={saving} onClick={() => void saveTask()}>{t('tasks.save')}</Button>
          </div>
        }>
        {dlg && (
          <div style={{ display: 'grid', gap: 4 }}>
            <Field label={t('tasks.typeLabel')}>
              <div style={{ display: 'flex', gap: 8 }}>
                <Button size="small" variant={dlg.task_type === 'daily' ? 'base' : 'outline'} theme={dlg.task_type === 'daily' ? 'primary' : 'default'} onClick={() => setDlg((d) => (d ? { ...d, task_type: 'daily' } : d))}>{t('tasks.daily')}</Button>
                <Button size="small" variant={dlg.task_type === 'once' ? 'base' : 'outline'} theme={dlg.task_type === 'once' ? 'warning' : 'default'} onClick={() => setDlg((d) => (d ? { ...d, task_type: 'once' } : d))}>{t('tasks.once')}</Button>
              </div>
            </Field>
            <Field label={t('tasks.titleLabel')}>
              <Input value={String(dlg.title || '')} onChange={(v: string) => setDlg((d) => (d ? { ...d, title: v } : d))} placeholder="如 每日登录/完成一次翻译" />
            </Field>
            <Field label={t('tasks.descLabel')}>
              <Textarea value={String(dlg.description || '')} onChange={(v) => setDlg((d) => (d ? { ...d, description: v } : d))} autosize={{ minRows: 2 }} placeholder="任务说明（可空）" />
            </Field>
            <Field label={t('tasks.rewardLabel')}>
              <Input type="number" value={String(dlg.reward_tokens ?? 0)} onChange={(v: string) => setDlg((d) => (d ? { ...d, reward_tokens: Number(v) || 0 } : d))} style={{ width: 200 }} />
            </Field>
            <Field label={t('tasks.sortLabel')}>
              <Input type="number" value={String(dlg.sort_order ?? 0)} onChange={(v: string) => setDlg((d) => (d ? { ...d, sort_order: Number(v) || 0 } : d))} style={{ width: 200 }} />
            </Field>
            <Field label={t('tasks.enabledLabel')}>
              <Switch size="small" value={dlg.enabled !== 0} onChange={(v: boolean) => setDlg((d) => (d ? { ...d, enabled: v ? 1 : 0 } : d))} />
            </Field>
          </div>
        )}
      </Dialog>
    </div>
  )
}
