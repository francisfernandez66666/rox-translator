// ============================================================================
// components/admin/WorkflowP.tsx — 流程引擎面板
// 职责：翻译流程步骤开关配置与模型评估记录
// 从 panels_d.tsx 拆分
// ============================================================================
import { useCallback, useEffect, useState } from 'react'
import { Button, Table, Switch } from 'tdesign-react'
import { flowConfig, flowSave, evalsList as apiEvalsList } from '@/api'
import { toastResp } from './parts'
import { fmtTime } from '@/lib/ui'
import { useT } from '@/i18n'
import { useAdmin } from '@/stores/admin'

type Any = Record<string, any>

/** 流程引擎面板：流程步骤开关配置与模型评估记录 */
export function WorkflowP() {
  const [, t] = useT()
  const { activeTenantId } = useAdmin()
  const [steps, setSteps] = useState<Any[]>([])
  const [evals, setEvals] = useState<Any[]>([])

  const loadAll = useCallback(async () => {
    try { const f = await flowConfig(); if (f.success) setSteps((f as unknown as { steps?: Any[] }).steps || []) } catch {}
    try { const e = await apiEvalsList(); if (e.success) setEvals((e as unknown as { records?: Any[] }).records || []) } catch {}
  }, [])
  useEffect(() => { void loadAll() }, [activeTenantId, loadAll])

  return (
    <>
      <h2 style={{ margin: '4px 0 8px' }}>{t('workflow.title')}</h2>
      <p style={{ fontSize: 13, color: '#667', margin: '0 0 12px' }}>{t('workflow.hint')}</p>
      {steps.map((s, i) => (
        <div key={s.key} style={{ display: 'flex', alignItems: 'center', gap: 10, padding: '6px 0' }}>
          <Switch value={!!s.enable} onChange={(v: any) => setSteps(steps.map((x, j) => (j === i ? { ...x, enable: !!v } : x)))} />
          <span style={{ fontSize: 14 }}>{s.name}</span>
          <code style={{ fontSize: 12, color: '#889' }}>{s.key}</code>
        </div>
      ))}
      <Button theme="primary" style={{ marginTop: 8 }} onClick={async () => toastResp(await flowSave(steps as never), t('workflow.savedFlow'))}>{t('workflow.saveFlow')}</Button>

      <h2 style={{ margin: '32px 0 8px' }}>{t('workflow.evalsTitle')}</h2>
      <Button variant="outline" style={{ marginBottom: 8 }} onClick={() => void loadAll()}>{t('workflow.refresh')}</Button>
      <Table rowKey="id" size="small" maxHeight={400} data={evals}
        columns={[
          { colKey: 'id', title: t('workflow.colId'), width: 70 },
          { colKey: 'task_type', title: t('workflow.colTask'), width: 100 },
          { colKey: 'model', title: t('workflow.colLang'), width: 120 },
          { colKey: 'total', title: t('workflow.colScore'), width: 80, cell: ({ row }: any) => Number(row.total).toFixed(1) },
          { colKey: 'status', title: t('workflow.colStatus'), width: 90 },
          { colKey: 'created_at', title: t('workflow.colTime'), width: 160, cell: ({ row }: any) => fmtTime(row.created_at as string) },
          { colKey: 'output_text', title: t('workflow.colOutput'), ellipsis: true },
        ] as never} />
    </>
  )
}
