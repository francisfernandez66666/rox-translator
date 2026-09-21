// ============================================================================
// components/admin/IndustriesP.tsx — 行业管理面板（2026-09-10 需求：超管可创建/维护行业）
// 行业以 kb_packages 平台行业包（pack_type=industry，宿主租户0）为承载：
//  - 列表：全部行业（code / 名称 / 语料条目数 / 启用状态 / 创建时间）
//  - 新建行业：填 code（小写字母/数字/下划线，全局唯一）+ 中文名
//  - 编辑行业名 / 启用停用 / 删除（被企业租户引用的行业拒绝删除，提示改用停用）
// 行业字典经 GET /api/admin/industries 提供；注册页/租户表单/数据采集下拉均动态拉取，
// 故此处新建/编辑即时影响全站行业下拉（停用后注册选择中消失）。
// ============================================================================
import { useEffect, useState } from 'react'
import { runGuarded } from '@/lib/runGuarded'
import { Button, DataTable, Dialog, Switch, StatusPill, Link } from '@/ui/langcross/src'
import { confirmDialog } from '@/components/uiDialogs'
import { industries, industryCreate, industryUpdate, industryStatus, industryDelete, type IndustryItem } from '@/api/industry'
import { useT } from '@/i18n'
import { toastSuccess, toastError, toastWarn } from '@/lib/toastBus'

/** Props 无入参（行业管理为自足组件） */
type Props = Record<string, never>

/** 表格时间格式：截取到分（2026-09-10T08:00:00+08:00 → 08:00） */
const shortTime = (s?: string) => (s && s.length >= 19 ? s.slice(11, 16) : s || '—')

/**
 * IndustriesP 行业管理面板（2026-09-10 需求：超管可创建和维护行业字段）。
 * 展示平台全部行业；超管可新建（code 唯一）、编辑名称、启停、删除（引用保护）。
 */
