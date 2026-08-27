// ============================================================================
// components/admin/panels_a.tsx — Overview / Users / Alerts / Audit / Usage / Invites
// ============================================================================
import { useCallback, useEffect, useMemo, useState } from 'react'
import {
  Button, Table, Dialog, Input, Select, Switch, Tag, Space, Popconfirm, Tabs, Empty,
} from 'tdesign-react'
import {
  systemHealth, systemAudit, systemAlerts, alertResolve,
  adminUsers, adminUserCreate, adminUserUpdate, adminUserDelete, adminUserResetPassword,
  usageMe, usageOrg, usageCost, inviteCodes, inviteCodeCreate,
  orgList, adminPackageSettings, adminPackageSettingsSave,
  API_BASE, authHeaders, getAuthToken, getActiveTenantId,
  type OrgInfo,
} from '@/api'
import { useAdmin, roleName } from '@/stores/admin'
import { Panel, Field, toastResp } from './parts'
import { fmtTime } from '@/lib/ui'
import { useT, t as tFn } from '@/i18n'

// 审计动作键→中英文映射名（模块级，避免渲染闭包作用域问题；未命中字典时回退原始动作键）
function auditActionLabel(a: string): string {
	const key = 'audit.action.' + a
	const v = tFn(key)
	return v && v !== key ? v : a
}

type Any = any

// Vue 版 shortJSON：把 before/after 的 JSON 字符串压成「k=v,k=v…」摘要（最多 3 键）
function shortDiffJSON(s: string): string {
  try {
    const o = JSON.parse(s)
    const keys = Object.keys(o)
    return keys.slice(0, 3).map((k) => `${k}=${o[k]}`).join(',') + (keys.length > 3 ? '…' : '')
  } catch {
    return s.length > 24 ? s.slice(0, 24) + '…' : s
  }
}

// ---------------- 总览（Vue Dashboard/Overview.vue） ----------------
// 指标卡片：仅做展示，value 可直接为 React 节点
function HealthCard({ value, label }: { value: React.ReactNode; label: string }) {
  return (
    <div style={{ minWidth: 120, border: '1px solid #e5e7eb', borderRadius: 8, padding: '10px 14px' }}>
      <b style={{ fontSize: 18, display: 'block' }}>{value}</b>
      <span style={{ fontSize: 12, color: '#889' }}>{label}</span>
    </div>
  )
}

