// ============================================================================
// components/admin/panels_a.tsx — Overview / Users / Alerts / Audit / Usage / Invites
// 职责：后台面板 A，包含概览、用户管理、系统告警、审计日志、用量统计与邀请码管理。
// ============================================================================
import { useCallback, useEffect, useMemo, useState } from 'react'
import { Button, DataTable, Dialog, EmptyState, Link, StatusPill, Switch, Tabs } from '@/ui/langcross/src'
// confirmDialog 用于邀请码撤销的**唯一**确认点（★ G9，2026-10-19）；其余危险操作仍走 promptText/直接执行
import { promptText, confirmDialog } from '@/components/uiDialogs'
import {
  systemHealth, systemAudit, systemAlerts, alertResolve, alertSilence, alertUnsilence,
  adminUsers, adminUserCreate, adminUserUpdate, adminUserDelete, adminUserResetPassword,
  usageMe, usageOrg, usageCost, inviteCodes, inviteCodeCreate,
  orgList, adminPackageSettings, adminPackageSettingsSave,
  API_BASE, authHeaders, getAuthToken, getActiveTenantId, handleUnauthorized,
  request, bizResp, // ★ G9：邀请码撤销（后端端点尚缺，前端按契约先接；失败走诚实错误提示）
  type OrgInfo,
  type InviteLinkCreateResult, // ★ B4-c：企业建号回执（与管理台建号/邀请面板共用同一形态）
} from '@/api'
import { InviteLinkCard, InviteLinkSecurityNote } from './InviteLinkCard' // ★ B4-c：邀请链接转发卡片 + §6.5 安全警示的唯一实现
import { useAdmin, roleName } from '@/stores/admin'
import { Panel, Field, toastResp } from './parts'
import { fmtTime, fmtNum } from '@/lib/ui'
import { fmtPoints } from '@/utils/points' // ★ S1 积分口径展示
import { useT, t as tFn, tpl as tplFn } from '@/i18n'
import { toastError, toastWarn, toastSuccess } from '@/lib/toastBus'
import { runGuarded } from '@/lib/runGuarded' // ★ #42：取数失败必须有可见出口（见 loadDash / 用量看板注释）

/** 审计动作键→中英文映射名（模块级，避免渲染闭包作用域问题；未命中字典时回退原始动作键） */
function auditActionLabel(a: string): string {
	const key = 'audit.action.' + a
	const v = tFn(key)
	return v && v !== key ? v : a
}

// Any 简化别名：接口返回的非强类型数据统一用 any 承接，避免逐处标注
type Any = any

/** Vue 版 shortJSON：把 before/after 的 JSON 字符串压成「k=v,k=v…」摘要（最多 3 键） */
function shortDiffJSON(s: string): string {
  try {
    const o = JSON.parse(s)
    const keys = Object.keys(o)
    return keys.slice(0, 3).map((k) => `${k}=${o[k]}`).join(',') + (keys.length > 3 ? '…' : '')
  } catch {
    return s.length > 24 ? s.slice(0, 24) + '…' : s
  }
}

// ==================== 三处 CSV 导出的唯一入口（★ 2026-11-04 体验批 A2） ====================
// 背景：exportAuditCSV / AuditP.exportCsv / exportUsageCSV 原先各自裸 XHR / 裸 fetch，
//   三条通道零超时保护、失败各弹各的，任一挂死都静默（#42「失败必须有可见出口」被架空）。
//   现收成一个入口：统一鉴权头、401 收口、blob 下载触发，并给 XHR 挂 timeout 看门狗 +
//   全程 runGuarded 出声。§2.10「导出类不设硬闸」指不套 request() 的 30/45s 档
//   （导出走浏览器下载、可能几分钟），下面 5 分钟看门狗只防「连接挂死永不回包」，不是请求硬闸。

/** 导出看门狗时长（5 分钟）：只防无限挂起，不是 §2.10 的 30/45s 请求硬闸 */
const CSV_EXPORT_TIMEOUT_MS = 300_000

/** 共用导出入参：完整下载地址、落盘文件名、按状态码取词的失败提示语 */
interface CsvExportOpts {
  url: string
  filename: string
  failMsg: (status: number | null) => string
}

/** 触发浏览器下载：用临时 <a> 把 blob 落盘并回收 objectURL（三处原先各抄一遍） */
function triggerCsvDownload(blob: Blob, filename: string) {
  const a = document.createElement('a')
  a.href = URL.createObjectURL(blob)
  a.download = filename
  document.body.appendChild(a)
  a.click()
  a.remove()
  URL.revokeObjectURL(a.href)
}

/** 三处 CSV 导出的唯一入口：XHR 取 blob → 触发下载；401/非 200/超时/网络错一律经 runGuarded 出声，
 *  绝不静默（保住 #42）。不设 30/45s 硬闸，只留上面的看门狗与可取消的 XHR 形态。 */
function exportCsvFile(opts: CsvExportOpts) {
  void runGuarded(() => new Promise<void>((resolve, reject) => {
    const xhr = new XMLHttpRequest()
    xhr.open('GET', opts.url, true)
    const tk = getAuthToken()
    if (tk) xhr.setRequestHeader('Authorization', `Bearer ${tk}`)
    const tid = getActiveTenantId()
    if (tid > 0) xhr.setRequestHeader('X-Tenant-ID', String(tid))
    xhr.responseType = 'blob'
    xhr.timeout = CSV_EXPORT_TIMEOUT_MS
    // 失败造一句可读 Error 交给 runGuarded 弹（带状态码便于排查；超时/网络错没有状态码）。
    const fail = (status: number | null) => reject(new Error(opts.failMsg(status)))
    xhr.onload = () => {
      // ★ #42：裸 XHR 不经 request()，401 必须显式交 core 清登录态；此后 resolve 不再弹二次。
      if (xhr.status === 401) { handleUnauthorized(opts.url); resolve(); return }
      if (xhr.status !== 200) { fail(xhr.status); return }
      triggerCsvDownload(xhr.response as Blob, opts.filename)
      resolve()
    }
    xhr.ontimeout = () => fail(null)
    xhr.onerror = () => fail(null)
    xhr.send()
  }))
}

// ==================== 总览面板（Vue Dashboard/Overview.vue） ====================

/** 指标卡片组件：仅做展示，value 可直接为 React 节点 */
function HealthCard({ value, label }: { value: React.ReactNode; label: string }) {
  return (
    <div style={{ minWidth: 120, border: '1.2px solid var(--adm-line)', borderRadius: 8, padding: '10px 14px' }}>
      <b style={{ fontSize: 18, display: 'block' }}>{value}</b>
      <span style={{ fontSize: 14, color: 'var(--adm-faint)' }}>{label}</span>
    </div>
  )
}

