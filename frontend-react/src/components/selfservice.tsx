// ============================================================================
// components/selfservice.tsx — 端用户交易自服务面板
// 路由：/billing 余额  /invites 我的邀请  /packages 我的套餐  /my 我的账号
// 由 FrontShell 按 pathname 渲染，复用已通的计费/邀请/套餐接口。
// ============================================================================

// ============ 本文件职责中文说明 ============
// 端用户交易自服务面板：余额、邀请、套餐与账号信息展示（路由 /billing /invites /packages /my）。
// ========================================

import { useCallback, useEffect, useState } from 'react'
import { fmtPoints } from '@/utils/points' // ★ S1 积分展示
import { useNavigate } from 'react-router-dom'
import { Badge, Button, SkeletonCard, Switch } from '@/ui/langcross/src'
import { toastSuccess, toastError } from '@/lib/toastBus'
import { myPackage } from '@/api/billing'
import { referralMy, referralFunnel, type ReferralMyResp, type ReferralFunnel } from '@/api/referral'
import { scimConfigGet, scimConfigSave } from '@/api/scim'
import { meContext } from '@/api'
import { orgList, type OrgInfo } from '@/api/org'
import { useAuth } from '@/stores/auth'
import { fmtNum } from '@/lib/ui'
import { useT } from '@/i18n'

