// ============================================================================
// components/admin/TaskCenterP.tsx — 任务中心面板（功能③：个人中心 → 任务中心）
// 职责：用户视角展示启用任务（手工任务一键领取 / 事件任务自动到账 + 周期进度）；
//   超管可增删改任务定义（含发放方式、计数周期、上限、有效天数、叠加口径），
//   并可手动「重置已订阅用户的任务积分消耗量」（有效期不变）。
// 依赖后端：/api/admin/tasks*（超管）与 /api/me/tasks*（登录用户，见 api/tasks.ts）
// 2026-09-18（UI 融合）：领取状态与「启用」列的 ✓/✅ 图形字符随 emoji 清理移除，
//   状态改由 Tag 配色（success/default/warning）+ 文案表意；奖励额仍按积分口径录入与展示。
// 2026-09-21（★ #33 任务系统）：接入事件自动发放口径——
//   自动发放任务不再提供领取按钮（由后端登录/翻译/邀请/知识库钩子入账），
//   改展示「临时积分 + 有效期」或「永久积分」以及本期进度；
//   全部积分口径展示与录入，界面零 token 裸值。
// ============================================================================

/**
 * TaskCenterP.tsx · 职责说明
 * 任务中心面板：
 * - 用户视图：列出启用任务，标注发放方式（自动/手动）与奖励形态（临时/永久积分 + 有效期），
 *   自动任务展示本期进度（今日/本周/累计次数），手动任务保留一键领取
 * - 超管视图：新增/编辑/启停/删除任务，配置类型、周期、每日/每周上限、有效天数与到期叠加口径；
 *   并提供特殊任务「重置积分消耗量」（仅超管，有效期不改写）
 */

import { useCallback, useEffect, useState } from 'react'
import { fmtPoints } from '@/utils/points' // ★ S1 积分展示
import { Badge, Button, DataTable, Dialog, StatusPill, Switch, type TableColumn } from '@/ui/langcross/src'
import { useT } from '@/i18n'
import { useAdmin } from '@/stores/admin'
import { Panel, Field, toastResp } from './parts'
import { confirmDialog } from '@/components/uiDialogs'
import {
  adminTasks, adminTaskSave, adminTaskDelete, adminTaskResetConsumption,
  myTasks, claimTask, type TaskRewardStat, type UserTask, type UserTaskView,
} from '@/api/tasks'
import { type Any } from '@/api'
import { toastSuccess, toastError, toastWarn } from '@/lib/toastBus'
import { runGuarded } from '@/lib/runGuarded'