// 总览面板：加载系统健康度与最近审计日志，仅超管可见审计部分
export default function Overview() {
  const [, t, tpl] = useT()
  const { isSuper, activeTenantId } = useAdmin()
  const [health, setHealth] = useState<Any | null>(null)
  const [audit, setAudit] = useState<Any[]>([])

  // 拉取 health 与 audit；非超管清空 audit
  const loadDash = useCallback(async () => {
    try {
      const h = await systemHealth()
      if (h.success) setHealth(((h as unknown as Any).health as Any) ?? null)
    } catch { /* ignore */ }
    if (isSuper) {
      try {
        const a = await systemAudit()
        if (a.success) setAudit(((a as unknown as Any).logs as Any[]) || [])
      } catch { /* ignore */ }
    } else {
      setAudit([])
    }
  }, [isSuper])

  // 初始化加载
  useEffect(() => { void loadDash() }, [loadDash])
  // 切换生效租户后重新加载（对齐 Vue watch(activeTenantId, loadDash)）
  useEffect(() => { void loadDash() }, [activeTenantId, loadDash])

  // 导出审计 CSV：XHR 下载 blob，手动带 token 与 tenant header
  function exportAuditCSV() {
    const url = `${API_BASE}/api/system/audit?export=csv`
    const xhr = new XMLHttpRequest()
    xhr.open('GET', url, true)
    const tk = getAuthToken()
    if (tk) xhr.setRequestHeader('Authorization', `Bearer ${tk}`)
    const tid = getActiveTenantId()
    if (tid > 0) xhr.setRequestHeader('X-Tenant-ID', String(tid))
    xhr.responseType = 'blob'
    xhr.onload = () => {
      if (xhr.status !== 200) { alert(t('overview.exportFailed')); return }
      const a = document.createElement('a')
      a.href = URL.createObjectURL(xhr.response)
      a.download = `audit_${new Date().toISOString().slice(0, 10)}.csv`
      a.click()
      URL.revokeObjectURL(a.href)
    }
    xhr.send()
  }

  // 打开 Prometheus metrics 页面
  function openMetrics() {
    window.open(`${API_BASE}/metrics`, '_blank')
  }

  return (
    <Panel title={t('overview.title')}
      extra={<Space>
        <Button onClick={loadDash}>{t('overview.refresh')}</Button>
        {isSuper && <Button theme="success" onClick={exportAuditCSV}>{t('overview.exportAuditCsv')}</Button>}
        <Button onClick={openMetrics}>{t('overview.prometheus')}</Button>
      </Space>}>
      {/* 健康指标卡片网格 */}
      {!health && <Empty description={t('overview.refresh')} />}
      {health && (
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 12 }}>
          <HealthCard value={String(health.kb_entries ?? '')} label={t('overview.kbEntries')} />
          <HealthCard value={String((health.balance as Any)?.balance ?? '')} label={t('overview.balance')} />
          <HealthCard value={`${health.flow_steps_enabled ?? ''}/${health.flow_steps_total ?? ''}`} label={t('overview.flowSteps')} />
          <HealthCard value={String(health.usage ? Object.keys(health.usage as object).length : 0)} label={t('overview.usageTypes')} />
          <HealthCard value={health.breaker_open ? t('overview.breakerOpen') : t('overview.breakerNormal')} label={t('overview.mainModel')} />
          <HealthCard value={String(health.llm_error_rate ?? '')} label={t('overview.llmErrorRate')} />
        </div>
      )}

      {/* 超管最近审计日志表格 */}
      {isSuper && audit.length > 0 && (
        <div style={{ marginTop: 16 }}>
          <h3 style={{ fontSize: 15 }}>{t('overview.recentAudit')}</h3>
          <Table rowKey="id" size="small" maxHeight={360} data={audit as never}
            columns={[
              { colKey: 'created_at', title: t('overview.colTime'), width: 165, cell: ({ row }: any) => fmtTime(row.created_at) },
              { colKey: 'tenant', title: t('audit.tenant'), width: 130, cell: ({ row }: any) => (
                <>{row.tenant_name || '—'}{row.username && <span style={{ color: '#999' }}> @{row.username}</span>}</>
              ) },
          { colKey: 'action', title: t('overview.colAction'), width: 150, cell: ({ row }: any) => auditActionLabel(row.action) },
              { colKey: 'resource', title: t('overview.colResource'), width: 110 },
              { colKey: 'detail', title: t('overview.colDetail'), ellipsis: true },
              { colKey: 'change', title: t('overview.colChange'), ellipsis: true, cell: ({ row }: any) =>
                (row.before_val && row.after_val)
                  ? tpl('overview.diffOldNew', { old: shortDiffJSON(row.before_val), new: shortDiffJSON(row.after_val) })
                  : '—' },
            ] as never} />
        </div>
      )}
    </Panel>
  )
}