/** 总览面板组件：加载系统健康度与最近审计日志，仅超管可见审计部分 */
export default function Overview() {
  const [, t, tpl] = useT()
  const { isSuper, activeTenantId, myLevel } = useAdmin()
  // 系统健康指标数据
  const [health, setHealth] = useState<Any | null>(null)
  // 审计日志列表
  const [audit, setAudit] = useState<Any[]>([])
  // ★ 2026-11-04 超时批 A2：审计区**默认收起、展开才取数**。
  // 理由：概览首屏真正要回答的是"系统活着吗、余额还剩多少"，而"最近谁改过配置"是第二屏问题；
  // 它在改造前是首屏必发的一条**无租户限定、按时间全表排序**的查询（平台全量视图），
  // 与健康指标串在一起时就是把"进后台老超时"推过 30 秒线的那半条腿。
  const [auditOpen, setAuditOpen] = useState(false)
  // 总览内部分屏：系统看板（运行状态+审计日志）/ 用量看板（个人+系统用量）
  const [ovTab, setOvTab] = useState<'system' | 'usage'>('system')

  /** 拉取 health（以及**已展开时**的 audit）；非超管清空 audit
   *  ★ #42（前端坏味道：数据加载的空 catch）：两处 catch 吞错后，概览页把「后端挂了」渲染成
   *  「健康指标全空 + 审计日志零条」，超管会误判成系统无事发生；改走 runGuarded 弹后端原文
   *  （不新增 i18n 键），成功路径与旧实现一致。**A2 之后这一条不许回退成静默**——懒取只是把
   *  请求推到展开那一刻，失败仍然必须出声。
   *  ★★ A1：多条请求**并发**发。旧写法是 await health 再 await audit，两条共用同一段时间预算——
   *  慢的那条把另一条也拖过 30 秒，屏幕上就成了"进后台老报超时"，而实际只有一条腿慢。 */
  const loadDash = useCallback(async () => {
    const jobs: Promise<unknown>[] = [
      runGuarded(() => systemHealth()).then((h) => {
        if (h?.success) setHealth(((h as unknown as Any).health as Any) ?? null)
      }),
    ]
    if (auditOpen && (isSuper || myLevel >= 3)) {
      jobs.push(runGuarded(() => systemAudit()).then((a) => {
        if (a?.success) setAudit(((a as unknown as Any).logs as Any[]) || [])
      }))
    } else if (!auditOpen) {
      // 收起态：把上一租户/上一轮的读数清掉，别让"展开后先看到旧数据再变新"变成两种读数同屏
      setAudit([])
    }
    await Promise.all(jobs)
    // ★ 依赖补 myLevel / activeTenantId / auditOpen：旧写法 deps 只有 [isSuper]，而函数体读了 myLevel
    //  ⇒ closure 里是旧值；"切租户要重发"过去是靠**第二个 useEffect**凑出来的（见下）。
  }, [isSuper, myLevel, activeTenantId, auditOpen])

  // 初始化加载与租户切换时重新加载
  // ★★ 2026-11-04 超时批 A1：**只留一个 useEffect**。旧形态这里有两个（deps 分别是
  //  [loadDash] 与 [activeTenantId, loadDash]），挂载那一刻**两个都会各跑一次**
  //  ⇒ 系统健康与全平台审计各发两遍。审计那条是"不带租户限定＋按时间全表排序＋拖前后值大字段"
  //  的查询（病灶见 store/audit.go 与 indexperf 迁移），翻倍直接把首屏推过 30 秒超时线——
  //  用户报的"进后台老超时"的放大系数就在这一步。切租户仍需重发，语义由上面 deps 承担。
  useEffect(() => { void loadDash() }, [loadDash])

  /** 导出审计 CSV：改走共用入口 exportCsvFile（鉴权/401 收口/超时兜底/失败出声都在那里） */
  function exportAuditCSV() {
    exportCsvFile({
      url: `${API_BASE}/api/system/audit?export=csv`,
      filename: `audit_${new Date().toISOString().slice(0, 10)}.csv`,
      failMsg: () => t('overview.exportFailed'),
    })
  }

  // ★ F-20/F-39（2026-09-25 UAT 修复批G）：原 openMetrics()（window.open(`${API_BASE}/metrics`)）
  //   连同工具条上的 Prometheus 按钮一并删除——/metrics 暴露的是运行时指标（请求量/错误率/熔断），
  //   属平台运维面信息，管理台总览把它挂给租户管理员是信息面越权。
  //   i18n 键 overview.prometheus 保留不删（删键是 12 语种文件手术、零收益），仅注释标注入口已移除。
  //   ⚠️ API_BASE import 不删：导出审计 CSV / 用量 CSV 导出仍在用。
  return (
    <>
      <Tabs activeKey={ovTab} onChange={(k) => setOvTab(k as 'system' | 'usage')} items={[
        { key: 'system', label: t('overview.tabSystem') },
        { key: 'usage', label: t('overview.tabUsage') },
      ]} />
      {/* 系统看板 Tab */}
      {ovTab === 'system' && (
        <Panel title={t('overview.title')}
          extra={<div style={{ display: 'flex', gap: 8 }}>
            <Button variant="secondary" onClick={loadDash}>{t('overview.refresh')}</Button>
            {isSuper && <Button variant="primary" onClick={exportAuditCSV}>{t('overview.exportAuditCsv')}</Button>}
          </div>}>
      {/* 健康指标卡片网格 */}
      {!health && <EmptyState title={t('overview.refresh')} />}
      {health && (
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 12 }}>
          <HealthCard value={String(health.kb_entries ?? '')} label={t('overview.kbEntries')} />
          {/* ★ F-16（2026-09-25 UAT 修复批G·前端半）：积分余额改读 balance.total_points——
              批B 为该出参补的「双桶合计」（免费/体验桶 quota_grants + 付费永久桶 balance_accounts.balance 折积分），
              balance_points 只是永久桶明细，只持有 grants 的租户曾显 0（总览读错桶的根因）。
              total_points 缺失（旧后端未带该字段）时回落 balance_points，原明细行展示语义保留；
              标签 overview.balance 随之订正为「合计」口径（zh/en 见 panels/overview.ts，十语种由序列组同步）。 */}
          <HealthCard value={(() => {
            const b = health.balance as Any
            const v = b?.total_points ?? b?.balance_points
            return v != null ? `${fmtPoints(Number(v))}` : ''
          })()} label={t('overview.balance')} />
          <HealthCard value={`${health.flow_steps_enabled ?? ''}/${health.flow_steps_total ?? ''}`} label={t('overview.flowSteps')} />
          <HealthCard value={String(health.usage ? Object.keys(health.usage as object).length : 0)} label={t('overview.usageTypes')} />
          <HealthCard value={health.breaker_open ? t('overview.breakerOpen') : t('overview.breakerNormal')} label={t('overview.mainModel')} />
          <HealthCard value={String(health.llm_error_rate ?? '')} label={t('overview.llmErrorRate')} />
        </div>
      )}

      {/* 审计日志表格：超管看全平台，企业租户管理员看本租户（后端按 X-Tenant-ID 自动隔离） */}
      {/* ★ A2（2026-11-04）审计区改成「点开设」：收起时**一个请求都不发**（见 loadDash 的 auditOpen 分支）。
          非超管/等级<3 这一族仍然整块不出现（判据没放宽，只是把取数时机挪到展开那一刻）。 */}
      {(isSuper || myLevel >= 3) && (
        <div style={{ marginTop: 16 }}>
          <button
            type="button"
            className="lc-btn"
            data-testid="overview-audit-toggle"
            aria-expanded={auditOpen}
            onClick={() => setAuditOpen((v) => !v)}
          >
            {t('overview.recentAudit')}
          </button>
          {auditOpen && (
            audit.length > 0 ? (
              <DataTable<any> rowKey={(row) => String(row.id)} rows={audit as never}
                columns={[
                  { key: 'created_at', title: t('overview.colTime'), width: 165, render: (row) => fmtTime(row.created_at) },
                  { key: 'tenant', title: t('audit.tenant'), width: 130, render: (row) => (
                    <>{row.tenant_name || '—'}{row.username && <span style={{ color: 'var(--adm-faint)' }}> @{row.username}</span>}</>
                  ) },
                  { key: 'action', title: t('overview.colAction'), width: 150, render: (row) => auditActionLabel(row.action) },
                  { key: 'resource', title: t('overview.colResource'), width: 110 },
                  { key: 'detail', title: t('overview.colDetail'), dim: true, render: (row) => String(row.detail ?? '—') },
                  { key: 'change', title: t('overview.colChange'), dim: true, render: (row) =>
                    (row.before_val && row.after_val)
                      ? tpl('overview.diffOldNew', { old: shortDiffJSON(row.before_val), new: shortDiffJSON(row.after_val) })
                      : '—' },
                ]}  />
            ) : (
              // 展开后零条：给一句空态而不是"什么都没有"——
              // 「没有留痕」与「取数失败」必须不同形（#42 那条在这里的另一半）。
              <div data-testid="overview-audit-empty" style={{ padding: 12, color: 'var(--adm-faint)', fontSize: 14 }}>
                {t('audit.empty')}
              </div>
            )
          )}
        </div>
      )}
        </Panel>
      )}
      {/* 用量看板 Tab */}
      {ovTab === 'usage' && (
        <UsageP />
      )}
    </>
  )
}

// ==================== 账户管理面板（Vue Users.vue） ====================

