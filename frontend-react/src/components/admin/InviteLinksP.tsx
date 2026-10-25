// ============================================================================
// components/admin/InviteLinksP.tsx — 企业邀请链接管理面板（★ 多身份方案 B4-c）
// 职责：列本租户（超管为当前上下文租户）的全部专属邀请链接，按后端派生的四档状态分组，
//       并提供生成／复制／二维码／撤销／重新生成五个动作。
//
// 与「邀请码管理」（panels_a 的 InvitesP）为什么是两块面板而不是加一列：
//   §6.4 命名隔离——手输码是"一个谁都能拿来注册的字符串、发给过谁不知道"，
//   专属链接是"系统生成、预绑了某个用户名与部门、用一次即失效的一次性凭证"。
//   两者共用同一张 invite_codes 表与同一台状态机，但**语义与安全等级都不同**，
//   混排成一张表会让人以为撤销链接＝撤销码（或反之），所以两块面板、两套文案、互不引用。
//
// 四条硬口径（改本件前必读）：
// ① **状态只读后端派生的 `status`，前端不重算四档**。过期判定要吃 `expires_at` 与服务器时钟，
//    客户端时间不可信（改系统时间就能把"已过期"看成"待使用"）；后端那一份是唯一尺子
//    （`store.InviteCode.Status()`），这里只做分组，不做判定。
// ② **每行都带 §6.5 风险标识**，页面顶部还有一条常驻警示条（`InviteLinkSecurityNote`）。
//    警示语只有一份实现——「创建后即时警示框」与「本页常驻条」若各写一遍，迟早一面提示一面沉默。
// ③ **撤销确认只落在动作函数内这一处**（AGENTS §一·5 双确认那条 + `doubleConfirmGate.test.ts`）：
//    行内 Link 不再叠一层 confirmDialog。
// ④ **链接串由服务端出栈**（`row.url`），本页禁止拼 `${location.host}/?invite=`：
//    host 的取法是「配置 ＞ 该租户品牌域 ＞ 主站 ＞ 请求 Host」，前端拿到的那一个是门面域名，
//    给品牌域租户发出去的链接会把受邀人送到错误的域名。
//
// 「重新生成」不是改库里的旧行：它拿同一份预绑信息（用户名/部门/角色/邮箱）新建一枚，
//   旧行留在原档位（已撤销／已过期）可追溯——覆盖旧行会把"谁在什么时候发过哪一枚"这条留痕抹掉。
// ============================================================================
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Button, DataTable, Dialog, Icon, Link, StatusPill, Tabs } from '@/ui/langcross/src'
import { confirmDialog } from '@/components/uiDialogs'
import {
  inviteLinks, inviteLinkCreate, inviteCodeRevoke, orgList, fetchInviteLinkQrBlob,
  type InviteLinkCreateResult, type InviteLinkRow, type OrgInfo,
} from '@/api'
import { useAdmin } from '@/stores/admin'
import { useT } from '@/i18n'
import { toastError, toastSuccess, toastWarn } from '@/lib/toastBus'
import { fmtTime } from '@/lib/ui'
import { Panel, Field } from './parts'
import { InviteLinkCard, InviteLinkSecurityNote, copyTextWithFallback } from './InviteLinkCard'

// 图标在文字档内的统一基线（与其余管理台面同档，不为本台另立视觉规格）
const MI = { fontSize: 14, verticalAlign: '-2px', marginInlineEnd: 6 }

/** 四档展示顺序（分组键＝后端 status 原值；未知值一律并进「待使用」以外的那三档之后，不丢行） */
const BUCKETS: Array<{ key: string; labelKey: string }> = [
  { key: 'active', labelKey: 'inviteLink.tabActive' },
  { key: 'used', labelKey: 'inviteLink.tabUsed' },
  { key: 'revoked', labelKey: 'inviteLink.tabRevoked' },
  { key: 'expired', labelKey: 'inviteLink.tabExpired' },
]

/** 后端派生的四档已知集合。第五档＝「后端加了档、前端还没跟上」那一族退化形态：
 *  行不丢（并进 expired 档看得见），但胶囊必须说一句人话，不许在界面上显示裸 key。 */
const KNOWN_STATUS = BUCKETS.map((b) => b.key)

// 面板内跨档传递的行读数：后端字段仍在演进（status/url/用量计数等），
// 这里刻意不做前端类型收窄，取数层给什么就照原样渲染，缺字段由各自的可选链兜住。
type Any = any

