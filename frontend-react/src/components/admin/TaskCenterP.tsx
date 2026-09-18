// ============================================================================
// components/admin/TaskCenterP.tsx — 任务中心面板（功能③：个人中心 → 任务中心）
// 职责：用户视角展示启用任务并一键领取（永久 token 奖励）；超管可增删改任务定义。
// 依赖后端：/api/admin/tasks*（超管）与 /api/me/tasks*（登录用户，见 api/tasks.ts）
// 2026-09-18（UI 融合）：领取状态与「启用」列的 ✓/✅ 图形字符随 emoji 清理移除，
//   状态改由 Tag 配色（success/default/warning）+ 文案表意；奖励额仍按积分口径录入与展示。
// ============================================================================

/**
 * TaskCenterP.tsx · 职责说明
 * 任务中心面板：
 * - 用户视图：列出启用任务（每日/一次性），显示本人领取状态，未领取可一键领取
 * - 超管视图：新增/编辑/启停/删除任务，配置任务类型（daily/once）、标题、说明与永久 token 奖励
 */

import { useCallback, useEffect, useState } from 'react'
import { fmtPoints, pointsOf, pointsToTokens } from '@/utils/points' // ★ S1 积分展示/录入折算
import { Badge, Button, DataTable, Dialog, StatusPill, Switch, type TableColumn } from '@/ui/langcross/src'
import { useT } from '@/i18n'
import { useAdmin } from '@/stores/admin'
import { Panel, Field, toastResp } from './parts'
import { confirmDialog } from '@/components/uiDialogs'
import { adminTasks, adminTaskSave, adminTaskDelete, myTasks, claimTask, type UserTask, type UserTaskView } from '@/api/tasks'
import { toastSuccess, toastError, toastWarn } from '@/lib/toastBus'

/** Any 任务中心出参宽松别名 */
type Any = Record<string, any> // 兜底类型：任务对象字段多变，避免过度强类型化（沿用项目惯例）

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
  const [, setSaving] = useState(false)

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
    // doClaim 用户领取任务奖励额度
async function doClaim(row: UserTaskView) {
    const r = await claimTask(row.id)
    if (!r.success) {
      toastError(tpl('tasks.claimFail', { msg: r.message || '' }))
      return
    }
    toastSuccess(tpl('tasks.claimedOk', { points: pointsOf(Number(r.tokens) || 0) }))
    await loadMy()
  }

  /** 保存任务定义（新增/更新） */
    // saveTask 新建/更新增长任务
