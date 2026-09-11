// ============================================================================
// components/admin/OrgP.tsx — 组织架构面板
// 职责：组织树展示、组织 CRUD、用户管理、预算设置、邀请码、组织移动
// 从 panels_b.tsx 拆分
// ============================================================================
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  Button, Table, Dialog, Input, Select, Tag, Space, MessagePlugin, Tabs,
} from 'tdesign-react'
import { confirmDialog, promptText } from '@/components/uiDialogs'
import {
  orgList, orgCreate, orgRename, orgMove, orgDelete, orgUsers,
  orgBudgetSummary, orgTokenLimit,
  adminUserCreate, adminUserDelete, adminUserResetPassword, userBulkImport, downloadUserImportTemplate,
  inviteCodes, inviteCodeCreate,
  tenantSetStatus,
  request, authHeaders,
  type OrgInfo,
} from '@/api'
import { Panel, Field, toastResp, num } from './parts'
import { fmtNum, fmtTime } from '@/lib/ui'
import { useAdmin } from '@/stores/admin'
import { InvitesP } from './panels_a'
import { t, tpl, useT } from '@/i18n'

type Any = any

/** 数字缩写：≥1万 显示为 x.xw */
function fmtNumShort(n: number): string {
  if (n >= 10000) return (n / 10000).toFixed(1).replace(/\.0$/, '') + 'w'
  return String(n)
}

