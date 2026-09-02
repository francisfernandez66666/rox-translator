// ============================================================================
// components/admin/panels_b.tsx — Tenants / Org
// 职责：后台面板 B，包含租户管理与组织架构功能。
// 功能对齐 Vue: frontend/src/components/admin/Tenants.vue 与 Org.vue
// ============================================================================
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  Button, Table, Dialog, Input, Select, Switch, Tag, Space, Popconfirm, Textarea, Tabs, MessagePlugin,
} from 'tdesign-react'
import { confirmDialog, promptText } from '@/components/uiDialogs'
import {
  tenantList, tenantCreate, tenantUpdate, tenantSetStatus, tenantDelete,
  tenantGrantTrial, tenantErase, adminOrderCreate, adminOrderPay,
  orgList, orgCreate, orgRename, orgMove, orgDelete, orgUsers,
  orgBudgetSummary, orgTokenLimit,
  adminUserCreate, adminUserDelete, adminUserResetPassword, userBulkImport,
  inviteCodes, inviteCodeCreate,
  request, authHeaders, API_BASE, getAuthToken,
  type TenantInfo, type OrgInfo,
} from '@/api'
import { Panel, Field, toastResp, num } from './parts'
import { fmtNum, fmtTime } from '@/lib/ui'
import { useAdmin } from '@/stores/admin'
import { InvitesP } from './panels_a'
import { t, tpl, useT, type Lang } from '@/i18n'
import { industryName, industryOptions, industryCodeOf } from '@/lib/industries'

type Any = any // 兜底类型：避免对 TD 组件回调/返回结构强类型化（沿用项目惯例）

/** 数字缩写：≥1万 显示为 x.xw（用于组织预算展示） */
function fmtNumShort(n: number): string {
  if (n >= 10000) return (n / 10000).toFixed(1).replace(/\.0$/, '') + 'w'
  return String(n)
}

/** 行业下拉可选项（含空项，label 按当前语言自适应，值=中文名，功能②/③） */
const tenantIndustryOptions = (lang: Lang) => [
  { value: '', label: lang === 'en' ? 'Unset' : '（未设置）' },
  ...industryOptions(lang),
]

/** 行业 code → 当前语言展示名（列表列用） */
const industryLabel = (code: string, lang: Lang): string => industryName(code || '', lang) || '—'

// ==================== 租户管理面板（Vue Tenants.vue） ====================