/** 用户列表、行内编辑、创建用户弹窗组件；含组织/角色级联约束 */
export function UsersP() {
  const [, t, tpl] = useT()
  const { isSuper, myLevel, roleOptions, tenants, activeTenantId } = useAdmin()
  // 用户列表数据
  const [rows, setRows] = useState<Any[]>([])
  // 组织列表数据
  const [orgs, setOrgs] = useState<OrgInfo[]>([])
  // 创建用户弹窗开关
  const [dlg, setDlg] = useState(false)
  // 新建用户表单
  // ★ B4-c（R9）：password 一格只在**平台档**出现并必填；企业档改成一格可选邮箱
  //   （预绑给受邀人），提交后后端不建号、只回一枚一次性专属邀请链接。
  const [uForm, setUForm] = useState<Any>({ username: '', password: '', email: '', display_name: '', role: 'user', tenant_id: 1, org_id: 0 })
  // 刚生成的邀请链接回执（非空 ⇒ 打开转发卡片）+ 受邀人邮箱（卡片「发邮件」预填用）
  const [issued, setIssued] = useState<InviteLinkCreateResult | null>(null)
  const [issuedEmail, setIssuedEmail] = useState('')
  // 判据与后端 auth.go 的 tid 分派**同一把尺**：只有 super_admin/admin 会落到平台档（tid=0）并要口令。
  // 刻意不用「租户/组织是否为空」判——那两条为空时后端仍按生效租户走企业档，那是合法形态。
  const isPlatformGrant = isSuper && (uForm.role === 'admin' || uForm.role === 'super_admin')

  /** 加载用户列表 */
  const load = useCallback(async () => {
    const r = await adminUsers()
    if (r.success) setRows(((r as unknown as { users?: Any[] }).users) || [])
  }, [])

  /** 加载组织列表 */
  const loadOrgs = useCallback(async () => {
    const r = await orgList()
    if (r.success) setOrgs((r as unknown as { orgs?: OrgInfo[] }).orgs || [])
  }, [])

  // 初始加载用户与组织列表
  useEffect(() => { void load(); void loadOrgs() }, [load, loadOrgs])
  // 切换租户后重新加载
  useEffect(() => { void load(); void loadOrgs() }, [activeTenantId, load, loadOrgs])

  /** 组织下拉选项：根组织 + 全部子组织（带父级路径） */
  const orgOptions = useMemo(() => {
    const children = orgs.map((o) => ({ id: o.id, name: orgPath(orgs, o) }))
    return [{ id: 0, name: t('users.rootOrgOption') }, ...children]
  }, [orgs, t])

  /** 递归计算组织的完整路径名（如 "根组织 / 部门A / 子部门B"） */
  function orgPath(list: OrgInfo[], o: OrgInfo): string {
    if (o.parent_id === 0) return o.name
    const parent = list.find((x) => x.id === o.parent_id)
    return parent ? `${orgPath(list, parent)} / ${o.name}` : o.name
  }


  /** 级联：可选部门随超管所选租户过滤；角色选项随部门层级收窄 */
  const cascadeOrgs = useMemo(() => {
    if (!isSuper) return orgs
    const tid = Number(uForm.tenant_id || 0)
    return orgs.filter((o: any) => o.tenant_id === tid)
  }, [isSuper, orgs, uForm.tenant_id])

  /** 根据当前选择的组织计算可选角色列表 */
  const cascadeRoles = useMemo<string[]>(() => {
    const oid = Number(uForm.org_id || 0)
    if (!oid) return myLevel >= 3 ? ['tenant_admin', 'dept_admin', 'user'] : ['user']
    const org = orgs.find((x: any) => x.id === oid)
    if (org && org.type === 'root') return myLevel >= 3 ? ['tenant_admin', 'dept_admin', 'user'] : ['user']
    return myLevel >= 2 ? ['dept_admin', 'user'] : ['user']
  }, [orgs, uForm.org_id, myLevel])

  /** 组织/租户变动后，若当前角色不在可用角色列表则回退到最低角色 */
  function onCascadeChange() {
    if (!cascadeRoles.includes(String(uForm.role))) {
      setUForm((p: Any) => ({ ...p, role: cascadeRoles[cascadeRoles.length - 1] || 'user' }))
    }
  }

  /** 创建用户：企业档提交后展示邀请链接（不建号），平台档才直建账号并清空表单 */
  async function createUser() {
    const uname = String(uForm.username || '').trim()
    if (!uname) { void toastWarn(t('org.userNameRequired')); return }
    if (isPlatformGrant && (String(uForm.password || '').length ?? 0) < 6) { void toastWarn(t('org.pwdMinLength')); return }
    const body: Any = {
      username: uname,
      role: uForm.role,
      org_id: Number(uForm.org_id) || undefined,
      tenant_id: Number(uForm.tenant_id) || undefined,
      email: String(uForm.email || '').trim() || undefined,
      // 显示名只有平台档认（企业档由受邀人注册时自填，后端忽略这一格）
      display_name: isPlatformGrant ? (String(uForm.display_name || '').trim() || uname) : undefined,
    }
    // 企业档刻意**不带** password 字段（后端见非空口令直接 400）；传空串虽能过，
    // 但会把「这一档不收口令」这件事在下一次改动里抹平。
    if (isPlatformGrant) body.password = uForm.password
    const r = await adminUserCreate(body)
    if (!r.success) { toastError(r.message); return }
    if (r.invite) {
      // 这一趟没有创建任何账号，只有链接：文案不能说「已开通」，
      // 否则管理员会以为当事人现在已经能登录。
      setIssuedEmail(String(uForm.email || '').trim())
      setIssued(r.invite)
      toastSuccess(tpl('org.inviteIssued', { name: uname }))
    } else {
      toastSuccess(tpl('org.userCreated', { name: uname }))
    }
    setUForm({ username: '', password: '', email: '', display_name: '', role: uForm.role, tenant_id: uForm.tenant_id, org_id: uForm.org_id })
    setDlg(false); void load()
  }

  /** 行内更新用户字段（display_name / role / status / org_id） */
  async function editUser(u: Any, field: string, val: string | number) {
    const data: Any = { display_name: u.display_name, role: u.role, status: u.status, org_id: u.org_id || 0 }
    if (field === 'org_id') data.org_id = Number(val)
    else data[field] = val
    const r = await adminUserUpdate(Number(u.id), data as never)
    if (!r.success) toastError(r.message)
    void load()
  }

  /** 切换用户启用/禁用状态 */
  async function toggleUser(u: Any) {
    const r = await adminUserUpdate(Number(u.id), {
      display_name: u.display_name, role: u.role,
      status: u.status === 'active' ? 'disabled' : 'active', org_id: u.org_id || 0,
    } as never)
    if (!r.success) toastError(r.message)
    void load()
  }

  /** 弹窗重置用户密码 */
  async function resetPwd(u: Any) {
    const pwd = await promptText({ header: t('users.resetPwdPrompt'), body: tpl('users.resetPwdPrompt', { name: String(u.username ?? '') }) })
    if (!pwd) return
    void adminUserResetPassword(Number(u.id), pwd).then((r) => { if (!r.success) toastError(r.message) })
  }

  /**
   * 移除成员（★ 2026-10-24 B4-d，R8+R2 拍板）：确认框**只落在这一个动作函数里**。
   * 旧形态是行内 `<Link onClick={async () => { await adminUserDelete(...) }}>`＝零确认直接删，
   * 而这一条按新模型删的**不是账号**，是「这个人在本企业的那一份身份」——
   * 文案必须把"数据留在企业、个人账号不受影响"说出来（说错了管理员会以为把人整个删了，
   * 反过来当事人以为自己被删号），这也是 `org.deleteUserConfirm` 与 OrgP 侧共用一把尺子的原因。
   */
  async function removeMember(u: Any) {
    if (!(await confirmDialog({ body: tpl('org.deleteUserConfirm', { name: String(u.username ?? '') }), danger: true }))) return
    toastResp(await adminUserDelete(Number(u.id)), t('common.delete'))
    void load()
  }

  return (
    <Panel title={t('users.title')}
      extra={<Button variant="primary" onClick={() => { setUForm({ username: '', password: '', display_name: '', role: 'user', tenant_id: activeTenantId || 1, org_id: 0 }); setDlg(true) }}>{t('users.create')}</Button>}>
      {/* 用户列表表格 */}
      <DataTable<any> rowKey={(row) => String(row.id)} rows={rows}
        columns={[
          { key: 'id', title: t('users.colId'), width: 70 },
          { key: 'username', title: t('users.colUsername') },
          { key: 'display_name', title: t('users.colName'), render: (row) =>
            <input className="lc-input" value={String(row.display_name ?? '')} onChange={(e) => editUser(row, 'display_name', e.target.value)} /> },
          { key: 'org', title: t('users.colOrg'), render: (row) =>
            <select className="lc-select" value={String(Number(row.org_id || 0))} onChange={(e) => editUser(row, 'org_id', Number(e.target.value))}>
              {orgOptions.map((o) => <option key={o.id} value={String(o.id)}>{o.name}</option>)}
            </select> },
          { key: 'role', title: t('users.colRole'), width: 140, render: (row) =>
            <select className="lc-select" value={String(row.role)} onChange={(e) => editUser(row, 'role', e.target.value)}>
              {roleOptions.map((r) => <option key={r} value={r}>{t('users.role.' + r)}</option>)}
            </select> },
          { key: 'status', title: t('users.colStatus'), width: 90, render: (row) =>
            <StatusPill tone={row.status === 'active' ? 'success' : 'idle'}>{row.status === 'active' ? t('users.enable') : row.status === 'disabled' ? t('users.disable') : row.status}</StatusPill> },
          { key: 'last_login_at', title: t('users.colLastLogin'), width: 165, render: (row) => fmtTime(row.last_login_at) },
          { key: 'op', title: t('users.colActions'), width: 200, render: (row) => (
            <div style={{ display: 'flex', gap: 10, alignItems: 'center', flexWrap: 'wrap' }}>
              <Link onClick={() => resetPwd(row)}>{t('users.resetPwd')}</Link>
              <Link tone="danger" onClick={async () => { await toggleUser(row) }}>{row.status === 'active' ? t('users.disable') : t('users.enable')}</Link>
              <Link tone="danger" onClick={() => { void removeMember(row) }}>{t('users.removeMember')}</Link>
            </div>
          ) },
        ]}  />

      {/* 创建用户弹窗 */}
      <Dialog open={dlg} onCancel={() => setDlg(false)} title={t('users.create')}
        onConfirm={async () => { await createUser() }}>
        <Field label={t('users.usernamePlaceholder')}><input className="lc-input" value={String(uForm.username || '')} onChange={(e) => setUForm((p: Any) => ({ ...p, username: e.target.value }))} /></Field>
        {/* ★ B4-c（R9）：企业档不收口令——改一格**可选**邮箱（预绑后只有该邮箱能完成注册）。
            显示名也只在平台档录：企业档由受邀人注册时自填，留这一格会让管理员以为名字已定。 */}
        {isPlatformGrant
          ? <Field label={t('users.passPlaceholder')}><input className="lc-input" type="password" autoComplete="new-password" value={String(uForm.password || '')} onChange={(e) => setUForm((p: Any) => ({ ...p, password: e.target.value }))} /></Field>
          : <Field label={t('users.emailOptionalPlaceholder')}><input className="lc-input" type="email" autoComplete="off" value={String(uForm.email || '')} onChange={(e) => setUForm((p: Any) => ({ ...p, email: e.target.value }))} /></Field>}
        {isPlatformGrant && (
          <Field label={t('users.displayNamePlaceholder')}><input className="lc-input" value={String(uForm.display_name || '')} onChange={(e) => setUForm((p: Any) => ({ ...p, display_name: e.target.value }))} /></Field>
        )}
        <p style={{ fontSize: 14, color: 'var(--adm-faint)', margin: '0 0 10px' }}>
          {t(isPlatformGrant ? 'users.createHintPlatform' : 'users.createHintEnterprise')}
        </p>
        {/* 超管可选择租户 */}
        {isSuper && (
          <Field label={t('users.colOrg')}>
            <select className="lc-select" value={String(Number(uForm.tenant_id || 1))} onChange={(e) => { setUForm((p: Any) => ({ ...p, tenant_id: Number(e.target.value) })); onCascadeChange() }}>
              {tenants.map((tt: any) => <option key={tt.id} value={String(tt.id)}>{tpl('users.orgItem', { id: tt.id, code: tt.code })}</option>)}
            </select>
          </Field>
        )}
        <Field label={t('users.colOrg')}>
          <select className="lc-select" value={String(Number(uForm.org_id || 0))} onChange={(e) => { setUForm((p: Any) => ({ ...p, org_id: Number(e.target.value) })); onCascadeChange() }}>
            {([{ id: 0, name: t('org.rootOption'), type: '' }, ...cascadeOrgs.map((o) => ({ id: o.id, name: o.name, type: o.type }))] as any[]).map((o) => <option key={o.id} value={String(o.id)}>{o.type === 'root' ? ` ${o.name}` : o.name}</option>)}
          </select>
        </Field>
        <Field label={t('users.colRole')}>
          <select className="lc-select" value={String(uForm.role || 'user')} onChange={(e) => setUForm((p: Any) => ({ ...p, role: e.target.value }))}>
            {cascadeRoles.map((r) => <option key={r} value={r}>{t('users.role.' + r)}</option>)}
          </select>
        </Field>
      </Dialog>

      {/* ★ B4-c：企业建号的成功回执就是一枚链接（账号要等本人设置口令后才存在）。
          §6.5 的安全警示由卡片自带，与邀请管理页常驻条同一份实现，这里不再另写一句。 */}
      <Dialog open={!!issued} onCancel={() => setIssued(null)}
              title={t('org.inviteIssuedTitle')} confirmText={t('inviteLink.close')}
              onConfirm={() => setIssued(null)}>
        {issued && <InviteLinkCard link={issued} email={issuedEmail} />}
      </Dialog>
    </Panel>
  )
}

