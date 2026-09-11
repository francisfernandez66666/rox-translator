// ============================================================================
// components/admin/TenantsP.tsx — 租户管理面板
// 职责：租户 CRUD、试用开通、充值、导出、状态启停、GDPR 擦除
// 从 panels_b.tsx 拆分
// ============================================================================
import { useCallback, useEffect, useState } from 'react'
import {
  Button, Table, Dialog, Input, Select, Switch, Tag, Space, Popconfirm, Textarea, MessagePlugin,
} from 'tdesign-react'
import { confirmDialog, promptText } from '@/components/uiDialogs'
import {
  tenantList, tenantCreate, tenantUpdate, tenantSetStatus, tenantDelete,
  tenantGrantTrial, tenantErase, adminOrderCreate, adminOrderPay,
  request, authHeaders, API_BASE, getAuthToken,
  type TenantInfo,
} from '@/api'
import { Panel, Field, toastResp, num } from './parts'
import { useAdmin } from '@/stores/admin'
import { t, tpl, useT, type Lang } from '@/i18n'
import { industryName, industryCodeOf } from '@/lib/industries'
import { industries as fetchIndustries } from '@/api/industry'

type Any = any

/** 行业 code → 当前语言展示名（列表列用） */
const industryLabel = (code: string, lang: Lang): string => industryName(code || '', lang) || '—'