/** 任务中心面板组件：超管管理 + 用户领取/进度一体 */
export default function TaskCenterP() {
  const [, t, tpl] = useT()
  const { isSuper } = useAdmin()
  // 用户视图任务列表（启用任务 + 领取状态 / 自动发放进度）
  const [myRows, setMyRows] = useState<UserTaskView[]>([])
  // 超管视图任务定义列表（含停用项）
  const [adminRows, setAdminRows] = useState<UserTask[]>([])
  // 超管新增/编辑弹窗表单
  const [dlg, setDlg] = useState<null | Partial<UserTask>>(null)
  const [, setSaving] = useState(false)

  /** 加载用户视角任务（个人中心 → 任务中心 tab 展示）
   *  ★ #42：原先两处「吞掉异常」的空 catch 把「取数失败」做成了「本期暂无任务」——
   *  面板一片空白且无任何理由，改走 runGuarded（提示后端原文，不新增 i18n 键），成功路径与旧实现一致。 */
  const loadMy = useCallback(async () => {
    const r = await runGuarded(() => myTasks())
    if (r?.success) setMyRows(r.tasks || [])
  }, [])
  /** 加载超管任务定义列表 */
  const loadAdmin = useCallback(async () => {
    const r = await runGuarded(() => adminTasks())
    if (r?.success) setAdminRows(r.tasks || [])
  }, [])
  // 初始化加载（超管同时加载管理列表）
  useEffect(() => { void loadMy(); if (isSuper) void loadAdmin() }, [isSuper, loadMy, loadAdmin])

  /** 一键领取任务奖励（仅手工任务；奖励入永久余额） */
  // doClaim 用户领取任务奖励额度
  async function doClaim(row: UserTaskView) {
    const r = await claimTask(row.id)
    if (!r.success) {
      toastError(tpl('tasks.claimFail', { msg: r.message || '' }))
      return
    }
    toastSuccess(tpl('tasks.claimedOk', { points: Number(r.points) || 0 }))
    await loadMy()
  }

  /** 保存任务定义（新增/更新） */
  // saveTask 新建/更新任务定义
  async function saveTask() {
    if (!dlg) return
    if (!String(dlg.title || '').trim()) { void toastWarn(t('tasks.titleRequired')); return }
    setSaving(true)
    try {
      const r = await adminTaskSave({
        id: dlg.id || 0,
        task_type: (dlg.task_type as 'daily' | 'once') || 'daily',
        title: String(dlg.title || '').trim(),
        description: String(dlg.description || '').trim(),
        reward_points: Number(dlg.reward_points) || 0,
        enabled: dlg.enabled === undefined ? 1 : Number(dlg.enabled),
        sort_order: Number(dlg.sort_order) || 0,
        // ★ #33：事件自动发放口径。手工任务不带 task_key（后端按 manual 语义处理）
        grant_mode: dlg.grant_mode === 'auto' ? 'auto' : 'manual',
        task_key: dlg.grant_mode === 'auto' ? String(dlg.task_key || '').trim() : '',
        period: dlg.period || 'daily',
        valid_days: Number(dlg.valid_days) || 0,
        stack_expiry: Number(dlg.stack_expiry) || 0,
        cap_per_day: Number(dlg.cap_per_day) || 0,
        cap_per_week: Number(dlg.cap_per_week) || 0,
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

  /** ★ #33 特殊任务：重置已订阅用户的任务临时积分消耗量（有效期不变） */
  // resetConsumption 超管手动重置任务积分消耗量
  async function resetConsumption() {
    if (!(await confirmDialog({ body: t('tasks.resetConfirm') }))) return
    const r = await adminTaskResetConsumption(true)
    if (!r.success) { toastError(r.message || t('tasks.resetConsumption')); return }
    toastSuccess(tpl('tasks.resetOk', { tenants: Number(r.tenants) || 0, rows: Number(r.reset_rows) || 0 }))
    await loadMy()
  }

  /** 奖励形态文案：临时积分带有效期、永久积分不带（积分口径，零 token） */
  function rewardText(row: Partial<UserTask>): string {
    const pts = `+${fmtPoints(Number(row.reward_points) || 0)}`
    if (Number(row.valid_days) > 0) return `${pts} · ${t('tasks.rewardTemporary')} · ${tpl('tasks.validDaysTip', { days: Number(row.valid_days) })}`
    return `${pts} · ${t('tasks.rewardPermanent')}`
  }

  /** 发放规则文案：周期 + 上限 + 叠加口径（超管列表与弹窗共用同一口径描述） */
  function ruleText(row: Partial<UserTask>): string {
    const parts: string[] = []
    const period = row.period || (row.task_type === 'once' ? 'once' : 'daily')
    parts.push(period === 'daily' ? t('tasks.periodDaily') : period === 'weekly' ? t('tasks.periodWeekly')
      : period === 'once' ? t('tasks.periodOnce') : t('tasks.periodEvent'))
    if (Number(row.cap_per_day) > 0) parts.push(tpl('tasks.capDayRule', { cap: Number(row.cap_per_day) }))
    if (Number(row.cap_per_week) > 0) parts.push(tpl('tasks.capWeekRule', { cap: Number(row.cap_per_week) }))
    if (Number(row.valid_days) > 0 && Number(row.stack_expiry) === 1) parts.push(t('tasks.stackLabel'))
    return parts.join(' · ')
  }

  /** 本期进度文案（自动任务；无流水时返回空串） */
  function progressText(row: UserTaskView): string {
    const s: TaskRewardStat | undefined = row.reward
    if (!s) return ''
    const period = row.period || 'daily'
    if (period === 'daily') return tpl('tasks.progressDay', { done: Number(s.today_count) || 0, cap: Number(row.cap_per_day) || '—' })
    if (period === 'weekly') return tpl('tasks.progressWeek', { done: Number(s.week_count) || 0, cap: Number(row.cap_per_week) || '—' })
    return tpl('tasks.progressTotal', { count: Number(s.total_count) || 0 })
  }

  // 用户视图表格列
  const myCols: TableColumn<any>[] = [
    { key: 'task_type', title: t('tasks.colType'), width: 100, render: (row) => (
      <StatusPill tone={row.task_type === 'daily' ? 'idle' : 'warn'}>{row.task_type === 'daily' ? t('tasks.daily') : t('tasks.once')}</StatusPill>
    ) },
    { key: 'title', title: t('tasks.colTitle') },
    { key: 'description', title: t('tasks.colDesc'), render: (row) => row.description || '—' },
    { key: 'reward_points', title: t('tasks.colReward'), width: 220, render: (row) => <Badge>{rewardText(row)}</Badge> },
    { key: 'grant_mode', title: t('tasks.colGrant'), width: 110, render: (row) => (
      <StatusPill tone={row.grant_mode === 'auto' ? 'success' : 'idle'}>{row.grant_mode === 'auto' ? t('tasks.autoGrant') : t('tasks.manualGrant')}</StatusPill>
    ) },
    { key: 'status', title: t('tasks.colStatus'), width: 180, render: (row) => (
      row.grant_mode === 'auto'
        ? <span style={{ fontSize: 13 }}>{progressText(row) || t('tasks.autoGrant')}</span>
        : row.claimed
          ? <Badge>{t('tasks.claimed')}</Badge>
          : <StatusPill tone="warn">{t('tasks.claim')}</StatusPill>
    ) },
    { key: 'op', title: t('tasks.colOp'), width: 96, render: (row) => (
      // 自动发放任务由后端事件钩子入账，前端不给领取入口（避免点了必失败的死按钮）
      row.grant_mode === 'auto'
        ? <span style={{ fontSize: 13, color: 'var(--adm-faint)' }}>{row.claimed ? t('tasks.grantedAuto') : '—'}</span>
        : <Button size="sm" variant={row.claimed ? 'secondary' : 'primary'} disabled={!!row.claimed} onClick={() => void doClaim(row)}>
            {row.claimed ? t('tasks.claimed') : t('tasks.claim')}
          </Button>
    ) },
  ]

  // 超管管理表格列
  const adminCols: TableColumn<any>[] = [
    { key: 'id', title: 'ID', width: 70 },
    { key: 'task_type', title: t('tasks.colType'), width: 100, render: (row) => row.task_type === 'daily' ? t('tasks.daily') : t('tasks.once') },
    { key: 'title', title: t('tasks.colTitle') },
    { key: 'grant_mode', title: t('tasks.colGrant'), width: 110, render: (row) => (
      <StatusPill tone={row.grant_mode === 'auto' ? 'success' : 'idle'}>{row.grant_mode === 'auto' ? t('tasks.autoGrant') : t('tasks.manualGrant')}</StatusPill>
    ) },
    { key: 'reward_points', title: t('tasks.colReward'), width: 220, render: (row) => <Badge>{rewardText(row)}</Badge> },
    { key: 'rule', title: t('tasks.colRule'), render: (row) => <span style={{ fontSize: 13 }}>{ruleText(row)}</span> },
    { key: 'sort_order', title: t('tasks.colSort'), width: 70 },
    { key: 'enabled', title: t('tasks.colEnabled'), width: 80, render: (row) => <StatusPill tone={row.enabled === 1 ? 'success' : 'idle'}>{row.enabled === 1 ? t('tasks.enabledLabel') : '—'}</StatusPill> },
    { key: 'op', title: t('tasks.colOp'), width: 160, render: (row) => (
      <div style={{ display: 'flex', gap: 4 }}>
        <Button size="sm" variant="secondary" onClick={() => setDlg({ period: 'daily', grant_mode: 'manual', stack_expiry: 0, ...row })}>{t('tasks.edit')}</Button>
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
        <Panel title={t('tasks.adminTitle')} extra={<div style={{ display: 'flex', gap: 8 }}>
          {/* ★ #33 特殊任务：手动重置已订阅用户的任务积分消耗量（有效期不变） */}
          <Button variant="secondary" onClick={() => void resetConsumption()}>{t('tasks.resetConsumption')}</Button>
          <Button variant="primary" onClick={() => setDlg({
            id: 0, task_type: 'daily', title: '', description: '', reward_points: 100, enabled: 1, sort_order: 0,
            grant_mode: 'manual', task_key: '', period: 'daily', valid_days: 0, stack_expiry: 0, cap_per_day: 0, cap_per_week: 0,
          })}>＋ {t('tasks.add')}</Button>
        </div>}>
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
              {/* 积分口径录入：界面填积分，接口出入参同为积分（内部汇率折算在后端完成） */}
              <input className="lc-input" type="number" value={String(dlg.reward_points ?? 0)} onChange={(e) => setDlg((d) => (d ? { ...d, reward_points: Number(e.target.value) || 0 } : d))} style={{ width: 200 }} />
            </Field>

            {/* ★ #33 发放方式与事件标识：auto 任务由后端事件钩子按 task_key 触发发放 */}
            <Field label={t('tasks.grantModeLabel')}>
              <div style={{ display: 'flex', gap: 8 }}>
                <Button size="sm" variant={dlg.grant_mode !== 'auto' ? 'primary' : 'secondary'} onClick={() => setDlg((d) => (d ? { ...d, grant_mode: 'manual' } : d))}>{t('tasks.manualGrant')}</Button>
                <Button size="sm" variant={dlg.grant_mode === 'auto' ? 'primary' : 'secondary'} onClick={() => setDlg((d) => (d ? { ...d, grant_mode: 'auto' } : d))}>{t('tasks.autoGrant')}</Button>
              </div>
            </Field>
            {dlg.grant_mode === 'auto' && (
              <Field label="task_key">
                <input className="lc-input" value={String(dlg.task_key || '')} onChange={(e) => setDlg((d) => (d ? { ...d, task_key: e.target.value } : d))} placeholder="如 login_daily / translate_week / 自定义事件标识" style={{ width: 320 }} />
              </Field>
            )}
            <Field label={t('tasks.periodLabel')}>
              <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
                {(['daily', 'weekly', 'once', 'event'] as const).map((p) => (
                  <Button key={p} size="sm" variant={dlg.period === p ? 'primary' : 'secondary'} onClick={() => setDlg((d) => (d ? { ...d, period: p } : d))}>
                    {p === 'daily' ? t('tasks.periodDaily') : p === 'weekly' ? t('tasks.periodWeekly') : p === 'once' ? t('tasks.periodOnce') : t('tasks.periodEvent')}
                  </Button>
                ))}
              </div>
            </Field>
            <Field label={t('tasks.validDaysLabel')}>
              <input className="lc-input" type="number" value={String(dlg.valid_days ?? 0)} onChange={(e) => setDlg((d) => (d ? { ...d, valid_days: Number(e.target.value) || 0 } : d))} style={{ width: 200 }} />
            </Field>
            <Field label={t('tasks.stackLabel')}>
              <Switch checked={Number(dlg.stack_expiry) === 1} onChange={(e) => setDlg((d) => (d ? { ...d, stack_expiry: e.target.checked ? 1 : 0 } : d))} />
            </Field>
            <Field label={t('tasks.capDayLabel')}>
              <input className="lc-input" type="number" value={String(dlg.cap_per_day ?? 0)} onChange={(e) => setDlg((d) => (d ? { ...d, cap_per_day: Number(e.target.value) || 0 } : d))} style={{ width: 200 }} />
            </Field>
            <Field label={t('tasks.capWeekLabel')}>
              <input className="lc-input" type="number" value={String(dlg.cap_per_week ?? 0)} onChange={(e) => setDlg((d) => (d ? { ...d, cap_per_week: Number(e.target.value) || 0 } : d))} style={{ width: 200 }} />
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