// ==================== 系统告警面板（Vue Alerts.vue） ====================

/** 告警列表 + 注册/触达配置组件（邮件验证、通知、人工审核、验证码、Webhook） */
export function AlertsP() {
  const [, t] = useT()
  const { activeTenantId } = useAdmin()
  // 告警列表数据
  const [rows, setRows] = useState<Any[]>([])
  // 告警状态筛选
  const [status, setStatus] = useState('')
  // ★ F9：级别/类型客户端筛选 + 静音管理
  const [fLevel, setFLevel] = useState('')
  const [fKind, setFKind] = useState('')
  const [silences, setSilences] = useState<{ tenant_id: number; kind: string; until: string }[]>([])
  const [silDlg, setSilDlg] = useState<{ kind: string; tenant_id: number } | null>(null)
  const [silMin, setSilMin] = useState('720')
  // 注册与触达配置表单（布尔以 '0'/'1' 字符串存储以兼容后端）
  const [regCfg, setRegCfg] = useState<Record<string, string | boolean>>({
    email_verify_enabled: '0', email_notify_enabled: '0',
    captcha_provider: '', captcha_site_key: '', captcha_secret_key: '',
    wecom_webhook_url: '', dingtalk_webhook_url: '', slack_webhook_url: '', teams_webhook_url: '',
  })

  /** 加载告警列表 */
  const load = useCallback(async () => {
    const r = await systemAlerts(status || undefined)
    if (r.success) {
      setRows(((r as unknown as { alerts?: Any[] }).alerts) || [])
      setSilences(((r as unknown as { silences?: { tenant_id: number; kind: string; until: string }[] }).silences) || [])
    }
  }, [status])

  /** 加载注册与触达配置 */
  const loadRegCfg = useCallback(async () => {
    const cfg = await runGuarded(() => adminPackageSettings())
    if (!cfg) return
    if (cfg.success) {
      const c = cfg as Any
      for (const k of ['email_verify_enabled', 'email_notify_enabled',
        'captcha_provider', 'captcha_site_key', 'wecom_webhook_url', 'dingtalk_webhook_url', 'slack_webhook_url', 'teams_webhook_url']) {
        if (c[k] !== undefined && c[k] !== '') setRegCfg((p) => ({ ...p, [k]: c[k] }) as Record<string, string | boolean>)
      }
    }
  }, [])

  // 初始化加载告警与注册配置
  useEffect(() => { void load() }, [load])
  useEffect(() => { void loadRegCfg() }, [loadRegCfg])
  // 切换租户后刷新告警
  useEffect(() => { void load() }, [activeTenantId, load])

  /** 将告警标记为已解决 */
  async function resolveAlert(a: Any) {
    await alertResolve(Number(a.id))
    await load()
  }

  // ★ F9：级别/类型可选项（从当前列表数据推导）与过滤视图
  const levelOpts = Array.from(new Set(rows.map((r) => String(r.level)).filter(Boolean)))
  const kindOpts = Array.from(new Set(rows.map((r) => String(r.kind)).filter(Boolean)))
  const viewRows = rows.filter((r) => (!fLevel || r.level === fLevel) && (!fKind || r.kind === fKind))

  /** 静音（弹层选择时长）/解除静音 */
  async function doSilence() {
    if (!silDlg) return
    if (toastResp(await alertSilence(Number(silDlg.tenant_id), silDlg.kind, Number(silMin) || 720), t('alerts.silActive'))) {
      setSilDlg(null)
      await load()
    }
  }
  async function doUnsilence(s: { tenant_id: number; kind: string }) {
    if (await alertUnsilence(Number(s.tenant_id), s.kind)) await load()
  }

  /** Switch 变动时统一把布尔转 '1'/'0' */
  function setSwitch(k: string, v: boolean) {
    setRegCfg((p) => ({ ...p, [k]: v ? '1' : '0' }))
  }

  /** 保存注册/触达配置（布尔开关 + 验证码与 webhook 字段） */
  async function saveRegCfg() {
    const payload: Record<string, string> = {}
    for (const k of ['email_verify_enabled', 'email_notify_enabled']) {
      const val = regCfg[k]
      payload[k] = String(val) === 'true' || val === '1' ? '1' : '0'
    }
    for (const k of ['captcha_provider', 'captcha_site_key', 'wecom_webhook_url', 'dingtalk_webhook_url', 'slack_webhook_url', 'teams_webhook_url']) {
      payload[k] = String(regCfg[k] || '')
    }
    if (regCfg.captcha_secret_key) payload.captcha_secret_key = String(regCfg.captcha_secret_key)
    // React api 签名仅声明部分字段；后端按 system_config 全量接收
    // ★ 2026-10-16 静默失败批：旧写法 `void adminPackageSettingsSave(...)` 是纯 fire-and-forget——
    //   成功没提示、失败也没提示（4xx 还会变成一条未处理 rejection）；改回读结果再出话。
    const r = await adminPackageSettingsSave(payload as never)
    toastResp(r, t('common.saved'))
  }

  return (
    <Panel title={t('alerts.title')}
      extra={<div style={{ display: 'flex', gap: 8 }}>
        <select className="lc-select" value={status} onChange={(e) => setStatus(e.target.value)} style={{ width: 120 }}>
          <option value="">{t('alerts.all')}</option>
          <option value="open">{t('alerts.open')}</option>
          <option value="resolved">{t('alerts.resolved')}</option>
        </select>
        <select className="lc-select" value={fLevel} onChange={(e) => setFLevel(e.target.value)} style={{ width: 110 }}>
          <option value="">{`${t('alerts.fLevel')}·${t('alerts.all')}`}</option>
          {levelOpts.map((l) => <option key={l} value={l}>{l}</option>)}
        </select>
        <select className="lc-select" value={fKind} onChange={(e) => setFKind(e.target.value)} style={{ width: 130 }}>
          <option value="">{`${t('alerts.fKind')}·${t('alerts.all')}`}</option>
          {kindOpts.map((k) => <option key={k} value={k}>{k}</option>)}
        </select>
        <Button variant="secondary" onClick={load}>{t('alerts.refresh')}</Button>
      </div>}>
      {/* 告警列表表格 */}
      {/* ★ F9：生效中的静音规则条 */}
      {silences.length > 0 && (
        <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', marginBottom: 8 }}>
          {silences.map((sv) => (
            <span key={`${sv.tenant_id}:${sv.kind}`} title={t('alerts.silUntil').replace('{t}', fmtTime(sv.until))}>
              <StatusPill tone="idle">
                {sv.kind}@#{sv.tenant_id} · {t('alerts.silUntil').replace('{t}', fmtTime(sv.until))}{' '}
                <Link onClick={() => void doUnsilence(sv)}>{t('alerts.unsilence')}</Link>
              </StatusPill>
            </span>
          ))}
        </div>
      )}
      <DataTable<any> rowKey={(row) => String(row.id)} rows={viewRows}
        columns={[
          { key: 'level', title: t('alerts.colLevel'), width: 90, render: (row) => <StatusPill tone={row.level === 'critical' ? 'danger' : row.level === 'warning' ? 'warn' : 'idle'}>{row.level}</StatusPill> },
          { key: 'kind', title: t('alerts.colKind'), width: 130 },
          { key: 'tenant_id', title: t('alerts.colTenant'), width: 90, render: (row) => `#${row.tenant_id}` },
          { key: 'message', title: t('alerts.colContent'), dim: true, render: (row) => String(row.message ?? '—') },
          { key: 'status', title: t('alerts.colStatus'), width: 90, render: (row) => row.status === 'open' ? t('alerts.open') : t('alerts.resolved') },
          { key: 'created_at', title: t('alerts.colTime'), width: 160, render: (row) => fmtTime(row.created_at) },
          { key: 'op', title: '', width: 150, render: (row) =>
            row.status === 'open'
              ? <div style={{ display: 'flex', gap: 10, alignItems: 'center' }}>
                <Link onClick={() => resolveAlert(row)}>{t('alerts.close')}</Link>
                <Link onClick={() => setSilDlg({ kind: String(row.kind), tenant_id: Number(row.tenant_id) })}>{t('alerts.silence')}</Link>
              </div>
              : <StatusPill tone="success">{t('alerts.resolved')}</StatusPill> },
        ]}  />
      {!viewRows.length && <div style={{ textAlign: 'center', color: 'var(--adm-faint)', padding: 12 }}>{t('alerts.empty')}</div>}

      {/* ★ F9：静音时长选择弹层 */}
      <Dialog title={`${t('alerts.silenceTitle')}（${silDlg?.kind ?? ''}@#${silDlg?.tenant_id ?? ''}）`} open={!!silDlg} onCancel={() => setSilDlg(null)}
        onConfirm={() => void doSilence()} confirmText={t('alerts.silence')} cancelText={t('common.cancel')}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
          <span style={{ fontSize: 15 }}>{t('alerts.silDur')}</span>
          <select className="lc-select" value={silMin} onChange={(e) => setSilMin(e.target.value)} style={{ width: 150 }}>
            <option value="60">{t('alerts.dur1h')}</option>
            <option value="720">{t('alerts.dur12h')}</option>
            <option value="1440">{t('alerts.dur24h')}</option>
            <option value="10080">{t('alerts.dur7d')}</option>
          </select>
        </div>
      </Dialog>

      {/* 注册与触达配置区域 */}
      <Panel title={t('packages.regNotifyTitle')}>
        <div style={{ fontSize: 15, color: 'var(--adm-faint)', marginBottom: 8 }}>{t('packages.regNotifyHint')}</div>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8 }}>
          <Switch checked={regCfg.email_verify_enabled === '1' || regCfg.email_verify_enabled === true} onChange={(e) => setSwitch('email_verify_enabled', e.target.checked)} />
          <span style={{ fontSize: 15, color: 'var(--adm-hint)' }}>{t('packages.emailVerify')}</span>
        </div>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8 }}>
          <Switch checked={regCfg.email_notify_enabled === '1' || regCfg.email_notify_enabled === true} onChange={(e) => setSwitch('email_notify_enabled', e.target.checked)} />
          <span style={{ fontSize: 15, color: 'var(--adm-hint)' }}>{t('packages.emailNotify')}</span>
        </div>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8 }}>
          <span style={{ fontSize: 15, color: 'var(--adm-hint)', minWidth: 130 }}>{t('packages.captchaProvider')}</span>
          <select className="lc-select" value={String(regCfg.captcha_provider || '')} onChange={(e) => setRegCfg((p) => ({ ...p, captcha_provider: e.target.value }))} style={{ width: 160 }}>
            <option value="">{t('packages.captchaOff')}</option>
            <option value="turnstile">Turnstile</option>
          </select>
        </div>
        {/* Turnstile 验证码配置（仅当启用 Turnstile 时显示） */}
        {regCfg.captcha_provider === 'turnstile' && (
          <div style={{ display: 'flex', gap: 8, marginBottom: 8 }}>
            <input className="lc-input" value={String(regCfg.captcha_site_key || '')} placeholder={t('packages.captchaSiteKey')} onChange={(e) => setRegCfg((p) => ({ ...p, captcha_site_key: e.target.value }))} />
            <input className="lc-input" type="password" value={String(regCfg.captcha_secret_key || '')} placeholder={t('packages.captchaSecretKey')} onChange={(e) => setRegCfg((p) => ({ ...p, captcha_secret_key: e.target.value }))} />
          </div>
        )}
        <input className="lc-input" value={String(regCfg.wecom_webhook_url || '')} placeholder={t('packages.wecomWebhook')} onChange={(e) => setRegCfg((p) => ({ ...p, wecom_webhook_url: e.target.value }))} style={{ marginBottom: 8 }} />
        <input className="lc-input" value={String(regCfg.dingtalk_webhook_url || '')} placeholder={t('packages.dingtalkWebhook')} onChange={(e) => setRegCfg((p) => ({ ...p, dingtalk_webhook_url: e.target.value }))} style={{ marginBottom: 8 }} />
        <input className="lc-input" value={String(regCfg.slack_webhook_url || '')} placeholder={t('packages.slackWebhook')} onChange={(e) => setRegCfg((p) => ({ ...p, slack_webhook_url: e.target.value }))} style={{ marginBottom: 8 }} />
        <input className="lc-input" value={String(regCfg.teams_webhook_url || '')} placeholder={t('packages.teamsWebhook')} onChange={(e) => setRegCfg((p) => ({ ...p, teams_webhook_url: e.target.value }))} style={{ marginBottom: 8 }} />
        <Button variant="primary" onClick={saveRegCfg}>{t('common.save')}</Button>
      </Panel>
    </Panel>
  )
}