/** 租户 CRUD、试用开通、充值、导出、状态启停、GDPR 擦除组件 */
export function TenantsP() {
  const ad = useAdmin()
  const [lang] = useT()
  // 租户列表数据
  const [rows, setRows] = useState<TenantInfo[]>([])
  // 弹窗状态：复用 create / edit / order 三种模式
  const [dlg, setDlg] = useState<null | 'create' | { edit: TenantInfo } | { order: TenantInfo }>(null)
  // 表单数据
  const [form, setForm] = useState<Any>({})

  /** 解析租户权限 JSON 中的 package_code（空表示未开通套餐，可发放试用） */
  const pkgOf = (row: TenantInfo): string => {
    try { return (JSON.parse(row.permissions || '{}') as Any).package_code || '' } catch { return '' }
  }

  /** 加载租户列表 */
  const load = useCallback(async () => {
    const r: any = await tenantList()
    if (r.success) setRows(r.tenants || [])
  }, [])
  useEffect(() => { void load() }, [load])

  /** 保存租户：区分创建与编辑 */
  async function save() {
    if (dlg === 'create') {
      // 创建租户：校验编码必填
      if (!String(form.code || '').trim()) { void MessagePlugin.warning(t('tenants.codeRequired')); return }
      const r: any = await tenantCreate({
        code: String(form.code || ''), name: String(form.name || ''),
        expires_at: String(form.expires_at || ''), permissions: String(form.permissions || '{}'),
        admin_user: form.admin_user ? String(form.admin_user) : undefined,
        admin_pass: form.admin_pass ? String(form.admin_pass) : undefined,
      })
      if (!r.success) { void MessagePlugin.error(r.message); return }
      setForm({}); setDlg(null); await load(); ad.loadTenants()
    } else if (dlg && typeof dlg === 'object' && 'edit' in dlg) {
      // 编辑租户：更新名称、过期时间、权限、行业（功能②）
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

  /** 导出租户数据：POST /api/tenant/export 返回 blob，XHR 下载（对齐 Vue） */
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

  /** 超管代充值：输入 token 数创建订单并自动模拟支付入账 */
  async function charge(tt: TenantInfo) {
    const tokens = await promptText({ body: tpl('tenants.chargePrompt', { name: tt.name }) })
    if (!tokens || Number(tokens) <= 0) return
    const r: any = await adminOrderCreate({ tenant_id: tt.id, tokens: Number(tokens), money: 0 })
    if (!r.success) { void MessagePlugin.error(r.message); return }
    const o = r.order
    if (o && o.id) await adminOrderPay(o.id)
    void MessagePlugin.success(tpl('tenants.charged', { tokens }))
    await load()
  }

  /** 删除租户（id=1 为平台默认租户，禁止删除） */
  async function removeTenant(tt: TenantInfo) {
    if (!(await confirmDialog({ body: tpl('tenants.deleteConfirm', { name: tt.name }) }))) return
    const r: any = await tenantDelete(tt.id)
    if (!r.success) { void MessagePlugin.error(r.message); return }
    await load(); ad.loadTenants()
  }

  /** GDPR 擦除：两次确认后调用擦除接口（对齐 Vue confirm×2） */
  async function erase(tt: TenantInfo) {
    if (!(await confirmDialog({ body: tpl('tenants.eraseConfirm', { name: tt.name }) }))) return
    if (!(await confirmDialog({ body: tpl('tenants.eraseConfirm2', { name: tt.name }) }))) return
    const r: any = await tenantErase(tt.id)
    if (!r.success) { void MessagePlugin.error(r.message); return }
    void MessagePlugin.success(t('tenants.erased'))
    await load()
  }

  /** 切换租户「邀请好友」功能开关（超管按租户控制是否开放邀请裂变） */
  async function setInvite(tt: TenantInfo, val: boolean) {
    const r: any = await tenantUpdate({
      id: tt.id, name: tt.name, expires_at: tt.expires_at || '',
      permissions: tt.permissions || '{}', invite_enabled: val,
    })
    if (!r.success) { void MessagePlugin.error(r.message); return }
    await load()
  }

  /** 为租户发放/重新发放体验额度（任务2.4：叠加发放，已开通/已订阅也可再领） */
  async function grantTrial(tt: TenantInfo) {
    if (!(await confirmDialog({ body: tpl('tenants.grantTrialConfirm', { name: tt.name }) }))) return
    const r: any = await tenantGrantTrial(tt.id)
    if (!r.success) { void MessagePlugin.error(r.message); return }
    void MessagePlugin.success(t('tenants.grantTrialDone'))
    await load()
  }

  return (
    <Panel title={t('tenants.title')} extra={<Button theme="primary" onClick={() => { setForm({ permissions: '{}' }); setDlg('create') }}>{t('tenants.create')}</Button>}>
      {/* 租户列表表格 */}
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
                   {/* 编辑租户（名称/权限/行业，功能②） */}
                   <Button size="small" variant="text" onClick={() => { setForm({ name: row.name, expires_at: row.expires_at || '', permissions: row.permissions || '{}', industry: row.industry ? industryName(row.industry, 'zh') : '' }); setDlg({ edit: row }) }}>{t('tenants.edit')}</Button>
                   {/* ★ 发放/重新发放体验额度（任务2.4：对所有租户可用，叠加发放新体验；已开通也可再领） */}
                   <Button size="small" variant="text" onClick={() => grantTrial(row)}>{t('tenants.grantTrial')}</Button>
                   {/* 启用/禁用租户 */}
                   <Button size="small" variant="text"
                            onClick={async () => { const r: any = await tenantSetStatus(row.id, row.status === 'active' ? 'disabled' : 'active'); if (!r.success) void MessagePlugin.error(r.message); await load() }}>
                     {row.status === 'active' ? t('tenants.disable') : t('tenants.enable')}
                   </Button>
                   {/* 删除租户（id=1 为平台默认租户，禁止删除） */}
                   {row.id !== 1 && (
                     <Popconfirm content={t('tenants.deleteConfirm')} onConfirm={async () => { await removeTenant(row) }}>
                       <Button size="small" variant="text" theme="danger">{t('tenants.delete')}</Button>
                     </Popconfirm>
                   )}
                   {/* 超管代充值 */}
                   <Button size="small" variant="text" onClick={() => charge(row)}>{t('tenants.charge')}</Button>
                   {/* 导出租户数据 */}
                   <Button size="small" variant="text" onClick={() => doExport(row)}>{t('tenants.exportData')}</Button>
                   {/* GDPR 擦除（id=1 为平台默认租户，禁止擦除） */}
                   {row.id !== 1 && (
                     <Button size="small" variant="text" theme="danger" onClick={() => erase(row)}>{t('tenants.eraseData')}</Button>
                   )}
                 </Space>
               ) },
             ] as never} />

      {/* 创建/编辑租户弹窗 */}
      <Dialog visible={!!dlg && (dlg === 'create' || (dlg && typeof dlg === 'object' && 'edit' in dlg))}
               onClose={() => setDlg(null)} header={dlg === 'create' ? t('tenants.create') : t('tenants.title')} width={520}
               onConfirm={save}>
        {(dlg === 'create' || (dlg && typeof dlg === 'object' && 'edit' in dlg)) && (
          <>
            {/* 创建模式：显示租户编码输入框 */}
            {dlg === 'create' && (
              <Field label={t('tenants.codePlaceholder')}><Input value={String(form.code || '')} onChange={(v) => setForm({ ...form, code: v })} /></Field>
            )}
            <Field label={t('tenants.namePlaceholder')}><Input value={String(form.name ?? '')} onChange={(v) => setForm({ ...form, name: v })} /></Field>
            <Field label={t('tenants.colExpires')}><Input value={String(form.expires_at ?? '')} placeholder="YYYY-MM-DD" onChange={(v) => setForm({ ...form, expires_at: v })} /></Field>
            <Field label={t('tenants.permissionsHint')}><Textarea autosize={{ minRows: 3 }} value={String(form.permissions ?? '{}')} onChange={(v) => setForm({ ...form, permissions: v })} /></Field>
            {/* 行业属性（功能②：决定共享行业包载入范围，超管可修正） */}
            <Field label={t('tenants.industry')}>
              <Select value={String(form.industry ?? '')} onChange={(v: any) => setForm({ ...form, industry: String(v) })}>
                {tenantIndustryOptions(lang).map((o) => <Select.Option key={o.value} value={o.value} label={o.label} />)}
              </Select>
            </Field>
            {/* 创建模式：显示管理员账号与初始密码 */}
            {dlg === 'create' && (
              <>
                <Field label={t('tenants.adminUserPlaceholder')}><Input value={String(form.admin_user || '')} onChange={(v) => setForm({ ...form, admin_user: v })} /></Field>
                <Field label={t('tenants.initPassPlaceholder')}><Input type="password" value={String(form.admin_pass || '')} onChange={(v) => setForm({ ...form, admin_pass: v })} /></Field>
              </>
            )}
          </>
        )}
      </Dialog>

      {/* 超管代充值弹窗 */}
      <Dialog visible={!!dlg && typeof dlg === 'object' && 'order' in dlg}
              onClose={() => setDlg(null)} header={dlg && typeof dlg === 'object' && 'order' in dlg ? `#${(dlg as { order: TenantInfo }).order.id} ${t('tenants.charge')}` : ''} width={440}
              onConfirm={async () => {
                if (!(dlg && typeof dlg === 'object' && 'order' in dlg)) return
                const tt = (dlg as { order: TenantInfo }).order
                const r: any = await adminOrderCreate({ tenant_id: tt.id, tokens: Number(form.tokens || 0), money: Number(form.money || 0) })
                if (toastResp(r, t('tenants.charged'))) {
                  const oid = Number(r.order?.id ?? r.id ?? 0)
                  if (oid > 0) await adminOrderPay(oid)
                  setDlg(null)
                }
              }}>
        <Field label="token"><Input type="number" value={num(form.tokens || 0)} onChange={(v) => setForm({ ...form, tokens: v })} /></Field>
        <Field label="¥"><Input type="number" value={num(form.money || 0)} onChange={(v) => setForm({ ...form, money: v })} /></Field>
      </Dialog>
    </Panel>
  )
}

