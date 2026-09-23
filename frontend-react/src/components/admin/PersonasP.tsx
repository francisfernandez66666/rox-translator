// ============================================================================
// components/admin/PersonasP.tsx — 角色管理面板（2026-09-19 需求：用户职业角色）
// 角色以 kb_packages 平台角色包（pack_type=persona，宿主租户0）为承载：
//  - 列表：全部角色（code / 名称 / 语料条目数 / 启用状态 / 创建时间）
//  - 新建角色：填 code（小写字母/数字/下划线，全局唯一）+ 中文名
//  - 编辑角色名 / 启用停用 / 删除（被用户 job_role 引用的角色拒绝删除，提示改用停用）
// 角色只绑定用户、不绑定企业（退出企业角色仍生效）；注册页与个人中心下拉动态拉取。
// 出厂 8 角色 + 维基词典采集源由 store.PersonaMigrate 幂等种入。
// ============================================================================
import { useEffect, useState } from 'react'
import { runGuarded } from '@/lib/runGuarded'
import { Button, DataTable, Dialog, Switch, StatusPill, Link } from '@/ui/langcross/src'
import { confirmDialog } from '@/components/uiDialogs'
import { personas, personaCreate, personaUpdate, personaStatus, personaDelete, type PersonaItem } from '@/api/persona'
import { useT } from '@/i18n'
import { toastSuccess, toastError, toastWarn } from '@/lib/toastBus'

/** Props 无入参（角色管理为自足组件） */
type Props = Record<string, never>

/** 表格时间格式：截取到分（2026-09-19T08:00:00+08:00 → 08:00） */
const shortTime = (s?: string) => (s && s.length >= 19 ? s.slice(11, 16) : s || '—')

/**
 * PersonasP 角色管理面板（2026-09-19 需求：全栈/前端/后端/产品/项目/UIUX/运营/销售等）。
 * 展示平台全部角色；超管可新建（code 唯一）、编辑名称、启停、删除（用户引用保护）。
 */