/** 企业邀请链接面板：四档分组 + 生成/撤销/重新生成 */
export function InviteLinksP() {
  const [, t, tpl] = useT()
  const { isSuper, activeTenantId, myLevel } = useAdmin()
  // 链接列表（后端已按 kind='link' 过滤，手输码不在这一面）
  const [rows, setRows] = useState<InviteLinkRow[]>([])
  // 当前档位 tab
  const [tab, setTab] = useState('active')
  // 生成弹窗开关与表单
  const [dlg, setDlg] = useState(false)
  const [form, setForm] = useState<Any>({ username: '', org_id: 0, role: 'user', email: '', expires_days: 7 })
  // 部门下拉数据（预绑部门用，与「账户管理」面板同源同尺：都取 orgList）
  const [orgs, setOrgs] = useState<OrgInfo[]>([])
  // 刚生成的那一枚（卡片带 §6.5 警示框）
  const [issued, setIssued] = useState<InviteLinkCreateResult | null>(null)
  // 卡片要用的收件邮箱：表单在提交成功后立刻清空，所以那一刻的值必须单独存一份给回执卡片，
  // 否则「发邮件」这一钮会拼出一个空收件人（tenant_name 顶上去更是把企业名当邮箱，属自伤）。
  const [issuedEmail, setIssuedEmail] = useState('')
  // 行内二维码的 objectURL 池：id → url（卸载整池释放，同 §4.2-1 blob 纪律）
  const qrPool = useRef<Record<number, string>>({})
  const [qrMap, setQrMap] = useState<Record<number, string>>({})

  useEffect(() => () => {
    Object.values(qrPool.current).forEach((u) => URL.revokeObjectURL(u))
    qrPool.current = {}
  }, [])

  /** 拉列表（诚实失败：取数失败不把「一条都没有」画出来） */
  const load = useCallback(async () => {
    const r = await inviteLinks()
    if (r.success) setRows(((r as Any).links as InviteLinkRow[]) || [])
    else toastError(r.message || t('inviteLink.loadFail'))
  }, [t])

  const loadOrgs = useCallback(async () => {
    const r = await orgList()
    if (r.success) setOrgs(((r as Any).orgs as OrgInfo[]) || [])
  }, [])

  useEffect(() => { void load(); void loadOrgs() }, [load, loadOrgs])
  // 切换租户上下文后重新加载（超管平台视图与单租户视图是两份数据）
  useEffect(() => { void load(); void loadOrgs() }, [activeTenantId, load, loadOrgs])

  /** 按后端 status 分组；不认识的档位单独留在一侧，绝不静默丢行 */
  const grouped = useMemo(() => {
    const g: Record<string, InviteLinkRow[]> = { active: [], used: [], revoked: [], expired: [] }
    for (const row of rows) {
      const k = KNOWN_STATUS.includes(row.status) ? row.status : 'expired'
      g[k].push(row)
    }
    return g
  }, [rows])

  /** 可授角色：与「账户管理」面板的级联同一把尺子（部门管理员必须绑部门由后端兜） */
  const roleOptions = useMemo<string[]>(
    () => (myLevel >= 3 ? ['tenant_admin', 'dept_admin', 'user'] : myLevel >= 2 ? ['dept_admin', 'user'] : ['user']),
    [myLevel],
  )

  /** 生成一枚新链接（R9：只录用户名 + 部门/角色，邮箱可空、不录口令） */
  async function createLink() {
    const username = String(form.username || '').trim()
    if (!username) { void toastWarn(t('inviteLink.userRequired')); return }
    const r = await inviteLinkCreate({
      username,
      org_id: Number(form.org_id || 0),
      role: String(form.role || 'user'),
      email: String(form.email || '').trim(),
      expires_days: Number(form.expires_days || 0) || undefined,
      tenant_id: isSuper ? Number(activeTenantId || 0) : undefined,
    })
    if (!r.success) { toastError(r.message); return }
    setDlg(false)
    setIssuedEmail(String(form.email || '').trim())
    setForm({ username: '', org_id: 0, role: 'user', email: '', expires_days: 7 })
    // 回执缺 url 时不弹卡片：一张"链接栏是空的"卡片比一句报错更坏（会让人以为生成失败又点一次）
    if (!r.url) { void toastWarn(t('inviteLink.noUrlInResp')) }
    else setIssued(r)
    void load()
  }

  /** 撤销一枚（确认框只在此函数体内一次；行内 Link 不再叠一层） */
  async function revokeLink(row: InviteLinkRow) {
    if (!(await confirmDialog({ body: tpl('inviteLink.revokeConfirm', { name: row.bound_username || row.code }), danger: true }))) return
    const r = await inviteCodeRevoke(row.id)
    if (!r.success) { toastError(r.message); return }
    void toastSuccess(t('inviteLink.revokeDone'))
    void load()
  }

  /** 重新生成＝按同一份预绑信息新建一枚（旧行保留可追溯，见文件头那条） */
  async function regenerate(row: InviteLinkRow) {
    const r = await inviteLinkCreate({
      username: row.bound_username,
      org_id: Number(row.org_id || 0),
      role: row.role || 'user',
      email: row.bound_email || '',
      tenant_id: isSuper ? Number(row.tenant_id || activeTenantId || 0) : undefined,
    })
    if (!r.success) { toastError(r.message); return }
    void toastSuccess(t('inviteLink.regenerated'))
    if (r.url) { setIssuedEmail(row.bound_email || ''); setIssued(r) }
    void load()
  }

  async function copyRow(row: InviteLinkRow) {
    if (await copyTextWithFallback(row.url || '')) void toastSuccess(t('inviteLink.copied'))
    else void toastError(t('inviteLink.copyFail'))
  }

  /** 行内取二维码：死链由后端那句 400 文案直接顶出来，不放一张空图 */
  async function showQr(row: InviteLinkRow) {
    if (qrPool.current[row.id]) return
    try {
      const blob = await fetchInviteLinkQrBlob(row.id)
      const u = URL.createObjectURL(blob)
      qrPool.current[row.id] = u
      setQrMap((m) => ({ ...m, [row.id]: u }))
    } catch (e) {
      void toastError((e as Error)?.message || t('inviteLink.qrFail'))
    }
  }

  return (
    <>
      <Panel title={t('inviteLink.title')}
        extra={<Button variant="primary" onClick={() => { setDlg(true) }}>{t('inviteLink.create')}</Button>}>
        {/* §6.5 之二：常驻警示条（不随滚动消失、不是一次性 toast） */}
        <InviteLinkSecurityNote />
        <p style={{ fontSize: 15, color: 'var(--adm-faint)', margin: '0 0 8px' }}>{t('inviteLink.hint')}</p>

        <div style={{ marginBottom: 10 }}>
          <Tabs activeKey={tab} onChange={(k) => setTab(k as string)} items={BUCKETS.map((b) => ({
            key: b.key,
            // 计数写在标签上：四档是"这一档有几枚"的唯一读数，翻完一页数不清等于没分组
            label: `${t(b.labelKey)} (${grouped[b.key].length})`,
          }))} />
        </div>

        <div style={{ marginTop: 10 }}>
          <DataTable<InviteLinkRow> rowKey={(row) => String(row.id)} rows={grouped[tab] || []}
            columns={[
              { key: 'bound_username', title: t('inviteLink.colUser'), render: (row) => row.bound_username || '—' },
              { key: 'bound_email', title: t('inviteLink.colEmail'), render: (row) => row.bound_email || t('inviteLink.emailFree') },
              { key: 'role', title: t('inviteLink.colRole'), width: 120, render: (row) => row.role || '—' },
              { key: 'status', title: t('inviteLink.colStatus'), width: 150, render: (row) => {
                const tone = row.status === 'active' ? 'success' : row.status === 'used' ? 'idle' : 'danger'
                return (
                  <span style={{ display: 'inline-flex', gap: 6, alignItems: 'center' }}>
                    <StatusPill tone={tone}>{t(KNOWN_STATUS.includes(row.status) ? `inviteLink.status.${row.status}` : 'inviteLink.statusOther')}</StatusPill>
                    {/* §6.5 之三：每行风险标识——只有还没花掉的凭证才需要挂"这是一条凭据" */}
                    {row.status === 'active' && <StatusPill tone="warn"><Icon n="alert" style={MI} />{t('inviteLink.riskTag')}</StatusPill>}
                  </span>
                )
              } },
              { key: 'expires_at', title: t('inviteLink.colExpires'), width: 165, render: (row) => fmtTime(row.expires_at) },
              { key: 'used_by', title: t('inviteLink.colUsedBy'), width: 150, render: (row) => row.used_by || '—' },
              { key: 'created_at', title: t('inviteLink.colCreatedAt'), width: 165, render: (row) => fmtTime(row.created_at) },
              { key: 'op', title: t('inviteLink.colActions'), width: 260, render: (row) => (
                <div style={{ display: 'flex', gap: 10, alignItems: 'center', flexWrap: 'wrap' }}>
                  <Link onClick={() => { void copyRow(row) }}>{t('inviteLink.copy')}</Link>
                  {row.status === 'active' && <Link onClick={() => { void showQr(row) }}>{t('inviteLink.qr')}</Link>}
                  {row.status === 'active' && <Link tone="danger" onClick={() => { void revokeLink(row) }}>{t('inviteLink.revoke')}</Link>}
                  {row.status !== 'active' && row.status !== 'used' && (
                    <Link onClick={() => { void regenerate(row) }}>{t('inviteLink.regenerate')}</Link>
                  )}
                </div>
              ) },
            ]} />
          {!(grouped[tab] || []).length && (
            <div style={{ textAlign: 'center', color: 'var(--adm-faint)', padding: 12 }}>{t('inviteLink.empty')}</div>
          )}
          {/* 行内二维码（点开才取图，四档列表不预先打十几次请求） */}
          {Object.entries(qrMap).filter(([id]) => (grouped[tab] || []).some((r) => r.id === Number(id))).map(([id, u]) => (
            <div key={`qr-${id}`} style={{ marginTop: 8 }}>
              <img src={u} width={130} height={130} alt="QR" style={{ borderRadius: 8, border: '1.2px solid var(--lc-border-card)', background: '#fff' }} />
            </div>
          ))}
        </div>
      </Panel>

      {/* 生成弹窗：R9 形态——没有口令这一栏 */}
      <Dialog open={dlg} onCancel={() => setDlg(false)} title={t('inviteLink.create')} onConfirm={() => { void createLink() }}>
        <p style={{ fontSize: 14, color: 'var(--adm-hint)', margin: '0 0 10px' }}>{t('inviteLink.createHint')}</p>
        <Field label={t('inviteLink.colUser')}>
          <input className="lc-input" data-testid="invite-link-username" value={String(form.username || '')} onChange={(e) => setForm((p: Any) => ({ ...p, username: e.target.value }))} />
        </Field>
        <Field label={t('inviteLink.colEmail')}>
          <input className="lc-input" type="email" value={String(form.email || '')} onChange={(e) => setForm((p: Any) => ({ ...p, email: e.target.value }))} />
        </Field>
        <Field label={t('inviteLink.colOrg')}>
          <select className="lc-select" value={String(Number(form.org_id || 0))} onChange={(e) => setForm((p: Any) => ({ ...p, org_id: Number(e.target.value) }))}>
            <option value="0">{t('inviteLink.rootOrgOption')}</option>
            {orgs.map((o) => <option key={o.id} value={String(o.id)}>{`#${o.id} ${o.name}`}</option>)}
          </select>
        </Field>
        <Field label={t('inviteLink.colRole')}>
          <select className="lc-select" value={String(form.role || 'user')} onChange={(e) => setForm((p: Any) => ({ ...p, role: e.target.value }))}>
            {roleOptions.map((r) => <option key={r} value={r}>{t('users.role.' + r)}</option>)}
          </select>
        </Field>
        <Field label={t('inviteLink.colDays')}>
          <input className="lc-input" type="number" min={1} max={90} value={String(form.expires_days || 7)} onChange={(e) => setForm((p: Any) => ({ ...p, expires_days: Number(e.target.value) }))} />
        </Field>
        {/* 弹窗里也放一条：管理员在按下「生成」之前就该知道这东西等同凭证（事后提示会被"已经发出去了"冲掉） */}
        <InviteLinkSecurityNote />
      </Dialog>

      {/* 创建回执：链接 + §6.5 即时警示框 + 复制/发邮件/二维码 */}
      <Dialog open={!!issued} onCancel={() => setIssued(null)} title={t('inviteLink.createdTitle')}
        confirmText={t('inviteLink.close')} onConfirm={() => setIssued(null)}>
        {issued && <InviteLinkCard link={issued} email={issuedEmail} />}
      </Dialog>
    </>
  )
}