async function saveTask() {
    if (!dlg) return
    if (!String(dlg.title || '').trim()) { void toastWarn(t('tasks.titleLabel') + '不能为空'); return }
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
    // deleteTask 删除任务
async function deleteTask(row: UserTask) {
    if (!(await confirmDialog({ body: t('tasks.deleteConfirm') }))) return
    const r = await adminTaskDelete(row.id)
    if (!toastResp(r, t('tasks.deleted'))) return
    await loadAdmin(); await loadMy()
  }

  // 用户视图表格列
  const myCols: TableColumn<any>[] = [
    { key: 'task_type', title: t('tasks.colType'), width: 100, render: (row) => (
      <StatusPill tone={row.task_type === 'daily' ? 'idle' : 'warn'}>{row.task_type === 'daily' ? t('tasks.daily') : t('tasks.once')}</StatusPill>
    ) },
    { key: 'title', title: t('tasks.colTitle') },
    { key: 'description', title: t('tasks.colDesc'), render: (row) => row.description || '—' },
    { key: 'reward_tokens', title: t('tasks.colReward'), width: 110, render: (row) => <Badge>+{fmtPoints(Number(row.reward_tokens))}</Badge> },
    { key: 'status', title: '状态', width: 100, render: (row) => row.claimed
 ? <Badge> {t('tasks.claimed')}</Badge>
      : <StatusPill tone="warn">{t('tasks.claim')}</StatusPill> },
    { key: 'op', title: '操作', width: 96, render: (row) => (
      <Button size="sm" variant={row.claimed ? 'secondary' : 'primary'} disabled={!!row.claimed} onClick={() => void doClaim(row)}>
        {row.claimed ? t('tasks.claimed') : t('tasks.claim')}
      </Button>
    ) },
  ]

  // 超管管理表格列
  const adminCols: TableColumn<any>[] = [
    { key: 'id', title: 'ID', width: 70 },
    { key: 'task_type', title: t('tasks.colType'), width: 100, render: (row) => row.task_type === 'daily' ? t('tasks.daily') : t('tasks.once') },
    { key: 'title', title: t('tasks.colTitle') },
    { key: 'reward_tokens', title: t('tasks.colReward'), width: 110, render: (row) => <Badge>+{fmtPoints(Number(row.reward_tokens))}</Badge> },
    { key: 'sort_order', title: t('tasks.colSort'), width: 70 },
 { key:'enabled', title: t('tasks.colEnabled'), width: 80, render: (row) => <StatusPill tone={row.enabled === 1 ? 'success' : 'idle'}>{row.enabled === 1 ? '' : '—'}</StatusPill> },
    { key: 'op', title: t('tasks.colOp'), width: 160, render: (row) => (
      <div style={{ display: 'flex', gap: 4 }}>
        <Button size="sm" variant="secondary" onClick={() => setDlg({ ...row })}>{t('tasks.edit')}</Button>
        <Button size="sm" variant="danger" onClick={() => void deleteTask(row)}>{t('common.delete')}</Button>
      </div>
    ) },
  ]

  return (
    <div>
      <Panel title={t('tasks.title')}>
        <p style={{ fontSize: 13, color: 'var(--adm-hint)', margin: '0 0 12px' }}>{t('tasks.hint')}</p>
        <DataTable<any> rowKey={(row) => String(row.id)} rows={myRows as Any[]} columns={myCols}  />
        {!myRows.length && <div style={{ textAlign: 'center', color: 'var(--adm-faint)', padding: 16 }}>{t('tasks.empty')}</div>}
      </Panel>

      {/* 超管任务管理 */}
      {isSuper && (
        <Panel title={t('tasks.adminTitle')} extra={<Button variant="primary" onClick={() => setDlg({ id: 0, task_type: 'daily', title: '', description: '', reward_tokens: 10000, enabled: 1, sort_order: 0 })}>＋ {t('tasks.add')}</Button>}>
          <p style={{ fontSize: 13, color: 'var(--adm-hint)', margin: '0 0 12px' }}>{t('tasks.adminHint')}</p>
          <DataTable<any> rowKey={(row) => String(row.id)} rows={adminRows as Any[]} columns={adminCols}  />
        </Panel>
      )}

      {/* 新增/编辑任务弹窗 */}
      <Dialog open={!!dlg} onCancel={() => setDlg(null)} title={dlg?.id ? t('tasks.edit') : t('tasks.add')}
        confirmText={t('tasks.save')} cancelText={t('common.cancel')} onConfirm={() => void saveTask()}>
        {dlg && (
          <div style={{ display: 'grid', gap: 4 }}>
            <Field label={t('tasks.typeLabel')}>
              <div style={{ display: 'flex', gap: 8 }}>
                <Button size="sm" variant={dlg.task_type === 'daily' ? 'primary' : 'secondary'} onClick={() => setDlg((d) => (d ? { ...d, task_type: 'daily' } : d))}>{t('tasks.daily')}</Button>
                <Button size="sm" variant={dlg.task_type === 'once' ? 'primary' : 'secondary'} onClick={() => setDlg((d) => (d ? { ...d, task_type: 'once' } : d))}>{t('tasks.once')}</Button>
              </div>
            </Field>
            <Field label={t('tasks.titleLabel')}>
              <input className="lc-input" value={String(dlg.title || '')} onChange={(e) => setDlg((d) => (d ? { ...d, title: e.target.value } : d))} placeholder="如 每日登录/完成一次翻译" />
            </Field>
            <Field label={t('tasks.descLabel')}>
              <textarea className="lc-textarea" rows={2} value={String(dlg.description || '')} onChange={(e) => setDlg((d) => (d ? { ...d, description: e.target.value } : d))} placeholder="任务说明（可空）" style={{ width: '100%', resize: 'vertical' }} />
            </Field>
            <Field label={t('tasks.rewardLabel')}>
              {/* ★ 积分口径录入（2026-09-15）：界面填积分，保存折算回内部 reward_tokens */}
              <input className="lc-input" type="number" value={String(pointsOf(Number(dlg.reward_tokens) || 0))} onChange={(e) => setDlg((d) => (d ? { ...d, reward_tokens: pointsToTokens(Number(e.target.value) || 0) } : d))} style={{ width: 200 }} />
            </Field>
            <Field label={t('tasks.sortLabel')}>
              <input className="lc-input" type="number" value={String(dlg.sort_order ?? 0)} onChange={(e) => setDlg((d) => (d ? { ...d, sort_order: Number(e.target.value) || 0 } : d))} style={{ width: 200 }} />
            </Field>
            <Field label={t('tasks.enabledLabel')}>
              <Switch checked={dlg.enabled !== 0} onChange={(e) => setDlg((d) => (d ? { ...d, enabled: e.target.checked ? 1 : 0 } : d))} />
            </Field>
          </div>
        )}
      </Dialog>
    </div>
  )
}