// ==================== 组织架构面板（Vue Org.vue） ====================

/** 组织树展示、组织 CRUD、用户管理、预算设置、邀请码、组织移动组件 */
export function OrgP() {
  const ad = useAdmin()
  // 组织数据
  const [orgs, setOrgs] = useState<OrgInfo[]>([])
  const [rootOrg, setRootOrg] = useState<OrgInfo | null>(null)
  const [isPlatformView, setIsPlatformView] = useState(false)
  // 预算：org_id => { limit, used }
  const [budgetMap, setBudgetMap] = useState<Record<number, { limit: number; used: number }>>({})
  // 当前选中组织与其下的用户
  const [selectedOrg, setSelectedOrg] = useState(0)
  const [orgUserList, setOrgUserList] = useState<Any[]>([])
  // 新建组织表单
  const [parentId, setParentId] = useState(0)
  const [newName, setNewName] = useState('')
  // 开通用户表单
  const [nu, setNu] = useState<Any>({ username: '', password: '', display_name: '', role: 'user' })
  const [nuOrgId, setNuOrgId] = useState(0)
  const [creating, setCreating] = useState(false)
  // ★ 批量导入（2026-09-02 功能）：Excel 导入用户（建号+首登强制改密+邮件通知）
  const importFileRef = useRef<HTMLInputElement>(null)
  const [importFile, setImportFile] = useState<File | null>(null)
  const [importing, setImporting] = useState(false)
  const [importResult, setImportResult] = useState<Any[] | null>(null)
  // 预算/邀请弹窗状态
  const [budgetModal, setBudgetModal] = useState<{ id: number; name: string; limit: number; used: number } | null>(null)
  const [budgetInput, setBudgetInput] = useState(0)
  const [inviteModal, setInviteModal] = useState<{ id: number; name: string } | null>(null)
  const [inviteItems, setInviteItems] = useState<Any[]>([])
  const [inviteCodeInput, setInviteCodeInput] = useState('')
  // 移动弹窗状态
  const [moveDlg, setMoveDlg] = useState<{ node: Any } | null>(null)
  const [moveParent, setMoveParent] = useState(0)

  const myLevel = ad.myLevel
  const isSuper = ad.isSuper
  // 「企业管理」内部双 Tab：组织结构 / 邀请员工（邀请码并入此处）
  const [tab, setTab] = useState<'org' | 'invite'>('org')

  /** 根组织显示名称：优先取后端返回的根组织，否则用当前租户名兜底 */
  const rootOrgName = useMemo(() => {
    if (rootOrg?.name) return rootOrg.name
    const tt = ad.tenants.find((x) => x.id === ad.activeTenantId)
    return tt?.name || tpl('org.orgHash', { id: ad.activeTenantId })
  }, [rootOrg, ad.tenants, ad.activeTenantId])

  /** 过滤后的组织列表（平台视图排除当前根；普通视图排除 root 类型） */
  const flatOrgs = useMemo(() => {
    if (isPlatformView) {
      return orgs.filter((o) => !(o.type === 'root' && rootOrg && o.id === rootOrg.id))
    }
    return orgs.filter((o) => o.type !== 'root')
  }, [orgs, isPlatformView, rootOrg])

  /** 组装扁平树（深度优先，记录层级 _depth） */
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

  /** 递归计算组织的完整路径名 */
  const orgPath = (o: OrgInfo): string => {
    if (o.parent_id === 0) return o.name
    const parent = flatOrgs.find((x) => x.id === o.parent_id)
    return parent ? `${orgPath(parent)} / ${o.name}` : o.name
  }

  /** 获取组织图标：部门→标签、组织→商场、根→办公楼 */
  const orgIcon = (o: OrgInfo): string => (o.type === 'dept' ? '🏷️' : o.type === 'org' ? '🏬' : '🏢')

  /** 获取预算文本显示（已用/限额） */
  const budgetText = (o: Any): string => {
    const b = budgetMap[o.id]
    if (!b || !(b.limit > 0)) return t('org.budgetUnset')
    return `${fmtNumShort(b.used)}/${fmtNumShort(b.limit)}`
  }

  /** 判断组织是否超预算 */
  const isOverBudget = (o: Any): boolean => {
    const b = budgetMap[o.id]
    return !!b && b.limit > 0 && b.used >= b.limit
  }

  /** 开通用户可选角色：根据所选组织类型与当前权限级联 */
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

  /** 组织选择变更时，若当前角色不在可用角色列表则回退 */
  function onNuOrgChange(v: any) {
    setNuOrgId(v)
    setNu((n: Any) => {
      if (!nuRoleOptions.includes(n.role)) return { ...n, role: nuRoleOptions[0] || 'user' }
      return n
    })
  }

  /** 用户字段更新：构造全量数据提交 /api/admin/users/update（对齐 Vue editUser） */
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

  /** 加载各部门 token 预算汇总 */
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

  /** 加载选中组织下的用户（不传则取当前用户可见全部） */
  async function loadOrgUsers() {
    const r: any = await orgUsers(selectedOrg || undefined)
    if (r.success) setOrgUserList(r.users || [])
  }

  /** 初始/刷新：组织列表 + 用户列表 */
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

  // 初始加载组织、用户、预算
  useEffect(() => { void loadAll(); void loadBudget() }, [loadAll])
  // activeTenantId 变化时重置选中并重载
  useEffect(() => {
    setSelectedOrg(0); setNuOrgId(0)
    void loadAll(); void loadBudget()
  }, [ad.activeTenantId])

  /** 选中组织并加载其用户 */
  function selectOrg(id: number) {
    setSelectedOrg(id); setNuOrgId(id); void loadOrgUsers()
  }

  /** 在选中组织下开通新用户 */
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

  /** 批量导入用户：选择 Excel 后上传（表头：用户名称、姓名、部门、角色、邮箱） */
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

  /** 弹窗重置组织用户密码 */
  async function resetPwd(u: Any) {    const pwd = await promptText({ header: t('org.resetPwdPrompt'), body: tpl('org.resetPwdPrompt', { name: u.username }) })
    if (!pwd || pwd.length < 6) { void MessagePlugin.warning(t('org.pwdMinLength')); return }
    const r: any = await adminUserResetPassword(u.id, pwd)
    if (!r.success) { void MessagePlugin.error(r.message); return }
    void MessagePlugin.success(t('org.pwdReset'))
  }

  /** 启用/禁用组织用户 */
  async function setStatus(u: Any, status: string) {
    if (await updateUser(u, { status })) await loadAll()
  }

  /** 删除组织用户 */
  async function deleteUser(u: Any) {
    if (!(await confirmDialog({ body: tpl('org.deleteUserConfirm', { name: u.username }) }))) return
    const r: any = await adminUserDelete(u.id)
    if (!r.success) { void MessagePlugin.error(r.message); return }
    await loadAll()
  }

  /** 行内编辑用户字段（display_name / org_id / role） */
  async function editUser(u: Any, field: string, val: string) {
    const v = field === 'org_id' ? Number(val) : val
    if (field === 'display_name' && !String(val).trim()) return
    if (await updateUser(u, { [field]: v })) await loadAll()
  }

  /** 超管启停租户根（联动租户状态） */
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

  /** 设置新建组织的父节点并清空名称输入 */
  function setParent(id: number) { setParentId(id); setNewName('') }

  /** 创建组织或部门（parent_id=0 为组织，否则为部门） */
  async function createOrg() {
    if (!newName.trim()) { void MessagePlugin.warning(t('org.nameRequired')); return }
    const r: any = await orgCreate({ name: newName.trim(), parent_id: parentId, type: parentId === 0 ? 'org' : 'dept' })
    if (!r.success) { void MessagePlugin.error(r.message); return }
    setNewName(''); await loadAll()
  }

  /** 重命名组织；若重命名根组织且为超管，同步刷新租户列表 */
  async function renameOrg(o: OrgInfo) {
    const name = await promptText({ header: t('org.renamePrompt'), body: tpl('org.renamePrompt', { name: o.name }), defaultValue: o.name })
    if (!name || !name.trim()) return
    const r: any = await orgRename(o.id, name.trim())
    if (!r.success) { void MessagePlugin.error(r.message); return }
    if (isSuper && o.type === 'root') ad.loadTenants()
    await loadAll()
  }

  /** 重命名当前租户根组织 */
  async function renameRootOrg() {
    if (!rootOrg) return
    const name = await promptText({ header: t('org.renameRoot'), body: t('org.renameRoot'), defaultValue: rootOrg.name })
    if (!name || !name.trim()) return
    const r: any = await orgRename(rootOrg.id, name.trim())
    if (!r.success) { void MessagePlugin.error(r.message); return }
    if (isSuper) ad.loadTenants()
    await loadAll()
  }

  /** 删除组织；若当前正选中该组织则重置 */
  async function deleteOrg(o: OrgInfo) {
    if (!(await confirmDialog({ body: tpl('org.deleteConfirm', { name: o.name }) }))) return
    const r: any = await orgDelete(o.id)
    if (!r.success) { void MessagePlugin.error(r.message); return }
    if (selectedOrg === o.id) setSelectedOrg(0)
    await loadAll()
  }

  /** 打开预算弹窗并预填当前限制 */
  function openBudget(o: Any) {
    const b = budgetMap[o.id]
    setBudgetModal({ id: o.id, name: o.name, limit: b?.limit || 0, used: b?.used || 0 })
    setBudgetInput(b?.limit || 0)
  }

  /** 保存预算设置 */
  async function saveBudget() {
    if (!budgetModal) return
    if (!(budgetInput >= 0)) { void MessagePlugin.warning(t('org.budgetInvalid')); return }
    const r: any = await orgTokenLimit(budgetModal.id, Math.floor(budgetInput))
    if (!r.success) { void MessagePlugin.error(r.message); return }
    setBudgetMap((m) => ({ ...m, [budgetModal.id]: { limit: budgetInput, used: budgetModal.used } }))
    setBudgetModal(null)
  }

  /** 打开邀请码弹窗并加载该组织的邀请码 */
  async function openInvites(o: Any) {
    setInviteModal({ id: o.id, name: o.name })
    setInviteItems([])
    try {
      const r: any = await inviteCodes()
      if (r.success) setInviteItems((r.codes || []).filter((x: Any) => x.org_id === o.id))
    } catch { setInviteItems([]) }
  }

  /** 创建邀请码 */
  async function createInvite() {
    if (!inviteModal) return
    const code = inviteCodeInput.trim()
    if (!code) { void MessagePlugin.warning(t('org.inviteNeedCode')); return }
    const r: any = await inviteCodeCreate({ code, tenant_id: inviteModal.id, org_id: inviteModal.id })
    if (!r.success) { void MessagePlugin.error(r.message); return }
    setInviteCodeInput('')
    await openInvites(inviteModal)
  }

  /** 打开移动弹窗，默认目标为当前父节点 */
  function openMove(o: Any) { setMoveParent(Number(o.parent_id ?? 0)); setMoveDlg({ node: o }) }

  /** 提交组织移动 */
  async function doMove() {
    if (!moveDlg) return
    const r: any = await orgMove(moveDlg.node.id, moveParent)
    if (toastResp(r, '已移动')) { setMoveDlg(null); await loadAll() }
  }

  // 用于添加用户时的组织标题显示
  const addUserHeading = nuOrgId === 0
    ? (isPlatformView ? t('admin.platformRoot') : rootOrgName)
    : (flatOrgs.find((x) => x.id === nuOrgId)?.name || '')

  // 组织选择下拉选项
  const orgSelectOptions = [
    { label: isPlatformView ? t('admin.platformRoot') : t('org.rootOption'), value: 0 },
    ...flatTree.map((o) => ({ label: orgPath(o), value: o.id })),
  ]
  // 用户组织选择下拉选项
  const userOrgOptions = [
    { label: rootOrgName, value: 0 },
    ...flatTree.map((o) => ({ label: orgPath(o), value: o.id })),
  ]

  return (
    <Tabs value={tab} onChange={(v) => setTab(v as 'org' | 'invite')}>
      {/* 组织结构 Tab */}
      <Tabs.TabPanel value="org" label={t('org.tabOrg')}>
        <Panel title={t('org.title')}>
      <p style={{ fontSize: 12, color: '#888', margin: '0 0 12px' }}>{t('org.treeHint')}</p>

      {/* 新建组织/部门区域（仅租户管理员及以上可见） */}
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
        {/* 组织树（左侧） */}
        <div style={{ minWidth: 280, flex: '1 1 320px' }}>
          {/* 根组织节点 */}
          <div
            onClick={() => selectOrg(0)}
            style={{ padding: '8px 10px', borderRadius: 8, cursor: 'pointer', background: selectedOrg === 0 ? '#e8f3ff' : '#f5f7fa', display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}
          >
            <span>🏢 {rootOrgName}（{isPlatformView || isSuper ? t('org.typePlatform') : t('org.typeRoot')}）</span>
            {myLevel >= 3 && (
              <Button size="small" variant="text" title={t('org.renameRoot')} onClick={(e) => { e.stopPropagation(); renameRootOrg() }}>✎</Button>
            )}
          </div>
          {/* 子组织列表（扁平树，按深度缩进） */}
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
                {/* 预算按钮（非根组织且租户管理员及以上可见） */}
                {o.type !== 'root' && myLevel >= 3 && (
                  <span className={isOverBudget(o) ? 'budget-over' : ''}
                        title={t('org.budgetSet')}
                        onClick={(e) => { e.stopPropagation(); openBudget(o) }}
                        style={{ cursor: 'pointer', fontSize: 12, color: isOverBudget(o) ? '#c62828' : '#667', border: `1px solid ${isOverBudget(o) ? '#c62828' : '#d0d7de'}`, borderRadius: 8, padding: '0 6px' }}>
                    💰 {budgetText(o)}
                  </span>
                )}
                {/* 邀请码按钮（非根组织且租户管理员及以上可见） */}
                {o.type !== 'root' && myLevel >= 3 && (
                  <Button size="small" variant="text" title={t('org.inviteEntry')} onClick={(e) => { e.stopPropagation(); openInvites(o) }}>🎟️</Button>
                )}
                {/* 超管启停租户根（仅根组织显示） */}
                {isSuper && o.type === 'root' && (
                  <Button size="small" variant="text"
                          onClick={(e) => { e.stopPropagation(); toggleTenantByOrg(o) }}>
                    {tenantStatusOf(o.tenant_id) === 'active' ? t('tenants.disable') : t('tenants.enable')}
                  </Button>
                )}
                {/* 添加子节点按钮 */}
                <Button size="small" variant="text" title={t('org.addChild')} onClick={(e) => { e.stopPropagation(); setParent(o.id) }}>+</Button>
                {/* 重命名按钮（租户管理员及以上可见） */}
                {myLevel >= 3 && (
                  <Button size="small" variant="text" title={t('org.rename')} onClick={(e) => { e.stopPropagation(); renameOrg(o) }}>✎</Button>
                )}
                {/* 移动与删除按钮（非根组织且租户管理员及以上可见） */}
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

        {/* 组织下用户列表（含子孙归集，右侧） */}
        <div style={{ flex: '2 1 480px', minWidth: 360 }}>
          <h3 style={{ margin: '0 0 4px' }}>
            {selectedOrg === 0 ? tpl('org.allUsersRootTpl', { name: rootOrgName }) : tpl('org.usersInChildren', { name: flatOrgs.find((x) => x.id === selectedOrg)?.name || '' })}
          </h3>
          <p style={{ fontSize: 12, color: '#888', margin: '0 0 12px' }}>{t('org.usersHint')}</p>
          {/* 用户列表表格 */}
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

          {/* 开通用户表单 */}
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
              {/* ★ 批量导入（2026-09-02 功能）：仅租户管理员及以上 */}
              {myLevel >= 3 && (
                <>
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

          {/* 批量导入结果弹窗 */}
          <Dialog visible={!!importResult} onClose={() => setImportResult(null)}
                  header={`📥 ${tpl('org.importResultTitle', { total: importResult?.length || 0 })}`} width={520}>
            <Table rowKey="username" size="small" maxHeight={360} data={importResult || []}
                   columns={[
                     { colKey: 'username', title: t('org.importResultCol') },
                     { colKey: 'ok', title: t('org.importResultStatus'), width: 70, cell: ({ row }: any) => row.ok ? <Tag theme="success">{t('org.importResultOk')}</Tag> : <Tag theme="danger">{t('org.importResultFail')}</Tag> },
                     { colKey: 'message', title: '', cell: ({ row }: any) => <span style={{ fontSize: 12, color: row.ok ? '#666' : '#c62828' }}>{row.message}</span> },
                   ] as never} />
          </Dialog>

          {/* 预算弹窗 */}
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

      {/* 邀请码弹窗 */}
      <Dialog visible={!!inviteModal} onClose={() => setInviteModal(null)}
              header={`🎟️ ${t('org.inviteTitle')} · ${inviteModal?.name || ''}`} width={440}>
        {inviteModal && (
          <>
            <p style={{ fontSize: 12, color: '#888', margin: '0 0 10px' }}>{t('org.inviteHint')}</p>
            <div style={{ display: 'flex', gap: 8, marginBottom: 8 }}>
              <Input value={inviteCodeInput} placeholder={t('org.inviteInputPlaceholder')} onChange={(v) => setInviteCodeInput(v)} style={{ flex: 1 }} />
              <Button theme="primary" disabled={!inviteCodeInput.trim()} onClick={createInvite}>➕ {t('org.inviteCreate')}</Button>
            </div>
            {/* 已有邀请码列表 */}
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

      {/* 移动弹窗 */}
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
      {/* 邀请员工 Tab（复用 InvitesP 组件） */}
      <Tabs.TabPanel value="invite" label={t('org.tabInvite')}>
        <InvitesP />
      </Tabs.TabPanel>
    </Tabs>
  )
}