// ---------------- 账户管理（Vue Users.vue） ----------------
// 用户列表、行内编辑、创建用户弹窗；含组织/角色级联约束
export function UsersP() {
  const [, t, tpl] = useT()
  const { isSuper, myLevel, roleOptions, tenants, activeTenantId } = useAdmin()
  const [rows, setRows] = useState<Any[]>([])
  const [orgs, setOrgs] = useState<OrgInfo[]>([])
  const [dlg, setDlg] = useState(false)
  const [uForm, setUForm] = useState<Any>({ username: '', password: '', display_name: '', role: 'user', tenant_id: 1, org_id: 0 })

  // 加载用户列表
  const load = useCallback(async () => {
    const r = await adminUsers()
    if (r.success) setRows(((r as unknown as { users?: Any[] }).users) || [])
  }, [])
  const loadOrgs = useCallback(async () => {
    const r = await orgList()
    if (r.success) setOrgs((r as unknown as { orgs?: OrgInfo[] }).orgs || [])
  }, [])
  // 初始同时加载用户与组织
  useEffect(() => { void load(); void loadOrgs() }, [load, loadOrgs])
  // 切换租户后重新加载（对齐 Vue watch(activeTenantId)）
  useEffect(() => { void load(); void loadOrgs() }, [activeTenantId, load, loadOrgs])

  // 组织下拉选项：根组织 + 全部子组织（带父级路径）
  const orgOptions = useMemo(() => {
    const children = orgs.map((o) => ({ id: o.id, name: orgPath(orgs, o) }))
    return [{ id: 0, name: t('users.rootOrgOption') }, ...children]
  }, [orgs, t])

  function orgPath(list: OrgInfo[], o: OrgInfo): string {
    if (o.parent_id === 0) return o.name
    const parent = list.find((x) => x.id === o.parent_id)
    return parent ? `${orgPath(list, parent)} / ${o.name}` : o.name
  }
  function userOrgName(orgId: number): string {
    if (!orgId) return t('users.rootOrg')
    return orgs.find((o) => o.id === orgId)?.name || tpl('users.orgHash', { id: orgId })
  }

  // 级联：可选部门随超管所选租户过滤；角色选项随部门层级收窄
  const cascadeOrgs = useMemo(() => {
    if (!isSuper) return orgs
    const tid = Number(uForm.tenant_id || 0)
    return orgs.filter((o: any) => o.tenant_id === tid)
  }, [isSuper, orgs, uForm.tenant_id])
  const cascadeRoles = useMemo<string[]>(() => {
    const oid = Number(uForm.org_id || 0)
    if (!oid) return myLevel >= 3 ? ['tenant_admin', 'dept_admin', 'user'] : ['user']
    const org = orgs.find((x: any) => x.id === oid)
    if (org && org.type === 'root') return myLevel >= 3 ? ['tenant_admin', 'dept_admin', 'user'] : ['user']
    return myLevel >= 2 ? ['dept_admin', 'user'] : ['user']
  }, [orgs, uForm.org_id, myLevel])

  // 组织/租户变动后，若当前角色不在可用角色列表则回退到最低角色
  function onCascadeChange() {
    if (!cascadeRoles.includes(String(uForm.role))) {
      setUForm((p: Any) => ({ ...p, role: cascadeRoles[cascadeRoles.length - 1] || 'user' }))
    }
  }

  // 创建用户并清空表单
  async function createUser() {
    if (!uForm.username || !uForm.password) { alert(t('users.required')); return }
    const r = await adminUserCreate({ ...uForm } as never)
    if (!r.success) { alert(r.message); return }
    setUForm({ username: '', password: '', display_name: '', role: 'user', tenant_id: activeTenantId || 1, org_id: 0 })
    setDlg(false); void load()
  }

  // 行内更新用户字段（display_name / role / status / org_id）
  async function editUser(u: Any, field: string, val: string | number) {
    const data: Any = { display_name: u.display_name, role: u.role, status: u.status, org_id: u.org_id || 0 }
    if (field === 'org_id') data.org_id = Number(val)
    else data[field] = val
    const r = await adminUserUpdate(Number(u.id), data as never)
    if (!r.success) alert(r.message)
    void load()
  }

  // 切换用户启用/禁用状态
  async function toggleUser(u: Any) {
    const r = await adminUserUpdate(Number(u.id), {
      display_name: u.display_name, role: u.role,
      status: u.status === 'active' ? 'disabled' : 'active', org_id: u.org_id || 0,
    } as never)
    if (!r.success) alert(r.message)
    void load()
  }

  // 弹窗重置用户密码
  function resetPwd(u: Any) {
    const pwd = prompt(tpl('users.resetPwdPrompt', { name: String(u.username ?? '') }))
    if (!pwd) return
    void adminUserResetPassword(Number(u.id), pwd).then((r) => { if (!r.success) alert(r.message) })
  }

  return (
    <Panel title={t('users.title')}
      extra={<Button theme="primary" onClick={() => { setUForm({ username: '', password: '', display_name: '', role: 'user', tenant_id: activeTenantId || 1, org_id: 0 }); setDlg(true) }}>{t('users.create')}</Button>}>
      <Table rowKey="id" size="small" data={rows}
        columns={[
          { colKey: 'id', title: t('users.colId'), width: 70 },
          { colKey: 'username', title: t('users.colUsername') },
          { colKey: 'display_name', title: t('users.colName'), cell: ({ row }: any) =>
            <Input size="small" value={String(row.display_name ?? '')} onChange={(v) => editUser(row, 'display_name', v)} /> },
          { colKey: 'org', title: t('users.colOrg'), cell: ({ row }: any) =>
            <Select size="small" value={Number(row.org_id || 0)} onChange={(v) => editUser(row, 'org_id', v as number)}
              options={orgOptions.map((o) => ({ label: o.name, value: o.id }))} /> },
          { colKey: 'role', title: t('users.colRole'), width: 140, cell: ({ row }: any) =>
            <Select size="small" value={String(row.role)} onChange={(v) => editUser(row, 'role', v as string)}
              options={roleOptions.map((r) => ({ label: t('users.role.' + r), value: r }))} /> },
          { colKey: 'status', title: t('users.colStatus'), width: 90, cell: ({ row }: any) =>
            <Tag theme={row.status === 'active' ? 'success' : 'default'}>{row.status === 'active' ? t('users.enable') : row.status === 'disabled' ? t('users.disable') : row.status}</Tag> },
          { colKey: 'last_login_at', title: t('users.colLastLogin'), width: 165, cell: ({ row }: any) => fmtTime(row.last_login_at) },
          { colKey: 'op', title: t('users.colActions'), width: 200, cell: ({ row }: any) => (
            <Space size={4}>
              <Button size="small" variant="text" onClick={() => resetPwd(row)}>{t('users.resetPwd')}</Button>
              <Popconfirm content={t('users.disable') + '/' + t('users.enable')} onConfirm={async () => { await toggleUser(row) }}>
                <Button size="small" variant="text" theme={row.status === 'active' ? 'danger' : 'primary'}>{row.status === 'active' ? t('users.disable') : t('users.enable')}</Button>
              </Popconfirm>
              <Popconfirm content={t('common.delete')} onConfirm={async () => { toastResp(await adminUserDelete(Number(row.id)), t('common.delete')); void load() }}>
                <Button size="small" variant="text" theme="danger">{t('common.delete')}</Button>
              </Popconfirm>
            </Space>
          ) },
        ] as never} />

      <Dialog visible={dlg} onClose={() => setDlg(false)} header={t('users.create')} width={460}
        onConfirm={async () => { await createUser() }}>
        <Field label={t('users.usernamePlaceholder')}><Input value={String(uForm.username || '')} onChange={(v) => setUForm((p: Any) => ({ ...p, username: v }))} /></Field>
        <Field label={t('users.passPlaceholder')}><Input value={String(uForm.password || '')} onChange={(v) => setUForm((p: Any) => ({ ...p, password: v }))} /></Field>
        <Field label={t('users.displayNamePlaceholder')}><Input value={String(uForm.display_name || '')} onChange={(v) => setUForm((p: Any) => ({ ...p, display_name: v }))} /></Field>
        {isSuper && (
          <Field label={t('users.colOrg')}>
            <Select value={Number(uForm.tenant_id || 1)} onChange={(v) => { setUForm((p: Any) => ({ ...p, tenant_id: v })); onCascadeChange() }}
              options={tenants.map((tt: any) => ({ label: tpl('users.orgItem', { id: tt.id, code: tt.code }), value: tt.id }))} />
          </Field>
        )}
        <Field label={t('users.colOrg')}>
          <Select value={Number(uForm.org_id || 0)} onChange={(v) => { setUForm((p: Any) => ({ ...p, org_id: v })); onCascadeChange() }}
            options={[{ id: 0, name: t('org.rootOption') }, ...cascadeOrgs.map((o) => ({ id: o.id, name: o.name, type: o.type }))].map((o: any) => ({ label: o.type === 'root' ? `🏢 ${o.name}` : o.name, value: o.id }))} />
        </Field>
        <Field label={t('users.colRole')}>
          <Select value={String(uForm.role || 'user')} onChange={(v) => setUForm((p: Any) => ({ ...p, role: v }))}
            options={cascadeRoles.map((r) => ({ label: t('users.role.' + r), value: r }))} />
        </Field>
      </Dialog>
    </Panel>
  )
}