// ==================== 审计日志面板（Vue Audit.vue） ====================

/** 系统审计日志组件：按操作类型、日期范围筛选并导出 CSV */
export function AuditP() {
  const [, t, tpl] = useT()
  // 审计日志列表数据
  const [rows, setRows] = useState<Any[]>([])
  // 筛选条件：操作类型、开始日期、结束日期
  const [fAction, setFAction] = useState('')
  const [fFrom, setFFrom] = useState('')
  const [fTo, setFTo] = useState('')

  // 支持的审计动作类型清单（用于 Select 筛选）
  const actions = ['login', 'user_create', 'user_update', 'user_delete', 'user_reset_pwd',
    'org_create', 'org_rename', 'org_delete', 'kb_package_create', 'kb_package_status',
    'kb_entries_import', 'model_save', 'stage_models_save', 'package_subscribe']

  // 后端列表腿此刻套的是哪一档缺省时间界（0＝没套，即调用方显式给了 from）
  // ★ 2026-11-04 体验批 A3：这一维必须上屏幕。界是服务端加的，本地筛看不见它——
  //   不报出来，「30 天前没有留痕」与「留痕被界挡住了」在用户脸上完全同形（AGENTS「空态不许伪装成检查过」）。
  const [windowDays, setWindowDays] = useState(0)

  /** 拉取审计日志（★ A3：动作/日期一律交服务端判，前端不再本地二次过滤）
   *  旧写法 `systemAudit()` 不带参数 ＋ 拿回 100 条在浏览器里 filter，
   *  两个后果：① 筛的其实是"最新 100 条里的命中"，用户以为筛到了全部；② 服务端加缺省界后
   *  会把更早的记录直接挡在请求之外，而本地 filter 给不出任何解释。 */
  const load = useCallback(async () => {
    const r = await runGuarded(() => systemAudit({
      action: fAction || undefined,
      from: fFrom || undefined,
      to: fTo || undefined,
    }))
    if (r?.success) {
      setRows(((r as unknown as Any).logs as Any[]) || [])
      setWindowDays(Number((r as unknown as Any).window_days ?? 0) || 0)
    }
  }, [fAction, fFrom, fTo])
  useEffect(() => { void load() }, [load])

  /** 导出 CSV：改走共用入口 exportCsvFile（原裸 fetch 无超时保护且失败各弹各的，现与另两处合并） */
  function exportCsv() {
    exportCsvFile({
      url: `${API_BASE}/api/system/audit?export=csv`,
      filename: `audit_${new Date().toISOString().slice(0, 10)}.csv`,
      failMsg: (code) => code ? tplFn('common.exportFailCode', { code }) : t('common.exportFail'),
    })
  }

  return (
    <Panel title={t('audit.title')}
      extra={<Button onClick={exportCsv}>{t('audit.export')}</Button>}>
      <p className="ad-hint" style={{ fontSize: 15, color: 'var(--adm-faint)', margin: '0 0 8px' }}>{t('audit.hint')}</p>
      {/* 筛选条件：操作类型、日期范围 */}
      <div style={{ display: 'flex', gap: 8, marginBottom: 8, alignItems: 'center', flexWrap: 'wrap' }}>
        <select className="lc-select" value={fAction} onChange={(e) => setFAction(e.target.value)} style={{ width: 180 }}>
          <option value="">{t('audit.allActions')}</option>
          {actions.map((a) => <option key={a} value={a}>{auditActionLabel(a)}</option>)}
        </select>
        <input type="date" value={fFrom} onChange={(e) => setFFrom(e.target.value)} style={{ height: 30, border: '1.2px solid var(--adm-line)', borderRadius: 8, padding: '0 8px', width: 150 }} />
        <span style={{ color: 'var(--adm-faint)' }}>→</span>
        <input type="date" value={fTo} onChange={(e) => setFTo(e.target.value)} style={{ height: 30, border: '1.2px solid var(--adm-line)', borderRadius: 8, padding: '0 8px', width: 150 }} />
        <Button variant="secondary" onClick={load}>{t('common.refresh')}</Button>
      </div>

      {/* ★ A3（2026-11-04）缺省时间界必须出声：界是服务端加的，没有这一行，
          "这一档没有留痕"与"留痕被 30 天界挡住"在用户脸上完全同形。
          显式设了开始日期（或走导出）时 window_days 回 0 ⇒ 这一行不出现，不制造噪音。 */}
      {windowDays > 0 && (
        <div data-testid="audit-window-hint" style={{ fontSize: 13, color: 'var(--adm-faint)', margin: '0 0 8px' }}>
          {tpl('audit.windowHint', { days: windowDays })}
        </div>
      )}
      {/* 审计日志表格 */}
      <DataTable<any> rowKey={(row) => String(row.id)} rows={rows}
        columns={[
          { key: 'created_at', title: t('overview.colTime'), width: 165, render: (row) => fmtTime(row.created_at) },
          { key: 'operator', title: t('audit.operator'), width: 140, render: (row) => row.username || row.user_id || '—' },
          { key: 'action', title: t('overview.colAction'), width: 150 },
          { key: 'resource', title: t('overview.colResource'), width: 110 },
          { key: 'detail', title: t('overview.colDetail'), dim: true, render: (row) => String(row.detail ?? '—') },
          { key: 'change', title: t('overview.colChange'), dim: true, render: (row) =>
            (row.before_val && row.after_val)
              ? tpl('overview.diffOldNew', { old: shortDiffJSON(row.before_val), new: shortDiffJSON(row.after_val) })
              : '—' },
        ]}  />
      {!rows.length && <div style={{ textAlign: 'center', color: 'var(--adm-faint)', padding: 12 }}>{t('audit.empty')}</div>}
    </Panel>
  )
}