/** 租户 CRUD、试用开通、充值、导出、状态启停、GDPR 擦除组件 */
export function TenantsP() {
  const ad = useAdmin()
  const [lang] = useT()
  const [rows, setRows] = useState<TenantInfo[]>([])
  const [dlg, setDlg] = useState<null | 'create' | { edit: TenantInfo } | { order: TenantInfo }>(null)
  const [form, setForm] = useState<Any>({})
  const [indList, setIndList] = useState<Array<{ code: string; name: string; enabled: number }>>([])

  const loadInd = useCallback(async () => {
    try {
      const r = await fetchIndustries()
      if (r.success && r.industries) setIndList(r.industries)
    } catch { /* ignore */ }
  }, [])
  useEffect(() => { void loadInd() }, [loadInd])

  const load = useCallback(async () => {
    const r: any = await tenantList()
    if (r.success) setRows(r.tenants || [])
  }, [])
  useEffect(() => { void load() }, [load])

  async function save() {
    if (dlg === 'create') {
      if (!String(form.code || '').trim()) { void MessagePlugin.warning(t('tenants.codeRequired')); return }
      const r: any = await tenantCreate({
        code: String(form.code || ''), name: String(form.name || ''),
        expires_at: String(form.expires_at || ''), permissions: String(form.permissions || '{}'),
        admin_user: form.admin_user ? String(form.admin_user) : undefined,
        admin_pass: form.admin_pass ? String(form.admin_pass) : undefined,
        industry: form.industry ? String(form.industry) : undefined,
      })
      if (!r.success) { void MessagePlugin.error(r.message); return }
      setForm({}); setDlg(null); await load(); ad.loadTenants()
    } else if (dlg && typeof dlg === 'object' && 'edit' in dlg) {
      const tt = dlg.edit
      const r: any = await tenantUpdate({
        id: tt.id, name: String(form.name ?? tt.name),
        expires_at: String(form.expires_at ?? tt.expires_at ?? ''),
        permissions: String(form.permissions ?? tt.permissions ?? '{}'),
        industry: String(form.industry ? industryCodeOf(String(form.industry)) : (tt.industry ?? '')),
      })
      if (!r.success) { void MessagePlugin.error(r.message); return }
      setDlg(null); await load()
    }
  }

  async function doExport(tt: TenantInfo) {
    if (!(await confirmDialog({ body: tpl('tenants.exportConfirm', { name: tt.name }) }))) return
    const url = `${API_BASE}/api/tenant/export`
    const xhr = new XMLHttpRequest()
    xhr.open('POST', url, true)
    xhr.setRequestHeader('Content-Type', 'application/json')
    const tk = getAuthToken()
    if (tk) xhr.setRequestHeader('Authorization', `Bearer ${tk}`)
    xhr.responseType = 'blob'
    xhr.onload = () => {
      if (xhr.status !== 200) { void MessagePlugin.error(t('tenants.exportFailed')); return }
      const a = document.createElement('a')
      a.href = URL.createObjectURL(xhr.response)
      a.download = `tenant_${tt.id}_${tt.code}.json`
      a.click()
      URL.revokeObjectURL(a.href)
    }
    xhr.send(JSON.stringify({ id: tt.id }))
  }

  async function charge(tt: TenantInfo) {
    const tokens = await promptText({ body: tpl('tenants.chargePrompt', { name: tt.name }) })
    if (!tokens || Number(tokens) <= 0) return
    const r: any = await adminOrderCreate({ tenant_id: tt.id, tokens: Number(tokens), money: 0 })
    if (!r.success) { void MessagePlugin.error(r.message); return }
    const o = r.order
    if (o && o.id) await adminOrderPay(o.id, tt.id)
    void MessagePlugin.success(tpl('tenants.charged', { tokens }))
    await load()
  }

  async function removeTenant(tt: TenantInfo) {
    if (!(await confirmDialog({ body: tpl('tenants.deleteConfirm', { name: tt.name }) }))) return
    const r: any = await tenantDelete(tt.id)
    if (!r.success) { void MessagePlugin.error(r.message); return }
    await load(); ad.loadTenants()
  }

  async function erase(tt: TenantInfo) {
    if (!(await confirmDialog({ body: tpl('tenants.eraseConfirm', { name: tt.name }) }))) return
    if (!(await confirmDialog({ body: tpl('tenants.eraseConfirm2', { name: tt.name }) }))) return
    const r: any = await tenantErase(tt.id)
    if (!r.success) { void MessagePlugin.error(r.message); return }
    void MessagePlugin.success(t('tenants.erased'))
    await load()
  }

  async function setInvite(tt: TenantInfo, val: boolean) {
    const r: any = await tenantUpdate({
      id: tt.id, name: tt.name, expires_at: tt.expires_at || '',
      permissions: tt.permissions || '{}', invite_enabled: val,
    })
    if (!r.success) { void MessagePlugin.error(r.message); return }
    await load()
  }

  async function grantTrial(tt: TenantInfo) {
    if (!(await confirmDialog({ body: tpl('tenants.grantTrialConfirm', { name: tt.name }) }))) return
    const r: any = await tenantGrantTrial(tt.id)
    if (!r.success) { void MessagePlugin.error(r.message); return }
    void MessagePlugin.success(t('tenants.grantTrialDone'))
    await load()
  }

  return (
    <Panel title={t('tenants.title')} extra={<Button theme="primary" onClick={() => { setForm({ permissions: '{}' }); setDlg('create') }}>{t('tenants.create')}</Button>}>
      <Table rowKey="id" size="small" data={rows}
             columns={[
               { colKey: 'id', title: t('tenants.colId'), width: 60 },
               { colKey: 'code', title: t('tenants.colCode'), width: 120 },
               { colKey: 'name', title: t('tenants.colName') },
               { colKey: 'industry', title: t('tenants.industry'), width: 130, cell: ({ row }: any) => industryLabel(row.industry || '', lang) },
               { colKey: 'status', title: t('tenants.colStatus'), width: 90, cell: ({ row }: any) => {
                 const s = row.status
                 const label = s === 'active' ? t('tenants.enable') : s === 'disabled' ? t('tenants.disable') : t('tenants.expired')
                 return <Tag theme={s === 'active' ? 'success' : s === 'expired' ? 'warning' : 'default'}>{label}</Tag>
               } },
                { colKey: 'expires_at', title: t('tenants.colExpires'), width: 120, cell: ({ row }: any) => row.expires_at || t('tenants.forever') },
                { colKey: 'invite', title: t('tenants.invite'), width: 110, cell: ({ row }: any) => (
                  <Switch size="small" value={!!row.invite_enabled} onChange={(v: boolean) => { void setInvite(row, v) }} />
                ) },
               { colKey: 'op', title: t('tenants.colActions'), width: 380, cell: ({ row }: any) => (
                 <Space size={2} breakLine>
                   <Button size="small" variant="text" onClick={() => { setForm({ name: row.name, expires_at: row.expires_at || '', permissions: row.permissions || '{}', industry: row.industry || '' }); setDlg({ edit: row }) }}>{t('tenants.edit')}</Button>
                   <Button size="small" variant="text" onClick={() => grantTrial(row)}>{t('tenants.grantTrial')}</Button>
                   <Button size="small" variant="text"
                            onClick={async () => { const r: any = await tenantSetStatus(row.id, row.status === 'active' ? 'disabled' : 'active'); if (!r.success) void MessagePlugin.error(r.message); await load() }}>
                     {row.status === 'active' ? t('tenants.disable') : t('tenants.enable')}
                   </Button>
                   {row.id !== 1 && (
                     <Popconfirm content={t('tenants.deleteConfirm')} onConfirm={async () => { await removeTenant(row) }}>
                       <Button size="small" variant="text" theme="danger">{t('tenants.delete')}</Button>
                     </Popconfirm>
                   )}
                   <Button size="small" variant="text" onClick={() => charge(row)}>{t('tenants.charge')}</Button>
                   <Button size="small" variant="text" onClick={() => doExport(row)}>{t('tenants.exportData')}</Button>
                   {row.id !== 1 && (
                     <Button size="small" variant="text" theme="danger" onClick={() => erase(row)}>{t('tenants.eraseData')}</Button>
                   )}
                 </Space>
               ) },
             ] as never} />

      <Dialog visible={!!dlg && (dlg === 'create' || (dlg && typeof dlg === 'object' && 'edit' in dlg))}
               onClose={() => setDlg(null)} header={dlg === 'create' ? t('tenants.create') : t('tenants.title')} width={520}
               onConfirm={save}>
        {(dlg === 'create' || (dlg && typeof dlg === 'object' && 'edit' in dlg)) && (
          <>
            {dlg === 'create' && (
              <Field label={t('tenants.codePlaceholder')}><Input value={String(form.code || '')} onChange={(v) => setForm({ ...form, code: v })} /></Field>
            )}
            <Field label={t('tenants.namePlaceholder')}><Input value={String(form.name ?? '')} onChange={(v) => setForm({ ...form, name: v })} /></Field>
            <Field label={t('tenants.colExpires')}><Input value={String(form.expires_at ?? '')} placeholder="YYYY-MM-DD" onChange={(v) => setForm({ ...form, expires_at: v })} /></Field>
            <Field label={t('tenants.permissionsHint')}><Textarea autosize={{ minRows: 3 }} value={String(form.permissions ?? '{}')} onChange={(v) => setForm({ ...form, permissions: v })} /></Field>
            <Field label={t('tenants.industry')}>
              <Select value={String(form.industry ?? '')} onChange={(v: any) => setForm({ ...form, industry: String(v) })}>
                <Select.Option key="" value="" label={lang === 'en' ? 'Unset' : '（未设置）'} />
                {indList.map((o) => <Select.Option key={o.code} value={o.code} label={o.name} />)}
              </Select>
            </Field>
            {dlg === 'create' && (
              <>
                <Field label={t('tenants.adminUserPlaceholder')}><Input value={String(form.admin_user || '')} onChange={(v) => setForm({ ...form, admin_user: v })} /></Field>
                <Field label={t('tenants.initPassPlaceholder')}><Input type="password" value={String(form.admin_pass || '')} onChange={(v) => setForm({ ...form, admin_pass: v })} /></Field>
              </>
            )}
          </>
        )}
      </Dialog>

      <Dialog visible={!!dlg && typeof dlg === 'object' && 'order' in dlg}
              onClose={() => setDlg(null)} header={dlg && typeof dlg === 'object' && 'order' in dlg ? `#${(dlg as { order: TenantInfo }).order.id} ${t('tenants.charge')}` : ''} width={440}
              onConfirm={async () => {
                if (!(dlg && typeof dlg === 'object' && 'order' in dlg)) return
                const tt = (dlg as { order: TenantInfo }).order
                const r: any = await adminOrderCreate({ tenant_id: tt.id, tokens: Number(form.tokens || 0), money: Number(form.money || 0) })
                if (toastResp(r, t('tenants.charged'))) {
                  const oid = Number(r.order?.id ?? r.id ?? 0)
                  if (oid > 0) await adminOrderPay(oid, tt.id)
                  setDlg(null)
                }
              }}>
        <Field label="token"><Input type="number" value={num(form.tokens || 0)} onChange={(v) => setForm({ ...form, tokens: v })} /></Field>
        <Field label="¥"><Input type="number" value={num(form.money || 0)} onChange={(v) => setForm({ ...form, money: v })} /></Field>
      </Dialog>
    </Panel>
  )
}