// ---------------- 系统告警（Vue Alerts.vue） ----------------
// 告警列表 + 注册/触达配置（邮件验证、通知、人工审核、验证码、Webhook）
export function AlertsP() {
  const [, t] = useT()
  const { activeTenantId } = useAdmin()
  const [rows, setRows] = useState<Any[]>([])
  const [status, setStatus] = useState('')
  // 注册与触达配置表单（布尔以 '0'/'1' 字符串存储以兼容后端）
  const [regCfg, setRegCfg] = useState<Record<string, string | boolean>>({
    email_verify_enabled: '0', email_notify_enabled: '0', registration_review: '0',
    captcha_provider: '', captcha_site_key: '', captcha_secret_key: '',
    wecom_webhook_url: '', dingtalk_webhook_url: '',
  })

  const load = useCallback(async () => {
    const r = await systemAlerts(status || undefined)
    if (r.success) setRows(((r as unknown as { alerts?: Any[] }).alerts) || [])
  }, [status])
  const loadRegCfg = useCallback(async () => {
    const cfg = await adminPackageSettings()
    if (cfg.success) {
      const c = cfg as Any
      for (const k of ['email_verify_enabled', 'email_notify_enabled', 'registration_review',
        'captcha_provider', 'captcha_site_key', 'wecom_webhook_url', 'dingtalk_webhook_url']) {
        if (c[k] !== undefined && c[k] !== '') setRegCfg((p) => ({ ...p, [k]: c[k] }) as Record<string, string | boolean>)
      }
    }
  }, [])
  // 初始加载告警与注册配置
  useEffect(() => { void load() }, [load])
  useEffect(() => { void loadRegCfg() }, [loadRegCfg])
  // 切换租户后刷新告警
  useEffect(() => { void load() }, [activeTenantId, load])

  // 将告警标记为已解决
  async function resolveAlert(a: Any) {
    await alertResolve(Number(a.id))
    await load()
  }

  // Switch 变动时统一把布尔转 '1'/'0'
  function setSwitch(k: string, v: boolean) {
    setRegCfg((p) => ({ ...p, [k]: v ? '1' : '0' }))
  }

  // 保存注册/触达配置（布尔开关 + 验证码与 webhook 字段）
  async function saveRegCfg() {
    const payload: Record<string, string> = {}
    for (const k of ['email_verify_enabled', 'email_notify_enabled', 'registration_review']) {
      const val = regCfg[k]
      payload[k] = String(val) === 'true' || val === '1' ? '1' : '0'
    }
    for (const k of ['captcha_provider', 'captcha_site_key', 'wecom_webhook_url', 'dingtalk_webhook_url']) {
      payload[k] = String(regCfg[k] || '')
    }
    if (regCfg.captcha_secret_key) payload.captcha_secret_key = String(regCfg.captcha_secret_key)
    // React api 签名仅声明部分字段；后端按 system_config 全量接收，这里按 Vue 透传
    void adminPackageSettingsSave(payload as never)
  }

  return (
    <Panel title={t('alerts.title')}
      extra={<Space size={8}>
        <Select value={status} onChange={(v) => setStatus(v as string)} style={{ width: 120 }}
          options={[{ label: t('alerts.all'), value: '' }, { label: t('alerts.open'), value: 'open' }, { label: t('alerts.resolved'), value: 'resolved' }]} />
        <Button onClick={load}>{t('alerts.refresh')}</Button>
      </Space>}>
      <Table rowKey="id" size="small" data={rows}
        columns={[
          { colKey: 'level', title: t('alerts.colLevel'), width: 90, cell: ({ row }: any) => <Tag theme={row.level === 'critical' ? 'danger' : row.level === 'warning' ? 'warning' : 'default'}>{row.level}</Tag> },
          { colKey: 'kind', title: t('alerts.colKind'), width: 130 },
          { colKey: 'tenant_id', title: t('alerts.colTenant'), width: 90, cell: ({ row }: any) => `#${row.tenant_id}` },
          { colKey: 'message', title: t('alerts.colContent'), ellipsis: true },
          { colKey: 'status', title: t('alerts.colStatus'), width: 90, cell: ({ row }: any) => row.status === 'open' ? t('alerts.open') : t('alerts.resolved') },
          { colKey: 'created_at', title: t('alerts.colTime'), width: 160, cell: ({ row }: any) => fmtTime(row.created_at) },
          { colKey: 'op', title: '', width: 90, cell: ({ row }: any) =>
            row.status === 'open'
              ? <Button size="small" variant="text" onClick={() => resolveAlert(row)}>{t('alerts.close')}</Button>
              : <Tag theme="success">{t('alerts.resolved')}</Tag> },
        ] as never} />
      {!rows.length && <div style={{ textAlign: 'center', color: '#999', padding: 12 }}>{t('alerts.empty')}</div>}

      {/* 注册与触达配置（Vue Alerts.vue 整合段） */}
      <Panel title={t('packages.regNotifyTitle')}>
        <div style={{ fontSize: 13, color: '#889', marginBottom: 8 }}>{t('packages.regNotifyHint')}</div>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8 }}>
          <Switch value={regCfg.email_verify_enabled === '1' || regCfg.email_verify_enabled === true} onChange={(v) => setSwitch('email_verify_enabled', v as boolean)} />
          <span style={{ fontSize: 13, color: '#556' }}>{t('packages.emailVerify')}</span>
        </div>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8 }}>
          <Switch value={regCfg.email_notify_enabled === '1' || regCfg.email_notify_enabled === true} onChange={(v) => setSwitch('email_notify_enabled', v as boolean)} />
          <span style={{ fontSize: 13, color: '#556' }}>{t('packages.emailNotify')}</span>
        </div>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8 }}>
          <Switch value={regCfg.registration_review === '1' || regCfg.registration_review === true} onChange={(v) => setSwitch('registration_review', v as boolean)} />
          <span style={{ fontSize: 13, color: '#556' }}>{t('packages.regReview')}</span>
        </div>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8 }}>
          <span style={{ fontSize: 13, color: '#556', minWidth: 130 }}>{t('packages.captchaProvider')}</span>
          <Select value={String(regCfg.captcha_provider || '')} onChange={(v) => setRegCfg((p) => ({ ...p, captcha_provider: v as string }))} style={{ width: 160 }}
            options={[{ label: t('packages.captchaOff'), value: '' }, { label: 'Turnstile', value: 'turnstile' }]} />
        </div>
        {regCfg.captcha_provider === 'turnstile' && (
          <div style={{ display: 'flex', gap: 8, marginBottom: 8 }}>
            <Input value={String(regCfg.captcha_site_key || '')} placeholder={t('packages.captchaSiteKey')} onChange={(v) => setRegCfg((p) => ({ ...p, captcha_site_key: v }))} />
            <Input type="password" value={String(regCfg.captcha_secret_key || '')} placeholder={t('packages.captchaSecretKey')} onChange={(v) => setRegCfg((p) => ({ ...p, captcha_secret_key: v }))} />
          </div>
        )}
        <Input value={String(regCfg.wecom_webhook_url || '')} placeholder={t('packages.wecomWebhook')} onChange={(v) => setRegCfg((p) => ({ ...p, wecom_webhook_url: v }))} style={{ marginBottom: 8 }} />
        <Input value={String(regCfg.dingtalk_webhook_url || '')} placeholder={t('packages.dingtalkWebhook')} onChange={(v) => setRegCfg((p) => ({ ...p, dingtalk_webhook_url: v }))} style={{ marginBottom: 8 }} />
        <Button theme="success" onClick={saveRegCfg}>{t('common.save')}</Button>
      </Panel>
    </Panel>
  )
}