// useAsync：通用异步数据加载 Hook，自动管理 data/err/loading 状态；组件卸载后不再写入
function useAsync<T>(fn: () => Promise<T>, deps: readonly unknown[]) {
  const [data, setData] = useState<T | null>(null)
  const [err, setErr] = useState('')
  const [loading, setLoading] = useState(true)
  useEffect(() => {
    let alive = true
    setLoading(true)
    fn().then(r => { if (alive) { setData(r as any); setErr('') } })
      .catch(e => { if (alive) setErr(String(e?.message ?? e)) })
      .finally(() => { if (alive) setLoading(false) })
    return () => { alive = false }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps)
  return { data, err, loading }
}

// 余额面板：展示当前用户的永久余额、发放台账与可用总额
export function BalancePanel() {
  const navigate = useNavigate()
  const [, t] = useT()
  // ★ 修复（2026-09-02 前端交互审计）：改用 /api/me/package（登录用户即可读）。
  //   原 /api/billing/balance 后端 handleBalance 需租户管理员（requireTenantAdmin），
  //   普通用户访问「我的余额」（/billing）会 403 显示错误卡片——本页为端用户自服务。
  const { data, err, loading } = useAsync(() => myPackage(), [])
  if (loading) return <div className="ss-loading"><SkeletonCard /></div>
  if (err) return <div className="ssc-card"><span className="ssc-err">{err}</span></div>
  const p = (data as any) ?? {}
  // ★ 双桶口径（/api/me/package 积分出参）：points_permanent_balance=永久余额、
  //   points_grants_left=未过期台账合计、points_balance=可用总额（接口已无 token 裸值）
  const permanent = Number(p.points_permanent_balance ?? 0)
  const grants = Number(p.points_grants_left ?? 0)
  const totalAvailable = Number(p.points_balance ?? 0)
  return (
    <div className="ss-grid">
      <style>{CSS_SSC}</style>
      {totalAvailable <= 0 && (
        <div className="ssc-card">
          <div style={{ padding: '10px 14px', borderRadius: 8, background: 'rgba(210,153,34,0.10)', border: '1.2px solid rgba(210,153,34,0.32)', fontSize: 15, color: 'var(--lc-warn)', lineHeight: 1.7 }}>
            {t('ss.exhaustedHint')}
            <div style={{ marginTop: 6 }}>
              <Button size="sm" variant="primary" onClick={() => { navigate('/packages') }}>{t('ss.gotoRecharge')}</Button>
            </div>
          </div>
        </div>
      )}
      <div className="ssc-card">
        <h3>{t('ss.myBalance')}</h3>
        {/* 余额一律积分口径展示（接口出口即积分，前端无换算逻辑） */}
        <div className="ss-row"><span>{t('ss.permanentBalance')}</span><b>{fmtPoints(permanent)}</b></div>
        <div className="ss-row"><span>{t('ss.grantLedger')}</span><b>{fmtPoints(grants)}</b></div>
        <div className="ss-row"><span>{t('ss.totalAvailable')}</span><b>{fmtPoints(totalAvailable)}</b></div>
      </div>
    </div>
  )
}

// 我的邀请面板：展示个人邀请码、邀请链接、奖励统计与邀请记录
export function ReferralPanel() {
  const [, t] = useT()
  const { data, err, loading } = useAsync<ReferralMyResp>(() => referralMy(), [])
  const { data: ctxData } = useAsync(() => meContext(), []) // ★ H9 个人租户才展示归因看板
  const { data: fd } = useAsync<ReferralFunnel & { pct: number }>(async () => {
    const r = await referralFunnel()
    return r.success ? ({ ...(r.funnel as ReferralFunnel), pct: r.l2_pct ?? 0 } as any) : null as any
  }, [])
  if (loading) return <div className="ss-loading"><SkeletonCard /></div>
  if (err) return <div className="ssc-card"><span className="ssc-err">{err}</span></div>
  const d = data as ReferralMyResp
  const code = d?.ref_code ?? ''
  const url = d?.invite_url ?? ''
  const records = d?.records ?? []
  return (
    <div className="ss-grid">
      <style>{CSS_SSC}</style>
      <div className="ssc-card">
        <h3>{t('ss.myReferral')}</h3>
        <div className="ss-row">
          <span>{t('ss.myCode')}</span>
          <div className="ss-copy">
            <Badge mono>{code}</Badge>
            <Button size="sm" variant="secondary" onClick={() => { navigator.clipboard?.writeText(code) }}>{t('ss.copy')}</Button>
          </div>
        </div>
        {url && <div className="ss-row"><span>{t('ss.inviteLink')}</span><Badge mono>{url}</Badge></div>}
        <div className="ss-stats">
          <div className="ss-stat"><span>{t('ss.trialStacked')}</span><b>{fmtPoints(d?.trial_points ?? 0)}</b></div>
          <div className="ss-stat"><span>{t('ss.paidBonus')}</span><b>{fmtPoints(d?.paid_points ?? 0)}</b></div>
          <div className="ss-stat"><span>{t('ss.invitedCount')}</span><b>{d?.invited ?? 0}</b></div>
        </div>
      </div>
      {/* ★ H9 归因看板：2 级邀请树漏斗（仅个人推广场景展示） */}
      {(ctxData as any)?.is_personal !== false && fd && (
        <div className="ssc-card">
          <h3>{t('ss.funnelTitle')}</h3>
          <div className="ss-stats">
            <div className="ss-stat"><span>{t('ss.funnelL1')}</span><b>{(fd as any).l1_invited ?? 0}</b></div>
            <div className="ss-stat"><span>{t('ss.funnelL1Paid')}</span><b>{(fd as any).l1_paid ?? 0}</b></div>
            <div className="ss-stat"><span>{t('ss.funnelL2')}</span><b>{(fd as any).l2_invited ?? 0}</b></div>
            <div className="ss-stat"><span>{t('ss.funnelL2Share')}</span><b>{fmtPoints((fd as any).reward_points_l2 ?? 0)}{(fd as any).pct ? `（${(fd as any).pct}%）` : ''}</b></div>
          </div>
          <div style={{ fontSize: 14, color: 'var(--lc-text-4)', marginTop: 8 }}>{t('ss.funnelHint')}</div>
        </div>
      )}
      {records.length > 0 && <div className="ssc-card">
        <h3>{t('ss.referralRecords')}</h3>
        <table className="ss-table"><thead><tr><th>{t('ss.refTypeHeader')}</th><th>{t('ss.refPointsHeader')}</th><th>{t('ss.refDateHeader')}</th></tr></thead>
          <tbody>{records.map((r, i) => <tr key={i}><td>{r.type === 'paid_perm' ? t('ss.refTypePaid') : r.type === 'paid_perm_l2' ? t('ss.refTypePaidL2') : t('ss.refTypeTrial')}</td><td>{fmtPoints(r.reward_points)}</td><td>{r.created_at?.slice(0, 10)}</td></tr>)}</tbody></table>
      </div>}
    </div>
  )
}

// 我的套餐面板：展示当前套餐、剩余句数、可用积分与永久余额
export function MyPackagePanel() {
  const navigate = useNavigate()
  const [, t] = useT()
  const { data, err, loading } = useAsync(() => myPackage(), [])
  if (loading) return <div className="ss-loading"><SkeletonCard /></div>
  if (err) return <div className="ssc-card"><span className="ssc-err">{err}</span></div>
  const p = (data as any) ?? {}
  const total = Number(p.points_balance ?? 0)
  const hasPlan = !!(p.package_code && p.package_code !== 'trial')
  return (
    <div className="ss-grid">
      <style>{CSS_SSC}</style>
      {total <= 0 && !hasPlan && (
        <div className="ssc-card">
          <div style={{ padding: '10px 14px', borderRadius: 8, background: 'rgba(210,153,34,0.10)', border: '1.2px solid rgba(210,153,34,0.32)', fontSize: 15, color: 'var(--lc-warn)', lineHeight: 1.7 }}>
            {t('ss.exhaustedHint')}
            <div style={{ marginTop: 6 }}>
              <Button size="sm" variant="primary" onClick={() => { navigate('/billing') }}>{t('ss.gotoTopUp')}</Button>
            </div>
          </div>
        </div>
      )}
      <div className="ssc-card">
        <h3>{t('ss.myPackage')}</h3>
        <div className="ss-row"><span>{t('ss.currentPkg')}</span><Badge mono>{p.package_code ?? '—'}</Badge></div>
        <div className="ss-row"><span>{t('ss.remainingSentences')}</span><b>{t('ss.approxPrefix')}{fmtNum(p.balance_sentences_approx ?? 0)} {t('ss.sentenceUnit')}{t('ss.approxSuffix')}</b></div>
        <div className="ss-row"><span>{t('ss.availablePoints')}</span><b>{fmtPoints(total)}</b></div>
        <div className="ss-row"><span>{t('ss.permanentBalance')}</span><b>{fmtPoints(p.points_permanent_balance ?? 0)}</b></div>
      </div>
    </div>
  )
}

// 我的账号面板：展示用户名/邮箱/角色/租户，并提供余额/邀请/套餐快捷入口
export function AccountPanel() {
  const navigate = useNavigate()
  const [, t] = useT()
  const { user } = useAuth()
  const { data, err, loading } = useAsync(() => meContext(), [])
  const ctx = (data as any) ?? {}
  if (loading) return <div className="ss-loading"><SkeletonCard /></div>
  if (err) return <div className="ssc-card"><span className="ssc-err">{err}</span></div>
  // 是否个人用户租户：企业用户/平台超管不参与「邀请好友 · 多邀多得」，隐藏邀请入口（2026-09）
  const isPersonal = ctx.is_personal !== false
  return (
    <div className="ss-grid">
      <style>{CSS_SSC}</style>
      <div className="ssc-card">
        <h3>{t('ss.myAccount')}</h3>
        <div className="ss-row"><span>{t('ss.username')}</span><b>{ctx.username ?? user?.username ?? '—'}</b></div>
        <div className="ss-row"><span>{t('ss.email')}</span><Badge>{ctx.email ?? user?.email ?? t('ss.emailUnbound')}</Badge></div>
        <div className="ss-row"><span>{t('ss.role')}</span><Badge>{ctx.role ?? user?.role ?? '—'}</Badge></div>
        <div className="ss-row"><span>{t('ss.tenant')}</span><b>{ctx.tenant_name ?? ctx.tenant_id ?? '—'}</b></div>
      </div>
      <div className="ssc-card">
        <h3>{t('ss.quickLinks')}</h3>
        <div className="ss-quick">
          <Button size="sm" variant="secondary" onClick={() => { navigate('/billing') }}>{t('ss.navBalance')}</Button>
          {isPersonal && <Button size="sm" variant="secondary" onClick={() => { navigate('/invites') }}>{t('ss.navReferral')}</Button>}
          <Button size="sm" variant="secondary" onClick={() => { navigate('/packages') }}>{t('ss.navPackage')}</Button>
        </div>
      </div>
      {/* ★ H10 SCIM 2.0 自助配置：仅租户管理员/超管可见 */}
      {['tenant_admin', 'super_admin', 'admin'].includes(String(ctx.role ?? user?.role ?? '')) && <ScimCard />}
    </div>
  )
}

// ScimCard ★ H10：IdP 用户/组织同步开通卡片（开关 + 端点/令牌展示 + 轮换）
// ★ #38（2026-09-21）：补「同步挂载点」选择——SCIM 推送的用户/组织此前只能落到租户根，
//   租户想把同步结果隔离到某个子部门时只能事后再手工搬；后端 root_org_id 早已落库，缺的只是入口。
function ScimCard() {
  const [, t] = useT()
  const [cfg, setCfg] = useState<{ config?: any; endpoint?: string } | null>(null)
  const [busy, setBusy] = useState(false)
  const [orgs, setOrgs] = useState<OrgInfo[]>([])
  const load = useCallback(async () => {
    const r = await scimConfigGet()
    if (r.success) setCfg(r as any)
  }, [])
  useEffect(() => { void load() }, [load])
  // 组织列表取不到不影响主卡片：挂载点下拉退化为「仅根组织」一项
  useEffect(() => {
    void orgList().then((r) => { if (r.success) setOrgs(r.orgs || []) }).catch(() => { /* 静默 */ })
  }, [])
  const save = async (patch: { enabled?: boolean; rotate?: boolean; root_org_id?: number }) => {
    setBusy(true)
    const r = await scimConfigSave(patch)
    setBusy(false)
    if (r.success) { setCfg(r as any); toastSuccess(t('ss.scimSaved')) }
    else toastError(r.message || '')
  }
  if (!cfg) return null
  const enabled = !!cfg.config?.enabled
  const token = String(cfg.config?.token || '')
  const rootOrgId = Number(cfg.config?.root_org_id ?? 0)
  // 超管看到的组织树是平台级（跨租户），必须按本配置所属租户再过滤一遍，否则下拉会列出别家部门
  const cfgTenantId = Number(cfg.config?.tenant_id ?? 0)
  const mountOptions = orgs.filter((o) => o.parent_id > 0 && (!cfgTenantId || o.tenant_id === cfgTenantId))
  // 指向的组织已被删除时补一条 #id 占位项：否则 select 找不到匹配 option 会静默显示第一项，
  // 用户以为挂载点还在，下次保存就把 root_org_id 改写回 0
  const mountMissing = rootOrgId > 0 && !mountOptions.some((o) => o.id === rootOrgId)
  return (
    <div className="ssc-card">
      <h3>{t('ss.scimTitle')}</h3>
      <div className="ss-row"><span>{t('ss.scimStatus')}</span>
        <Switch checked={enabled} disabled={busy} onChange={(e) => void save({ enabled: e.target.checked })} /></div>
      <div className="ss-row"><span>{t('ss.scimEndpoint')}</span><Badge mono>{cfg.endpoint ?? '—'}</Badge></div>
      <div className="ss-row"><span>{t('ss.scimToken')}</span>
        <div className="ss-copy">
          <Badge mono>{token ? `${token.slice(0, 6)}${'•'.repeat(10)}${token.slice(-4)}` : '—'}</Badge>
          {token && <Button size="sm" variant="secondary" onClick={() => { navigator.clipboard?.writeText(token) }}>{t('ss.copy')}</Button>}
          <Button size="sm" variant="secondary" disabled={busy} onClick={() => void save({ rotate: true })}>{t('ss.scimRotate')}</Button>
        </div>
      </div>
      {/* 同步挂载点：0＝租户根组织；其余为本租户已建组织/部门（下拉只列非根项，根另起一条明确文案） */}
      <div className="ss-row"><span>{t('ss.scimRootOrg')}</span>
        <select className="lc-select" value={String(rootOrgId)} disabled={busy}
          onChange={(e) => void save({ root_org_id: Number(e.target.value) })}>
          <option value="0">{t('ss.scimRootOrgTenant')}</option>
          {mountMissing && <option value={String(rootOrgId)}>{`#${rootOrgId}`}</option>}
          {mountOptions.map((o) => <option key={o.id} value={String(o.id)}>{o.name}</option>)}
        </select>
      </div>
      <div style={{ fontSize: 14, color: 'var(--lc-text-4)', marginTop: 8 }}>{t('ss.scimHint')}</div>
    </div>
  )
}

// 页面级样式：ssc- 前缀（防与组件库/其他页面类名重名）
const CSS_SSC = `
.ssc-card{background:var(--lc-panel);border:1.2px solid var(--lc-border-card);border-radius:14px;padding:18px;box-shadow:var(--lc-panel-highlight)}
.ssc-card h3{margin:0 0 6px;font-size:17px}
.ssc-err{color:var(--lc-danger);font-size:15px}
.ssc-card + style{display:none}
`
