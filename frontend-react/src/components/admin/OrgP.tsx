// ============================================================================
// components/admin/OrgP.tsx — 组织架构面板
// 职责：组织树展示、组织 CRUD、用户管理、预算设置、邀请码、组织移动
// 从 panels_b.tsx 拆分
// 2026-09-18（UI 融合）：树节点/弹窗标题的 emoji 前缀清理（orgIcon 暂退化为空串），
//   若干图形按钮改由 title 表意；权限收敛（L3 起可维护组织与预算、超管独占租户启停）未变。
// ============================================================================
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Button, DataTable, Dialog, Icon, Link, StatusPill, Tabs } from '@/ui/langcross/src'
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
import { fmtPoints } from '@/utils/points' // ★ S1 部门预算积分口径（接口出入参即积分）
import { fmtTime } from '@/lib/ui'
import { useAdmin } from '@/stores/admin'
import { InvitesP, UsersP } from './panels_a' // ★ Tab 精简（2026-09-15）：成员账户并入组织 Hub 子 tab；租户管理移至「计费与套餐」Hub
import { t, tpl } from '@/i18n'
import { toastSuccess, toastError, toastWarn } from '@/lib/toastBus'

/** Any 组织管理出参宽松别名 */
type Any = any

/** 组织架构面板（超管/租户管理员/部门管理员三级视角）：
 * 组织树 CRUD、成员表格行内编辑、部门预算（积分口径）、邀请码、组织移动、批量导入 */