// ---------------- 审计日志（Vue Audit.vue） ----------------
// 系统审计日志：按操作类型、日期范围筛选并导出 CSV
export function AuditP() {
  const [, t, tpl] = useT()
  const [rows, setRows] = useState<Any[]>([])
  const [fAction, setFAction] = useState('')
  const [fFrom, setFFrom] = useState('')
  const [fTo, setFTo] = useState('')

  // 支持的审计动作类型清单（用于 Select 筛选）
  const actions = ['login', 'user_create', 'user_update', 'user_delete', 'user_reset_pwd',
    'org_create', 'org_rename', 'org_delete', 'kb_package_create', 'kb_package_status',
    'kb_entries_import', 'model_save', 'stage_models_save', 'package_subscribe']

  // 拉取全部审计日志并在前端按条件过滤
  const load = useCallback(async () => {
    const r = await systemAudit()
    if (r.success) {
      let list = ((r as unknown as { logs?: Any[] }).logs) || []
      if (fAction) list = list.filter((l: any) => l.action === fAction)
      if (fFrom) list = list.filter((l: any) => l.created_at >= fFrom)
      if (fTo) list = list.filter((l: any) => l.created_at <= fTo + 'T23:59:59')
      setRows(list)
    }
  }, [fAction, fFrom, fTo])
  useEffect(() => { void load() }, [load])

  // 导出 CSV：fetch blob 并触发下载
  async function exportCsv() {
    const resp = await fetch(`${API_BASE}/api/system/audit?export=csv`, { headers: authHeaders() })
    if (!resp.ok) { void MessagePluginError(`导出失败 (${resp.status})`); return }
    const blob = await resp.blob()
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `audit_${new Date().toISOString().slice(0, 10)}.csv`
    document.body.appendChild(a); a.click(); a.remove()
    URL.revokeObjectURL(url)
  }

  return (
    <Panel title={t('audit.title')}
      extra={<Button onClick={exportCsv}>{t('audit.export')}</Button>}>
      <p className="ad-hint" style={{ fontSize: 13, color: '#889', margin: '0 0 8px' }}>{t('audit.hint')}</p>
      <Space style={{ marginBottom: 8 }}>
        <Select value={fAction} onChange={(v) => setFAction(v as string)} style={{ width: 180 }}
          options={[{ label: t('audit.allActions'), value: '' }, ...actions.map((a) => ({ label: auditActionLabel(a), value: a }))]} />
        <input type="date" value={fFrom} onChange={(e) => setFFrom(e.target.value)} style={{ height: 30, border: '1px solid #dcdcdc', borderRadius: 4, padding: '0 8px', width: 150 }} />
        <span style={{ color: '#999' }}>→</span>
        <input type="date" value={fTo} onChange={(e) => setFTo(e.target.value)} style={{ height: 30, border: '1px solid #dcdcdc', borderRadius: 4, padding: '0 8px', width: 150 }} />
        <Button onClick={load}>{t('common.refresh')}</Button>
      </Space>

      <Table rowKey="id" size="small" maxHeight={520} data={rows}
        columns={[
          { colKey: 'created_at', title: t('overview.colTime'), width: 165, cell: ({ row }: any) => fmtTime(row.created_at) },
          { colKey: 'operator', title: t('audit.operator'), width: 140, cell: ({ row }: any) => row.username || row.user_id || '—' },
          { colKey: 'action', title: t('overview.colAction'), width: 150 },
          { colKey: 'resource', title: t('overview.colResource'), width: 110 },
          { colKey: 'detail', title: t('overview.colDetail'), ellipsis: true },
          { colKey: 'change', title: t('overview.colChange'), ellipsis: true, cell: ({ row }: any) =>
            (row.before_val && row.after_val)
              ? tpl('overview.diffOldNew', { old: shortDiffJSON(row.before_val), new: shortDiffJSON(row.after_val) })
              : '—' },
        ] as never} />
      {!rows.length && <div style={{ textAlign: 'center', color: '#999', padding: 12 }}>{t('audit.empty')}</div>}
    </Panel>
  )
}