export default function PersonasP(_props: Props) {
  const [, t] = useT()
  const [rows, setRows] = useState<PersonaItem[]>([])
  const [dlg, setDlg] = useState<null | { mode: 'create' } | { mode: 'edit'; row: PersonaItem }>(null)
  const [code, setCode] = useState('')   // 新建角色 code（小写字母/数字/下划线）
  const [name, setName] = useState('')   // 角色显示名（中/英文均可）

  /** 拉取角色字典（承载体为租户 0 的 pack_type=persona 平台包），失败时保留旧列表 */
  const load = async () => {
    const r = await runGuarded(() => personas())
    if (!r) return // ★ E10：网络/超时异常已提示，中断后续
    if (r.success && r.personas) setRows(r.personas)
  }
  useEffect(() => { void load() }, []) // eslint-disable-line react-hooks/exhaustive-deps

  // 新建角色
  const doCreate = async () => {
    const c = code.trim()
    const n = name.trim()
    if (!c || !n) { void toastWarn(t('persona.needCodeName')); return }
    const r = await runGuarded(() => personaCreate({ code: c, name: n }))
    if (!r) return
    if (r.success) {
      toastSuccess(t('persona.created'))
      setDlg(null); setCode(''); setName('')
      await load()
    } else {
      toastError(r.message || t('persona.createFail'))
    }
  }

  // 编辑角色名
  const doUpdate = async () => {
    if (!(dlg && dlg.mode === 'edit')) return
    const n = name.trim()
    if (!n) { void toastWarn(t('persona.needName')); return }
    const r = await runGuarded(() => personaUpdate(dlg.row.id, n))
    if (!r) return
    if (r.success) {
      toastSuccess(t('persona.saved'))
      setDlg(null)
      await load()
    } else {
      toastError(r.message || t('persona.saveFail'))
    }
  }

  // 启停角色
  const doToggle = async (row: PersonaItem, enabled: number) => {
    const r = await runGuarded(() => personaStatus(row.id, enabled))
    if (!r) return
    if (r.success) {
      toastSuccess(enabled === 1 ? t('persona.enabledMsg') : t('persona.disabledMsg'))
      await load()
    } else {
      toastError(r.message || t('persona.opFail'))
    }
  }

  // 删除角色（被用户引用时后端拒绝）
  const doDelete = async (row: PersonaItem) => {
    const r = await runGuarded(() => personaDelete(row.id))
    if (!r) return
    if (r.success) {
      toastSuccess(t('persona.deleted'))
      await load()
    } else {
      toastError(r.message || t('persona.deleteFail'))
    }
  }

  return (
    <>
      <div style={{ marginBottom: 10, fontSize: 15, color: 'var(--adm-hint)' }}>
        {t('persona.hint')}
      </div>
      <Button variant="primary" onClick={() => { setCode(''); setName(''); setDlg({ mode: 'create' }) }}>{t('persona.new')}</Button>
      <DataTable<any> rowKey={(row) => String(row.id)} rows={rows}
             columns={[
               { key: 'id', title: 'ID', width: 50 },
               { key: 'code', title: t('persona.colCode'), width: 130, render: (row) => <code>{row.code}</code> },
               { key: 'name', title: t('persona.colName') },
               { key: 'entry_count', title: t('persona.colEntries'), width: 90, render: (row) => row.entry_count || 0 },
               { key: 'enabled', title: t('persona.colStatus'), width: 90, render: (row) => (
                 <StatusPill tone={row.enabled === 1 ? 'success' : 'idle'}>{row.enabled === 1 ? t('persona.on') : t('persona.off')}</StatusPill>
               ) },
               { key: 'created_at', title: t('persona.colCreated'), width: 100, render: (row) => shortTime(row.created_at) },
               { key: 'op', title: t('persona.colOp'), width: 260, render: (row) => (
                 <div style={{ display: 'flex', gap: 10, alignItems: 'center' }}>
                   <Switch checked={row.enabled === 1}
                           onChange={(e) => void doToggle(row, e.target.checked ? 1 : 0)} />
                   <Link onClick={() => { setName(row.name); setDlg({ mode: 'edit', row }) }}>{t('persona.edit')}</Link>
                   <Link tone="danger" onClick={async () => { if (!(await confirmDialog({ body: t('persona.deleteConfirm') }))) return; doDelete(row) }}>{t('persona.del')}</Link>
                 </div>
               ) },
             ]}  />

      {/* 新建角色弹窗 */}
      <Dialog open={!!dlg && dlg.mode === 'create'} onCancel={() => setDlg(null)} title={t('persona.newTitle')} onConfirm={doCreate}>
        <div style={{ marginBottom: 12 }}>
          <div style={{ marginBottom: 4, fontSize: 15, color: 'var(--adm-hint)' }}>{t('persona.codeLabel')}</div>
          <input className="lc-input" value={code} onChange={(e) => setCode(String(e.target.value))} placeholder={t('persona.namePlaceholder')} />
        </div>
        <div>
          <div style={{ marginBottom: 4, fontSize: 15, color: 'var(--adm-hint)' }}>{t('persona.nameLabel')}</div>
          <input className="lc-input" value={name} onChange={(e) => setName(String(e.target.value))} placeholder={t('persona.namePlaceholder')} />
        </div>
      </Dialog>

      {/* 编辑角色名弹窗 */}
      <Dialog open={!!dlg && dlg.mode === 'edit'} onCancel={() => setDlg(null)} title={t('persona.editTitle')} onConfirm={doUpdate}>
        <div>
          <div style={{ marginBottom: 4, fontSize: 15, color: 'var(--adm-hint)' }}>{t('persona.nameLabel')}</div>
          <input className="lc-input" value={name} onChange={(e) => setName(String(e.target.value))} />
        </div>
      </Dialog>
    </>
  )
}