export function OrgP() {
  const ad = useAdmin()
  // ===== 面板状态：组织树、选中节点、成员列表/分页、预算、批量导入 =====
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
  const [tab, setTab] = useState<'org' | 'invite' | 'users'>('org') // ★ Tab 精简（2026-09-15）：成员并入组织 Hub

  // 根组织/平台根名：优先根组织名，回退当前激活租户名
  const rootOrgName = useMemo(() => {
    if (rootOrg?.name) return rootOrg.name
    const tt = ad.tenants.find((x) => x.id === ad.activeTenantId)
    return tt?.name || tpl('org.orgHash', { id: ad.activeTenantId })
  }, [rootOrg, ad.tenants, ad.activeTenantId])

  // 平铺组织列表：平台视图剔除所属根组织外的 root 节点，租户视图剔除 root
  const flatOrgs = useMemo(() => {
    if (isPlatformView) return orgs.filter((o) => !(o.type === 'root' && rootOrg && o.id === rootOrg.id))
    return orgs.filter((o) => o.type !== 'root')
  }, [orgs, isPlatformView, rootOrg])

  // 深度优先展开为带层级的线性列表（渲染缩进用）
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

  // 递归拼组织全路径名（父 / 子），供下拉选项展示
  const orgPath = (o: OrgInfo): string => {
    if (o.parent_id === 0) return o.name
    const parent = flatOrgs.find((x) => x.id === o.parent_id)
    return parent ? `${orgPath(parent)} / ${o.name}` : o.name
  }

  // 节点类型图标：dept 部门 / org 组织 / root 根
  // 2026-09-18：三类 emoji 图标随 emoji 清理被置空，函数退化为恒返回空串；
  //   保留签名与调用点（树节点前缀位）不改，后续接 ui/langcross <Icon> 时在此按 type 分发。
 const orgIcon = (o: OrgInfo): string => (o.type ==='dept'?'': o.type ==='org'?'':'')

  // 预算文案：本月已用/限额（积分口径，接口出口即积分）；未设置显示占位
  const budgetText = (o: Any): string => {
    const b = budgetMap[o.id]
    if (!b || !(b.limit > 0)) return t('org.budgetUnset')
    return `${fmtPoints(b.used)}/${fmtPoints(b.limit)}`
  }

  // 是否超预算（已用 ≥ 限额），命中行标红
  const isOverBudget = (o: Any): boolean => {
    const b = budgetMap[o.id]
    return !!b && b.limit > 0 && b.used >= b.limit
  }

  // 新建用户的角色级联：随所选组织层级与当前登录者权限收敛可选项
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

  // 切换归属组织：若当前角色不在新可选集内则自动落到首个合法角色
  function onNuOrgChange(v: any) {
    setNuOrgId(v)
    setNu((n: Any) => {
      if (!nuRoleOptions.includes(n.role)) return { ...n, role: nuRoleOptions[0] || 'user' }
      return n
    })
  }

  // 用户更新统一出口（PUT users/update）：失败弹错误并返回 false 阻断后续刷新
  async function updateUser(u: Any, patch: Any): Promise<boolean> {
    const data: Any = {
      display_name: u.display_name, role: u.role, status: u.status,
      org_id: Number(u.org_id || 0),
    }
    Object.assign(data, patch)
    const r: any = await request('/api/admin/users/update', {
      method: 'POST', headers: authHeaders(), body: JSON.stringify({ id: u.id, ...data }),
    })
    if (!r.success) { toastError(r.message); return false }
    return true
  }

  // 拉取部门预算汇总（限额/本月已用），非租管无权限时静默跳过
  async function loadBudget() {
    try {
      const r: any = await orgBudgetSummary()
      if (r.success) {
        const m: Record<number, { limit: number; used: number }> = {}
        for (const d of r.summary?.depts || []) m[d.org_id] = { limit: d.limit_points, used: d.used_points }
        setBudgetMap(m)
      }
    } catch { /* 非租管静默 */ }
  }

  // 拉取选中组织（含下级）成员列表
  async function loadOrgUsers() {
    const r: any = await orgUsers(selectedOrg || undefined)
    if (r.success) setOrgUserList(r.users || [])
  }

  // loadAll 组织面板整体刷新：组织树 + 成员列表并行拉取（含平台视图标记）
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

  // 选中组织节点：同步新建用户的默认归属组织
  function selectOrg(id: number) {
    setSelectedOrg(id); setNuOrgId(id); void loadOrgUsers()
  }

  // 建号：用户名/密码强度预检 → adminUserCreate → 成功后清空表单并整体刷新
  async function createUser() {
    if (!nu.username?.trim() || (nu.password?.length ?? 0) < 6) { void toastWarn(t('org.userValidation')); return }
    setCreating(true)
    try {
      const r: any = await adminUserCreate({
        username: nu.username.trim(),
        password: nu.password,
        display_name: nu.display_name?.trim() || nu.username.trim(),
        role: nu.role,
        org_id: nuOrgId || undefined,
      })
      if (!r.success) { toastError(r.message); return }
      toastSuccess(tpl('org.userCreated', { name: nu.username }))
      setNu({ username: '', password: '', display_name: '', role: 'user' })
      await loadAll()
    } catch (e) { // ★ E10：创建用户网络错误可见（旧实现 try/finally 静默）
      toastError(e instanceof Error ? e.message : '创建失败')
    } finally { setCreating(false) }
  }

  // Excel/CSV 批量导入成员：结果逐行回显（成功/失败原因）
  async function doBulkImport() {
    if (!importFile) { void toastWarn(t('org.importNeedFile')); return }
    setImporting(true)
    try {
      const r: any = await userBulkImport(importFile)
      if (!r.success) { toastError(r.message); return }
      setImportResult(r.results || [])
      toastSuccess(tpl('org.importDone', { ok: r.created || 0, fail: r.failed || 0 }))
      setImportFile(null)
      await loadAll()
    } catch (e) { // ★ E10
      toastError(e instanceof Error ? e.message : '导入失败')
    } finally { setImporting(false) }
  }

  // 下载批量导入模板（列头与后端解析对齐）
  async function downloadTemplate() {
    const ok = await downloadUserImportTemplate()
    if (!ok) toastError(t('org.importTplFail'))
  }

  // 管理员重置成员密码（弹窗输入，≥6 位）
  async function resetPwd(u: Any) {
    const pwd = await promptText({ header: t('org.resetPwdPrompt'), body: tpl('org.resetPwdPrompt', { name: u.username }) })
    if (!pwd || pwd.length < 6) { void toastWarn(t('org.pwdMinLength')); return }
    const r: any = await adminUserResetPassword(u.id, pwd)
    if (!r.success) { toastError(r.message); return }
    toastSuccess(t('org.pwdReset'))
  }

  // 启用/停用成员账号
  async function setStatus(u: Any, status: string) {
    if (await updateUser(u, { status })) await loadAll()
  }

  // 删除成员（二次确认）
  async function deleteUser(u: Any) {
    if (!(await confirmDialog({ body: tpl('org.deleteUserConfirm', { name: u.username }) }))) return
    const r: any = await adminUserDelete(u.id)
    if (!r.success) { toastError(r.message); return }
    await loadAll()
  }

  // 成员表格行内编辑：显示名/组织/角色单字段更新
  async function editUser(u: Any, field: string, val: string) {
    const v = field === 'org_id' ? Number(val) : val
    if (field === 'display_name' && !String(val).trim()) return
    if (await updateUser(u, { [field]: v })) await loadAll()
  }

  // 按 org 查其租户当前状态（平台视图启停按钮用）
  function tenantStatusOf(tid: number): string {
    return ad.tenants.find((x) => x.id === tid)?.status || 'active'
  }

  // 平台视图整租户启停：确认后 tenantSetStatus 并刷新租户缓存
  async function toggleTenantByOrg(o: OrgInfo) {
    const cur = tenantStatusOf(o.tenant_id)
    const next = cur === 'active' ? 'disabled' : 'active'
    const key = next === 'disabled' ? 'org.disableTenantConfirm' : 'org.enableTenantConfirm'
    if (!(await confirmDialog({ body: tpl(key, { name: o.name }) }))) return
    const r: any = await tenantSetStatus(o.tenant_id, next)
    if (!r.success) { toastError(r.message); return }
    ad.loadTenants(); await loadAll()
  }

  // 进入"新建子组织"模式：预选父节点并清空名称输入
  function setParent(id: number) { setParentId(id); setNewName('') }

  // 新建组织/部门：父为 0 建 org，否则建 dept（后端校验名称非空）
  async function createOrg() {
    if (!newName.trim()) { void toastWarn(t('org.nameRequired')); return }
    const r: any = await orgCreate({ name: newName.trim(), parent_id: parentId, type: parentId === 0 ? 'org' : 'dept' })
    if (!r.success) { toastError(r.message); return }
    setNewName(''); await loadAll()
  }

  // 重命名组织；根组织改名同步刷新超管租户缓存
  async function renameOrg(o: OrgInfo) {
    const name = await promptText({ header: t('org.renamePrompt'), body: tpl('org.renamePrompt', { name: o.name }), defaultValue: o.name })
    if (!name || !name.trim()) return
    const r: any = await orgRename(o.id, name.trim())
    if (!r.success) { toastError(r.message); return }
    if (isSuper && o.type === 'root') ad.loadTenants()
    await loadAll()
  }

  // 重命名根组织（=租户显示名）
  async function renameRootOrg() {
    if (!rootOrg) return
    const name = await promptText({ header: t('org.renameRoot'), body: t('org.renameRoot'), defaultValue: rootOrg.name })
    if (!name || !name.trim()) return
    const r: any = await orgRename(rootOrg.id, name.trim())
    if (!r.success) { toastError(r.message); return }
    if (isSuper) ad.loadTenants()
    await loadAll()
  }

  // 删除组织（后端守卫：有子节点/成员时拒绝），当前选中则回落到根
  async function deleteOrg(o: OrgInfo) {
    if (!(await confirmDialog({ body: tpl('org.deleteConfirm', { name: o.name }) }))) return
    const r: any = await orgDelete(o.id)
    if (!r.success) { toastError(r.message); return }
    if (selectedOrg === o.id) setSelectedOrg(0)
    await loadAll()
  }

  // 打开部门预算弹窗：限额即积分，直接回填
  function openBudget(o: Any) {
    const b = budgetMap[o.id]
    setBudgetModal({ id: o.id, name: o.name, limit: b?.limit || 0, used: b?.used || 0 })
    setBudgetInput(Number(b?.limit) || 0)
  }

  // 保存部门月预算（积分口径，接口出入参同为积分），本地预算表同步回填
  async function saveBudget() {
    if (!budgetModal) return
    if (!(budgetInput >= 0)) { void toastWarn(t('org.budgetInvalid')); return }
    const limitPoints = Math.floor(budgetInput)
    const r: any = await orgTokenLimit(budgetModal.id, limitPoints)
    if (!r.success) { toastError(r.message); return }
    setBudgetMap((m) => ({ ...m, [budgetModal.id]: { limit: limitPoints, used: budgetModal.used } }))
    setBudgetModal(null)
  }

  // 打开组织邀请码弹窗并拉取该组织的码列表
  async function openInvites(o: Any) {
    setInviteModal({ id: o.id, name: o.name })
    setInviteItems([])
    try {
      const r: any = await inviteCodes()
      if (r.success) setInviteItems((r.codes || []).filter((x: Any) => x.org_id === o.id))
    } catch { setInviteItems([]) }
  }

  // 新建邀请码（绑定当前组织）
  async function createInvite() {
    if (!inviteModal) return
    const code = inviteCodeInput.trim()
    if (!code) { void toastWarn(t('org.inviteNeedCode')); return }
    const r: any = await inviteCodeCreate({ code, tenant_id: inviteModal.id, org_id: inviteModal.id })
    if (!r.success) { toastError(r.message); return }
    setInviteCodeInput('')
    await openInvites(inviteModal)
  }

  // 打开移动组织对话框：回填当前父节点
  function openMove(o: Any) { setMoveParent(Number(o.parent_id ?? 0)); setMoveDlg({ node: o }) }

  // 执行组织移动（后端防环校验），成功后关闭并刷新
  async function doMove() {
    if (!moveDlg) return
    const r: any = await orgMove(moveDlg.node.id, moveParent)
    if (toastResp(r, '已移动')) { setMoveDlg(null); await loadAll() }
  }

  // 建号区标题：当前归属组织名（0=根/平台）
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
    <>
    <Tabs activeKey={tab} onChange={(k) => setTab(k as 'org' | 'invite')} items={[
      { key: 'org', label: t('org.tabOrg') },
      { key: 'invite', label: t('org.tabInvite') },
      { key: 'users', label: t('hub.tabUsers') },
    ]} />
      {/* Tab 面板 */}
      {tab === 'org' && (<>
        <Panel title={t('org.title')}>
      <p style={{ fontSize: 12, color: 'var(--adm-faint)', margin: '0 0 12px' }}>{t('org.treeHint')}</p>

      {myLevel >= 3 && (
        <div style={{ display: 'flex', gap: 8, alignItems: 'center', marginBottom: 12, flexWrap: 'wrap' }}>
          <select className="lc-select" value={String(parentId)} onChange={(e) => setParentId(Number(e.target.value))} style={{ minWidth: 200 }}>
            {myLevel >= 3 && <option value="0">{tpl('org.rootOptionTpl', { name: rootOrgName })}</option>}
            {flatTree.map((o) => <option key={o.id} value={String(o.id)}>{orgPath(o)}</option>)}
          </select>
          <input className="lc-input" value={newName} placeholder={t('org.namePlaceholder')}
                 onChange={(e) => setNewName(e.target.value)} onKeyDown={(e) => { if (e.key === 'Enter') createOrg() }} style={{ flex: 1, minWidth: 160 }} />
          <Button variant="primary" onClick={createOrg}>{parentId === 0 ? t('org.create') : t('org.createDept')}</Button>
        </div>
      )}

      <div style={{ display: 'flex', gap: 16, alignItems: 'flex-start', flexWrap: 'wrap' }}>
        <div style={{ minWidth: 280, flex: '1 1 320px' }}>
          <div
            onClick={() => selectOrg(0)}
            style={{ padding: '8px 10px', borderRadius: 8, cursor: 'pointer', background: selectedOrg === 0 ? 'var(--lc-raised, #16181C)' : 'var(--lc-inset, #0E1014)', border: '1.2px solid var(--lc-border-card)', display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}
          >
            <span> {rootOrgName}（{isPlatformView || isSuper ? t('org.typePlatform') : t('org.typeRoot')}）</span>
            {myLevel >= 3 && (
 <Button size="sm" variant="secondary" title={t('org.renameRoot')} aria-label={t('org.renameRoot')} onClick={(e) => { e.stopPropagation(); renameRootOrg() }}><Icon n="pencil" /></Button>
            )}
          </div>
          {flatTree.map((o) => (
            <div
              key={o.id}
              onClick={() => selectOrg(o.id)}
              style={{ padding: `8px 10px 8px ${8 + o._depth * 18}px`, borderRadius: 8, cursor: 'pointer', margin: '4px 0', background: selectedOrg === o.id ? 'var(--adm-info-bg)' : 'var(--adm-card)', border: '1.2px solid var(--adm-line)', display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: 6, flexWrap: 'wrap' }}
            >
              <span>
                <span style={{ opacity: 0.6, marginRight: 4 }}>⠿</span>
                {orgIcon(o)} {o.name}
              </span>
              {/* 行内操作区（权限逐级收敛）：
                  预算芯片/邀请/重命名/删除 = 租管及以上（myLevel>=3）；
                  启停租户 = 仅超管且节点为租户根（isSuper && type==='root'）；
                  新增子部门 = 任何能看到本面板的角色。
                  stopPropagation 必加：按钮嵌在「点击选中组织」的节点里，否则会连带切换选中项。
                  注：重命名/邀请/删除三枚按钮原为 ✎/🎟️/✕ 图形字符，emoji 清理后已无可视内容，
                  目前只靠 title 悬浮提示，待接 <Icon> 恢复可见性。 */}
              <span style={{ display: 'flex', alignItems: 'center', gap: 4, flexWrap: 'wrap' }}>
                {o.type !== 'root' && myLevel >= 3 && (
                  <span className={isOverBudget(o) ? 'budget-over' : ''}
                        title={t('org.budgetSet')}
                        onClick={(e) => { e.stopPropagation(); openBudget(o) }}
                        style={{ cursor: 'pointer', fontSize: 12, color: isOverBudget(o) ? 'var(--lc-danger)' : 'var(--lc-text-3)', border: `1.2px solid ${isOverBudget(o) ? 'var(--lc-danger)' : 'var(--lc-border-card)'}`, borderRadius: 8, padding: '0 6px' }}>
                     {budgetText(o)}
                  </span>
                )}
                {o.type !== 'root' && myLevel >= 3 && (
 <Button size="sm" variant="secondary" title={t('org.inviteEntry')} aria-label={t('org.inviteEntry')} onClick={(e) => { e.stopPropagation(); openInvites(o) }}><Icon n="key" /></Button>
                )}
                {isSuper && o.type === 'root' && (
                  <Button size="sm" variant="secondary"
                          onClick={(e) => { e.stopPropagation(); toggleTenantByOrg(o) }}>
                    {tenantStatusOf(o.tenant_id) === 'active' ? t('tenants.disable') : t('tenants.enable')}
                  </Button>
                )}
                <Button size="sm" variant="secondary" title={t('org.addChild')} aria-label={t('org.addChild')} onClick={(e) => { e.stopPropagation(); setParent(o.id) }}><Icon n="plus" /></Button>
                {myLevel >= 3 && (
 <Button size="sm" variant="secondary" title={t('org.rename')} aria-label={t('org.rename')} onClick={(e) => { e.stopPropagation(); renameOrg(o) }}><Icon n="pencil" /></Button>
                )}
                {o.type !== 'root' && myLevel >= 3 && (
                  <>
                    <Button size="sm" variant="secondary" title={t('org.move')} aria-label={t('org.move')} onClick={(e) => { e.stopPropagation(); openMove(o) }}><Icon n="swap" /></Button>
 <Button size="sm" variant="danger" title={t('org.delete')} aria-label={t('org.delete')} onClick={(e) => { e.stopPropagation(); deleteOrg(o) }}><Icon n="trash" /></Button>
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
          <p style={{ fontSize: 12, color: 'var(--adm-faint)', margin: '0 0 12px' }}>{t('org.usersHint')}</p>
          {/* 数据表格 */}
          <DataTable<any> rowKey={(row) => String(row.id)} rows={orgUserList}
                 columns={[
                   { key: 'id', title: t('org.colId'), width: 60 },
                   { key: 'username', title: t('org.colUsername'), width: 110 },
                   { key: 'display_name', title: t('org.colName'), render: (row) => (
                     <input className="lc-input" value={String(row.display_name ?? '')}
                            onChange={(e) => editUser(row, 'display_name', e.target.value)} />
                   ) },
                   { key: 'org', title: t('org.colOrg'), width: 160, render: (row) => (
                     <select className="lc-select" value={String(Number(row.org_id || 0))} onChange={(e) => editUser(row, 'org_id', e.target.value)}>
                       {(userOrgOptions as Array<{ id: number; name: string } | { label: string; value: number }>).map((o, oi) => {
                         const val = 'id' in o ? String(o.id) : String(o.value)
                         const lab = 'name' in o ? o.name : o.label
                         return <option key={oi} value={val}>{lab}</option>
                       })}
                     </select>
                   ) },
                   { key: 'role', title: t('org.colRole'), width: 130, render: (row) => (
                     <select className="lc-select" value={String(row.role)} onChange={(e) => editUser(row, 'role', e.target.value)}>
                       {ad.roleOptions.map((r: string) => <option key={r} value={r}>{t('users.role.' + r)}</option>)}
                     </select>
                   ) },
                   { key: 'last_login_at', title: t('org.colLastLogin'), width: 140, render: (row) => fmtTime(row.last_login_at as string) },
                   { key: 'op', title: t('org.colActions'), width: 200, render: (row) => (
                     <div style={{ display: 'flex', gap: 10, alignItems: 'center', flexWrap: 'wrap' }}>
                       <Link onClick={() => resetPwd(row)}>{t('org.resetPwd')}</Link>
                       {row.status === 'disabled'
                         ? <Link onClick={() => setStatus(row, 'active')}>{t('org.enable')}</Link>
                         : <Link tone="danger" onClick={() => setStatus(row, 'disabled')}>{t('org.disable')}</Link>}
                       <Link tone="danger" onClick={async () => { if (!(await confirmDialog({ body: t('common.delete') }))) return; deleteUser(row) }}>{t('common.delete')}</Link>
                     </div>
                   ) },
                  ]} />
          {!orgUserList.length && <div style={{ fontSize: 12, color: 'var(--adm-faint)', padding: 8 }}>{t('org.noUsers')}</div>}

          <div style={{ marginTop: 16, border: '1.2px solid var(--adm-line)', borderRadius: 8, padding: 14 }}>
            <h3 style={{ margin: '0 0 10px' }}>{tpl('org.addUser', { org: addUserHeading })}</h3>
            <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', marginBottom: 8 }}>
              <input className="lc-input" value={String(nu.username || '')} placeholder={t('org.usernamePlaceholder')} onChange={(e) => setNu((n: Any) => ({ ...n, username: e.target.value }))} style={{ flex: 1, minWidth: 140 }} />
              <input className="lc-input" type="password" autoComplete="new-password" value={String(nu.password || '')} placeholder={t('org.passPlaceholder')} onChange={(e) => setNu((n: Any) => ({ ...n, password: e.target.value }))} style={{ flex: 1, minWidth: 140 }} />
              <input className="lc-input" value={String(nu.display_name || '')} placeholder={t('org.displayNamePlaceholder')} onChange={(e) => setNu((n: Any) => ({ ...n, display_name: e.target.value }))} style={{ flex: 1, minWidth: 140 }} />
            </div>
            <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', alignItems: 'center' }}>
              <span style={{ fontSize: 12, color: 'var(--adm-hint)' }}>{t('org.orgLabel')}</span>
              <select className="lc-select" value={String(nuOrgId)} onChange={(e) => onNuOrgChange(e.target.value)} style={{ flex: 1, minWidth: 160 }}>
                {(orgSelectOptions as Array<{ label: string; value: number }>).map((o) => <option key={o.value} value={String(o.value)}>{o.label}</option>)}
              </select>
              <span style={{ fontSize: 12, color: 'var(--adm-hint)' }}>{t('org.roleLabel')}</span>
              <select className="lc-select" value={String(nu.role)} onChange={(e) => setNu((n: Any) => ({ ...n, role: e.target.value }))}>
                {nuRoleOptions.map((r: string) => <option key={r} value={r}>{t('users.role.' + r)}</option>)}
              </select>
              <span style={{ fontSize: 12, color: 'var(--adm-faint)', flex: 1, minWidth: 120 }}>{t('org.cascadeHint')}</span>
              <Button variant="primary" disabled={creating} onClick={createUser}>{creating ? t('org.creating') : t('org.addUserBtn')}</Button>
              {myLevel >= 3 && (
                <>
                  <Button variant="secondary" onClick={() => void downloadTemplate()}
                          title={t('org.importTplHint')}> {t('org.importTplBtn')}</Button>
                  <input ref={importFileRef} type="file" hidden accept=".xlsx,.xls,.csv"
                         onChange={(e) => { setImportFile(e.target.files?.[0] || null); e.target.value = '' }} />
                  <Button variant="secondary" disabled={importing} onClick={() => importFileRef.current?.click()}
                          title={t('org.importHint')}>{importing ? t('org.importing') : t('org.importBtn')}</Button>
                  <Button variant="secondary" disabled={importing || !importFile} onClick={doBulkImport}
                          title={importFile ? importFile.name : ''}>↗</Button>
                </>
              )}
            </div>
          </div>
        </div>
      </div>

          <Dialog open={!!importResult} onCancel={() => setImportResult(null)}
                  title={`${tpl('org.importResultTitle', { total: importResult?.length || 0 })}`}>
            {/* 数据表格 */}
            <DataTable<any> rowKey={(row) => String(row.username)} rows={importResult || []}
                   columns={[
                     { key: 'username', title: t('org.importResultCol') },
                     { key: 'ok', title: t('org.importResultStatus'), width: 70, render: (row) => row.ok ? <StatusPill tone="success">{t('org.importResultOk')}</StatusPill> : <StatusPill tone="danger">{t('org.importResultFail')}</StatusPill> },
                     { key: 'message', title: '', render: (row) => <span style={{ fontSize: 12, color: row.ok ? 'var(--lc-text-3)' : 'var(--lc-danger)' }}>{row.message}</span> },
                  ]} />
          </Dialog>

      <Dialog open={!!budgetModal} onCancel={() => setBudgetModal(null)}
              title={`${t('org.budgetTitle')} · ${budgetModal?.name || ''}`}
              onConfirm={saveBudget}>
        {budgetModal && (
          <>
            <p style={{ fontSize: 12, color: 'var(--adm-faint)', margin: '0 0 10px' }}>{tpl('org.budgetHint', { used: fmtPoints(budgetModal.used) })}</p>
            <label style={{ display: 'block', marginBottom: 4, fontSize: 12, color: 'var(--adm-hint)' }}>{t('org.budgetLimit')}</label>
            <input className="lc-input" type="number" value={num(budgetInput)} placeholder={t('org.budgetPlaceholder')} onChange={(e) => setBudgetInput(Number(e.target.value))} />
          </>
        )}
      </Dialog>

      <Dialog open={!!inviteModal} onCancel={() => setInviteModal(null)}
 title={`${t('org.inviteTitle')} · ${inviteModal?.name || ''}`}>
        {inviteModal && (
          <>
            <p style={{ fontSize: 12, color: 'var(--adm-faint)', margin: '0 0 10px' }}>{t('org.inviteHint')}</p>
            <div style={{ display: 'flex', gap: 8, marginBottom: 8 }}>
              <input className="lc-input" value={inviteCodeInput} placeholder={t('org.inviteInputPlaceholder')} onChange={(e) => setInviteCodeInput(e.target.value)} style={{ flex: 1 }} />
 <Button variant="primary"disabled={!inviteCodeInput.trim()} onClick={createInvite}> {t('org.inviteCreate')}</Button>
            </div>
            {inviteItems.length ? (
              <DataTable<any> rowKey={(row) => String(row.id)} rows={inviteItems}
                     columns={[
                       { key: 'code', title: t('org.inviteCode'), render: (row) => <code>{row.code}</code> },
                       { key: 'status', title: t('org.inviteStatus'), render: (row) => (Number(row.used_count) > 0 ? tpl('org.inviteUsed', { n: row.used_count }) : t('org.inviteUnused')) },
                  ]} />
            ) : <p style={{ fontSize: 12, color: 'var(--adm-faint)' }}>{t('org.inviteNoCodes')}</p>}
          </>
        )}
      </Dialog>

      <Dialog open={!!moveDlg} onCancel={() => setMoveDlg(null)} title="移动组织" onConfirm={doMove}>
        {moveDlg && (
          <Field label="移动到">
            <select className="lc-select" value={String(moveParent)} onChange={(e) => setMoveParent(Number(e.target.value))}>
              <option value="0">{t('org.rootOption')}</option>
              {flatTree.filter((o) => o.id !== moveDlg.node.id).map((o) => <option key={o.id} value={String(o.id)}>{`#${o.id} ${o.name}`}</option>)}
            </select>
          </Field>
        )}
      </Dialog>
        </Panel>
      </>)}
      {/* Tab 面板 */}
      {tab === 'invite' && <InvitesP />}
      {tab === 'users' && <UsersP />}
    </>
  )
}