// 错误提示封装：动态导入 tdesign 的 MessagePlugin.error，规避循环依赖/SSR 问题
function MessagePluginError(m: string) { void import('tdesign-react').then((M) => M.MessagePlugin.error(m)) }

// ---------------- 用量看板（Vue Usage.vue） ----------------
// 个人/组织/成本用量以 Tabs 展示，过滤掉对象字段
export function UsageP() {
  const [, t] = useT()
  const [me, setMe] = useState<Any | null>(null)
  const [org, setOrg] = useState<Any | null>(null)
  const [cost, setCost] = useState<Any | null>(null)
  // 并行拉取三类用量数据
  useEffect(() => {
    void (async () => {
      try { const r = await usageMe(); if (r.success) setMe(r as unknown as Any) } catch {}
      try { const r = await usageOrg(); if (r.success) setOrg(r as unknown as Any) } catch {}
      try { const r = await usageCost(); if (r.success) setCost(r as unknown as Any) } catch {}
    })()
  }, [])
  // 把对象拍平成 k-v 统计卡片（排除嵌套对象）
  const renderKV = (d: Any | null) => !d ? <Empty description="—" /> : (
    <div className="stat-grid">
      {Object.entries(d).filter(([, v]) => typeof v !== 'object').map(([k, v]) => (
        <div key={k} className="stat-card"><div style={{ fontSize: 12, color: '#889' }}>{k}</div><b>{String(v)}</b></div>
      ))}
    </div>
  )
  return (
    <Panel title={t('usage.dashboardTitle')}>
      <Tabs placement="top" list={[
        { label: t('usage.tabMine'), value: 'me', panel: renderKV(me) },
        { label: t('usage.tabOrg'), value: 'org', panel: renderKV(org) },
        ...(cost ? [{ label: t('usage.tabCost'), value: 'cost', panel: renderKV(cost) }] : []),
      ]} />
    </Panel>
  )
}