// ==================== 用量看板面板（Vue Usage.vue） ====================

/** 个人用量 / 系统用量（组织）/ 模型成本组件：逐类渲染，剥离 success 元字段。
 *  ★ 2026-09-05 升级：支持「自定义日期区间」查询（from/to 分别透传后端；
 *    TDesign DateRangePicker 选择任一起止日期，1 天/3 天/任意区间均可）。 */
export function UsageP() {
  const [, t, tpl] = useT()
  // 个人用量数据
  const [me, setMe] = useState<Any | null>(null)
  // 组织用量数据
  const [org, setOrg] = useState<Any | null>(null)
  // 模型成本数据
  const [cost, setCost] = useState<Any | null>(null)
  // 用量看板默认打开「个人用量」
  const [usageTab, setUsageTab] = useState<'me' | 'org' | 'cost'>('me')
  // 按日/区间查询：from/to=YYYY-MM-DD（均空=累计+当日口径；选自选区间如近1天/近3天/任意）
  const [usageFrom, setUsageFrom] = useState('')
  const [usageTo, setUsageTo] = useState('')

  // 并行拉取三类用量数据（剔除 success/message 等接口元字段）；日期变化时重新拉取
  // ★ #42：三段空 catch 换 runGuarded——失败时最坏的表现是「成本」页签整块凭空消失
  //   （cost 为 null 即不渲染该页签）而没有任何原因；分开兜底保留「一块失败不清空另一块」的旧语义。
  useEffect(() => {
    void (async () => {
      const me = await runGuarded(() => usageMe(usageFrom || undefined, usageTo || undefined))
      if (me?.success) { const { success, message, ...d } = me as Any; setMe(d) }
      const org = await runGuarded(() => usageOrg(undefined, usageFrom || undefined, usageTo || undefined))
      if (org?.success) { const { success, message, ...d } = org as Any; setOrg(d) }
      const cost = await runGuarded(() => usageCost())
      if (cost?.success) { const { success, message, ...d } = cost as Any; setCost(d) }
    })()
  }, [usageFrom, usageTo])

  /** 个人用量：基础费用/句数指标卡片 */
  // ★ 积分口径：total/today/points_available 出参已是积分，卡片补「积分」单位；
  //   句数/次数类保持原值；日期字符串（from/to/date）不再当指标卡渲染；字段给中文标签。
  const ME_POINTS_FIELDS = ['total', 'today', 'points_available']
  const meCards = (d: Any) => !d ? <EmptyState title="—" /> : (
    <div className="stat-grid">
      {Object.entries(d).filter(([, v]) => typeof v === 'number').map(([k, v]) => (
        <div key={k} className="stat-card">
          <div style={{ fontSize: 14, color: 'var(--adm-faint)' }}>{t('usage.field.' + k) !== 'usage.field.' + k ? t('usage.field.' + k) : k}</div>
          {ME_POINTS_FIELDS.includes(k)
            ? <b>{fmtPoints(Number(v))} <span style={{ fontSize: 14, fontWeight: 400, color: 'var(--adm-faint)' }}>{t('ss2.unitPoints')}</span></b>
            : <b>{fmtNum(Number(v))}</b>}
        </div>
      ))}
    </div>
  )

  /** 系统用量：组织下用户成本明细表 + 合计 */
  const orgTable = (d: Any) => !d ? <EmptyState title="—" /> : (
    <div>
      <p style={{ fontSize: 15, color: 'var(--adm-hint)', margin: '0 0 8px' }}>{tpl('usage.orgTotal', { n: fmtPoints(Number(d.total) || 0) })}</p>
      <DataTable<any> rowKey={(row) => String(row.id)} rows={d.users || []}
        columns={[
          { key: 'username', title: t('usage.colUser'), width: 160 },
          { key: 'display_name', title: t('usage.colName'), width: 160 },
          { key: 'org_name', title: t('usage.colOrg'), width: 160 },
          { key: 'cost', title: t('usage.colCost'), width: 120, render: (row) => fmtPoints(Number(row.cost) || 0) },
        ]}  />
    </div>
  )

  /** 模型成本：平台承担成本单列 + 按模型的成本/用量两张表 */
  // ★ 2026-09-29 〇-AD：`platform` 块只在本接口（/api/billing/usage/cost，level 4 专属）
  //   回吐，租户/个人用户既取不到也不渲染——他们看到的「消耗」从此等于真扣掉的积分。
  //   空对象＝后端这次聚合失败（不回吐 0 假数），整块不渲染，比「平台承担 0 积分」诚实。
  const costTables = (d: Any) => !d ? <EmptyState title="—" /> : (
    <div style={{ display: 'flex', flexWrap: 'wrap', gap: 16 }}>
      {!!d.platform && Object.keys(d.platform).length > 0 && (
        <div style={{ flex: '1 1 100%', minWidth: 320 }}>
          <h4 style={{ fontSize: 16, margin: '4px 0' }}>{t('usage.platformTitle')}</h4>
          <p style={{ fontSize: 14, color: 'var(--adm-faint)', margin: '0 0 8px' }}>{t('usage.platformHint')}</p>
          <div className="stat-grid">
            {[
              ['total', 'usage.platformTotal'], ['policy_borne', 'usage.platformPolicy'],
              ['other_log', 'usage.platformOtherLog'], ['settled', 'usage.platformSettled'],
            ].map(([k, label]) => (
              <div key={k} className="stat-card">
                <div style={{ fontSize: 14, color: 'var(--adm-faint)' }}>{t(label)}</div>
                <b>{fmtPoints(Number(d.platform?.[k]) || 0)} <span style={{ fontSize: 14, fontWeight: 400, color: 'var(--adm-faint)' }}>{t('ss2.unitPoints')}</span></b>
              </div>
            ))}
          </div>
          <div style={{ display: 'flex', flexWrap: 'wrap', gap: 16, marginTop: 8 }}>
            <div style={{ flex: 1, minWidth: 280 }}>
              <DataTable<any> rowKey={(row) => String(row.k)}
                rows={Object.entries(d.platform.by_reason || {}).map(([k, v]) => ({ k, v }))}
                columns={[{ key: 'k', title: t('usage.platformReason') }, { key: 'v', title: t('usage.colCost'), render: (row) => fmtPoints(Number(row.v)) }]}  />
            </div>
            <div style={{ flex: 1, minWidth: 280 }}>
              <DataTable<any> rowKey={(row) => String(row.k)}
                rows={Object.entries(d.platform.by_model || {}).map(([k, v]) => ({ k, v }))}
                columns={[{ key: 'k', title: t('usage.colModel') }, { key: 'v', title: t('usage.colCost'), render: (row) => fmtPoints(Number(row.v)) }]}  />
            </div>
          </div>
        </div>
      )}
      <div style={{ flex: 1, minWidth: 320 }}>
        <h4 style={{ fontSize: 16, margin: '4px 0' }}>{t('usage.costBy')}</h4>
        <DataTable<any> rowKey={(row) => String(row.k)} rows={Object.entries(d.costs || {}).map(([k, v]) => ({ k, v }))}
          columns={[{ key: 'k', title: t('usage.colModel') }, { key: 'v', title: t('usage.colCost'), render: (row) => fmtPoints(Number(row.v)) }]}  />
      </div>
      <div style={{ flex: 1, minWidth: 320 }}>
        <h4 style={{ fontSize: 16, margin: '4px 0' }}>{t('usage.quantBy')}</h4>
        <DataTable<any> rowKey={(row) => String(row.k)} rows={Object.entries(d.quants || {}).map(([k, v]) => ({ k, v }))}
          columns={[{ key: 'k', title: t('usage.colModel') }, { key: 'v', title: t('usage.colCount'), render: (row) => fmtNum(row.v) }]}  />
      </div>
    </div>
  )

  // ★ P2 报表导出（2026-09-15，见《P0P2待办核实报告_20260915.md》P2-2）：
  // 用量明细 CSV 导出——与面板同一鉴权口径（Bearer token + X-Tenant-ID 超管切换），
  // 日期区间跟随当前选择（from/to 空=全量近 2 万行）；后端 ?export=csv 流式返回。
  function exportUsageCSV() {
    const qs = new URLSearchParams({ export: 'csv' })
    if (usageFrom) qs.set('from', usageFrom)
    if (usageTo) qs.set('to', usageTo)
    exportCsvFile({
      url: `${API_BASE}/api/billing/usage?${qs.toString()}`,
      filename: `usage_${usageFrom || 'all'}_${usageTo || 'now'}.csv`,
      failMsg: () => t('overview.exportFailed'),
    })
  }

  return (
    <Panel title={t('usage.dashboardTitle')}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 12, flexWrap: 'wrap' }}>
        <span style={{ fontSize: 15, color: 'var(--adm-hint)' }}>{t('usage.dateQuery')}</span>
        {/* 腾讯 TDesign 日期范围选择器（2026-09-05）：任选起止日期 → 分别写入 usageFrom/usageTo，
            空=累计+当日口径（后端 from/to 均缺省）；单日区间可视同按日查询 */}
        <input className="lc-input" type="date" value={usageFrom} placeholder={t('usage.dateFrom')}
          onChange={(e) => setUsageFrom(e.target.value)} style={{ width: 150 }} />
        <span style={{ color: 'var(--adm-hint)' }}>–</span>
        <input className="lc-input" type="date" value={usageTo} placeholder={t('usage.dateTo')}
          onChange={(e) => setUsageTo(e.target.value)} style={{ width: 150 }} />
        {(usageFrom || usageTo) && (
          <Button size="sm" variant="secondary" onClick={() => { setUsageFrom(''); setUsageTo('') }}>{t('usage.dateClear')}</Button>
        )}
        {(usageFrom || usageTo) && <StatusPill tone="idle"> {usageFrom || usageTo}{usageFrom && usageTo && usageFrom !== usageTo ? ` ~ ${usageTo}` :''}</StatusPill>}
        {/* 明细导出按钮：跟随当前日期区间；鉴权与列脱敏在后端统一处理 */}
        <Button size="sm" variant="secondary" onClick={exportUsageCSV}>{t('usage.exportCsv')}</Button>
      </div>
      <Tabs activeKey={usageTab} onChange={(k) => setUsageTab(k as 'me' | 'org' | 'cost')} items={[
        { key: 'me', label: t('usage.tabMine') },
        { key: 'org', label: t('usage.tabOrg') },
        ...(cost ? [{ key: 'cost', label: t('usage.tabCost') }] : []),
      ]} />
      {usageTab === 'me' && meCards(me)}
      {usageTab === 'org' && orgTable(org)}
      {usageTab === 'cost' && cost && costTables(cost)}
    </Panel>
  )
}