/** 组织树展示、组织 CRUD、用户管理、预算设置、邀请码、组织移动组件 */
export function OrgP() {
  const ad = useAdmin()
  const [orgs, setOrgs] = useState<OrgInfo[]>([])
  const [rootOrg, setRootOrg] = useState<OrgInfo | null>(null)
  const [isPlatformView, setIsPlatformView] = useState(false)
  const [budgetMap, setBudgetMap] = useState<Record<number, { limit: number; used: number }>>({})
  const [selectedOrg, setSelectedOrg] = useState(0)
  const [orgUserList, setOrgUserList] = useState<Any[]>([])
  const [parentId, setParentId] = useState(0)
  const [newName, setNewName] = useState('')
  const [nu, setNu] = useState<Any>({ username: '', password: '', display_name: '', role: 'user' })
  const [nuOrgId, setNuOrgId] = useState(0)
  const [creating, setCreating] = useState(false)
  const importFileRef = useRef<HTMLInputElement>(null)
  const [importFile, setImportFile] = useState<File | null>(null)
  const [importing, setImporting] = useState(false)
  const [importResult, setImportResult] = useState<Any[] | null>(null)
  const [budgetModal, setBudgetModal] = useState<{ id: number; name: string; limit: number; used: number } | null>(null)
  const [budgetInput, setBudgetInput] = useState(0)
  const [inviteModal, setInviteModal] = useState<{ id: number; name: string } | null>(null)
  const [inviteItems, setInviteItems] = useState<Any[]>([])
  const [inviteCodeInput, setInviteCodeInput] = useState('')
  const [moveDlg, setMoveDlg] = useState<{ node: Any } | null>(null)
  const [moveParent, setMoveParent] = useState(0)

  const myLevel = ad.myLevel
  const isSuper = ad.isSuper
  const [tab, setTab] = useState<'org' | 'invite'>('org')

  const rootOrgName = useMemo(() => {
    if (rootOrg?.name) return rootOrg.name
    const tt = ad.tenants.find((x) => x.id === ad.activeTenantId)
    return tt?.name || tpl('org.orgHash', { id: ad.activeTenantId })
  }, [rootOrg, ad.tenants, ad.activeTenantId])

  const flatOrgs = useMemo(() => {
    if (isPlatformView) return orgs.filter((o) => !(o.type === 'root' && rootOrg && o.id === rootOrg.id))
    return orgs.filter((o) => o.type !== 'root')
  }, [orgs, isPlatformView, rootOrg])

  const flatTree = useMemo(() => {
    const byParent: Record<number, OrgInfo[]> = {}
    for (const o of flatOrgs) {
      ;(byParent[o.parent_id] = byParent[o.parent_id] || []).push(o)
    }
    const out: Array<OrgInfo & { _depth: number }> = []
    const walk = (pid: number, depth: number) => {
      for (const o of byParent[pid] || []) {
        ;(o as Any)._depth = depth
        out.push(o as OrgInfo & { _depth: number })
        walk(o.id, depth + 1)
      }
    }
    walk(isPlatformView ? (rootOrg?.id || 0) : 0, 0)
    return out
  }, [flatOrgs, isPlatformView, rootOrg])

  const orgPath = (o: OrgInfo): string => {
    if (o.parent_id === 0) return o.name
    const parent = flatOrgs.find((x) => x.id === o.parent_id)
    return parent ? `${orgPath(parent)} / ${o.name}` : o.name
  }

  const orgIcon = (o: OrgInfo): string => (o.type === 'dept' ? '🏷️' : o.type === 'org' ? '🏬' : '🏢')

  const budgetText = (o: Any): string => {
    const b = budgetMap[o.id]
    if (!b || !(b.limit > 0)) return t('org.budgetUnset')
    return `${fmtNumShort(b.used)}/${fmtNumShort(b.limit)}`
  }

  const isOverBudget = (o: Any): boolean => {
    const b = budgetMap[o.id]
    return !!b && b.limit > 0 && b.used >= b.limit
  }

  const nuRoleOptions = useMemo(() => {
    const oid = nuOrgId
    if (!oid) {
      if (isPlatformView) return myLevel >= 4 ? ['admin'] : []
      return myLevel >= 3 ? ['tenant_admin', 'dept_admin', 'user'] : ['user']
    }
    const org = flatOrgs.find((x) => x.id === oid)
    if (org && org.type === 'root') return myLevel >= 3 ? ['tenant_admin', 'dept_admin', 'user'] : ['user']
    return myLevel >= 2 ? ['dept_admin', 'user'] : ['user']
  }, [nuOrgId, isPlatformView, myLevel, flatOrgs])

  function onNuOrgChange(v: any) {
    setNuOrgId(v)
    setNu((n: Any) => {
      if (!nuRoleOptions.includes(n.role)) return { ...n, role: nuRoleOptions[0] || 'user' }
      return n
    })
  }

  async function updateUser(u: Any, patch: Any): Promise<boolean> {
    const data: Any = {
      display_name: u.display_name, role: u.role, status: u.status,
      org_id: Number(u.org_id || 0),
    }
    Object.assign(data, patch)
    const r: any = await request('/api/admin/users/update', {
      method: 'POST', headers: authHeaders(), body: JSON.stringify({ id: u.id, ...data }),
    })
    if (!r.success) { void MessagePlugin.error(r.message); return false }
    return true
  }

  async function loadBudget() {
    try {
      const r: any = await orgBudgetSummary()
      if (r.success) {
        const m: Record<number, { limit: number; used: number }> = {}
        for (const d of r.summary?.depts || []) m[d.org_id] = { limit: d.token_limit, used: d.used_this_month }
        setBudgetMap(m)
      }
    } catch { /* 非租管静默 */ }
  }

  async function loadOrgUsers() {
    const r: any = await orgUsers(selectedOrg || undefined)
    if (r.success) setOrgUserList(r.users || [])
  }

  const loadAll = useCallback(async () => {
    const [r, ru] = await Promise.all([orgList(), orgUsers()])
    if (r.success) {
      setIsPlatformView(!!(r as Any).platform)
      setRootOrg((r as Any).root || null)
      setOrgs((r as Any).orgs || [])
    }
    if (ru.success) setOrgUserList(ru.users || [])
    await loadOrgUsers()
  }, [selectedOrg])

  useEffect(() => { void loadAll(); void loadBudget() }, [loadAll])
  useEffect(() => {
    setSelectedOrg(0); setNuOrgId(0)
    void loadAll(); void loadBudget()
  }, [ad.activeTenantId])

  function selectOrg(id: number) {
    setSelectedOrg(id); setNuOrgId(id); void loadOrgUsers()
  }

  async function createUser() {
    if (!nu.username?.trim() || (nu.password?.length ?? 0) < 6) { void MessagePlugin.warning(t('org.userValidation')); return }
    setCreating(true)
    try {
      const r: any = await adminUserCreate({
        username: nu.username.trim(),
        password: nu.password,
        display_name: nu.display_name?.trim() || nu.username.trim(),
        role: nu.role,
        org_id: nuOrgId || undefined,
      })
      if (!r.success) { void MessagePlugin.error(r.message); return }
      void MessagePlugin.success(tpl('org.userCreated', { name: nu.username }))
      setNu({ username: '', password: '', display_name: '', role: 'user' })
      await loadAll()
    } finally { setCreating(false) }
  }

  async function doBulkImport() {
    if (!importFile) { void MessagePlugin.warning(t('org.importNeedFile')); return }
    setImporting(true)
    try {
      const r: any = await userBulkImport(importFile)
      if (!r.success) { void MessagePlugin.error(r.message); return }
      setImportResult(r.results || [])
      void MessagePlugin.success(tpl('org.importDone', { ok: r.created || 0, fail: r.failed || 0 }))
      setImportFile(null)
      await loadAll()
    } finally { setImporting(false) }
  }

  async function downloadTemplate() {
    const ok = await downloadUserImportTemplate()
    if (!ok) void MessagePlugin.error(t('org.importTplFail'))
  }

  async function resetPwd(u: Any) {
    const pwd = await promptText({ header: t('org.resetPwdPrompt'), body: tpl('org.resetPwdPrompt', { name: u.username }) })
    if (!pwd || pwd.length < 6) { void MessagePlugin.warning(t('org.pwdMinLength')); return }
    const r: any = await adminUserResetPassword(u.id, pwd)
    if (!r.success) { void MessagePlugin.error(r.message); return }
    void MessagePlugin.success(t('org.pwdReset'))
  }

  async function setStatus(u: Any, status: string) {
    if (await updateUser(u, { status })) await loadAll()
  }

  async function deleteUser(u: Any) {
    if (!(await confirmDialog({ body: tpl('org.deleteUserConfirm', { name: u.username }) }))) return
    const r: any = await adminUserDelete(u.id)
    if (!r.success) { void MessagePlugin.error(r.message); return }
    await loadAll()
  }

  async function editUser(u: Any, field: string, val: string) {
    const v = field === 'org_id' ? Number(val) : val
    if (field === 'display_name' && !String(val).trim()) return
    if (await updateUser(u, { [field]: v })) await loadAll()
  }

  function tenantStatusOf(tid: number): string {
    return ad.tenants.find((x) => x.id === tid)?.status || 'active'
  }

  async function toggleTenantByOrg(o: OrgInfo) {
    const cur = tenantStatusOf(o.tenant_id)
    const next = cur === 'active' ? 'disabled' : 'active'
    const key = next === 'disabled' ? 'org.disableTenantConfirm' : 'org.enableTenantConfirm'
    if (!(await confirmDialog({ body: tpl(key, { name: o.name }) }))) return
    const r: any = await tenantSetStatus(o.tenant_id, next)
    if (!r.success) { void MessagePlugin.error(r.message); return }
    ad.loadTenants(); await loadAll()
  }

  function setParent(id: number) { setParentId(id); setNewName('') }

  async function createOrg() {
    if (!newName.trim()) { void MessagePlugin.warning(t('org.nameRequired')); return }
    const r: any = await orgCreate({ name: newName.trim(), parent_id: parentId, type: parentId === 0 ? 'org' : 'dept' })
    if (!r.success) { void MessagePlugin.error(r.message); return }
    setNewName(''); await loadAll()
  }

  async function renameOrg(o: OrgInfo) {
    const name = await promptText({ header: t('org.renamePrompt'), body: tpl('org.renamePrompt', { name: o.name }), defaultValue: o.name })
    if (!name || !name.trim()) return
    const r: any = await orgRename(o.id, name.trim())
    if (!r.success) { void MessagePlugin.error(r.message); return }
    if (isSuper && o.type === 'root') ad.loadTenants()
    await loadAll()
  }

  async function renameRootOrg() {
    if (!rootOrg) return
    const name = await promptText({ header: t('org.renameRoot'), body: t('org.renameRoot'), defaultValue: rootOrg.name })
    if (!name || !name.trim()) return
    const r: any = await orgRename(rootOrg.id, name.trim())
    if (!r.success) { void MessagePlugin.error(r.message); return }
    if (isSuper) ad.loadTenants()
    await loadAll()
  }

  async function deleteOrg(o: OrgInfo) {
    if (!(await confirmDialog({ body: tpl('org.deleteConfirm', { name: o.name }) }))) return
    const r: any = await orgDelete(o.id)
    if (!r.success) { void MessagePlugin.error(r.message); return }
    if (selectedOrg === o.id) setSelectedOrg(0)
    await loadAll()
  }

  function openBudget(o: Any) {
    const b = budgetMap[o.id]
    setBudgetModal({ id: o.id, name: o.name, limit: b?.limit || 0, used: b?.used || 0 })
    setBudgetInput(b?.limit || 0)
  }

  async function saveBudget() {
    if (!budgetModal) return
    if (!(budgetInput >= 0)) { void MessagePlugin.warning(t('org.budgetInvalid')); return }
    const r: any = await orgTokenLimit(budgetModal.id, Math.floor(budgetInput))
    if (!r.success) { void MessagePlugin.error(r.message); return }
    setBudgetMap((m) => ({ ...m, [budgetModal.id]: { limit: budgetInput, used: budgetModal.used } }))
    setBudgetModal(null)
  }

  async function openInvites(o: Any) {
    setInviteModal({ id: o.id, name: o.name })
    setInviteItems([])
    try {
      const r: any = await inviteCodes()
      if (r.success) setInviteItems((r.codes || []).filter((x: Any) => x.org_id === o.id))
    } catch { setInviteItems([]) }
  }

  async function createInvite() {
    if (!inviteModal) return
    const code = inviteCodeInput.trim()
    if (!code) { void MessagePlugin.warning(t('org.inviteNeedCode')); return }
    const r: any = await inviteCodeCreate({ code, tenant_id: inviteModal.id, org_id: inviteModal.id })
    if (!r.success) { void MessagePlugin.error(r.message); return }
    setInviteCodeInput('')
    await openInvites(inviteModal)
  }

  function openMove(o: Any) { setMoveParent(Number(o.parent_id ?? 0)); setMoveDlg({ node: o }) }

  async function doMove() {
    if (!moveDlg) return
    const r: any = await orgMove(moveDlg.node.id, moveParent)
    if (toastResp(r, '已移动')) { setMoveDlg(null); await loadAll() }
  }

  const addUserHeading = nuOrgId === 0
    ? (isPlatformView ? t('admin.platformRoot') : rootOrgName)
    : (flatOrgs.find((x) => x.id === nuOrgId)?.name || '')

  const orgSelectOptions = [
    { label: isPlatformView ? t('admin.platformRoot') : t('org.rootOption'), value: 0 },
    ...flatTree.map((o) => ({ label: orgPath(o), value: o.id })),
  ]
  const userOrgOptions = [
    { label: rootOrgName, value: 0 },
    ...flatTree.map((o) => ({ label: orgPath(o), value: o.id })),
  ]

  return (
    <Tabs value={tab} onChange={(v) => setTab(v as 'org' | 'invite')}>
      <Tabs.TabPanel value="org" label={t('org.tabOrg')}>
        <Panel title={t('org.title')}>
      <p style={{ fontSize: 12, color: '#888', margin: '0 0 12px' }}>{t('org.treeHint')}</p>

      {myLevel >= 3 && (
        <div style={{ display: 'flex', gap: 8, alignItems: 'center', marginBottom: 12, flexWrap: 'wrap' }}>
          <Select value={parentId} onChange={(v) => setParentId(Number(v))} style={{ minWidth: 200 }}
                  options={[
                    ...(myLevel >= 3 ? [{ label: tpl('org.rootOptionTpl', { name: rootOrgName }), value: 0 }] : []),
                    ...flatTree.map((o) => ({ label: orgPath(o), value: o.id })),
                  ]} />
          <Input value={newName} placeholder={t('org.namePlaceholder')}
                 onChange={(v) => setNewName(v)} onEnter={createOrg} style={{ flex: 1, minWidth: 160 }} />
          <Button theme="primary" onClick={createOrg}>{parentId === 0 ? t('org.create') : t('org.createDept')}</Button>
        </div>
      )}

      <div style={{ display: 'flex', gap: 16, alignItems: 'flex-start', flexWrap: 'wrap' }}>
        <div style={{ minWidth: 280, flex: '1 1 320px' }}>
          <div
            onClick={() => selectOrg(0)}
            style={{ padding: '8px 10px', borderRadius: 8, cursor: 'pointer', background: selectedOrg === 0 ? '#e8f3ff' : '#f5f7fa', display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}
          >
            <span>🏢 {rootOrgName}（{isPlatformView || isSuper ? t('org.typePlatform') : t('org.typeRoot')}）</span>
            {myLevel >= 3 && (
              <Button size="small" variant="text" title={t('org.renameRoot')} onClick={(e) => { e.stopPropagation(); renameRootOrg() }}>✎</Button>
            )}
          </div>
          {flatTree.map((o) => (
            <div
              key={o.id}
              onClick={() => selectOrg(o.id)}
              style={{ padding: `8px 10px 8px ${8 + o._depth * 18}px`, borderRadius: 8, cursor: 'pointer', margin: '4px 0', background: selectedOrg === o.id ? '#e8f3ff' : '#fff', border: '1px solid #eef0f3', display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: 6, flexWrap: 'wrap' }}
            >
              <span>
                <span style={{ opacity: 0.6, marginRight: 4 }}>⠿</span>
                {orgIcon(o)} {o.name}
              </span>
              <span style={{ display: 'flex', alignItems: 'center', gap: 4, flexWrap: 'wrap' }}>
                {o.type !== 'root' && myLevel >= 3 && (
                  <span className={isOverBudget(o) ? 'budget-over' : ''}
                        title={t('org.budgetSet')}
                        onClick={(e) => { e.stopPropagation(); openBudget(o) }}
                        style={{ cursor: 'pointer', fontSize: 12, color: isOverBudget(o) ? '#c62828' : '#667', border: `1px solid ${isOverBudget(o) ? '#c62828' : '#d0d7de'}`, borderRadius: 8, padding: '0 6px' }}>
                    💰 {budgetText(o)}
                  </span>
                )}
                {o.type !== 'root' && myLevel >= 3 && (
                  <Button size="small" variant="text" title={t('org.inviteEntry')} onClick={(e) => { e.stopPropagation(); openInvites(o) }}>🎟️</Button>
                )}
                {isSuper && o.type === 'root' && (
                  <Button size="small" variant="text"
                          onClick={(e) => { e.stopPropagation(); toggleTenantByOrg(o) }}>
                    {tenantStatusOf(o.tenant_id) === 'active' ? t('tenants.disable') : t('tenants.enable')}
                  </Button>
                )}
                <Button size="small" variant="text" title={t('org.addChild')} onClick={(e) => { e.stopPropagation(); setParent(o.id) }}>+</Button>
                {myLevel >= 3 && (
                  <Button size="small" variant="text" title={t('org.rename')} onClick={(e) => { e.stopPropagation(); renameOrg(o) }}>✎</Button>
                )}
                {o.type !== 'root' && myLevel >= 3 && (
                  <>
                    <Button size="small" variant="text" title={t('org.move')} onClick={(e) => { e.stopPropagation(); openMove(o) }}>⇄</Button>
                    <Button size="small" variant="text" theme="danger" title={t('org.delete')} onClick={(e) => { e.stopPropagation(); deleteOrg(o) }}>✕</Button>
                  </>
                )}
              </span>
            </div>
          ))}
        </div>

        <div style={{ flex: '2 1 480px', minWidth: 360 }}>
          <h3 style={{ margin: '0 0 4px' }}>
            {selectedOrg === 0 ? tpl('org.allUsersRootTpl', { name: rootOrgName }) : tpl('org.usersInChildren', { name: flatOrgs.find((x) => x.id === selectedOrg)?.name || '' })}
          </h3>
          <p style={{ fontSize: 12, color: '#888', margin: '0 0 12px' }}>{t('org.usersHint')}</p>
          <Table rowKey="id" size="small" maxHeight={360} data={orgUserList}
                 columns={[
                   { colKey: 'id', title: t('org.colId'), width: 60 },
                   { colKey: 'username', title: t('org.colUsername'), width: 110 },
                   { colKey: 'display_name', title: t('org.colName'), cell: ({ row }: any) => (
                     <Input size="small" value={String(row.display_name ?? '')}
                            onChange={(v) => editUser(row, 'display_name', v)} />
                   ) },
                   { colKey: 'org', title: t('org.colOrg'), width: 160, cell: ({ row }: any) => (
                     <Select size="small" value={Number(row.org_id || 0)} onChange={(v) => editUser(row, 'org_id', String(v))}
                             options={userOrgOptions} />
                   ) },
                   { colKey: 'role', title: t('org.colRole'), width: 130, cell: ({ row }: any) => (
                     <Select size="small" value={String(row.role)} onChange={(v) => editUser(row, 'role', String(v))}
                             options={ad.roleOptions.map((r) => ({ label: t('users.role.' + r), value: r }))} />
                   ) },
                   { colKey: 'last_login_at', title: t('org.colLastLogin'), width: 140, cell: ({ row }: any) => fmtTime(row.last_login_at as string) },
                   { colKey: 'op', title: t('org.colActions'), width: 200, cell: ({ row }: any) => (
                     <Space size={2}>
                       <Button size="small" variant="text" onClick={() => resetPwd(row)}>{t('org.resetPwd')}</Button>
                       {row.status === 'disabled'
                         ? <Button size="small" variant="text" onClick={() => setStatus(row, 'active')}>{t('org.enable')}</Button>
                         : <Button size="small" variant="text" theme="danger" onClick={() => setStatus(row, 'disabled')}>{t('org.disable')}</Button>}
                       <Button size="small" variant="text" theme="danger" onClick={() => deleteUser(row)}>✕</Button>
                     </Space>
                   ) },
                 ] as never} />
          {!orgUserList.length && <div style={{ fontSize: 12, color: '#999', padding: 8 }}>{t('org.noUsers')}</div>}

          <div style={{ marginTop: 16, border: '1px solid #e3e6ef', borderRadius: 8, padding: 14 }}>
            <h3 style={{ margin: '0 0 10px' }}>{tpl('org.addUser', { org: addUserHeading })}</h3>
            <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', marginBottom: 8 }}>
              <Input value={String(nu.username || '')} placeholder={t('org.usernamePlaceholder')} onChange={(v) => setNu((n: Any) => ({ ...n, username: v }))} style={{ flex: 1, minWidth: 140 }} />
              <Input value={String(nu.password || '')} placeholder={t('org.passPlaceholder')} onChange={(v) => setNu((n: Any) => ({ ...n, password: v }))} style={{ flex: 1, minWidth: 140 }} />
              <Input value={String(nu.display_name || '')} placeholder={t('org.displayNamePlaceholder')} onChange={(v) => setNu((n: Any) => ({ ...n, display_name: v }))} style={{ flex: 1, minWidth: 140 }} />
            </div>
            <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', alignItems: 'center' }}>
              <span style={{ fontSize: 12, color: '#555' }}>{t('org.orgLabel')}</span>
              <Select value={nuOrgId} onChange={onNuOrgChange} style={{ flex: 1, minWidth: 160 }} options={orgSelectOptions} />
              <span style={{ fontSize: 12, color: '#555' }}>{t('org.roleLabel')}</span>
              <Select value={String(nu.role)} onChange={(v) => setNu((n: Any) => ({ ...n, role: v }))}
                      options={nuRoleOptions.map((r) => ({ label: t('users.role.' + r), value: r }))} />
              <span style={{ fontSize: 12, color: '#888', flex: 1, minWidth: 120 }}>{t('org.cascadeHint')}</span>
              <Button theme="primary" disabled={creating} onClick={createUser}>{creating ? t('org.creating') : t('org.addUserBtn')}</Button>
              {myLevel >= 3 && (
                <>
                  <Button variant="outline" onClick={() => void downloadTemplate()}
                          title={t('org.importTplHint')}>📄 {t('org.importTplBtn')}</Button>
                  <input ref={importFileRef} type="file" hidden accept=".xlsx,.xls,.csv"
                         onChange={(e) => { setImportFile(e.target.files?.[0] || null); e.target.value = '' }} />
                  <Button variant="outline" disabled={importing} onClick={() => importFileRef.current?.click()}
                          title={t('org.importHint')}>{importing ? t('org.importing') : t('org.importBtn')}</Button>
                  <Button variant="outline" disabled={importing || !importFile} onClick={doBulkImport}
                          title={importFile ? importFile.name : ''}>↗</Button>
                </>
              )}
            </div>
          </div>
        </div>
      </div>

          <Dialog visible={!!importResult} onClose={() => setImportResult(null)}
                  header={`📥 ${tpl('org.importResultTitle', { total: importResult?.length || 0 })}`} width={520}>
            <Table rowKey="username" size="small" maxHeight={360} data={importResult || []}
                   columns={[
                     { colKey: 'username', title: t('org.importResultCol') },
                     { colKey: 'ok', title: t('org.importResultStatus'), width: 70, cell: ({ row }: any) => row.ok ? <Tag theme="success">{t('org.importResultOk')}</Tag> : <Tag theme="danger">{t('org.importResultFail')}</Tag> },
                     { colKey: 'message', title: '', cell: ({ row }: any) => <span style={{ fontSize: 12, color: row.ok ? '#666' : '#c62828' }}>{row.message}</span> },
                   ] as never} />
          </Dialog>

      <Dialog visible={!!budgetModal} onClose={() => setBudgetModal(null)}
              header={`💰 ${t('org.budgetTitle')} · ${budgetModal?.name || ''}`} width={440}
              onConfirm={saveBudget}>
        {budgetModal && (
          <>
            <p style={{ fontSize: 12, color: '#888', margin: '0 0 10px' }}>{tpl('org.budgetHint', { used: fmtNumShort(budgetModal.used) })}</p>
            <label style={{ display: 'block', marginBottom: 4, fontSize: 12, color: '#555' }}>{t('org.budgetLimit')}</label>
            <Input type="number" value={num(budgetInput)} placeholder={t('org.budgetPlaceholder')} onChange={(v) => setBudgetInput(Number(v))} />
          </>
        )}
      </Dialog>

      <Dialog visible={!!inviteModal} onClose={() => setInviteModal(null)}
              header={`🎟️ ${t('org.inviteTitle')} · ${inviteModal?.name || ''}`} width={440}>
        {inviteModal && (
          <>
            <p style={{ fontSize: 12, color: '#888', margin: '0 0 10px' }}>{t('org.inviteHint')}</p>
            <div style={{ display: 'flex', gap: 8, marginBottom: 8 }}>
              <Input value={inviteCodeInput} placeholder={t('org.inviteInputPlaceholder')} onChange={(v) => setInviteCodeInput(v)} style={{ flex: 1 }} />
              <Button theme="primary" disabled={!inviteCodeInput.trim()} onClick={createInvite}>➕ {t('org.inviteCreate')}</Button>
            </div>
            {inviteItems.length ? (
              <Table rowKey="id" size="small" data={inviteItems}
                     columns={[
                       { colKey: 'code', title: t('org.inviteCode'), cell: ({ row }: any) => <code>{row.code}</code> },
                       { colKey: 'status', title: t('org.inviteStatus'), cell: ({ row }: any) => (Number(row.used_count) > 0 ? tpl('org.inviteUsed', { n: row.used_count }) : t('org.inviteUnused')) },
                     ] as never} />
            ) : <p style={{ fontSize: 12, color: '#999' }}>{t('org.inviteNoCodes')}</p>}
          </>
        )}
      </Dialog>

      <Dialog visible={!!moveDlg} onClose={() => setMoveDlg(null)} header="移动组织" width={440} onConfirm={doMove}>
        {moveDlg && (
          <Field label="移动到">
            <Select value={moveParent} onChange={(v) => setMoveParent(Number(v))}
                    options={[{ label: t('org.rootOption'), value: 0 }, ...flatTree.filter((o) => o.id !== moveDlg.node.id).map((o) => ({ label: `#${o.id} ${o.name}`, value: o.id }))]} />
          </Field>
        )}
      </Dialog>
        </Panel>
      </Tabs.TabPanel>
      <Tabs.TabPanel value="invite" label={t('org.tabInvite')}>
        <InvitesP />
      </Tabs.TabPanel>
    </Tabs>
  )
}