export default function IndustriesP(_props: Props) {
  const [, t] = useT()
  const [rows, setRows] = useState<IndustryItem[]>([])
  const [dlg, setDlg] = useState<null | { mode: 'create' } | { mode: 'edit'; row: IndustryItem }>(null)
  const [code, setCode] = useState('')   // 新建行业 code（小写字母/数字/下划线）
  const [name, setName] = useState('')   // 行业显示名（中/英文均可）

  /** 拉取行业字典（承载体为租户 0 的 pack_type=industry 平台包），失败时保留旧列表 */
  const load = async () => {
    const r = await runGuarded(() => industries())
    if (!r) return // ★ E10：网络/超时异常已提示，中断后续
    if (r.success && r.industries) setRows(r.industries)
  }
  useEffect(() => { void load() }, []) // eslint-disable-line react-hooks/exhaustive-deps

  // 新建行业
  const doCreate = async () => {
    const c = code.trim()
    const n = name.trim()
    if (!c || !n) { void toastWarn(t('ind.needCodeName')); return }
    const r = await runGuarded(() => industryCreate({ code: c, name: n }))
    if (!r) return // ★ E10：网络/超时异常已提示，中断后续
    if (r.success) {
      toastSuccess(t('ind.created'))
      setDlg(null); setCode(''); setName('')
      await load()
    } else {
      toastError(r.message || t('ind.createFail'))
    }
  }

  // 编辑行业名
  const doUpdate = async () => {
    if (!(dlg && dlg.mode === 'edit')) return
    const n = name.trim()
    if (!n) { void toastWarn(t('ind.needName')); return }
    const r = await runGuarded(() => industryUpdate(dlg.row.id, n))
    if (!r) return // ★ E10：网络/超时异常已提示，中断后续
    if (r.success) {
      toastSuccess(t('ind.saved'))
      setDlg(null)
      await load()
    } else {
      toastError(r.message || t('ind.saveFail'))
    }
  }

  // 启停行业
  const doToggle = async (row: IndustryItem, enabled: number) => {
    const r = await runGuarded(() => industryStatus(row.id, enabled))
    if (!r) return // ★ E10：网络/超时异常已提示，中断后续
    if (r.success) {
      toastSuccess(enabled === 1 ? t('ind.enabledMsg') : t('ind.disabledMsg'))
      await load()
    } else {
      toastError(r.message || t('ind.opFail'))
    }
  }

  // 删除行业
  const doDelete = async (row: IndustryItem) => {
    const r = await runGuarded(() => industryDelete(row.id))
    if (!r) return // ★ E10：网络/超时异常已提示，中断后续
    if (r.success) {
      toastSuccess(t('ind.deleted'))
      await load()
    } else {
      toastError(r.message || t('ind.deleteFail'))
    }
  }

  return (
    <>
      <div style={{ marginBottom: 10, fontSize: 14, color: 'var(--adm-hint)' }}>
        {t('ind.hint')}
      </div>
      <Button variant="primary" onClick={() => { setCode(''); setName(''); setDlg({ mode: 'create' }) }}>{t('ind.new')}</Button>
      <DataTable<any> rowKey={(row) => String(row.id)} rows={rows}
             columns={[
               { key: 'id', title: 'ID', width: 50 },
               { key: 'code', title: t('ind.colCode'), width: 130, render: (row) => <code>{row.code}</code> },
               { key: 'name', title: t('ind.colName') },
               { key: 'entry_count', title: t('ind.colEntries'), width: 90, render: (row) => row.entry_count || 0 },
               { key: 'enabled', title: t('ind.colStatus'), width: 90, render: (row) => (
                 <StatusPill tone={row.enabled === 1 ? 'success' : 'idle'}>{row.enabled === 1 ? t('ind.on') : t('ind.off')}</StatusPill>
               ) },
               { key: 'created_at', title: t('ind.colCreated'), width: 100, render: (row) => shortTime(row.created_at) },
               { key: 'op', title: t('ind.colOp'), width: 260, render: (row) => (
                 <div style={{ display: 'flex', gap: 10, alignItems: 'center' }}>
                   <Switch checked={row.enabled === 1}
                           onChange={(e) => void doToggle(row, e.target.checked ? 1 : 0)} />
                   <Link onClick={() => { setName(row.name); setDlg({ mode: 'edit', row }) }}>{t('ind.edit')}</Link>
                   <Link tone="danger" onClick={async () => { if (!(await confirmDialog({ body: t('ind.deleteConfirm') }))) return; doDelete(row) }}>{t('ind.del')}</Link>
                 </div>
               ) },
             ]}  />

      {/* 新建行业弹窗 */}
      <Dialog open={!!dlg && dlg.mode === 'create'} onCancel={() => setDlg(null)} title={t('ind.newTitle')} onConfirm={doCreate}>
        <div style={{ marginBottom: 12 }}>
          <div style={{ marginBottom: 4, fontSize: 14, color: 'var(--adm-hint)' }}>{t('ind.codeLabel')}</div>
          <input className="lc-input" value={code} onChange={(e) => setCode(String(e.target.value))} placeholder="auto / realestate / ..." />
        </div>
        <div>
          <div style={{ marginBottom: 4, fontSize: 14, color: 'var(--adm-hint)' }}>{t('ind.nameLabel')}</div>
          <input className="lc-input" value={name} onChange={(e) => setName(String(e.target.value))} placeholder={t('ind.namePlaceholder')} />
        </div>
      </Dialog>

      {/* 编辑行业名弹窗 */}
      <Dialog open={!!dlg && dlg.mode === 'edit'} onCancel={() => setDlg(null)} title={t('ind.editTitle')} onConfirm={doUpdate}>
        <div>
          <div style={{ marginBottom: 4, fontSize: 14, color: 'var(--adm-hint)' }}>{t('ind.nameLabel')}</div>
          <input className="lc-input" value={name} onChange={(e) => setName(String(e.target.value))} />
        </div>
      </Dialog>
    </>
  )
}