// ==================== 邀请码面板（Vue Invites.vue） ====================

/** 邀请码列表与创建组件：可指定归属租户或新建组织 */
export function InvitesP() {
  const [, t, tpl] = useT()
  const { tenants, activeTenantId, isSuper } = useAdmin()
  // 邀请码列表数据
  const [rows, setRows] = useState<Any[]>([])
  // 创建邀请码弹窗开关
  const [dlg, setDlg] = useState(false)
  // 新建邀请码表单
  const [code, setCode] = useState('')
  // 归属租户 id（仅超管可选；企业用户强制绑定本企业）
  const [tenantId, setTenantId] = useState(0)

  /** 加载邀请码列表（兼容后端返回 invite_codes / codes 两种字段名） */
  const load = useCallback(async () => {
    const r = await inviteCodes()
    if (r.success) setRows(((r as unknown as { invite_codes?: Any[] }).invite_codes) || ((r as unknown as { codes?: Any[] }).codes) || [])
  }, [])
  // 初始加载与租户切换后刷新
  useEffect(() => { void load() }, [load])
  useEffect(() => { void load() }, [activeTenantId, load])

  // ★ G9（2026-10-19）：撤销邀请码。误发的码过去无法作废（面板只能生成/查看）。
  //   确认框**只此一处**（行内 Link 不再叠一层，见 admin/doubleConfirmGate.test.ts）。
  //   后端 revoke 端点尚缺 ⇒ 打到即回结构化失败，这里按诚实错误提示（不谎报成功）。
  //   只对「未使用且未撤销」的行开放此动作（已使用的码保留用于追溯，不删行）。
  async function revokeCode(row: Any) {
    if (!(await confirmDialog({ body: tpl('invites.revokeConfirm', { code: String(row.code ?? '') }), danger: true }))) return
    const r: any = await bizResp(() => request('/api/admin/invite-codes/revoke', {
      method: 'POST',
      headers: authHeaders(),
      body: JSON.stringify({ id: row.id }),
    }))
    if (r.success) { toastSuccess(t('invites.revokeDone')); void load() }
    else toastError(r.message)
  }

  return (
    <Panel title={t('invites.title')}
      extra={<Button variant="primary" onClick={() => { setCode(''); setTenantId(0); setDlg(true) }}>{t('invites.create')}</Button>}>
      <p className="ad-hint" style={{ fontSize: 15, color: 'var(--adm-faint)', margin: '0 0 8px' }}>{t('invites.hint')}</p>
      {/* ★ §6.5 第 5 条（2026-10-25 对账批）：手输码与专属链接**同一风险面**（后端共用 invite_codes
          那一台状态机，见 store/invite.go 的 Kind 注释），所以警示条必须也出在这一面。
          组件调用点，不抄文案字面量——警示语只允许有 InviteLinkSecurityNote 一个出处。 */}
      <InviteLinkSecurityNote />
      {/* 邀请码列表表格 */}
      <DataTable<any> rowKey={(row) => String(row.id)} rows={rows}
        columns={[
          { key: 'code', title: t('invites.colCode'), render: (row) => (
            // §6.5 之三同口径：只有**还没花掉**的凭据才需要挂"这是一条凭据"（已用/已撤销的行不再提示）
            <span style={{ display: 'inline-flex', gap: 6, alignItems: 'center' }}>
              <span>{String(row.code ?? '')}</span>
              {Number(row.used) !== 1 && !row.revoked && (
                <StatusPill tone="warn">{t('inviteLink.riskTag')}</StatusPill>
              )}
            </span>
          ) },
          { key: 'tenant_id', title: t('invites.colTenant'), width: 110, render: (row) => row.tenant_id > 0 ? `#${row.tenant_id}` : t('invites.newOrg') },
          { key: 'used', title: t('invites.colStatus'), width: 90, render: (row) => {
            // ★ G9：三态——已撤销 > 已使用 > 未使用（撤销只作废未使用的码，已使用的码保留追溯）
            const revoked = !!row.revoked
            const used = Number(row.used) === 1
            return (
              <StatusPill tone={revoked ? 'danger' : used ? 'idle' : 'success'}>
                {revoked ? t('invites.revoked') : used ? t('invites.used') : t('invites.unused')}
              </StatusPill>
            )
          } },
          { key: 'used_by', title: t('invites.colUsedBy'), width: 140 },
          { key: 'created_at', title: t('invites.colCreatedAt'), width: 165, render: (row) => fmtTime(row.created_at) },
          { key: 'used_at', title: t('invites.colUsedAt'), width: 165, render: (row) => fmtTime(row.used_at) },
          // ★ G9：撤销动作列——只对「未使用且未撤销」的行开放（其余行显示占位，行本身不删）
          { key: 'op', title: t('invites.colActions'), width: 90, render: (row) => (
            (Number(row.used) === 1 || row.revoked)
              ? <span style={{ color: 'var(--adm-faint)' }}>—</span>
              : <Link tone="danger" onClick={() => { void revokeCode(row) }}>{t('invites.revoke')}</Link>
          ) },
        ]}  />
      {!rows.length && <div style={{ textAlign: 'center', color: 'var(--adm-faint)', padding: 12 }}>{t('invites.empty')}</div>}

      {/* 创建邀请码弹窗 */}
      <Dialog open={dlg} onCancel={() => setDlg(false)} title={t('invites.create')} onConfirm={async () => {
        if (!code.trim()) { void toastWarn(t('invites.codeRequired')); return }
        // 企业用户（非超管）创建邀请码只能绑定本企业，忽略前端选择的租户
        const tid = isSuper ? tenantId : activeTenantId
        const r = await inviteCodeCreate({ code: code.trim(), tenant_id: tid })
        if (toastResp(r, t('invites.create'))) { setDlg(false); void load() }
      }}>
        <Field label={t('invites.codePlaceholder')}><input className="lc-input" value={code} onChange={(e) => setCode(e.target.value)} /></Field>
        {/* 仅超管可选择归属租户；企业用户强制绑定本企业 */}
        {isSuper && (
          <Field label={t('invites.colTenant')}>
            <select className="lc-select" value={String(tenantId)} onChange={(e) => setTenantId(Number(e.target.value))}>
              <option value="0">{t('invites.newOrg')}</option>
              {tenants.map((tt: any) => <option key={tt.id} value={String(tt.id)}>{`${tt.name} (#${tt.id})`}</option>)}
            </select>
          </Field>
        )}
      </Dialog>
    </Panel>
  )
}

