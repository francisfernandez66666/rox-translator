// ============================================================================
// components/admin/WorkflowP.tsx — 流程引擎面板
// 职责：翻译流程步骤开关配置与模型评估记录
// 从 panels_d.tsx 拆分
// 2026-09-18（UI 融合）：Button/DataTable/Switch 全部改取 ui/langcross ——
//   TDesign Table 的 colKey/cell/ellipsis 对应 DataTable 的 key/render/dim；
//   Switch 由「value + onChange(bool)」改为原生语义「checked + onChange(event)」。
// ============================================================================
import { useCallback, useEffect, useState } from 'react'
import { Button, DataTable, Switch } from '@/ui/langcross/src'
import { flowConfig, flowSave, evalsList as apiEvalsList, type Any } from '@/api'
import { runGuarded } from '@/lib/runGuarded'
import { toastResp } from './parts'
import { fmtTime } from '@/lib/ui'
import { useT } from '@/i18n'
import { useAdmin } from '@/stores/admin'

/** 流程引擎面板：流程步骤开关配置与模型评估记录 */
export function WorkflowP() {
  const [, t] = useT()
  const { activeTenantId } = useAdmin()
  const [steps, setSteps] = useState<Any[]>([])
  const [evals, setEvals] = useState<Any[]>([])

  /** 拉流程配置 + 评估记录：两段各自兜底，一块失败不把另一块也清空
   *  ★ #42：原先两个空 catch 把「后端挂了」渲染成「没有步骤、没有评估记录」，
   *  超管会以为流程配置被清空了；改 runGuarded 让错误可见，其余语义不变。 */
  const loadAll = useCallback(async () => {
    const f = await runGuarded(() => flowConfig())
    if (f?.success) setSteps((f as unknown as { steps?: Any[] }).steps || [])
    const e = await runGuarded(() => apiEvalsList())
    if (e?.success) setEvals((e as unknown as { records?: Any[] }).records || [])
  }, [])
  useEffect(() => { void loadAll() }, [activeTenantId, loadAll])

  return (
    <>
      <h2 style={{ margin: '4px 0 8px' }}>{t('workflow.title')}</h2>
      <p style={{ fontSize: 15, color: 'var(--adm-hint)', margin: '0 0 12px' }}>{t('workflow.hint')}</p>
      {steps.map((s, i) => (
        // 步骤开关：langcross Switch 是原生 <input type=checkbox role=switch>，
        // onChange 收的是 DOM 事件，需 e.target.checked 取布尔（TDesign 版直接给 bool）。
        // 这里只改本地 steps 草稿，点「保存流程」才整体提交，避免每次拨动都打一次接口。
        <div key={s.key} style={{ display: 'flex', alignItems: 'center', gap: 10, padding: '6px 0' }}>
          <Switch checked={!!s.enable} onChange={(e) => setSteps(steps.map((x, j) => (j === i ? { ...x, enable: e.target.checked } : x)))} />
          <span style={{ fontSize: 16 }}>{s.name}</span>
          <code style={{ fontSize: 14, color: 'var(--adm-faint)' }}>{s.key}</code>
        </div>
      ))}
      {/* 一次性提交整份 steps（含未改动项），后端按数组覆盖式保存 */}
      <Button variant="primary" style={{ marginTop: 8 }} onClick={async () => toastResp(await flowSave(steps as never), t('workflow.savedFlow'))}>{t('workflow.saveFlow')}</Button>

      <h2 style={{ margin: '32px 0 8px' }}>{t('workflow.evalsTitle')}</h2>
      <Button variant="secondary" style={{ marginBottom: 8 }} onClick={() => void loadAll()}>{t('workflow.refresh')}</Button>
      <DataTable<Any> rowKey={(row) => String(row.id)} rows={evals}
        columns={[
          { key: 'id', title: t('workflow.colId'), width: 70 },
          { key: 'task_type', title: t('workflow.colTask'), width: 100 },
          { key: 'model', title: t('workflow.colLang'), width: 120 },
          { key: 'total', title: t('workflow.colScore'), width: 80, render: (row) => Number(row.total).toFixed(1) },
          { key: 'status', title: t('workflow.colStatus'), width: 90 },
          { key: 'created_at', title: t('workflow.colTime'), width: 160, render: (row) => fmtTime(row.created_at as string) },
          { key: 'output_text', title: t('workflow.colOutput'), dim: true, render: (row) => String(row.output_text ?? '—') },
        ]} />
    </>
  )
}