// ---------------- 邀请码（Vue Invites.vue） ----------------
// 邀请码列表与创建；可指定归属租户或新建组织
export function InvitesP() {
  const [, t] = useT()
  const { tenants, activeTenantId } = useAdmin()
  const [rows, setRows] = useState<Any[]>([])
  const [dlg, setDlg] = useState(false)
  const [code, setCode] = useState('')
  const [tenantId, setTenantId] = useState(0)

  // 加载邀请码列表（兼容后端返回 invite_codes / codes 两种字段名）
  const load = useCallback(async () => {
    const r = await inviteCodes()
    if (r.success) setRows(((r as unknown as { invite_codes?: Any[] }).invite_codes) || ((r as unknown as { codes?: Any[] }).codes) || [])
  }, [])
  // 初始加载与租户切换后刷新
  useEffect(() => { void load() }, [load])
  useEffect(() => { void load() }, [activeTenantId, load])

  return (
    <Panel title={t('invites.title')}
      extra={<Button theme="primary" onClick={() => { setCode(''); setTenantId(0); setDlg(true) }}>{t('invites.create')}</Button>}>
      <p className="ad-hint" style={{ fontSize: 13, color: '#889', margin: '0 0 8px' }}>{t('invites.hint')}</p>
      <Table rowKey="id" size="small" data={rows}
        columns={[
          { colKey: 'code', title: t('invites.colCode') },
          { colKey: 'tenant_id', title: t('invites.colTenant'), width: 110, cell: ({ row }: any) => row.tenant_id > 0 ? `#${row.tenant_id}` : t('invites.newOrg') },
          { colKey: 'used', title: t('invites.colStatus'), width: 90, cell: ({ row }: any) => <Tag theme={Number(row.used) === 1 ? 'default' : 'success'}>{Number(row.used) === 1 ? t('invites.used') : t('invites.unused')}</Tag> },
          { colKey: 'used_by', title: t('invites.colUsedBy'), width: 140 },
          { colKey: 'created_at', title: t('invites.colCreatedAt'), width: 165, cell: ({ row }: any) => fmtTime(row.created_at) },
          { colKey: 'used_at', title: t('invites.colUsedAt'), width: 165, cell: ({ row }: any) => fmtTime(row.used_at) },
        ] as never} />
      {!rows.length && <div style={{ textAlign: 'center', color: '#999', padding: 12 }}>{t('invites.empty')}</div>}

      <Dialog visible={dlg} onClose={() => setDlg(false)} header={t('invites.create')} onConfirm={async () => {
        if (!code.trim()) { alert(t('invites.codeRequired')); return }
        const r = await inviteCodeCreate({ code: code.trim(), tenant_id: tenantId })
        if (toastResp(r, t('invites.create'))) { setDlg(false); void load() }
      }}>
        <Field label={t('invites.codePlaceholder')}><Input value={code} onChange={setCode} /></Field>
        <Field label={t('invites.colTenant')}>
          <Select value={tenantId} onChange={(v) => setTenantId(v as number)}
            options={[{ label: t('invites.newOrg'), value: 0 }, ...tenants.map((tt: any) => ({ label: `${tt.name} (#${tt.id})`, value: tt.id }))]} />
        </Field>
      </Dialog>
    </Panel>
  )
}