// ==================== 协议签署面板（Vue 无对应，新增后台面板） ====================

/** 列出全部用户，展示是否已阅读并同意《用户协议》与《隐私协议》及签署时间 */
export function AgreementsP() {
  const [, t, tpl] = useT()
  // 用户列表数据
  const [rows, setRows] = useState<Any[]>([])
  // 表格加载中状态
  const [, setLoading] = useState(false)

  /** 加载用户列表（获取协议签署状态） */
  const load = useCallback(async () => {
    setLoading(true)
    try {
      const r = await adminUsers()
      if (r.success) setRows(((r as unknown as { users?: Any[] }).users) || [])
    } finally { setLoading(false) }
  }, [])
  useEffect(() => { void load() }, [load])

  // 统计已签署协议的用户数
  const signed = rows.filter((u: Any) => (u.agreed_at || '').trim() !== '').length

  // 表格列定义
  const columns = [
    { key: 'username', title: t('agreements.user'), width: 160 },
    { key: 'role', title: t('agreements.role'), width: 130, render: (row: Any) => roleName(row.role) },
    { key: 'email', title: t('agreements.email'), width: 200 },
    {
      key: 'status', title: t('agreements.status'), width: 110,
      render: (row: Any) => {
        const ok = (row.agreed_at || '').trim() !== ''
        return <StatusPill tone={ok ? 'success' : 'warn'}>{ok ? t('agreements.signed') : t('agreements.unsigned')}</StatusPill>
      },
    },
    {
      key: 'agreed_at', title: t('agreements.signedAt'), width: 180,
      render: (row: Any) => (row.agreed_at ? fmtTime(row.agreed_at) : '—'),
    },
  ]

  return (
    <Panel title={t('agreements.title')}>
      <div style={{ color: 'var(--adm-hint)', marginBottom: 12 }}>{t('agreements.desc')}</div>
      <div style={{ marginBottom: 12 }}>{tpl('agreements.total', { n: rows.length, m: signed })}</div>
      <DataTable<any> rowKey={(row) => String(row.id)} rows={rows as never} columns={columns}  />
    </Panel>
  )
}