// ---------------- 协议签署（Vue 无对应，新增后台面板） ----------------
// 列出全部用户，展示是否已阅读并同意《用户协议》与《隐私协议》及签署时间
export function AgreementsP() {
  const [, t, tpl] = useT()
  const [rows, setRows] = useState<Any[]>([])
  const [loading, setLoading] = useState(false)

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const r = await adminUsers()
      if (r.success) setRows(((r as unknown as { users?: Any[] }).users) || [])
    } finally { setLoading(false) }
  }, [])
  useEffect(() => { void load() }, [load])

  const signed = rows.filter((u: Any) => (u.agreed_at || '').trim() !== '').length

  const columns = [
    { colKey: 'username', title: t('agreements.user'), width: 160 },
    { colKey: 'role', title: t('agreements.role'), width: 130, cell: ({ row }: Any) => roleName(row.role) },
    { colKey: 'email', title: t('agreements.email'), width: 200 },
    {
      colKey: 'status', title: t('agreements.status'), width: 110,
      cell: ({ row }: Any) => {
        const ok = (row.agreed_at || '').trim() !== ''
        return <Tag theme={ok ? 'success' : 'warning'} variant="light">{ok ? t('agreements.signed') : t('agreements.unsigned')}</Tag>
      },
    },
    {
      colKey: 'agreed_at', title: t('agreements.signedAt'), width: 180,
      cell: ({ row }: Any) => (row.agreed_at ? fmtTime(row.agreed_at) : '—'),
    },
  ] as never

  return (
    <Panel title={t('agreements.title')}>
      <div style={{ color: '#666', marginBottom: 12 }}>{t('agreements.desc')}</div>
      <div style={{ marginBottom: 12 }}>{tpl('agreements.total', { n: rows.length, m: signed })}</div>
      <Table rowKey="id" size="small" loading={loading} data={rows as never} columns={columns} />
    </Panel>
  )
}
